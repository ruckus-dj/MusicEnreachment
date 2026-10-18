//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"
)

// A complete cleanup journal is not sufficient to settle if the durable source
// hold has disappeared: settlement must leave the operation and execution
// attempt intact so the delivery can be retried or diagnosed.
func TestSettleFirstAbsentPublicationRejectsMissingSourceHoldPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, repo := readyFirstPublicationCommitFixture(t)
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
		var prep PublicationPreparationRecord
		if json.Unmarshal(entry, &prep) == nil && prep.Kind == "publication_preparation" && prep.Step == "input_ready" {
			if prep.DirectoryIdentity == nil || prep.CopiedInput == nil || prep.CopiedInput.SizeBytes == nil || prep.CopiedInput.MTime == nil {
				t.Fatal("input_ready preparation is missing cleanup witness identities")
			}
			size, mtime := *prep.CopiedInput.SizeBytes, *prep.CopiedInput.MTime
			witness = PublicationFirstAbsentCleanupWitness{PrivateDirRel: prep.PrivateDirRel, InputRel: prep.InputRel,
				Directory: *prep.DirectoryIdentity, Input: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime}}
			break
		}
	}
	if !validFirstAbsentCleanupWitness(witness) {
		t.Fatal("committed fixture did not provide a valid cleanup witness")
	}
	for _, action := range []struct {
		name string
		call func() (PublicationFirstAbsentCleanupOutcome, error)
	}{
		{"input intent", func() (PublicationFirstAbsentCleanupOutcome, error) {
			return repo.BeginPublicationFirstAbsentCleanup(ctx, f.guard, witness)
		}},
		{"input result", func() (PublicationFirstAbsentCleanupOutcome, error) {
			return repo.RecordPublicationFirstAbsentCleanupResult(ctx, f.guard, witness, "input", "already_absent")
		}},
		{"directory intent", func() (PublicationFirstAbsentCleanupOutcome, error) {
			return repo.BeginPublicationFirstAbsentDirectoryCleanup(ctx, f.guard, witness)
		}},
		{"directory result", func() (PublicationFirstAbsentCleanupOutcome, error) {
			return repo.RecordPublicationFirstAbsentCleanupResult(ctx, f.guard, witness, "directory", "already_absent")
		}},
	} {
		got, err := action.call()
		if err != nil || got != PublicationFirstAbsentCleanupRecorded {
			t.Fatalf("%s = %q, %v", action.name, got, err)
		}
	}

	var holds int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &holds); err != nil {
		t.Fatal(err)
	}
	if holds != 0 {
		t.Fatalf("fixture unexpectedly has %d source holds", holds)
	}
	settled, err := repo.SettleFirstAbsentPublication(ctx, f.guard, witness, true)
	if err != nil || settled {
		t.Fatalf("settle without source hold = %t, %v; want false, nil", settled, err)
	}
	var state, stage string
	if err := f.db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state, &stage); err != nil {
		t.Fatal(err)
	}
	if state != "running" || stage != "committed" {
		t.Fatalf("operation after rejected settlement = %s/%s, want running/committed", state, stage)
	}
	remaining, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || remaining == nil || remaining.Phase != "committed" {
		t.Fatalf("publication attempt after rejected settlement = %#v, %v; want committed attempt retained", remaining, err)
	}
}
