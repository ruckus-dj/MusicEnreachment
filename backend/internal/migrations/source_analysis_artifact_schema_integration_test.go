//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
)

func TestSourceAnalysisArtifactOwnershipConstraintsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	workID, operationID, operationJobID := insertArtifactOwnershipFixture(t, ctx, database)

	artifactID := insertSourceAnalysisArtifact(t, ctx, database, workID, operationID, 1, operationJobID, "album/track.flac")
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_artifact_binding
		(work_id, artifact_id, borrower_operation_id, borrower_operation_attempt, borrower_job_id, requested_steps)
		VALUES (?, ?, ?, 1, ?, ARRAY['sha256'])`, workID, artifactID, operationID, operationJobID); err != nil {
		t.Fatalf("insert artifact binding: %v", err)
	}
	foreignWorkID, foreignOperationID, foreignJobID := insertArtifactOwnershipFixture(t, ctx, database)
	foreignArtifactID := insertSourceAnalysisArtifact(t, ctx, database, foreignWorkID, foreignOperationID, 1, foreignJobID, "album/foreign.flac")
	unboundWorkID, _, _ := insertArtifactOwnershipFixture(t, ctx, database)
	_, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_artifact_binding (work_id, artifact_id) VALUES (?, ?)`, unboundWorkID, foreignArtifactID)
	requireArtifactConstraintCode(t, err, "23503", "source_analysis_work_artifact_binding_artifact_fk")
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_artifact_binding
		(work_id, artifact_id, borrower_operation_id, borrower_operation_attempt, borrower_job_id)
		VALUES (?, ?, ?, 2, ?)`, foreignWorkID, foreignArtifactID, foreignOperationID, foreignJobID); err == nil {
		t.Fatal("binding accepted a delivery without a registered execution identity")
	} else {
		requireArtifactConstraintCode(t, err, "23503", "source_analysis_work_artifact_binding_execution_fk")
	}
	for _, values := range []struct {
		attempt any
		job     any
	}{{nil, int64(904)}, {2, nil}} {
		partialWork, partialOperation, partialJobID := insertArtifactOwnershipFixture(t, ctx, database)
		partialArtifact := insertSourceAnalysisArtifact(t, ctx, database, partialWork, partialOperation, 1, partialJobID, "album/"+uuid.NewString()+".flac")
		_, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_artifact_binding
			(work_id, artifact_id, borrower_operation_id, borrower_operation_attempt, borrower_job_id)
			VALUES (?, ?, ?, ?, ?)`, partialWork, partialArtifact, partialOperation, values.attempt, values.job)
		requireArtifactConstraintCode(t, err, "23514", "source_analysis_work_artifact_binding_borrower_shape")
	}

	assertArtifactInsertRejected(t, ctx, database, uuid.New(), workID, operationID, 1, operationJobID, "album/track.flac", "ready", nil, nil)
	assertArtifactInsertRejected(t, ctx, database, uuid.New(), workID, uuid.New(), 1, 902, "album/other.flac", "ready", nil, nil)
	assertArtifactInsertRejected(t, ctx, database, uuid.New(), uuid.New(), operationID, 1, operationJobID, "album/missing-work.flac", "ready", nil, nil)
	assertArtifactInsertRejected(t, ctx, database, uuid.New(), workID, operationID, 1, operationJobID, "../outside.flac", "ready", nil, nil)
	assertArtifactInsertRejected(t, ctx, database, uuid.New(), workID, operationID, 1, operationJobID, "album/failed-without-error.flac", "cleanup_failed", nil, nil)
	cleanupAt := time.Now().UTC()
	assertArtifactInsertRejected(t, ctx, database, uuid.New(), workID, operationID, 1, operationJobID, "album/error-in-ready-state.flac", "ready", ptrString("failure"), &cleanupAt)

	if _, err := database.ExecContext(ctx, `DELETE FROM source_analysis_work WHERE id=?`, workID); err == nil {
		t.Fatal("deleting work with an owned artifact succeeded, want RESTRICT")
	}
	var retained int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=? AND work_id=?`, artifactID, workID).Scan(ctx, &retained); err != nil {
		t.Fatalf("count artifact after rejected work deletion: %v", err)
	}
	if retained != 1 {
		t.Fatalf("artifact rows retained after rejected work deletion = %d, want 1", retained)
	}
}

func requireArtifactConstraintCode(t *testing.T, err error, code, constraint string) {
	t.Helper()
	var pgErr pgdriver.Error
	if !errors.As(err, &pgErr) || pgErr.Field('C') != code || pgErr.Field('n') != constraint {
		t.Fatalf("error = %v, want PostgreSQL %s for constraint %s", err, code, constraint)
	}
}

var artifactOwnershipJobID atomic.Int64

func insertArtifactOwnershipFixture(t *testing.T, ctx context.Context, database *bun.DB) (uuid.UUID, uuid.UUID, int64) {
	t.Helper()
	rootID, locationID := uuid.New(), uuid.New()
	workID, operationID := uuid.New(), uuid.New()
	jobID := artifactOwnershipJobID.Add(1) + 900
	rootPath := "/srv/artifact-ownership/" + uuid.NewString()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_root (id, configured_path, display_name)
		VALUES (?, ?, 'artifact ownership')`, rootID, rootPath); err != nil {
		t.Fatalf("insert artifact fixture root: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_location
		(id, source_root_id, relative_path, size_bytes, mtime, last_seen_scan_generation, probe_status)
		VALUES (?, ?, 'album/track.flac', 12, now(), 1, 'audio')`, locationID, rootID); err != nil {
		t.Fatalf("insert artifact fixture location: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work
		(id, location_id, source_root_id, configured_path, inventory_path, relative_path, size_bytes, mtime, sha256_enabled, origin_scan_operation_id)
		VALUES (?, ?, ?, ?, ?, 'album/track.flac', 12, now(), true, ?)`,
		workID, locationID, rootID, rootPath, rootPath, uuid.New()); err != nil {
		t.Fatalf("insert artifact fixture work: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO operation
		(id, kind, state, stage, input_snapshot, attempt, river_job_id, created_at, updated_at, finished_at)
		VALUES (?, 'analyze_source', 'succeeded', 'complete', '{}', 1, ?, now(), now(), now())`, operationID, jobID); err != nil {
		t.Fatalf("insert artifact fixture delivery: %v", err)
	}
	return workID, operationID, jobID
}

func insertSourceAnalysisArtifact(t *testing.T, ctx context.Context, database *bun.DB, workID, operationID uuid.UUID, attempt int, jobID int64, relativePath string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_execution
		(work_id,operation_id,operation_attempt,job_id,processing_mode) VALUES (?,?,?,?,'staged')`,
		workID, operationID, attempt, jobID); err != nil {
		t.Fatalf("insert artifact execution identity: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_artifact
		(id, work_id, relative_output_path, source_size_bytes, source_mtime, owner_operation_id, owner_operation_attempt, owner_job_id, state)
		VALUES (?, ?, ?, 12, now(), ?, ?, ?, 'ready')`, id, workID, relativePath, operationID, attempt, jobID); err != nil {
		t.Fatalf("insert valid source-analysis artifact: %v", err)
	}
	return id
}

func assertArtifactInsertRejected(t *testing.T, ctx context.Context, database *bun.DB, id, workID, operationID uuid.UUID, attempt int, jobID int64, path, state string, cleanupError *string, cleanupAt *time.Time) {
	t.Helper()
	query := `INSERT INTO source_analysis_artifact
		(id, work_id, relative_output_path, source_size_bytes, source_mtime, owner_operation_id, owner_operation_attempt, owner_job_id, state, cleanup_error, cleanup_at)
		VALUES (?, ?, ?, 12, now(), ?, ?, ?, ?, ?, ?)`
	if _, err := database.ExecContext(ctx, query, id, workID, path, operationID, attempt, jobID, state, cleanupError, cleanupAt); err == nil {
		t.Fatalf("insert invalid artifact (path %q, state %q) succeeded", path, state)
	}
}

func ptrString(value string) *string { return &value }
