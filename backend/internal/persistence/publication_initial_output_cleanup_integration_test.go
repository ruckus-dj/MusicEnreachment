//go:build integration

package persistence

import (
	"context"
	"testing"
)

func TestPublicationInitialOutputCleanupAcknowledgesAndSettlesReturnedValidationFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	ctx := context.Background()
	if got, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || got != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", got, err)
	}
	input, valid, err := repo.PublicationPreparationInputs(ctx, f.guard)
	if err != nil || !valid || input.InitialLyrics == nil {
		t.Fatalf("preparation inputs = valid %t, %v", valid, err)
	}
	prep := publicationPreparationFixtureRecord(f.guard)
	if got, err := repo.RecordPublicationPreparation(ctx, f.guard, prep); err != nil || got != PublicationPreparationRecorded {
		t.Fatalf("preparation intent = %q, %v", got, err)
	}
	created := prep
	created.Step = "directory_created"
	created.DirectoryIdentity = &PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk1", FileID: "private-99"}
	if got, err := repo.RecordPublicationPreparation(ctx, f.guard, created); err != nil || got != PublicationPreparationRecorded {
		t.Fatalf("directory created = %q, %v", got, err)
	}
	size, mtime := int64(5), int64(123)
	ready := created
	ready.Step, ready.InputSize, ready.InputMTime = "input_ready", &size, &mtime
	ready.CopiedInput = &PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if got, err := repo.RecordPublicationPreparation(ctx, f.guard, ready); err != nil || got != PublicationPreparationRecorded {
		t.Fatalf("input ready = %q, %v", got, err)
	}
	acquisition := PublicationInitialLyricsAcquisitionRecord{Version: PublicationInitialLyricsVersion, Kind: PublicationInitialLyricsAcquisitionKind, Guard: f.guard, Authority: *input.InitialLyrics, Source: "none"}
	if got, err := repo.GuardedRecordPublicationInitialLyricsAcquisition(ctx, f.guard, acquisition); err != nil || got != PublicationOptionalLyricsRecorded {
		t.Fatalf("acquisition = %q, %v", got, err)
	}
	outSize, outMTime := int64(12), int64(456)
	witness := PublicationInitialOutputCleanupWitness{
		Preparation: ready,
		Input:       *ready.CopiedInput,
		Output:      PublicationFileIdentity{SizeBytes: &outSize, MTime: &outMTime, SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		OutputRel:   ready.OutputRel,
	}
	if got, err := repo.BeginPublicationInitialOutputCleanup(ctx, f.guard, witness); err != nil || got != PublicationInitialOutputCleanupRecorded {
		t.Fatalf("cleanup intent = %q, %v", got, err)
	}
	if got, err := repo.BeginPublicationInitialOutputCleanup(ctx, f.guard, witness); err != nil || got != PublicationInitialOutputCleanupStale {
		t.Fatalf("replayed cleanup intent = %q, %v", got, err)
	}
	if got, err := repo.RecordPublicationInitialOutputCleanupResult(ctx, f.guard, witness); err != nil || got != PublicationInitialOutputCleanupRecorded {
		t.Fatalf("cleanup completion = %q, %v", got, err)
	}
	settled, err := repo.SettlePublicationInitialOutputFailure(ctx, f.guard, witness, true)
	if err != nil || !settled {
		t.Fatalf("settle = %t, %v", settled, err)
	}
	var state, stage, safeError string
	if err := f.db.NewRaw(`SELECT state,stage,safe_error FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state, &stage, &safeError); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || stage != "publication_preparation" || safeError == "" {
		t.Fatalf("operation = %q/%q safe error %q", state, stage, safeError)
	}
	var attemptCount, claimCount, publicationCount int
	for _, check := range []struct {
		query string
		into  *int
	}{
		{`SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, &attemptCount},
		{`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, &claimCount},
		{`SELECT count(*) FROM track_publication WHERE position_id=?`, &publicationCount},
	} {
		arg := any(f.guard.OperationID)
		if check.into == &publicationCount {
			arg = f.guard.ExpectedPositionID
		}
		if err := f.db.NewRaw(check.query, arg).Scan(ctx, check.into); err != nil {
			t.Fatal(err)
		}
	}
	if attemptCount != 0 || claimCount != 0 || publicationCount != 0 {
		t.Fatalf("settlement rows remain: attempt=%d claim=%d current publication=%d", attemptCount, claimCount, publicationCount)
	}
	if settled, err := repo.SettlePublicationInitialOutputFailure(ctx, f.guard, witness, true); err != nil || settled {
		t.Fatalf("replayed settlement = %t, %v", settled, err)
	}
}
