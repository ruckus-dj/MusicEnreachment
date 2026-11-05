//go:build integration

package persistence

import (
	"context"
	"testing"
)

func disablePublicationSourceRemoval(t *testing.T, ctx context.Context, f publicationExecutionFixture) {
	t.Helper()
	if _, err := f.db.ExecContext(ctx, `UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`); err != nil {
		t.Fatal(err)
	}
}

func TestCancelUnstartedPublicationSourceCleanupDeliveryPostgreSQL(t *testing.T) {
	t.Parallel()
	t.Run("claimed queued and running unstarted", func(t *testing.T) {
		for _, start := range []bool{false} {
			name := "queued"
			if start {
				name = "running"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				f, repo, _ := publicationSourceCleanupReadyFixture(t)
				guard, operationID := admitAndClaimSourceCleanup(t, ctx, f, repo)
				if start {
					started, err := repo.StartPublicationSourceCleanupExecution(ctx, guard)
					if err != nil || !started {
						t.Fatalf("start cleanup=%v, %v", started, err)
					}
				}
				disablePublicationSourceRemoval(t, ctx, f)
				cancelled, err := repo.CancelUnstartedPublicationSourceCleanupDelivery(ctx, guard, "disabled")
				if err != nil || !cancelled {
					t.Fatalf("cancel cleanup=%v, %v", cancelled, err)
				}
				for _, table := range []string{"source_cleanup_item", "source_cleanup_owner", "publication_execution_claim"} {
					var count int
					if err := f.db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, operationID).Scan(ctx, &count); err != nil || count != 0 {
						t.Fatalf("%s count=%d, %v", table, count, err)
					}
				}
			})
		}
	})

}
