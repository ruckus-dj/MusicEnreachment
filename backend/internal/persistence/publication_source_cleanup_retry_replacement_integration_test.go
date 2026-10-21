//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// This covers the replacement frontier's complete owner-return boundary. The
// retry callback is allowed to start but not complete; only after its failure
// has been persisted by the owner may its successor consume that uncertainty.
func TestReplacePublicationSourceCleanupExplicitRetryAdvancesOwnerReturnedFrontierPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	baseline := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID)
	oldRecoveryID := f.newGuard.OperationID
	oldGuard := f.newGuard

	callbackErr := errors.New("replacement callback returned uncertain")
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, oldGuard, f.itemIDs[2], func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		return PublicationSourceCleanupNativeUnresolved, callbackErr
	}); outcome != PublicationSourceCleanupRetryResolutionBlocked || !errors.Is(err, callbackErr) {
		t.Fatalf("start then fail recovery item=%s,%v", outcome, err)
	}
	// River can have completed/retried independently. Persist the actual owner
	// return before requesting replacement, which is the authority being tested.
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, oldGuard, "retry removal result is unknown"); err != nil || !failed {
		t.Fatalf("return failed recovery=%t,%v", failed, err)
	}

	// A wrong expected recovery must not consume the real grant/frontier.
	wrong, err := f.repository.ReplacePublicationSourceCleanupExplicitRetry(f.ctx, f.originalID, uuid.New(), new(publicationSourceCleanupTestInserter))
	if err != nil || wrong.OperationID != uuid.Nil || len(wrong.Blockers) == 0 {
		t.Fatalf("replacement with wrong expected owner=%+v,%v; want blocker", wrong, err)
	}
	if got := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID); got != baseline {
		t.Fatalf("wrong-owner replacement mutated retained evidence: %s", got)
	}

	inserter := new(publicationSourceCleanupTestInserter)
	replaced, err := f.repository.ReplacePublicationSourceCleanupExplicitRetry(f.ctx, f.originalID, oldRecoveryID, inserter)
	if err != nil || replaced.OperationID == uuid.Nil || len(replaced.Blockers) != 0 {
		t.Fatalf("replace failed owner-returned recovery=%+v,%v", replaced, err)
	}
	var args publicationSourceCleanupRetryJobArgs
	var recovery Operation
	if err := f.fixture.db.NewSelect().Model(&recovery).Where("id=?", replaced.OperationID).Scan(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshalRetryArgs(inserter.args, &args); err != nil || args.OperationID != replaced.OperationID {
		t.Fatalf("successor enqueued args=%+v err=%v", args, err)
	}
	if recovery.State != "queued" || recovery.Attempt != 1 || recovery.RiverJobID == nil {
		t.Fatalf("successor operation=%+v; want fresh queued delivery", recovery)
	}
	var grant SourceCleanupExplicitRetry
	if err := f.fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", replaced.OperationID).Scan(f.ctx); err != nil {
		t.Fatal(err)
	}
	var frontier SourceCleanupRecoveryState
	if err := f.fixture.db.NewSelect().Model(&frontier).Where("original_operation_id=?", f.originalID).Scan(f.ctx); err != nil {
		t.Fatal(err)
	}
	if grant.FrontierRevision == nil || *grant.FrontierRevision != frontier.Revision || frontier.ReturnedRecoveryOperationID != oldRecoveryID {
		t.Fatalf("successor grant/frontier not pinned to returned owner: grant=%+v frontier=%+v", grant, frontier)
	}
	var oldGrant, oldClaim, oldOperation, oldResolution int
	for _, q := range []struct {
		sql  string
		into *int
	}{
		{`SELECT count(*) FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, &oldGrant},
		{`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, &oldClaim},
		{`SELECT count(*) FROM operation WHERE id=?`, &oldOperation},
		{`SELECT count(*) FROM source_cleanup_explicit_retry_resolution WHERE recovery_operation_id=?`, &oldResolution},
	} {
		if err := f.fixture.db.NewRaw(q.sql, oldRecoveryID).Scan(f.ctx, q.into); err != nil {
			t.Fatal(err)
		}
	}
	if oldGrant != 0 || oldClaim != 0 || oldOperation != 0 || oldResolution != 0 {
		t.Fatalf("old recovery artifacts remain: grant=%d claim=%d operation=%d resolutions=%d", oldGrant, oldClaim, oldOperation, oldResolution)
	}
	if got := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID); got != baseline {
		t.Fatalf("replacement changed original cleanup evidence: before %s after %s", baseline, got)
	}

	var jobID int64
	if err := f.fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, replaced.OperationID).Scan(f.ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
	var successorGrant SourceCleanupExplicitRetry
	if err := f.fixture.db.NewSelect().Model(&successorGrant).Where("recovery_operation_id=?", replaced.OperationID).Scan(f.ctx); err != nil {
		t.Fatal(err)
	}
	newGuard := PublicationSourceCleanupDeliveryGuard{OperationID: replaced.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1,
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "replacement-frontier", Epoch: successorGrant.Epoch, PositionID: successorGrant.PositionID}
	if outcome, err := f.repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(f.ctx, newGuard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim successor=%s,%v", outcome, err)
	}
	callbackCalls := 0
	missing := func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		callbackCalls++
		return PublicationSourceCleanupNativeMissingLeaf, fs.ErrNotExist
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, newGuard, f.itemIDs[2], missing); err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
		t.Fatalf("resolve uncertain item as absent=%s,%v", outcome, err)
	}
	if callbackCalls != 1 {
		t.Fatalf("uncertain absence callback calls=%d", callbackCalls)
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, newGuard, f.itemIDs[0], nil); err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
		t.Fatalf("acknowledge original native completion=%s,%v", outcome, err)
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, newGuard, f.itemIDs[0], missing); err != nil || outcome != PublicationSourceCleanupRetryResolutionAlreadyCompleted {
		t.Fatalf("replay original completion=%s,%v", outcome, err)
	}
	if callbackCalls != 1 {
		t.Fatalf("completed-item replay invoked native callback: %d", callbackCalls)
	}
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, newGuard, f.itemIDs[1], missing); err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
		t.Fatalf("resolve retained original uncertainty=%s,%v", outcome, err)
	}
	if settled, err := f.repository.SettlePublicationSourceCleanupExplicitRetry(f.ctx, newGuard); err != nil || !settled {
		t.Fatalf("settle all frontier items=%t,%v", settled, err)
	}
	var currentItems, currentOwners int
	for _, q := range []struct {
		sql  string
		into *int
	}{
		{`SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`, &currentItems},
		{`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, &currentOwners},
	} {
		if err := f.fixture.db.NewRaw(q.sql, f.originalID).Scan(f.ctx, q.into); err != nil {
			t.Fatal(err)
		}
	}
	if currentItems != 0 || currentOwners != 0 {
		t.Fatalf("successful settlement retained current auxiliary rows: items=%d owners=%d", currentItems, currentOwners)
	}
}

// A second owner can itself return with an incomplete native removal. The
// next owner must preserve both prior acknowledgements and the new uncertainty
// until every current frontier item has been explicitly resolved.
func TestReplacePublicationSourceCleanupExplicitRetryPreservesTwoReplacementFrontierPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	baseline := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID)
	firstID, firstGuard := f.newGuard.OperationID, f.newGuard

	// Complete the original uncertainty as removed, then leave the original
	// unstarted item unresolved. The replacement frontier must carry both.
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, firstGuard, f.itemIDs[1], func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		return PublicationSourceCleanupNativeRemoved, nil
	}); err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
		t.Fatalf("resolve original uncertainty as removed=%s,%v", outcome, err)
	}
	callbackErr := errors.New("first successor native result uncertain")
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, firstGuard, f.itemIDs[2], func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		return PublicationSourceCleanupNativeUnresolved, callbackErr
	}); outcome != PublicationSourceCleanupRetryResolutionBlocked || !errors.Is(err, callbackErr) {
		t.Fatalf("leave unstarted item uncertain=%s,%v", outcome, err)
	}
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, firstGuard, "first successor removal result is unknown"); err != nil || !failed {
		t.Fatalf("return first failed owner=%t,%v", failed, err)
	}

	firstResult, err := f.repository.ReplacePublicationSourceCleanupExplicitRetry(f.ctx, f.originalID, firstID, new(publicationSourceCleanupTestInserter))
	if err != nil || firstResult.OperationID == uuid.Nil || len(firstResult.Blockers) != 0 {
		t.Fatalf("first replacement=%+v,%v", firstResult, err)
	}
	assertRetryReplacementStagingEmpty(t, f)
	firstSuccessor, firstSuccessorGrant := claimReplacementRetryForTest(t, f, firstResult)
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, firstSuccessor, f.itemIDs[2], func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		return PublicationSourceCleanupNativeMissingLeaf, fs.ErrNotExist
	}); err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
		t.Fatalf("resolve first successor uncertainty as absent=%s,%v", outcome, err)
	}
	assertIncomingRetryFrontierKinds(t, f, firstResult.OperationID, map[uuid.UUID]string{
		f.itemIDs[0]: "original_native_completed", f.itemIDs[1]: "native_removed", f.itemIDs[2]: "uncertain",
	})

	// Interrupt metadata acknowledgement of an already completed frontier. Its
	// durable start remains, but no filesystem callback may run or be re-enabled.
	if _, err := f.fixture.db.ExecContext(f.ctx, `
		CREATE FUNCTION reject_retry_completion_for_replacement() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.completed_at IS NOT NULL THEN RAISE EXCEPTION 'injected retry completion failure'; END IF;
			RETURN NEW;
		END $$;
		CREATE CONSTRAINT TRIGGER reject_retry_completion_for_replacement AFTER UPDATE ON source_cleanup_explicit_retry_resolution
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_retry_completion_for_replacement()`); err != nil {
		t.Fatal(err)
	}
	ackCallbackCalls := 0
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, firstSuccessor, f.itemIDs[1], func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		ackCallbackCalls++
		return PublicationSourceCleanupNativeRemoved, nil
	}); outcome != PublicationSourceCleanupRetryResolutionBlocked || err == nil {
		t.Fatalf("interrupted native completion=%s,%v; want deferred commit failure", outcome, err)
	}
	if ackCallbackCalls != 0 {
		t.Fatalf("interrupted acknowledgement invoked filesystem callback %d times", ackCallbackCalls)
	}
	if _, err := f.fixture.db.ExecContext(f.ctx, `DROP TRIGGER reject_retry_completion_for_replacement ON source_cleanup_explicit_retry_resolution; DROP FUNCTION reject_retry_completion_for_replacement()`); err != nil {
		t.Fatal(err)
	}
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, firstSuccessor, "completion acknowledgement was interrupted"); err != nil || !failed {
		t.Fatalf("return interrupted owner=%t,%v", failed, err)
	}

	secondResult, err := f.repository.ReplacePublicationSourceCleanupExplicitRetry(f.ctx, f.originalID, firstResult.OperationID, new(publicationSourceCleanupTestInserter))
	if err != nil || secondResult.OperationID == uuid.Nil || len(secondResult.Blockers) != 0 {
		t.Fatalf("second replacement=%+v,%v", secondResult, err)
	}
	secondGuard, _ := claimReplacementRetryForTest(t, f, secondResult)
	if got := cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID); got != baseline {
		t.Fatalf("replacements mutated original evidence before settlement:\nbefore %s\nafter  %s", baseline, got)
	}
	assertIncomingRetryFrontierKinds(t, f, secondResult.OperationID, map[uuid.UUID]string{
		f.itemIDs[0]: "original_native_completed", f.itemIDs[1]: "native_removed", f.itemIDs[2]: "absent_after_explicit_retry",
	})
	var frontier SourceCleanupRecoveryState
	if err := f.fixture.db.NewSelect().Model(&frontier).Where("original_operation_id=?", f.originalID).Scan(f.ctx); err != nil {
		t.Fatal(err)
	}
	var secondGrant SourceCleanupExplicitRetry
	if err := f.fixture.db.NewSelect().Model(&secondGrant).Where("recovery_operation_id=?", secondResult.OperationID).Scan(f.ctx); err != nil {
		t.Fatal(err)
	}
	if firstSuccessorGrant.FrontierRevision == nil || secondGrant.FrontierRevision == nil || *firstSuccessorGrant.FrontierRevision == *secondGrant.FrontierRevision || *secondGrant.FrontierRevision != frontier.Revision {
		t.Fatalf("replacement revisions did not advance and pin current frontier: first=%+v second=%+v frontier=%+v", firstSuccessorGrant, secondGrant, frontier)
	}
	assertRetryReplacementStagingEmpty(t, f)

	// All three incoming resolution kinds are acknowledgements now: nil callback
	// proves that no filesystem work is re-run, including the interrupted item.
	for _, id := range f.itemIDs {
		if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, secondGuard, id, nil); err != nil || outcome != PublicationSourceCleanupRetryResolutionCompleted {
			t.Fatalf("acknowledge preserved item %s=%s,%v", id, outcome, err)
		}
	}
	if settled, err := f.repository.SettlePublicationSourceCleanupExplicitRetry(f.ctx, secondGuard); err != nil || !settled {
		t.Fatalf("settle second replacement=%t,%v", settled, err)
	}
	for _, check := range []struct {
		name, query string
	}{
		{"cleanup items", `SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`},
		{"cleanup owners", `SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`},
		{"frontier items", `SELECT count(*) FROM source_cleanup_recovery_item WHERE original_operation_id=?`},
		{"transfer proofs", `SELECT count(*) FROM source_cleanup_frontier_transfer_proof WHERE original_operation_id=?`},
	} {
		var count int
		if err := f.fixture.db.NewRaw(check.query, f.originalID).Scan(f.ctx, &count); err != nil || count != 0 {
			t.Fatalf("post-settlement %s=%d,%v", check.name, count, err)
		}
	}
	assertRetryReplacementStagingEmpty(t, f)
}

func TestSourceCleanupFrontierRejectsCanonicalSameRevisionRewritePostgreSQL(t *testing.T) {
	t.Parallel()
	f := returnFailedRetryForReplacement(t)
	firstResult, err := f.repository.ReplacePublicationSourceCleanupExplicitRetry(f.ctx, f.originalID, f.newGuard.OperationID, new(publicationSourceCleanupTestInserter))
	if err != nil || firstResult.OperationID == uuid.Nil || len(firstResult.Blockers) != 0 {
		t.Fatalf("first replacement=%+v,%v", firstResult, err)
	}
	assertRetryReplacementStagingEmpty(t, f)
	currentGuard, currentGrant := claimReplacementRetryForTest(t, f, firstResult)
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, currentGuard, "replacement owner returned for review"); err != nil || !failed {
		t.Fatalf("return current failed owner=%t,%v", failed, err)
	}
	if currentGrant.FrontierRevision == nil {
		t.Fatalf("replacement grant has no pinned frontier revision: %+v", currentGrant)
	}
	before := snapshotRejectedFrontierRewrite(t, f, currentGuard.OperationID)
	var stagedProof string
	err = f.fixture.db.RunInTx(f.ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// The database function creates the canonical proof for this exact valid
		// failed owner and stages it in this transaction. This is not a malformed
		// proof being rejected through an unrelated validation path.
		if err := tx.NewRaw(`SELECT public.source_cleanup_frontier_transfer_proof(?,?)::text`, f.originalID, currentGuard.OperationID).Scan(ctx, &stagedProof); err != nil {
			return err
		}
		_, err := tx.NewRaw(`UPDATE source_cleanup_recovery_state SET proof=?::jsonb,
			returned_recovery_operation_id=?, returned_application_attempt=?, returned_river_job_id=?, returned_river_attempt=?,
			returned_claim_uuid=?, returned_boot_uuid=?, returned_delivery_token=? WHERE original_operation_id=?`,
			stagedProof, currentGuard.OperationID, currentGuard.ApplicationAttempt, currentGuard.RiverJobID, currentGuard.RiverAttempt,
			currentGuard.ClaimUUID, currentGuard.BootUUID, currentGuard.DeliveryToken, f.originalID).Exec(ctx)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "fresh revision and retired outgoing grant") {
		t.Fatalf("same-revision canonical frontier rewrite error=%v; want fresh-revision/retired-grant rejection", err)
	}
	if stagedProof == "" || !json.Valid([]byte(stagedProof)) {
		t.Fatalf("canonical transfer proof was not produced: %q", stagedProof)
	}
	after := snapshotRejectedFrontierRewrite(t, f, currentGuard.OperationID)
	for key, value := range before {
		if after[key] != value {
			t.Errorf("rejected same-revision rewrite changed %s:\nbefore %s\nafter  %s", key, value, after[key])
		}
	}
	assertRetryReplacementStagingEmpty(t, f)
}

func snapshotRejectedFrontierRewrite(t *testing.T, f retryResolutionFixture, recoveryID uuid.UUID) map[string]string {
	t.Helper()
	queries := map[string]struct {
		query string
		args  []any
	}{
		"original":       {`SELECT COALESCE(to_jsonb(x)::text,'null') FROM operation x WHERE id=?`, []any{f.originalID}},
		"recovery":       {`SELECT COALESCE(to_jsonb(x)::text,'null') FROM operation x WHERE id=?`, []any{recoveryID}},
		"original_claim": {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY application_attempt)::text,'[]') FROM publication_execution_claim x WHERE operation_id=?`, []any{f.originalID}},
		"recovery_claim": {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY application_attempt)::text,'[]') FROM publication_execution_claim x WHERE operation_id=?`, []any{recoveryID}},
		"grants":         {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY recovery_operation_id)::text,'[]') FROM source_cleanup_explicit_retry x WHERE original_operation_id=?`, []any{f.originalID}},
		"frontier":       {`SELECT COALESCE(to_jsonb(x)::text,'null') FROM source_cleanup_recovery_state x WHERE original_operation_id=?`, []any{f.originalID}},
		"frontier_items": {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY item_id)::text,'[]') FROM source_cleanup_recovery_item x WHERE original_operation_id=?`, []any{f.originalID}},
		"retry_items":    {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY retry_id,item_id)::text,'[]') FROM source_cleanup_explicit_retry_item x WHERE original_operation_id=?`, []any{f.originalID}},
		"resolutions":    {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY recovery_operation_id,item_id)::text,'[]') FROM source_cleanup_explicit_retry_resolution x WHERE retry_id IN (SELECT id FROM source_cleanup_explicit_retry WHERE original_operation_id=?)`, []any{f.originalID}},
		"cleanup_items":  {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id)::text,'[]') FROM source_cleanup_item x WHERE operation_id=?`, []any{f.originalID}},
		"cleanup_owners": {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY item_id)::text,'[]') FROM source_cleanup_owner x WHERE operation_id=?`, []any{f.originalID}},
		"staging":        {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY transaction_id)::text,'[]') FROM source_cleanup_frontier_transfer_proof x WHERE original_operation_id=?`, []any{f.originalID}},
	}
	snapshot := make(map[string]string, len(queries))
	for name, spec := range queries {
		var value string
		if err := f.fixture.db.NewRaw(spec.query, spec.args...).Scan(f.ctx, &value); err != nil {
			t.Fatalf("snapshot rejected frontier rewrite %s: %v", name, err)
		}
		snapshot[name] = value
	}
	return snapshot
}

