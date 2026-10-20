//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestPublicationReconciliationOperationShapeWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	position, epoch := uuid.New(), uuid.New()
	valid := `{"version":1,"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `"}`
	if err := insertReconciliationOperation(ctx, db, uuid.New(), valid); err != nil {
		t.Fatalf("insert valid reconciliation operation: %v", err)
	}
	for name, snapshot := range map[string]string{
		"null":                  `null`,
		"null_version":          `{"version":null,"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `"}`,
		"null_position":         `{"version":1,"position_id":null,"output_epoch":"` + epoch.String() + `"}`,
		"null_epoch":            `{"version":1,"position_id":"` + position.String() + `","output_epoch":null}`,
		"missing_version":       `{"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `"}`,
		"malformed_uuid":        `{"version":1,"position_id":"bad","output_epoch":"` + epoch.String() + `"}`,
		"extra_source":          `{"version":1,"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `","source_root_id":"` + uuid.NewString() + `"}`,
		"extra_tool":            `{"version":1,"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `","tools":[]}`,
		"tools_flag":            `{"version":1,"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `","tools_read_required":true}`,
		"extra_provider_target": `{"version":1,"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `","provider_source_id":"` + uuid.NewString() + `"}`,
		"target_selector":       `{"version":1,"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `","target_work_id":"` + uuid.NewString() + `"}`,
		"extra_intent":          `{"version":1,"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `","intent_kind":"audio"}`,
	} {
		t.Run(name, func(t *testing.T) {
			err := insertReconciliationOperation(ctx, db, uuid.New(), snapshot)
			if err == nil || !strings.Contains(err.Error(), "operation_publication_reconciliation_snapshot_check") {
				t.Fatalf("invalid snapshot error = %v, want reconciliation snapshot CHECK violation", err)
			}
		})
	}

	// Preserve precisely the existing scan/tool-reader contract and its invalid case.
	scanID := insertSourceScanReaderOperation(t, ctx, db, true)
	assertNonScanToolReaderOperationRejected(t, ctx, db)
	assertSourceScanReaderShapeExists(t, db)
	assertSourceScanReaderFlag(t, ctx, db, scanID)
}

func insertReconciliationOperation(ctx context.Context, db *bun.DB, id uuid.UUID, snapshot string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO operation
		(id, kind, state, stage, input_snapshot, created_at, finished_at, safe_error)
		VALUES (?, 'publication_reconcile_v1', 'failed', 'failed', ?::jsonb, ?, ?, 'test terminal failure')`, id, snapshot, time.Now().UTC(), time.Now().UTC())
	return err
}
