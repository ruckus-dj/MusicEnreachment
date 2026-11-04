//go:build integration

package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func failedPublicationSourceCleanupForRetry(t *testing.T) (context.Context, publicationExecutionFixture, *SetupManagerRepository, PublicationSourceCleanupDeliveryGuard, uuid.UUID) {
	t.Helper()
	ctx, fixture, repository, guard, operationID, itemID, _ := setupStartedCleanupRemoval(t)
	removeErr := errors.New("unlink result is uncertain")
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return removeErr }); outcome != PublicationSourceCleanupRemovalBlocked || !errors.Is(err, removeErr) {
		t.Fatalf("persist started failure frontier = %v, %v", outcome, err)
	}
	failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, guard, "source removal outcome is unknown")
	if err != nil || !failed {
		t.Fatalf("owner-returned failure transition = %v, %v", failed, err)
	}
	return ctx, fixture, repository, guard, operationID
}

func TestAdmitPublicationSourceCleanupExplicitRetryRejectsThenBindsOwnerReturnedEvidencePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, guard, originalOperationID, _, _ := setupStartedCleanupRemoval(t)
	assertBlocked := func(name string) {
		t.Helper()
		before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID)
		result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, new(publicationSourceCleanupTestInserter))
		if err != nil || result.OperationID != uuid.Nil || len(result.Blockers) == 0 {
			t.Fatalf("%s blocked retry = %+v, %v", name, result, err)
		}
		if after := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID); after != before {
			t.Fatalf("%s rejection changed retained evidence:\nbefore %s\nafter  %s", name, before, after)
		}
		var grants int
		if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_explicit_retry WHERE original_operation_id=?`, originalOperationID).Scan(ctx, &grants); err != nil || grants != 0 {
			t.Fatalf("%s rejection left %d retry grants: %v", name, grants, err)
		}
	}
	// The executing claim independently blocks admission before native completion
	// or owner return is recorded.
	assertBlocked("claim still executing")
	itemID := mustCleanupItemID(t, ctx, fixture, originalOperationID)
	uncertain := errors.New("uncertain native result")
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, itemID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return uncertain }); outcome != PublicationSourceCleanupRemovalBlocked || !errors.Is(err, uncertain) {
		t.Fatalf("persist uncertain removal=%s,%v", outcome, err)
	}
	if failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, guard, "source removal outcome is unknown"); err != nil || !failed {
		t.Fatalf("record owner return=%t,%v", failed, err)
	}
	before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID)
	var locationID uuid.UUID
	var originalSize int64
	if err := fixture.db.NewRaw(`SELECT l.id,l.size_bytes FROM source_location l JOIN source_cleanup_owner o ON o.source_location_id=l.id WHERE o.operation_id=? ORDER BY l.id LIMIT 1`, originalOperationID).Scan(ctx, &locationID, &originalSize); err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.db.NewRaw(`UPDATE source_location SET size_bytes=size_bytes+1 WHERE id=?`, locationID).Exec(ctx); err != nil {
		t.Fatal(err)
	} else if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("mutate source facts affected %d rows: %v", rows, err)
	}
	assertBlocked("changed source facts")
	if result, err := fixture.db.NewRaw(`UPDATE source_location SET size_bytes=? WHERE id=?`, originalSize, locationID).Exec(ctx); err != nil {
		t.Fatal(err)
	} else if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("restore source facts affected %d rows: %v", rows, err)
	}
	var mediaVariantID uuid.UUID
	if err := fixture.db.NewRaw(`SELECT (source_snapshot->>'media_variant_id')::uuid FROM source_cleanup_item WHERE id=?`, itemID).Scan(ctx, &mediaVariantID); err != nil {
		t.Fatal(err)
	}
	var links []struct {
		ID       uuid.UUID `bun:"id"`
		Selected bool      `bun:"selected"`
	}
	if err := fixture.db.NewRaw(`SELECT id,selected FROM track_source WHERE media_variant_id=? ORDER BY id`, mediaVariantID).Scan(ctx, &links); err != nil || len(links) == 0 {
		t.Fatalf("read borrower links=%d: %v", len(links), err)
	}
	if result, err := fixture.db.NewRaw(`UPDATE track_source SET selected=NOT selected WHERE media_variant_id=?`, mediaVariantID).Exec(ctx); err != nil {
		t.Fatal(err)
	} else if rows, err := result.RowsAffected(); err != nil || rows != int64(len(links)) {
		t.Fatalf("mutate borrower links affected %d rows, want %d: %v", rows, len(links), err)
	}
	assertBlocked("changed borrower links")
	for _, link := range links {
		result, err := fixture.db.NewRaw(`UPDATE track_source SET selected=? WHERE id=?`, link.Selected, link.ID).Exec(ctx)
		if err != nil {
			t.Fatalf("restore borrower link %s: %v", link.ID, err)
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			t.Fatalf("restore borrower link %s affected %d rows: %v", link.ID, rows, err)
		}
	}
	var permission string
	if err := fixture.db.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(ctx, &permission); err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(ctx); err != nil {
		t.Fatal(err)
	} else if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("disable cleanup permission affected %d rows: %v", rows, err)
	}
	assertBlocked("disabled permission")
	if result, err := fixture.db.NewRaw(`UPDATE app_setting SET setting_value=? WHERE setting_name='publication_remove_sources'`, permission).Exec(ctx); err != nil {
		t.Fatal(err)
	} else if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("restore cleanup permission affected %d rows: %v", rows, err)
	}
	if got := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID); got != before {
		t.Fatalf("restored rejection baseline differs:\nbefore %s\nafter  %s", before, got)
	}
	var riverState string
	if err := fixture.db.NewRaw(`SELECT j.state FROM river_job j JOIN operation o ON o.river_job_id=j.id WHERE o.id=?`, originalOperationID).Scan(ctx, &riverState); err != nil || riverState != "running" {
		t.Fatalf("original River job state=%q err=%v; want retained running row", riverState, err)
	}
	inserter := new(publicationSourceCleanupTestInserter)
	result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, inserter)
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("valid explicit retry admission=%+v,%v", result, err)
	}
	if got := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID); got != before {
		t.Fatalf("admission rewrote retained original evidence:\nbefore %s\nafter  %s", before, got)
	}
	var kind string
	var argsOnlyOperationID bool
	if err := fixture.db.NewRaw(`SELECT kind,args::jsonb=jsonb_build_object('operation_id',?) FROM river_job WHERE id=(SELECT river_job_id FROM operation WHERE id=?)`, result.OperationID, result.OperationID).Scan(ctx, &kind, &argsOnlyOperationID); err != nil {
		t.Fatal(err)
	}
	if kind != "retry_publication_sources_v1" || !argsOnlyOperationID {
		t.Fatalf("retry job kind/args shape=%q/only operation id:%t", kind, argsOnlyOperationID)
	}
	var grants, boundItems int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, result.OperationID).Scan(ctx, &grants); err != nil || grants != 1 {
		t.Fatalf("retry grants=%d err=%v", grants, err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_explicit_retry_item WHERE retry_id=(SELECT id FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?)`, result.OperationID).Scan(ctx, &boundItems); err != nil || boundItems != 1 {
		t.Fatalf("bound retained items=%d err=%v", boundItems, err)
	}
	var bindingOK bool
	if err := fixture.db.NewRaw(`SELECT binding->>'original_claim_state'='recovery_required' AND binding->>'original_river_kind'='cleanup_publication_sources_v1' AND binding->>'original_river_attempt'=(SELECT river_attempt::text FROM publication_execution_claim WHERE operation_id=?) FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, originalOperationID, result.OperationID).Scan(ctx, &bindingOK); err != nil || !bindingOK {
		t.Fatalf("retry grant binding=%t err=%v", bindingOK, err)
	}
	duplicate, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, new(publicationSourceCleanupTestInserter))
	if err != nil || duplicate.OperationID != uuid.Nil || len(duplicate.Blockers) == 0 {
		t.Fatalf("duplicate retry=%+v,%v", duplicate, err)
	}
}

func TestAdmitPublicationSourceCleanupExplicitRetryRollsBackActualRiverInsertErrorPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, _, originalOperationID := failedPublicationSourceCleanupForRetry(t)
	var before int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='retry_publication_sources_v1'`).Scan(ctx, &before); err != nil {
		t.Fatal(err)
	}
	inserter := &publicationSourceCleanupTestInserter{err: errors.New("injected after actual River insert")}
	result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, inserter)
	if err == nil || result.OperationID != uuid.Nil || inserter.args == nil {
		t.Fatalf("injected actual River insert failure = %+v,%v args=%#v", result, err, inserter.args)
	}
	var after, grants, operations int
	for query, destination := range map[string]*int{
		`SELECT count(*) FROM river_job WHERE kind='retry_publication_sources_v1'`: &after,
		`SELECT count(*) FROM source_cleanup_explicit_retry`:                       &grants,
		`SELECT count(*) FROM operation WHERE kind='retry_publication_sources_v1'`: &operations,
	} {
		if err := fixture.db.NewRaw(query).Scan(ctx, destination); err != nil {
			t.Fatal(err)
		}
	}
	if after != before || grants != 0 || operations != 0 {
		t.Fatalf("failed admission left river jobs=%d grants=%d recovery operations=%d (prior jobs=%d)", after, grants, operations, before)
	}
}

func cleanupRetryRetainedEvidence(t *testing.T, ctx context.Context, fixture publicationExecutionFixture, operationID uuid.UUID) string {
	t.Helper()
	var value string
	const query = `SELECT jsonb_build_object(
		'operation',(SELECT to_jsonb(o) FROM operation o WHERE id=?),
		'items',(SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY id),'[]') FROM source_cleanup_item i WHERE operation_id=?),
		'owners',(SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY item_id),'[]') FROM source_cleanup_owner o WHERE operation_id=?),
		'claims',(SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY application_attempt),'[]') FROM publication_execution_claim c WHERE operation_id=?))::text`
	if err := fixture.db.NewRaw(query, operationID, operationID, operationID, operationID).Scan(ctx, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func mustCleanupItemID(t *testing.T, ctx context.Context, fixture publicationExecutionFixture, operationID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := fixture.db.NewRaw(`SELECT id FROM source_cleanup_item WHERE operation_id=? ORDER BY id LIMIT 1`, operationID).Scan(ctx, &id); err != nil {
		t.Fatal(err)
	}
	return id
}
