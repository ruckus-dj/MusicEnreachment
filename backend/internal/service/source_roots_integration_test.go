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
	database := testpostgres.OpenMigrated(t)
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
		ID: uuid.New(), Kind: "scan_source", State: state, Stage: "applying",
		InputSnapshot:      []byte(`{"source_root_id":"` + rootID.String() + `"}`),
		TargetSourceRootID: &rootID,
	}
	if err := persistence.NewSetupManagerRepository(database).CreateOperation(ctx, operation); err != nil {
		t.Fatalf("create scan operation: %v", err)
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

func TestSourceRootEditContractWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	integration := newSourceRootsIntegration(t)
	source, otherSource := t.TempDir(), t.TempDir()
	newPath := normalizedPath(t, otherSource)

	created, err := integration.roots.Create(ctx, "Music", source)
	if err != nil {
		t.Fatalf("create source root: %v", err)
	}
	configuredPath := created.ConfiguredPath
	if configuredPath != normalizedPath(t, source) || !created.Enabled || created.Stale || created.LocationCount != 0 {
		t.Fatalf("created root = %+v, want a normalized, enabled root without inventory", created)
	}
	if _, err := integration.roots.Create(ctx, "Duplicate", source); err == nil {
		t.Fatal("a second root with the same configured path was accepted")
	}

	// A successful scan gives the root an inventory the edit rules must preserve.
	seed := startScanOperation(t, ctx, integration.database, created.ID, "running")
	applyScanInventory(t, ctx, integration.inventory, seed, configuredPath, "album/track.flac")
	finishScanOperation(t, ctx, integration.database, seed.ID)
	scanned, err := integration.roots.Get(ctx, created.ID)
	if err != nil || scanned.Stale || scanned.ScanGeneration != 1 || scanned.LocationCount != 1 {
		t.Fatalf("root after its first scan = %+v, %v", scanned, err)
	}

	active := startScanOperation(t, ctx, integration.database, created.ID, "running")

	name := "Archive"
	renamed, err := integration.roots.Edit(ctx, created.ID, service.SourceRootEdit{DisplayName: &name})
	if err != nil {
		t.Fatalf("rename a root while its scan is active: %v", err)
	}
	if renamed.DisplayName != "Archive" || renamed.ConfiguredPath != configuredPath || !renamed.Enabled ||
		renamed.InventoryPath == nil || *renamed.InventoryPath != configuredPath || renamed.LocationCount != 1 {
		t.Fatalf("renamed root = %+v, want only the name changed", renamed)
	}

	disabled := false
	for _, edit := range []service.SourceRootEdit{
		{ConfiguredPath: &newPath},
		{Enabled: &disabled},
	} {
		if view, err := integration.roots.Edit(ctx, created.ID, edit); !errors.Is(err, service.ErrSourceRootBusy) {
			t.Fatalf("edit %+v while a scan is active = %+v, %v; want ErrSourceRootBusy", edit, view, err)
		}
	}
	if err := integration.roots.Delete(ctx, created.ID, configuredPath, 1); !errors.Is(err, service.ErrSourceRootBusy) {
		t.Fatalf("deletion while a scan is active = %v, want ErrSourceRootBusy", err)
	}
	if current, err := integration.roots.Get(ctx, created.ID); err != nil ||
		current.ConfiguredPath != configuredPath || !current.Enabled || current.LocationCount != 1 {
		t.Fatalf("root after the refused edits and deletion = %+v, %v; want the previous data", current, err)
	}

	// Only an idle root accepts the path change, and the previous inventory stays
	// visible as a stale inventory of the path it really describes.
	finishScanOperation(t, ctx, integration.database, active.ID)
	changed, err := integration.roots.Edit(ctx, created.ID, service.SourceRootEdit{ConfiguredPath: &newPath})
	if err != nil {
		t.Fatalf("change the configured path of an idle root: %v", err)
	}
	if changed.ConfiguredPath != newPath || !changed.Stale || changed.InventoryPath == nil ||
		*changed.InventoryPath != configuredPath || changed.ScanGeneration != 1 || changed.LocationCount != 1 {
		t.Fatalf("root after the path change = %+v, want the previous inventory reported stale", changed)
	}

	// A rejected edit must leave the root exactly as it was, including the
	// inventory of the path the root no longer describes.
	missing := filepath.Join(t.TempDir(), "absent")
	rejected := "Discarded"
	if view, err := integration.roots.Edit(ctx, created.ID, service.SourceRootEdit{
		DisplayName: &rejected, ConfiguredPath: &missing, Enabled: &disabled,
	}); err == nil || errors.Is(err, service.ErrSourceRootBusy) {
		t.Fatalf("edit with a missing path = %+v, %v; want a validation failure", view, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("the rejected source path exists after the edit: %v", err)
	}
	if current, err := integration.roots.Get(ctx, created.ID); err != nil ||
		current.DisplayName != "Archive" || current.ConfiguredPath != newPath || !current.Enabled ||
		current.InventoryPath == nil || *current.InventoryPath != configuredPath || current.LocationCount != 1 {
		t.Fatalf("root after the rejected edit = %+v, %v; want the previous data", current, err)
	}

	if err := integration.roots.Delete(ctx, created.ID, configuredPath, 1); !errors.Is(err, service.ErrSourceRootConfirmation) {
		t.Fatalf("deletion with the previous path = %v, want ErrSourceRootConfirmation", err)
	}
	if err := integration.roots.Delete(ctx, created.ID, newPath, 2); !errors.Is(err, service.ErrSourceRootConfirmation) {
		t.Fatalf("deletion with an invented location count = %v, want ErrSourceRootConfirmation", err)
	}
	if err := integration.roots.Delete(ctx, created.ID, newPath, 1); err != nil {
		t.Fatalf("confirmed deletion: %v", err)
	}
	if _, err := integration.roots.Get(ctx, created.ID); err == nil {
		t.Fatal("deleted source root is still readable")
	}
	if count, err := integration.inventory.CountSourceLocations(ctx, created.ID); err != nil || count != 0 {
		t.Fatalf("locations of the deleted root = %d, %v; want 0", count, err)
	}
}

func TestSourceRootEditRacesActiveScanWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	integration := newSourceRootsIntegration(t)
	source, otherSource := t.TempDir(), t.TempDir()
	created, err := integration.roots.Create(ctx, "Music", source)
	if err != nil {
		t.Fatalf("create source root: %v", err)
	}
	newPath := normalizedPath(t, otherSource)

	// The scan side holds the operation lock while it inserts its own scan, so
	// the edit can only reach its check after that scan is durable: the refusal is
	// a property of the lock, not of a scheduling coincidence.
	locked := make(chan struct{})
	release := make(chan struct{})
	scanResult := make(chan error, 1)
	go func() {
		scanResult <- holdActiveScan(ctx, integration.database, created.ID, locked, release)
	}()
	<-locked

	editResult := make(chan error, 1)
	go func() {
		_, err := integration.roots.Edit(ctx, created.ID, service.SourceRootEdit{ConfiguredPath: &newPath})
		editResult <- err
	}()
	close(release)

	if err := <-scanResult; err != nil {
		t.Fatalf("the scan side = %v, want it to record the active scan", err)
	}
	editErr := <-editResult
	if !errors.Is(editErr, service.ErrSourceRootBusy) {
		t.Fatalf("the racing path edit = %v, want ErrSourceRootBusy", editErr)
	}
	current, err := integration.roots.Get(ctx, created.ID)
	if err != nil || current.ConfiguredPath != created.ConfiguredPath {
		t.Fatalf("root after the raced edit = %+v, %v; want the configured path untouched", current, err)
	}

	var active uuid.UUID
	if err := integration.database.NewRaw(
		"SELECT id FROM operation WHERE target_source_root_id = ? AND state = 'running'", created.ID,
	).Scan(ctx, &active); err != nil {
		t.Fatalf("read the scan the raced edit conflicted with: %v", err)
	}
	finishScanOperation(t, ctx, integration.database, active)
	if _, err := integration.roots.Edit(ctx, created.ID, service.SourceRootEdit{ConfiguredPath: &newPath}); err != nil {
		t.Fatalf("path edit after the scan finished: %v", err)
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

	created, err := integration.roots.Create(ctx, "Music", source)
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

func TestSourceRootDeletionRechecksConfirmationAfterServiceReadWithPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"path patch", "scan inventory change"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			integration := newSourceRootsIntegration(t)
			source := t.TempDir()
			created, err := integration.roots.Create(ctx, "Music", source)
			if err != nil {
				t.Fatalf("create source root: %v", err)
			}
			initial := startScanOperation(t, ctx, integration.database, created.ID, "running")
			applyScanInventory(t, ctx, integration.inventory, initial, created.ConfiguredPath, "album/one.flac")
			finishScanOperation(t, ctx, integration.database, initial.ID)

			barrier := &sourceRootDeleteBarrier{
				SourceRootRepository: integration.inventory,
				entered:              make(chan struct{}), release: make(chan struct{}),
			}
			roots := service.NewSourceRoots(barrier, managedPathsFixture{tools: integration.tools, output: integration.output})
			deleteResult := make(chan error, 1)
			var expectedPath string
			go func() { deleteResult <- roots.Delete(ctx, created.ID, created.ConfiguredPath, 1) }()
			<-barrier.entered // GetSourceRoot and CountSourceLocations have completed.

			switch scenario {
			case "path patch":
				expectedPath = normalizedPath(t, t.TempDir())
				if _, err := integration.roots.Edit(ctx, created.ID, service.SourceRootEdit{ConfiguredPath: &expectedPath}); err != nil {
					t.Fatalf("patch configured path after deletion prechecks: %v", err)
				}
			case "scan inventory change":
				update := startScanOperation(t, ctx, integration.database, created.ID, "running")
				applyScanInventory(t, ctx, integration.inventory, update, created.ConfiguredPath,
					"album/one.flac", "album/two.flac")
				finishScanOperation(t, ctx, integration.database, update.ID)
			}
			close(barrier.release)
			if err := <-deleteResult; !errors.Is(err, service.ErrSourceRootConfirmation) {
				t.Fatalf("deletion after %s changed between precheck and transaction = %v, want confirmation conflict", scenario, err)
			}
			current, err := integration.inventory.GetSourceRoot(ctx, created.ID)
			if err != nil {
				t.Fatalf("read root after refused deletion: %v", err)
			}
			wantCount := int64(1)
			if scenario == "path patch" {
				if current.ConfiguredPath != expectedPath {
					t.Fatalf("configured path after refused deletion = %q, want competing patch %q", current.ConfiguredPath, expectedPath)
				}
			} else {
				wantCount = 2
			}
			count, err := integration.inventory.CountSourceLocations(ctx, created.ID)
			if err != nil || count != wantCount {
				t.Fatalf("locations after refused deletion = %d, %v; want %d", count, err, wantCount)
			}
		})
	}
}

