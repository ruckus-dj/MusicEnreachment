//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

func TestSourceAnalysisRetryRefusesStaleSnapshotWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := newAnalysisRetryFixture(t, true)
	a := insertFailedAnalysisOperation(t, ctx, fixture)

	// B runs to completion and replaces the variant v0 the file still describes.
	b := insertAnalysisOperation(t, ctx, fixture, &fixture.previousVariantID, "running", "probing")
	apply := persistence.SourceAnalysisApply{
		OperationID: b.ID, RelativePath: fixture.location.RelativePath,
		SizeBytes: fixture.location.SizeBytes, Mtime: fixture.location.Mtime,
		AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion, FFProbeVersion: "7.1",
		FFProbeJSON: json.RawMessage(`{"format":{},"streams":[]}`), ObservedTags: json.RawMessage(`{}`),
		InspectedAt: time.Now().UTC(),
	}
	if _, err := fixture.inventory.ApplyAnalysisResult(ctx, apply); err != nil {
		t.Fatalf("apply B: %v", err)
	}
	if mediaVariantExists(t, ctx, fixture.database, fixture.previousVariantID) {
		t.Fatal("B did not orphan the variant A pins in its snapshot")
	}

	// Retrying A must refuse; it changes neither state, attempt, holds nor jobs.
	if _, err := fixture.retry(t, ctx, a.ID); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("retry against a replaced variant = %v, want %v", err, persistence.ErrSourceAnalysisStale)
	}
	requireAnalysisRetryUntouched(t, ctx, fixture, a.ID, 1)
}

// TestSourceAnalysisRetryRefusalsWithPostgreSQL covers every precondition the
// retry repeats under the operation table lock: a disabled root, a changed
// inventory path, a deleted or not-ready pinned installation, an active tools
// move, an active scan or analysis of the same root, and an operation that is
// not failed. Each refusal leaves the failed row, its attempt, its snapshot and
// its empty holds exactly as they were and inserts no River job.
func TestSourceAnalysisRetryRefusalsWithPostgreSQL(t *testing.T) {
	cases := []struct {
		name   string
		want   error
		mutate func(t *testing.T, ctx context.Context, fixture analysisRetryFixture)
	}{
		{"disabled root", persistence.ErrSourceRootDisabled, func(t *testing.T, ctx context.Context, fixture analysisRetryFixture) {
			execAnalysisRetrySQL(t, ctx, fixture.database, "UPDATE source_root SET enabled = false WHERE id = ?", fixture.root.ID)
		}},
		{"changed inventory path", persistence.ErrSourceAnalysisStale, func(t *testing.T, ctx context.Context, fixture analysisRetryFixture) {
			execAnalysisRetrySQL(t, ctx, fixture.database, "UPDATE source_root SET inventory_path = inventory_path || '-moved' WHERE id = ?", fixture.root.ID)
		}},
		{"deleted installation", persistence.ErrSourceAnalysisInstallationUnusable, func(t *testing.T, ctx context.Context, fixture analysisRetryFixture) {
			execAnalysisRetrySQL(t, ctx, fixture.database, "DELETE FROM tool_installation WHERE id = ?", fixture.installationID)
		}},
		{"not ready installation", persistence.ErrSourceAnalysisInstallationUnusable, func(t *testing.T, ctx context.Context, fixture analysisRetryFixture) {
			execAnalysisRetrySQL(t, ctx, fixture.database, "UPDATE tool_installation SET state = 'preparing' WHERE id = ?", fixture.installationID)
		}},
		{"active tools move", persistence.ErrToolsRootMoveActive, func(t *testing.T, ctx context.Context, fixture analysisRetryFixture) {
			insertOperationRow(t, ctx, fixture.database, &persistence.Operation{
				ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued", InputSnapshot: json.RawMessage(`{}`),
			})
		}},
		{"active scan of the root", persistence.ErrSourceRootActiveScan, func(t *testing.T, ctx context.Context, fixture analysisRetryFixture) {
			insertOperationRow(t, ctx, fixture.database, &persistence.Operation{
				ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: "queued",
				InputSnapshot: json.RawMessage(`{}`), TargetSourceRootID: &fixture.root.ID,
			})
		}},
		{"active analysis of the root", persistence.ErrSourceRootActiveAnalysis, func(t *testing.T, ctx context.Context, fixture analysisRetryFixture) {
			insertAnalysisOperation(t, ctx, fixture, nil, "queued", "queued")
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := newAnalysisRetryFixture(t, true)
			operation := insertFailedAnalysisOperation(t, ctx, fixture)
			testCase.mutate(t, ctx, fixture)
			if _, err := fixture.retry(t, ctx, operation.ID); !errors.Is(err, testCase.want) {
				t.Fatalf("retry = %v, want %v", err, testCase.want)
			}
			requireAnalysisRetryUntouched(t, ctx, fixture, operation.ID, 1)
		})
	}

	t.Run("not failed", func(t *testing.T) {
		ctx := context.Background()
		fixture := newAnalysisRetryFixture(t, false)
		operation := insertAnalysisOperation(t, ctx, fixture, nil, "queued", "queued")
		if _, err := fixture.retry(t, ctx, operation.ID); err == nil {
			t.Fatal("a queued analysis was retried")
		}
		stored, err := fixture.setup.GetOperation(ctx, operation.ID)
		if err != nil {
			t.Fatalf("read the queued analysis: %v", err)
		}
		if stored.State != "queued" || stored.Attempt != 1 {
			t.Fatalf("refused queued operation = %+v, want it untouched", stored)
		}
		if jobs := countAnalysisRetryJobs(t, ctx, fixture.database, operation.ID); jobs != 0 {
			t.Fatalf("River jobs of the refused queued operation = %d, want 0", jobs)
		}
	})
}

