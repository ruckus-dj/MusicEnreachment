//go:build integration

package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type sourceScanStartIntegration struct {
	scans     *service.SourceScanOperations
	roots     *service.SourceRoots
	inventory *persistence.SourceInventoryRepository
	registry  *settings.Registry
	client    *river.Client[*sql.Tx]
	database  *bun.DB
	root      service.SourceRoot
	source    string
}

func newSourceScanStartIntegration(t *testing.T, platform settings.PlatformState, completeSetup bool) sourceScanStartIntegration {
	t.Helper()
	ctx := context.Background()
	database := testpostgres.OpenMigrated(t)
	client := openSourceScanRiver(t, database)
	registry := settings.New(persistence.NewSettingsRepository(database), nil)
	if completeSetup {
		if err := registry.CompleteSetup(ctx); err != nil {
			t.Fatalf("complete setup: %v", err)
		}
	}
	inventory := persistence.NewSourceInventoryRepository(database)
	tools, output, source := t.TempDir(), t.TempDir(), t.TempDir()
	roots := service.NewSourceRoots(inventory, managedPathsFixture{tools: tools, output: output})
	root, err := roots.Create(ctx, "Music", source, "in_place")
	if err != nil {
		t.Fatalf("create the source root: %v", err)
	}
	return sourceScanStartIntegration{
		scans: service.NewSourceScanOperations(inventory, roots, registry, platform, client),
		roots: roots, inventory: inventory, registry: registry, client: client,
		database: database, root: root, source: source,
	}
}

// openSourceScanRiver applies River's own schema and returns an insert-only
// client: a scan start needs the real river_job transaction the production
// client writes, and nothing in these tests runs a worker.
func openSourceScanRiver(t *testing.T, database *bun.DB) *river.Client[*sql.Tx] {
	t.Helper()
	driver := riverdatabasesql.New(database.DB)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatalf("create River migrator: %v", err)
	}
	if _, err := migrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}
	client, err := river.NewClient(driver, &river.Config{})
	if err != nil {
		t.Fatalf("create insert-only River client: %v", err)
	}
	return client
}

type scanOperationRow struct {
	Kind               string          `bun:"kind"`
	State              string          `bun:"state"`
	Stage              string          `bun:"stage"`
	InputSnapshot      json.RawMessage `bun:"input_snapshot"`
	TargetSourceRootID *uuid.UUID      `bun:"target_source_root_id,type:uuid"`
	RiverJobID         *int64          `bun:"river_job_id"`
	Attempt            int             `bun:"attempt"`
}

func readScanOperationRow(t *testing.T, ctx context.Context, database *bun.DB, id uuid.UUID) scanOperationRow {
	t.Helper()
	row := scanOperationRow{}
	if err := database.NewRaw(`SELECT kind, state, stage, input_snapshot, target_source_root_id, river_job_id, attempt
		FROM operation WHERE id = ?`, id).Scan(ctx, &row); err != nil {
		t.Fatalf("read operation %s: %v", id, err)
	}
	return row
}

type scanJobRow struct {
	Kind string `bun:"kind"`
	Args string `bun:"args"`
}

func readScanJobRow(t *testing.T, ctx context.Context, database *bun.DB, id int64) scanJobRow {
	t.Helper()
	row := scanJobRow{}
	if err := database.NewRaw("SELECT kind, args::text AS args FROM river_job WHERE id = ?", id).Scan(ctx, &row); err != nil {
		t.Fatalf("read River job %d: %v", id, err)
	}
	return row
}

func countScanStartRows(t *testing.T, ctx context.Context, database *bun.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := database.NewRaw(query, args...).Scan(ctx, &count); err != nil {
		t.Fatalf("count rows (%s): %v", query, err)
	}
	return count
}

// logScanStartState writes the durable rows a scan start produced into the test
// log, so a verbose verification run shows the operation, its snapshot and the
// River job that carries it.
func logScanStartState(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) {
	t.Helper()
	rows := make([]scanOperationRow, 0)
	if err := database.NewRaw(`SELECT kind, state, stage, input_snapshot, target_source_root_id, river_job_id, attempt
		FROM operation WHERE kind = 'scan_source' ORDER BY created_at`).Scan(ctx, &rows); err != nil {
		t.Fatalf("read the scan operations: %v", err)
	}
	for _, row := range rows {
		if row.TargetSourceRootID == nil || *row.TargetSourceRootID != rootID {
			continue
		}
		job := "no River job"
		if row.RiverJobID != nil {
			jobRow := readScanJobRow(t, ctx, database, *row.RiverJobID)
			job = fmt.Sprintf("river job %d kind=%s args=%s", *row.RiverJobID, jobRow.Kind, jobRow.Args)
		}
		t.Logf("operation kind=%s state=%s stage=%s attempt=%d snapshot=%s %s",
			row.Kind, row.State, row.Stage, row.Attempt, row.InputSnapshot, job)
	}
	t.Logf("scan_source operations of the root=%d river jobs=%d",
		scanOperationsOfRoot(t, ctx, database, rootID), scanSourceJobs(t, ctx, database))
}

