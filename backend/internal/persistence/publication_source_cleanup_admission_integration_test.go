//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/uptrace/bun"
)

type publicationSourceCleanupTestInserter struct {
	args river.JobArgs
	err  error
}

func (inserter *publicationSourceCleanupTestInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	inserter.args = args
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var jobID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args) VALUES ($1,$2::jsonb) RETURNING id`, args.Kind(), string(encoded)).Scan(&jobID); err != nil {
		return nil, err
	}
	if inserter.err != nil {
		return nil, inserter.err
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: jobID}}, nil
}

func publicationSourceCleanupReadyFixture(t *testing.T) (publicationExecutionFixture, *SetupManagerRepository, uuid.UUID) {
	t.Helper()
	f := preparePublicationResultFixture(t)
	return publicationSourceCleanupReadyFixtureFromResult(t, f)
}

// publicationSourceCleanupReadyFixtureWithDatabase reuses the caller's migrated
// database; it sets publication_remove_sources=true, so shared-db callers own
// saving and restoring that global setting around their fixture group.
func publicationSourceCleanupReadyFixtureWithDatabase(t *testing.T, db *bun.DB, scope string) (publicationExecutionFixture, *SetupManagerRepository, uuid.UUID) {
	t.Helper()
	f := preparePublicationResultFixtureWithDatabase(t, db, scope)
	return publicationSourceCleanupReadyFixtureFromResult(t, f)
}

func publicationSourceCleanupReadyFixtureFromResult(t *testing.T, f publicationExecutionFixture) (publicationExecutionFixture, *SetupManagerRepository, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	repo := NewSetupManagerRepository(f.db)
	_, _, variantID, _ := pubSourceProtectionHoldWithFixture(t, f)
	var hold PublicationSourceHold
	if err := f.db.NewSelect().Model(&hold).Where("operation_id=?", f.guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	workID := hold.WorkID
	// Build the same canonical identity and source association that settled source
	// analysis would leave behind, while retaining the committed publication.
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, workID, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method) SELECT ?,track_id,?,false,true,'manual' FROM release_track WHERE id=?`, uuid.New(), variantID, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE source_root SET inventory_path=configured_path WHERE id IN (SELECT source_root_id FROM publication_source_hold WHERE operation_id=?)`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE source_analysis_work SET inventory_path=configured_path WHERE id=?`, workID); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitPublicationResult(ctx, f.guard, publicationResultForFixture(f)); err != nil || !committed {
		t.Fatalf("commit ready publication result = %v, %v", committed, err)
	}
	if err := NewSettingsRepository(f.db).Set(ctx, "publication_remove_sources", "true"); err != nil {
		t.Fatal(err)
	}
	return f, repo, variantID
}