// TestSourceAnalysisRetryRestoresHoldsAndPinsSnapshotWithPostgreSQL proves a
// successful retry keeps the same logical operation and byte-identical snapshot,
// restores both read holds from that snapshot rather than from any current
// setting, increments the attempt and inserts exactly one new job on the
// dedicated analysis queue. Activating another ready FFmpeg version before the
// retry does not change which installation is re-pinned.
func TestSourceAnalysisRetryRestoresHoldsAndPinsSnapshotWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := newAnalysisRetryFixture(t, true)
	operation := insertFailedAnalysisOperation(t, ctx, fixture)
	snapshotBefore := append([]byte(nil), operation.InputSnapshot...)

	replacement := insertAnalysisInstallation(t, ctx, fixture.database, "replacement")
	if err := fixture.setup.ActivateInstallation(ctx, replacement, "ffmpeg", analysisTestGOOS, analysisTestGOARCH, settings.ActiveFFmpegInstallationKey); err != nil {
		t.Fatalf("activate another ready ffmpeg version: %v", err)
	}

	retried, err := fixture.retry(t, ctx, operation.ID)
	if err != nil {
		t.Fatalf("retry the failed analysis: %v", err)
	}
	if retried.State != "queued" || retried.Stage != "retry:probing" || retried.Attempt != 2 || retried.RiverJobID == nil {
		t.Fatalf("retried operation = %+v, want queued at retry:probing on attempt 2", retried)
	}
	if !sameOptionalUUIDRetry(retried.AnalysisMediaVariantID, previousOrNil(fixture)) {
		t.Fatalf("variant hold = %v, want the pinned %s", retried.AnalysisMediaVariantID, fixture.previousVariantID)
	}
	if !sameOptionalUUIDRetry(retried.AnalysisInstallationID, &fixture.installationID) {
		t.Fatalf("installation hold = %v, want the pinned %s", retried.AnalysisInstallationID, fixture.installationID)
	}
	stored, err := fixture.setup.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the retried operation: %v", err)
	}
	if !reflect.DeepEqual(mustDecodeSnapshot(t, stored.InputSnapshot), mustDecodeSnapshot(t, snapshotBefore)) {
		t.Fatalf("retry rewrote the immutable snapshot:\n got %s\nwant %s", stored.InputSnapshot, snapshotBefore)
	}
	kind, queue := readAnalysisRetryJob(t, ctx, fixture.database, *retried.RiverJobID)
	if kind != service.SourceAnalysisJobKind || queue != service.SourceAnalysisQueue {
		t.Fatalf("retry River job = %s on %q, want %s on %q", kind, queue, service.SourceAnalysisJobKind, service.SourceAnalysisQueue)
	}
	if jobs := countAnalysisRetryJobs(t, ctx, fixture.database, operation.ID); jobs != 1 {
		t.Fatalf("River jobs of the retried operation = %d, want 1", jobs)
	}
}

