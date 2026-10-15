//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestCurrentMatchingReconciliationOriginConstraintWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	ordinaryID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO incoming_matching_reconcile_event
		(id,source_operation_id,source_attempt,reason,source_input_snapshot)
		VALUES (?, ?, 1, 'analysis_success', '{}'::jsonb)`, ordinaryID, uuid.New()); err != nil {
		t.Fatalf("insert current source-origin event: %v", err)
	}
	startupID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO incoming_matching_reconcile_event
		(id,reason,source_input_snapshot) VALUES (?, 'startup', '{}'::jsonb)`, startupID); err != nil {
		t.Fatalf("insert current startup event: %v", err)
	}
	for _, invalid := range []struct {
		name string
		stmt string
		args []any
	}{
		{name: "partial source identity", stmt: `INSERT INTO incoming_matching_reconcile_event (id,source_operation_id,reason,source_input_snapshot) VALUES (?,?,'analysis_success','{}'::jsonb)`, args: []any{uuid.New(), uuid.New()}},
		{name: "startup with fabricated source", stmt: `INSERT INTO incoming_matching_reconcile_event (id,source_operation_id,source_attempt,reason,source_input_snapshot) VALUES (?,?,1,'startup','{}'::jsonb)`, args: []any{uuid.New(), uuid.New()}},
		{name: "startup nonempty snapshot", stmt: `INSERT INTO incoming_matching_reconcile_event (id,reason,source_input_snapshot) VALUES (?,'startup','{"fake":true}'::jsonb)`, args: []any{uuid.New()}},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, invalid.stmt, invalid.args...); err == nil {
				t.Fatalf("accepted invalid reconciliation origin: %s", invalid.name)
			}
		})
	}
}

func insertGuardClosureWork(t *testing.T, ctx context.Context, database *bun.DB, rootID, locationID uuid.UUID) uuid.UUID {
	t.Helper()
	workID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work
		(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id)
		SELECT ?,l.id,l.source_root_id,r.configured_path,COALESCE(r.inventory_path,r.configured_path),l.relative_path,l.size_bytes,l.mtime,true,?
		FROM source_location l JOIN source_root r ON r.id=l.source_root_id WHERE l.id=? AND l.source_root_id=?`,
		workID, uuid.New(), locationID, rootID); err != nil {
		t.Fatalf("insert current-schema source-analysis work: %v", err)
	}
	return workID
}

func constraintExists(t *testing.T, database *bun.DB, name string) bool {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM pg_constraint WHERE conname = ?", name).
		Scan(context.Background(), &count); err != nil {
		t.Fatalf("read constraint %s: %v", name, err)
	}
	return count == 1
}
