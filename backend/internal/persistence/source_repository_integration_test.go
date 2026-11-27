//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"runtime"

	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestSourceScanEnqueueLifecycleWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigratedWithRiver(t)
	for _, test := range []struct {
		name string
		run  func(*testing.T, *bun.DB)
	}{
		{"TestSourceScanEnqueueRollsBackTheOrphanJobWithPostgreSQL", runSourceScanEnqueueRollsBackTheOrphanJobWithPostgreSQL},
		{"TestSourceScanEnqueueRefusesDisabledAndActiveRootsWithPostgreSQL", runSourceScanEnqueueRefusesDisabledAndActiveRootsWithPostgreSQL},
	} {
		t.Run(test.name, func(t *testing.T) { test.run(t, database) })
	}
}

func waitForSourceRootLockWaiter(ctx context.Context, database *bun.DB, blockerPID int) error {
	for {
		var waiting bool
		err := database.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE ? = ANY(pg_blocking_pids(pid))
			  AND datname = current_database()
			  AND wait_event_type = 'Lock'
		)`, blockerPID).Scan(ctx, &waiting)
		if err != nil {
			return fmt.Errorf("inspect PostgreSQL lock wait: %w", err)
		}
		if waiting {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("timed out waiting for PostgreSQL lock wait: %w", err)
		}
		runtime.Gosched()
	}
}

func createInventoryRoot(t *testing.T, ctx context.Context, inventory *persistence.SourceInventoryRepository, path string) *persistence.SourceRoot {
	t.Helper()
	root := &persistence.SourceRoot{ConfiguredPath: path, DisplayName: "Inventory", ProcessingMode: "in_place"}
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create source root %s: %v", path, err)
	}
	return root
}

func deleteInventoryRoot(ctx context.Context, inventory *persistence.SourceInventoryRepository, id uuid.UUID) error {
	root, err := inventory.GetSourceRoot(ctx, id)
	if err != nil {
		return err
	}
	count, err := inventory.CountSourceLocations(ctx, id)
	if err != nil {
		return err
	}
	return inventory.DeleteSourceRoot(ctx, id, root.ConfiguredPath, count)
}

func newSourceScanOperation(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot, state string) *persistence.Operation {
	t.Helper()
	repository := persistence.NewSetupManagerRepository(database)
	snapshot, err := json.Marshal(service.ScanSourceSnapshot{
		SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: root.ID,
	})
	if err != nil {
		t.Fatalf("encode scan snapshot: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "queued", Stage: "queued",
		InputSnapshot:      snapshot,
		TargetSourceRootID: &root.ID,
	}
	client := openScanEnqueueRiver(t, database)
	if err := repository.CreateOperationAndEnqueue(ctx, operation, client,
		service.ScanSourceJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("enqueue scan operation: %v", err)
	}
	if state == "running" {
		inventory := persistence.NewSourceInventoryRepository(database)
		if err := inventory.StartSourceScanDelivery(ctx, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
			t.Fatalf("start scan delivery: %v", err)
		}
		operation.State = "running"
	} else if state != "queued" {
		t.Fatalf("unsupported scan fixture state %q", state)
	}
	return operation
}

func probeMtime() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func assertCandidateCount(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, want int) {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM source_scan_candidate WHERE operation_id = ?", operationID).Scan(ctx, &count); err != nil {
		t.Fatalf("count candidates of operation %s: %v", operationID, err)
	}
	if count != want {
		t.Fatalf("candidates of operation %s = %d, want %d", operationID, count, want)
	}
}

func readLocation(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID, relativePath string) persistence.SourceLocation {
	t.Helper()
	location, err := readLocationResult(ctx, database, rootID, relativePath)
	if err != nil {
		t.Fatalf("read location %q of root %s: %v", relativePath, rootID, err)
	}
	return location
}

func readLocationResult(ctx context.Context, database *bun.DB, rootID uuid.UUID, relativePath string) (persistence.SourceLocation, error) {
	var location persistence.SourceLocation
	err := database.NewSelect().Model(&location).
		Where("source_root_id = ?", rootID).Where("relative_path = ?", relativePath).Scan(ctx)
	return location, err
}

func setOperationState(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, state string) {
	t.Helper()
	query := database.NewUpdate().Model((*persistence.Operation)(nil)).
		Set("state = ?", state).Set("updated_at = now()").Where("id = ?", operationID)
	if state == "succeeded" || state == "failed" {
		query = query.Set("finished_at = now()")
	} else {
		query = query.Set("finished_at = NULL")
	}
	if _, err := query.Exec(ctx); err != nil {
		t.Fatalf("set operation %s to %s: %v", operationID, state, err)
	}
}

func persistOperationState(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, state string) error {
	t.Helper()
	_, err := database.NewUpdate().Model((*persistence.Operation)(nil)).
		Set("state = ?", state).Set("finished_at = now()").Set("updated_at = now()").
		Where("id = ?", operationID).Exec(ctx)
	return err
}

// TestSourceScanEnqueueRollsBackTheOrphanJobWithPostgreSQL fails the operation
// insert of an enqueue whose River job was already inserted: the transaction
// must take the job with it, so no River job is left for an operation that does
// not exist, and the root must stay usable for the next start.
func runSourceScanEnqueueRollsBackTheOrphanJobWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enqueue")

	jobsBefore := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind)
	operationsBefore := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'scan_source'")
	offset := time.Now().UTC().Truncate(time.Microsecond)
	occupied := uuid.New()
	if _, err := database.NewRaw(`INSERT INTO operation (id, kind, state, stage, input_snapshot, target_source_root_id, finished_at, updated_at)
		VALUES (?, 'scan_source', 'succeeded', 'applied', '{}', ?, ?, ?)`, occupied, root.ID, offset, offset).Exec(ctx); err != nil {
		t.Fatalf("seed the finished scan operation: %v", err)
	}
	collision := scanEnqueueOperation(root, occupied)
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, collision, client, service.ScanSourceJobArgs{OperationID: occupied}, nil); err == nil {
		t.Fatal("an enqueue that reuses the id of an existing operation was accepted")
	}

	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE id = ? AND kind = 'scan_source'", occupied); count != 1 {
		t.Fatalf("pre-existing operation %s count = %d, want exactly one", occupied, count)
	}
	if jobs := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind); jobs != jobsBefore {
		t.Fatalf("River jobs after rolled-back enqueue = %d, want baseline %d", jobs, jobsBefore)
	}
	if operations := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'scan_source'"); operations != operationsBefore+1 {
		t.Fatalf("scan operations after rolled-back enqueue = %d, want baseline %d plus the pre-existing operation", operations, operationsBefore)
	}

	fresh := scanEnqueueOperation(root, uuid.New())
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, fresh, client, service.ScanSourceJobArgs{OperationID: fresh.ID}, nil); err != nil {
		t.Fatalf("enqueue after the rolled-back transaction: %v", err)
	}
	if fresh.RiverJobID == nil {
		t.Fatalf("enqueued operation %+v has no River job", fresh)
	}
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE id = ? AND kind = ?", *fresh.RiverJobID, service.SourceScanJobKind); count != 1 {
		t.Fatalf("fresh River job %d count = %d, want exactly one", *fresh.RiverJobID, count)
	}
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE id = ? AND kind = 'scan_source'", fresh.ID); count != 1 {
		t.Fatalf("fresh scan operation %s count = %d, want exactly one", fresh.ID, count)
	}
	recovered := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind)
	t.Logf("after the recovered enqueue: operation %s river job %d, scan_source river jobs=%d", fresh.ID, *fresh.RiverJobID, recovered)
	if recovered != jobsBefore+1 {
		t.Fatalf("River jobs after the recovered enqueue = %d, want baseline %d plus one", recovered, jobsBefore)
	}
}

// TestSourceScanEnqueueRefusesDisabledAndActiveRootsWithPostgreSQL pins the two
// refusals the enqueue decides under the root lock: neither may store an
// operation and neither may insert a River job.
func runSourceScanEnqueueRefusesDisabledAndActiveRootsWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/refusals")

	active := newSourceScanOperation(t, ctx, database, root, "queued")
	// The active scan fixture stores one operation and its River job; the refused
	// enqueues must not add another of either, so capture the real baseline first.
	operationsBefore := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'scan_source'")
	jobsBefore := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind)
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE id = ? AND kind = 'scan_source'", active.ID); count != 1 {
		t.Fatalf("active operation %s count = %d, want exactly one", active.ID, count)
	}
	activeJobID := *active.RiverJobID
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE id = ? AND kind = ?", activeJobID, service.SourceScanJobKind); count != 1 {
		t.Fatalf("active River job %d count = %d, want exactly one", activeJobID, count)
	}
	activeRefusal := scanEnqueueOperation(root, uuid.New())
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, activeRefusal, client,
		service.ScanSourceJobArgs{OperationID: active.ID}, nil); !errors.Is(err, persistence.ErrSourceRootActiveScan) {
		t.Fatalf("enqueue on a root with a queued scan = %v, want ErrSourceRootActiveScan", err)
	}
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE id = ?", activeRefusal.ID); count != 0 {
		t.Fatalf("refused active-scan operation %s count = %d, want 0", activeRefusal.ID, count)
	}

	if err := persistOperationState(t, ctx, database, active.ID, "succeeded"); err != nil {
		t.Fatalf("finish the active scan: %v", err)
	}
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE id = ? AND kind = ?", activeJobID, service.SourceScanJobKind); count != 1 {
		t.Fatalf("active River job %d count after refusals = %d, want exactly one", activeJobID, count)
	}

	operations := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'scan_source'")
	jobs := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind)
	t.Logf("after the refused enqueues: scan operations=%d scan_source river jobs=%d", operations, jobs)
	if operations != operationsBefore {
		t.Fatalf("scan operations after the refusals = %d, want the initial %d preserved", operations, operationsBefore)
	}
	if jobs != jobsBefore {
		t.Fatalf("River jobs after the refusals = %d, want the initial %d preserved", jobs, jobsBefore)
	}
}

func scanEnqueueOperation(root *persistence.SourceRoot, id uuid.UUID) *persistence.Operation {
	return &persistence.Operation{
		ID: id, Kind: "scan_source", State: "queued", Stage: "queued",
		InputSnapshot:      []byte(`{"source_root_id":"` + root.ID.String() + `","configured_path":"` + root.ConfiguredPath + `"}`),
		TargetSourceRootID: &root.ID,
	}
}

func countScanEnqueueRows(t *testing.T, ctx context.Context, database *bun.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := database.NewRaw(query, args...).Scan(ctx, &count); err != nil {
		t.Fatalf("count rows (%s): %v", query, err)
	}
	return count
}

// openScanEnqueueRiver returns an insert-only client using River's real schema
// from the isolated application-and-River template.
func openScanEnqueueRiver(t *testing.T, database *bun.DB) *river.Client[*sql.Tx] {
	t.Helper()
	driver := riverdatabasesql.New(database.DB)
	client, err := river.NewClient(driver, &river.Config{})
	if err != nil {
		t.Fatalf("create insert-only River client: %v", err)
	}
	return client
}
