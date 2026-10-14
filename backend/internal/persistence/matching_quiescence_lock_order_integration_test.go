//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestQuiesceObsoleteAutomaticMatchingRunLocksParentBeforeRunPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	fixture := newIncomingMatchFixture(t, ctx, db)
	setup := persistence.NewSetupManagerRepository(db)
	inventory := persistence.NewSourceInventoryRepository(db)
	client := openScanEnqueueRiver(t, db)
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return persistence.RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{fixture.group.ID})
	}); err != nil {
		t.Fatalf("request initial current reconciliation: %v", err)
	}

	var event persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&event).Where("reason = ?", "current").Where("state = ?", "pending").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read initial reconciliation event: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch initial reconciliation: %v", err)
	}
	dispatched, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil || dispatched.RegisteredJobID == nil {
		t.Fatalf("read registered reconciliation: event=%+v err=%v", dispatched, err)
	}
	eventEpoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *dispatched.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim reconciliation event: epoch=%d claimed=%v err=%v", eventEpoch, claimed, err)
	}
	var generation int64
	if err := db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &generation); err != nil {
		t.Fatalf("read initial request generation: %v", err)
	}
	var providerSource struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := db.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &providerSource); err != nil {
		t.Fatalf("read provider fence: %v", err)
	}
	query := json.RawMessage(`{"schema_version":1,"query":"Fixture Artist","offset":0,"limit":10}`)
	cacheKey := "artist-search:0:10:Fixture Artist"
	parent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	child := &persistence.Operation{
		ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &fixture.source, ProviderID: &providerSource.ProviderID,
		ProviderConfigurationIdentity: &providerSource.Identity, ProviderEndpoint: providerSource.Endpoint,
		ProviderCacheKey: &cacheKey, Attempt: 1,
	}
	admission, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parent.ID},
		Provider: persistence.MatchingProviderRequest{Operation: child, Args: service.ProviderFetchOperationArgs{OperationID: child.ID}},
		GroupID:  fixture.group.ID, ExpectedGroupRevision: fixture.group.Revision,
		PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: event.ID, EventRiverJobID: *dispatched.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: eventEpoch,
		RequestGeneration: generation,
	})
	if err != nil {
		t.Fatalf("admit automatic artist matching run: %v", err)
	}

	// Advance the current-state generation and admit its replacement. This makes
	// the first run a real scanner candidate without manually editing run state.
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return persistence.RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{fixture.group.ID})
	}); err != nil {
		t.Fatalf("request newer generation: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch newer reconciliation: %v", err)
	}
	var newerEvent persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&newerEvent).Where("reason = ?", "current").Where("state = ?", "dispatched").Where("id <> ?", event.ID).Order("created_at DESC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read newer reconciliation event: %v", err)
	}
	newerEpoch, newerClaimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, newerEvent.ID, *newerEvent.RegisteredJobID, 1)
	if err != nil || !newerClaimed {
		t.Fatalf("claim newer reconciliation: epoch=%d claimed=%v err=%v", newerEpoch, newerClaimed, err)
	}
	var newerGeneration int64
	if err := db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &newerGeneration); err != nil {
		t.Fatalf("read newer request generation: %v", err)
	}
	secondChild := &persistence.Operation{
		ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &fixture.source, ProviderID: &providerSource.ProviderID,
		ProviderConfigurationIdentity: &providerSource.Identity, ProviderEndpoint: providerSource.Endpoint,
		ProviderCacheKey: &cacheKey, Attempt: 1,
	}
	secondParent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	if _, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: secondParent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: secondParent.ID},
		Provider: persistence.MatchingProviderRequest{Operation: secondChild, Args: service.ProviderFetchOperationArgs{OperationID: secondChild.ID}},
		GroupID:  fixture.group.ID, ExpectedGroupRevision: fixture.group.Revision,
		PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: newerEvent.ID, EventRiverJobID: *newerEvent.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: newerEpoch,
		RequestGeneration: newerGeneration,
	}); err != nil {
		t.Fatalf("admit newer automatic run: %v", err)
	}

	// Hold the old run's parent operation while the real public scanner attempts
	// to classify it. Consumption uses the same parent-then-run lock ordering.
	txA, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin parent lock transaction: %v", err)
	}
	defer txA.Rollback()
	var blockerPID int
	if err := txA.NewRaw(`SELECT pg_backend_pid()`).Scan(ctx, &blockerPID); err != nil {
		t.Fatalf("read parent lock backend PID: %v", err)
	}
	var lockedParent uuid.UUID
	if err := txA.NewRaw(`SELECT id FROM operation WHERE id=? FOR UPDATE`, admission.Operation.ID).Scan(ctx, &lockedParent); err != nil {
		t.Fatalf("lock parent operation: %v", err)
	}

	scanCtx, cancelScan := context.WithTimeout(ctx, 15*time.Second)
	scanResult := make(chan error, 1)
	go func() { scanResult <- setup.QuiesceObsoleteAutomaticMatchingRuns(scanCtx) }()

	// Canonical teardown: stop the scanner, release the parent lock it may be
	// blocked on, then join it with a bounded wait. Releasing the parent before
	// joining lets a blocked scanner exit cleanly instead of being left canceled
	// on the parent lock, and joining before the test database is torn down
	// guarantees the scanner goroutine has returned.
	joined := false
	defer func() {
		cancelScan()
		_ = txA.Rollback()
		if joined {
			return
		}
		select {
		case err := <-scanResult:
			joined = true
			if err != nil {
				t.Errorf("quiesce obsolete automatic runs: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("scanner goroutine did not return during cleanup")
		}
	}()

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	observeCtx, cancelObserve := context.WithTimeout(ctx, 10*time.Second)
	defer cancelObserve()
	blockedOnParent := false
	for !blockedOnParent {
		var waiting bool
		if err := db.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity activity
			WHERE activity.pid <> pg_backend_pid()
			  AND activity.wait_event_type='Lock'
			  AND ? = ANY(pg_blocking_pids(activity.pid))
			  AND activity.query ILIKE '%FROM operation%')`, blockerPID).Scan(observeCtx, &waiting); err != nil {
			t.Fatalf("observe scanner lock wait: %v", err)
		}
		if waiting {
			blockedOnParent = true
			break
		}
		select {
		case err := <-scanResult:
			joined = true
			t.Fatalf("scanner returned before waiting on locked parent: %v", err)
		case <-deadline.C:
			t.Fatal("scanner did not wait on the locked parent operation")
		case <-ticker.C:
		}
	}

	// If classification locks matching_run before operation, NOWAIT fails here.
	txB, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin run lock probe: %v", err)
	}
	var lockedRun uuid.UUID
	if err := txB.NewRaw(`SELECT id FROM matching_run WHERE id=? FOR UPDATE NOWAIT`, admission.OriginalRunID).Scan(ctx, &lockedRun); err != nil {
		_ = txB.Rollback()
		t.Fatalf("scanner held matching run lock while waiting for parent: %v", err)
	}
	if err := txB.Rollback(); err != nil {
		t.Fatalf("rollback run lock probe: %v", err)
	}
	if err := txA.Rollback(); err != nil {
		t.Fatalf("release parent lock: %v", err)
	}
	err = <-scanResult
	joined = true
	if err != nil {
		t.Fatalf("quiesce obsolete automatic runs: %v", err)
	}
}
