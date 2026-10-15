//go:build integration

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestSourceAnalysisStepInputStorageCurrentSchemaWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	step := insertStorageStep(t, ctx, database, "sha256")
	assertStorageStepInputIsNull(t, ctx, database, step)
	input := `{"sha256_enabled":true}`
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET input_snapshot=?::jsonb WHERE work_id=? AND step='sha256'`, input, step.WorkID); err != nil {
		t.Fatalf("store step input: %v", err)
	}
	var preserved int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE work_id=? AND step='sha256' AND input_snapshot=?::jsonb`, step.WorkID, input).Scan(ctx, &preserved); err != nil {
		t.Fatalf("read stored step input: %v", err)
	}
	if preserved != 1 {
		t.Fatal("stored step input was not persisted by current schema")
	}
}

func insertStorageStep(t *testing.T, ctx context.Context, database *bun.DB, step string) persistence.SourceAnalysisStep {
	t.Helper()
	workID := insertStorageWork(t, ctx, database)
	row := persistence.SourceAnalysisStep{WorkID: workID, Step: step, State: "pending"}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state) VALUES(?,?,?)`, row.WorkID, row.Step, row.State); err != nil {
		t.Fatalf("insert analysis step: %v", err)
	}
	return row
}

func insertStorageWork(t *testing.T, ctx context.Context, database *bun.DB) uuid.UUID {
	t.Helper()
	path := "/srv/step-input-storage-" + uuid.NewString()
	rootID := newVariantRoot(t, ctx, database, path)
	locationID := newVariantLocation(t, ctx, database, rootID, "album/track.flac")
	workID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id) VALUES(?,?,?,?,?,?,1,?,false,?)`,
		workID, locationID, rootID, path, path, "album/track.flac", time.Now().UTC(), uuid.New()); err != nil {
		t.Fatalf("insert analysis work: %v", err)
	}
	return workID
}

func assertStorageStepInputIsNull(t *testing.T, ctx context.Context, database *bun.DB, key persistence.SourceAnalysisStep) {
	t.Helper()
	var inputSnapshot []byte
	if err := database.NewRaw(`SELECT input_snapshot FROM source_analysis_step WHERE work_id=? AND step=?`, key.WorkID, key.Step).Scan(ctx, &inputSnapshot); err != nil {
		t.Fatalf("load analysis step: %v", err)
	}
	if len(inputSnapshot) != 0 {
		t.Fatalf("step input = %s, want SQL NULL", inputSnapshot)
	}
}
