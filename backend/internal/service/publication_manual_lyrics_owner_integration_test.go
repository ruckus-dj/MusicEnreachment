//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	riverdatabasesql "github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// Admission is seeded here; preparation and outcome records must be produced by
// the actual native service and PostgreSQL repository, never by this fixture.
func TestPreparedManualLyricsFLACOwnerPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture, _, original := preparedManualLyricsFLACStage(t)
	input := fixture.repo.input.Clone()
	input.DesiredTags = map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}
	installationID := uuid.New()
	input.Tools = []persistence.SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffmpeg", Executable: "ffmpeg", Version: "1", VersionBanner: "ffmpeg version 1"},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffprobe", Executable: "ffprobe", Version: "1", VersionBanner: "ffprobe version 1"},
	}
	input.ManualLyrics.State = "value"
	input.ManualLyrics.Value = "[00:01.00]native PostgreSQL lyrics\r\n"
	input.ManualLyrics.ExpectedMetadataRevision = 7
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
	manifest := encode(map[string]any{"version": 1, "nodes": []persistence.PublicationNamespaceNode{{CanonicalKey: input.Rewrite.RelativePath, RelativePath: input.Rewrite.RelativePath, NodeKind: "file"}}})
	exec(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id) VALUES (?,'publish_track_v1','queued','queued',?::jsonb,1,?)`, guard.OperationID, snapshot, guard.RiverJobID)
	exec(`INSERT INTO track_publication_attempt(operation_id,position_id,epoch,expected_generation,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest,mutation_journal) VALUES (?,?,?,?,?,'rewrite','queued',?::jsonb,?::jsonb,'[]')`, guard.OperationID, guard.ExpectedPositionID, epoch, input.Rewrite.CurrentGeneration, guard.CandidateGeneration, encode(input), manifest)
	exec(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,1,now())`, guard.ExpectedPositionID, epoch, input.Rewrite.CurrentGeneration, input.Rewrite.RelativePath,
		encode(persistence.PublicationCurrentOwnership{Version: 1, Identity: input.Rewrite.Ownership}),
		encode(persistence.PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts}),
		encode(persistence.PublicationCommitMetadata{Version: 1, Tags: input.DesiredTags}),
		`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`)
	claimID := uuid.New()
	digest := sha256.Sum256([]byte(input.Rewrite.RelativePath))
	exec(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id) VALUES (?,?,?,?,?,'file',?)`, claimID, epoch, digest[:], input.Rewrite.RelativePath, input.Rewrite.RelativePath, guard.ExpectedPositionID)
	for _, generation := range []uuid.UUID{input.Rewrite.CurrentGeneration, guard.CandidateGeneration} {
		exec(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, guard.ExpectedPositionID, generation)
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
	if _, err := applyAndRecordPreparedManualLyricsFLAC(ctx, scope, reservation, proof, input); err != nil {
		t.Fatal(err)
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
	if attempt.Phase != "preparing" || len(records) != 4 || records[0].Step != "intent" || records[1].Step != "directory_created" || records[2].Step != "input_ready" || records[3].Kind != "publication_optional_lyrics_outcome" {
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
	scope.releaseSynchronousWork(reservation)
}