// TestSourceAnalysisRetryRefusesMoveCommittedFirstWithPostgreSQL proves that a
// tools move committed under the operation table lock before the retry refuses
// the retry: the competitor's transaction takes the lock, the retry's attempt to
// take the same lock is the deterministic checkpoint, and the committed move is
// then observed.
func TestSourceAnalysisRetryRefusesMoveCommittedFirstWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := newAnalysisRetryFixture(t, false)
	operation := insertFailedAnalysisOperation(t, ctx, fixture)
	err := runOperationLockRace(t, ctx, fixture.database,
		func(ctx context.Context, tx bun.Tx) error {
			return insertQueuedOperation(ctx, tx, &persistence.Operation{
				ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued", InputSnapshot: json.RawMessage(`{}`),
			})
		},
		func(ctx context.Context) error { _, err := fixture.retry(t, ctx, operation.ID); return err })
	if !errors.Is(err, persistence.ErrToolsRootMoveActive) {
		t.Fatalf("retry with a committed move = %v, want %v", err, persistence.ErrToolsRootMoveActive)
	}
	requireAnalysisRetryUntouched(t, ctx, fixture, operation.ID, 1)
}

// TestSourceAnalysisRetryRefusesMoveWhenHoldCommittedFirstWithPostgreSQL proves
// the reverse ordering with an exact transaction checkpoint: the competing
// transaction holds the operation table lock and prepares the queued retry and
// its restored installation hold uncommitted; the real tools move reaches the
// same lock (the barrier proves it), the retry then commits, and the move refuses
// because the committed analysis hold exists. It asserts the restored retry is
// the queued holder and that no move operation or job was inserted.
func TestSourceAnalysisRetryRefusesMoveWhenHoldCommittedFirstWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := newAnalysisRetryFixture(t, false)
	operation := insertFailedAnalysisOperation(t, ctx, fixture)
	move := &persistence.Operation{ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued", InputSnapshot: json.RawMessage(`{}`)}
	err := runQueryRace(t, ctx, fixture.database,
		"LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE",
		"LOCK TABLE operation",
		func(ctx context.Context, tx bun.Tx) error {
			return restoreQueuedRetryHold(ctx, tx, operation, previousOrNil(fixture), fixture.installationID, fixture.client)
		},
		func(ctx context.Context) error {
			return fixture.setup.CreateToolsMoveOperationAndEnqueue(ctx, move, fixture.client,
				serviceOperationArgs{OperationID: move.ID}, nil)
		})
	if !errors.Is(err, persistence.ErrToolsInstallationHeldByAnalysis) {
		t.Fatalf("move against a retry hold that then committed = %v, want %v", err, persistence.ErrToolsInstallationHeldByAnalysis)
	}
	stored, err := fixture.setup.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the restored retry: %v", err)
	}
	if stored.State != "queued" || stored.AnalysisInstallationID == nil || *stored.AnalysisInstallationID != fixture.installationID {
		t.Fatalf("restored retry = %+v, want the queued holder of the installation", stored)
	}
	if moves := countScanEnqueueRows(t, ctx, fixture.database, "SELECT count(*) FROM operation WHERE kind = 'move_tools_root'"); moves != 0 {
		t.Fatalf("move operations after the refusal = %d, want 0", moves)
	}
}

