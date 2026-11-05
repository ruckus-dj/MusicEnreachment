//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func publicationResultForFixture(f publicationExecutionFixture) PublicationResult {
	size, mtime := int64(10), int64(20)
	observation := newValidPublicationStageObservation()
	observation.RelativePath = scopePublicationPath(observation.RelativePath, f.namespaceScope)
	return PublicationResult{
		Observation: observation,
		InstalledOwnership: PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: PublicationFileIdentity{
			SizeBytes: &size, MTime: &mtime, SHA256: observation.PayloadSHA256,
		}},
	}
}

func alignManualPublicationResultTarget(t *testing.T, ctx context.Context, f publicationExecutionFixture, observation *PublicationStageObservation) {
	t.Helper()
	seedManualPublicationResultChoice(t, ctx, f)
	observation.RelativePath = scopePublicationPath("root/final.flac", f.namespaceScope)
	nodes := scopePublicationManifest([]PublicationNamespaceNode{{NodeKind: "file", RelativePath: "root/final.flac"}}, f.namespaceScope)
	raw, err := json.Marshal(struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}{Version: 1, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE track_publication_attempt SET namespace_manifest=?::jsonb WHERE operation_id=?`, string(raw), f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
}

func seedManualPublicationResultChoice(t *testing.T, ctx context.Context, f publicationExecutionFixture) {
	t.Helper()
	attempt, err := NewSetupManagerRepository(f.db).ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || attempt == nil {
		t.Fatalf("read manual fixture attempt: %v", err)
	}
	input, err := DecodePublicationInputs(attempt.FrozenInputs)
	if err != nil || input.ManualLyrics == nil {
		t.Fatalf("read manual fixture choice: %v", err)
	}
	manual := input.ManualLyrics
	input.Format = "source"
	if _, err := f.db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(mustJSON(input)), f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	envelope := map[string]any{"version": 1, "scope": "position", "desired": map[string]any{
		"synced_lyrics": map[string]any{"state": manual.State, "value": manual.Value, "authority": "manual", "provenance": map[string]any{"kind": "manual"}},
	}}
	if _, err := f.db.ExecContext(ctx, `UPDATE release_track SET metadata_revision=?,metadata=jsonb_set(metadata,'{local_metadata}',?::jsonb,true) WHERE id=?`, manual.ExpectedMetadataRevision, string(mustJSON(envelope)), f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
}

func preparePublicationResultFixture(t *testing.T) publicationExecutionFixture {
	t.Helper()
	return preparePublicationResultFixtureFromExecution(t, newPublicationExecutionFixture(t), "")
}

func preparePublicationResultFixtureWithDatabase(t *testing.T, db *bun.DB, scope string) publicationExecutionFixture {
	t.Helper()
	return preparePublicationResultFixtureFromExecution(t, newPublicationExecutionFixtureWithDatabase(t, db, scope), scope)
}

func preparePublicationResultFixtureFromExecution(t *testing.T, f publicationExecutionFixture, scope string) publicationExecutionFixture {
	t.Helper()
	ctx := context.Background()
	nodes := scopePublicationManifest([]PublicationNamespaceNode{
		{CanonicalKey: "album", RelativePath: "album", NodeKind: "dir", DirectoryScope: "album"},
		{CanonicalKey: "album/track.flac", RelativePath: "album/track.flac", NodeKind: "file"},
	}, scope)
	manifest, err := json.Marshal(struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}{Version: 1, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE track_publication_attempt SET namespace_manifest=?::jsonb WHERE operation_id=?`, string(manifest), f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if outcome, err := NewSetupManagerRepository(f.db).ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim publication delivery = %q, %v", outcome, err)
	}
	return f
}