// The inverse of the edit-before-delete case: deletion must get the operation
// table lock before a competing root PATCH or scan inventory apply. Once deletion
// commits, the waiting writer must observe the missing root, never recreate it.
func TestSourceRootDeletionSerializesWritersWithPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, writer := range []string{"path patch", "scan inventory apply"} {
		t.Run(writer, func(t *testing.T) {
			baseCtx := context.Background()
			integration := newSourceRootsIntegration(t)
			created, err := integration.roots.Create(baseCtx, "Music", t.TempDir())
			if err != nil {
				t.Fatalf("create source root: %v", err)
			}
			seed := startScanOperation(t, baseCtx, integration.database, created.ID, "running")
			applyScanInventory(t, baseCtx, integration.inventory, seed, created.ConfiguredPath, "album/one.flac")
			finishScanOperation(t, baseCtx, integration.database, seed.ID)

			// This transaction owns the real lock used by deletion. The query
			// barrier below proves the writer reached PostgreSQL and is blocked.
			lockCtx, cancelLock := context.WithTimeout(baseCtx, 10*time.Second)
			defer cancelLock()
			locked, release := make(chan struct{}), make(chan struct{})
			deleteResult := make(chan error, 1)
			go func() {
				deleteResult <- holdSourceRootDeletion(lockCtx, integration.database, created.ID, locked, release)
			}()
			select {
			case <-locked:
			case <-lockCtx.Done():
				t.Fatal("deletion did not acquire its operation lock")
			}

			// For a PATCH, Get/validation precedes UpdateSourceRoot. For an
			// inventory writer, begin its transaction with the same operation lock.
			writerStarted := make(chan error, 1)
			writerCtx, cancelWriter := context.WithTimeout(baseCtx, 10*time.Second)
			defer cancelWriter()
			newPath := normalizedPath(t, t.TempDir())
			go func() {
				if writer == "path patch" {
					_, err := integration.roots.Edit(writerCtx, created.ID, service.SourceRootEdit{ConfiguredPath: &newPath})
					writerStarted <- err
					return
				}
				writerStarted <- integration.inventory.ApplySourceScan(writerCtx, persistence.SourceScanApply{
					OperationID: seed.ID, ExpectedConfiguredPath: created.ConfiguredPath,
				})
			}()
			// Ensure writer attempt is observable without timing assumptions: wait
			// until PostgreSQL reports an ungranted conflicting lock for its backend.
			waitForBlockedWriter(t, writerCtx, integration.database, created.ID)

			close(release)
			if err := <-deleteResult; err != nil {
				t.Fatalf("deletion transaction: %v", err)
			}
			if err := <-writerStarted; err == nil {
				t.Fatal("writer succeeded after deletion; it must report not found/conflict")
			}
			var count int
			if err := integration.database.NewRaw("SELECT count(*) FROM source_root WHERE id = ?", created.ID).Scan(baseCtx, &count); err != nil || count != 0 {
				t.Fatalf("root count after competing writer = %d, %v; want deleted", count, err)
			}
		})
	}
}

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
