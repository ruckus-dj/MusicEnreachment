//go:build integration

package migrations_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/migrate"
)

const sourceAnalysisArtifactRequestedStepsMigration = "20261027000000"

func TestSourceAnalysisArtifactRequestedStepsMigrationKeepsOnlyProvenIntent(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	ctx := context.Background()
	collection := mustMigrations(t)
	migration := migrationNamed(t, collection, sourceAnalysisArtifactRequestedStepsMigration)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, sourceAnalysisArtifactRequestedStepsMigration))

	boundWorkID, boundOperationID, boundJobID := insertArtifactOwnershipFixture(t, ctx, database)
	boundArtifactID := insertRequestedStepsArtifact(t, ctx, database, boundWorkID, boundOperationID, boundJobID, "ready")
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_artifact_binding
		(work_id, artifact_id, requested_steps) VALUES (?, ?, ARRAY['fingerprint','sha256','fingerprint'])`, boundWorkID, boundArtifactID); err != nil {
		t.Fatalf("insert accumulated artifact binding: %v", err)
	}

	unboundWorkID, singleOperationID, singleJobID := insertArtifactOwnershipFixture(t, ctx, database)
	unboundArtifactID := insertRequestedStepsArtifact(t, ctx, database, unboundWorkID, singleOperationID, singleJobID, "cleanup_eligible")
	singleSnapshot := json.RawMessage(`{"schema_version":1,"mode":"single_step","work_ids":["` + unboundWorkID.String() + `"],"target_work_id":"` + unboundWorkID.String() + `","target_step":"fingerprint","rerun_target":false}`)
	updateRequestedStepsOperation(t, ctx, database, singleOperationID, "single_step", singleSnapshot)

	batchOperationID, batchJobID := uuid.New(), int64(9901)
	otherWorkID := uuid.New()
	batchSnapshot := json.RawMessage(`{"schema_version":1,"mode":"batch","work_ids":["` + unboundWorkID.String() + `","` + otherWorkID.String() + `"],"rerun_target":false,"selected_steps":[{"work_id":"` + unboundWorkID.String() + `","step":"probe"},{"work_id":"` + unboundWorkID.String() + `","step":"sha256"},{"work_id":"` + otherWorkID.String() + `","step":"fingerprint"}]}`)
	if _, err := database.ExecContext(ctx, `INSERT INTO operation
		(id,kind,state,stage,input_snapshot,attempt,river_job_id,created_at,updated_at,finished_at,source_analysis_mode)
		VALUES (?, 'analyze_source','succeeded','complete',?::jsonb,1,?,now(),now(),now(),'batch')`, batchOperationID, batchSnapshot, batchJobID); err != nil {
		t.Fatalf("insert historical batch operation: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_execution
		(work_id,operation_id,operation_attempt,job_id,processing_mode) VALUES (?, ?, 1, ?, 'staged')`, unboundWorkID, batchOperationID, batchJobID); err != nil {
		t.Fatalf("insert historical batch execution: %v", err)
	}

	unknownWorkID, unknownOperationID, unknownJobID := insertArtifactOwnershipFixture(t, ctx, database)
	unknownArtifactID := insertRequestedStepsArtifact(t, ctx, database, unknownWorkID, unknownOperationID, unknownJobID, "ready")
	ownerSnapshot := json.RawMessage(`{"schema_version":1,"mode":"single_step","target_work_id":"` + unknownWorkID.String() + `","target_step":"fingerprint"}`)
	updateRequestedStepsOperation(t, ctx, database, unknownOperationID, "single_step", ownerSnapshot)
	secondOperationID, secondJobID := uuid.New(), int64(9902)
	secondSnapshot := json.RawMessage(`{"schema_version":1,"mode":"single_step","target_work_id":"` + unknownWorkID.String() + `","target_step":"probe"}`)
	if _, err := database.ExecContext(ctx, `INSERT INTO operation
		(id,kind,state,stage,input_snapshot,attempt,river_job_id,created_at,updated_at,finished_at,source_analysis_mode)
		VALUES (?, 'analyze_source','succeeded','complete',?::jsonb,1,?,now(),now(),now(),'single_step')`, secondOperationID, secondSnapshot, secondJobID); err != nil {
		t.Fatalf("insert second artifact creator operation: %v", err)
	}
	secondArtifactID := insertRequestedStepsArtifact(t, ctx, database, unknownWorkID, secondOperationID, secondJobID, "ready")
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_execution
		(work_id,operation_id,operation_attempt,job_id,processing_mode) VALUES (?, ?, 1, ?, 'staged')`, unknownWorkID, batchOperationID, batchJobID); err != nil {
		t.Fatalf("insert unrelated same-work execution: %v", err)
	}

	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	assertArtifactRequestedSteps(t, ctx, database, boundArtifactID, []string{"sha256", "fingerprint"}, true)
	assertArtifactRequestedSteps(t, ctx, database, unboundArtifactID, []string{}, false)
	assertArtifactRequestedSteps(t, ctx, database, unknownArtifactID, []string{}, false)
	assertArtifactRequestedSteps(t, ctx, database, secondArtifactID, []string{}, false)
}

func insertRequestedStepsArtifact(t *testing.T, ctx context.Context, database *bun.DB, workID, operationID uuid.UUID, jobID int64, state string) uuid.UUID {
	t.Helper()
	artifactID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work_execution
		(work_id,operation_id,operation_attempt,job_id,processing_mode) VALUES (?, ?, 1, ?, 'staged')`, workID, operationID, jobID); err != nil {
		t.Fatalf("insert artifact execution identity: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_artifact
		(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state)
		VALUES (?, ?, ?, 12, now(), ?, 1, ?, ?)`, artifactID, workID, "analysis/staging/"+artifactID.String(), operationID, jobID, state); err != nil {
		t.Fatalf("insert artifact for requested-steps migration: %v", err)
	}
	return artifactID
}

func updateRequestedStepsOperation(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, mode string, snapshot json.RawMessage) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `UPDATE operation SET source_analysis_mode=?, input_snapshot=?::jsonb WHERE id=?`, mode, snapshot, operationID); err != nil {
		t.Fatalf("set historical single-step intent: %v", err)
	}
}

func assertArtifactRequestedSteps(t *testing.T, ctx context.Context, database *bun.DB, artifactID uuid.UUID, want []string, wantKnown bool) {
	t.Helper()
	var steps []string
	var known bool
	if err := database.NewRaw(`SELECT requested_steps, requested_steps_known FROM source_analysis_artifact WHERE id=?`, artifactID).Scan(ctx, pgdialect.Array(&steps), &known); err != nil {
		t.Fatalf("read migrated artifact intent: %v", err)
	}
	if len(steps) != len(want) {
		t.Fatalf("artifact requested steps = %v, want %v", steps, want)
	}
	for index := range want {
		if steps[index] != want[index] {
			t.Fatalf("artifact requested steps = %v, want %v", steps, want)
		}
	}
	if known != wantKnown {
		t.Fatalf("artifact requested steps known = %t, want %t", known, wantKnown)
	}
}