func TestCommitPublicationResultInstallsCurrentProjectionPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	repository := NewSetupManagerRepository(f.db)
	result := publicationResultForFixture(f)
	committed, err := repository.CommitPublicationResult(ctx, f.guard, result)
	if err != nil || !committed {
		t.Fatalf("commit publication result = %v, %v", committed, err)
	}
	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if current.Epoch != f.guard.Epoch || current.Generation != f.guard.CandidateGeneration || current.RelativePath != result.Observation.RelativePath || !sameJSONValue(current.ActualAudio, result.Observation.ActualAudio) || !sameJSONValue(current.ActualMetadata, result.Observation.ActualMetadata) {
		t.Fatalf("current publication does not match installed result: %+v", current)
	}
	var state, stage string
	if err := f.db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state, &stage); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || stage != "succeeded" {
		t.Fatalf("operation terminal state = %s/%s", state, stage)
	}
	for _, query := range []string{
		`SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`,
		`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`,
	} {
		var count int
		if err := f.db.NewRaw(query, f.guard.OperationID).Scan(ctx, &count); err != nil || count != 0 {
			t.Fatalf("consumed delivery rows count = %d, %v", count, err)
		}
	}
	if err := f.db.NewRaw(`SELECT state,attempt FROM river_job WHERE id=?`, f.guard.RiverJobID).Scan(ctx, &state, new(int)); err != nil || state != "running" {
		t.Fatalf("River job is not the running delivery: %s, %v", state, err)
	}
	committed, err = repository.CommitPublicationResult(ctx, f.guard, result)
	if err != nil || committed {
		t.Fatalf("retry without attempt = %v, %v", committed, err)
	}
}

func startPublicationReconciliationForResult(t *testing.T, ctx context.Context, repository *SetupManagerRepository, epoch, position uuid.UUID) (PublicationReconciliationDeliveryGuard, *reconciliationDeliveryInserter) {
	t.Helper()
	if err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repository.RequestPublicationReconciliationWith(ctx, tx, epoch, []uuid.UUID{position}, []string{"publication_result_test"})
	}); err != nil {
		t.Fatal(err)
	}
	inserter := new(reconciliationDeliveryInserter)
	if dispatched, err := repository.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || dispatched != 1 {
		t.Fatalf("dispatch initial reconciliation=%d err=%v", dispatched, err)
	}
	jobID, operationID := inserter.ids[0], inserter.args[0].OperationID
	if _, err := repository.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	guard, outcome, err := repository.ClaimPublicationReconciliationDelivery(ctx, operationID, jobID, 1)
	if err != nil || outcome != PublicationReconciliationClaimed {
		t.Fatalf("claim reconciliation=%s err=%v", outcome, err)
	}
	return guard, inserter
}

func assertReconciliationWake(t *testing.T, ctx context.Context, db *bun.DB, epoch, position uuid.UUID, requested, dirty int) {
	t.Helper()
	var generation, dirtyCount int
	var pending int
	if err := db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &generation); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &dirtyCount); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE output_epoch=? AND position_id=? AND state='pending'`, epoch, position).Scan(ctx, &pending); err != nil {
		t.Fatal(err)
	}
	if generation != requested || dirtyCount != dirty || pending != 1 {
		t.Fatalf("wake generation=%d dirty=%d pending=%d; want %d/%d/1", generation, dirtyCount, pending, requested, dirty)
	}
}

func TestCommitPublicationResultSQLFailureRollsBackAllStatePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	var generation uuid.UUID
	if err := f.db.NewRaw(`SELECT candidate_generation FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &generation); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `CREATE FUNCTION fail_publication_result_attempt_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected publication result failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `CREATE TRIGGER fail_publication_result_attempt_delete AFTER DELETE ON track_publication_attempt FOR EACH ROW EXECUTE FUNCTION fail_publication_result_attempt_delete()`); err != nil {
		t.Fatal(err)
	}
	_, err := NewSetupManagerRepository(f.db).CommitPublicationResult(ctx, f.guard, publicationResultForFixture(f))
	if err == nil {
		t.Fatal("expected injected SQL failure")
	}
	var count int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &count); err != nil || count != 0 {
		t.Fatalf("projection survived rollback: count=%d err=%v", count, err)
	}
	var state, phase, claimState string
	if err := f.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT phase FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &phase); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &claimState); err != nil {
		t.Fatal(err)
	}
	if state != "running" || phase != "preparing" || claimState != "executing" {
		t.Fatalf("rollback changed authority: operation=%s attempt=%s claim=%s", state, phase, claimState)
	}
	for _, table := range []string{"publication_source_hold", "operation_tool_read_hold"} {
		if err := f.db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &count); err != nil || count != 0 {
			t.Fatalf("rollback changed %s: count=%d err=%v", table, count, err)
		}
	}
	if err := f.db.NewRaw(`SELECT candidate_generation FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &generation); err != nil || generation != f.guard.CandidateGeneration {
		t.Fatalf("attempt changed after rollback: generation=%s err=%v", generation, err)
	}
}
