//go:build integration

package persistence

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPublicationDeliverySupportRequiresExistingHoldAndLiveGuardPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	got, found, err := repo.PublicationSourceHoldForDelivery(context.Background(), f.guard)
	if err != nil || found || got != uuid.Nil {
		t.Fatalf("missing hold = %s, %v, %v", got, found, err)
	}
	stale := f.guard
	stale.DeliveryToken = uuid.NewString()
	got, found, err = repo.PublicationSourceHoldForDelivery(context.Background(), stale)
	if err != nil || found || got != uuid.Nil {
		t.Fatalf("stale hold lookup = %s, %v, %v", got, found, err)
	}
}

func TestPublicationDeliverySupportReturnsExistingHoldForLiveGuardPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	_, _, _, operationID := pubSourceProtectionHoldWithFixture(t, f)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	var persistedHoldUUID uuid.UUID
	if err := f.db.NewRaw(`SELECT hold_uuid FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(context.Background(), &persistedHoldUUID); err != nil {
		t.Fatal(err)
	}
	got, found, err := repo.PublicationSourceHoldForDelivery(context.Background(), f.guard)
	if err != nil || !found || got != persistedHoldUUID {
		t.Fatalf("existing hold = %s, %v, %v; want persisted UUID %s", got, found, err, persistedHoldUUID)
	}
	var holdCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(context.Background(), &holdCount); err != nil {
		t.Fatal(err)
	}
	if holdCount != 1 {
		t.Fatalf("hold count = %d, want existing hold only", holdCount)
	}
	for name, stale := range map[string]PublicationDeliveryGuard{
		"claim UUID": func() PublicationDeliveryGuard { guard := f.guard; guard.ClaimUUID = uuid.New(); return guard }(),
		"job ID":     func() PublicationDeliveryGuard { guard := f.guard; guard.RiverJobID++; return guard }(),
		"attempt":    func() PublicationDeliveryGuard { guard := f.guard; guard.RiverAttempt++; return guard }(),
	} {
		if staleUUID, staleFound, staleErr := repo.PublicationSourceHoldForDelivery(context.Background(), stale); staleErr != nil || staleFound || staleUUID != uuid.Nil {
			t.Errorf("stale %s lookup = %s, %v, %v", name, staleUUID, staleFound, staleErr)
		}
	}
	var holdUUIDAfter uuid.UUID
	if err := f.db.NewRaw(`SELECT hold_uuid FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(context.Background(), &holdUUIDAfter); err != nil {
		t.Fatal(err)
	}
	if holdUUIDAfter != persistedHoldUUID {
		t.Fatalf("stale lookup changed hold UUID: got %s, want %s", holdUUIDAfter, persistedHoldUUID)
	}
}

func TestPublicationAttemptCoverInputReadRequiresLiveClaimedDeliveryPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
	var releaseID uuid.UUID
	if err := f.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_attempt_cover_input(operation_id,position_id,release_id,authority_revision,authority_mode,state) VALUES (?,?,?,1,'automatic','absent')`, f.guard.OperationID, f.guard.ExpectedPositionID, releaseID); err != nil {
		t.Fatal(err)
	}
	repo := NewSetupManagerRepository(f.db)
	if _, found, err := repo.PublicationAttemptCoverInputForDelivery(ctx, f.guard); err != nil || found {
		t.Fatalf("unclaimed receipt read = found %t, %v", found, err)
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	input, found, err := repo.PublicationAttemptCoverInputForDelivery(ctx, f.guard)
	if err != nil || !found || input.State != "absent" || input.OperationID != f.guard.OperationID {
		t.Fatalf("claimed receipt read = %+v, %t, %v", input, found, err)
	}
	stale := f.guard
	stale.DeliveryToken = uuid.NewString()
	if _, found, err := repo.PublicationAttemptCoverInputForDelivery(ctx, stale); err != nil || found {
		t.Fatalf("stale receipt read = found %t, %v", found, err)
	}
}

func TestGuardedPublicationDeliveryDiagnosticOnlyUpdatesSafeErrorPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	beforeAttempt, err := repo.ReadPublicationAttempt(context.Background(), f.guard.OperationID)
	if err != nil || beforeAttempt == nil {
		t.Fatalf("read publication attempt before diagnostic = %v, %v", beforeAttempt, err)
	}
	var stateBefore string
	if err := f.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.guard.OperationID).Scan(context.Background(), &stateBefore); err != nil {
		t.Fatal(err)
	}
	var holdsBefore int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &holdsBefore); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.GuardedPublicationDeliveryDiagnostic(context.Background(), f.guard, "The publication could not be completed.")
	if err != nil || !changed {
		t.Fatalf("diagnostic = %v, %v", changed, err)
	}
	var state, safeError string
	var attempt int
	if err := f.db.NewRaw(`SELECT state, safe_error, attempt FROM operation WHERE id=?`, f.guard.OperationID).Scan(context.Background(), &state, &safeError, &attempt); err != nil {
		t.Fatal(err)
	}
	if state != stateBefore || state != "running" || safeError != "The publication could not be completed." || attempt != f.guard.ApplicationAttempt {
		t.Fatalf("operation after diagnostic: state=%q safe_error=%q attempt=%d", state, safeError, attempt)
	}
	if changed, err := repo.GuardedPublicationDeliveryDiagnostic(context.Background(), f.guard, " \t"); err == nil || changed {
		t.Fatalf("empty diagnostic = %v, %v", changed, err)
	}
	stale := f.guard
	stale.DeliveryToken = uuid.NewString()
	if changed, err := repo.GuardedPublicationDeliveryDiagnostic(context.Background(), stale, "A later safe failure."); err != nil || changed {
		t.Fatalf("stale diagnostic = %v, %v", changed, err)
	}
	var finalState, finalError string
	var finalAttempt int
	if err := f.db.NewRaw(`SELECT state, safe_error, attempt FROM operation WHERE id=?`, f.guard.OperationID).Scan(context.Background(), &finalState, &finalError, &finalAttempt); err != nil {
		t.Fatal(err)
	}
	if finalState != state || finalError != safeError || finalAttempt != attempt {
		t.Fatalf("stale guard changed operation: state=%q error=%q attempt=%d", finalState, finalError, finalAttempt)
	}
	afterAttempt, err := repo.ReadPublicationAttempt(context.Background(), f.guard.OperationID)
	if err != nil || afterAttempt == nil {
		t.Fatalf("read publication attempt after diagnostic = %v, %v", afterAttempt, err)
	}
	if afterAttempt.Phase != beforeAttempt.Phase || !bytes.Equal(afterAttempt.FrozenInputs, beforeAttempt.FrozenInputs) {
		t.Fatalf("diagnostic changed publication attempt: phase=%q (before %q), frozen inputs changed=%v", afterAttempt.Phase, beforeAttempt.Phase, !bytes.Equal(afterAttempt.FrozenInputs, beforeAttempt.FrozenInputs))
	}
	var holdsAfter int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &holdsAfter); err != nil {
		t.Fatal(err)
	}
	if holdsAfter != holdsBefore {
		t.Fatalf("diagnostic changed current source holds: count=%d (before %d)", holdsAfter, holdsBefore)
	}
}
