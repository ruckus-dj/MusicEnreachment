//go:build integration

package persistence

import (
	"context"
	"testing"
)

func TestPublicationPreparationCleanupFailureSettlesDeliveryPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	ctx := context.Background()
	if got, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || got != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", got, err)
	}
	preparation := publicationPreparationFixtureRecord(f.guard)
	preparation.PrivateDirRel = "private-op"
	preparation.InputRel = "private-op/input.flac"
	preparation.OutputRel = "private-op/output.flac"
	if got, err := repo.RecordPublicationPreparation(ctx, f.guard, preparation); err != nil || got != PublicationPreparationRecorded {
		t.Fatalf("record intent = %q, %v", got, err)
	}
	directory := preparation
	directory.Step = "directory_created"
	directory.DirectoryIdentity = &PublicationDirectoryIdentity{Platform: preparation.ParentIdentity.Platform, Volume: preparation.ParentIdentity.Volume, FileID: "private-99"}
	if got, err := repo.RecordPublicationPreparation(ctx, f.guard, directory); err != nil || got != PublicationPreparationRecorded {
		t.Fatalf("record directory = %q, %v", got, err)
	}
	witness := PublicationPreparationCleanupWitness{Preparation: directory}
	if got, err := repo.BeginPublicationPreparationCleanup(ctx, f.guard, witness); err != nil || got != PublicationPreparationCleanupRecorded {
		t.Fatalf("begin cleanup = %q, %v", got, err)
	}
	if got, err := repo.RecordPublicationPreparationCleanupResult(ctx, f.guard, witness); err != nil || got != PublicationPreparationCleanupRecorded {
		t.Fatalf("record cleanup result = %q, %v", got, err)
	}
	settled, err := repo.SettlePublicationPreparationFailure(ctx, f.guard, witness, true)
	if err != nil || !settled {
		t.Fatalf("settle = %t, %v", settled, err)
	}
	var state, stage, safeError string
	if err := f.db.NewRaw(`SELECT state,stage,safe_error FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state, &stage, &safeError); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || stage != "publication_preparation" || safeError != "Publication preparation failed before input creation; its empty private directory was removed." {
		t.Fatalf("operation = %q/%q error %q", state, stage, safeError)
	}
	for _, check := range []struct {
		name  string
		query string
	}{
		{"attempt", `SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`},
		{"claim", `SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`},
		{"source hold", `SELECT count(*) FROM publication_source_hold WHERE operation_id=?`},
		{"tool hold", `SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`},
		{"reservation", `SELECT count(*) FROM publication_namespace_reservation WHERE position_id=? AND generation=?`},
	} {
		var count int
		args := []any{f.guard.OperationID}
		if check.name == "reservation" {
			args = []any{f.guard.ExpectedPositionID, f.guard.CandidateGeneration}
		}
		if err := f.db.NewRaw(check.query, args...).Scan(ctx, &count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 0 {
			t.Errorf("%s rows remain: %d", check.name, count)
		}
	}
}
