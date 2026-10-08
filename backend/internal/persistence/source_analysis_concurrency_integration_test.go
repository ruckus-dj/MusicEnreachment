//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// analysisRaceTimeout bounds every channel wait and transaction in a barrier
// test, so a broken implementation fails the test instead of hanging it.
const analysisRaceTimeout = 20 * time.Second

// TestSourceAnalysisEnqueueRefusesMoveCommittedUnderLockWithPostgreSQL arms a
// transaction that holds the operation table lock and writes a queued tools
// move. The analysis start reaches the same lock (proven by the query barrier),
// waits for the holder to commit, and only then observes the committed move, so
// its refusal is a property of the shared lock order and not of a scheduling
// race.
func TestSourceAnalysisEnqueueRefusesMoveCommittedUnderLockWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-move-barrier")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "barrier-move")
	setActiveAnalysisFFmpeg(t, ctx, database, installationID)

	tool := normalizedToolSelection(t, ctx, database, installationID, "ffprobe")
	operation := normalizedQueuedAnalysis(t, ctx, inventory, root, location, []persistence.SourceAnalysisToolSelection{tool})
	err := runQueryRace(t, ctx, database,
		"SELECT pg_advisory_xact_lock(1297371734, 1)", "pg_advisory_xact_lock_shared",
		func(ctx context.Context, tx bun.Tx) error {
			return insertQueuedOperation(ctx, tx, &persistence.Operation{
				ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
				InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"/srv/tools-before","new_root":"/srv/tools-after"}`),
			})
		},
		func(ctx context.Context) error {
			return enqueueNormalizedAnalysis(t, ctx, inventory, client, operation)
		})
	if !errors.Is(err, persistence.ErrToolsRootMoveActive) {
		t.Fatalf("analysis start with a committed move = %v, want ErrToolsRootMoveActive", err)
	}
	assertNoAnalysisStart(t, ctx, database)
}

// TestToolsMoveEnqueueRefusesAnalysisCommittedUnderLockWithPostgreSQL arms the
// same barrier with a queued analysis that holds a managed installation, so the
// reverse check of the tools move is decided under the shared lock as well.
func TestToolsMoveEnqueueRefusesAnalysisCommittedUnderLockWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	setup := persistence.NewSetupManagerRepository(database)
	if err := persistence.NewSettingsRepository(database).Set(ctx, "tools_directory", "/srv/tools-before"); err != nil {
		t.Fatal(err)
	}
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/move-analysis-barrier")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "barrier-analysis")
	setActiveAnalysisFFmpeg(t, ctx, database, installationID)

	tool := normalizedToolSelection(t, ctx, database, installationID, "ffprobe")
	analysis := normalizedQueuedAnalysis(t, ctx, inventory, root, location, []persistence.SourceAnalysisToolSelection{tool})
	if err := enqueueNormalizedAnalysis(t, ctx, inventory, client, analysis); err != nil {
		t.Fatalf("enqueue normalized analysis: %v", err)
	}
	move := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
		InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"/srv/tools-before","new_root":"/srv/tools-after"}`),
	}
	err := setup.CreateToolsMoveOperationAndEnqueue(ctx, move, client, serviceOperationArgs{OperationID: move.ID}, nil)
	if !errors.Is(err, persistence.ErrToolsInstallationHeldByAnalysis) {
		t.Fatalf("tools move with a committed analysis hold = %v, want ErrToolsInstallationHeldByAnalysis", err)
	}
	if moves := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'move_tools_root'"); moves != 0 {
		t.Fatalf("move operations after the refusal = %d, want 0", moves)
	}
	if jobs := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", serviceOperationArgs{}.Kind()); jobs != 0 {
		t.Fatalf("move River jobs after the refusal = %d, want 0", jobs)
	}
}