func scanOperationsOfRoot(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) int {
	t.Helper()
	return countScanStartRows(t, ctx, database,
		"SELECT count(*) FROM operation WHERE kind = 'scan_source' AND target_source_root_id = ?", rootID)
}

func scanSourceJobs(t *testing.T, ctx context.Context, database *bun.DB) int {
	t.Helper()
	return countScanStartRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind)
}

// TestSourceScanStartEnqueuesTheOperationAndItsJobWithPostgreSQL drives a start
// against real PostgreSQL and a real River job table: the operation and its job
// describe the root and nothing else, the start writes no inventory, and the
// source directory is left exactly as the operator has it.
func TestSourceScanStartEnqueuesTheOperationAndItsJobWithPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newSourceScanStartIntegration(t, supportedScanStartPlatform(), true)
	ctx := context.Background()
	track := filepath.Join(fixture.source, "album", "track.flac")
	writeSourceWalkFile(t, track, "audio bytes")

	operation, err := fixture.scans.Start(ctx, fixture.root.ID)
	if err != nil {
		t.Fatalf("start a scan: %v", err)
	}

	row := readScanOperationRow(t, ctx, fixture.database, operation.ID)
	if row.Kind != service.SourceScanOperationKind || row.State != "queued" ||
		row.Stage != service.SourceScanStageQueued || row.Attempt != 1 {
		t.Fatalf("stored operation = %+v, want a first-attempt queued scan", row)
	}
	if row.TargetSourceRootID == nil || *row.TargetSourceRootID != fixture.root.ID {
		t.Fatalf("stored operation target = %v, want %s", row.TargetSourceRootID, fixture.root.ID)
	}
	if row.RiverJobID == nil || operation.RiverJobID == nil || *row.RiverJobID != *operation.RiverJobID {
		t.Fatalf("stored River job id = %v, want the returned %v", row.RiverJobID, operation.RiverJobID)
	}
	var snapshot service.ScanSourceSnapshot
	if err := json.Unmarshal(row.InputSnapshot, &snapshot); err != nil {
		t.Fatalf("decode the stored snapshot %s: %v", row.InputSnapshot, err)
	}
	want := service.ScanSourceSnapshot{
		SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: fixture.root.ID,
		ConfiguredPath: fixture.root.ConfiguredPath,
		ScanGeneration: fixture.root.ScanGeneration,
		SHA256Enabled:  new(true),
		Tools:          []persistence.SourceAnalysisToolSelection{},
	}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("stored snapshot = %+v, want %+v", snapshot, want)
	}

	job := readScanJobRow(t, ctx, fixture.database, *row.RiverJobID)
	if job.Kind != service.SourceScanJobKind {
		t.Fatalf("stored River job kind = %q, want %q", job.Kind, service.SourceScanJobKind)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(job.Args), &args); err != nil {
		t.Fatalf("decode the stored River args %s: %v", job.Args, err)
	}
	if !reflect.DeepEqual(args, map[string]any{"operation_id": operation.ID.String()}) {
		t.Fatalf("stored River args = %v, want the operation id alone", args)
	}

	if operations := scanOperationsOfRoot(t, ctx, fixture.database, fixture.root.ID); operations != 1 {
		t.Fatalf("scan operations of the root = %d, want 1", operations)
	}
	if jobs := scanSourceJobs(t, ctx, fixture.database); jobs != 1 {
		t.Fatalf("scan River jobs = %d, want 1", jobs)
	}
	if locations := countScanStartRows(t, ctx, fixture.database,
		"SELECT count(*) FROM source_location WHERE source_root_id = ?", fixture.root.ID); locations != 0 {
		t.Fatalf("source locations after the start = %d, want the start to write no inventory", locations)
	}
	stored, err := fixture.inventory.GetSourceRoot(ctx, fixture.root.ID)
	if err != nil || stored.ScanGeneration != 0 || stored.InventoryPath != nil || stored.Status != "unknown" {
		t.Fatalf("root after the start = %+v, %v; want it untouched", stored, err)
	}
	entries, err := os.ReadDir(fixture.source)
	if err != nil {
		t.Fatalf("read the source directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "album" {
		t.Fatalf("source directory entries = %v, want only the album the operator has", entries)
	}
	logScanStartState(t, ctx, fixture.database, fixture.root.ID)
}