func claimReplacementRetryForTest(t *testing.T, f retryResolutionFixture, result PublicationSourceCleanupExplicitRetryResult) (PublicationSourceCleanupDeliveryGuard, SourceCleanupExplicitRetry) {
	t.Helper()
	var operation Operation
	if err := f.fixture.db.NewSelect().Model(&operation).Where("id=?", result.OperationID).Scan(f.ctx); err != nil {
		t.Fatal(err)
	}
	if operation.RiverJobID == nil {
		t.Fatalf("replacement %s has no River job", result.OperationID)
	}
	jobID := *operation.RiverJobID
	if _, err := f.fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
	var grant SourceCleanupExplicitRetry
	if err := f.fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", result.OperationID).Scan(f.ctx); err != nil {
		t.Fatal(err)
	}
	guard := PublicationSourceCleanupDeliveryGuard{OperationID: result.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1,
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(), Epoch: grant.Epoch, PositionID: grant.PositionID}
	if outcome, err := f.repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(f.ctx, guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim replacement %s=%s,%v", result.OperationID, outcome, err)
	}
	return guard, grant
}

func assertIncomingRetryFrontierKinds(t *testing.T, f retryResolutionFixture, recoveryID uuid.UUID, want map[uuid.UUID]string) {
	t.Helper()
	for itemID, expected := range want {
		var got string
		if err := f.fixture.db.NewRaw(`SELECT i.kind FROM source_cleanup_recovery_item i JOIN source_cleanup_recovery_state s ON s.original_operation_id=i.original_operation_id JOIN source_cleanup_explicit_retry g ON g.original_operation_id=s.original_operation_id AND g.frontier_revision=s.revision WHERE g.recovery_operation_id=? AND i.item_id=?`, recoveryID, itemID).Scan(f.ctx, &got); err != nil || got != expected {
			t.Fatalf("incoming frontier recovery=%s item=%s kind=%q err=%v; want %q", recoveryID, itemID, got, err, expected)
		}
	}
}

