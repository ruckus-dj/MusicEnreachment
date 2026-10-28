//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// Exercise typed provider admission with a real CURRENT and transactional River
// insertion, as well as invalidation of the frozen provider authority.
func TestPublicationProviderTagAuthorityLifecyclePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	const identity = "provider-tag-lifecycle"
	const endpoint = "https://musicbrainz.example/ws/2"
	if _, err := f.db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES('musicbrainz_config_identity', ?) ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	provider := Provider{ID: uuid.New(), Code: "musicbrainz"}
	cache := NewProviderCacheRepository(f.db)
	if err := cache.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "provider-tag-lifecycle", Endpoint: endpoint, ConfigurationIdentity: identity}
	if err := cache.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	graph := publicationProviderTagAuthorityGraph()
	ticket, err := cache.BeginRefresh(ctx, source.ID, "release-search:0:1:lifecycle", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[{"id":"` + releaseKey + `"}]}`), Graph: graph,
	}); err != nil {
		t.Fatal(err)
	}
	var localTrackID, releaseID uuid.UUID
	if err := f.db.NewRaw(`SELECT track_id,release_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &localTrackID, &releaseID); err != nil {
		t.Fatal(err)
	}
	var providerReleaseID uuid.UUID
	if err := f.db.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, provider.ID, releaseKey).Scan(ctx, &providerReleaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES(?,?,?,'manual')`, provider.ID, providerReleaseID, releaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method) VALUES(?,?,?,'manual')`, provider.ID, graph.Tracks[0].ID, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	load := func() (*PublicationProviderTagContext, PublicationInputs) {
		t.Helper()
		var accepted *PublicationProviderTagContext
		if err := f.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
			return readPublicationProviderTagContextForPositionTx(ctx, tx, releaseID, f.guard.ExpectedPositionID, endpoint, identity, false, &accepted)
		}); err != nil {
			t.Fatal(err)
		}
		input := publicationProviderTagFixture(t)
		input.ProviderTags.ProviderSourceID = accepted.Source.ID
		input.ProviderTags.Endpoint = accepted.Source.Endpoint
		input.ProviderTags.ConfigurationIdentity = accepted.Source.ConfigurationIdentity
		input.ProviderTags.AuthorityRevision = accepted.Digest
		input.ProviderTags.Patch, err = BuildPublicationProviderCanonicalTagPatch(*accepted)
		if err != nil {
			t.Fatal(err)
		}
		protection, err := BuildPublicationProviderTagProtection(accepted)
		if err != nil {
			t.Fatal(err)
		}
		input.ProviderTags.ProtectedKeys = protection.Keys
		input.DesiredTags, err = publication.ApplyProviderTagPatch(input.ProviderTags.Current.Tags, input.ProviderTags.Patch, protection.Keys)
		if err != nil {
			t.Fatal(err)
		}
		return accepted, input
	}
	accepted, input := load()
	validate := func(candidate PublicationInputs) error {
		t.Helper()
		return f.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
			return validatePublicationProviderTagAuthorityTx(ctx, tx, releaseID, f.guard.ExpectedPositionID, candidate)
		})
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate frozen provider-tag contract: %v", err)
	}
	if err := validate(input); err != nil {
		t.Fatalf("live accepted provider authority rejected: %v", err)
	}

	// First establish a genuine CURRENT publication on this same position. The
	// typed admission below must consume its live provider authority, not merely
	// validate a fabricated frozen input contract.
	repo := NewSetupManagerRepository(f.db)
	committed, err := repo.CommitPublicationResult(ctx, f.guard, publicationResultForFixture(f))
	if err != nil || !committed {
		t.Fatalf("establish CURRENT publication: committed=%v err=%v", committed, err)
	}
	projection, err := repo.ReadLibraryPublicationContext(ctx, releaseID, f.guard.ExpectedPositionID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Current == nil || projection.ActiveAttempt != nil {
		t.Fatalf("expected CURRENT and no active attempt: %#v", projection)
	}
	manifest := []PublicationNamespaceNode{
		{CanonicalKey: "album", RelativePath: "album", NodeKind: "dir", DirectoryScope: "album"},
		{CanonicalKey: "album/track.flac", RelativePath: "album/track.flac", NodeKind: "file"},
	}
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return reservePublicationNamespace(ctx, tx, PublicationAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, CandidateGeneration: projection.Current.Record.Generation, NamespaceManifest: manifest})
	}); err != nil {
		t.Fatal(err)
	}
	projection, err = repo.ReadLibraryPublicationContext(ctx, releaseID, f.guard.ExpectedPositionID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Current == nil || projection.ActiveAttempt != nil || len(projection.NamespaceManifest) != 2 {
		t.Fatalf("CURRENT admission context invalid after namespace claims: %#v", projection)
	}

	// Create a real verified installation and build V3 inputs from the persisted
	// CURRENT projection, including the accepted provider authority snapshot.
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	installationID := uuid.New()
	verifiedAt := time.Now().UTC()
	installation := &ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "release-1", RelativePath: filepath.Join("ffmpeg", "release-1"), State: "ready", ExecutableVerificationOutput: versions, Version: "ffmpeg 7.0 + ffprobe 7.0", ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verifiedAt}
	if err := repo.CreateInstallation(ctx, installation); err != nil {
		t.Fatal(err)
	}
	selections := []SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: filepath.ToSlash(installation.RelativePath), Executable: "ffmpeg", Version: installation.Version, VersionBanner: ffmpegBanner},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: filepath.ToSlash(installation.RelativePath), Executable: "ffprobe", Version: installation.Version, VersionBanner: ffprobeBanner},
	}
	current := projection.Current
	if _, err := f.db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value) VALUES ('publication_format','source') ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`); err != nil {
		t.Fatal(err)
	}
	providerGuard, _ := startPublicationReconciliationForResult(t, ctx, repo, f.guard.Epoch, f.guard.ExpectedPositionID)
	providerDecision, err := repo.EvaluatePublicationReconciliationCurrent(ctx, releaseID, providerGuard, endpoint, identity)
	if err != nil || providerDecision.Action != ProviderTagsRequired {
		t.Fatalf("independent provider update was blocked by unknown audio authority: decision=%+v err=%v", providerDecision, err)
	}
	authorityRevision, authorityLocation, authorityVariant := uuid.New(), uuid.New(), uuid.New()
	if _, err := f.db.ExecContext(ctx, `INSERT INTO position_source_authority(position_id,mode,revision,publication_epoch,publication_generation,source_location_id,media_variant_id) VALUES (?,'manual',?,?,?,?,?)`, f.guard.ExpectedPositionID, authorityRevision, current.Record.Epoch, current.Record.Generation, authorityLocation, authorityVariant); err != nil {
		t.Fatal(err)
	}
	input = publicationProviderTagFixture(t)
	input.Tools = selections
	input.Rewrite = &PublicationRewriteInput{CurrentGeneration: current.Record.Generation, RelativePath: current.Record.RelativePath, Ownership: current.Ownership.Identity, AudioFacts: current.Audio.Facts}
	input.ProviderTags.Current = PublicationCurrentWitness{
		Generation: current.Record.Generation, RelativePath: current.Record.RelativePath,
		Ownership: current.Ownership.Identity, AudioFacts: current.Audio.Facts, Assets: current.Assets,
		Tags: current.Metadata.Tags, ArtworkInventory: []PublicationArtworkWitness{},
	}
	input.ProviderTags.AudioPayloadSHA256 = current.Ownership.Identity.SHA256
	input.ProviderTags.ProviderSourceID = accepted.Source.ID
	input.ProviderTags.Endpoint, input.ProviderTags.ConfigurationIdentity = accepted.Source.Endpoint, accepted.Source.ConfigurationIdentity
	input.ProviderTags.AuthorityRevision = accepted.Digest
	input.ProviderTags.Patch, err = BuildPublicationProviderCanonicalTagPatch(*accepted)
	if err != nil {
		t.Fatal(err)
	}
	protection, err := BuildPublicationProviderTagProtection(accepted)
	if err != nil {
		t.Fatal(err)
	}
	input.ProviderTags.ProtectedKeys = protection.Keys
	input.DesiredTags, err = publication.ApplyProviderTagPatch(input.ProviderTags.Current.Tags, input.ProviderTags.Patch, protection.Keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate typed V3 inputs: %v", err)
	}
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := PublicationAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedReleaseID: releaseID, ExpectedEpoch: projection.ActiveEpoch, ExpectedGeneration: uuidPointer(current.Record.Generation), CandidateGeneration: uuid.New(), IntentKind: "rewrite", ToolsReadRequired: true, Tools: selections, FrozenInputs: frozen, NamespaceManifest: manifest}
	failedInserter := &publicationAdmissionInserter{failAfterInsert: true}
	if _, err := repo.AdmitPublicationProviderTagsFromReconciliation(ctx, request, providerGuard, failedInserter); err == nil {
		t.Fatal("expected real River insertion rollback")
	}
	if failedInserter.id <= 0 {
		t.Fatal("failure was not injected after River row insertion")
	}
	rollbackOperationID := failedInserter.args.(publication.TrackArgs).OperationID
	for _, check := range []struct {
		query string
		args  []any
	}{
		{`SELECT count(*) FROM operation WHERE id=?`, []any{rollbackOperationID}},
		{`SELECT count(*) FROM track_publication_attempt WHERE position_id=?`, []any{request.PositionID}},
		{`SELECT count(*) FROM publication_namespace_reservation WHERE position_id=? AND generation=?`, []any{request.PositionID, request.CandidateGeneration}},
		{`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, []any{rollbackOperationID}},
		{`SELECT count(*) FROM river_job WHERE id=?`, []any{failedInserter.id}},
	} {
		var count int
		if err := f.db.NewRaw(check.query, check.args...).Scan(ctx, &count); err != nil || count != 0 {
			t.Fatalf("failed admission retained artifacts: count=%d err=%v query=%s", count, err, check.query)
		}
	}
	assertCurrentUnchanged := func() {
		t.Helper()
		var persisted TrackPublication
		if err := f.db.NewSelect().Model(&persisted).Where("position_id=?", request.PositionID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if persisted.Generation != current.Record.Generation || !sameJSONValue(persisted.ActualMetadata, current.Record.ActualMetadata) || !sameJSONValue(persisted.ActualAssets, current.Record.ActualAssets) {
			t.Fatal("admission changed the CURRENT projection before result commit")
		}
	}
	assertCurrentUnchanged()
	successfulInserter := new(publicationAdmissionInserter)
	operationID, err := repo.AdmitPublicationProviderTagsFromReconciliation(ctx, request, providerGuard, successfulInserter)
	if err != nil {
		t.Fatalf("typed provider-tag admission: %v", err)
	}
	if operationID == uuid.Nil || successfulInserter.id <= 0 || successfulInserter.args.(publication.TrackArgs).OperationID != operationID {
		t.Fatal("typed provider-tag admission did not persist a linked River job")
	}
	assertCurrentUnchanged()
	var successorGeneration int64
	var retainedScopes int
	if err := f.db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, request.ExpectedEpoch, request.PositionID).Scan(ctx, &successorGeneration); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, request.ExpectedEpoch, request.PositionID).Scan(ctx, &retainedScopes); err != nil {
		t.Fatal(err)
	}
	if successorGeneration <= providerGuard.ObservedGeneration || retainedScopes != 1 {
		t.Fatalf("provider admission erased unevaluated audio work: generation=%d scopes=%d", successorGeneration, retainedScopes)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, operationID)
	if err != nil || attempt == nil || !sameJSONValue(attempt.FrozenInputs, frozen) {
		t.Fatalf("read admitted frozen provider contract: attempt=%+v err=%v", attempt, err)
	}
	var held bool
	if err := f.db.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_tool_read_hold WHERE operation_id=? AND installation_id=?)`, operationID, installationID).Scan(ctx, &held); err != nil || !held {
		t.Fatalf("provider admission did not pin its verified tools: held=%v err=%v", held, err)
	}
	operation, err := repo.GetOperation(ctx, operationID)
	if err != nil || operation == nil || operation.RiverJobID == nil || *operation.RiverJobID != successfulInserter.id {
		t.Fatalf("read admitted provider operation: operation=%+v err=%v", operation, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, successfulInserter.id); err != nil {
		t.Fatal(err)
	}
	guard := PublicationDeliveryGuard{
		OperationID: operationID, ApplicationAttempt: operation.Attempt, RiverJobID: successfulInserter.id, RiverAttempt: 1,
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(),
		Epoch: request.ExpectedEpoch, CandidateGeneration: request.CandidateGeneration, ExpectedPositionID: request.PositionID,
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim provider delivery: outcome=%s err=%v", outcome, err)
	}
	if prepared, live, err := repo.PublicationPreparationInputs(ctx, guard); err != nil || !live || prepared.ProviderTags == nil || !sameJSONValue(mustJSON(prepared), frozen) {
		t.Fatalf("read claimed provider preparation inputs: live=%v err=%v", live, err)
	}
	if fresh, err := repo.CheckPublicationResultCurrent(ctx, guard); err != nil || !fresh {
		t.Fatalf("provider result freshness: fresh=%v err=%v", fresh, err)
	}
	size, mtime := int64(12), int64(30)
	payloadHash := strings.Repeat("c", 64)
	result := PublicationResult{
		Observation: PublicationStageObservation{
			Version: 1, RelativePath: current.Record.RelativePath, PayloadSHA256: payloadHash,
			Ownership:   ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime},
			ActualAudio: current.Record.ActualAudio, ActualAssets: current.Record.ActualAssets,
			ActualMetadata:       mustJSON(PublicationCommitMetadata{Version: 1, Tags: input.DesiredTags}),
			QualityPolicyVersion: publication.QualityPolicyVersion,
		},
		InstalledOwnership: PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion,
			Identity: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: payloadHash}},
		PreservedAudioSHA256: input.ProviderTags.AudioPayloadSHA256,
	}
	if committed, err := repo.CommitPublicationResult(ctx, guard, result); err != nil || !committed {
		t.Fatalf("commit provider result: committed=%v err=%v", committed, err)
	}
	var installed TrackPublication
	if err := f.db.NewSelect().Model(&installed).Where("position_id=?", request.PositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if installed.Generation != request.CandidateGeneration || !sameJSONValue(installed.ActualMetadata, result.Observation.ActualMetadata) || !sameJSONValue(installed.ActualAssets, current.Record.ActualAssets) {
		t.Fatalf("provider result did not preserve CURRENT assets and apply admitted tags: %+v", installed)
	}
	var remaining int
	if err := f.db.NewRaw(`SELECT (SELECT count(*) FROM track_publication_attempt WHERE operation_id=?)+(SELECT count(*) FROM publication_execution_claim WHERE operation_id=?)+(SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?)`, operationID, operationID, operationID).Scan(ctx, &remaining); err != nil || remaining != 0 {
		t.Fatalf("provider result retained consumed authority: count=%d err=%v", remaining, err)
	}
	if committed, err := repo.CommitPublicationResult(ctx, guard, result); err != nil || committed {
		t.Fatalf("completed provider result replay: committed=%v err=%v", committed, err)
	}
	authority, err := repo.ReadPositionSourceAuthority(ctx, request.PositionID)
	if err != nil || authority == nil || authority.Mode != "manual" || authority.Revision != authorityRevision || authority.PublicationGeneration != installed.Generation || authority.SourceLocationID != authorityLocation || authority.MediaVariantID != authorityVariant {
		t.Fatalf("provider rewrite changed source authority instead of carrying it: authority=%+v err=%v", authority, err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value) VALUES ('publication_format','source') ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`); err != nil {
		t.Fatal(err)
	}
	noopGuard, _ := startPublicationReconciliationForResult(t, ctx, repo, request.ExpectedEpoch, request.PositionID)
	decision, err := repo.EvaluatePublicationReconciliationCurrent(ctx, releaseID, noopGuard, endpoint, identity)
	if err != nil || decision.Action != AllNoop || decision.Noop == nil {
		t.Fatalf("evaluate unchanged bound provider and manual source: decision=%+v err=%v", decision, err)
	}
	if acknowledged, err := repo.AcknowledgePublicationReconciliationNoop(ctx, noopGuard, decision.Noop); err != nil || !acknowledged {
		t.Fatalf("acknowledge unchanged bound provider: acknowledged=%v err=%v", acknowledged, err)
	}

	// Changing the real local owner after freezing the contract must invalidate
	// its authority revision; a newly derived contract must then pass.
	if _, err := f.db.NewRaw(`UPDATE local_track SET metadata=?::jsonb WHERE id=?`, `{"manual_matching_override":{"title":"keep"}}`, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := validate(input); err == nil || !strings.Contains(err.Error(), ErrPublicationAdmissionConflict.Error()) {
		t.Fatalf("stale provider authority error = %v", err)
	}
	refreshed, freshInput := load()
	if refreshed.Digest == accepted.Digest {
		t.Fatal("local owner mutation did not advance provider authority revision")
	}
	if err := validate(freshInput); err != nil {
		t.Fatalf("rebuilt provider authority rejected: %v", err)
	}
}
