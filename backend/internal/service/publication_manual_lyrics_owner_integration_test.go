//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	riverdatabasesql "github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// Admission is seeded here; preparation and outcome records must be produced by
// the actual native service and PostgreSQL repository, never by this fixture.
func TestPreparedManualLyricsFLACOwnerPostgreSQL(t *testing.T) {
	for _, test := range []struct {
		name    string
		cover   bool
		removal bool
	}{
		{name: "no_cover_value"},
		{name: "cover_value", cover: true},
		{name: "cover_explicit_empty", cover: true, removal: true},
	} {
		t.Run(test.name, func(t *testing.T) { testPreparedManualLyricsFLACOwner(t, test.cover, test.removal, false, false) })
	}
}

func TestAdmittedPreparedManualLyricsFLACPostgreSQL(t *testing.T) {
	for _, test := range []struct {
		name    string
		cover   bool
		removal bool
	}{
		{name: "no_cover_value"},
		{name: "cover_value", cover: true},
		{name: "cover_explicit_empty", cover: true, removal: true},
	} {
		t.Run(test.name, func(t *testing.T) { testPreparedManualLyricsFLACOwner(t, test.cover, test.removal, true, true) })
	}
}

func TestAdmittedPreparedManualLyricsFLACNestedPathPostgreSQL(t *testing.T) {
	testPreparedManualLyricsFLACOwner(t, true, false, true, true, true)
}

