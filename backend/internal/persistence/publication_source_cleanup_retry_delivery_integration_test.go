//go:build integration

package persistence

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func admittedRunningCleanupRetry(t *testing.T) (context.Context, publicationExecutionFixture, *SetupManagerRepository, uuid.UUID, PublicationSourceCleanupDeliveryGuard) {
	t.Helper()
	ctx, fixture, repository, _, originalID := failedPublicationSourceCleanupForRetry(t)
	result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalID, new(publicationSourceCleanupTestInserter))
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("admit explicit retry: %+v, %v", result, err)
	}
	var jobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, result.OperationID).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var grant SourceCleanupExplicitRetry
	if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", result.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, fixture, repository, originalID, PublicationSourceCleanupDeliveryGuard{
		OperationID: result.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1,
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "explicit-retry-delivery", Epoch: grant.Epoch, PositionID: grant.PositionID,
	}
}

func TestClaimPublicationSourceCleanupExplicitRetryDeliveryAcquiresAndReplaysPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, originalID, guard := admittedRunningCleanupRetry(t)
	before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalID)
	if outcome, err := repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim retry delivery=%s,%v", outcome, err)
	}
	if got := cleanupRetryRetainedEvidence(t, ctx, fixture, originalID); got != before {
		t.Fatalf("claim rewrote original evidence:\nbefore %s\nafter  %s", before, got)
	}
	var grant SourceCleanupExplicitRetry
	if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var claim PublicationExecutionClaim
	if err := fixture.db.NewSelect().Model(&claim).Where("operation_id=?", guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var operation Operation
	if err := fixture.db.NewSelect().Model(&operation).Where("id=?", guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if grant.ConsumedApplicationAttempt == nil || *grant.ConsumedApplicationAttempt != guard.ApplicationAttempt || grant.ConsumedRiverJobID == nil || *grant.ConsumedRiverJobID != guard.RiverJobID || grant.ConsumedClaimUUID == nil || *grant.ConsumedClaimUUID != guard.ClaimUUID || claim.State != "executing" || !publicationSourceCleanupClaimMatches(claim, guard) || operation.State != "running" || operation.Stage != "cleanup_retry_running" {
		t.Fatalf("acquired retry tuple incomplete: grant=%+v claim=%+v operation=%+v", grant, claim, operation)
	}
	if outcome, err := repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, guard); err != nil || outcome != PublicationDeliveryAlreadyOwned {
		t.Fatalf("same retry delivery replay=%s,%v", outcome, err)
	}
	duplicate := guard
	duplicate.ClaimUUID = uuid.New()
	duplicate.DeliveryToken = "different-delivery"
	if outcome, err := repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, duplicate); err != nil || outcome != PublicationDeliveryBlocked {
		t.Fatalf("fresh retry delivery after consumption=%s,%v", outcome, err)
	}
	var claimCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, guard.OperationID).Scan(ctx, &claimCount); err != nil || claimCount != 1 {
		t.Fatalf("retry claims=%d err=%v", claimCount, err)
	}
}

func TestClaimPublicationSourceCleanupExplicitRetryDeliveryConcurrentPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, _, repository, _, guard := admittedRunningCleanupRetry(t)
	type result struct {
		outcome PublicationDeliveryClaimOutcome
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			outcome, err := repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, guard)
			results <- result{outcome, err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	acquired, replay := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		switch result.outcome {
		case PublicationDeliveryAcquired:
			acquired++
		case PublicationDeliveryAlreadyOwned:
			replay++
		default:
			t.Fatalf("unexpected concurrent claim outcome %q", result.outcome)
		}
	}
	if acquired != 1 || replay != 1 {
		t.Fatalf("concurrent claim outcomes acquired=%d replay=%d", acquired, replay)
	}
}

func TestClaimPublicationSourceCleanupExplicitRetryDeliveryRejectsStaleBindingsPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"setting off", "changed source", "wrong position", "wrong epoch", "wrong actual attempt", "old claim uuid"} {
		t.Run(name, func(t *testing.T) {
			ctx, fixture, repository, originalID, guard := admittedRunningCleanupRetry(t)
			before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalID)
			switch name {
			case "setting off":
				_, err := fixture.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(ctx)
				if err != nil {
					t.Fatal(err)
				}
			case "changed source":
				_, err := fixture.db.NewRaw(`UPDATE source_location SET size_bytes=size_bytes+1 WHERE id=(SELECT source_location_id FROM source_cleanup_owner WHERE operation_id=? LIMIT 1)`, originalID).Exec(ctx)
				if err != nil {
					t.Fatal(err)
				}
			case "wrong position":
				guard.PositionID = uuid.New()
			case "wrong epoch":
				guard.Epoch = uuid.New()
			case "wrong actual attempt":
				guard.RiverAttempt = 2
			case "old claim uuid":
				var oldClaim uuid.UUID
				if err := fixture.db.NewRaw(`SELECT original_claim_uuid FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, guard.OperationID).Scan(ctx, &oldClaim); err != nil {
					t.Fatal(err)
				}
				guard.ClaimUUID = oldClaim
			}
			if outcome, err := repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, guard); err != nil || outcome != PublicationDeliveryBlocked {
				t.Fatalf("stale retry claim=%s,%v", outcome, err)
			}
			var grant SourceCleanupExplicitRetry
			if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", guard.OperationID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			var claims int
			if err := fixture.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, guard.OperationID).Scan(ctx, &claims); err != nil {
				t.Fatal(err)
			}
			if grant.ConsumedApplicationAttempt != nil || grant.ConsumedRiverJobID != nil || grant.ConsumedClaimUUID != nil || claims != 0 {
				t.Fatalf("stale claim mutated grant=%+v claim count=%d", grant, claims)
			}
			if got := cleanupRetryRetainedEvidence(t, ctx, fixture, originalID); got != before {
				t.Fatalf("rejected claim changed original evidence:\nbefore %s\nafter  %s", before, got)
			}
		})
	}
}

func TestClaimPublicationSourceCleanupExplicitRetryDeliveryCommitFailureRollsBackPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, originalID, guard := admittedRunningCleanupRetry(t)
	before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalID)
	if _, err := fixture.db.ExecContext(ctx, `
		CREATE FUNCTION reject_retry_start_commit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.kind='retry_publication_sources_v1' AND NEW.state='running' THEN RAISE EXCEPTION 'injected retry start commit failure'; END IF;
			RETURN NEW;
		END $$;
		CREATE CONSTRAINT TRIGGER reject_retry_start_commit AFTER UPDATE ON operation
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_retry_start_commit()`); err != nil {
		t.Fatal(err)
	}
	outcome, err := repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, guard)
	if outcome != PublicationDeliveryBlocked || err == nil || !strings.Contains(err.Error(), "injected retry start commit failure") {
		t.Fatalf("commit failure outcome=%s err=%v", outcome, err)
	}
	if got := cleanupRetryRetainedEvidence(t, ctx, fixture, originalID); got != before {
		t.Fatalf("commit failure changed original evidence:\nbefore %s\nafter  %s", before, got)
	}
	var grant SourceCleanupExplicitRetry
	if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var operation Operation
	if err := fixture.db.NewSelect().Model(&operation).Where("id=?", guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var claims int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, guard.OperationID).Scan(ctx, &claims); err != nil {
		t.Fatal(err)
	}
	if grant.ConsumedApplicationAttempt != nil || grant.ConsumedRiverJobID != nil || grant.ConsumedClaimUUID != nil || operation.State != "queued" || claims != 0 {
		t.Fatalf("commit rollback residue grant=%+v operation=%+v claims=%d", grant, operation, claims)
	}
}
