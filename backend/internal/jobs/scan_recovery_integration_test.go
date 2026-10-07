//go:build integration

package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// TestSourceScanStartupRecoveryPostgreSQL drives the scan-specific startup
// recovery and the post-apply idempotency of the scan worker against real
// PostgreSQL and the real River dispatcher. An orphaned scan without a live
// delivery fails retryably with its private candidates dropped and the previous
// inventory untouched, a generation the root already records as applied finishes
// succeeded without being applied again and without losing a location, a retry
// walks the tree again, and a redelivery of an applied scan is a no-op. The
// install policy the same recovery runs is asserted unchanged at the end.
func TestSourceScanStartupRecoveryPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	databaseURL := testpostgres.URL(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	settingsRepository := persistence.NewSettingsRepository(database)
	toolsRoot, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setRuntimeRoots(t, ctx, settingsRepository, toolsRoot)
	registry := settings.New(settingsRepository, nil)
	platform := settings.PlatformState{Platform: settings.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}}
	writeScanDispatchFFmpeg(t, ctx, database, settingsRepository, toolsRoot)

	setupManager := persistence.NewSetupManagerRepository(database)
	inventory := persistence.NewSourceInventoryRepository(database)
	roots := service.NewSourceRoots(inventory, registry)
	slot := &scanRecoveryRiverSlot{}
	operations := service.NewOperationsWithRiver(setupManager, slot)
	source := t.TempDir()
	writeScanDispatchFile(t, filepath.Join(source, "album", "track.flac"), "audio bytes")
	writeScanDispatchFile(t, filepath.Join(source, "album", "silent.mka"), "video only bytes")
	writeScanDispatchFile(t, filepath.Join(source, "album", "broken.wav"), "unreadable bytes")
	root, err := roots.Create(ctx, "Music", source)
	if err != nil {
		t.Fatalf("create the source root: %v", err)
	}

	worker := NewSourceScanWorker(scanDispatchRepository{setupManager, inventory}, operations, roots, registry, platform, tools.NewLifecycle(nil))
	riverClient, listenerPool := startScanDispatchRiver(t, databaseURL, database, worker)
	defer listenerPool.Close()
	defer stopRiverClient(t, riverClient)
	slot.RiverInserter = riverClient
	events, cancelEvents := riverClient.Subscribe(river.EventKindJobCompleted)
	defer cancelEvents()
	probes := filepath.Join(toolsRoot, "probe-log")
	scans := service.NewSourceScanOperations(inventory, roots, registry, platform, riverClient)

	// Given a root whose first scan installed generation 1...
	first, err := scans.Start(ctx, root.ID)
	if err != nil {
		t.Fatalf("start the first scan: %v", err)
	}
	awaitRiverCompletion(t, ctx, events, *first.RiverJobID)
	assertOperationStage(t, ctx, setupManager, first.ID, "succeeded", "succeeded")
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 1)
	installed := readScanDispatchLocations(t, ctx, database, root.ID)
	if count := scanDispatchProbeCount(t, probes); count != 3 {
		t.Fatalf("probes after the first scan = %d, want one per approved file", count)
	}

	// Given a queued scan whose process left stored candidates behind and died
	// before any live delivery could run it...
	writeScanDispatchFile(t, filepath.Join(source, "album", "retry.flac"), "audio bytes")
	queued := createScanDispatchOperation(t, ctx, setupManager, inventory, first.InputSnapshot, root.ID, root.ConfiguredPath)
	attachScanRecoveryJob(t, ctx, database, riverClient, queued, false)
	appendScanRecoveryCandidates(t, ctx, inventory, queued.ID, scanRecoveryCandidates(t, source, "album/retry.flac")...)
	requireScanDispatchCandidates(t, ctx, database, queued.ID, 1)
	probesBeforeRecovery := scanDispatchProbeCount(t, probes)
	reconcileScanRecovery(t, ctx, setupManager, operations, registry)

	// Then startup recovery fails it retryably at the stage it stopped in, drops
	// its private candidates without probing anything and leaves the applied
	// generation exactly as the first scan left it.
	assertOperationStage(t, ctx, setupManager, queued.ID, "failed", service.SourceScanStageQueued)
	requireScanDispatchSafeError(t, readScanDispatchOperation(t, ctx, setupManager, queued.ID), scanSafeInterrupted)
	requireScanDispatchCandidates(t, ctx, database, queued.ID, 0)
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 1)
	requireScanDispatchLocations(t, readScanDispatchLocations(t, ctx, database, root.ID), installed)
	if count := scanDispatchProbeCount(t, probes); count != probesBeforeRecovery {
		t.Fatalf("probes during startup recovery = %d, want the unchanged %d", count, probesBeforeRecovery)
	}

	// Given the failed scan is retried through the production retry...
	retried, err := operations.Retry(ctx, queued.ID)
	if err != nil {
		t.Fatalf("retry the interrupted scan: %v", err)
	}
	requireScanDispatchCandidates(t, ctx, database, queued.ID, 0)
	awaitRiverCompletion(t, ctx, events, *retried.RiverJobID)

	// Then the retry walks the tree again: the file the interrupted attempt never
	// reached is in the inventory, and the dropped candidates played no part.
	assertOperationStage(t, ctx, setupManager, queued.ID, "succeeded", "succeeded")
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 2)
	installed = readScanDispatchLocations(t, ctx, database, root.ID)
	requireScanDispatchStatus(t, installed, "album/retry.flac", persistence.SourceProbeStatusAudio)

	// Given a scan that was interrupted while traversing and had already stored
	// candidates of a file the applied generation does not contain...
	writeScanDispatchFile(t, filepath.Join(source, "album", "second.flac"), "audio bytes")
	traversing := createScanDispatchOperation(t, ctx, setupManager, inventory, first.InputSnapshot, root.ID, root.ConfiguredPath)
	attachScanRecoveryJob(t, ctx, database, riverClient, traversing, false)
	if err := operations.Running(ctx, traversing.ID, service.SourceScanStageTraversing); err != nil {
		t.Fatalf("record the traversing scan as running: %v", err)
	}
	appendScanRecoveryCandidates(t, ctx, inventory, traversing.ID, scanRecoveryCandidates(t, source, "album/second.flac")...)

	// When the startup recovery resolves it...
	reconcileScanRecovery(t, ctx, setupManager, operations, registry)

	// Then it fails at the traversing stage with its candidates dropped, and the
	// previous inventory still does not know the file of the failed attempt.
	assertOperationStage(t, ctx, setupManager, traversing.ID, "failed", service.SourceScanStageTraversing)
	requireScanDispatchSafeError(t, readScanDispatchOperation(t, ctx, setupManager, traversing.ID), scanSafeInterrupted)
	requireScanDispatchCandidates(t, ctx, database, traversing.ID, 0)
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 2)
	requireScanDispatchLocations(t, readScanDispatchLocations(t, ctx, database, root.ID), installed)

	// Given a scan whose apply committed and whose process died before it could
	// report the success: the root records that operation as the applied one.
	tree := []string{"album/track.flac", "album/silent.mka", "album/broken.wav", "album/retry.flac", "album/second.flac"}
	applied := createScanDispatchOperation(t, ctx, setupManager, inventory, first.InputSnapshot, root.ID, root.ConfiguredPath)
	attachScanRecoveryJob(t, ctx, database, riverClient, applied, false)
	if err := operations.Running(ctx, applied.ID, service.SourceScanStageApplying); err != nil {
		t.Fatalf("record the applied scan as running: %v", err)
	}
	appendScanRecoveryCandidates(t, ctx, inventory, applied.ID, scanRecoveryCandidates(t, source, tree...)...)
	applyScanGeneration(t, ctx, setupManager, operations, inventory, applied.ID, root.ConfiguredPath)
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 3)
	appliedLocations := readScanDispatchLocations(t, ctx, database, root.ID)

	// When the startup recovery resolves it...
	reconcileScanRecovery(t, ctx, setupManager, operations, registry)

	// Then it is finished succeeded without a second apply and without deleting a
	// single location of the generation it already installed.
	assertOperationStage(t, ctx, setupManager, applied.ID, "succeeded", "succeeded")
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 3)
	requireScanDispatchLocations(t, readScanDispatchLocations(t, ctx, database, root.ID), appliedLocations)

	// Given a delivered scan whose generation an earlier delivery already applied
	// and whose operation never reported the success...
	writeScanDispatchFile(t, filepath.Join(source, "album", "delivery.flac"), "audio bytes")
	redelivered := createScanDispatchOperation(t, ctx, setupManager, inventory, first.InputSnapshot, root.ID, root.ConfiguredPath)
	attachScanRecoveryJob(t, ctx, database, riverClient, redelivered, false)
	appendScanRecoveryCandidates(t, ctx, inventory, redelivered.ID, scanRecoveryCandidates(t, source, append(tree, "album/delivery.flac")...)...)
	applyScanGeneration(t, ctx, setupManager, operations, inventory, redelivered.ID, root.ConfiguredPath)
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 4)
	redeliveredLocations := readScanDispatchLocations(t, ctx, database, root.ID)
	probesBeforeDelivery := scanDispatchProbeCount(t, probes)
	// Startup recovery recognizes the committed generation and makes the
	// operation terminal before an actual duplicate River delivery is sent.
	reconcileScanRecovery(t, ctx, setupManager, operations, registry)

	// When the job is delivered again through the real dispatcher...
	awaitRiverCompletion(t, ctx, events, deliverScanDispatchJob(t, ctx, database, riverClient, redelivered.ID))

	// Then the redelivery only finishes the operation: no traversal, no second
	// apply, and the installed inventory is untouched.
	assertOperationStage(t, ctx, setupManager, redelivered.ID, "succeeded", "succeeded")
	requireScanDispatchCandidates(t, ctx, database, redelivered.ID, 0)
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 4)
	requireScanDispatchLocations(t, readScanDispatchLocations(t, ctx, database, root.ID), redeliveredLocations)
	if count := scanDispatchProbeCount(t, probes); count != probesBeforeDelivery {
		t.Fatalf("probes during the redelivery = %d, want no second traversal", count)
	}

	// Given an interrupted install the same recovery also has to resolve...
	installationID := uuid.New()
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: "1.6.1", RelativePath: filepath.Join("fpcalc", "1.6.1"), State: "preparing",
	}
	if err := setupManager.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create the interrupted installation: %v", err)
	}
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		TargetIdentity: "fpcalc:chromaprint:1.6.1:linux:amd64", SchemaVersion: 2, ToolsRoot: toolsRoot,
		PackageKind: tools.PackageFPCalc, SourceName: "chromaprint", ReleaseIdentity: "1.6.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	interruptedInstall := &persistence.Operation{
		ID: uuid.New(), Kind: "install", State: "running", Stage: "materialize",
		InputSnapshot: snapshot, TargetInstallationID: &installationID,
	}
	if err := setupManager.CreateOperation(ctx, interruptedInstall); err != nil {
		t.Fatalf("create the interrupted install operation: %v", err)
	}

	// When the startup recovery runs...
	reconcileScanRecovery(t, ctx, setupManager, operations, registry)

	// Then the install policy is the one it always had and the scan inventory of
	// this root is not a side effect of it.
	requireScanDispatchSafeError(t, readScanDispatchOperation(t, ctx, setupManager, interruptedInstall.ID), "The operation was interrupted. Retry the operation.")
	failedInstallation, err := setupManager.GetInstallation(ctx, installationID)
	if err != nil || failedInstallation.State != "failed" {
		t.Fatalf("interrupted installation = %#v, lookup error %v; want failed", failedInstallation, err)
	}
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 4)
	requireScanDispatchLocations(t, readScanDispatchLocations(t, ctx, database, root.ID), redeliveredLocations)

	// A pre-pinning install snapshot cannot borrow the current setting as its
	// historical root. Recovery must fail it without cleaning staging under the
	// currently configured tools directory.
	legacyInstallationID := uuid.New()
	legacyInstallation := &persistence.ToolInstallation{
		ID: legacyInstallationID, PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: "1.6.2", RelativePath: filepath.Join("fpcalc", "1.6.2"), State: "preparing",
	}
	if err := setupManager.CreateInstallation(ctx, legacyInstallation); err != nil {
		t.Fatalf("create the legacy installation: %v", err)
	}
	legacyOperation := &persistence.Operation{
		ID: uuid.New(), Kind: "install", State: "running", Stage: "materialize",
		InputSnapshot:        json.RawMessage(`{"schema_version":1,"target_identity":"fpcalc:chromaprint:1.6.2:linux:amd64","package_kind":"fpcalc","source_name":"chromaprint","release_identity":"1.6.2"}`),
		TargetInstallationID: &legacyInstallationID,
	}
	if err := setupManager.CreateOperation(ctx, legacyOperation); err != nil {
		t.Fatalf("create the legacy install operation: %v", err)
	}
	legacyStaging, err := tools.EnsureOperationStaging(toolsRoot, legacyOperation.ID)
	if err != nil {
		t.Fatalf("create legacy staging marker directory: %v", err)
	}
	marker := filepath.Join(legacyStaging, "must-not-be-adopted")
	if err := os.WriteFile(marker, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("write legacy staging marker: %v", err)
	}
	if err := ReconcileInterruptedOperations(ctx, setupManager, operations,
		func(context.Context, *int64) (bool, error) { return false, nil }, registry); err != nil {
		t.Fatalf("reconcile legacy install: %v", err)
	}
	legacyFailed := readScanDispatchOperation(t, ctx, setupManager, legacyOperation.ID)
	requireScanDispatchSafeError(t, legacyFailed, "The installation snapshot predates tools-directory pinning and cannot be safely recovered. Start a new installation.")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("legacy recovery touched staging under the current tools root: %v", err)
	}
	failedLegacyInstallation, err := setupManager.GetInstallation(ctx, legacyInstallationID)
	if err != nil || failedLegacyInstallation.State != "failed" {
		t.Fatalf("legacy installation = %#v, lookup error %v; want failed", failedLegacyInstallation, err)
	}

	// Given a queued scan of the root whose River delivery is still live...
	live := createScanDispatchOperation(t, ctx, setupManager, inventory, first.InputSnapshot, root.ID, root.ConfiguredPath)
	attachScanRecoveryJob(t, ctx, database, riverClient, live, true)

	// When the recovery runs while that delivery is live...
	if err := ReconcileInterruptedOperations(ctx, setupManager, operations,
		func(context.Context, *int64) (bool, error) { return true, nil }, registry); err != nil {
		t.Fatalf("reconcile operations with a live scan delivery: %v", err)
	}

	// Then the scan is left to its own worker and the inventory is untouched.
	assertOperationStage(t, ctx, setupManager, live.ID, "queued", service.SourceScanStageQueued)
	requireScanDispatchGeneration(t, ctx, inventory, root.ID, 4)
	requireScanDispatchLocations(t, readScanDispatchLocations(t, ctx, database, root.ID), redeliveredLocations)
}

