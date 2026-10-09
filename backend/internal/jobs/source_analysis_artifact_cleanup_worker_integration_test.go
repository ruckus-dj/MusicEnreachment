//go:build integration

package jobs

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestSourceAnalysisArtifactCleanupWorkerPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	riverClient := openCleanupWorkerRiver(t, db.DB)
	repository := persistence.NewSetupManagerRepository(db)
	settingsRepository := persistence.NewSettingsRepository(db)
	runtimeSettings := settings.New(settingsRepository, nil)
	outputRoot := t.TempDir()
	if err := runtimeSettings.SetOutputDirectory(ctx, outputRoot, t.TempDir()); err != nil {
		t.Fatalf("set managed output directory: %v", err)
	}

	rootID, locationID, workID, creatorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	creatorJobID := int64(781204)
	rootPath := "/cleanup-worker/" + uuid.NewString()
	artifactIDs := make([]uuid.UUID, 3)
	allArtifactIDs := make([]uuid.UUID, 4)
	paths := make([]string, 4)
	wantRelativePaths := make(map[uuid.UUID]string, len(artifactIDs))
	for index := range allArtifactIDs {
		allArtifactIDs[index] = uuid.New()
		paths[index] = filepath.Join("analysis", "staging", rootID.String(), workID.String(), allArtifactIDs[index].String())
		fullPath := filepath.Join(outputRoot, paths[index])
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("create staged artifact directory: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte("staged"), 0o600); err != nil {
			t.Fatalf("write staged artifact: %v", err)
		}
		if index < len(artifactIDs) {
			artifactIDs[index] = allArtifactIDs[index]
			wantRelativePaths[allArtifactIDs[index]] = filepath.ToSlash(paths[index])
		}
	}
	for _, queryArgs := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO source_root(id,configured_path,display_name) VALUES(?,?,?)`, []any{rootID, rootPath, "cleanup worker integration"}},
		{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status) VALUES(?,?,'music/song.flac',5,now(),1,'audio')`, []any{locationID, rootID}},
		{`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id) VALUES(?,?,?,?,?,'music/song.flac',5,now(),true,?)`, []any{workID, locationID, rootID, rootPath, rootPath, uuid.New()}},
		{`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id,created_at,updated_at,finished_at) VALUES(?,'analyze_source','succeeded','complete','{}',1,?,now(),now(),now())`, []any{creatorID, creatorJobID}},
		{`INSERT INTO source_analysis_work_execution(work_id,operation_id,operation_attempt,job_id,processing_mode) VALUES(?,?,1,?,'staged')`, []any{workID, creatorID, creatorJobID}},
	} {
		if _, err := db.ExecContext(ctx, queryArgs.query, queryArgs.args...); err != nil {
			t.Fatalf("insert cleanup worker fixture: %v", err)
		}
	}
	for index, artifactID := range allArtifactIDs {
		if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_artifact(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state) VALUES(?,?,?,5,now(),?,1,?,'cleanup_eligible')`, artifactID, workID, filepath.ToSlash(paths[index]), creatorID, creatorJobID); err != nil {
			t.Fatalf("register staged artifact: %v", err)
		}
	}

	cleanup := service.NewSourceAnalysisArtifactCleanup(repository, runtimeSettings, riverClient)
	operation, err := cleanup.Admit(ctx, artifactIDs)
	if err != nil {
		t.Fatalf("admit cleanup with service args factory: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("admitted cleanup operation has no River job ID")
	}
	operations := service.NewOperations(repository)
	worker := NewSourceAnalysisArtifactCleanupWorker(repository, operations, cleanup)
	if err := worker.Work(ctx, &river.Job[service.SourceAnalysisArtifactCleanupJobArgs]{
		JobRow: &rivertype.JobRow{ID: *operation.RiverJobID}, Args: service.SourceAnalysisArtifactCleanupJobArgs{OperationID: operation.ID},
	}); err != nil {
		t.Fatalf("run cleanup worker against PostgreSQL: %v", err)
	}

	stored, err := repository.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read cleanup operation: %v", err)
	}
	if stored.State != "succeeded" || stored.Stage != "finished" || stored.StartedAt == nil || stored.FinishedAt == nil {
		t.Fatalf("cleanup operation terminal fields = state %q stage %q started %v finished %v", stored.State, stored.Stage, stored.StartedAt, stored.FinishedAt)
	}
	var riverJobCount int
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE id=? AND kind=? AND args->>'operation_id'=?`, *operation.RiverJobID, service.SourceAnalysisArtifactCleanupJobKind, operation.ID.String()).Scan(ctx, &riverJobCount); err != nil {
		t.Fatalf("read admitted River job: %v", err)
	}
	if riverJobCount != 1 {
		t.Fatalf("admitted cleanup River job count = %d, want persisted args for operation %s", riverJobCount, operation.ID)
	}
	items, err := repository.ListSourceAnalysisArtifactCleanupItems(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read durable cleanup outcomes: %v", err)
	}
	if len(items) != len(artifactIDs) {
		t.Fatalf("cleanup outcome count = %d, want %d", len(items), len(artifactIDs))
	}
	for _, item := range items {
		if item.State != "succeeded" || item.SafeError != nil {
			t.Errorf("cleanup outcome for %s = state %q safe error %v", item.ArtifactID, item.State, item.SafeError)
		}
		if want := wantRelativePaths[item.ArtifactID]; item.RelativePath != want {
			t.Errorf("cleanup outcome for %s has relative path %q, want %q", item.ArtifactID, item.RelativePath, want)
		}
	}
	for index, relativePath := range paths {
		_, err := os.Lstat(filepath.Join(outputRoot, relativePath))
		if index < len(artifactIDs) {
			if !os.IsNotExist(err) {
				t.Errorf("selected artifact %d remains or returned unexpected stat error: %v", index, err)
			}
		} else if err != nil {
			t.Errorf("unselected artifact was removed or returned stat error: %v", err)
		}
	}
	var unselectedCount int
	if err := db.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=? AND state='cleanup_eligible'`, allArtifactIDs[3]).Scan(ctx, &unselectedCount); err != nil {
		t.Fatalf("check unselected artifacts: %v", err)
	}
	if unselectedCount != 1 {
		t.Fatalf("unselected cleanup-eligible artifact count = %d, want 1", unselectedCount)
	}
}

func openCleanupWorkerRiver(t *testing.T, database *sql.DB) *river.Client[*sql.Tx] {
	t.Helper()
	driver := riverdatabasesql.New(database)
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
