//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestCleanupOperationDeliveryTransitionUsesPostgreSQLFences(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	repository := persistence.NewSetupManagerRepository(db)
	operations := service.NewOperations(repository)
	ctx := context.Background()
	baseJobID := int64(781203)
	sourceRootID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name) VALUES(?,?,?)`, sourceRootID, "/cleanup-transition/"+uuid.NewString(), "cleanup transition"); err != nil {
		t.Fatalf("create source root for unsupported scan operation: %v", err)
	}

	for index, test := range []struct {
		name       string
		attempt    int
		jobOffset  int64
		kind       string
		targetRoot *uuid.UUID
		state      string
		wantChange bool
		wantError  bool
	}{
		{name: "valid exact attempt and job", attempt: 1, kind: persistence.SourceAnalysisArtifactCleanupOperationKind, state: "queued", wantChange: true},
		{name: "wrong attempt", attempt: 2, kind: persistence.SourceAnalysisArtifactCleanupOperationKind, state: "queued"},
		{name: "wrong job", attempt: 1, jobOffset: 1, kind: persistence.SourceAnalysisArtifactCleanupOperationKind, state: "queued"},
		{name: "unsupported kind", attempt: 1, kind: "scan_source", targetRoot: &sourceRootID, state: "queued"},
		{name: "terminal state", attempt: 1, kind: persistence.SourceAnalysisArtifactCleanupOperationKind, state: "succeeded", wantError: true},
		{name: "failed terminal state", attempt: 1, kind: persistence.SourceAnalysisArtifactCleanupOperationKind, state: "failed", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			operationJobID := baseJobID + int64(index)*10
			operation := &persistence.Operation{
				ID: uuid.New(), Kind: persistence.SourceAnalysisArtifactCleanupOperationKind,
				State: "queued", Stage: "queued", Attempt: 1, RiverJobID: &operationJobID,
				InputSnapshot: json.RawMessage(`{"artifact_ids":["` + uuid.NewString() + `"]}`),
			}
			if err := repository.CreateOperation(ctx, operation); err != nil {
				t.Fatalf("create cleanup operation: %v", err)
			}
			stage := "queued"
			var finishedAt *time.Time
			var startedAt *time.Time
			var safeError *string
			if test.state == "succeeded" || test.state == "failed" {
				stage = "cleaning_artifacts"
				started := time.Now().UTC()
				startedAt = &started
				finished := time.Now().UTC()
				finishedAt = &finished
			}
			if test.state == "failed" {
				message := "The cleanup operation failed."
				safeError = &message
			}
			if _, err := db.ExecContext(ctx, `UPDATE operation SET kind=?,state=?,stage=?,attempt=?,river_job_id=?,started_at=?,finished_at=?,target_source_root_id=?,safe_error=? WHERE id=?`, test.kind, test.state, stage, 1, operationJobID, startedAt, finishedAt, test.targetRoot, safeError, operation.ID); err != nil {
				t.Fatalf("prepare %s operation: %v", test.name, err)
			}
			before, err := repository.GetOperation(ctx, operation.ID)
			if err != nil {
				t.Fatalf("read operation before %s delivery: %v", test.name, err)
			}
			expected := *operation
			expected.Attempt = test.attempt
			expected.Kind = test.kind
			expected.State = test.state
			fenceJobID := *operation.RiverJobID + test.jobOffset
			expected.RiverJobID = &fenceJobID
			err = operations.RunningForDelivery(ctx, &expected, "cleaning_artifacts")
			if test.wantError {
				if err == nil {
					t.Fatal("terminal operation accepted a delivery transition")
				}
			}
			if !test.wantChange {
				if err == nil {
					t.Fatal("stale, terminal, or unsupported delivery was accepted")
				}
				after, readErr := repository.GetOperation(ctx, operation.ID)
				if readErr != nil {
					t.Fatalf("read operation after rejected %s delivery: %v", test.name, readErr)
				}
				if after.State != before.State || after.Stage != before.Stage || after.Attempt != before.Attempt ||
					!sameOperationJobID(after.RiverJobID, before.RiverJobID) ||
					!sameOperationTime(after.StartedAt, before.StartedAt) || !sameOperationTime(after.FinishedAt, before.FinishedAt) {
					t.Fatalf("rejected %s delivery changed persisted operation: before %+v, after %+v", test.name, before, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid cleanup delivery transition: %v", err)
			}
			if test.wantChange {
				stored, err := repository.GetOperation(ctx, operation.ID)
				if err != nil {
					t.Fatalf("read transitioned cleanup operation: %v", err)
				}
				if stored.State != "running" || stored.Stage != "cleaning_artifacts" || stored.StartedAt == nil {
					t.Fatalf("transitioned cleanup operation = state %q stage %q started_at %v", stored.State, stored.Stage, stored.StartedAt)
				}
			}
		})
	}
}

func sameOperationJobID(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameOperationTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
