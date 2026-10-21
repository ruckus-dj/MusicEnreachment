//go:build integration

package persistence

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Add two additional eligible source graphs using the same persisted identity
// shape as the ordinary multi-file cleanup fixture. These are real source
// locations, work rows, variant identities, and borrower links, not fabricated
// cleanup rows or unbacked UUIDs.
func addCleanupRetrySourceCopies(t *testing.T, ctx context.Context, fixture publicationExecutionFixture, work SourceAnalysisWork, relatives ...string) {
	t.Helper()
	for _, relative := range relatives {
		variant, location, sourceWork := uuid.New(), uuid.New(), uuid.New()
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO media_variant(id,size_bytes) VALUES (?,0)`, []any{variant}},
			{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id)
				SELECT ?,source_root_id,?,0,mtime,last_seen_scan_generation,'audio',? FROM source_location WHERE id=?`, []any{location, relative, variant, work.LocationID}},
			{`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id)
				SELECT ?,?,source_root_id,configured_path,inventory_path,?,size_bytes,mtime,false,origin_scan_operation_id,? FROM source_analysis_work WHERE id=?`, []any{sourceWork, location, relative, location, work.ID}},
			{`INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, []any{sourceWork, variant}},
			{`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method)
				SELECT ?,track_id,?,false,true,'manual' FROM release_track WHERE id=?`, []any{uuid.New(), variant, fixture.guard.ExpectedPositionID}},
		} {
			if _, err := fixture.db.NewRaw(statement.query, statement.args...).Exec(ctx); err != nil {
				t.Fatalf("add eligible source %q: %v", relative, err)
			}
		}
	}
}

func TestAdmitPublicationSourceCleanupExplicitRetryMixedThreeItemFrontierPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture, repository, variantID := publicationSourceCleanupReadyFixture(t)
	if _, err := fixture.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-remove-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var work SourceAnalysisWork
	if err := fixture.db.NewSelect().Model(&work).Where("id IN (SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?)", variantID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	addCleanupRetrySourceCopies(t, ctx, fixture, work, "retry-second.flac", "retry-third.flac")
	guard, originalOperationID := admitAndClaimSourceCleanup(t, ctx, fixture, repository)
	if started, err := repository.StartPublicationSourceCleanupExecution(ctx, guard); err != nil || !started {
		t.Fatalf("start three-source cleanup=%t,%v", started, err)
	}
	var items []SourceCleanupItem
	if err := fixture.db.NewSelect().Model(&items).Where("operation_id=?", originalOperationID).Order("id ASC").Scan(ctx); err != nil || len(items) != 3 {
		t.Fatalf("cleanup items=%d,%v; want three", len(items), err)
	}
	// The first callback conclusively completed; the second has a durable started
	// frontier but an uncertain callback result; the third remains unstarted.
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, items[0].ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return nil }); err != nil || outcome != PublicationSourceCleanupRemovalCompleted {
		t.Fatalf("complete first item=%s,%v", outcome, err)
	}
	uncertain := errors.New("uncertain unlink result")
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, guard, items[1].ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return uncertain }); outcome != PublicationSourceCleanupRemovalBlocked || !errors.Is(err, uncertain) {
		t.Fatalf("leave second item uncertain=%s,%v", outcome, err)
	}
	failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, guard, "source removal outcome is unknown")
	if err != nil || !failed {
		t.Fatalf("owner-returned failure=%t,%v", failed, err)
	}
	before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID)
	var frontierOK bool
	if err := fixture.db.NewRaw(`SELECT count(*)=3
		AND count(*) FILTER (WHERE mutation_started_at IS NOT NULL AND mutation_completed_at IS NOT NULL)=1
		AND count(*) FILTER (WHERE mutation_started_at IS NOT NULL AND mutation_completed_at IS NULL)=1
		AND count(*) FILTER (WHERE state='claimed' AND mutation_started_at IS NULL AND mutation_completed_at IS NULL)=1
		FROM source_cleanup_item WHERE operation_id=?`, originalOperationID).Scan(ctx, &frontierOK); err != nil || !frontierOK {
		t.Fatalf("mixed retained frontier=%t err=%v", frontierOK, err)
	}
	result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, new(publicationSourceCleanupTestInserter))
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("admit mixed-frontier retry=%+v,%v", result, err)
	}
	if got := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID); got != before {
		t.Fatalf("retry admission changed original evidence:\nbefore %s\nafter  %s", before, got)
	}
	var bindingOK bool
	if err := fixture.db.NewRaw(`SELECT count(*)=3 AND count(*) FILTER (
		WHERE r.binding->>'original_claim_state'='recovery_required'
		AND
		exists (SELECT 1 FROM jsonb_array_elements(r.binding->'items') b
			WHERE b->>'item_id'=i.id::text AND b->>'owner_uuid'=o.owner_uuid::text
			AND b->>'state'=i.state
			AND (b->>'mutation_started_at')::timestamptz IS NOT DISTINCT FROM i.mutation_started_at
			AND (b->>'mutation_completed_at')::timestamptz IS NOT DISTINCT FROM i.mutation_completed_at)
	)=3 FROM source_cleanup_explicit_retry_item ri
	JOIN source_cleanup_explicit_retry r ON r.id=ri.retry_id
	JOIN source_cleanup_item i ON i.id=ri.item_id
	JOIN source_cleanup_owner o ON o.item_id=i.id
	WHERE r.recovery_operation_id=?`, result.OperationID).Scan(ctx, &bindingOK); err != nil || !bindingOK {
		t.Fatalf("mixed retry owner/frontier bindings=%t err=%v", bindingOK, err)
	}
}

// Concurrent admissions for one returned cleanup must serialize at the durable
// admission boundary: exactly one recovery operation may own its retained
// evidence, even when both callers begin from the same observed frontier.
func TestAdmitPublicationSourceCleanupExplicitRetryConcurrentPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, _, originalOperationID := failedPublicationSourceCleanupForRetry(t)
	before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID)

	type admission struct {
		result PublicationSourceCleanupExplicitRetryResult
		err    error
	}
	results := make(chan admission, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, new(publicationSourceCleanupTestInserter))
			results <- admission{result: result, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	var admitted uuid.UUID
	blocked := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent retry admission: %v", result.err)
		}
		if result.result.OperationID != uuid.Nil {
			if admitted != uuid.Nil {
				t.Fatalf("both concurrent retry admissions succeeded: %s and %s", admitted, result.result.OperationID)
			}
			admitted = result.result.OperationID
			if len(result.result.Blockers) != 0 {
				t.Fatalf("successful retry admission has blockers: %v", result.result.Blockers)
			}
		} else if len(result.result.Blockers) != 0 {
			blocked++
		}
	}
	if admitted == uuid.Nil || blocked != 1 {
		t.Fatalf("concurrent admission outcomes: admitted=%s blocked=%d; want one each", admitted, blocked)
	}
	if got := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID); got != before {
		t.Fatalf("concurrent admission changed original evidence:\nbefore %s\nafter  %s", before, got)
	}
	var grants, items, recoveryOps, jobs int
	for query, target := range map[string]*int{
		`SELECT count(*) FROM source_cleanup_explicit_retry WHERE original_operation_id=?`:                                                                    &grants,
		`SELECT count(*) FROM source_cleanup_explicit_retry_item WHERE retry_id=(SELECT id FROM source_cleanup_explicit_retry WHERE original_operation_id=?)`: &items,
	} {
		if err := fixture.db.NewRaw(query, originalOperationID).Scan(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM operation WHERE kind='retry_publication_sources_v1' AND id=?`, admitted).Scan(ctx, &recoveryOps); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='retry_publication_sources_v1' AND args->>'operation_id'=?`, admitted.String()).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if grants != 1 || items != 1 || recoveryOps != 1 || jobs != 1 {
		t.Fatalf("concurrent admission durable records: grants=%d items=%d recovery operations=%d jobs=%d", grants, items, recoveryOps, jobs)
	}
}
