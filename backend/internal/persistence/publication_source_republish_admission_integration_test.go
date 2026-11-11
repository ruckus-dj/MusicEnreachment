//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type sourceRepublishAdmissionFixture struct {
	request   PublicationAdmission
	admission SourceRepublishRetainAdmission
	position  uuid.UUID
	track     uuid.UUID
	current   uuid.UUID
	authority PositionSourceAuthority
}

type sourceRepublishFailAfterRiverInsert struct {
	RiverInserter
	operationID uuid.UUID
	jobID       int64
}

func (inserter *sourceRepublishFailAfterRiverInsert) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := inserter.RiverInserter.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return result, err
	}
	if result == nil || result.Job == nil {
		return result, errors.New("real River insertion returned no job")
	}
	inserter.jobID = result.Job.ID
	if typed, ok := args.(publication.TrackArgs); ok {
		inserter.operationID = typed.OperationID
	}
	return result, errors.New("injected failure after real River insertion")
}

func TestSourceRepublishRetainingAdmissionTypedCapabilityPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigratedWithRiver(t)
	initializeRiver(t, ctx, db)
	fixture := newFirstAdmissionFixture(t, ctx, db)
	if _, err := db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value) VALUES ('publication_format','mka') ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`); err != nil {
		t.Fatal(err)
	}
	repository := fixture.repo
	first := seedSourceRepublishAdmissionFixture(t, fixture, "source-republish-typed")
	repo := first.admission.PublicationAdmission
	if _, err := repository.AdmitPublication(ctx, repo, newSourceRepublishRiverClient(t, ctx, db)); !errors.Is(err, ErrPublicationAdmissionConflict) {
		t.Fatalf("generic admission of identical v4 payload = %v, want typed-capability conflict", err)
	}
	assertNoAdmissionArtifacts(t, ctx, db, repo, nil)
	assertCurrentSourceRepublishNamespace(t, ctx, db, first)

	alias := seedSourceRepublishAdmissionFixture(t, fixture, "source-republish-alias")
	alias.admission.Prepare = sourceRepublishAdmissionPlanner(t, fixture.selections[0].InstallationID, new(int), true)
	if _, err := repository.AdmitSourceRepublishRetainingSelection(ctx, alias.admission, newSourceRepublishRiverClient(t, ctx, db)); !errors.Is(err, ErrPublicationAdmissionConflict) {
		t.Fatalf("typed retain admission of a destination aliasing the CURRENT claim = %v, want conflict", err)
	}
	assertNoAdmissionArtifacts(t, ctx, db, alias.request, nil)
	assertCurrentSourceRepublishNamespace(t, ctx, db, alias)

	callbackCalls := 0
	first.admission.Prepare = sourceRepublishAdmissionPlanner(t, fixture.selections[0].InstallationID, &callbackCalls, false)
	client := newSourceRepublishRiverClient(t, ctx, db)
	operationID, err := repository.AdmitSourceRepublishRetainingSelection(ctx, first.admission, client)
	if err != nil {
		t.Fatalf("typed source-republish admission: %v", err)
	}
	if operationID == uuid.Nil || callbackCalls != 1 {
		t.Fatalf("typed admission operation=%s planner calls=%d", operationID, callbackCalls)
	}
	assertSourceRepublishAdmissionCommitted(t, ctx, db, first, operationID)

	rollback := seedSourceRepublishAdmissionFixture(t, fixture, "source-republish-rollback")
	rollback.admission.Prepare = sourceRepublishAdmissionPlanner(t, fixture.selections[0].InstallationID, new(int), false)
	failing := &sourceRepublishFailAfterRiverInsert{RiverInserter: client}
	_, err = repository.AdmitSourceRepublishRetainingSelection(ctx, rollback.admission, failing)
	if err == nil || !strings.Contains(err.Error(), "injected failure after real River insertion") || failing.jobID == 0 || failing.operationID == uuid.Nil {
		t.Fatalf("fail-after-insert admission job=%d operation=%s err=%v", failing.jobID, failing.operationID, err)
	}
	assertNoAdmissionArtifacts(t, ctx, db, rollback.request, &failing.operationID)
	var sourceHolds, toolHolds int
	if err := db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, failing.operationID).Scan(ctx, &sourceHolds); err != nil || sourceHolds != 0 {
		t.Fatalf("rolled-back source holds=%d err=%v", sourceHolds, err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, failing.operationID).Scan(ctx, &toolHolds); err != nil || toolHolds != 0 {
		t.Fatalf("rolled-back tool holds=%d err=%v", toolHolds, err)
	}
}

func newSourceRepublishRiverClient(t *testing.T, ctx context.Context, db *bun.DB) *river.Client[*sql.Tx] {
	t.Helper()
	client, err := river.NewClient(riverdatabasesql.New(db.DB), &river.Config{})
	if err != nil {
		t.Fatalf("create real River client: %v", err)
	}
	return client
}

func seedSourceRepublishAdmissionFixture(t *testing.T, fixture *firstAdmissionFixture, suffix string) sourceRepublishAdmissionFixture {
	t.Helper()
	ctx, db := fixture.ctx, fixture.db
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	generation, candidate := uuid.New(), uuid.New()
	associationID := uuid.New()
	currentPath := path.Join("Release-"+releaseID.String(), "track.mka")
	parent := path.Dir(currentPath)
	size, mtime := int64(40), int64(50)
	identity := PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: strings.Repeat("a", 64)}
	assets := PublicationAssetOutcomes{Version: PublicationAssetOutcomesVersion,
		Cover: PublicationCoverOutcome{State: PublicationAssetAbsent}, Lyrics: PublicationLyricsOutcome{State: PublicationAssetAbsent}}
	tags := map[string][]string{"TITLE": {"Existing title"}}
	metadata := PublicationCommitMetadata{Version: 1, Tags: tags}
	current := TrackPublication{
		PositionID: positionID, Epoch: fixture.epoch, Generation: generation, RelativePath: currentPath,
		Ownership:      mustJSON(PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: identity}),
		ActualAudio:    mustJSON(PublicationCurrentAudio{Version: 1, Facts: fixture.audioFacts}),
		ActualMetadata: mustJSON(metadata), ActualAssets: mustJSON(assets),
		QualityPolicyVersion: publication.QualityPolicyVersion,
	}
	baselineAuthority := PositionSourceAuthority{
		PositionID: positionID, Mode: "manual", Revision: uuid.New(), PublicationEpoch: fixture.epoch,
		PublicationGeneration: generation, SourceLocationID: fixture.locationID, MediaVariantID: fixture.variantID,
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'source republish retain')`, releaseID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'source republish retain')`, trackID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
			return err
		}
		if err := seedRecordingCreditBaselineTx(ctx, tx, positionID, trackID, "source republish retain", "Source republish artist"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,true,true,'manual','{}'::jsonb)`, associationID, trackID, fixture.variantID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at)
			VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,?,now())`,
			current.PositionID, current.Epoch, current.Generation, current.RelativePath, string(current.Ownership), string(current.ActualAudio), string(current.ActualMetadata), string(current.ActualAssets), current.QualityPolicyVersion); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO position_source_authority(position_id,mode,revision,publication_epoch,publication_generation,source_location_id,media_variant_id) VALUES (?,?,?,?,?,?,?)`,
			baselineAuthority.PositionID, baselineAuthority.Mode, baselineAuthority.Revision, baselineAuthority.PublicationEpoch,
			baselineAuthority.PublicationGeneration, baselineAuthority.SourceLocationID, baselineAuthority.MediaVariantID); err != nil {
			return err
		}
		for _, node := range []PublicationNamespaceNode{
			{CanonicalKey: parent, RelativePath: parent, NodeKind: "dir", DirectoryScope: releaseID.String()},
			{CanonicalKey: currentPath, RelativePath: currentPath, NodeKind: "file"},
		} {
			digest := publicationNamespaceKeyDigest(node.CanonicalKey)
			claimID := uuid.New()
			var owner, scope any = positionID, nil
			if node.NodeKind == "dir" {
				owner, scope = nil, node.DirectoryScope
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key) VALUES (?,?,?,?,?,?,?,?)`,
				claimID, fixture.epoch, digest[:], node.CanonicalKey, node.RelativePath, node.NodeKind, owner, scope); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, positionID, generation); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed source-republish retained-current fixture: %v", err)
	}

	contextSnapshot, err := repositoryReadFirstPublicationContext(ctx, fixture.repo, releaseID, positionID)
	if err != nil {
		t.Fatalf("read persisted source selection context: %v", err)
	}
	if len(contextSnapshot.Sources) != 1 || !contextSnapshot.Sources[0].Selected || contextSnapshot.Sources[0].AssociationID != associationID {
		t.Fatalf("persisted source selection does not match fixture association: %+v", contextSnapshot.Sources)
	}
	var persistedCurrent TrackPublication
	if err := db.NewSelect().Model(&persistedCurrent).Where("position_id=?", positionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var persistedAuthority PositionSourceAuthority
	if err := db.NewSelect().Model(&persistedAuthority).Where("position_id=?", positionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var metadataRevision int64
	if err := db.NewRaw(`SELECT metadata_revision FROM release_track WHERE id=?`, positionID).Scan(ctx, &metadataRevision); err != nil {
		t.Fatal(err)
	}
	selected := contextSnapshot.Sources[0]
	input := PublicationInputs{
		Version: PublicationSourceRepublishInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "audio", Audio: &selected.Input, Format: publication.AudioFormatMKA,
		DesiredTags: tags, Tools: append([]SourceAnalysisToolSelection(nil), fixture.selections...),
		SourceRepublish: &PublicationSourceRepublishInput{
			Current: PublicationCurrentWitness{
				Generation: persistedCurrent.Generation, RelativePath: persistedCurrent.RelativePath, Ownership: identity,
				AudioFacts: fixture.audioFacts, Assets: assets, Tags: tags, ArtworkInventory: []PublicationArtworkWitness{},
			},
			Mode: "ordinary",
			SourceAuthority: PublicationSourceAuthority{
				Present: true, PositionID: persistedAuthority.PositionID, Mode: persistedAuthority.Mode,
				Revision: persistedAuthority.Revision, PublicationEpoch: persistedAuthority.PublicationEpoch,
				PublicationGeneration: persistedAuthority.PublicationGeneration,
				SourceLocationID:      persistedAuthority.SourceLocationID, MediaVariantID: persistedAuthority.MediaVariantID,
			},
			Association:     PublicationRepublishAssociation{ID: selected.AssociationID, LocationID: selected.Selection.LocationID, MediaVariantID: selected.Selection.MediaVariantID},
			Lyrics:          PublicationRepublishLyrics{State: "absent", Origin: "none"},
			LyricsAuthority: PublicationInitialLyricsInput{ExpectedMetadataRevision: metadataRevision},
			Cover:           PublicationRepublishCover{ReleaseID: releaseID, AuthorityRevision: 1, AuthorityMode: "automatic", Derivation: "absent"},
		},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate persisted source-republish request: %v", err)
	}
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	source := selected.Selection
	source.HoldUUID = uuid.New()
	finalProvisional := path.Join("Provisional", suffix, "track.mka")
	request := PublicationAdmission{
		PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: fixture.epoch,
		ExpectedGeneration: &generation, CandidateGeneration: candidate, IntentKind: "audio", ToolsReadRequired: true,
		Tools: append([]SourceAnalysisToolSelection(nil), fixture.selections...), Source: &source,
		FrozenInputs: frozen,
		NamespaceManifest: []PublicationNamespaceNode{
			{CanonicalKey: "Provisional", RelativePath: "Provisional", NodeKind: "dir", DirectoryScope: "provisional"},
			{CanonicalKey: path.Dir(finalProvisional), RelativePath: path.Dir(finalProvisional), NodeKind: "dir", DirectoryScope: suffix},
			{CanonicalKey: finalProvisional, RelativePath: finalProvisional, NodeKind: "file"},
		},
	}
	return sourceRepublishAdmissionFixture{
		request: request, admission: SourceRepublishRetainAdmission{PublicationAdmission: request},
		position: positionID, track: trackID, current: generation, authority: persistedAuthority,
	}
}

func repositoryReadFirstPublicationContext(ctx context.Context, repository *SetupManagerRepository, releaseID, positionID uuid.UUID) (*LibraryFirstPublicationContext, error) {
	return repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
}

func sourceRepublishAdmissionPlanner(t *testing.T, installationID uuid.UUID, calls *int, aliasCurrentClaim bool) PrepareFirstPublicationAdmission {
	t.Helper()
	return func(_ context.Context, snapshot FirstPublicationAdmissionSnapshot, request PublicationAdmission) ([]PublicationNamespaceNode, error) {
		*calls++
		if snapshot.Context == nil || snapshot.Context.Library == nil || snapshot.Context.Library.Current == nil ||
			request.ExpectedGeneration == nil || snapshot.Context.Library.Current.Record.Generation != *request.ExpectedGeneration || len(snapshot.Context.Sources) != 1 {
			return nil, errors.New("planner snapshot did not include the persisted CURRENT and selected source")
		}
		if snapshot.Settings["publication_format"] != "mka" || request.FrozenInputs == nil || request.Source == nil {
			return nil, errors.New("planner snapshot settings or source input did not match frozen request")
		}
		selected := snapshot.Context.Sources[0]
		if !selected.Selected || selected.Selection.LocationID != request.Source.LocationID ||
			selected.Selection.MediaVariantID != request.Source.MediaVariantID || selected.Selection.WorkID != request.Source.WorkID ||
			selected.Input.LocationID != request.Source.LocationID {
			return nil, errors.New("planner snapshot selected source did not match the admission source")
		}
		foundInstallation := false
		for _, installation := range snapshot.Installations {
			if installation.ID == installationID && installation.State == "ready" {
				foundInstallation = true
			}
		}
		if !foundInstallation || len(request.Tools) != 2 || request.Tools[0].InstallationID != installationID {
			return nil, errors.New("planner snapshot did not contain the frozen ready ffmpeg installation")
		}
		frozen, err := DecodePublicationInputs(request.FrozenInputs)
		currentPath := snapshot.Context.Library.Current.Record.RelativePath
		if err != nil || frozen.Format != publication.AudioFormatMKA || frozen.Audio == nil || frozen.Audio.LocationID != selected.Selection.LocationID ||
			frozen.SourceRepublish == nil || frozen.SourceRepublish.Current.RelativePath != currentPath {
			return nil, errors.New("planner frozen audio did not match persisted selected source")
		}
		currentClaimFound := false
		for _, claim := range snapshot.Claims {
			if claim.RelativePath == currentPath && claim.NodeKind == "file" && claim.OwnerPositionID != nil && *claim.OwnerPositionID == request.PositionID {
				currentClaimFound = true
			}
		}
		if !currentClaimFound {
			return nil, errors.New("planner snapshot omitted the claimed CURRENT path")
		}
		directory := path.Dir(currentPath)
		finalPath, canonicalKey := currentPath, currentPath
		if aliasCurrentClaim {
			// A differently rendered path that still probes to the CURRENT
			// canonical identity is a conflicting destination claim.
			finalPath = path.Join(directory, "Track.mka")
		}
		return []PublicationNamespaceNode{
			{CanonicalKey: directory, RelativePath: directory, NodeKind: "dir", DirectoryScope: request.ExpectedReleaseID.String()},
			{CanonicalKey: canonicalKey, RelativePath: finalPath, NodeKind: "file"},
		}, nil
	}
}

func assertCurrentSourceRepublishNamespace(t *testing.T, ctx context.Context, db *bun.DB, fixture sourceRepublishAdmissionFixture) {
	t.Helper()
	var fileClaims, generationReservations int
	if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_claim WHERE epoch=? AND owner_position_id=? AND relative_path=(SELECT relative_path FROM track_publication WHERE position_id=?) AND node_kind='file'`, fixture.request.ExpectedEpoch, fixture.position, fixture.position).Scan(ctx, &fileClaims); err != nil || fileClaims != 1 {
		t.Fatalf("CURRENT namespace file claims=%d err=%v", fileClaims, err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation WHERE position_id=? AND generation=?`, fixture.position, fixture.current).Scan(ctx, &generationReservations); err != nil || generationReservations != 2 {
		t.Fatalf("CURRENT namespace generation reservations=%d err=%v", generationReservations, err)
	}
}

func assertSourceRepublishAdmissionCommitted(t *testing.T, ctx context.Context, db *bun.DB, fixture sourceRepublishAdmissionFixture, operationID uuid.UUID) {
	t.Helper()
	var jobID int64
	var operationState, operationStage string
	if err := db.NewRaw(`SELECT river_job_id,state,stage FROM operation WHERE id=?`, operationID).Scan(ctx, &jobID, &operationState, &operationStage); err != nil {
		t.Fatalf("read committed source-republish operation: %v", err)
	}
	if jobID == 0 || operationState != "queued" || operationStage != "queued" {
		t.Fatalf("source-republish operation state=%s/%s job=%d", operationState, operationStage, jobID)
	}
	var riverJobCount int
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE id=? AND args->>'operation_id'=?`, jobID, operationID.String()).Scan(ctx, &riverJobCount); err != nil || riverJobCount != 1 {
		t.Fatalf("real River job count=%d err=%v", riverJobCount, err)
	}
	var attempt TrackPublicationAttempt
	if err := db.NewSelect().Model(&attempt).Where("operation_id=?", operationID).Scan(ctx); err != nil {
		t.Fatalf("read admitted source-republish attempt: %v", err)
	}
	if attempt.ExpectedGeneration == nil || *attempt.ExpectedGeneration != fixture.current || attempt.CandidateGeneration != fixture.request.CandidateGeneration {
		t.Fatalf("source-republish attempt does not retain CURRENT and candidate: %+v", attempt)
	}
	var sourceHolds, toolHolds, reservations int
	for query, destination := range map[string]*int{
		`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`:                           &sourceHolds,
		`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`:                          &toolHolds,
		`SELECT count(*) FROM publication_namespace_reservation WHERE position_id=? AND generation=?`: &reservations,
	} {
		var args []any
		if strings.Contains(query, "position_id") {
			args = []any{fixture.position, fixture.request.CandidateGeneration}
		} else {
			args = []any{operationID}
		}
		if err := db.NewRaw(query, args...).Scan(ctx, destination); err != nil {
			t.Fatal(err)
		}
	}
	if sourceHolds != 1 || toolHolds != 1 || reservations != 2 {
		t.Fatalf("admitted source/tool holds/reservations=%d/%d/%d", sourceHolds, toolHolds, reservations)
	}
	var associationSelected bool
	if err := db.NewRaw(`SELECT selected FROM track_source WHERE track_id=? AND media_variant_id=(SELECT media_variant_id FROM position_source_authority WHERE position_id=?)`, fixture.track, fixture.position).Scan(ctx, &associationSelected); err != nil || !associationSelected {
		t.Fatalf("retained source association selected=%v err=%v", associationSelected, err)
	}
	var authority PositionSourceAuthority
	if err := db.NewSelect().Model(&authority).Where("position_id=?", fixture.position).Scan(ctx); err != nil || authority != fixture.authority {
		t.Fatalf("retained source authority changed: %+v err=%v", authority, err)
	}
}

var _ RiverInserter = (*sourceRepublishFailAfterRiverInsert)(nil)