func TestLegacyInstallStartupRecoveryPreservesTerminalInstallationStatesPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()

	settingsRepository := persistence.NewSettingsRepository(database)
	root, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setRuntimeRoots(t, ctx, settingsRepository, root)
	runtimeSettings := settings.New(settingsRepository, nil)
	repository := persistence.NewSetupManagerRepository(database)
	operations := service.NewOperations(repository)

	for _, state := range []string{"ready", "failed"} {
		t.Run(state, func(t *testing.T) {
			release := "1.6.9-" + state
			installation := &persistence.ToolInstallation{
				ID: uuid.New(), PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "linux", PlatformGOARCH: "amd64",
				SourceName: "chromaprint", ReleaseIdentity: release, RelativePath: filepath.Join("fpcalc", release), State: "preparing",
			}
			if err := repository.CreateInstallation(ctx, installation); err != nil {
				t.Fatalf("create %s installation: %v", state, err)
			}
			switch state {
			case "ready":
				if err := repository.MarkInstallationReady(ctx, installation.ID, json.RawMessage(`{"fpcalc":"fpcalc version 1.6.9"}`), time.Now().UTC()); err != nil {
					t.Fatalf("mark installation ready: %v", err)
				}
			case "failed":
				if err := repository.MarkInstallationFailed(ctx, installation.ID); err != nil {
					t.Fatalf("mark installation failed: %v", err)
				}
			}
			operation := &persistence.Operation{
				ID: uuid.New(), Kind: "install", State: "running", Stage: "materialize",
				InputSnapshot: json.RawMessage(`{"schema_version":1,"target_identity":"fpcalc:chromaprint:` + release + `:linux:amd64"}`), TargetInstallationID: &installation.ID,
			}
			if err := repository.CreateOperation(ctx, operation); err != nil {
				t.Fatalf("create legacy installation operation: %v", err)
			}
			if err := ReconcileInterruptedOperations(ctx, repository, operations,
				func(context.Context, *int64) (bool, error) { return false, nil }, runtimeSettings); err != nil {
				t.Fatalf("reconcile legacy %s installation: %v", state, err)
			}
			gotInstallation, err := repository.GetInstallation(ctx, installation.ID)
			if err != nil || gotInstallation.State != state {
				t.Fatalf("recovered installation state = %v, %v; want preserved %s", gotInstallation, err, state)
			}
			gotOperation, err := repository.GetOperation(ctx, operation.ID)
			const safeError = "The installation snapshot predates tools-directory pinning and cannot be safely recovered. Start a new installation."
			if err != nil || gotOperation.State != "failed" || gotOperation.SafeError == nil || *gotOperation.SafeError != safeError {
				t.Fatalf("recovered operation = %#v, %v; want failed with safe legacy error", gotOperation, err)
			}
		})
	}
}

