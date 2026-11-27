//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestListCurrentOperationsExcludesMatchingHandedOffToReviewPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	repo := NewSetupManagerRepository(db)

	reviewID, activeID, failedID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,safe_error,finished_at)
		VALUES (?,'matching_evaluation_v1','running','manual_review','{}',NULL,NULL),
		       (?,'matching_evaluation_v1','running','queued','{}',NULL,NULL),
		       (?,'matching_evaluation_v1','failed','failed','{}','matching failed',now())`,
		reviewID, activeID, failedID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	rows, total, err := repo.ListCurrentOperations(ctx, CurrentOperationsFilter{Kind: "matching", Status: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("got %d total and %d rows, want 2", total, len(rows))
	}
	want := map[uuid.UUID]bool{activeID: true, failedID: true}
	for _, row := range rows {
		if !want[row.ID] {
			t.Fatalf("unexpected operation %s from stage %q", row.ID, row.Stage)
		}
	}
}
