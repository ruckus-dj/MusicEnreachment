//go:build integration

package persistence

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func admittedRunningCleanupRetry(t *testing.T) (context.Context, publicationExecutionFixture, *SetupManagerRepository, uuid.UUID, PublicationSourceCleanupDeliveryGuard) {
	t.Helper()
	return admittedRunningCleanupRetryWithDatabase(t, nil, "")
}

func admittedRunningCleanupRetryWithDatabase(t *testing.T, db *bun.DB, scope string) (context.Context, publicationExecutionFixture, *SetupManagerRepository, uuid.UUID, PublicationSourceCleanupDeliveryGuard) {
	t.Helper()
	ctx, fixture, repository, _, originalID := failedPublicationSourceCleanupForRetryWithDatabase(t, db, scope)
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

func testClaimPublicationSourceCleanupExplicitRetryDeliveryAcquiresAndReplaysPostgreSQL(t *testing.T, db *bun.DB) {
	scope := uuid.NewString()
	preservePublicationRemoveSourcesSetting(t, db)
	ctx, fixture, repository, originalID, guard := admittedRunningCleanupRetryWithDatabase(t, db, scope)
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

func testClaimPublicationSourceCleanupExplicitRetryDeliveryConcurrentPostgreSQL(t *testing.T, db *bun.DB) {
	scope := uuid.NewString()
	preservePublicationRemoveSourcesSetting(t, db)
	ctx, _, repository, _, guard := admittedRunningCleanupRetryWithDatabase(t, db, scope)
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

func testClaimPublicationSourceCleanupExplicitRetryDeliveryRejectsStaleBindingsPostgreSQL(t *testing.T, db *bun.DB) {
	scope := uuid.NewString()
	preservePublicationRemoveSourcesSetting(t, db)
	ctx, fixture, repository, originalID, guard := admittedRunningCleanupRetryWithDatabase(t, db, scope)
	before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalID)
	assertRejected := func(name string, stale PublicationSourceCleanupDeliveryGuard) {
		t.Helper()
		if outcome, err := repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, stale); err != nil || outcome != PublicationDeliveryBlocked {
			t.Fatalf("%s stale retry claim=%s,%v", name, outcome, err)
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
			t.Fatalf("%s stale claim mutated grant=%+v claim count=%d", name, grant, claims)
		}
		if got := cleanupRetryRetainedEvidence(t, ctx, fixture, originalID); got != before {
			t.Fatalf("%s rejected claim changed original evidence:\nbefore %s\nafter  %s", name, before, got)
		}
	}
	for _, alter := range []struct {
		name   string
		change func(*PublicationSourceCleanupDeliveryGuard)
	}{
		{"wrong position", func(g *PublicationSourceCleanupDeliveryGuard) { g.PositionID = uuid.New() }},
		{"wrong epoch", func(g *PublicationSourceCleanupDeliveryGuard) { g.Epoch = uuid.New() }},
		{"wrong actual attempt", func(g *PublicationSourceCleanupDeliveryGuard) { g.RiverAttempt++ }},
		{"old claim uuid", func(g *PublicationSourceCleanupDeliveryGuard) {
			var oldClaim uuid.UUID
			if err := fixture.db.NewRaw(`SELECT original_claim_uuid FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, guard.OperationID).Scan(ctx, &oldClaim); err != nil {
				t.Fatal(err)
			}
			g.ClaimUUID = oldClaim
		}},
	} {
		stale := guard
		alter.change(&stale)
		assertRejected(alter.name, stale)
	}
	var setting string
	if err := fixture.db.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(ctx, &setting); err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(ctx); err != nil {
		t.Fatal(err)
	} else if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("disable source-removal permission affected %d rows: %v", rows, err)
	}
	assertRejected("permission disabled", guard)
	if result, err := fixture.db.NewRaw(`UPDATE app_setting SET setting_value=? WHERE setting_name='publication_remove_sources'`, setting).Exec(ctx); err != nil {
		t.Fatal(err)
	} else if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("restore source-removal permission affected %d rows: %v", rows, err)
	}
	var locationID uuid.UUID
	var size int64
	if err := fixture.db.NewRaw(`SELECT l.id,l.size_bytes FROM source_location l JOIN source_cleanup_owner o ON o.source_location_id=l.id WHERE o.operation_id=? ORDER BY l.id LIMIT 1`, originalID).Scan(ctx, &locationID, &size); err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.db.NewRaw(`UPDATE source_location SET size_bytes=size_bytes+1 WHERE id=?`, locationID).Exec(ctx); err != nil {
		t.Fatal(err)
	} else if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("mutate source fact affected %d rows: %v", rows, err)
	}
	assertRejected("changed source facts", guard)
	if result, err := fixture.db.NewRaw(`UPDATE source_location SET size_bytes=? WHERE id=?`, size, locationID).Exec(ctx); err != nil {
		t.Fatal(err)
	} else if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("restore source fact affected %d rows: %v", rows, err)
	}
	if outcome, err := repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim restored valid baseline=%s,%v", outcome, err)
	}
}

func testClaimPublicationSourceCleanupExplicitRetryDeliveryCommitFailureRollsBackPostgreSQL(t *testing.T, db *bun.DB) {
	scope := uuid.NewString()
	preservePublicationRemoveSourcesSetting(t, db)
	ctx, fixture, repository, originalID, guard := admittedRunningCleanupRetryWithDatabase(t, db, scope)
	before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalID)
	t.Cleanup(func() {
		_, _ = fixture.db.ExecContext(ctx, `DROP TRIGGER IF EXISTS reject_retry_start_commit ON operation; DROP FUNCTION IF EXISTS reject_retry_start_commit()`)
	})
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
