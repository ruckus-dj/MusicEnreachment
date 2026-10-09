//go:build integration

package persistence

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// A process can unlink the owned output and die before committing the durable
// outcome. Startup recovery must settle metadata only; a later explicit cleanup
// can then record the already-missing file as successful.
func TestInterruptedCleanupAfterUnlinkRequiresExplicitSettlement(t *testing.T) {
	ctx, repo, artifactID, fence := admitCleanupOutcomeFixture(t)
	output := t.TempDir()
	relative := filepath.Join("analysis", "staging", artifactID.String())
	full := filepath.Join(output, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("owned staged output"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE source_analysis_artifact SET relative_output_path=? WHERE id=?`, filepath.ToSlash(relative), artifactID); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	err := repo.RunSourceAnalysisArtifactCleanupDelivery(canceled, fence, func(_ context.Context, artifact SourceAnalysisArtifact) (SourceAnalysisArtifactCleanupOutcome, error) {
		if artifact.ID != artifactID || artifact.RelativeOutputPath != filepath.ToSlash(relative) {
			t.Fatalf("cleanup received unexpected ownership record: %+v", artifact)
		}
		if err := os.Remove(full); err != nil {
			t.Fatalf("simulate completed unlink: %v", err)
		}
		cancel() // settlement transaction cannot commit: process lost its context.
		return SourceAnalysisArtifactCleanupOutcome{Succeeded: true}, nil
	})
	if err == nil {
		t.Fatal("delivery unexpectedly settled after its context was canceled")
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Fatalf("owned file still exists after simulated unlink: %v", err)
	}

	if err := repo.RecoverInterruptedSourceAnalysisArtifactCleanupAtStartup(ctx, fence.OperationID, fence.OperationAttempt, fence.JobID); err != nil {
		t.Fatalf("recover orphaned cleanup: %v", err)
	}
	active, err := repo.ListOperations(ctx, "queued", "running")
	if err != nil {
		t.Fatalf("list operations for startup reconciliation: %v", err)
	}
	for _, operation := range active {
		if operation.ID == fence.OperationID {
			t.Fatalf("settled cleanup operation %s was returned for startup reconciliation", operation.ID)
		}
	}
	var artifactCount int
	if err := repo.db.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=? AND state='cleanup_failed'`, artifactID).Scan(ctx, &artifactCount); err != nil || artifactCount != 1 {
		t.Fatalf("recovered ownership rows = %d, err %v; want retained cleanup_failed row", artifactCount, err)
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Fatalf("recovery changed missing file: %v", err)
	}

	// A new, explicit admission/delivery owns the settlement of the absent file.
	client := &cleanupAcceptanceInserter{jobID: 981237}
	op, err := repo.AdmitSourceAnalysisArtifactCleanupWithArgsFactory(ctx, []uuid.UUID{artifactID}, client,
		func(id uuid.UUID) river.JobArgs { return cleanupAcceptanceArgs{OperationID: id} }, nil)
	if err != nil {
		t.Fatalf("explicitly re-admit missing artifact: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE operation SET state='running',started_at=now() WHERE id=?`, op.ID); err != nil {
		t.Fatal(err)
	}
	newFence := SourceAnalysisArtifactCleanupFence{OperationID: op.ID, OperationAttempt: op.Attempt, JobID: client.jobID}
	if err := repo.RunSourceAnalysisArtifactCleanupDelivery(ctx, newFence, func(_ context.Context, artifact SourceAnalysisArtifact) (SourceAnalysisArtifactCleanupOutcome, error) {
		if artifact.ID != artifactID {
			t.Fatalf("explicit cleanup received foreign artifact: %+v", artifact)
		}
		if _, err := os.Stat(full); !os.IsNotExist(err) {
			return SourceAnalysisArtifactCleanupOutcome{}, err
		}
		return SourceAnalysisArtifactCleanupOutcome{Succeeded: true}, nil
	}); err != nil {
		t.Fatalf("settle explicitly selected missing file: %v", err)
	}
	var ownedRows, successes int
	if err := repo.db.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=?`, artifactID).Scan(ctx, &ownedRows); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT count(*) FROM source_analysis_artifact_cleanup_item WHERE operation_id=? AND state='succeeded'`, op.ID).Scan(ctx, &successes); err != nil {
		t.Fatal(err)
	}
	if ownedRows != 0 || successes != 1 {
		t.Fatalf("explicit settlement left ownership/success rows = %d/%d, want 0/1", ownedRows, successes)
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Fatalf("settlement changed absent file: %v", err)
	}
}