// scanRecoveryRiverSlot lets the operations service be built before the River
// client exists, exactly as the composition root wires the application: the retry
// inserts its job through the slot once River has started.
type scanRecoveryRiverSlot struct{ persistence.RiverInserter }

// scanRecoveryCandidates describes approved files of a source tree the way a
// traversal would have stored them: the on-disk size and mtime plus the status
// the fake managed ffprobe answers for the file name.
func scanRecoveryCandidates(t *testing.T, sourceDirectory string, relativePaths ...string) []persistence.SourceScanCandidateInput {
	t.Helper()
	candidates := make([]persistence.SourceScanCandidateInput, 0, len(relativePaths))
	for _, relativePath := range relativePaths {
		info, err := os.Stat(filepath.Join(sourceDirectory, relativePath))
		if err != nil {
			t.Fatalf("stat the source file %q: %v", relativePath, err)
		}
		candidate := persistence.SourceScanCandidateInput{
			RelativePath: relativePath, SizeBytes: info.Size(),
			Mtime: info.ModTime().Truncate(time.Microsecond), ProbeStatus: persistence.SourceProbeStatusAudio,
		}
		switch {
		case strings.Contains(relativePath, "silent"):
			candidate.ProbeStatus = persistence.SourceProbeStatusNoAudio
		case strings.Contains(relativePath, "broken"):
			reason := "ffprobe could not confirm an audio stream in this file. The next scan will check it again."
			candidate.ProbeStatus = persistence.SourceProbeStatusProbeError
			candidate.SafeError = &reason
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func appendScanRecoveryCandidates(t *testing.T, ctx context.Context, inventory *persistence.SourceInventoryRepository, operationID uuid.UUID, candidates ...persistence.SourceScanCandidateInput) {
	t.Helper()
	if err := inventory.AppendSourceScanCandidates(ctx, operationID, candidates); err != nil {
		t.Fatalf("store the scan candidates of %s: %v", operationID, err)
	}
}

func applyScanGeneration(t *testing.T, ctx context.Context, setup *persistence.SetupManagerRepository, operations *service.Operations, inventory *persistence.SourceInventoryRepository, operationID uuid.UUID, configuredPath string) {
	t.Helper()
	operation, err := setup.GetOperation(ctx, operationID)
	if err != nil {
		t.Fatalf("read persisted scan operation %s: %v", operationID, err)
	}
	if operation.RiverJobID == nil || operation.Attempt < 1 {
		t.Fatalf("scan operation %s has no persisted attempt/job fence: %+v", operationID, operation)
	}
	if operation.State != "running" {
		if err := operations.Running(ctx, operationID, service.SourceScanStageApplying); err != nil {
			t.Fatalf("mark persisted scan operation %s running before apply: %v", operationID, err)
		}
	}
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: operationID, ExpectedConfiguredPath: configuredPath,
		ExpectedAttempt: operation.Attempt, ExpectedJobID: *operation.RiverJobID,
	}); err != nil {
		t.Fatalf("apply the scan generation of %s: %v", operationID, err)
	}
}

func attachScanRecoveryJob(t *testing.T, ctx context.Context, database *bun.DB, client *river.Client[*sql.Tx], operation *persistence.Operation, live bool) {
	t.Helper()
	tx, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin persisted recovery delivery: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	inserted, err := client.InsertTx(ctx, tx, service.ScanSourceJobArgs{OperationID: operation.ID}, nil)
	if err != nil {
		t.Fatalf("insert persisted recovery delivery: %v", err)
	}
	if live {
		if _, err := tx.ExecContext(ctx, `UPDATE river_job SET state='scheduled', scheduled_at=now() + interval '1 hour' WHERE id=$1`, inserted.Job.ID); err != nil {
			t.Fatalf("schedule live recovery delivery: %v", err)
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE river_job SET state='completed', finalized_at=now() WHERE id=$1`, inserted.Job.ID); err != nil {
		t.Fatalf("finish orphan recovery delivery: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE operation SET river_job_id = $1 WHERE id = $2", inserted.Job.ID, operation.ID); err != nil {
		t.Fatalf("attach persisted recovery delivery to operation: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit persisted recovery delivery: %v", err)
	}
	operation.RiverJobID = &inserted.Job.ID
}

// reconcileScanRecovery runs the production startup recovery with no live River
// delivery, which is the state the application reconciles before it starts its
// workers.
func reconcileScanRecovery(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, operations *service.Operations, runtimeSettings interruptedOperationSettings) {
	t.Helper()
	if err := ReconcileInterruptedOperations(ctx, repository, operations,
		func(context.Context, *int64) (bool, error) { return false, nil }, runtimeSettings); err != nil {
		t.Fatalf("reconcile interrupted operations: %v", err)
	}
}