// TestSourceScanStartRefusesASecondScanOfTheSameRootWithPostgreSQL proves the
// duplicate guard is the stored state: a second start creates neither an
// operation nor a job, and the root accepts a new scan once the first one is
// terminal.
func TestSourceScanStartRefusesASecondScanOfTheSameRootWithPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newSourceScanStartIntegration(t, supportedScanStartPlatform(), true)
	ctx := context.Background()

	first, err := fixture.scans.Start(ctx, fixture.root.ID)
	if err != nil {
		t.Fatalf("start the first scan: %v", err)
	}
	second, err := fixture.scans.Start(ctx, fixture.root.ID)
	if !errors.Is(err, service.ErrSourceRootBusy) {
		t.Fatalf("second start = %+v, %v; want ErrSourceRootBusy", second, err)
	}
	if second != nil {
		t.Fatalf("refused second start returned %+v, want no operation", second)
	}
	if operations := scanOperationsOfRoot(t, ctx, fixture.database, fixture.root.ID); operations != 1 {
		t.Fatalf("scan operations after the refused second start = %d, want 1", operations)
	}
	if jobs := scanSourceJobs(t, ctx, fixture.database); jobs != 1 {
		t.Fatalf("scan River jobs after the refused second start = %d, want 1", jobs)
	}
	if row := readScanOperationRow(t, ctx, fixture.database, first.ID); row.State != "queued" {
		t.Fatalf("the running scan state = %q after the refused start, want queued", row.State)
	}

	finishScanOperation(t, ctx, fixture.database, first.ID)
	third, err := fixture.scans.Start(ctx, fixture.root.ID)
	if err != nil {
		t.Fatalf("start a scan after the first one finished: %v", err)
	}
	if third.ID == first.ID {
		t.Fatalf("the new scan reused the finished operation %s", first.ID)
	}
	if operations := scanOperationsOfRoot(t, ctx, fixture.database, fixture.root.ID); operations != 2 {
		t.Fatalf("scan operations after the finished scan = %d, want 2", operations)
	}
	if jobs := scanSourceJobs(t, ctx, fixture.database); jobs != 2 {
		t.Fatalf("scan River jobs after the finished scan = %d, want 2", jobs)
	}
	logScanStartState(t, ctx, fixture.database, fixture.root.ID)
}

