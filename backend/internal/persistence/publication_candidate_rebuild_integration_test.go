//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func publicationCandidateRebuildFixtureRecord(guard PublicationDeliveryGuard, step string) PublicationCandidateRebuildRecord {
	size, mtime := int64(42), int64(1700000000)
	return PublicationCandidateRebuildRecord{
		Version: PublicationCandidateRebuildVersion, Kind: "publication_candidate_rebuild", Step: step,
		Guard: guard, OwnerToken: uuid.New(), PrivateDirRel: "private/op",
		InputRel: "private/op/input.flac", OutputRel: "private/op/output.flac",
		DirectoryIdentity: PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk1", FileID: "dir-1"},
		Candidate:         &PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "0123456789abcdef"},
		Input:             &PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "fedcba9876543210"},
	}
}

func preparePublicationCandidateRebuild(t *testing.T) (*publicationExecutionFixture, *SetupManagerRepository) {
	t.Helper()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	return &f, repo
}

func TestPublicationCandidateRebuildRejectsUnclaimedOwnerWithoutMutationPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	intent := publicationCandidateRebuildFixtureRecord(f.guard, "intent")
	if err := repo.RecordPublicationCandidateRebuild(context.Background(), f.guard, intent); err == nil {
		t.Fatal("recorded rebuild intent without an executing claim")
	}
	attempt, err := repo.ReadPublicationAttempt(context.Background(), f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if string(attempt.MutationJournal) != "[]" {
		t.Fatalf("unclaimed owner mutated journal: %s", attempt.MutationJournal)
	}
	var claimCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &claimCount); err != nil {
		t.Fatal(err)
	}
	if claimCount != 0 {
		t.Fatalf("unclaimed fixture unexpectedly has %d claims", claimCount)
	}
}

func TestPublicationCandidateRebuildRecordsIntentAndCompletionPostgreSQL(t *testing.T) {
	t.Parallel()
	f, repo := preparePublicationCandidateRebuild(t)
	ctx := context.Background()
	intent := publicationCandidateRebuildFixtureRecord(f.guard, "intent")
	if err := repo.RecordPublicationCandidateRebuild(ctx, f.guard, intent); err != nil {
		t.Fatalf("record intent: %v", err)
	}
	complete := intent
	complete.Step = "complete"
	if err := repo.RecordPublicationCandidateRebuild(ctx, f.guard, complete); err != nil {
		t.Fatalf("record completion: %v", err)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil || len(entries) != 2 {
		t.Fatalf("journal entries=%d, err=%v; journal=%s", len(entries), err, attempt.MutationJournal)
	}
	var recordedIntent, recordedComplete PublicationCandidateRebuildRecord
	if err := decodeStrictJSON(entries[0], &recordedIntent); err != nil {
		t.Fatal(err)
	}
	if err := decodeStrictJSON(entries[1], &recordedComplete); err != nil {
		t.Fatal(err)
	}
	if !sameCandidateRebuildPlan(recordedIntent, recordedComplete) {
		t.Fatal("persisted intent and completion do not have the same plan")
	}
}

func TestPublicationCandidateRebuildRejectsLostClaimWithoutAcknowledgingPostgreSQL(t *testing.T) {
	t.Parallel()
	f, repo := preparePublicationCandidateRebuild(t)
	ctx := context.Background()
	intent := publicationCandidateRebuildFixtureRecord(f.guard, "intent")
	if err := repo.RecordPublicationCandidateRebuild(ctx, f.guard, intent); err != nil {
		t.Fatalf("record intent: %v", err)
	}
	if ok, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || !ok {
		t.Fatalf("mark claim quiescent = %t, %v", ok, err)
	}
	complete := intent
	complete.Step = "complete"
	if err := repo.RecordPublicationCandidateRebuild(ctx, f.guard, complete); err == nil {
		t.Fatal("completion succeeded after live claim was lost")
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil || len(entries) != 1 {
		t.Fatalf("lost-claim call acknowledged completion: entries=%d err=%v journal=%s", len(entries), err, attempt.MutationJournal)
	}
}
