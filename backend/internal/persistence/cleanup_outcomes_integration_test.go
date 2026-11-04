//go:build integration

package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestCleanupAcceptanceAndOutcomesDatabaseGroup(t *testing.T) {
	db := testpostgres.OpenMigratedWithRiver(t)
	t.Run("deleted_missing_redelivery", func(t *testing.T) { runCleanupOutcomeDeletedAndMissingFinalizeArtifactAndRedelivery(t, db) })
	t.Run("callback_error", func(t *testing.T) { runCleanupOutcomeCallbackErrorIsSafeAndCandidateCanBeSelectedAgain(t, db) })
	t.Run("admission_atomic_rollback", func(t *testing.T) { runCleanupAcceptanceAdmissionAndAtomicRollback(t, db) })
}

func admitCleanupOutcomeFixture(t *testing.T) (context.Context, *SetupManagerRepository, uuid.UUID, SourceAnalysisArtifactCleanupFence) {
	t.Helper()
	return admitCleanupOutcomeFixtureWithDatabase(t, testpostgres.OpenMigratedWithRiver(t))
}

func admitCleanupOutcomeFixtureWithDatabase(t *testing.T, db *bun.DB) (context.Context, *SetupManagerRepository, uuid.UUID, SourceAnalysisArtifactCleanupFence) {
	t.Helper()
	ctx := context.Background()
	repo := NewSetupManagerRepository(db)
	id := insertCleanupAcceptanceArtifactAtRoot(t, ctx, db, SourceAnalysisArtifactCleanupEligible, t.TempDir())
	client := &cleanupAcceptanceInserter{jobID: cleanupAcceptanceJobID.Add(1) + 981236}
	op, err := repo.AdmitSourceAnalysisArtifactCleanupWithArgsFactory(ctx, []uuid.UUID{id}, client, func(id uuid.UUID) river.JobArgs {
		return cleanupAcceptanceArgs{OperationID: id}
	}, nil)
	if err != nil {
		t.Fatalf("admit cleanup: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE operation SET state='running',started_at=now() WHERE id=?`, op.ID); err != nil {
		t.Fatalf("start cleanup operation: %v", err)
	}
	return ctx, repo, id, SourceAnalysisArtifactCleanupFence{OperationID: op.ID, OperationAttempt: op.Attempt, JobID: client.jobID}
}

func runCleanupOutcomeDeletedAndMissingFinalizeArtifactAndRedelivery(t *testing.T, db *bun.DB) {
	for _, outcomeName := range []string{"deleted", "missing"} {
		t.Run(outcomeName, func(t *testing.T) {
			ctx, repo, artifactID, fence := admitCleanupOutcomeFixtureWithDatabase(t, db)
			calls := 0
			err := repo.RunSourceAnalysisArtifactCleanupDelivery(ctx, fence, func(context.Context, SourceAnalysisArtifact) (SourceAnalysisArtifactCleanupOutcome, error) {
				calls++
				return SourceAnalysisArtifactCleanupOutcome{Succeeded: true}, nil
			})
			if err != nil || calls != 1 {
				t.Fatalf("delivery error/callback calls = %v/%d", err, calls)
			}
			var artifacts, succeeded int
			if err := repo.db.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE id=?`, artifactID).Scan(ctx, &artifacts); err != nil {
				t.Fatal(err)
			}
			if err := repo.db.NewRaw(`SELECT count(*) FROM source_analysis_artifact_cleanup_item WHERE artifact_id=? AND state='succeeded'`, artifactID).Scan(ctx, &succeeded); err != nil {
				t.Fatal(err)
			}
			if artifacts != 0 || succeeded != 1 {
				t.Fatalf("artifact rows/item successes = %d/%d, want 0/1", artifacts, succeeded)
			}
			if err := repo.RunSourceAnalysisArtifactCleanupDelivery(ctx, fence, func(context.Context, SourceAnalysisArtifact) (SourceAnalysisArtifactCleanupOutcome, error) {
				calls++
				return SourceAnalysisArtifactCleanupOutcome{Succeeded: true}, nil
			}); err == nil {
				t.Fatal("duplicate delivery unexpectedly succeeded")
			}
			if calls != 1 {
				t.Fatalf("duplicate delivery re-executed filesystem callback: %d calls", calls)
			}
		})
	}
}

func runCleanupOutcomeCallbackErrorIsSafeAndCandidateCanBeSelectedAgain(t *testing.T, db *bun.DB) {
	ctx, repo, artifactID, fence := admitCleanupOutcomeFixtureWithDatabase(t, db)
	if err := repo.RunSourceAnalysisArtifactCleanupDelivery(ctx, fence, func(context.Context, SourceAnalysisArtifact) (SourceAnalysisArtifactCleanupOutcome, error) {
		return SourceAnalysisArtifactCleanupOutcome{}, errors.New("sensitive filesystem path")
	}); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	var state, cleanupError, itemState, itemError string
	var cleanupAt time.Time
	if err := repo.db.NewRaw(`SELECT state,cleanup_error,cleanup_at FROM source_analysis_artifact WHERE id=?`, artifactID).Scan(ctx, &state, &cleanupError, &cleanupAt); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT state,safe_error FROM source_analysis_artifact_cleanup_item WHERE artifact_id=?`, artifactID).Scan(ctx, &itemState, &itemError); err != nil {
		t.Fatal(err)
	}
	const safe = "The staged analysis artifact could not be cleaned up."
	if state != "cleanup_failed" || cleanupError != safe || cleanupAt.IsZero() || itemState != "failed" || itemError != safe {
		t.Fatalf("artifact/item outcome = %q %q %v / %q %q", state, cleanupError, cleanupAt, itemState, itemError)
	}
	if candidates, err := repo.ListSourceAnalysisArtifactCleanupCandidates(ctx, 10); err != nil || len(candidates) != 1 || candidates[0].ID != artifactID {
		t.Fatalf("cleanup candidates = %+v, %v; want failed artifact selectable", candidates, err)
	}
	var steps int
	if err := repo.db.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE work_id=(SELECT work_id FROM source_analysis_artifact WHERE id=?)`, artifactID).Scan(ctx, &steps); err != nil {
		t.Fatal(err)
	}
	if steps != 0 {
		t.Fatalf("cleanup mutated analysis steps: %d", steps)
	}
}
