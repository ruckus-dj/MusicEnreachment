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

func TestAdmitPublicationSourceCleanupExplicitRetryBindsOwnerReturnedEvidencePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, _, originalOperationID := failedPublicationSourceCleanupForRetry(t)
	inserter := new(publicationSourceCleanupTestInserter)
	before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID)
	var riverState string
	if err := fixture.db.NewRaw(`SELECT j.state FROM river_job j JOIN operation o ON o.river_job_id=j.id WHERE o.id=?`, originalOperationID).Scan(ctx, &riverState); err != nil || riverState != "running" {
		t.Fatalf("original job state before retry=%q err=%v; expected owner-returned but still-running job", riverState, err)
	}
	result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, inserter)
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("explicit retry admission = %+v, %v", result, err)
	}
	if got := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID); got != before {
		t.Fatalf("admission rewrote original retained evidence:\nbefore %s\nafter  %s", before, got)
	}
	var kind string
	var argsOnlyOperationID bool
	if err := fixture.db.NewRaw(`SELECT kind,args::jsonb=jsonb_build_object('operation_id',?) FROM river_job WHERE id=(SELECT river_job_id FROM operation WHERE id=?)`, result.OperationID, result.OperationID).Scan(ctx, &kind, &argsOnlyOperationID); err != nil {
		t.Fatal(err)
	}
	if kind != "retry_publication_sources_v1" || !argsOnlyOperationID {
		t.Fatalf("retry job kind/args shape = %q/only operation id:%t", kind, argsOnlyOperationID)
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
		t.Fatalf("duplicate retry = %+v, %v", duplicate, err)
	}
}

func TestAdmitPublicationSourceCleanupExplicitRetryBlocksUnreturnedOrChangedEvidencePostgreSQL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(context.Context, publicationExecutionFixture, PublicationSourceCleanupDeliveryGuard, uuid.UUID) error
	}{
		{name: "claim still executing", mutate: func(context.Context, publicationExecutionFixture, PublicationSourceCleanupDeliveryGuard, uuid.UUID) error {
			return nil
		}},
		{name: "changed source facts", mutate: func(ctx context.Context, fixture publicationExecutionFixture, _ PublicationSourceCleanupDeliveryGuard, operationID uuid.UUID) error {
			_, err := fixture.db.NewRaw(`UPDATE source_location SET size_bytes=size_bytes+1 WHERE id=(SELECT source_location_id FROM source_cleanup_owner WHERE operation_id=? LIMIT 1)`, operationID).Exec(ctx)
			return err
		}},
		{name: "changed borrower links", mutate: func(ctx context.Context, fixture publicationExecutionFixture, _ PublicationSourceCleanupDeliveryGuard, operationID uuid.UUID) error {
			_, err := fixture.db.NewRaw(`UPDATE track_source SET selected=NOT selected WHERE media_variant_id=(SELECT (source_snapshot->>'media_variant_id')::uuid FROM source_cleanup_item WHERE operation_id=? LIMIT 1)`, operationID).Exec(ctx)
			return err
		}},
		{name: "disabled permission", mutate: func(ctx context.Context, fixture publicationExecutionFixture, _ PublicationSourceCleanupDeliveryGuard, _ uuid.UUID) error {
			_, err := fixture.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(ctx)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, fixture, repository, guard, originalOperationID, _, _ := setupStartedCleanupRemoval(t)
			if tc.name != "claim still executing" {
				if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, mustCleanupItemID(t, ctx, fixture, originalOperationID), func(context.Context, PublicationSourceCleanupRemovalRequest) error { return errors.New("uncertain") }); outcome != PublicationSourceCleanupRemovalBlocked || err == nil {
					t.Fatalf("create uncertain frontier = %v, %v", outcome, err)
				}
				failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, guard, "source removal outcome is unknown")
				if err != nil || !failed {
					t.Fatalf("record owner return = %v, %v", failed, err)
				}
			}
			if err := tc.mutate(ctx, fixture, guard, originalOperationID); err != nil {
				t.Fatal(err)
			}
			result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, new(publicationSourceCleanupTestInserter))
			if err != nil || result.OperationID != uuid.Nil || len(result.Blockers) == 0 {
				t.Fatalf("blocked retry = %+v, %v", result, err)
			}
		})
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
