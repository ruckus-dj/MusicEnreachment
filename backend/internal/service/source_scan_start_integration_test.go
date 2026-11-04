//go:build integration

package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
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
	database := testpostgres.OpenMigratedWithRiver(t)
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
	writeSourceScanFile(t, fixture.source, filepath.Join("album", "track.flac"), "audio bytes")

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