// TestSourceScanStartRefusesDisabledRootUnfinishedSetupAndAnUnusableInstance
// covers every refusal that is decided before a scan is recorded: an unfinished
// Setup, a disabled root, an unusable platform and a configured path that no
// longer exists. None of them may leave an operation, a job or a reverted root
// behind.
func TestSourceScanStartRefusesDisabledRootUnfinishedSetupAndAnUnusableInstance(t *testing.T) {
	t.Parallel()
	fixture := newSourceScanStartIntegration(t, supportedScanStartPlatform(), true)
	ctx := context.Background()

	// The Setup is unfinished while its completion row is absent from the very
	// database the registry reads, and it is completed again for the rest of the
	// refusals.
	if _, err := fixture.database.NewDelete().Model((*persistence.AppSetting)(nil)).
		Where("setting_name = ?", settings.SetupCompletedAtKey).Exec(ctx); err != nil {
		t.Fatalf("remove the setup completion row: %v", err)
	}
	operation, err := fixture.scans.Start(ctx, fixture.root.ID)
	if !errors.Is(err, service.ErrSourceScanNotReady) {
		t.Fatalf("start with an unfinished setup = %+v, %v; want ErrSourceScanNotReady", operation, err)
	}
	if err := fixture.registry.CompleteSetup(ctx); err != nil {
		t.Fatalf("complete the setup again: %v", err)
	}

	disabled := false
	if _, err := fixture.roots.Edit(ctx, fixture.root.ID, service.SourceRootEdit{Enabled: &disabled}); err != nil {
		t.Fatalf("disable the source root: %v", err)
	}
	operation, err = fixture.scans.Start(ctx, fixture.root.ID)
	if !errors.Is(err, service.ErrSourceScanDisabled) {
		t.Fatalf("start on a disabled root = %+v, %v; want ErrSourceScanDisabled", operation, err)
	}

	diagnostic := service.NewSourceScanOperations(fixture.inventory, fixture.roots, fixture.registry,
		settings.PlatformState{
			Platform:   settings.Platform{GOOS: "windows", GOARCH: "arm64"},
			Diagnostic: true, Reason: "instance platform differs from current process",
		}, fixture.client)
	operation, err = diagnostic.Start(ctx, fixture.root.ID)
	if !errors.Is(err, service.ErrSourceScanNotReady) {
		t.Fatalf("start on an unusable instance platform = %+v, %v; want ErrSourceScanNotReady", operation, err)
	}

	if err := os.RemoveAll(fixture.source); err != nil {
		t.Fatalf("remove the source directory: %v", err)
	}
	operation, err = fixture.scans.Start(ctx, fixture.root.ID)
	if err == nil || operation != nil {
		t.Fatalf("start on a missing source directory = %+v, %v; want a refusal", operation, err)
	}
	if errors.Is(err, service.ErrSourceRootBusy) || errors.Is(err, service.ErrSourceScanDisabled) ||
		errors.Is(err, service.ErrSourceScanNotReady) {
		t.Fatalf("start on a missing source directory = %v, want the path failure itself", err)
	}
	unavailable, err := fixture.inventory.GetSourceRoot(ctx, fixture.root.ID)
	if err != nil {
		t.Fatalf("read the root after the path refusal: %v", err)
	}
	if unavailable.Status != persistence.SourceRootStatusUnavailable || unavailable.SafeError == nil ||
		*unavailable.SafeError != service.SourceScanDirectoryUnavailableReason {
		t.Fatalf("root after the path refusal = %s/%v, want unavailable with the safe reason", unavailable.Status, unavailable.SafeError)
	}

	if operations := scanOperationsOfRoot(t, ctx, fixture.database, fixture.root.ID); operations != 0 {
		t.Fatalf("scan operations after the refusals = %d, want 0", operations)
	}
	if jobs := scanSourceJobs(t, ctx, fixture.database); jobs != 0 {
		t.Fatalf("scan River jobs after the refusals = %d, want 0", jobs)
	}
	stored, err := fixture.inventory.GetSourceRoot(ctx, fixture.root.ID)
	if err != nil || stored.Enabled || stored.ScanGeneration != 0 || stored.InventoryPath != nil ||
		stored.Status != persistence.SourceRootStatusUnavailable || stored.SafeError == nil {
		t.Fatalf("root after the refusals = %+v, %v; want only the disabled flag and the proven unavailability", stored, err)
	}
	logScanStartState(t, ctx, fixture.database, fixture.root.ID)
}

// TestSourceScanStartRollsBackAFailedRiverInsertWithPostgreSQL fails the job
// insert of a real start: the refusal must leave neither an operation nor a job,
// and it must release the root so the next start succeeds.
func TestSourceScanStartRollsBackAFailedRiverInsertWithPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newSourceScanStartIntegration(t, supportedScanStartPlatform(), true)
	ctx := context.Background()
	unreachable := errors.New("River is unreachable")
	scans := service.NewSourceScanOperations(fixture.inventory, fixture.roots, fixture.registry,
		supportedScanStartPlatform(), &riverInserterFixture{err: unreachable})

	operation, err := scans.Start(ctx, fixture.root.ID)
	if !errors.Is(err, unreachable) {
		t.Fatalf("start with a failing River insert = %+v, %v; want the insert failure", operation, err)
	}
	if operation != nil {
		t.Fatalf("failed start returned the operation %+v, want none", operation)
	}
	if operations := scanOperationsOfRoot(t, ctx, fixture.database, fixture.root.ID); operations != 0 {
		t.Fatalf("scan operations after the failed insert = %d, want an orphan-free 0", operations)
	}
	if jobs := scanSourceJobs(t, ctx, fixture.database); jobs != 0 {
		t.Fatalf("scan River jobs after the failed insert = %d, want 0", jobs)
	}

	started, err := fixture.scans.Start(ctx, fixture.root.ID)
	if err != nil {
		t.Fatalf("start after the failed insert: %v", err)
	}
	if started.RiverJobID == nil {
		t.Fatalf("recovered start %+v has no River job", started)
	}
	if operations, jobs := scanOperationsOfRoot(t, ctx, fixture.database, fixture.root.ID), scanSourceJobs(t, ctx, fixture.database); operations != 1 || jobs != 1 {
		t.Fatalf("rows after the recovered start = %d operations, %d jobs; want one of each", operations, jobs)
	}
	logScanStartState(t, ctx, fixture.database, fixture.root.ID)
}