func testPreparedManualLyricsFLACOwner(t *testing.T, withCover, removal, continuous bool, options ...bool) {
	ctx := context.Background()
	fixture, fakeStage, _ := preparedManualLyricsFLACStage(t)
	t.Cleanup(func() { _ = fakeStage.Close() })
	currentPath := filepath.Join(fixture.output, "current.flac")
	throughAdmission := len(options) > 0 && options[0]
	nestedPath := len(options) > 1 && options[1]
	if nestedPath {
		currentPath = filepath.Join(fixture.output, "Artist", "Album", "Current.FLAC")
		if err := os.MkdirAll(filepath.Dir(currentPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(fixture.output, "current.flac"), currentPath); err != nil {
			t.Fatal(err)
		}
	}
	if removal {
		if _, err := (metadata.Writer{}).Write(ctx, currentPath, map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {"[00:00.00]old current lyrics\n"}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if withCover {
		current, err := os.ReadFile(currentPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(currentPath, appendNativeTestPicture(t, current), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	original, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.repo.input.Clone()
	if nestedPath {
		input.Rewrite.RelativePath = "Artist/Album/Current.FLAC"
	}
	input.DesiredTags = map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}
	installationID := uuid.New()
	input.Tools = []persistence.SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffmpeg", Executable: "ffmpeg", Version: "1", VersionBanner: "ffmpeg version 1"},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffprobe", Executable: "ffprobe", Version: "1", VersionBanner: "ffprobe version 1"},
	}
	input.ManualLyrics.State = "value"
	input.ManualLyrics.Value = "[00:01.00]native PostgreSQL lyrics\r\n"
	if removal {
		input.ManualLyrics.State = "explicit_empty"
		input.ManualLyrics.Value = ""
	}
	input.ManualLyrics.ExpectedMetadataRevision = 7
	info, err := os.Stat(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256(original)
	input.Rewrite.Ownership = persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: fmt.Sprintf("%x", digest)}
	fixture.repo.input = input.Clone()
	capture, err := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	baselineArtwork, err := metadata.SnapshotFlacArtwork(ctx, currentPath)
	if err != nil {
		t.Fatal(err)
	}
	actualAssets, err := ObservePublicationAssets(capture)
	if err != nil {
		t.Fatal(err)
	}
	if withCover && (baselineArtwork.ApprovedCoverFacts().Count != 1 || actualAssets.Cover.State != persistence.PublicationAssetEmbedded) {
		t.Fatal("covered fixture has no verified native front cover")
	}
	if removal && (len(capture.Tags["LYRICS"]) != 1 || actualAssets.Lyrics.State != persistence.PublicationAssetEmbedded) {
		t.Fatal("removal fixture must contain actual current lyrics")
	}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	db := testpostgres.OpenMigrated(t)
	migrator, err := rivermigrate.New(riverdatabasesql.New(db.DB), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	encode := func(value any) string {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	guard := persistence.PublicationDeliveryGuard{
		OperationID: uuid.New(), ExpectedPositionID: uuid.New(), CandidateGeneration: uuid.New(),
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(),
	}
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	guard.Epoch = epoch
	releaseID, trackID := uuid.New(), uuid.New()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO local_release(id,title) VALUES (?,'native lyrics')`, releaseID)
	exec(`INSERT INTO local_track(id,title) VALUES (?,'native lyrics')`, trackID)
	exec(`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, guard.ExpectedPositionID, releaseID, trackID)
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ('publish_track_v1','{}','running',1) RETURNING id`).Scan(&guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	guard.RiverAttempt, guard.ApplicationAttempt = 1, 1
	snapshot := encode(map[string]any{"version": 1, "position_id": guard.ExpectedPositionID, "output_epoch": epoch, "intent_kind": "rewrite"})
	nodes := []persistence.PublicationNamespaceNode{{CanonicalKey: input.Rewrite.RelativePath, RelativePath: input.Rewrite.RelativePath, NodeKind: "file"}}
	if nestedPath {
		nodes = []persistence.PublicationNamespaceNode{
			{CanonicalKey: "artist", RelativePath: "Artist", NodeKind: "dir", DirectoryScope: "artist"},
			{CanonicalKey: "artist/album", RelativePath: "Artist/Album", NodeKind: "dir", DirectoryScope: "album"},
			{CanonicalKey: "artist/album/current.flac", RelativePath: "Artist/Album/Current.FLAC", NodeKind: "file"},
		}
	}
	manifest := encode(map[string]any{"version": 1, "nodes": nodes})
	exec(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id) VALUES (?,'publish_track_v1','queued','queued',?::jsonb,1,?)`, guard.OperationID, snapshot, guard.RiverJobID)
	exec(`INSERT INTO track_publication_attempt(operation_id,position_id,epoch,expected_generation,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest,mutation_journal) VALUES (?,?,?,?,?,'rewrite','queued',?::jsonb,?::jsonb,'[]')`, guard.OperationID, guard.ExpectedPositionID, epoch, input.Rewrite.CurrentGeneration, guard.CandidateGeneration, encode(input), manifest)
	exec(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,1,now())`, guard.ExpectedPositionID, epoch, input.Rewrite.CurrentGeneration, input.Rewrite.RelativePath,
		encode(persistence.PublicationCurrentOwnership{Version: 1, Identity: input.Rewrite.Ownership}),
		encode(persistence.PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts}),
		encode(persistence.PublicationCommitMetadata{Version: 1, Tags: capture.Tags}),
		encode(actualAssets))
	for _, node := range nodes {
		claimID := uuid.New()
		namespaceDigest := sha256.Sum256([]byte(node.CanonicalKey))
		var owner any
		if node.NodeKind == "file" {
			owner = guard.ExpectedPositionID
		}
		var directoryScope any
		if node.NodeKind == "dir" {
			directoryScope = node.DirectoryScope
		}
		exec(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key) VALUES (?,?,?,?,?,?,?,?)`, claimID, epoch, namespaceDigest[:], node.CanonicalKey, node.RelativePath, node.NodeKind, owner, directoryScope)
		for _, generation := range []uuid.UUID{input.Rewrite.CurrentGeneration, guard.CandidateGeneration} {
			exec(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, guard.ExpectedPositionID, generation)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	repository := persistence.NewSetupManagerRepository(db)
	claim, err := repository.ClaimPublicationDelivery(ctx, guard)
	if err != nil || claim != persistence.PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", claim, err)
	}
	preparer := fixture.preparer
	preparer.Repository = repository
	value, err := preparer.prepareManualLyricsFLAC(ctx, guard, claim, true)
	if err != nil {
		t.Fatal(err)
	}
	stage := value.(*preparedPublicationStage)
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Errorf("close real stage: %v", err)
		}
	})
	scope, reservation := initialOutputCopyScope(t, guard)
	scope.repository = repository
	proof, err := stage.initialOutputCopy(ctx, scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	if throughAdmission {
		if _, err := admitPreparedManualLyricsFLACExposure(ctx, scope, reservation, proof, input); err != nil {
			t.Fatal(err)
		}
	} else if continuous {
		if _, err := validatePreparedManualLyricsFLAC(ctx, scope, reservation, proof, input); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := applyAndRecordPreparedManualLyricsFLAC(ctx, scope, reservation, proof, input); err != nil {
			t.Fatal(err)
		}
	}
	attempt, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		Kind string `json:"kind"`
		Step string `json:"step"`
	}
	if err := json.Unmarshal(attempt.MutationJournal, &records); err != nil {
		t.Fatal(err)
	}
	var outcome persistence.PublicationOptionalLyricsOutcomeEvidence
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil {
		t.Fatal(err)
	}
	for _, raw := range entries {
		var item struct {
			Kind             string                                   `json:"kind"`
			Outcome          string                                   `json:"outcome"`
			FinalObservation persistence.PublicationStageObservation  `json:"final_observation"`
			ManualLyrics     persistence.PublicationManualLyricsInput `json:"manual_lyrics"`
			Preparation      persistence.PublicationPreparationRecord `json:"preparation"`
			InputIdentity    persistence.PublicationFileIdentity      `json:"input_identity"`
			OutputIdentity   persistence.PublicationFileIdentity      `json:"output_identity"`
			Diagnostic       string                                   `json:"diagnostic"`
		}
		if json.Unmarshal(raw, &item) == nil && item.Kind == "publication_optional_lyrics_outcome" {
			outcome = persistence.PublicationOptionalLyricsOutcomeEvidence{Kind: item.Outcome, FinalObservation: item.FinalObservation, ManualLyrics: item.ManualLyrics, Preparation: item.Preparation, InputIdentity: item.InputIdentity, OutputIdentity: item.OutputIdentity, Diagnostic: item.Diagnostic}
		}
	}
	if outcome.Kind != persistence.PublicationOptionalLyricsExactRoundTrip {
		t.Fatalf("unexpected durable lyrics outcome: %q", outcome.Kind)
	}
	var finalAssets persistence.PublicationAssetOutcomes
	if err := json.Unmarshal(outcome.FinalObservation.ActualAssets, &finalAssets); err != nil {
		t.Fatalf("final assets: %v", err)
	}
	if finalAssets.Cover.State != actualAssets.Cover.State || finalAssets.Cover.SHA256 != actualAssets.Cover.SHA256 || finalAssets.Cover.MIME != actualAssets.Cover.MIME || !equalOptionalSize(finalAssets.Cover.SizeBytes, actualAssets.Cover.SizeBytes) {
		t.Fatalf("final cover outcome = %+v, want observed %+v", finalAssets.Cover, actualAssets.Cover)
	}
	var finalMetadata persistence.PublicationCommitMetadata
	if err := json.Unmarshal(outcome.FinalObservation.ActualMetadata, &finalMetadata); err != nil {
		t.Fatal(err)
	}
	if removal {
		if _, present := finalMetadata.Tags["LYRICS"]; present {
			t.Fatalf("removed LYRICS still present: %#v", finalMetadata.Tags["LYRICS"])
		}
	} else if got := finalMetadata.Tags["LYRICS"]; len(got) != 1 || got[0] != input.ManualLyrics.Value {
		t.Fatalf("final lyrics = %#v", got)
	}
	if err := baselineArtwork.VerifyEquivalent(ctx, stage.paths.OutputPath); err != nil {
		t.Fatalf("private final output changed native FLAC picture/audio: %v", err)
	}
	wantPhase, wantRecords := "preparing", 4
	if throughAdmission {
		wantPhase, wantRecords = "exposing", 7
	} else if continuous {
		wantPhase, wantRecords = "validated", 6
	}
	if attempt.Phase != wantPhase || len(records) != wantRecords || records[0].Step != "intent" || records[1].Step != "directory_created" || records[2].Step != "input_ready" || records[3].Kind != "publication_optional_lyrics_outcome" {
		t.Fatalf("unexpected native preparation/outcome journal: %s", attempt.MutationJournal)
	}
	var generation uuid.UUID
	if err := db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, guard.ExpectedPositionID).Scan(ctx, &generation); err != nil || generation != input.Rewrite.CurrentGeneration {
		t.Fatalf("current generation changed: %s, %v", generation, err)
	}
	contents, err := os.ReadFile(filepath.Join(fixture.output, input.Rewrite.RelativePath))
	if err != nil || !bytes.Equal(contents, original) {
		t.Fatalf("current bytes changed: %v", err)
	}
	// These calls test DB transition authority only: the owner above has already
	// released its private work capability. They are not a continuous runtime handoff.
	if throughAdmission {
		if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil || len(entries) != 7 {
			t.Fatalf("admitted owner did not persist exposure admission: %s, %v", attempt.MutationJournal, err)
		}
		var exposure struct {
			Kind string `json:"kind"`
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(entries[6], &exposure); err != nil || exposure.Kind != "publication_exposure" || exposure.From != "validated" || exposure.To != "exposing" {
			t.Fatalf("invalid native exposure admission record: %s, %v", entries[6], err)
		}
		frozen, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || !reflect.DeepEqual(frozen, input) {
			t.Fatalf("admitted owner changed frozen publication inputs: %v", err)
		}
		scope.releaseSynchronousWork(reservation)
		return
	}
	if continuous && !nestedPath {
		frozen, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || !reflect.DeepEqual(frozen, input) {
			t.Fatalf("validated owner changed frozen publication inputs: %v", err)
		}
		var transitions []struct {
			Kind        string                                  `json:"kind"`
			From        string                                  `json:"from"`
			To          string                                  `json:"to"`
			Observation persistence.PublicationStageObservation `json:"observation"`
		}
		if err := json.Unmarshal(attempt.MutationJournal, &transitions); err != nil || len(transitions) != 6 || transitions[4].Kind != "stage_observation" || transitions[4].From != "preparing" || transitions[4].To != "staged" || transitions[5].Kind != "stage_observation" || transitions[5].From != "staged" || transitions[5].To != "validated" || !reflect.DeepEqual(transitions[4].Observation, outcome.FinalObservation) || !reflect.DeepEqual(transitions[5].Observation, outcome.FinalObservation) {
			t.Fatalf("validated owner did not persist both stage acknowledgements: %s, %v", attempt.MutationJournal, err)
		}
		// This is a DB-only admission test after the native owner returned its
		// validated evidence. It does not retain or exercise filesystem authority.
		intent := persistence.PublicationExposureIntent{
			Version: persistence.PublicationExposureVersion, StagedPath: stage.paths.RelativePath,
			TargetCanonicalKey: input.Rewrite.RelativePath, TargetRelativePath: input.Rewrite.RelativePath,
			Expectation: "expected_current", PrepOwner: stage.preparationRecord.OwnerToken,
			OutputRootParent: persistence.PublicationDirectoryIdentity{
				Platform: stage.parentIdentity.Platform, Volume: stage.parentIdentity.Volume, FileID: stage.parentIdentity.FileID,
			},
			TargetParent: persistence.PublicationDirectoryIdentity{
				Platform: stage.parentIdentity.Platform, Volume: stage.parentIdentity.Volume, FileID: stage.parentIdentity.FileID,
			},
		}
		observation := outcome.FinalObservation.Clone()
		currentInputs, currentValid, err := repository.PublicationManualLyricsCurrentInputs(ctx, guard)
		if err != nil || !currentValid || !reflect.DeepEqual(currentInputs, input) {
			t.Fatalf("validated current manual lyrics inputs = valid %v, err %v", currentValid, err)
		}
		currentInputs.DesiredTags["TITLE"][0] = "detached mutation"
		reloadedInputs, reloadedValid, err := repository.PublicationManualLyricsCurrentInputs(ctx, guard)
		if err != nil || !reloadedValid || !reflect.DeepEqual(reloadedInputs, input) {
			t.Fatalf("current manual lyrics inputs were not detached: valid %v, err %v", reloadedValid, err)
		}
		if _, valid, err := repository.PublicationPreparationInputs(ctx, guard); err != nil || valid {
			t.Fatalf("generic preparation reader accepted validated V3 inputs: valid %v, err %v", valid, err)
		}
		if _, valid, err := repository.CheckPublicationRewriteForDelivery(ctx, guard); err != nil || valid {
			t.Fatalf("preparation rewrite checker accepted validated stage: valid %v, err %v", valid, err)
		}
		var validatedEntries []json.RawMessage
		if err := json.Unmarshal(attempt.MutationJournal, &validatedEntries); err != nil {
			t.Fatal(err)
		}
		for _, count := range []int{4, 5} {
			if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, encode(validatedEntries[:count]), guard.OperationID); err != nil {
				t.Fatal(err)
			}
			if got, err := repository.BeginManualLyricsExposure(ctx, guard, intent, observation); err != nil || got != persistence.PublicationExposureStale {
				t.Fatalf("truncated %d-entry exposure prefix = %q, %v", count, got, err)
			}
			unchanged, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
			if err != nil || unchanged == nil || unchanged.Phase != "validated" {
				t.Fatalf("truncated admission changed phase: %v", err)
			}
			var actual, expected any
			if json.Unmarshal(unchanged.MutationJournal, &actual) != nil || json.Unmarshal([]byte(encode(validatedEntries[:count])), &expected) != nil || !reflect.DeepEqual(actual, expected) {
				t.Fatal("truncated admission mutated journal")
			}
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(attempt.MutationJournal), guard.OperationID); err != nil {
			t.Fatal(err)
		}
		admitted, err := repository.BeginManualLyricsExposure(ctx, guard, intent, observation)
		if err != nil || admitted != persistence.PublicationExposureRecorded {
			t.Fatalf("manual lyrics exposure admission = %q, %v", admitted, err)
		}
		admittedAttempt, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil || admittedAttempt.Phase != "exposing" {
			t.Fatalf("manual lyrics exposure phase = %v, %v", admittedAttempt, err)
		}
		if err := json.Unmarshal(admittedAttempt.MutationJournal, &records); err != nil || len(records) != 7 {
			t.Fatalf("manual lyrics exposure journal length = %d, %v", len(records), err)
		}
		exposingInputs, exposingValid, err := repository.PublicationManualLyricsCurrentInputs(ctx, guard)
		if err != nil || !exposingValid || !reflect.DeepEqual(exposingInputs, input) {
			t.Fatalf("exposing current manual lyrics inputs = valid %v, err %v", exposingValid, err)
		}
		staleInputGuard := guard
		staleInputGuard.ClaimUUID = uuid.New()
		if _, valid, err := repository.PublicationManualLyricsCurrentInputs(ctx, staleInputGuard); err != nil || valid {
			t.Fatalf("current manual lyrics reader accepted stale delivery guard: valid %v, err %v", valid, err)
		}
		unchangedAfterRead, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil || unchangedAfterRead.Phase != admittedAttempt.Phase || !bytes.Equal(unchangedAfterRead.MutationJournal, admittedAttempt.MutationJournal) {
			t.Fatalf("current-input reads changed exposure phase/journal: %v", err)
		}
		replayed, err := repository.BeginManualLyricsExposure(ctx, guard, intent, observation)
		if err != nil || replayed != persistence.PublicationExposureAlreadyRecorded {
			t.Fatalf("manual lyrics exposure replay = %q, %v", replayed, err)
		}
		if replayed, err := repository.BeginPublicationExposure(ctx, guard, intent, observation); err != nil || replayed != persistence.PublicationExposureStale {
			t.Fatalf("generic exposure API accepted V3 replay = %q, %v", replayed, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs='{"version":3}'::jsonb WHERE operation_id=?`, guard.OperationID); err != nil {
			t.Fatal(err)
		}
		if replayed, err := repository.BeginPublicationExposure(ctx, guard, intent, observation); err != nil || replayed != persistence.PublicationExposureStale {
			t.Fatalf("generic exposure accepted malformed V3 replay = %q, %v", replayed, err)
		}
		malformedAttempt, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil || malformedAttempt.Phase != "exposing" || !bytes.Equal(malformedAttempt.MutationJournal, admittedAttempt.MutationJournal) {
			t.Fatalf("malformed generic replay mutated phase/journal: %v", err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(admittedAttempt.FrozenInputs), guard.OperationID); err != nil {
			t.Fatal(err)
		}
		assertRejected := func(label string, candidateGuard persistence.PublicationDeliveryGuard, candidateIntent persistence.PublicationExposureIntent, candidateObservation persistence.PublicationStageObservation) {
			t.Helper()
			got, err := repository.BeginManualLyricsExposure(ctx, candidateGuard, candidateIntent, candidateObservation)
			if err != nil || got != persistence.PublicationExposureStale {
				t.Fatalf("%s admission = %q, %v", label, got, err)
			}
		}
		alteredObservation := observation.Clone()
		alteredObservation.PayloadSHA256 = fmt.Sprintf("%064x", 2)
		assertRejected("altered observation", guard, intent, alteredObservation)
		alteredIntent := intent
		alteredIntent.PrepOwner = uuid.New()
		assertRejected("altered owner", guard, alteredIntent, observation)
		staleGuard := guard
		staleGuard.Epoch = uuid.New()
		assertRejected("stale guard", staleGuard, intent, observation)
		unchanged, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil || unchanged.Phase != "exposing" || !bytes.Equal(unchanged.MutationJournal, admittedAttempt.MutationJournal) {
			t.Fatalf("rejected admission changed exposure journal/phase: %v", err)
		}
		currentAfter, err := os.ReadFile(filepath.Join(fixture.output, input.Rewrite.RelativePath))
		if err != nil || !bytes.Equal(currentAfter, original) {
			t.Fatalf("DB-only admission changed current bytes: %v", err)
		}
		scope.releaseSynchronousWork(reservation)
		return
	}
	observation := outcome.FinalObservation.Clone()
	wrong := observation.Clone()
	wrong.PayloadSHA256 = fmt.Sprintf("%064x", 1)
	if ack, err := repository.GuardedAdvanceManualLyricsStage(ctx, guard, "preparing", wrong); err != nil || ack != persistence.ManualLyricsStageStale {
		t.Fatalf("mismatched stage observation = %q, %v", ack, err)
	}
	for _, from := range []string{"preparing", "staged"} {
		if ack, err := repository.GuardedAdvanceManualLyricsStage(ctx, guard, from, observation); err != nil || ack != persistence.ManualLyricsStageAdvanced {
			t.Fatalf("advance from %s = %q, %v", from, ack, err)
		}
		if ack, err := repository.GuardedAdvanceManualLyricsStage(ctx, guard, from, observation); err != nil || ack != persistence.ManualLyricsStageAlreadyRecorded {
			t.Fatalf("replay from %s = %q, %v", from, ack, err)
		}
		phase := "staged"
		if from == "staged" {
			phase = "validated"
		}
		before, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		// Deliberately inconsistent test state must not authorize another append.
		resetPhase := func(phase string) {
			t.Helper()
			if _, err := db.ExecContext(ctx, `WITH changed AS (UPDATE track_publication_attempt SET phase=? WHERE operation_id=? RETURNING operation_id) UPDATE operation SET stage=? WHERE id IN (SELECT operation_id FROM changed)`, phase, guard.OperationID, phase); err != nil {
				t.Fatal(err)
			}
		}
		resetPhase("preparing")
		if ack, err := repository.GuardedAdvanceManualLyricsStage(ctx, guard, "preparing", observation); err != nil || ack != persistence.ManualLyricsStageStale {
			t.Fatalf("preparing with existing suffix = %q, %v", ack, err)
		}
		after, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil || !bytes.Equal(before.MutationJournal, after.MutationJournal) {
			t.Fatalf("invalid frontier mutated journal: %v", err)
		}
		resetPhase(phase)
	}
	advanced, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
	if err != nil || advanced.Phase != "validated" || !bytes.Equal(advanced.FrozenInputs, attempt.FrozenInputs) {
		t.Fatalf("invalid validated attempt: %+v, %v", advanced, err)
	}
	if err := json.Unmarshal(advanced.MutationJournal, &records); err != nil || len(records) != 6 {
		t.Fatalf("invalid validated journal: %s, %v", advanced.MutationJournal, err)
	}
	scope.releaseSynchronousWork(reservation)
}

func equalOptionalSize(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// appendNativeTestPicture serializes a FLAC PICTURE metadata block, leaving
// the original STREAMINFO/audio payload untouched.
func appendNativeTestPicture(t *testing.T, data []byte) []byte {
	t.Helper()
	var pngData bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 1, color.RGBA{B: 255, A: 255})
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}
	imageData := pngData.Bytes()
	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, uint32(3))
	writeText := func(value string) {
		_ = binary.Write(&body, binary.BigEndian, uint32(len(value)))
		body.WriteString(value)
	}
	writeText("image/png")
	writeText("正面の絵🎵")
	for _, value := range []uint32{2, 2, 32, 1, uint32(len(imageData))} {
		_ = binary.Write(&body, binary.BigEndian, value)
	}
	body.Write(imageData)
	if len(data) < 8 || string(data[:4]) != "fLaC" {
		t.Fatal("invalid FLAC fixture")
	}
	out := append([]byte(nil), data[:4]...)
	for offset := 4; ; {
		header := data[offset]
		length := int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
		end := offset + 4 + length
		if end > len(data) {
			t.Fatal("malformed FLAC metadata")
		}
		if header&0x80 != 0 {
			out = append(out, header&0x7f, data[offset+1], data[offset+2], data[offset+3])
			out = append(out, data[offset+4:end]...)
			data = data[end:]
			break
		}
		out = append(out, data[offset:end]...)
		offset = end
	}
	block := body.Bytes()
	out = append(out, 0x80|6, byte(len(block)>>16), byte(len(block)>>8), byte(len(block)))
	out = append(out, block...)
	return append(out, data...)
}
