//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestCurrentSourceExecutionTupleRejectsPartialIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.OpenMigrated(t)
	rootID := newVariantRoot(t, ctx, database, "/srv/execution-tuple")
	for _, test := range []struct {
		name      string
		operation bool
		attempt   any
		job       any
	}{
		{name: "operation only", operation: true},
		{name: "attempt only", attempt: 1},
		{name: "job only", job: int64(1)},
		{name: "operation and attempt", operation: true, attempt: 1},
		{name: "operation and job", operation: true, job: int64(1)},
		{name: "attempt and job", attempt: 1, job: int64(1)},
		{name: "zero attempt", operation: true, attempt: 0, job: int64(1)},
		{name: "negative attempt", operation: true, attempt: -1, job: int64(1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			locationID := newVariantLocation(t, ctx, database, rootID, uuid.NewString()+".flac")
			workID := insertGuardClosureWork(t, ctx, database, rootID, locationID)
			var operation any
			if test.operation {
				operation = uuid.New()
			}
			_, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step
				(work_id,step,state,execution_operation_id,execution_operation_attempt,execution_job_id)
				VALUES (?, 'sha256','pending',?,?,?)`, workID, operation, test.attempt, test.job)
			var databaseError *pgconn.PgError
			if !errors.As(err, &databaseError) || databaseError.Code != "23514" || databaseError.ConstraintName != "source_analysis_step_execution_all_or_none" {
				t.Fatalf("invalid execution tuple returned %v, want execution CHECK violation (not an unrelated FK or trigger failure)", err)
			}
		})
	}
}