func assertRetryReplacementStagingEmpty(t *testing.T, f retryResolutionFixture) {
	t.Helper()
	var proofs int
	if err := f.fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_frontier_transfer_proof WHERE original_operation_id=?`, f.originalID).Scan(f.ctx, &proofs); err != nil || proofs != 0 {
		t.Fatalf("frontier transfer staging rows=%d,%v; want empty", proofs, err)
	}
}

// Keep the test's River assertion independent of a concrete job argument
// implementation while still verifying the durable operation identity.
func jsonUnmarshalRetryArgs(value any, target *publicationSourceCleanupRetryJobArgs) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func returnFailedRetryForReplacement(t *testing.T) retryResolutionFixture {
	t.Helper()
	f := newRetryResolutionFixture(t)
	errCallback := errors.New("native result cannot be determined")
	if outcome, err := f.repository.ResolvePublicationSourceCleanupRetryItem(f.ctx, f.newGuard, f.itemIDs[2], func(context.Context, PublicationSourceCleanupRemovalRequest) (PublicationSourceCleanupNativeOutcome, error) {
		return PublicationSourceCleanupNativeUnresolved, errCallback
	}); outcome != PublicationSourceCleanupRetryResolutionBlocked || !errors.Is(err, errCallback) {
		t.Fatalf("leave real retry resolution incomplete=%s,%v", outcome, err)
	}
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, f.newGuard, "native retry outcome is unknown"); err != nil || !failed {
		t.Fatalf("persist failed owner return=%t,%v", failed, err)
	}
	return f
}

func replacementRollbackSnapshot(t *testing.T, f retryResolutionFixture) map[string]string {
	t.Helper()
	queries := map[string]struct {
		query string
		args  []any
	}{
		"original_operation": {`SELECT COALESCE(to_jsonb(x)::text,'null') FROM operation x WHERE id=?`, []any{f.originalID}},
		"failed_operation":   {`SELECT COALESCE(to_jsonb(x)::text,'null') FROM operation x WHERE id=?`, []any{f.newGuard.OperationID}},
		"original_claim":     {`SELECT COALESCE(to_jsonb(x)::text,'null') FROM publication_execution_claim x WHERE operation_id=?`, []any{f.originalID}},
		"failed_claims":      {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY application_attempt)::text,'[]') FROM publication_execution_claim x WHERE operation_id=?`, []any{f.newGuard.OperationID}},
		"grants":             {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id)::text,'[]') FROM source_cleanup_explicit_retry x WHERE original_operation_id=?`, []any{f.originalID}},
		"retry_items":        {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY retry_id,item_id)::text,'[]') FROM source_cleanup_explicit_retry_item x WHERE original_operation_id=?`, []any{f.originalID}},
		"resolutions":        {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY retry_id,item_id)::text,'[]') FROM source_cleanup_explicit_retry_resolution x WHERE recovery_operation_id=?`, []any{f.newGuard.OperationID}},
		"frontier_state":     {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY original_operation_id)::text,'[]') FROM source_cleanup_recovery_state x WHERE original_operation_id=?`, []any{f.originalID}},
		"frontier_items":     {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY item_id)::text,'[]') FROM source_cleanup_recovery_item x WHERE original_operation_id=?`, []any{f.originalID}},
		"cleanup_items":      {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id)::text,'[]') FROM source_cleanup_item x WHERE operation_id=?`, []any{f.originalID}},
		"cleanup_owners":     {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY item_id)::text,'[]') FROM source_cleanup_owner x WHERE operation_id=?`, []any{f.originalID}},
		"river_jobs":         {`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id)::text,'[]') FROM river_job x WHERE id IN (SELECT river_job_id FROM operation WHERE id IN (?,?))`, []any{f.originalID, f.newGuard.OperationID}},
	}
	snapshot := make(map[string]string, len(queries)+1)
	for name, spec := range queries {
		var value string
		if err := f.fixture.db.NewRaw(spec.query, spec.args...).Scan(f.ctx, &value); err != nil {
			t.Fatalf("snapshot %s: %v", name, err)
		}
		snapshot[name] = value
	}
	snapshot["retained_evidence"] = cleanupRetryRetainedEvidence(t, f.ctx, f.fixture, f.originalID)
	return snapshot
}

func TestReplacePublicationSourceCleanupExplicitRetryEnqueueFailureRollsBackEverythingPostgreSQL(t *testing.T) {
	t.Parallel()
	f := returnFailedRetryForReplacement(t)
	before := replacementRollbackSnapshot(t, f)
	sentinel := errors.New("insert job then fail replacement")
	inserter := &publicationSourceCleanupTestInserter{err: sentinel}
	result, err := f.repository.ReplacePublicationSourceCleanupExplicitRetry(f.ctx, f.originalID, f.newGuard.OperationID, inserter)
	if !errors.Is(err, sentinel) || result.OperationID != uuid.Nil {
		t.Fatalf("failed replacement=%+v,%v; want sentinel and nil operation", result, err)
	}
	if inserter.args == nil {
		t.Fatal("failure inserter was not called; test must insert a real River job before returning error")
	}
	after := replacementRollbackSnapshot(t, f)
	for key, value := range before {
		if after[key] != value {
			t.Errorf("failed replacement changed %s:\nbefore %s\nafter  %s", key, value, after[key])
		}
	}
}

func TestReplacePublicationSourceCleanupExplicitRetryConcurrentSingleWinnerPostgreSQL(t *testing.T) {
	t.Parallel()
	f := returnFailedRetryForReplacement(t)
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	type outcome struct {
		result PublicationSourceCleanupExplicitRetryResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				results <- outcome{err: ctx.Err()}
			case <-start:
				result, err := f.repository.ReplacePublicationSourceCleanupExplicitRetry(ctx, f.originalID, f.newGuard.OperationID, new(publicationSourceCleanupTestInserter))
				results <- outcome{result: result, err: err}
			}
		}()
	}
	close(start)
	got := make([]outcome, 0, 2)
	for range 2 {
		select {
		case result := <-results:
			got = append(got, result)
		case <-ctx.Done():
			t.Fatalf("concurrent replacement timed out: %v", ctx.Err())
		}
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("replacement goroutines did not finish: %v", ctx.Err())
	}
	winners, blocked := 0, 0
	var winnerID uuid.UUID
	for _, result := range got {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.result.OperationID != uuid.Nil {
			winners++
			winnerID = result.result.OperationID
		} else if len(result.result.Blockers) != 0 {
			blocked++
		} else {
			t.Errorf("losing concurrent replacement returned neither operation nor blocker: %+v", result.result)
		}
	}
	if winners != 1 || blocked != 1 || winnerID == uuid.Nil {
		t.Fatalf("concurrent replacement winners=%d blocked=%d id=%s", winners, blocked, winnerID)
	}
	var operationCount, grantCount, allGrantCount, jobCount, retryOperationCount, frontierCount int
	for _, check := range []struct {
		query string
		into  *int
	}{
		{`SELECT count(*) FROM operation WHERE id=? AND kind='retry_publication_sources_v1'`, &operationCount},
		{`SELECT count(*) FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, &grantCount},
		{`SELECT count(*) FROM source_cleanup_explicit_retry WHERE original_operation_id=?`, &allGrantCount},
		{`SELECT count(*) FROM river_job WHERE id=(SELECT river_job_id FROM operation WHERE id=?)`, &jobCount},
		{`SELECT count(*) FROM operation WHERE kind='retry_publication_sources_v1' AND input_snapshot->>'original_operation_id'=?`, &retryOperationCount},
		{`SELECT count(*) FROM source_cleanup_recovery_state WHERE original_operation_id=?`, &frontierCount},
	} {
		arg := winnerID
		if check.query == `SELECT count(*) FROM source_cleanup_recovery_state WHERE original_operation_id=?` || check.query == `SELECT count(*) FROM source_cleanup_explicit_retry WHERE original_operation_id=?` || check.query == `SELECT count(*) FROM operation WHERE kind='retry_publication_sources_v1' AND input_snapshot->>'original_operation_id'=?` {
			arg = f.originalID
		}
		if err := f.fixture.db.NewRaw(check.query, arg).Scan(ctx, check.into); err != nil {
			t.Fatal(err)
		}
	}
	if operationCount != 1 || grantCount != 1 || allGrantCount != 1 || jobCount != 1 || retryOperationCount != 1 || frontierCount != 1 {
		t.Fatalf("winner artifacts operation=%d grant=%d all grants=%d job=%d retry operations=%d frontier=%d", operationCount, grantCount, allGrantCount, jobCount, retryOperationCount, frontierCount)
	}
	var grant SourceCleanupExplicitRetry
	if err := f.fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", winnerID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var frontier SourceCleanupRecoveryState
	if err := f.fixture.db.NewSelect().Model(&frontier).Where("original_operation_id=?", f.originalID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if grant.FrontierRevision == nil || *grant.FrontierRevision != frontier.Revision || frontier.ReturnedRecoveryOperationID != f.newGuard.OperationID {
		t.Fatalf("winner does not pin the sole returned frontier: grant=%+v frontier=%+v", grant, frontier)
	}
	var oldOperations, oldClaims, oldGrants, oldResolutions int
	for _, check := range []struct {
		query string
		into  *int
	}{
		{`SELECT count(*) FROM operation WHERE id=?`, &oldOperations},
		{`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, &oldClaims},
		{`SELECT count(*) FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, &oldGrants},
		{`SELECT count(*) FROM source_cleanup_explicit_retry_resolution WHERE recovery_operation_id=?`, &oldResolutions},
	} {
		if err := f.fixture.db.NewRaw(check.query, f.newGuard.OperationID).Scan(ctx, check.into); err != nil {
			t.Fatal(err)
		}
	}
	if oldOperations != 0 || oldClaims != 0 || oldGrants != 0 || oldResolutions != 0 {
		t.Fatalf("superseded recovery remains operation=%d claim=%d grant=%d resolutions=%d", oldOperations, oldClaims, oldGrants, oldResolutions)
	}
}