// TestSourceScanStartSerializesWithRootDeletionWithPostgreSQL pins both
// interleavings of a start and a deletion of the same root. Each side holds the
// operation table lock the other needs until the test releases it, so the
// outcome is decided by the lock and not by scheduling: a scan is never
// recorded for a deleted root, and a root with an active scan is never deleted.
func TestSourceScanStartSerializesWithRootDeletionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newSourceScanStartIntegration(t, supportedScanStartPlatform(), true)
	ctx := context.Background()

	locked, release := make(chan struct{}), make(chan struct{})
	deletion := make(chan error, 1)
	go func() {
		deletion <- holdSourceRootDeletion(ctx, fixture.database, fixture.root.ID, locked, release)
	}()
	<-locked

	started := make(chan *persistence.Operation, 1)
	startResult := make(chan error, 1)
	go func() {
		operation, err := fixture.scans.Start(ctx, fixture.root.ID)
		started <- operation
		startResult <- err
	}()
	close(release)

	if err := <-deletion; err != nil {
		t.Fatalf("the deletion side = %v, want it to delete the root", err)
	}
	operation, err := <-started, <-startResult
	if err == nil || operation != nil {
		t.Fatalf("start against the deleted root = %+v, %v; want a refusal", operation, err)
	}
	if remaining := countScanStartRows(t, ctx, fixture.database,
		"SELECT count(*) FROM source_root WHERE id = ?", fixture.root.ID); remaining != 0 {
		t.Fatalf("roots left after the deletion = %d, want 0", remaining)
	}
	if operations := scanOperationsOfRoot(t, ctx, fixture.database, fixture.root.ID); operations != 0 {
		t.Fatalf("scan operations of the deleted root = %d, want 0", operations)
	}
	if jobs := scanSourceJobs(t, ctx, fixture.database); jobs != 0 {
		t.Fatalf("scan River jobs after the refused start = %d, want 0", jobs)
	}

	other, err := fixture.roots.Create(ctx, "Archive", t.TempDir(), "in_place")
	if err != nil {
		t.Fatalf("create the second source root: %v", err)
	}
	lockedOther, releaseOther := make(chan struct{}), make(chan struct{})
	scanResult := make(chan error, 1)
	go func() {
		scanResult <- holdActiveScan(ctx, fixture.database, other.ID, lockedOther, releaseOther)
	}()
	<-lockedOther

	deleteStarted := make(chan struct{})
	deleteResult := make(chan error, 1)
	go func() {
		close(deleteStarted)
		deleteResult <- fixture.roots.Delete(ctx, other.ID, other.ConfiguredPath, 0)
	}()
	<-deleteStarted
	close(releaseOther)

	if err := <-scanResult; err != nil {
		t.Fatalf("the scan side = %v, want it to record the active scan", err)
	}
	if err := <-deleteResult; !errors.Is(err, service.ErrSourceRootBusy) {
		t.Fatalf("deletion racing a scan that became durable first = %v, want ErrSourceRootBusy", err)
	}
	if remaining := countScanStartRows(t, ctx, fixture.database,
		"SELECT count(*) FROM source_root WHERE id = ?", other.ID); remaining != 1 {
		t.Fatalf("roots left after the refused deletion = %d, want 1", remaining)
	}
	if operations := countScanStartRows(t, ctx, fixture.database,
		"SELECT count(*) FROM operation WHERE target_source_root_id = ? AND state = 'running'", other.ID); operations != 1 {
		t.Fatalf("active scans of the surviving root = %d, want 1", operations)
	}
	logScanStartState(t, ctx, fixture.database, other.ID)
}

// holdSourceRootDeletion deletes a root in a transaction that keeps the
// operation table lock until the test releases it, which is the lock a scan
// start takes before it reads the root.
func holdSourceRootDeletion(ctx context.Context, database *bun.DB, rootID uuid.UUID, locked chan struct{}, release chan struct{}) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		_ = transaction.Rollback()
		return err
	}
	result, err := transaction.NewDelete().Model((*persistence.SourceRoot)(nil)).Where("id = ?", rootID).Exec(ctx)
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		_ = transaction.Rollback()
		return errors.New("the root to delete does not exist")
	}
	close(locked)
	<-release
	return transaction.Commit()
}
