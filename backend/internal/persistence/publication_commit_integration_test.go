//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestCommitFirstPublicationAndQuiescePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, _, repo := readyFirstPublicationCommitFixture(t)

	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("commit = %t, %v", committed, err)
	}
	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if current.Generation != f.guard.CandidateGeneration || current.Epoch != f.guard.Epoch || current.RelativePath != intent.TargetRelativePath || current.QualityPolicyVersion != 1 {
		t.Fatalf("unexpected committed projection: %#v", current)
	}
	var frozenIsNull bool
	if err := f.db.NewRaw(`SELECT frozen_inputs IS NULL FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &frozenIsNull); err != nil || !frozenIsNull {
		t.Fatalf("frozen inputs SQL NULL = %t, %v", frozenIsNull, err)
	}
	var phase, operationStage, operationState string
	var frozenAfter json.RawMessage
	if err := f.db.NewRaw(`SELECT a.phase,a.frozen_inputs,o.state,o.stage FROM track_publication_attempt a JOIN operation o ON o.id=a.operation_id WHERE a.operation_id=?`, f.guard.OperationID).Scan(ctx, &phase, &frozenAfter, &operationState, &operationStage); err != nil {
		t.Fatal(err)
	}
	if phase != "committed" || len(frozenAfter) != 0 || operationState != "running" || operationStage != "committed" {
		t.Fatalf("attempt/operation after commit = %q frozen=%s operation=%q/%q", phase, frozenAfter, operationState, operationStage)
	}
	var claimState string
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=? AND application_attempt=?`, f.guard.OperationID, f.guard.ApplicationAttempt).Scan(ctx, &claimState); err != nil || claimState != "executing" {
		t.Fatalf("claim state = %q, %v", claimState, err)
	}
	var journalBefore string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journalBefore); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("commit replay = %t, %v", committed, err)
	}
	var journalAfter string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journalAfter); err != nil || journalAfter != journalBefore {
		t.Fatalf("replay changed journal: %v", err)
	}
	if quiescent, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || !quiescent {
		t.Fatalf("quiesce committed delivery = %t, %v", quiescent, err)
	}
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=? AND application_attempt=?`, f.guard.OperationID, f.guard.ApplicationAttempt).Scan(ctx, &claimState); err != nil || claimState != "quiescent" {
		t.Fatalf("claim after quiescence = %q, %v", claimState, err)
	}
}

func TestCommitFirstPublicationRejectsStaleNamespacePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, _, repo := readyFirstPublicationCommitFixture(t)
	var journalBefore string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journalBefore); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(intent.TargetCanonicalKey))
	if _, err := f.db.NewRaw(`DELETE FROM publication_namespace_reservation r USING publication_namespace_claim c WHERE r.namespace_claim_id=c.id AND c.epoch=? AND c.key_digest=? AND r.position_id=? AND r.generation=?`, f.guard.Epoch, digest[:], f.guard.ExpectedPositionID, f.guard.CandidateGeneration).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || committed {
		t.Fatalf("stale namespace commit = %t, %v", committed, err)
	}
	var count int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &count); err != nil || count != 0 {
		t.Fatalf("current rows after stale commit = %d, %v", count, err)
	}
	var phase, stage string
	if err := f.db.NewRaw(`SELECT a.phase,o.stage FROM track_publication_attempt a JOIN operation o ON o.id=a.operation_id WHERE a.operation_id=?`, f.guard.OperationID).Scan(ctx, &phase, &stage); err != nil || phase != "exposing" || stage != "exposing" {
		t.Fatalf("stale commit changed state: attempt=%q operation=%q, %v", phase, stage, err)
	}
	var claims int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=? AND application_attempt=?`, f.guard.OperationID, f.guard.ApplicationAttempt).Scan(ctx, &claims); err != nil || claims != 1 {
		t.Fatalf("stale commit changed claim count: %d, %v", claims, err)
	}
	var journalAfter string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journalAfter); err != nil || journalAfter != journalBefore {
		t.Fatalf("stale commit changed journal: %v", err)
	}
}

func readyFirstPublicationCommitFixture(t *testing.T) (publicationExecutionFixture, PublicationExposureIntent, PublicationStageObservation, *SetupManagerRepository) {
	t.Helper()
	ctx := context.Background()
	f, intent, staged := readyPublicationExposureFixture(t)
	repo := NewSetupManagerRepository(f.db)
	staged.ActualAssets = json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`)
	staged.QualityPolicyVersion = publication.QualityPolicyVersion
	staged.ActualMetadata = json.RawMessage(`{"version":1,"tags":{"TITLE":["Track"]}}`)
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil {
		t.Fatal(err)
	}
	for i, raw := range entries {
		var record struct {
			Version     int                         `json:"version"`
			Kind        string                      `json:"kind"`
			From        string                      `json:"from"`
			To          string                      `json:"to"`
			Observation PublicationStageObservation `json:"observation"`
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record.Kind == "stage_observation" {
			record.Observation = staged
			entries[i], err = json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
		} else if record.Kind == PublicationInitialLyricsOutcomeKind {
			var outcome PublicationInitialLyricsOutcomeRecord
			if err := json.Unmarshal(raw, &outcome); err != nil {
				t.Fatal(err)
			}
			outcome.FinalObservation = staged
			entries[i], err = json.Marshal(outcome)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	journal, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	input, err := DecodePublicationInputs(attempt.FrozenInputs)
	if err != nil {
		t.Fatal(err)
	}
	input.Version = PublicationInputsVersion
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	input.DesiredTags = map[string][]string{"TITLE": {"Track"}}
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb, frozen_inputs=?::jsonb WHERE operation_id=?`, string(journal), string(frozen), f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, staged); err != nil || outcome != PublicationExposureRecorded {
		t.Fatalf("begin exposure = %q, %v", outcome, err)
	}
	size, mtime := int64(10), int64(20)
	installed := PublicationCandidateInstalledObservation{Version: 1, TargetRelativePath: intent.TargetRelativePath, Identity: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: staged.PayloadSHA256}}
	if outcome, err := repo.RecordPublicationCandidateInstalled(ctx, f.guard, intent, staged, installed); err != nil || outcome != PublicationCandidateInstalledRecorded {
		t.Fatalf("record installed = %q, %v", outcome, err)
	}
	return f, intent, staged, repo
}

