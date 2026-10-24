//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func publicationResultForFixture(f publicationExecutionFixture) PublicationResult {
	size, mtime := int64(10), int64(20)
	observation := newValidPublicationStageObservation()
	return PublicationResult{
		Observation: observation,
		InstalledOwnership: PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: PublicationFileIdentity{
			SizeBytes: &size, MTime: &mtime, SHA256: observation.PayloadSHA256,
		}},
	}
}

func TestCommitPublicationResultCarriesUnchangedCoverSummaryPostgreSQL(t *testing.T) {
	for _, outcome := range []string{"verified", "mismatch"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, repository, input, observation, _ := prepareManualLyricsCommitFixture(t)
			alignManualPublicationResultTarget(t, ctx, f, &observation)
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte("cover bytes")))
			size := int64(11)
			cover := PublicationCoverOutcome{State: PublicationAssetEmbedded, SHA256: digest, MIME: "image/png", SizeBytes: &size}
			assets, err := DecodePublicationAssetOutcomes(observation.ActualAssets)
			if err != nil {
				t.Fatal(err)
			}
			assets.Cover = cover
			observation.ActualAssets = mustJSON(assets)
			projection := PublicationCoverEmbeddingProjection{
				Version: PublicationCoverEmbeddingProjectionVersion, Generation: input.Rewrite.CurrentGeneration,
				ReceiptOperationID: uuid.New(), ReceiptReleaseID: uuid.New(), ReceiptRevision: 1, ReceiptMode: "manual",
				RequestedGeneration: uuid.New(), RequestedSHA256: digest, Outcome: outcome,
			}
			if outcome == "mismatch" {
				projection.WarningCode = "cover_mismatch"
				projection.RequestedSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte("different requested cover")))
			}
			if _, err := f.db.ExecContext(ctx, `UPDATE track_publication SET actual_assets=jsonb_set(actual_assets,'{cover}',?::jsonb),cover_embedding=?::jsonb WHERE position_id=?`, string(mustJSON(cover)), string(mustJSON(projection)), f.guard.ExpectedPositionID); err != nil {
				t.Fatal(err)
			}
			committed, err := repository.CommitPublicationResult(ctx, f.guard, PublicationResult{
				Observation: observation,
				InstalledOwnership: PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: PublicationFileIdentity{
					SizeBytes: observation.Ownership.ExpectedSize, MTime: observation.Ownership.ExpectedMTime, SHA256: observation.PayloadSHA256,
				}},
			})
			if err != nil || !committed {
				t.Fatalf("rewrite with unchanged cover = %v, %v", committed, err)
			}
			var raw string
			if err := f.db.NewRaw(`SELECT cover_embedding::text FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &raw); err != nil {
				t.Fatal(err)
			}
			current, err := decodePublicationCoverEmbeddingProjection(json.RawMessage(raw))
			if err != nil || current.Generation != f.guard.CandidateGeneration || current.Outcome != outcome || current.WarningCode != projection.WarningCode {
				t.Fatalf("current rebound cover summary = %s, %v", raw, err)
			}
		})
	}
}

func TestPublicationCoverEmbeddingV2SQLConstraintPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, _, _ := prepareManualLyricsCommitFixture(t)
	valid := map[string]any{
		"version": 2, "generation": uuid.NewString(), "receipt_operation_id": uuid.NewString(),
		"receipt_release_id": uuid.NewString(), "receipt_revision": 1, "receipt_mode": "manual",
		"requested_generation": uuid.NewString(), "requested_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte("original"))),
		"outcome": "verified", "warning_code": "", "embedded_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte("derivative"))),
		"embedded_mime": "image/png", "embedded_size": 10,
	}
	write := func(value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = f.db.ExecContext(ctx, `UPDATE track_publication SET cover_embedding=?::jsonb WHERE position_id=?`, string(raw), f.guard.ExpectedPositionID)
		return err
	}
	if err := write(valid); err != nil {
		t.Fatalf("valid v2 derivative projection was rejected: %v", err)
	}
	invalid := []struct {
		name string
		edit func(map[string]any)
	}{
		{name: "missing derivative hash", edit: func(value map[string]any) { delete(value, "embedded_sha256") }},
		{name: "JSON null size", edit: func(value map[string]any) { value["embedded_size"] = nil }},
		{name: "invalid nested hash", edit: func(value map[string]any) { value["embedded_sha256"] = map[string]any{"hex": "bad"} }},
		{name: "missing receipt fields", edit: func(value map[string]any) { delete(value, "receipt_revision") }},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			candidate := make(map[string]any, len(valid))
			for key, value := range valid {
				candidate[key] = value
			}
			test.edit(candidate)
			if err := write(candidate); err == nil {
				t.Fatal("invalid v2 projection passed the SQL constraint")
			}
		})
	}
}

func TestCommitPublicationResultManualLyricsFreshnessPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repository, input, observation, _ := prepareManualLyricsCommitFixture(t)
	alignManualPublicationResultTarget(t, ctx, f, &observation)
	result := PublicationResult{
		Observation: observation,
		InstalledOwnership: PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: PublicationFileIdentity{
			SizeBytes: observation.Ownership.ExpectedSize, MTime: observation.Ownership.ExpectedMTime, SHA256: observation.PayloadSHA256,
		}},
	}
	committed, err := repository.CommitPublicationResult(ctx, f.guard, result)
	if err != nil || !committed {
		t.Fatalf("manual lyrics commit = %v, %v", committed, err)
	}
	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if current.Generation != f.guard.CandidateGeneration {
		t.Fatalf("manual lyrics commit did not install candidate generation: %s", current.Generation)
	}
	var receipt PublicationManualLyricsReceipt
	if err := f.db.NewSelect().Model(&receipt).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(input.ManualLyrics.Value)))
	if receipt.DesiredRevision != input.ManualLyrics.ExpectedMetadataRevision || receipt.ConfirmedGeneration != input.Rewrite.CurrentGeneration || receipt.PublishedGeneration != f.guard.CandidateGeneration || receipt.DesiredSHA256 == nil || *receipt.DesiredSHA256 != digest {
		t.Fatalf("manual lyrics receipt does not bind frozen authority: %+v", receipt)
	}

	stale, staleRepository, staleInput, staleObservation, _ := prepareManualLyricsCommitFixture(t)
	alignManualPublicationResultTarget(t, ctx, stale, &staleObservation)
	if _, err := stale.db.ExecContext(ctx, `UPDATE release_track SET metadata_revision=metadata_revision+1 WHERE id=?`, stale.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	staleResult := PublicationResult{Observation: staleObservation, InstalledOwnership: result.InstalledOwnership}
	committed, err = staleRepository.CommitPublicationResult(ctx, stale.guard, staleResult)
	if err != nil || committed {
		t.Fatalf("stale metadata revision commit = %v, %v", committed, err)
	}
	assertManualLyricsResultUnchanged(t, ctx, stale, staleInput)

	drift, driftRepository, driftInput, driftObservation, _ := prepareManualLyricsCommitFixture(t)
	alignManualPublicationResultTarget(t, ctx, drift, &driftObservation)
	changed := "[00:03.00]different\r\n"
	metadata, err := json.Marshal(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"TITLE": {"published title"}, "LYRICS": {changed}}})
	if err != nil {
		t.Fatal(err)
	}
	envelope := map[string]json.RawMessage{"local_metadata": mustJSON(map[string]any{
		"version": 1, "scope": "position", "desired": map[string]any{"synced_lyrics": map[string]any{
			"state": "value", "value": changed, "authority": "manual", "provenance": map[string]any{"kind": "manual"},
		}},
	})}
	if _, err := drift.db.ExecContext(ctx, `UPDATE release_track SET metadata=?::jsonb WHERE id=?`, string(mustJSON(envelope)), drift.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	_ = metadata
	if _, err := drift.db.ExecContext(ctx, `UPDATE track_publication SET actual_metadata=?::jsonb WHERE position_id=?`, string(metadata), drift.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	driftResult := PublicationResult{Observation: driftObservation, InstalledOwnership: result.InstalledOwnership}
	committed, err = driftRepository.CommitPublicationResult(ctx, drift.guard, driftResult)
	if err != nil || committed {
		t.Fatalf("same revision manual lyrics drift commit = %v, %v", committed, err)
	}
	assertManualLyricsResultUnchanged(t, ctx, drift, driftInput)
}

func alignManualPublicationResultTarget(t *testing.T, ctx context.Context, f publicationExecutionFixture, observation *PublicationStageObservation) {
	t.Helper()
	seedManualPublicationResultChoice(t, ctx, f)
	observation.RelativePath = "root/final.flac"
	nodes := []PublicationNamespaceNode{{NodeKind: "file", RelativePath: observation.RelativePath}}
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

func assertManualLyricsResultUnchanged(t *testing.T, ctx context.Context, f publicationExecutionFixture, input PublicationInputs) {
	t.Helper()
	var generation uuid.UUID
	if err := f.db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &generation); err != nil || generation != input.Rewrite.CurrentGeneration {
		t.Fatalf("rejected manual lyrics changed current generation: %s, %v", generation, err)
	}
	var claimState, attemptPhase, operationState string
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &claimState); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT phase FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &attemptPhase); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &operationState); err != nil {
		t.Fatal(err)
	}
	if claimState != "executing" || attemptPhase == "" || operationState != "running" {
		t.Fatalf("rejected manual lyrics changed authority: claim=%s attempt=%s operation=%s", claimState, attemptPhase, operationState)
	}
}

func preparePublicationResultFixture(t *testing.T) publicationExecutionFixture {
	t.Helper()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	manifest, err := json.Marshal(struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}{Version: 1, Nodes: []PublicationNamespaceNode{
		{CanonicalKey: "album", RelativePath: "album", NodeKind: "dir", DirectoryScope: "album"},
		{CanonicalKey: "album/track.flac", RelativePath: "album/track.flac", NodeKind: "file"},
	}})
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
