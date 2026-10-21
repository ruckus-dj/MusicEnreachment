//go:build integration

package persistence

import (
	"testing"

	"github.com/google/uuid"
)

func TestReadPublicationSourceCleanupExplicitRetryStatePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, _, originalID := failedPublicationSourceCleanupForRetry(t)
	state, err := repository.ReadPublicationSourceCleanupExplicitRetryState(ctx, originalID)
	if err != nil {
		t.Fatal(err)
	}
	if state.OperationID != originalID || state.Kind != "cleanup_publication_sources_v1" || state.State != "failed" || state.Stage != "cleanup_recovery_required" || !state.SnapshotValid || state.PositionID == uuid.Nil || state.OutputEpoch == uuid.Nil {
		t.Fatalf("failed original advisory state = %+v", state)
	}
	if state.CurrentGrant || state.RecoveryFrontier {
		t.Fatalf("initial state unexpectedly reports recovery grant/frontier: %+v", state)
	}
	admitted, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalID, new(publicationSourceCleanupTestInserter))
	if err != nil || admitted.OperationID == uuid.Nil || len(admitted.Blockers) != 0 {
		t.Fatalf("admit retry = %+v, %v", admitted, err)
	}
	state, err = repository.ReadPublicationSourceCleanupExplicitRetryState(ctx, originalID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.CurrentGrant || !state.RecoveryGrantValid || state.RecoveryOperationID != admitted.OperationID || state.RecoveryKind != "retry_publication_sources_v1" || state.RecoveryState != "queued" || state.RecoveryStage != "queued" || !state.RecoverySnapshotValid || state.RecoveryClaimState != "" || state.RecoveryFrontier {
		t.Fatalf("queued recovery advisory state = %+v", state)
	}
	// Reading routing metadata must not claim or consume the new grant.
	var consumed int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_explicit_retry WHERE recovery_operation_id=? AND consumed_claim_uuid IS NOT NULL`, admitted.OperationID).Scan(ctx, &consumed); err != nil || consumed != 0 {
		t.Fatalf("selector changed retry grant consumption: count=%d err=%v", consumed, err)
	}
}

func TestReadPublicationSourceCleanupExplicitRetryStateReturnedRecoveryPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, f.newGuard, "retry callback returned for review"); err != nil || !failed {
		t.Fatalf("return consumed retry owner=%t,%v", failed, err)
	}
	state, err := f.repository.ReadPublicationSourceCleanupExplicitRetryState(f.ctx, f.originalID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.CurrentGrant || !state.RecoveryGrantValid || state.RecoveryOperationID != f.newGuard.OperationID || state.RecoveryState != "failed" || state.RecoveryStage != "cleanup_retry_recovery_required" || !state.RecoverySnapshotValid || state.RecoveryClaimState != "recovery_required" || state.RecoveryFrontier {
		t.Fatalf("returned recovery advisory state = %+v", state)
	}
	var consumed, claimState string
	if err := f.fixture.db.NewRaw(`SELECT g.consumed_claim_uuid::text,c.state FROM source_cleanup_explicit_retry g JOIN publication_execution_claim c ON c.operation_id=g.recovery_operation_id AND c.claim_uuid=g.consumed_claim_uuid AND c.application_attempt=g.consumed_application_attempt AND c.river_job_id=g.consumed_river_job_id WHERE g.recovery_operation_id=?`, f.newGuard.OperationID).Scan(f.ctx, &consumed, &claimState); err != nil {
		t.Fatal(err)
	}
	if consumed != f.newGuard.ClaimUUID.String() || claimState != "recovery_required" {
		t.Fatalf("consumed retry claim=%s state=%s; want %s recovery_required", consumed, claimState, f.newGuard.ClaimUUID)
	}
	result, err := f.repository.ReplacePublicationSourceCleanupExplicitRetry(f.ctx, f.originalID, f.newGuard.OperationID, new(publicationSourceCleanupTestInserter))
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("replace returned retry=%+v,%v", result, err)
	}
	var grant SourceCleanupExplicitRetry
	if err := f.fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", result.OperationID).Scan(f.ctx); err != nil {
		t.Fatal(err)
	}
	if grant.FrontierRevision == nil {
		t.Fatalf("replacement grant has no frontier revision: %+v", grant)
	}
	state, err = f.repository.ReadPublicationSourceCleanupExplicitRetryState(f.ctx, f.originalID)
	if err != nil {
		t.Fatal(err)
	}
	if state.RecoveryOperationID != result.OperationID || !state.RecoveryFrontier {
		t.Fatalf("replacement frontier advisory state = %+v; latest operation %s", state, result.OperationID)
	}
}