func TestCommitFirstPublicationRejectsChangedReplayPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, repo := readyFirstPublicationCommitFixture(t)
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("commit = %t, %v", committed, err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication SET actual_metadata='{"version":1,"tags":{"TITLE":["Changed"]}}'::jsonb WHERE position_id=?`, f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || committed {
		t.Fatalf("changed replay = %t, %v", committed, err)
	}
	if quiescent, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || quiescent {
		t.Fatalf("quiesce changed projection = %t, %v", quiescent, err)
	}
	var state string
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=? AND application_attempt=?`, f.guard.OperationID, f.guard.ApplicationAttempt).Scan(ctx, &state); err != nil || state != "executing" {
		t.Fatalf("claim after rejected quiescence = %q, %v", state, err)
	}
}

func TestCommitFirstPublicationRejectsDuplicateWitnessPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, repo := readyFirstPublicationCommitFixture(t)
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("commit = %t, %v", committed, err)
	}
	var journal string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journal); err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(journal), &entries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var kind struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(entry, &kind) == nil && kind.Kind == "publication_commit" {
			entries = append(entries, entry)
			break
		}
	}
	duplicate, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(duplicate), f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || committed {
		t.Fatalf("duplicate-witness replay = %t, %v", committed, err)
	}
	if quiescent, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || quiescent {
		t.Fatalf("quiesce duplicate witness = %t, %v", quiescent, err)
	}
}

func TestCommitFirstPublicationRejectsChangedRiverAttemptPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, repo := readyFirstPublicationCommitFixture(t)
	if _, err := f.db.NewRaw(`UPDATE publication_execution_claim SET river_attempt=river_attempt+1 WHERE operation_id=? AND application_attempt=?`, f.guard.OperationID, f.guard.ApplicationAttempt).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || committed {
		t.Fatalf("changed river attempt commit = %t, %v", committed, err)
	}
	var count int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &count); err != nil || count != 0 {
		t.Fatalf("current rows = %d, %v", count, err)
	}
	var phase, stage string
	if err := f.db.NewRaw(`SELECT a.phase,o.stage FROM track_publication_attempt a JOIN operation o ON o.id=a.operation_id WHERE a.operation_id=?`, f.guard.OperationID).Scan(ctx, &phase, &stage); err != nil || phase != "exposing" || stage != "exposing" {
		t.Fatalf("state after stale delivery = %q/%q, %v", phase, stage, err)
	}
	var claims int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=? AND application_attempt=?`, f.guard.OperationID, f.guard.ApplicationAttempt).Scan(ctx, &claims); err != nil || claims != 1 {
		t.Fatalf("stale delivery changed claim count: %d, %v", claims, err)
	}
}

func TestCommitFirstPublicationRollsBackOnOperationUpdateFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, repo := readyFirstPublicationCommitFixture(t)
	_, err := f.db.NewRaw(`CREATE FUNCTION reject_publication_commit_stage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.stage='committed' THEN RAISE EXCEPTION 'injected operation update failure'; END IF; RETURN NEW; END $$`).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`CREATE TRIGGER reject_publication_commit_stage BEFORE UPDATE ON operation FOR EACH ROW EXECUTE FUNCTION reject_publication_commit_stage()`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.db.NewRaw(`DROP TRIGGER IF EXISTS reject_publication_commit_stage ON operation`).Exec(context.Background())
		_, _ = f.db.NewRaw(`DROP FUNCTION IF EXISTS reject_publication_commit_stage()`).Exec(context.Background())
	})
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err == nil || committed {
		t.Fatalf("commit with injected failure = %t, %v", committed, err)
	}
	var count int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &count); err != nil || count != 0 {
		t.Fatalf("current rows after rollback = %d, %v", count, err)
	}
	var phase, stage string
	if err := f.db.NewRaw(`SELECT a.phase,o.stage FROM track_publication_attempt a JOIN operation o ON o.id=a.operation_id WHERE a.operation_id=?`, f.guard.OperationID).Scan(ctx, &phase, &stage); err != nil || phase != "exposing" || stage != "exposing" {
		t.Fatalf("state after rollback = %q/%q, %v", phase, stage, err)
	}
}
