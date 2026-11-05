//go:build integration

package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type sourceRootsIntegration struct {
	roots     *service.SourceRoots
	inventory *persistence.SourceInventoryRepository
	database  *bun.DB
	tools     string
	output    string
}

type sourceRootDeleteBarrier struct {
	service.SourceRootRepository
	entered chan struct{}
	release chan struct{}
}

func (repository *sourceRootDeleteBarrier) DeleteSourceRoot(ctx context.Context, id uuid.UUID, confirmedPath string, confirmedLocations int64) error {
	close(repository.entered)
	<-repository.release
	return repository.SourceRootRepository.DeleteSourceRoot(ctx, id, confirmedPath, confirmedLocations)
}

func newSourceRootsIntegration(t *testing.T) sourceRootsIntegration {
	t.Helper()
	database := testpostgres.OpenMigratedWithRiver(t)
	inventory := persistence.NewSourceInventoryRepository(database)
	tools, output := t.TempDir(), t.TempDir()
	return sourceRootsIntegration{
		roots:     service.NewSourceRoots(inventory, managedPathsFixture{tools: tools, output: output}),
		inventory: inventory,
		database:  database,
		tools:     tools,
		output:    output,
	}
}

// startScanOperation inserts a queued or running scan of the root, which is the
// durable form of an active scan the state machine in the worker will use.
func startScanOperation(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID, state string) *persistence.Operation {
	t.Helper()
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: "queued", Attempt: 1,
		InputSnapshot:      []byte(`{"source_root_id":"` + rootID.String() + `"}`),
		TargetSourceRootID: &rootID,
	}
	client := openSourceScanRiver(t, database)
	if err := persistence.NewSourceInventoryRepository(database).CreateSourceScanOperationAndEnqueue(
		ctx, operation, client, service.ScanSourceJobArgs{OperationID: operation.ID}, nil,
	); err != nil {
		t.Fatalf("enqueue scan operation: %v", err)
	}
	if state != "queued" {
		if _, err := database.NewUpdate().Model((*persistence.Operation)(nil)).
			Set("state = ?", state).Set("stage = 'applying'").Set("updated_at = now()").
			Where("id = ?", operation.ID).Exec(ctx); err != nil {
			t.Fatalf("set scan operation state: %v", err)
		}
		operation.State, operation.Stage = state, "applying"
	}
	return operation
}

func finishScanOperation(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.Operation)(nil)).
		Set("state = 'succeeded'").Set("finished_at = now()").Set("updated_at = now()").
		Where("id = ?", operationID).Exec(ctx); err != nil {
		t.Fatalf("finish scan operation %s: %v", operationID, err)
	}
}

// applyScanInventory is the traversal contract of a successful scan: the
// candidates are stored for the operation and then applied as a generation.
func applyScanInventory(t *testing.T, ctx context.Context, inventory *persistence.SourceInventoryRepository, operation *persistence.Operation, configuredPath string, relativePaths ...string) {
	t.Helper()
	candidates := make([]persistence.SourceScanCandidateInput, 0, len(relativePaths))
	for _, relativePath := range relativePaths {
		candidates = append(candidates, persistence.SourceScanCandidateInput{
			RelativePath: relativePath, SizeBytes: 1024,
			Mtime: time.Now().UTC().Truncate(time.Microsecond), ProbeStatus: persistence.SourceProbeStatusAudio,
		})
	}
	if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, candidates); err != nil {
		t.Fatalf("store candidates of operation %s: %v", operation.ID, err)
	}
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: operation.ID, ExpectedConfiguredPath: configuredPath,
		ExpectedAttempt: operation.Attempt, ExpectedJobID: *operation.RiverJobID,
	}); err != nil {
		t.Fatalf("apply the scan of operation %s: %v", operation.ID, err)
	}
}

func writeSourceFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRootModeEditSerializesOnRootRowWithPostgreSQL(t *testing.T) {
	t.Parallel()
	baseCtx := context.Background()
	integration := newSourceRootsIntegration(t)
	created, err := integration.roots.Create(baseCtx, "Music", t.TempDir(), service.SourceProcessingModeInPlace)
	if err != nil {
		t.Fatalf("create source root: %v", err)
	}

	lockCtx, cancelLock := context.WithTimeout(baseCtx, 10*time.Second)
	defer cancelLock()
	transaction, err := integration.database.BeginTx(lockCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()
	var backendPID int
	if err := transaction.NewRaw("SELECT pg_backend_pid() FROM source_root WHERE id = ? FOR UPDATE", created.ID).
		Scan(lockCtx, &backendPID); err != nil {
		t.Fatalf("lock source root row: %v", err)
	}

	staged := service.SourceProcessingModeStaged
	editResult := make(chan error, 1)
	writerCtx, cancelWriter := context.WithTimeout(baseCtx, 10*time.Second)
	defer cancelWriter()
	go func() {
		_, err := integration.roots.Edit(writerCtx, created.ID, service.SourceRootEdit{ProcessingMode: &staged})
		editResult <- err
	}()
	waitForRootRowLockWait(t, lockCtx, integration.database, backendPID)
	if err := transaction.Commit(); err != nil {
		t.Fatalf("release root row lock: %v", err)
	}
	if err := <-editResult; err != nil {
		t.Fatalf("mode edit after serialized row lock: %v", err)
	}
	current, err := integration.roots.Get(baseCtx, created.ID)
	if err != nil || current.ProcessingMode != staged {
		t.Fatalf("persisted mode after concurrent mutation = %+v, %v; want staged", current, err)
	}
}

func waitForRootRowLockWait(t *testing.T, ctx context.Context, database *bun.DB, blockingPID int) {
	t.Helper()
	for {
		var waiting bool
		err := database.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND ? = ANY(pg_blocking_pids(pid))
		)`, blockingPID).Scan(ctx, &waiting)
		if err != nil {
			t.Fatalf("observe root row lock waiter: %v", err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("mode edit did not wait for the locked source root row: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func holdActiveScan(ctx context.Context, database *bun.DB, rootID uuid.UUID, locked chan struct{}, release chan struct{}) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		_ = transaction.Rollback()
		return err
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "scan_source", State: "running", Stage: "traversing",
		InputSnapshot:      []byte(`{"source_root_id":"` + rootID.String() + `"}`),
		TargetSourceRootID: &rootID,
	}
	if err := persistence.NewSetupManagerRepository(database).CreateOperationWith(ctx, transaction, operation); err != nil {
		_ = transaction.Rollback()
		return err
	}
	close(locked)
	<-release
	return transaction.Commit()
}

func TestSourceRootDeletionLeavesFilesInPlaceWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	integration := newSourceRootsIntegration(t)
	source := t.TempDir()
	track := filepath.Join(source, "album", "track.flac")
	published := filepath.Join(integration.output, "album", "track.flac")
	executable := filepath.Join(integration.tools, "ffmpeg", "8.0", "ffmpeg")
	for path, contents := range map[string]string{track: "source bytes", published: "published bytes", executable: "binary"} {
		writeSourceFile(t, path, contents)
	}

	created, err := integration.roots.Create(ctx, "Music", source, "in_place")
	if err != nil {
		t.Fatalf("create source root: %v", err)
	}
	seed := startScanOperation(t, ctx, integration.database, created.ID, "running")
	applyScanInventory(t, ctx, integration.inventory, seed, created.ConfiguredPath, "album/track.flac")
	finishScanOperation(t, ctx, integration.database, seed.ID)

	if err := integration.roots.Delete(ctx, created.ID, normalizedPath(t, t.TempDir()), 1); !errors.Is(err, service.ErrSourceRootConfirmation) {
		t.Fatalf("deletion with another configured path = %v, want ErrSourceRootConfirmation", err)
	}
	active := startScanOperation(t, ctx, integration.database, created.ID, "queued")
	if err := integration.roots.Delete(ctx, created.ID, created.ConfiguredPath, 1); !errors.Is(err, service.ErrSourceRootBusy) {
		t.Fatalf("deletion while a scan is queued = %v, want ErrSourceRootBusy", err)
	}
	finishScanOperation(t, ctx, integration.database, active.ID)

	if err := integration.roots.Delete(ctx, created.ID, created.ConfiguredPath, 1); err != nil {
		t.Fatalf("confirmed deletion: %v", err)
	}
	if count, err := integration.inventory.CountSourceLocations(ctx, created.ID); err != nil || count != 0 {
		t.Fatalf("locations after the deletion = %d, %v; want the inventory rows gone", count, err)
	}
	for path, contents := range map[string]string{track: "source bytes", published: "published bytes", executable: "binary"} {
		stored, err := os.ReadFile(path)
		if err != nil || string(stored) != contents {
			t.Fatalf("file %q after the root deletion = %q, %v; want %q kept", path, stored, err, contents)
		}
	}
}

// The inverse of the edit-before-delete case: deletion must get the operation
// table lock before a competing root PATCH or scan inventory apply. Once deletion
// commits, the waiting writer must observe the missing root, never recreate it.

func waitForBlockedWriter(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) {
	t.Helper()
	// pg_stat_activity is a database-side barrier, not a sleep: the query returns
	// only once another backend is actively waiting on the operation-table lock.
	for {
		var waiting bool
		err := database.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
		)`).Scan(ctx, &waiting)
		if err == nil && waiting {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("writer did not block on operation lock for root %s: %v", rootID, ctx.Err())
		}
		// Repeated database observation is bounded by context deadline; no
		// scheduling-dependent sleep is used.
		if _, err := database.ExecContext(ctx, "SELECT 1"); err != nil {
			t.Fatalf("observe blocked writer: %v", err)
		}
	}
}