// TestSourceAnalysisRetryRefusesDeleteCommittedFirstWithPostgreSQL proves that
// an installation deleted under the operation table lock before the retry
// refuses the retry: the pinned installation no longer exists.
func TestSourceAnalysisRetryRefusesDeleteCommittedFirstWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := newAnalysisRetryFixture(t, false)
	operation := insertFailedAnalysisOperation(t, ctx, fixture)
	err := runOperationLockRace(t, ctx, fixture.database,
		func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.ExecContext(ctx, "DELETE FROM tool_installation WHERE id = ?", fixture.installationID)
			return err
		},
		func(ctx context.Context) error { _, err := fixture.retry(t, ctx, operation.ID); return err })
	if !errors.Is(err, persistence.ErrSourceAnalysisInstallationUnusable) {
		t.Fatalf("retry with a deleted installation = %v, want %v", err, persistence.ErrSourceAnalysisInstallationUnusable)
	}
	requireAnalysisRetryUntouched(t, ctx, fixture, operation.ID, 1)
}

// TestSourceAnalysisRetryRefusesDeleteWhenHoldCommittedFirstWithPostgreSQL
// proves the reverse ordering with an exact transaction checkpoint: the
// competing transaction prepares the queued retry and its restored installation
// hold uncommitted; the real deletion reaches the operation table lock (the
// barrier proves it), the retry then commits, and the deletion refuses. Another
// ready version is activated first, so the active-setting guard cannot be what
// refuses it: only the committed analysis read hold can, and the filesystem
// remover is never called.
func TestSourceAnalysisRetryRefusesDeleteWhenHoldCommittedFirstWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := newAnalysisRetryFixture(t, false)
	operation := insertFailedAnalysisOperation(t, ctx, fixture)
	replacement := insertAnalysisInstallation(t, ctx, fixture.database, "replacement")
	if err := fixture.setup.ActivateInstallation(ctx, replacement, "ffmpeg", analysisTestGOOS, analysisTestGOARCH, settings.ActiveFFmpegInstallationKey); err != nil {
		t.Fatalf("activate another ready ffmpeg version: %v", err)
	}
	removed := false
	err := runQueryRace(t, ctx, fixture.database,
		"LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE",
		"LOCK TABLE operation",
		func(ctx context.Context, tx bun.Tx) error {
			return restoreQueuedRetryHold(ctx, tx, operation, previousOrNil(fixture), fixture.installationID, fixture.client)
		},
		func(ctx context.Context) error {
			return fixture.setup.DeleteInstallation(ctx, fixture.installationID, "ffmpeg", analysisTestGOOS, analysisTestGOARCH,
				settings.ActiveFFmpegInstallationKey, func(*persistence.ToolInstallation, string) error { removed = true; return nil })
		})
	if err == nil || removed {
		t.Fatalf("deleting a held non-active installation after a committed retry hold = %v, removed = %v; want a refusal before file removal", err, removed)
	}
	if !installationExists(t, ctx, fixture.database, fixture.installationID) {
		t.Fatal("the held installation was deleted despite the refusal")
	}
	stored, err := fixture.setup.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the restored retry: %v", err)
	}
	if stored.State != "queued" || stored.AnalysisInstallationID == nil || *stored.AnalysisInstallationID != fixture.installationID {
		t.Fatalf("restored retry = %+v, want the queued holder of the installation", stored)
	}
}

// newAnalysisRetryFixture opens one PostgreSQL database, resets it, applies the
// migrations and builds a non-stale root with one audio location, a ready FFmpeg
// installation and, when asked, a linked previous variant.
