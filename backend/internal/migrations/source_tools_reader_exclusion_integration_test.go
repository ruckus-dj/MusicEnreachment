//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestSourceToolsReaderExclusionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	ctx := context.Background()
	// Reader transitions require the final scan-reader shape as well as the
	// normalized exclusion; exercise the complete forward contract together.
	testpostgres.ResetAndMigrate(t, database)

	rootID, scanID, moveID := uuid.New(), uuid.New(), uuid.New()
	otherRootID, otherScanID := uuid.New(), uuid.New()
	installationID := uuid.New()
	rootPath := "/srv/tools-reader-exclusion-" + rootID.String()
	if _, err := database.ExecContext(ctx, `
		INSERT INTO source_root (id, configured_path, display_name)
		VALUES (?, ?, 'Tools reader exclusion')`, rootID, rootPath); err != nil {
		t.Fatalf("insert source root: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO tool_installation (
			id, package_kind, platform_goos, platform_goarch, source_name,
			release_identity, relative_path, state, verified_at, executable_versions
		) VALUES (?, 'ffmpeg', 'linux', 'amd64', 'test', '7.1', 'ffmpeg/7.1', 'ready', now(),
		'{"ffmpeg":"ffmpeg version 7.1","ffprobe":"ffprobe version 7.1"}'::jsonb)`,
		installationID); err != nil {
		t.Fatalf("insert tool installation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO operation (id, kind, state, stage, input_snapshot, target_source_root_id, tools_read_required)
		VALUES (?, 'scan_source', 'running', 'scanning', ?::jsonb, ?, false)`,
		scanID, `{"schema_version":3,"source_root_id":"`+rootID.String()+`","configured_path":"`+rootPath+`","scan_generation":1,"sha256_enabled":false,"tools":[]}`, rootID); err != nil {
		t.Fatalf("insert active tools-free scan: %v", err)
	}
	// Reader exclusion uses durable operation selectors rather than snapshot tool
	// arrays, and readers on separate roots may coexist.
	if _, err := database.ExecContext(ctx, `INSERT INTO source_root (id, configured_path, display_name)
		VALUES (?, ?, 'Second tools reader')`, otherRootID, "/srv/tools-reader-exclusion-"+otherRootID.String()); err != nil {
		t.Fatalf("insert second source root: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO operation
		(id, kind, state, stage, input_snapshot, target_source_root_id, tools_read_required)
		VALUES (?, 'scan_source', 'running', 'scanning', '{}', ?, false)`, otherScanID, otherRootID); err != nil {
		t.Fatalf("insert second tools-free scan: %v", err)
	}
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation_tool_read_hold(operation_id, installation_id) VALUES (?,?)`, otherScanID, installationID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE operation SET tools_read_required=true WHERE id=?`, otherScanID)
		return err
	}); err != nil {
		t.Fatalf("enable second scan reader with its tool hold: %v", err)
	}
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation_tool_read_hold(operation_id, installation_id) VALUES (?, ?)`, scanID, installationID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE operation SET tools_read_required=true WHERE id=?`, scanID)
		return err
	}); err != nil {
		t.Fatalf("enable first scan reader alongside distinct-root reader: %v", err)
	}
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM operation_tool_read_hold WHERE operation_id=?`, scanID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE operation SET tools_read_required=false WHERE id=?`, scanID)
		return err
	}); err != nil {
		t.Fatalf("disable first scan reader and remove its tool hold: %v", err)
	}
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM operation_tool_read_hold WHERE operation_id=?`, otherScanID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE operation SET tools_read_required=false WHERE id=?`, otherScanID)
		return err
	}); err != nil {
		t.Fatalf("disable second scan reader and remove its tool hold: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO operation (id, kind, state, stage, input_snapshot)
		VALUES (?, 'move_tools_root', 'running', 'moving', '{"schema_version":1,"old_root":"/srv/tools"}'::jsonb)`, moveID); err != nil {
		t.Fatalf("insert active tools move alongside tools-free scans: %v", err)
	}
	assertOperationCount(t, ctx, database, "active scan and move", scanID, moveID)

	// A worker's reader transition inserts its hold and flips the operation flag
	// in one transaction. The exclusion is checked by the flag update; failure
	// must roll back the hold as well.
	tx, err := database.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		t.Fatalf("begin reader transition: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_tool_read_hold(operation_id, installation_id) VALUES (?, ?)`, scanID, installationID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert scan tool hold: %v", err)
	}
	_, transitionErr := tx.ExecContext(ctx, `UPDATE operation SET tools_read_required=true WHERE id=?`, scanID)
	if transitionErr == nil {
		_ = tx.Rollback()
		t.Fatal("tools-reader transition succeeded while a tools-root move was active")
	}
	if !strings.Contains(transitionErr.Error(), "operation_active_tools_operations_exclusive") {
		_ = tx.Rollback()
		t.Fatalf("reader transition error = %v, want tools-operation exclusion violation", transitionErr)
	}
	if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		t.Fatalf("roll back rejected reader transition: %v", err)
	}
	var toolsReadRequired bool
	if err := database.NewRaw(`SELECT tools_read_required FROM operation WHERE id=?`, scanID).Scan(ctx, &toolsReadRequired); err != nil {
		t.Fatalf("read scan tools requirement after rollback: %v", err)
	}
	if toolsReadRequired {
		t.Fatal("rejected reader transition left tools_read_required enabled")
	}
	var holdCount int
	if err := database.NewRaw(`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, scanID).Scan(ctx, &holdCount); err != nil {
		t.Fatalf("read scan holds after rollback: %v", err)
	}
	if holdCount != 0 {
		t.Fatalf("scan tool holds after rejected transition = %d, want 0", holdCount)
	}

	// With the reader flag still false, a subsequent tools-root move remains
	// admissible once the prior move has finished.
	if _, err := database.ExecContext(ctx, `UPDATE operation SET state='succeeded', stage='succeeded', finished_at=now() WHERE id=?`, moveID); err != nil {
		t.Fatalf("finish initial tools move: %v", err)
	}
	secondMoveID := uuid.New()
	if _, err := database.ExecContext(ctx, `
		INSERT INTO operation (id, kind, state, stage, input_snapshot)
		VALUES (?, 'move_tools_root', 'queued', 'queued', '{"schema_version":1,"old_root":"/srv/tools"}'::jsonb)`, secondMoveID); err != nil {
		t.Fatalf("insert tools move after rejected reader transition: %v", err)
	}
	assertOperationCount(t, ctx, database, "tools-free scan and next move", scanID, secondMoveID)
}

func assertOperationCount(t *testing.T, ctx context.Context, database *bun.DB, description string, ids ...uuid.UUID) {
	t.Helper()
	var count int
	if len(ids) != 2 {
		t.Fatalf("assertOperationCount requires two operation IDs, got %d", len(ids))
	}
	if err := database.NewRaw(`SELECT count(*) FROM operation WHERE id IN (?, ?)`, ids[0], ids[1]).Scan(ctx, &count); err != nil {
		t.Fatalf("count %s: %v", description, err)
	}
	if count != len(ids) {
		t.Fatalf("%s operation count = %d, want %d", description, count, len(ids))
	}
}
