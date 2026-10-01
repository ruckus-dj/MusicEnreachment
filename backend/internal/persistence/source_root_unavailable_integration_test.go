//go:build integration

package persistence_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// unavailableSafeReason is the user-facing text a scan records when its
// registered directory cannot be read. It carries no path and no diagnostic,
// which is what the root's safe_error must always be.
const unavailableSafeReason = "The configured source directory is unavailable or no longer readable. The previous inventory is unchanged."

// TestSourceRootUnavailableKeepsInventoryWithPostgreSQL drives the required
// sequence: a successful scan, an unavailable report, then a successful rescan.
// The unavailable report keeps the generation, the inventory path, the applied
// operation, the last successful timestamp and every location of the previous
// inventory, and the rescan clears the reason and preserves the identity of an
// unchanged file.
func TestSourceRootUnavailableKeepsInventoryWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/unavailable")

	first := newSourceScanOperation(t, ctx, database, root, "running")
	applySourceScan(t, ctx, inventory, first, root.ConfiguredPath,
		sourceCandidate("album/track.flac", 1024, probeMtime()),
		sourceCandidate("album/second.wav", 2048, probeMtime()))
	setOperationState(t, ctx, database, first.ID, "succeeded")
	applied, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read the root after the first scan: %v", err)
	}
	if applied.LastSuccessfulScanAt == nil {
		t.Fatal("the first successful scan recorded no last successful timestamp")
	}
	appliedLocations := inventoryLocations(t, ctx, database, root.ID)
	trackID := locationID(t, ctx, database, root.ID, "album/track.flac")

	// A newer scan finds the directory unreadable.
	failing := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.MarkSourceRootUnavailable(ctx, persistence.SourceScanUnavailable{
		OperationID: failing.ID, SafeError: unavailableSafeReason,
	}); err != nil {
		t.Fatalf("mark the root unavailable: %v", err)
	}
	unavailable, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read the root after the unavailable report: %v", err)
	}
	if unavailable.Status != persistence.SourceRootStatusUnavailable {
		t.Fatalf("status after the unavailable report = %q, want unavailable", unavailable.Status)
	}
	if unavailable.SafeError == nil || *unavailable.SafeError != unavailableSafeReason {
		t.Fatalf("safe error after the unavailable report = %v, want %q", unavailable.SafeError, unavailableSafeReason)
	}
	if unavailable.ScanGeneration != applied.ScanGeneration {
		t.Fatalf("generation after the unavailable report = %d, want %d", unavailable.ScanGeneration, applied.ScanGeneration)
	}
	if unavailable.InventoryPath == nil || *unavailable.InventoryPath != *applied.InventoryPath {
		t.Fatalf("inventory path after the unavailable report = %v, want %v", unavailable.InventoryPath, applied.InventoryPath)
	}
	if unavailable.LastAppliedOperationID == nil || *unavailable.LastAppliedOperationID != first.ID {
		t.Fatalf("applied operation after the unavailable report = %v, want %s", unavailable.LastAppliedOperationID, first.ID)
	}
	if unavailable.LastSuccessfulScanAt == nil || !unavailable.LastSuccessfulScanAt.Equal(*applied.LastSuccessfulScanAt) {
		t.Fatalf("last successful timestamp after the unavailable report = %v, want %v", unavailable.LastSuccessfulScanAt, applied.LastSuccessfulScanAt)
	}
	if current := inventoryLocations(t, ctx, database, root.ID); !reflect.DeepEqual(current, appliedLocations) {
		t.Fatalf("locations after the unavailable report = %v, want the previous inventory %v", current, appliedLocations)
	}

	// The next successful scan of the restored directory clears the reason,
	// advances the generation and keeps the unchanged file's identity.
	setOperationState(t, ctx, database, failing.ID, "succeeded")
	restored := newSourceScanOperation(t, ctx, database, root, "running")
	applySourceScan(t, ctx, inventory, restored, root.ConfiguredPath,
		sourceCandidate("album/track.flac", 1024, probeMtime()),
		sourceCandidate("album/third.flac", 4096, probeMtime()))
	setOperationState(t, ctx, database, restored.ID, "succeeded")
	recovered, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read the root after the successful rescan: %v", err)
	}
	if recovered.Status != persistence.SourceRootStatusAvailable || recovered.SafeError != nil {
		t.Fatalf("root after the successful rescan = %s/%v, want available without a safe error", recovered.Status, recovered.SafeError)
	}
	if recovered.ScanGeneration != applied.ScanGeneration+1 {
		t.Fatalf("generation after the successful rescan = %d, want %d", recovered.ScanGeneration, applied.ScanGeneration+1)
	}
	if locationID(t, ctx, database, root.ID, "album/track.flac") != trackID {
		t.Fatal("the successful rescan rewrote the identity of an unchanged location")
	}
	if _, err := readLocationResult(ctx, database, root.ID, "album/second.wav"); err == nil {
		t.Fatal("a location absent from the rescan survived the new inventory")
	}
}