// runOperationLockRace holds the operation table lock in one transaction while
// the action runs in another. The holder commits only after the query barrier
// proved the action's transaction reached the same lock, so an implementation
// that dropped the lock could not pass by running the action after the holder.
func runOperationLockRace(t *testing.T, ctx context.Context, database *bun.DB, insert func(context.Context, bun.Tx) error, action func(context.Context) error) error {
	t.Helper()
	return runQueryRace(t, ctx, database, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE", "LOCK TABLE operation", insert, action)
}

// runQueryRace arms a query barrier before the action and blocks the action's
// transaction on the holder's lock until the barrier proves the action reached
// the guarded statement. The holder commits only then, so the action observes
// the competing state committed under the same lock; a broken implementation
// cannot pass by running the action after the holder.
func runQueryRace(t *testing.T, ctx context.Context, database *bun.DB, hold, needle string, compete func(context.Context, bun.Tx) error, action func(context.Context) error) error {
	t.Helper()
	barrier := newQueryBarrier(needle)
	database.AddQueryHook(barrier)
	locked := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHolder := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseHolder()
	holder := make(chan error, 1)
	holderPID := make(chan int, 1)
	go func() {
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			holder <- err
			close(locked)
			return
		}
		var pid int
		if err := tx.NewRaw("SELECT pg_backend_pid()").Scan(ctx, &pid); err != nil {
			_ = tx.Rollback()
			holder <- err
			holderPID <- 0
			close(locked)
			return
		}
		holderPID <- pid
		if _, err := tx.ExecContext(ctx, hold); err != nil {
			_ = tx.Rollback()
			holder <- err
			close(locked)
			return
		}
		if err := compete(ctx, tx); err != nil {
			_ = tx.Rollback()
			holder <- err
			close(locked)
			return
		}
		close(locked)
		select {
		case <-release:
			holder <- tx.Commit()
		case <-time.After(analysisRaceTimeout):
			_ = tx.Rollback()
			holder <- errors.New("the action never attempted the guarded statement")
		}
	}()
	awaitSignal(t, locked, "the competing transaction to take its lock")
	barrier.armed.Store(true)
	actionCtx, cancel := context.WithTimeout(ctx, analysisRaceTimeout)
	defer cancel()
	actionResult := make(chan error, 1)
	go func() { actionResult <- action(actionCtx) }()
	select {
	case <-barrier.attempted:
	case actionErr := <-actionResult:
		releaseHolder()
		if holderErr := awaitResult(t, holder, "the competing transaction to release after early action exit"); holderErr != nil {
			return fmt.Errorf("competing transaction after early action exit: %w", holderErr)
		}
		if actionErr == nil {
			return errors.New("the action finished before attempting the guarded statement")
		}
		return fmt.Errorf("action finished before attempting the guarded statement: %w", actionErr)
	case <-actionCtx.Done():
		releaseHolder()
		return fmt.Errorf("the action did not attempt the guarded statement: %w", actionCtx.Err())
	}
	assertPostgresLockWait(t, actionCtx, database, <-holderPID)
	releaseHolder()
	if err := awaitResult(t, holder, "the competing transaction to commit"); err != nil {
		t.Fatalf("competing transaction: %v", err)
	}
	return awaitResult(t, actionResult, "the action to finish")
}

// assertPostgresLockWait verifies the action's PostgreSQL backend is actually
// blocked by the holder. A query-hook signal alone only proves that SQL was
// issued; pg_blocking_pids proves the lock order is what delayed the action.
func assertPostgresLockWait(t *testing.T, ctx context.Context, database *bun.DB, blockingPID int) {
	t.Helper()
	if blockingPID < 1 {
		t.Fatalf("invalid PostgreSQL lock holder backend id %d", blockingPID)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked int
		err := database.NewRaw(`SELECT count(*) FROM pg_stat_activity a
			WHERE a.wait_event_type='Lock' AND ? = ANY(pg_blocking_pids(a.pid))`, blockingPID).Scan(ctx, &blocked)
		if err != nil {
			t.Fatalf("inspect PostgreSQL blocking relation: %v", err)
		}
		if blocked > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("action did not block on PostgreSQL backend %d", blockingPID)
		case <-ticker.C:
		}
	}
}

// queryBarrier signals, once armed, the first query containing needle. It is a
// bun query hook because the attempt happens before PostgreSQL grants or blocks
// the lock, which is the exact moment the race becomes deterministic.
type queryBarrier struct {
	needle    string
	armed     atomic.Bool
	once      sync.Once
	attempted chan struct{}
}

func newQueryBarrier(needle string) *queryBarrier {
	return &queryBarrier{needle: needle, attempted: make(chan struct{})}
}

func (b *queryBarrier) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if b.armed.Load() && strings.Contains(event.Query, b.needle) {
		b.once.Do(func() { close(b.attempted) })
	}
	return ctx
}

func (b *queryBarrier) AfterQuery(context.Context, *bun.QueryEvent) {}

func insertQueuedOperation(ctx context.Context, tx bun.Tx, operation *persistence.Operation) error {
	operation.Attempt = 1
	_, err := tx.NewInsert().Model(operation).Exec(ctx)
	return err
}

func assertNoAnalysisStart(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	if operations := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'analyze_source'"); operations != 0 {
		t.Fatalf("analysis operations after the refusal = %d, want 0", operations)
	}
	if jobs := analysisJobs(t, ctx, database); jobs != 0 {
		t.Fatalf("analysis River jobs after the refusal = %d, want 0", jobs)
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(analysisRaceTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func awaitResult(t *testing.T, result <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(analysisRaceTimeout):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

// serviceOperationArgs is a River args shape for a tools move the test drives
// directly.
type serviceOperationArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (serviceOperationArgs) Kind() string { return "move_tools_root_v1" }
