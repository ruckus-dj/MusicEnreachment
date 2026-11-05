//go:build integration

package persistence

import (
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func testReadPublicationSourceCleanupExplicitRetryStatePostgreSQL(t *testing.T, db *bun.DB) {
	scope := uuid.NewString()
	preservePublicationRemoveSourcesSetting(t, db)
	ctx, fixture, repository, _, originalID := failedPublicationSourceCleanupForRetryWithDatabase(t, db, scope)
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