// TestSourceRootUnavailableRefusedForSupersededScanWithPostgreSQL pins the
// concurrency guard: once a newer scan installed its generation, neither an
// older operation's late report nor the operation that installed the generation
// may overwrite the available state.
func TestSourceRootUnavailableRefusedForSupersededScanWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/superseded")

	older := newSourceScanOperation(t, ctx, database, root, "queued")
	failScanOperation(t, ctx, database, older.ID, "The scan failed before the newer generation.")
	success := newSourceScanOperation(t, ctx, database, root, "queued")
	startScanOperation(t, ctx, database, success.ID)
	applySourceScan(t, ctx, inventory, success, root.ConfiguredPath,
		sourceCandidate("album/track.flac", 1024, probeMtime()))
	setOperationState(t, ctx, database, success.ID, "succeeded")
	baseline := snapshotInventory(t, ctx, database, root.ID)

	for _, stale := range []persistence.SourceScanUnavailable{
		{OperationID: older.ID, SafeError: unavailableSafeReason},
		{OperationID: success.ID, SafeError: unavailableSafeReason},
	} {
		if err := inventory.MarkSourceRootUnavailable(ctx, stale); err != nil {
			t.Fatalf("mark with the superseded operation %s: %v", stale.OperationID, err)
		}
		if current := snapshotInventory(t, ctx, database, root.ID); current != baseline {
			t.Fatalf("root after the superseded report of %s = %q, want the newer success %q", stale.OperationID, current, baseline)
		}
	}
}

// TestSourceRootUnavailableRefusedForOperationThatAlreadyAppliedWithPostgreSQL
// pins the ordering key against a late duplicate of the scan that installed the
// current generation. A source scan never records a started_at: the worker
// applies the generation from its queued operation and only then records
// success, which moves updated_at past last_successful_scan_at. A duplicate
// delivery that reports the root unavailable after that apply must still be
// refused, because its attempt began before the generation it would overwrite
// and a terminal operation's updated_at must not be read as the attempt origin.
func TestSourceRootUnavailableRefusedForOperationThatAlreadyAppliedWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/late-duplicate")

	operation := newSourceScanOperation(t, ctx, database, root, "queued")
	applySourceScan(t, ctx, inventory, operation, root.ConfiguredPath,
		sourceCandidate("album/track.flac", 1024, probeMtime()))
	setOperationState(t, ctx, database, operation.ID, "succeeded")
	baseline := snapshotInventory(t, ctx, database, root.ID)
	applied, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read the root after the success: %v", err)
	}

	if err := inventory.MarkSourceRootUnavailable(ctx, persistence.SourceScanUnavailable{
		OperationID: operation.ID, SafeError: unavailableSafeReason,
	}); err != nil {
		t.Fatalf("mark with the operation that already applied: %v", err)
	}
	if current := snapshotInventory(t, ctx, database, root.ID); current != baseline {
		t.Fatalf("root after the duplicate report = %q, want the applied generation %q", current, baseline)
	}
	current, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read the root after the refused report: %v", err)
	}
	if current.LastSuccessfulScanAt == nil || !current.LastSuccessfulScanAt.Equal(*applied.LastSuccessfulScanAt) {
		t.Fatalf("last successful timestamp after the refused report = %v, want %v", current.LastSuccessfulScanAt, applied.LastSuccessfulScanAt)
	}
}

