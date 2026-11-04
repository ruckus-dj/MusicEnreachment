//go:build integration

package persistence

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPublicationDeliverySupportLiveClaimAndHoldLifecyclePostgreSQL(t *testing.T) {
	ctx := context.Background()
	var repo *SetupManagerRepository
	invalid := PublicationDeliveryGuard{OperationID: uuid.New()}
	if _, found, err := repo.PublicationSourceHoldForDelivery(ctx, invalid); err == nil || found {
		t.Fatalf("malformed hold guard = found %t, %v", found, err)
	}
	if changed, err := repo.GuardedPublicationDeliveryDiagnostic(ctx, invalid, "safe diagnostic"); err == nil || changed {
		t.Fatalf("malformed diagnostic guard = changed %t, %v", changed, err)
	}

	f := newPublicationExecutionFixture(t)
	repo = NewSetupManagerRepository(f.db)

	var releaseID uuid.UUID
	if err := f.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_attempt_cover_input(operation_id,position_id,release_id,authority_revision,authority_mode,state) VALUES (?,?,?,1,'automatic','absent')`, f.guard.OperationID, f.guard.ExpectedPositionID, releaseID); err != nil {
		t.Fatal(err)
	}
	// A persisted receipt is not observable until this exact delivery is claimed.
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

	// Missing ownership is distinguished from a stale guard before installing a
	// valid source hold on the same database lifecycle.
	if got, found, err := repo.PublicationSourceHoldForDelivery(ctx, f.guard); err != nil || found || got != uuid.Nil {
		t.Fatalf("missing hold = %s, %t, %v", got, found, err)
	}
	stale := f.guard
	stale.DeliveryToken = uuid.NewString()
	if got, found, err := repo.PublicationSourceHoldForDelivery(ctx, stale); err != nil || found || got != uuid.Nil {
		t.Fatalf("stale hold lookup = %s, %t, %v", got, found, err)
	}
	_, _, _, operationID := pubSourceProtectionHoldWithFixture(t, f)
	var persistedHold uuid.UUID
	if err := f.db.NewRaw(`SELECT hold_uuid FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(ctx, &persistedHold); err != nil {
		t.Fatal(err)
	}
	got, found, err := repo.PublicationSourceHoldForDelivery(ctx, f.guard)
	if err != nil || !found || got != persistedHold {
		t.Fatalf("live hold = %s, %t, %v; want %s", got, found, err, persistedHold)
	}
	for name, staleGuard := range map[string]PublicationDeliveryGuard{
		"claim UUID": func() PublicationDeliveryGuard { guard := f.guard; guard.ClaimUUID = uuid.New(); return guard }(),
		"job ID":     func() PublicationDeliveryGuard { guard := f.guard; guard.RiverJobID++; return guard }(),
		"attempt":    func() PublicationDeliveryGuard { guard := f.guard; guard.RiverAttempt++; return guard }(),
		"token": func() PublicationDeliveryGuard {
			guard := f.guard
			guard.DeliveryToken = uuid.NewString()
			return guard
		}(),
	} {
		if staleID, staleFound, staleErr := repo.PublicationSourceHoldForDelivery(ctx, staleGuard); staleErr != nil || staleFound || staleID != uuid.Nil {
			t.Errorf("stale %s hold lookup = %s, %t, %v", name, staleID, staleFound, staleErr)
		}
		if _, staleFound, staleErr := repo.PublicationAttemptCoverInputForDelivery(ctx, staleGuard); staleErr != nil || staleFound {
			t.Errorf("stale %s receipt lookup = found %t, %v", name, staleFound, staleErr)
		}
	}
	var holds int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(ctx, &holds); err != nil || holds != 1 {
		t.Fatalf("hold count after stale reads = %d, %v", holds, err)
	}
	var holdAfter uuid.UUID
	if err := f.db.NewRaw(`SELECT hold_uuid FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(ctx, &holdAfter); err != nil || holdAfter != persistedHold {
		t.Fatalf("stale reads changed hold identity: %s, %v", holdAfter, err)
	}

	beforeAttempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || beforeAttempt == nil {
		t.Fatalf("read attempt before diagnostic = %v, %v", beforeAttempt, err)
	}
	var stateBefore string
	if err := f.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &stateBefore); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.GuardedPublicationDeliveryDiagnostic(ctx, f.guard, "The publication could not be completed.")
	if err != nil || !changed {
		t.Fatalf("diagnostic = %t, %v", changed, err)
	}
	var state, safeError string
	var attemptNumber int
	if err := f.db.NewRaw(`SELECT state,safe_error,attempt FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state, &safeError, &attemptNumber); err != nil {
		t.Fatal(err)
	}
	if state != stateBefore || state != "running" || safeError != "The publication could not be completed." || attemptNumber != f.guard.ApplicationAttempt {
		t.Fatalf("diagnostic altered operation lifecycle: state=%q error=%q attempt=%d", state, safeError, attemptNumber)
	}
	if changed, err := repo.GuardedPublicationDeliveryDiagnostic(ctx, f.guard, " \t"); err == nil || changed {
		t.Fatalf("empty diagnostic = %t, %v", changed, err)
	}
	if changed, err := repo.GuardedPublicationDeliveryDiagnostic(ctx, stale, "A later safe failure."); err != nil || changed {
		t.Fatalf("stale diagnostic = %t, %v", changed, err)
	}
	afterAttempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || afterAttempt == nil {
		t.Fatalf("read attempt after diagnostic = %v, %v", afterAttempt, err)
	}
	if afterAttempt.Phase != beforeAttempt.Phase || !bytes.Equal(afterAttempt.FrozenInputs, beforeAttempt.FrozenInputs) {
		t.Fatalf("diagnostic changed frozen attempt: phase=%q/%q inputsChanged=%t", afterAttempt.Phase, beforeAttempt.Phase, !bytes.Equal(afterAttempt.FrozenInputs, beforeAttempt.FrozenInputs))
	}
	var finalState, finalError string
	var finalAttempt int
	if err := f.db.NewRaw(`SELECT state,safe_error,attempt FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &finalState, &finalError, &finalAttempt); err != nil {
		t.Fatal(err)
	}
	if finalState != state || finalError != safeError || finalAttempt != attemptNumber {
		t.Fatalf("stale diagnostic changed operation: state=%q error=%q attempt=%d", finalState, finalError, finalAttempt)
	}
	var finalHold uuid.UUID
	if err := f.db.NewRaw(`SELECT hold_uuid FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(ctx, &finalHold); err != nil || finalHold != persistedHold {
		t.Fatalf("diagnostic changed hold identity: %s, %v", finalHold, err)
	}
}
