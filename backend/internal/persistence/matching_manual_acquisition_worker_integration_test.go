//go:build integration

package persistence_test

import (
	"context"
	"github.com/uptrace/bun"
	"testing"
)

func setManualMatchingRiverJobRunning(t *testing.T, ctx context.Context, db *bun.DB, jobID int64) {
	t.Helper()
	if _, err := db.NewRaw(`UPDATE river_job SET state='running', attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("mark River job %d running: %v", jobID, err)
	}
}
