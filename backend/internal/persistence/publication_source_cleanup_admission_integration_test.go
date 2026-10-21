//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
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

func TestPublicationSourceCleanupAdmissionRequiresEnabledSettingPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	repo := NewSetupManagerRepository(db)
	positionID := uuid.New()
	inserter := new(publicationSourceCleanupTestInserter)
	request := PublicationSourceCleanupAdmission{PositionID: positionID, ExpectedEpoch: uuid.New(), ExpectedGeneration: uuid.New()}

	for _, value := range []string{"", "false"} {
		if value == "" {
			if _, err := db.ExecContext(ctx, `DELETE FROM app_setting WHERE setting_name='publication_remove_sources'`); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := db.ExecContext(ctx, `DELETE FROM app_setting WHERE setting_name='publication_remove_sources'`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value) VALUES ('publication_remove_sources',?)`, value); err != nil {
				t.Fatal(err)
			}
		}
		result, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter)
		if err != nil {
			t.Fatalf("admission with setting %q: %v", value, err)
		}
		if result.OperationID != uuid.Nil || len(result.Blockers) == 0 {
			t.Fatalf("admission with setting %q = %+v, want blocker and no operation", value, result)
		}
		if inserter.args != nil {
			t.Fatalf("admission with setting %q enqueued a job: %#v", value, inserter.args)
		}
	}
}

func TestPublicationSourceCleanupAdmissionRejectsMalformedSettingPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	repo := NewSetupManagerRepository(db)
	if _, err := db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value) VALUES ('publication_remove_sources','sometimes')`); err != nil {
		t.Fatal(err)
	}
	result, err := repo.AdmitPublicationSourceCleanup(ctx, PublicationSourceCleanupAdmission{PositionID: uuid.New(), ExpectedEpoch: uuid.New(), ExpectedGeneration: uuid.New()}, &publicationSourceCleanupTestInserter{})
	if err == nil || result.OperationID != uuid.Nil {
		t.Fatalf("malformed setting admission = %+v, %v; want error and no operation", result, err)
	}
}

func publicationSourceCleanupReadyFixture(t *testing.T) (publicationExecutionFixture, *SetupManagerRepository, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	f, _, _, repo := readyFirstPublicationCommitFixture(t)
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
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("commit ready first publication = %v, %v", committed, err)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM publication_source_hold WHERE operation_id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := NewSettingsRepository(f.db).Set(ctx, "publication_remove_sources", "true"); err != nil {
		t.Fatal(err)
	}
	return f, repo, variantID
}

func TestPublicationSourceCleanupAdmissionUsesCurrentPublicationAndBlocksRepeatPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	request := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
	inserter := new(publicationSourceCleanupTestInserter)
	result, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter)
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("admission = %+v, %v", result, err)
	}
	args, ok := inserter.args.(publicationSourceCleanupJobArgs)
	if !ok || args.OperationID != result.OperationID {
		t.Fatalf("enqueued args = %#v, want operation %s", inserter.args, result.OperationID)
	}
	var snapshotRaw string
	if err := f.db.QueryRowContext(ctx, `SELECT source_snapshot FROM source_cleanup_item WHERE operation_id=?`, result.OperationID).Scan(&snapshotRaw); err != nil {
		t.Fatal(err)
	}
	var snapshot publicationSourceCleanupSnapshot
	if err := json.Unmarshal([]byte(snapshotRaw), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.MediaVariantID != variantID || len(snapshot.SourceLinks) != 1 || snapshot.SourceLinks[0].ID == uuid.Nil || snapshot.SourceLinks[0].Selected || snapshot.SourceLinks[0].TrackID == uuid.Nil || len(snapshot.Borrowers) != 1 || snapshot.Borrowers[0].ValidatedAt.IsZero() {
		t.Fatalf("cleanup snapshot lost current link/publication evidence: %+v", snapshot)
	}
	repeat, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter)
	if err != nil || repeat.OperationID != uuid.Nil || len(repeat.Blockers) == 0 {
		t.Fatalf("repeat admission = %+v, %v; want a blocker", repeat, err)
	}
	var owners, operations int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM source_cleanup_owner`).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM operation WHERE kind='cleanup_publication_sources_v1'`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if owners != 1 || operations != 1 {
		t.Fatalf("repeat admission artifacts owners=%d operations=%d, want 1 each", owners, operations)
	}
}

func TestPublicationSourceCleanupAdmissionRollsBackOnEnqueueFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	var baselineOperations, baselineJobs int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM operation`).Scan(&baselineOperations); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM river_job`).Scan(&baselineJobs); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("enqueue failed")
	inserter := &publicationSourceCleanupTestInserter{err: sentinel}
	request := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
	result, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter)
	if !errors.Is(err, sentinel) || result.OperationID != uuid.Nil {
		t.Fatalf("failed-enqueue admission = %+v, %v; want sentinel and no operation", result, err)
	}
	var operations, items, owners, jobs int
	for _, count := range []struct {
		query  string
		target *int
	}{
		{`SELECT count(*) FROM operation`, &operations},
		{`SELECT count(*) FROM source_cleanup_item`, &items},
		{`SELECT count(*) FROM source_cleanup_owner`, &owners},
		{`SELECT count(*) FROM river_job`, &jobs},
	} {
		if err := f.db.QueryRowContext(ctx, count.query).Scan(count.target); err != nil {
			t.Fatal(err)
		}
	}
	if operations != baselineOperations || items != 0 || owners != 0 || jobs != baselineJobs {
		t.Fatalf("failed enqueue left artifacts operations=%d (baseline %d), items=%d owners=%d jobs=%d (baseline %d)", operations, baselineOperations, items, owners, jobs, baselineJobs)
	}
}
