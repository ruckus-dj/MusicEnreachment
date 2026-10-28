//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestSourceScanToolReaderShapeWithPostgreSQL(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()

	scanID := insertSourceScanReaderOperation(t, ctx, database, true)
	assertNonScanToolReaderOperationRejected(t, ctx, database)
	assertSourceScanReaderShapeExists(t, database)
	assertSourceScanReaderFlag(t, ctx, database, scanID)
}

func insertSourceScanReaderOperation(t *testing.T, ctx context.Context, database *bun.DB, toolsReadRequired bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	rootID := newVariantRoot(t, ctx, database, "/srv/scan-reader-"+id.String())
	var installationID uuid.UUID
	if toolsReadRequired {
		installationID = uuid.New()
		releaseIdentity := uuid.NewString()
		if _, err := database.ExecContext(ctx, `INSERT INTO tool_installation
			(id, package_kind, platform_goos, platform_goarch, source_name, release_identity, relative_path, state, verified_at, version)
			VALUES (?, 'ffmpeg', 'linux', 'amd64', 'test', ?, ?, 'ready', ?, 'ffmpeg 7.1 + ffprobe 7.1')`,
			installationID, releaseIdentity, "ffmpeg/"+releaseIdentity, time.Now().UTC()); err != nil {
			t.Fatalf("insert source scan reader tool installation: %v", err)
		}
	}
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO operation (id, kind, state, stage, input_snapshot, target_source_root_id, tools_read_required)
			VALUES (?, 'scan_source', 'queued', 'queued', '{}'::jsonb, ?, ?)`, id, rootID, toolsReadRequired).Exec(ctx); err != nil {
			return err
		}
		if toolsReadRequired {
			_, err := tx.NewRaw(`INSERT INTO operation_tool_read_hold (operation_id, installation_id) VALUES (?, ?)`, id, installationID).Exec(ctx)
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("insert source scan reader operation and hold atomically: %v", err)
	}
	return id
}

func assertNonScanToolReaderOperationRejected(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	_, err := database.ExecContext(ctx, `
		INSERT INTO operation (id, kind, state, stage, input_snapshot, tools_read_required, finished_at)
		VALUES (?, 'move_tools_root', 'succeeded', 'complete', '{}'::jsonb, true, ?)
	`, uuid.New(), time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "operation_source_analysis_target_shape") {
		t.Fatalf("non-scan NULL-mode tool reader error = %v, want target-shape CHECK violation", err)
	}
}

func assertSourceScanReaderShapeExists(t *testing.T, database *bun.DB) {
	t.Helper()
	var constraint string
	if err := database.NewRaw(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='operation_source_analysis_target_shape'`).Scan(context.Background(), &constraint); err != nil {
		t.Fatalf("read source scan reader shape constraint: %v", err)
	}
	if !strings.Contains(constraint, "scan_source") {
		t.Fatalf("source scan reader shape constraint = %q, want scan_source allowance", constraint)
	}
}

func assertSourceScanReaderFlag(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) {
	t.Helper()
	var mode sql.NullString
	var targetWorkID uuid.NullUUID
	var targetStep sql.NullString
	var required bool
	if err := database.NewRaw(`SELECT source_analysis_mode, target_work_id, target_step, tools_read_required FROM operation WHERE id=?`, operationID).Scan(ctx, &mode, &targetWorkID, &targetStep, &required); err != nil {
		t.Fatalf("read source scan reader selectors: %v", err)
	}
	if mode.Valid || targetWorkID.Valid || targetStep.Valid || !required {
		t.Fatalf("source scan reader selectors/flag = %v/%v/%v/%v, want NULL/NULL/NULL/true", mode, targetWorkID, targetStep, required)
	}
}