// TestSourceRootUnavailableAcceptedForRetryStartedAfterNewerSuccessWithPostgreSQL
// pins the other half of the ordering key: a retry enqueued after a successful
// scan is a fresh attempt, so its report must still take effect, and its failure
// must not be mistaken for the older attempt's late report. The retry is driven
// through the real retry transaction, then fails before it starts its traversal,
// which is how an unreadable root is reported.
func TestSourceRootUnavailableAcceptedForRetryStartedAfterNewerSuccessWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	repository := persistence.NewSetupManagerRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/retry-unavailable")

	applied := newSourceScanOperation(t, ctx, database, root, "running")
	applySourceScan(t, ctx, inventory, applied, root.ConfiguredPath,
		sourceCandidate("album/track.flac", 1024, probeMtime()))
	setOperationState(t, ctx, database, applied.ID, "succeeded")
	appliedRoot, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read the root after the success: %v", err)
	}

	// A scan of the same root fails and is retried: the retry is a fresh attempt
	// enqueued after the success.
	failed := failedSourceScanRetryOperation(t, ctx, repository, root, "queued", unavailableSafeReason)
	retried, err := retrySourceScan(t, ctx, repository, failed.ID, client)
	if err != nil {
		t.Fatalf("retry the failed scan: %v", err)
	}
	if retried.Attempt != 2 || retried.State != "queued" {
		t.Fatalf("retried operation = attempt %d state %q, want a queued attempt 2", retried.Attempt, retried.State)
	}

	// The retry fails before it starts its traversal: started_at stays NULL and
	// the failure moves updated_at past the success.
	failScanOperation(t, ctx, database, retried.ID, unavailableSafeReason)
	if err := inventory.MarkSourceRootUnavailable(ctx, persistence.SourceScanUnavailable{
		OperationID: retried.ID, SafeError: unavailableSafeReason,
	}); err != nil {
		t.Fatalf("mark with the fresh retry: %v", err)
	}
	current, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read the root after the retry report: %v", err)
	}
	if current.Status != persistence.SourceRootStatusUnavailable || current.SafeError == nil || *current.SafeError != unavailableSafeReason {
		t.Fatalf("root after the fresh retry report = %s/%v, want unavailable with the safe reason", current.Status, current.SafeError)
	}
	if current.ScanGeneration != appliedRoot.ScanGeneration || current.InventoryPath == nil || *current.InventoryPath != *appliedRoot.InventoryPath {
		t.Fatalf("inventory after the fresh retry report = generation %d path %v, want generation %d path %v",
			current.ScanGeneration, current.InventoryPath, appliedRoot.ScanGeneration, appliedRoot.InventoryPath)
	}
	if current.LastSuccessfulScanAt == nil || !current.LastSuccessfulScanAt.Equal(*appliedRoot.LastSuccessfulScanAt) {
		t.Fatalf("last successful timestamp after the fresh retry report = %v, want %v", current.LastSuccessfulScanAt, appliedRoot.LastSuccessfulScanAt)
	}
}

// TestSourceRootUnavailableRejectsEmptyReasonWithPostgreSQL pins the boundary
// the SQL check cannot enforce: a reason that is non-NULL but empty is not a
// usable message, so the repository refuses it before any write.
func TestSourceRootUnavailableRejectsEmptyReasonWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/empty-reason")
	operation := newSourceScanOperation(t, ctx, database, root, "running")
	before := snapshotInventory(t, ctx, database, root.ID)

	if err := inventory.MarkSourceRootUnavailable(ctx, persistence.SourceScanUnavailable{
		OperationID: operation.ID, SafeError: "",
	}); err == nil {
		t.Fatal("an unavailable report with an empty safe error was accepted")
	}
	if current := snapshotInventory(t, ctx, database, root.ID); current != before {
		t.Fatalf("root after the refused empty report = %q, want %q", current, before)
	}
}

// TestSourceRootUnavailableRejectsNonScanOperationWithPostgreSQL pins that the
// targeted update only applies to a scan of a root: an operation without a
// source root target is refused.
func TestSourceRootUnavailableRejectsNonScanOperationWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/non-scan")
	operation := newSourceScanOperation(t, ctx, database, root, "queued")
	setOperationState(t, ctx, database, operation.ID, "succeeded")
	if err := persistOperationKindAndTarget(t, ctx, database, operation.ID, "install", nil, []byte(`{"target_identity":"ffmpeg:unavailable"}`)); err != nil {
		t.Fatalf("turn the scan operation into a non-scan one: %v", err)
	}
	before := snapshotInventory(t, ctx, database, root.ID)

	if err := inventory.MarkSourceRootUnavailable(ctx, persistence.SourceScanUnavailable{
		OperationID: operation.ID, SafeError: unavailableSafeReason,
	}); err == nil {
		t.Fatal("an unavailable report for an operation without a source root target was accepted")
	}
	if current := snapshotInventory(t, ctx, database, root.ID); current != before {
		t.Fatalf("root after the refused non-scan report = %q, want %q", current, before)
	}
}

// failScanOperation terminates a scan as failed with a safe reason, which is the
// state an operator retries. The terminal transition moves updated_at, so a
// report from this operation stays older than a later successful generation.
func failScanOperation(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, safe string) {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.Operation)(nil)).
		Set("state = 'failed'").Set("safe_error = ?", safe).
		Set("finished_at = now()").Set("updated_at = now()").
		Where("id = ?", operationID).Exec(ctx); err != nil {
		t.Fatalf("fail scan operation %s: %v", operationID, err)
	}
}

// startScanOperation models a delivered scan that began its traversal: the
// attempt start is recorded, which is the ordering key the unavailable guard
// reads.
func startScanOperation(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.Operation)(nil)).
		Set("state = 'running'").Set("started_at = now()").Set("updated_at = now()").
		Where("id = ?", operationID).Exec(ctx); err != nil {
		t.Fatalf("start scan operation %s: %v", operationID, err)
	}
}
