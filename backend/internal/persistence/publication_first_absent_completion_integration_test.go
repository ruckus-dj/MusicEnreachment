//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"
)

// Settlement must require the service's live original-scope acknowledgement;
// persistence must not interpret durable cleanup journal entries as a substitute.
func TestSettleFirstAbsentPublicationRequiresOriginalScopeAcknowledgementPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	witness := PublicationFirstAbsentCleanupWitness{}

	settled, err := repo.SettleFirstAbsentPublication(context.Background(), f.guard, witness, false)
	if err != nil || settled {
		t.Fatalf("settle without original-scope acknowledgement = %v, %v; want false, nil", settled, err)
	}

	var operationState string
	if err := f.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.guard.OperationID).Scan(context.Background(), &operationState); err != nil {
		t.Fatal(err)
	}
	if operationState != "queued" {
		t.Fatalf("operation state after rejected settlement = %q, want queued", operationState)
	}
}

func TestFirstAbsentCleanupRejectsInvalidWitnessPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	_, err := repo.BeginPublicationFirstAbsentCleanup(context.Background(), f.guard, PublicationFirstAbsentCleanupWitness{})
	if err == nil {
		t.Fatal("begin cleanup with empty witness succeeded")
	}
	_, err = repo.RecordPublicationFirstAbsentCleanupResult(context.Background(), f.guard, PublicationFirstAbsentCleanupWitness{}, "input", "removed")
	if err == nil {
		t.Fatal("record cleanup result with empty witness succeeded")
	}
}

func TestSettleFirstAbsentPublicationCompletesCommittedFirstPublicationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, repo := readyFirstPublicationCommitFixture(t)
	// A committed audio publication retains its source hold until settlement.
	// Seed the same durable hold used by the input-hold integration fixtures so
	// this test exercises the complete terminal transaction rather than a
	// missing-hold refusal.
	pubSourceProtectionHoldWithFixture(t, f)
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("commit = %t, %v", committed, err)
	}

	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil {
		t.Fatal(err)
	}
	var witness PublicationFirstAbsentCleanupWitness
	for _, entry := range entries {
		var preparation PublicationPreparationRecord
		if json.Unmarshal(entry, &preparation) == nil && preparation.Kind == "publication_preparation" && preparation.Step == "input_ready" {
			if preparation.DirectoryIdentity == nil || preparation.CopiedInput == nil || preparation.CopiedInput.SizeBytes == nil || preparation.CopiedInput.MTime == nil {
				t.Fatal("input_ready preparation is missing cleanup witness identities")
			}
			size, mtime := *preparation.CopiedInput.SizeBytes, *preparation.CopiedInput.MTime
			witness = PublicationFirstAbsentCleanupWitness{
				PrivateDirRel: preparation.PrivateDirRel,
				InputRel:      preparation.InputRel,
				Directory:     *preparation.DirectoryIdentity,
				Input:         PublicationFileIdentity{SizeBytes: &size, MTime: &mtime},
			}
			break
		}
	}
	if !validFirstAbsentCleanupWitness(witness) {
		t.Fatal("committed first-publication fixture did not provide a valid cleanup witness")
	}

	var namespaceClaimsBefore, reservationsBefore int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_namespace_claim`).Scan(ctx, &namespaceClaimsBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation`).Scan(ctx, &reservationsBefore); err != nil {
		t.Fatal(err)
	}
	if namespaceClaimsBefore == 0 || reservationsBefore == 0 {
		t.Fatalf("fixture lacks namespace protection: claims=%d reservations=%d", namespaceClaimsBefore, reservationsBefore)
	}

	assertCleanupOutcome := func(action string, got PublicationFirstAbsentCleanupOutcome, err error) {
		t.Helper()
		if err != nil || got != PublicationFirstAbsentCleanupRecorded {
			t.Fatalf("%s = %q, %v", action, got, err)
		}
	}
	outcome, err := repo.BeginPublicationFirstAbsentCleanup(ctx, f.guard, witness)
	assertCleanupOutcome("begin input cleanup", outcome, err)
	outcome, err = repo.RecordPublicationFirstAbsentCleanupResult(ctx, f.guard, witness, "input", "already_absent")
	assertCleanupOutcome("record input cleanup", outcome, err)
	outcome, err = repo.BeginPublicationFirstAbsentDirectoryCleanup(ctx, f.guard, witness)
	assertCleanupOutcome("begin directory cleanup", outcome, err)
	outcome, err = repo.RecordPublicationFirstAbsentCleanupResult(ctx, f.guard, witness, "directory", "already_absent")
	assertCleanupOutcome("record directory cleanup", outcome, err)
	if settled, err := repo.SettleFirstAbsentPublication(ctx, f.guard, witness, true); err != nil || !settled {
		t.Fatalf("settle acknowledged cleanup = %t, %v", settled, err)
	}

	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if current.Generation != f.guard.CandidateGeneration {
		t.Fatalf("current generation = %s, want committed candidate %s", current.Generation, f.guard.CandidateGeneration)
	}
	var operationState, operationStage string
	if err := f.db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &operationState, &operationStage); err != nil {
		t.Fatal(err)
	}
	if operationState != "succeeded" || operationStage != "finished" {
		t.Fatalf("operation after settlement = %s/%s, want succeeded/finished", operationState, operationStage)
	}
	var sourceHolds, toolHolds, claims, attempts int
	for _, check := range []struct {
		query string
		dest  *int
	}{
		{`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, &sourceHolds},
		{`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, &toolHolds},
		{`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, &claims},
		{`SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, &attempts},
	} {
		if err := f.db.NewRaw(check.query, f.guard.OperationID).Scan(ctx, check.dest); err != nil {
			t.Fatal(err)
		}
	}
	if sourceHolds != 0 || toolHolds != 0 || claims != 0 || attempts != 0 {
		t.Fatalf("settled durable state: source holds=%d tool holds=%d claims=%d attempts=%d", sourceHolds, toolHolds, claims, attempts)
	}
	var namespaceClaimsAfter, reservationsAfter int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_namespace_claim`).Scan(ctx, &namespaceClaimsAfter); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation`).Scan(ctx, &reservationsAfter); err != nil {
		t.Fatal(err)
	}
	if namespaceClaimsAfter != namespaceClaimsBefore || reservationsAfter != reservationsBefore {
		t.Fatalf("settlement changed namespace protection: claims %d->%d reservations %d->%d", namespaceClaimsBefore, namespaceClaimsAfter, reservationsBefore, reservationsAfter)
	}
	snapshot, err := repo.LoadPublicationStartupSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 0 {
		t.Fatalf("startup snapshot after settlement has %d records, want empty", len(snapshot.Records))
	}
}
