//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// The publication result fixture settles the publisher operation and consumes
// its attempt/claim. Finish its real River row before exercising reset's
// independent cleanup-owner proof.
func settleReadyPublicationForReset(t *testing.T, f publicationExecutionFixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, f.guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
}

type cleanupResetState struct {
	owners, items, publications int
	epoch                       uuid.UUID
	operation, job              string
	itemEvidence                string
}

func readCleanupResetState(t *testing.T, f publicationExecutionFixture, operationID uuid.UUID) cleanupResetState {
	t.Helper()
	ctx := context.Background()
	s := cleanupResetState{}
	for query, dest := range map[string]any{`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`: &s.owners, `SELECT count(*) FROM source_cleanup_item WHERE operation_id=?`: &s.items, `SELECT state FROM operation WHERE id=?`: &s.operation, `SELECT state FROM river_job WHERE id=(SELECT river_job_id FROM operation WHERE id=?)`: &s.job} {
		if err := f.db.QueryRowContext(ctx, query, operationID).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.QueryRowContext(ctx, `SELECT output_epoch FROM publication_namespace_state WHERE singleton`).Scan(&s.epoch); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(&s.publications); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id,'state',state,'snapshot',source_snapshot,'revision',borrower_revision) ORDER BY id)::text,'[]') FROM source_cleanup_item WHERE operation_id=?`, operationID).Scan(&s.itemEvidence); err != nil {
		t.Fatal(err)
	}
	return s
}

func assertCleanupResetState(t *testing.T, f publicationExecutionFixture, operationID uuid.UUID, want cleanupResetState) {
	t.Helper()
	if got := readCleanupResetState(t, f, operationID); got != want {
		t.Fatalf("cleanup/reset state changed: got %+v want %+v", got, want)
	}
}

func cleanupResetRequest(t *testing.T, f publicationExecutionFixture, prepare func(context.Context, func(OutputResetDirectory) error) error) OutputResetRequest {
	t.Helper()
	ctx := context.Background()
	oldRoot, toolsRoot := nativeTestPath(t, "/music/cleanup-reset-old"), nativeTestPath(t, "/tools/cleanup-reset")
	for name, value := range map[string]string{"output_directory": oldRoot, "tools_directory": toolsRoot} {
		if _, err := f.db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value,updated_at) VALUES (?, ?, now()) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value,updated_at=now()`, name, value); err != nil {
			t.Fatal(err)
		}
	}
	return OutputResetRequest{ExpectedOldRoot: oldRoot, ExpectedToolsRoot: toolsRoot, NewRoot: nativeTestPath(t, "/music/cleanup-reset-new"), Prepare: prepare, Finish: func(context.Context, OutputResetJournal) error { return nil }}
}
