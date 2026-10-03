//go:build integration

package jobs

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

func TestInstallationWorkerRiverDispatchPostgreSQL(t *testing.T) {
	database, databaseURL := openDispatchDatabase(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	repository := persistence.NewSetupManagerRepository(database)
	settingsRepository := persistence.NewSettingsRepository(database)
	root, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setRuntimeRoots(t, ctx, settingsRepository, root)
	runtimeSettings := settings.New(settingsRepository, nil)

	catalog := &dispatchCatalog{
		release: tools.Release{
			Identity:  "1.6.1",
			Artifacts: []tools.Artifact{{Name: "fpcalc.zip"}},
		},
		archive: dispatchZipWithExecutable(t, "fpcalc", []byte("test executable")),
	}
	catalog.checksum = sha256String(catalog.archive)
	catalog.release.Artifacts[0].ChecksumSHA256 = catalog.checksum
	workerOperations := service.NewOperations(repository)
	worker := NewInstallationWorker(repository, workerOperations, catalog, runtimeSettings,
		tools.Platform{GOOS: "linux", GOARCH: "amd64"}, tools.NewLifecycle(&dispatchRunner{}))
	riverClient, listenerPool := startDispatchRiver(t, databaseURL, database, worker)
	defer listenerPool.Close()
	defer stopRiverClient(t, riverClient)
	events, cancelEvents := riverClient.Subscribe(river.EventKindJobCompleted)
	defer cancelEvents()
	operations := service.NewOperationsWithRiver(repository, riverClient)

	succeeded := enqueueInstall(t, ctx, repository, riverClient, root, "1.6.1")
	awaitRiverCompletion(t, ctx, events, *succeeded.RiverJobID)
	assertOperationState(t, ctx, repository, succeeded.ID, "succeeded")
	ready, err := repository.GetInstallation(ctx, *succeeded.TargetInstallationID)
	if err != nil {
		t.Fatalf("load successful installation: %v", err)
	}
	if ready.State != "ready" {
		t.Fatalf("successful installation %s state = %q; want ready", ready.ID, ready.State)
	}
	executable := filepath.Join(root, ready.RelativePath, "fpcalc")
	if _, err := os.Stat(executable); err != nil {
		t.Fatalf("published executable is missing: %v", err)
	}

	resolvesBeforeDuplicate := catalog.resolveCount()
	duplicateJobID := enqueueDuplicateDelivery(t, ctx, database, riverClient, succeeded.ID)
	awaitRiverCompletion(t, ctx, events, duplicateJobID)
	if got := catalog.resolveCount(); got != resolvesBeforeDuplicate {
		t.Fatalf("duplicate delivery resolved catalog %d times after success; want no additional resolve", got-resolvesBeforeDuplicate)
	}
	assertOperationState(t, ctx, repository, succeeded.ID, "succeeded")

	catalog.setReleaseIdentity("1.6.2")
	catalog.setResolveError(errors.New("private upstream diagnostic"))
	failed := enqueueInstall(t, ctx, repository, riverClient, root, "1.6.2")
	awaitRiverCompletion(t, ctx, events, *failed.RiverJobID)
	failedSnapshot, err := repository.GetOperation(ctx, failed.ID)
	if err != nil || failedSnapshot.State != "failed" || failedSnapshot.SafeError == nil {
		state := ""
		hasSafeError := false
		if failedSnapshot != nil {
			state = failedSnapshot.State
			hasSafeError = failedSnapshot.SafeError != nil
		}
		t.Fatalf("failed operation state = %q, safe error present = %t, lookup error = %v; want failed with a safe error",
			state, hasSafeError, err)
	}
	if *failedSnapshot.SafeError == "private upstream diagnostic" {
		t.Fatal("raw catalog error was persisted as the user-facing failure")
	}
	failedInstallation, err := repository.GetInstallation(ctx, *failed.TargetInstallationID)
	if err != nil || failedInstallation.State != "failed" {
		state := ""
		if failedInstallation != nil {
			state = failedInstallation.State
		}
		t.Fatalf("failed target state = %q, lookup error = %v; want failed", state, err)
	}

	catalog.setResolveError(nil)
	retried, err := operations.Retry(ctx, failed.ID)
	if err != nil {
		t.Fatalf("enqueue production retry: %v", err)
	}
	if retried.Attempt != 2 || retried.TargetInstallationID == nil ||
		*retried.TargetInstallationID != *failed.TargetInstallationID || retried.RiverJobID == nil {
		t.Fatalf("retry did not preserve operation target and advance attempt: %#v", retried)
	}
	awaitRiverCompletion(t, ctx, events, *retried.RiverJobID)
	assertOperationState(t, ctx, repository, failed.ID, "succeeded")
	retriedInstallation, err := repository.GetInstallation(ctx, *failed.TargetInstallationID)
	if err != nil || retriedInstallation.State != "ready" {
		t.Fatalf("retried target = %#v, %v; want ready", retriedInstallation, err)
	}
	installations, err := repository.ListInstallations(ctx, "fpcalc", "linux", "amd64")
	if err != nil || len(installations) != 2 {
		t.Fatalf("installations after retry = %d, %v; want two identities and no duplicate target", len(installations), err)
	}
}

func TestInstallationWorkerRiverStageInterruptionRecoveryPostgreSQL(t *testing.T) {
	database, databaseURL := openDispatchDatabase(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	repository := persistence.NewSetupManagerRepository(database)
	settingsRepository := persistence.NewSettingsRepository(database)
	root, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setRuntimeRoots(t, ctx, settingsRepository, root)
	runtimeSettings := settings.New(settingsRepository, nil)
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}

	// These barriers fire after the operation-stage transaction commits or at a
	// narrow catalog/runner/repository boundary, then StopAndCancel interrupts the
	// actual River delivery. ExtractZip and the local SHA256 read have no hook;
	// the extract case therefore interrupts just after its durable stage update,
	// while their malformed/traversal/digest cases remain covered by focused tests.
	cases := []struct {
		name                  string
		pauseStage            string
		pauseIn               string
		runnerCall            int
		wantStage             string
		wantInstallationState string
		wantSucceededAtPause  bool
		wantPublishedAtPause  bool
	}{
		{name: "resolve_catalog", pauseIn: "resolve", wantStage: "resolve", wantInstallationState: "preparing"},
		{name: "download_payload", pauseIn: "download", wantStage: "download", wantInstallationState: "preparing"},
		{name: "checksum_lookup", pauseIn: "checksum", wantStage: "download", wantInstallationState: "preparing"},
		{name: "extract_stage_checkpoint", pauseStage: "extract", wantStage: "extract", wantInstallationState: "preparing"},
		{name: "materialize_stage_checkpoint", pauseStage: "materialize", wantStage: "materialize", wantInstallationState: "preparing"},
		{name: "materialize_executable_verification", pauseIn: "runner", runnerCall: 1, wantStage: "materialize", wantInstallationState: "preparing"},
		{name: "verify_published_executable", pauseIn: "runner", runnerCall: 2, wantStage: "materialize", wantInstallationState: "preparing", wantPublishedAtPause: true},
		{name: "publication_before_ready_commit", pauseStage: "files_materialized", wantStage: "files_materialized", wantInstallationState: "preparing", wantPublishedAtPause: true},
		{name: "ready_transaction_committed", pauseIn: "ready_commit", wantStage: "files_materialized", wantInstallationState: "ready", wantPublishedAtPause: true},
		{name: "cleanup_before_retry_ack", pauseIn: "success_commit", wantStage: "succeeded", wantInstallationState: "ready", wantSucceededAtPause: true, wantPublishedAtPause: true},
	}

	for index, interruption := range cases {
		t.Run(interruption.name, func(t *testing.T) {
			release := fmt.Sprintf("2.0.%d", index)
			catalog := newDispatchCatalog(t, release)
			gate := newDispatchBarrier()
			boundaryRepository := &riverBoundaryRepository{SetupManagerRepository: repository}
			runner := &dispatchRunner{}
			switch interruption.pauseIn {
			case "resolve":
				catalog.resolveGate = gate
			case "download":
				catalog.downloadGate = gate
			case "checksum":
				catalog.checksumGate = gate
			case "runner":
				runner.gate, runner.pauseAt = gate, interruption.runnerCall
			case "ready_commit":
				boundaryRepository.readyGate = gate
			case "success_commit":
				boundaryRepository.successGate = gate
			default:
				boundaryRepository.stage, boundaryRepository.stageGate = interruption.pauseStage, gate
			}

			operations := service.NewOperations(boundaryRepository)
			worker := NewInstallationWorker(boundaryRepository, operations, catalog, runtimeSettings, platform, tools.NewLifecycle(runner))
			session := newDispatchRiverSession(t, ctx, databaseURL, database, worker)
			events, cancelEvents := session.client.Subscribe(river.EventKindJobCompleted)
			defer cancelEvents()
			operation := enqueueInstall(t, ctx, repository, session.client, root, release)

			awaitDispatchBarrier(t, ctx, gate, events, *operation.RiverJobID, repository, operation.ID)
			current, err := repository.GetOperation(ctx, operation.ID)
			state, stage := operationStateStage(current)
			if err != nil {
				t.Fatalf("read operation at %s barrier: %v", interruption.name, err)
			}
			if interruption.wantSucceededAtPause {
				if state != "succeeded" || stage != interruption.wantStage {
					t.Fatalf("operation at %s barrier = %s/%s; want succeeded/%s",
						interruption.name, state, stage, interruption.wantStage)
				}
			} else if state != "running" || stage != interruption.wantStage {
				t.Fatalf("operation at %s barrier = %s/%s; want running/%s",
					interruption.name, state, stage, interruption.wantStage)
			}
			installation, err := repository.GetInstallation(ctx, *operation.TargetInstallationID)
			if err != nil || installation.State != interruption.wantInstallationState {
				installState := ""
				if installation != nil {
					installState = installation.State
				}
				t.Fatalf("installation at %s barrier = %q, lookup error %v; want %q",
					interruption.name, installState, err, interruption.wantInstallationState)
			}
			target := filepath.Join(root, installation.RelativePath, "fpcalc")
			_, targetErr := os.Lstat(target)
			if interruption.wantPublishedAtPause && targetErr != nil {
				t.Fatalf("published target missing at %s barrier: %v", interruption.name, targetErr)
			}
			if !interruption.wantPublishedAtPause && !os.IsNotExist(targetErr) {
				t.Fatalf("target unexpectedly published at %s barrier: %v", interruption.name, targetErr)
			}
			staging := filepath.Join(root, ".staging", operation.ID.String())
			_, stagingErr := os.Lstat(staging)
			if interruption.wantSucceededAtPause && !os.IsNotExist(stagingErr) {
				t.Fatalf("staging remains at post-cleanup barrier: %v", stagingErr)
			}

			session.stopAndCancel(t, ctx)
			boundaryRepository.clearBarriers()
			catalog.clearBarriers()
			runner.gate = nil

			if interruption.name == "publication_before_ready_commit" || interruption.name == "verify_published_executable" {
				activeFFmpegBefore, hasActiveFFmpegBefore, err := settingsRepository.Get(ctx, settings.ActiveFFmpegInstallationKey)
				if err != nil {
					t.Fatal(err)
				}
				activeFPCalcBefore, hasActiveFPCalcBefore, err := settingsRepository.Get(ctx, settings.ActiveFPCalcInstallationKey)
				if err != nil {
					t.Fatal(err)
				}
				if err := ReconcileInterruptedOperations(ctx, boundaryRepository, service.NewOperations(boundaryRepository),
					func(context.Context, *int64) (bool, error) { return false, nil }, runtimeSettings, tools.NewLifecycle(runner)); err != nil {
					t.Fatalf("reconcile verified publication before ready commit: %v", err)
				}
				completed, err := repository.GetOperation(ctx, operation.ID)
				if err != nil || completed.State != "succeeded" {
					t.Fatalf("reconciled published operation = %#v, %v; want succeeded", completed, err)
				}
				ready, err := repository.GetInstallation(ctx, *operation.TargetInstallationID)
				if err != nil || ready.State != "ready" {
					t.Fatalf("reconciled published installation = %#v, %v; want ready", ready, err)
				}
				if _, err := os.Lstat(staging); !os.IsNotExist(err) {
					t.Fatalf("reconciled published install staging remains: %v", err)
				}
				installations, err := repository.ListInstallations(ctx, "fpcalc", platform.GOOS, platform.GOARCH)
				if err != nil {
					t.Fatalf("list fpcalc installations after publication reconciliation: %v", err)
				}
				targetCount := 0
				for _, existing := range installations {
					if existing.ID == *operation.TargetInstallationID {
						targetCount++
					}
				}
				if targetCount != 1 {
					t.Fatalf("target installation rows after publication reconciliation = %d; want exactly 1", targetCount)
				}
				activeFFmpegAfter, hasActiveFFmpegAfter, err := settingsRepository.Get(ctx, settings.ActiveFFmpegInstallationKey)
				if err != nil || hasActiveFFmpegAfter != hasActiveFFmpegBefore || activeFFmpegAfter != activeFFmpegBefore {
					t.Fatalf("active FFmpeg ID changed during publication reconciliation: %q/%v -> %q/%v (%v)", activeFFmpegBefore, hasActiveFFmpegBefore, activeFFmpegAfter, hasActiveFFmpegAfter, err)
				}
				activeFPCalcAfter, hasActiveFPCalcAfter, err := settingsRepository.Get(ctx, settings.ActiveFPCalcInstallationKey)
				if err != nil {
					t.Fatal(err)
				}
				setupComplete, err := runtimeSettings.SetupCompleted(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if setupComplete {
					if hasActiveFPCalcAfter != hasActiveFPCalcBefore || activeFPCalcAfter != activeFPCalcBefore {
						t.Fatalf("completed Setup changed active fpcalc ID: %q/%v -> %q/%v", activeFPCalcBefore, hasActiveFPCalcBefore, activeFPCalcAfter, hasActiveFPCalcAfter)
					}
				} else if !hasActiveFPCalcAfter || activeFPCalcAfter != operation.TargetInstallationID.String() {
					t.Fatalf("incomplete Setup active fpcalc ID = %q/%v; want recovered installation %s", activeFPCalcAfter, hasActiveFPCalcAfter, *operation.TargetInstallationID)
				}
				if _, err := operations.Retry(ctx, operation.ID); err == nil {
					t.Fatal("verified publication was not terminal after reconciliation")
				}
				session.close(t)
				return
			}

			recoveryOperations := service.NewOperations(boundaryRepository)
			recoveryWorker := NewInstallationWorker(boundaryRepository, recoveryOperations, catalog, runtimeSettings, platform, tools.NewLifecycle(runner))
			recovery := newDispatchRiverSession(t, ctx, databaseURL, database, recoveryWorker)
			recoveryEvents, cancelRecoveryEvents := recovery.client.Subscribe(river.EventKindJobCompleted)
			defer cancelRecoveryEvents()
			recoveryJobID := enqueueDuplicateDelivery(t, ctx, database, recovery.client, operation.ID)
			awaitRiverCompletion(t, ctx, recoveryEvents, recoveryJobID)
			assertOperationState(t, ctx, repository, operation.ID, "succeeded")
			ready, err := repository.GetInstallation(ctx, *operation.TargetInstallationID)
			if err != nil || ready.State != "ready" {
				state := ""
				if ready != nil {
					state = ready.State
				}
				t.Fatalf("installation after %s recovery = %q, lookup error %v; want ready", interruption.name, state, err)
			}
			if _, err := os.Lstat(staging); !os.IsNotExist(err) {
				t.Fatalf("staging remains after %s recovery: %v", interruption.name, err)
			}
			recovery.close(t)
		})
	}
}

func TestMoveWorkerRiverInterruptionRecoveryPostgreSQL(t *testing.T) {
	database, databaseURL := openDispatchDatabase(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	repository := persistence.NewSetupManagerRepository(database)
	settingsRepository := persistence.NewSettingsRepository(database)
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}

	oldRoot, err := settings.NormalizePath(filepath.Join(t.TempDir(), "old-tools"))
	if err != nil {
		t.Fatalf("normalize old tools root fixture: %v", err)
	}
	newRoot, err := settings.NormalizePath(filepath.Join(t.TempDir(), "new-tools"))
	if err != nil {
		t.Fatalf("normalize new tools root fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(oldRoot, "fpcalc", "1.6.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(oldRoot, "fpcalc", "1.6.1", "fpcalc")
	payload := []byte("managed executable bytes")
	if err := os.WriteFile(source, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldRoot, "operator-data"), []byte("keep source unknowns"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "operator-data"), []byte("keep target unknowns"), 0o600); err != nil {
		t.Fatal(err)
	}
	setRuntimeRoots(t, ctx, settingsRepository, oldRoot)
	registry := settings.New(settingsRepository, nil)

	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFPCalc), PlatformGOOS: platform.GOOS,
		PlatformGOARCH: platform.GOARCH, SourceName: "chromaprint", ReleaseIdentity: "1.6.1",
		RelativePath: "fpcalc/1.6.1", State: "preparing",
	}
	if err := repository.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create managed installation: %v", err)
	}
	if err := repository.MarkInstallationReady(ctx, installation.ID,
		json.RawMessage(`{"fpcalc":"fpcalc version 1.6.1"}`), time.Now().UTC()); err != nil {
		t.Fatalf("mark managed installation ready: %v", err)
	}
	moveBarrier := newDispatchBarrier()
	barrierRunner := &dispatchRunner{gate: moveBarrier}
	workerOperations := service.NewOperations(repository)
	worker := NewInstallationWorker(repository, workerOperations, &dispatchCatalog{}, registry, platform,
		tools.NewLifecycle(barrierRunner))
	worker.SetMoveWorker(NewMoveWorker(repository, workerOperations, registry, platform, tools.NewLifecycle(barrierRunner)))
	riverClient, listenerPool := startDispatchRiver(t, databaseURL, database, worker)
	moveEvents, cancelMoveEvents := riverClient.Subscribe(river.EventKindJobCompleted)
	defer cancelMoveEvents()
	clientStopped := false
	t.Cleanup(func() {
		if !clientStopped {
			stopRiverClient(t, riverClient)
		}
		listenerPool.Close()
	})
	moveService := service.NewMoveTools(repository, registry, platform, riverClient)
	preflight, err := moveService.Preflight(ctx, newRoot, false)
	if err != nil {
		t.Fatalf("preflight production move: %v", err)
	}
	move, err := moveService.Start(ctx, preflight, nil)
	if err != nil {
		t.Fatalf("atomically start production move: %v", err)
	}

	select {
	case <-moveBarrier.entered:
	case event := <-moveEvents:
		if event != nil && event.Job != nil && event.Job.ID == *move.RiverJobID {
			diagnosticCtx, diagnosticCancel := context.WithTimeout(context.Background(), time.Second)
			defer diagnosticCancel()
			completed, lookupErr := repository.GetOperation(diagnosticCtx, move.ID)
			state, stage := operationStateStage(completed)
			var safeError string
			if completed != nil && completed.SafeError != nil {
				safeError = *completed.SafeError
			}
			t.Fatalf("move job completed before verification barrier: operation state/stage=%q/%q safe_error=%q lookup_error=%v",
				state, stage, safeError, lookupErr)
		}
		t.Fatalf("received unrelated River completion before move verification barrier")
	case <-ctx.Done():
		diagnosticCtx, diagnosticCancel := context.WithTimeout(context.Background(), time.Second)
		defer diagnosticCancel()
		current, lookupErr := repository.GetOperation(diagnosticCtx, move.ID)
		state, stage := operationStateStage(current)
		t.Fatalf("move did not reach executable verification barrier before deadline: state/stage=%q/%q lookup_error=%v; context=%v",
			state, stage, lookupErr, ctx.Err())
	}
	interrupted, err := repository.GetOperation(ctx, move.ID)
	if err != nil || interrupted.State != "running" || interrupted.Stage != "verify" {
		state, stage := operationStateStage(interrupted)
		t.Fatalf("operation at interruption barrier state/stage = %q/%q, lookup error = %v; want running/verify",
			state, stage, err)
	}
	if _, err := os.Stat(filepath.Join(newRoot, installation.RelativePath, "fpcalc")); !os.IsNotExist(err) {
		t.Fatalf("target was published before verification completed: stat error %v", err)
	}
	if err := riverClient.StopAndCancel(ctx); err != nil {
		t.Fatalf("cancel running River client at verification barrier: %v", err)
	}
	clientStopped = true
	barrierRunner.gate = nil
	afterInterruption, err := repository.GetOperation(ctx, move.ID)
	if err != nil || afterInterruption.State != "running" || afterInterruption.Stage != "verify" {
		state, stage := operationStateStage(afterInterruption)
		t.Fatalf("interrupted operation state/stage = %q/%q, lookup error = %v; want running/verify",
			state, stage, err)
	}
	currentRoot, exists, err := registry.GetToolsDirectory(ctx)
	if err != nil || !exists || currentRoot != oldRoot {
		t.Fatalf("tools root after interruption = %q, %v, %v; want old root", currentRoot, exists, err)
	}

	recoveryClient, recoveryListenerPool := startDispatchRiver(t, databaseURL, database, worker)
	defer recoveryListenerPool.Close()
	defer stopRiverClient(t, recoveryClient)
	recoveryEvents, cancelRecoveryEvents := recoveryClient.Subscribe(river.EventKindJobCompleted)
	defer cancelRecoveryEvents()
	recoveryJobID := enqueueDuplicateDelivery(t, ctx, database, recoveryClient, move.ID)
	awaitRiverCompletion(t, ctx, recoveryEvents, recoveryJobID)
	assertOperationState(t, ctx, repository, move.ID, "succeeded")
	currentRoot, exists, err = registry.GetToolsDirectory(ctx)
	if err != nil || !exists || currentRoot != newRoot {
		t.Fatalf("tools root after recovery = %q, %v, %v; want new root", currentRoot, exists, err)
	}
	target := filepath.Join(newRoot, installation.RelativePath, "fpcalc")
	if moved, err := os.ReadFile(target); err != nil || string(moved) != string(payload) {
		t.Fatalf("recovered target = %q, %v; want original payload", moved, err)
	}
	for _, unknown := range []struct {
		path, contents string
	}{
		{filepath.Join(oldRoot, "operator-data"), "keep source unknowns"},
		{filepath.Join(newRoot, "operator-data"), "keep target unknowns"},
	} {
		contents, err := os.ReadFile(unknown.path)
		if err != nil || string(contents) != unknown.contents {
			t.Fatalf("unknown move data %q = %q, %v", unknown.path, contents, err)
		}
	}
}

func TestMoveWorkerRiverSwitchAndCleanupInterruptionRecoveryPostgreSQL(t *testing.T) {
	database, databaseURL := openDispatchDatabase(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	repository := persistence.NewSetupManagerRepository(database)
	boundaryRepository := &riverBoundaryRepository{SetupManagerRepository: repository}
	settingsRepository := persistence.NewSettingsRepository(database)
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}
	oldRoot, err := settings.NormalizePath(filepath.Join(t.TempDir(), "tools-0"))
	if err != nil {
		t.Fatalf("normalize initial tools root: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(oldRoot, "fpcalc", "1.6.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("move switch fixture")
	if err := os.WriteFile(filepath.Join(oldRoot, "fpcalc", "1.6.1", "fpcalc"), payload, 0o755); err != nil {
		t.Fatal(err)
	}
	setRuntimeRoots(t, ctx, settingsRepository, oldRoot)
	registry := settings.New(settingsRepository, nil)
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFPCalc), PlatformGOOS: platform.GOOS,
		PlatformGOARCH: platform.GOARCH, SourceName: "chromaprint", ReleaseIdentity: "1.6.1",
		RelativePath: "fpcalc/1.6.1", State: "preparing",
	}
	if err := repository.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create managed installation: %v", err)
	}
	if err := repository.MarkInstallationReady(ctx, installation.ID,
		json.RawMessage(`{"fpcalc":"fpcalc version 1.6.1"}`), time.Now().UTC()); err != nil {
		t.Fatalf("mark managed installation ready: %v", err)
	}

	cases := []struct {
		name       string
		pauseAfter string
	}{
		{name: "tools_root_switch_committed", pauseAfter: "switch"},
		{name: "operation_success_before_staging_cleanup", pauseAfter: "cleanup"},
	}
	for index, interruption := range cases {
		newRoot, err := settings.NormalizePath(filepath.Join(t.TempDir(), fmt.Sprintf("tools-%d", index+1)))
		if err != nil {
			t.Fatalf("normalize target tools root: %v", err)
		}
		t.Run(interruption.name, func(t *testing.T) {
			gate := newDispatchBarrier()
			if interruption.pauseAfter == "switch" {
				boundaryRepository.moveCommit = gate
			} else {
				boundaryRepository.moveFinished = gate
			}
			workerOperations := service.NewOperations(boundaryRepository)
			worker := NewInstallationWorker(boundaryRepository, workerOperations, &dispatchCatalog{}, registry,
				platform, tools.NewLifecycle(&dispatchRunner{}))
			worker.SetMoveWorker(NewMoveWorker(boundaryRepository, workerOperations, registry, platform, tools.NewLifecycle(&dispatchRunner{})))
			session := newDispatchRiverSession(t, ctx, databaseURL, database, worker)
			events, cancelEvents := session.client.Subscribe(river.EventKindJobCompleted)
			defer cancelEvents()
			moveService := service.NewMoveTools(boundaryRepository, registry, platform, session.client)
			preflight, err := moveService.Preflight(ctx, newRoot, false)
			if err != nil {
				t.Fatalf("preflight move %s: %v", interruption.name, err)
			}
			move, err := moveService.Start(ctx, preflight, nil)
			if err != nil {
				t.Fatalf("start move %s: %v", interruption.name, err)
			}
			awaitDispatchBarrier(t, ctx, gate, events, *move.RiverJobID, repository, move.ID)
			current, err := repository.GetOperation(ctx, move.ID)
			state, stage := operationStateStage(current)
			if err != nil {
				t.Fatalf("read operation at %s barrier: %v", interruption.name, err)
			}
			wantState := "running"
			if interruption.pauseAfter == "switch" && (state != wantState || stage != "switched") {
				t.Fatalf("switch recovery barrier operation = %s/%s; want running/switched", state, stage)
			}
			if interruption.pauseAfter == "cleanup" {
				wantState = "succeeded"
				if state != wantState || stage != "switched" {
					t.Fatalf("cleanup recovery barrier operation = %s/%s; want succeeded/switched", state, stage)
				}
			}
			currentRoot, exists, err := registry.GetToolsDirectory(ctx)
			if err != nil || !exists || currentRoot != newRoot {
				t.Fatalf("tools root at %s barrier = %q, %v, %v; want committed target", interruption.name, currentRoot, exists, err)
			}
			staging := filepath.Join(newRoot, ".staging", move.ID.String())
			if _, err := os.Stat(staging); err != nil {
				t.Fatalf("staging missing at %s barrier: %v", interruption.name, err)
			}
			session.stopAndCancel(t, ctx)
			boundaryRepository.clearBarriers()

			recoveryOperations := service.NewOperations(boundaryRepository)
			recoveryWorker := NewInstallationWorker(boundaryRepository, recoveryOperations, &dispatchCatalog{}, registry,
				platform, tools.NewLifecycle(&dispatchRunner{}))
			recoveryWorker.SetMoveWorker(NewMoveWorker(boundaryRepository, recoveryOperations, registry, platform, tools.NewLifecycle(&dispatchRunner{})))
			recovery := newDispatchRiverSession(t, ctx, databaseURL, database, recoveryWorker)
			recoveryEvents, cancelRecoveryEvents := recovery.client.Subscribe(river.EventKindJobCompleted)
			defer cancelRecoveryEvents()
			jobID := enqueueDuplicateDelivery(t, ctx, database, recovery.client, move.ID)
			awaitRiverCompletion(t, ctx, recoveryEvents, jobID)
			assertOperationState(t, ctx, repository, move.ID, "succeeded")
			currentRoot, exists, err = registry.GetToolsDirectory(ctx)
			if err != nil || !exists || currentRoot != newRoot {
				t.Fatalf("tools root after %s recovery = %q, %v, %v", interruption.name, currentRoot, exists, err)
			}
			if _, err := os.Stat(staging); !os.IsNotExist(err) {
				t.Fatalf("staging remains after %s recovery: %v", interruption.name, err)
			}
			oldRoot = newRoot
			recovery.close(t)
		})
	}
}

func TestMoveWorkerRiverRecoversPublishedTargetBeforeOwnershipJournalPostgreSQL(t *testing.T) {
	database, databaseURL := openDispatchDatabase(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	repository := persistence.NewSetupManagerRepository(database)
	settingsRepository := persistence.NewSettingsRepository(database)
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}
	oldRoot, err := settings.NormalizePath(filepath.Join(t.TempDir(), "source-root"))
	if err != nil {
		t.Fatalf("normalize source tools root: %v", err)
	}
	newRoot, err := settings.NormalizePath(filepath.Join(t.TempDir(), "target-root"))
	if err != nil {
		t.Fatalf("normalize target tools root: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(oldRoot, "fpcalc", "1.6.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("published move witness")
	if err := os.WriteFile(filepath.Join(oldRoot, "fpcalc", "1.6.1", "fpcalc"), payload, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	setRuntimeRoots(t, ctx, settingsRepository, oldRoot)
	registry := settings.New(settingsRepository, nil)
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFPCalc), PlatformGOOS: platform.GOOS,
		PlatformGOARCH: platform.GOARCH, SourceName: "chromaprint", ReleaseIdentity: "1.6.1",
		RelativePath: "fpcalc/1.6.1", State: "preparing",
	}
	if err := repository.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create managed installation: %v", err)
	}
	if err := repository.MarkInstallationReady(ctx, installation.ID,
		json.RawMessage(`{"fpcalc":"fpcalc version 1.6.1"}`), time.Now().UTC()); err != nil {
		t.Fatalf("mark managed installation ready: %v", err)
	}
	moveService := service.NewMoveTools(repository, registry, platform, nil)
	preflight, err := moveService.Preflight(ctx, newRoot, false)
	if err != nil {
		t.Fatalf("preflight move publication fixture: %v", err)
	}
	operationID := uuid.New()
	staging, err := tools.EnsureOperationStaging(newRoot, operationID)
	if err != nil {
		t.Fatalf("create interrupted move staging: %v", err)
	}
	file := preflight.Snapshot.Files[0]
	payloadPath := filepath.Join(staging, "payload", file.RelativePath, file.Executable)
	if err := os.MkdirAll(filepath.Dir(payloadPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payloadPath, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	publication, err := newMovePublication(staging, operationID, preflight.Snapshot)
	if err != nil {
		t.Fatalf("write pre-publication journal: %v", err)
	}
	if publication.Files[0].Owned {
		t.Fatal("fixture journal already claims publication ownership")
	}
	// This durable fixture represents a process stop after os.Link created the
	// target but before the publication record could mark that target as owned.
	if err := os.MkdirAll(filepath.Dir(file.TargetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(payloadPath, file.TargetPath); err != nil {
		t.Fatalf("seed crash after target link before ownership journal update: %v", err)
	}

	boundaryRepository := &riverBoundaryRepository{SetupManagerRepository: repository}
	workerOperations := service.NewOperations(boundaryRepository)
	worker := NewInstallationWorker(boundaryRepository, workerOperations, &dispatchCatalog{}, registry,
		platform, tools.NewLifecycle(&dispatchRunner{}))
	worker.SetMoveWorker(NewMoveWorker(boundaryRepository, workerOperations, registry, platform, tools.NewLifecycle(&dispatchRunner{})))
	session := newDispatchRiverSession(t, ctx, databaseURL, database, worker)
	defer session.close(t)
	events, cancelEvents := session.client.Subscribe(river.EventKindJobCompleted)
	defer cancelEvents()
	snapshot, err := json.Marshal(preflight.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: operationID, Kind: "move_tools_root", State: "running", Stage: "commit_targets",
		InputSnapshot: snapshot,
	}
	if err := repository.CreateOperationAndEnqueue(ctx, operation, session.client,
		service.OperationJobArgs{OperationID: operationID}, nil); err != nil {
		t.Fatalf("enqueue publication recovery delivery: %v", err)
	}
	awaitRiverCompletion(t, ctx, events, *operation.RiverJobID)
	assertOperationState(t, ctx, repository, operationID, "succeeded")
	currentRoot, exists, err := registry.GetToolsDirectory(ctx)
	if err != nil || !exists || currentRoot != newRoot {
		t.Fatalf("root after publication-journal recovery = %q, %v, %v", currentRoot, exists, err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("publication recovery left staging: %v", err)
	}
}

type dispatchCatalog struct {
	mu           sync.Mutex
	release      tools.Release
	archive      []byte
	checksum     string
	resolveError error
	resolves     int
	resolveGate  *dispatchBarrier
	downloadGate *dispatchBarrier
	checksumGate *dispatchBarrier
}

func (catalog *dispatchCatalog) Resolve(ctx context.Context, _ tools.PackageKind, _ tools.Platform, _ string) (tools.Release, error) {
	catalog.mu.Lock()
	catalog.resolves++
	release, err, gate := catalog.release, catalog.resolveError, catalog.resolveGate
	catalog.mu.Unlock()
	if gate != nil {
		if err := gate.pause(ctx); err != nil {
			return release, err
		}
	}
	return release, err
}

func (catalog *dispatchCatalog) Download(ctx context.Context, _ tools.PackageKind, _ tools.Platform,
	_, name string, destination io.Writer, progress func(int64)) (tools.Artifact, int64, error) {
	catalog.mu.Lock()
	archive, gate := append([]byte(nil), catalog.archive...), catalog.downloadGate
	catalog.mu.Unlock()
	written, err := destination.Write(archive)
	if err != nil {
		return tools.Artifact{}, int64(written), err
	}
	if progress != nil {
		progress(int64(written))
	}
	if gate != nil {
		if err := gate.pause(ctx); err != nil {
			return tools.Artifact{Name: name}, int64(written), err
		}
	}
	return tools.Artifact{Name: name}, int64(written), nil
}

func (catalog *dispatchCatalog) Checksum(ctx context.Context, _ tools.PackageKind, _ tools.Platform, _, _ string) (string, error) {
	catalog.mu.Lock()
	checksum, gate := catalog.checksum, catalog.checksumGate
	catalog.mu.Unlock()
	if gate != nil {
		if err := gate.pause(ctx); err != nil {
			return "", err
		}
	}
	return checksum, nil
}

func (catalog *dispatchCatalog) setResolveError(err error) {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	catalog.resolveError = err
}

func (catalog *dispatchCatalog) clearBarriers() {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	catalog.resolveGate, catalog.downloadGate, catalog.checksumGate = nil, nil, nil
}

func (catalog *dispatchCatalog) setReleaseIdentity(identity string) {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	catalog.release.Identity = identity
}

func (catalog *dispatchCatalog) resolveCount() int {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	return catalog.resolves
}

type dispatchRunner struct {
	mu      sync.Mutex
	gate    *dispatchBarrier
	pauseAt int
	runs    int
}

func (runner *dispatchRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	if len(args) != 1 || args[0] != "-version" {
		return nil, fmt.Errorf("unexpected command arguments")
	}
	runner.mu.Lock()
	runner.runs++
	run, gate, pauseAt := runner.runs, runner.gate, runner.pauseAt
	runner.mu.Unlock()
	if gate != nil && (pauseAt == 0 || run == pauseAt) {
		if err := gate.pause(ctx); err != nil {
			return nil, err
		}
	}
	return []byte(filepath.Base(executable) + " version " + filepath.Base(filepath.Dir(executable))), nil
}

type dispatchBarrier struct {
	entered chan struct{}
	once    sync.Once
}

func newDispatchBarrier() *dispatchBarrier {
	return &dispatchBarrier{entered: make(chan struct{})}
}

func (barrier *dispatchBarrier) pause(ctx context.Context) error {
	barrier.once.Do(func() { close(barrier.entered) })
	<-ctx.Done()
	return ctx.Err()
}

type riverBoundaryRepository struct {
	*persistence.SetupManagerRepository
	stage        string
	stageGate    *dispatchBarrier
	readyGate    *dispatchBarrier
	successGate  *dispatchBarrier
	moveCommit   *dispatchBarrier
	moveFinished *dispatchBarrier
}

func (repository *riverBoundaryRepository) TransitionOperation(ctx context.Context, id uuid.UUID, transition func(*persistence.Operation) error) error {
	var gate *dispatchBarrier
	err := repository.SetupManagerRepository.TransitionOperation(ctx, id, func(operation *persistence.Operation) error {
		if err := transition(operation); err != nil {
			return err
		}
		switch {
		case repository.stageGate != nil && operation.Stage == repository.stage:
			gate = repository.stageGate
		case repository.successGate != nil && operation.State == "succeeded":
			gate = repository.successGate
		}
		return nil
	})
	if err != nil {
		return err
	}
	if gate != nil {
		return gate.pause(ctx)
	}
	return nil
}

func (repository *riverBoundaryRepository) MarkInstallationReady(ctx context.Context, id uuid.UUID, versions json.RawMessage, verifiedAt time.Time) error {
	if err := repository.SetupManagerRepository.MarkInstallationReady(ctx, id, versions, verifiedAt); err != nil {
		return err
	}
	if repository.readyGate != nil {
		return repository.readyGate.pause(ctx)
	}
	return nil
}

func (repository *riverBoundaryRepository) CommitToolsRootMove(ctx context.Context, id uuid.UUID, oldRoot, newRoot string) error {
	if err := repository.SetupManagerRepository.CommitToolsRootMove(ctx, id, oldRoot, newRoot); err != nil {
		return err
	}
	if repository.moveCommit != nil {
		return repository.moveCommit.pause(ctx)
	}
	return nil
}

func (repository *riverBoundaryRepository) FinishToolsRootMove(ctx context.Context, id uuid.UUID) error {
	if err := repository.SetupManagerRepository.FinishToolsRootMove(ctx, id); err != nil {
		return err
	}
	if repository.moveFinished != nil {
		return repository.moveFinished.pause(ctx)
	}
	return nil
}

func (repository *riverBoundaryRepository) clearBarriers() {
	repository.stage, repository.stageGate = "", nil
	repository.readyGate, repository.successGate = nil, nil
	repository.moveCommit, repository.moveFinished = nil, nil
}

type dispatchRiverSession struct {
	client       *river.Client[*sql.Tx]
	listenerPool *pgxpool.Pool
	stopped      bool
}

func newDispatchRiverSession(t *testing.T, ctx context.Context, databaseURL string, database *bun.DB, worker *InstallationWorker) *dispatchRiverSession {
	t.Helper()
	client, listenerPool := startDispatchRiver(t, databaseURL, database, worker)
	session := &dispatchRiverSession{client: client, listenerPool: listenerPool}
	t.Cleanup(func() { session.close(t) })
	return session
}

func (session *dispatchRiverSession) stopAndCancel(t *testing.T, ctx context.Context) {
	t.Helper()
	if session.stopped {
		return
	}
	err := session.client.StopAndCancel(ctx)
	session.stopped = true
	session.listenerPool.Close()
	if err != nil {
		t.Fatalf("cancel active River delivery: %v", err)
	}
}

func (session *dispatchRiverSession) close(t *testing.T) {
	t.Helper()
	if session.stopped {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := session.client.StopAndCancel(ctx); err != nil {
		t.Errorf("stop River client: %v", err)
	}
	session.stopped = true
	session.listenerPool.Close()
}

func awaitDispatchBarrier(t *testing.T, ctx context.Context, barrier *dispatchBarrier, events <-chan *river.Event,
	jobID int64, repository *persistence.SetupManagerRepository, operationID uuid.UUID) {
	t.Helper()
	for {
		select {
		case <-barrier.entered:
			return
		case event := <-events:
			if event == nil || event.Job == nil || event.Job.ID != jobID {
				continue
			}
			operation, err := repository.GetOperation(ctx, operationID)
			state, stage := operationStateStage(operation)
			t.Fatalf("River job completed before interruption barrier: operation=%s/%s lookup error=%v", state, stage, err)
		case <-ctx.Done():
			diagnosticCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			operation, err := repository.GetOperation(diagnosticCtx, operationID)
			state, stage := operationStateStage(operation)
			t.Fatalf("interruption barrier was not reached: operation=%s/%s lookup error=%v deadline=%v",
				state, stage, err, ctx.Err())
		}
	}
}

func newDispatchCatalog(t *testing.T, release string) *dispatchCatalog {
	t.Helper()
	archive := dispatchZipWithExecutable(t, "fpcalc", []byte("test executable "+release))
	checksum := sha256String(archive)
	return &dispatchCatalog{
		release: tools.Release{
			Identity: release,
			Artifacts: []tools.Artifact{{
				Name: "fpcalc.zip", ChecksumSHA256: checksum,
			}},
		},
		archive: archive, checksum: checksum,
	}
}

func openDispatchDatabase(t *testing.T) (*bun.DB, string) {
	t.Helper()
	startup, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	container, err := postgres.Run(startup, "postgres:17",
		postgres.WithDatabase("melotrove_jobs_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(45*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL Testcontainer: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("terminate PostgreSQL Testcontainer: %v", err)
		}
	})
	databaseURL, err := container.ConnectionString(startup, "sslmode=disable")
	if err != nil {
		t.Fatalf("get PostgreSQL Testcontainer connection string: %v", err)
	}
	sqlDatabase, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL Testcontainer database: %v", err)
	}
	database := bun.NewDB(sqlDatabase, pgdialect.New())
	if err := database.PingContext(startup); err != nil {
		_ = database.Close()
		t.Fatalf("connect to PostgreSQL Testcontainer: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database, databaseURL
}

func startDispatchRiver(t *testing.T, databaseURL string, database *bun.DB,
	worker *InstallationWorker) (*river.Client[*sql.Tx], *pgxpool.Pool) {
	t.Helper()
	client, listenerPool, err := StartWithWorkers(context.Background(), databaseURL, database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, worker)
	})
	if err != nil {
		t.Fatalf("start River worker client with production dispatcher: %v", err)
	}
	return client, listenerPool
}

func stopRiverClient(t *testing.T, client *river.Client[*sql.Tx]) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Stop(ctx); err != nil {
		t.Errorf("stop River worker client: %v", err)
	}
}

func enqueueInstall(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository,
	client *river.Client[*sql.Tx], toolsRoot, release string) *persistence.Operation {
	t.Helper()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: release, RelativePath: filepath.Join("fpcalc", release),
		State: "preparing",
	}
	snapshot, err := json.Marshal(service.InstallInputSnapshot{
		TargetIdentity:     "fpcalc:chromaprint:" + release + ":linux:amd64",
		SchemaVersion:      2,
		ToolsRoot:          toolsRoot,
		PackageKind:        tools.PackageFPCalc,
		SourceName:         "chromaprint",
		ReleaseIdentity:    release,
		ArtifactIdentities: []service.InstallArtifactIdentity{{Name: "fpcalc.zip", ChecksumAvailable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactIdentities, err := json.Marshal([]service.InstallArtifactIdentity{{Name: "fpcalc.zip", ChecksumAvailable: true}})
	if err != nil {
		t.Fatal(err)
	}
	installation.ArtifactIdentities = artifactIdentities
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "install", State: "queued", Stage: "queued",
		InputSnapshot: snapshot, TargetInstallationID: &installation.ID,
	}
	if err := repository.CreateInstallationOperationAndEnqueue(ctx, toolsRoot, installation, operation, client,
		service.OperationJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("atomically enqueue installation: %v", err)
	}
	return operation
}

func enqueueDuplicateDelivery(t *testing.T, ctx context.Context, database *bun.DB,
	client *river.Client[*sql.Tx], operationID uuid.UUID) int64 {
	t.Helper()
	tx, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin duplicate River delivery transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	inserted, err := client.InsertTx(ctx, tx,
		service.OperationJobArgs{OperationID: operationID}, nil)
	if err != nil {
		t.Fatalf("insert duplicate River delivery: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit duplicate River delivery: %v", err)
	}
	return inserted.Job.ID
}

func awaitRiverCompletion(t *testing.T, ctx context.Context, events <-chan *river.Event, id int64) {
	t.Helper()
	for {
		select {
		case event := <-events:
			if event != nil && event.Job != nil && event.Job.ID == id {
				return
			}
		case <-ctx.Done():
			t.Fatalf("River job %d did not complete before deadline: %v", id, ctx.Err())
			return
		}
	}
}

func setRuntimeRoots(t *testing.T, ctx context.Context, repository *persistence.SettingsRepository, root string) {
	t.Helper()
	if err := repository.Set(ctx, settings.ToolsDirectoryKey, root); err != nil {
		t.Fatalf("set tools directory: %v", err)
	}
	if err := repository.CompleteSetupOnce(ctx, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("mark setup complete: %v", err)
	}
}

func assertOperationState(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, id uuid.UUID, want string) {
	t.Helper()
	operation, err := repository.GetOperation(ctx, id)
	if err != nil || operation.State != want {
		state, _ := operationStateStage(operation)
		t.Fatalf("operation %s state = %q, lookup error = %v; want %s", id, state, err, want)
	}
}

func operationStateStage(operation *persistence.Operation) (string, string) {
	if operation == nil {
		return "", ""
	}
	return operation.State, operation.Stage
}

func sha256String(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func dispatchZipWithExecutable(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create(name)
	if err != nil {
		t.Fatalf("create test archive entry: %v", err)
	}
	if _, err := entry.Write(contents); err != nil {
		t.Fatalf("write test archive entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close test archive: %v", err)
	}
	return archive.Bytes()
}

var _ river.Worker[service.OperationJobArgs] = (*InstallationWorker)(nil)

func TestReconcileInterruptedMoveRetryPostgreSQL(t *testing.T) {
	database, databaseURL := openDispatchDatabase(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	repository := persistence.NewSetupManagerRepository(database)
	settingsRepository := persistence.NewSettingsRepository(database)
	oldRoot, err := settings.NormalizePath(filepath.Join(t.TempDir(), "old-tools"))
	if err != nil {
		t.Fatal(err)
	}
	newRoot, err := settings.NormalizePath(filepath.Join(t.TempDir(), "new-tools"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(oldRoot, "fpcalc", "1.6.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldRoot, "fpcalc", "1.6.1", "fpcalc"), []byte("managed executable bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	setRuntimeRoots(t, ctx, settingsRepository, oldRoot)
	runtimeSettings := settings.New(settingsRepository, nil)
	platform := tools.Platform{GOOS: "linux", GOARCH: "amd64"}
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "fpcalc", PlatformGOOS: platform.GOOS, PlatformGOARCH: platform.GOARCH,
		SourceName: "chromaprint", ReleaseIdentity: "1.6.1", RelativePath: "fpcalc/1.6.1", State: "preparing",
	}
	if err := repository.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create managed installation: %v", err)
	}
	if err := repository.MarkInstallationReady(ctx, installation.ID, json.RawMessage(`{"fpcalc":"fpcalc version 1.6.1"}`), time.Now().UTC()); err != nil {
		t.Fatalf("mark managed installation ready: %v", err)
	}
	if err := settingsRepository.Set(ctx, settings.ActiveFPCalcInstallationKey, installation.ID.String()); err != nil {
		t.Fatalf("select active fpcalc installation: %v", err)
	}
	ffmpegInstallation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: platform.GOOS, PlatformGOARCH: platform.GOARCH,
		SourceName: "btbn", ReleaseIdentity: "8.0", RelativePath: "ffmpeg/8.0", State: "preparing",
	}
	if err := os.MkdirAll(filepath.Join(oldRoot, ffmpegInstallation.RelativePath), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, executable := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(oldRoot, ffmpegInstallation.RelativePath, executable), []byte(executable+" version 8.0"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.CreateInstallation(ctx, ffmpegInstallation); err != nil {
		t.Fatalf("create active FFmpeg installation: %v", err)
	}
	if err := repository.MarkInstallationReady(ctx, ffmpegInstallation.ID,
		json.RawMessage(`{"ffmpeg":"ffmpeg version 8.0","ffprobe":"ffprobe version 8.0"}`), time.Now().UTC()); err != nil {
		t.Fatalf("mark active FFmpeg installation ready: %v", err)
	}
	if err := settingsRepository.Set(ctx, settings.ActiveFFmpegInstallationKey, ffmpegInstallation.ID.String()); err != nil {
		t.Fatalf("select active FFmpeg installation: %v", err)
	}
	activeFFmpegBefore, hasActiveFFmpegBefore, err := settingsRepository.Get(ctx, settings.ActiveFFmpegInstallationKey)
	if err != nil {
		t.Fatalf("read active FFmpeg ID before reconciliation: %q %v %v", activeFFmpegBefore, hasActiveFFmpegBefore, err)
	}
	activeFPCalcBefore, hasActiveFPCalcBefore, err := settingsRepository.Get(ctx, settings.ActiveFPCalcInstallationKey)
	if err != nil || !hasActiveFPCalcBefore {
		t.Fatalf("read active fpcalc ID before reconciliation: %q %v %v", activeFPCalcBefore, hasActiveFPCalcBefore, err)
	}

	moveService := service.NewMoveTools(repository, runtimeSettings, platform, nil)
	preflight, err := moveService.Preflight(ctx, newRoot, false)
	if err != nil {
		t.Fatalf("preflight tools-root move: %v", err)
	}
	operationID := uuid.New()
	snapshot, err := json.Marshal(preflight.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	staleRiverJobID := int64(987654321)
	operation := &persistence.Operation{
		ID: operationID, Kind: "move_tools_root", State: "running", Stage: "copy", InputSnapshot: snapshot,
		RiverJobID: &staleRiverJobID,
	}
	if err := repository.CreateOperation(ctx, operation); err != nil {
		t.Fatalf("create interrupted move operation: %v", err)
	}
	staging := filepath.Join(newRoot, ".staging", operationID.String())
	partialPayload := filepath.Join(staging, "payload", "fpcalc", "1.6.1", "fpcalc")
	if err := os.MkdirAll(filepath.Dir(partialPayload), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partialPayload, []byte("partial copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileInterruptedOperations(ctx, repository, service.NewOperations(repository),
		func(_ context.Context, id *int64) (bool, error) {
			if id == nil || *id != staleRiverJobID {
				t.Fatalf("checked River job id %v; want %d", id, staleRiverJobID)
			}
			return false, nil
		}, runtimeSettings); err != nil {
		t.Fatalf("reconcile interrupted move: %v", err)
	}
	recovered, err := repository.GetOperation(ctx, operationID)
	if err != nil || recovered.State != "failed" || recovered.SafeError == nil || !strings.Contains(*recovered.SafeError, "Retry") {
		t.Fatalf("recovered move = %#v, %v; want failed with safe retry guidance", recovered, err)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("interrupted move staging remains: %v", err)
	}
	currentRoot, rootExists, err := runtimeSettings.GetToolsDirectory(ctx)
	if err != nil || !rootExists || currentRoot != oldRoot {
		t.Fatalf("tools root after reconciliation = %q, %v, %v; want unchanged %q", currentRoot, rootExists, err, oldRoot)
	}

	workerOperations := service.NewOperations(repository)
	worker := NewInstallationWorker(repository, workerOperations, &dispatchCatalog{}, runtimeSettings, platform, tools.NewLifecycle(&dispatchRunner{}))
	worker.SetMoveWorker(NewMoveWorker(repository, workerOperations, runtimeSettings, platform, tools.NewLifecycle(&dispatchRunner{})))
	riverSession := newDispatchRiverSession(t, ctx, databaseURL, database, worker)
	riverClient := riverSession.client
	events, cancelEvents := riverClient.Subscribe(river.EventKindJobCompleted)
	defer cancelEvents()
	operations := service.NewOperationsWithRiver(repository, riverClient)
	retried, err := operations.Retry(ctx, operationID)
	if err != nil {
		t.Fatalf("retry recovered move: %v", err)
	}
	if retried.TargetInstallationID != nil || retried.Attempt != 2 {
		t.Fatalf("move retry changed target or attempt: %#v", retried)
	}
	awaitRiverCompletion(t, ctx, events, *retried.RiverJobID)
	completed, err := repository.GetOperation(ctx, operationID)
	if err != nil || completed.State != "succeeded" {
		t.Fatalf("retried move = %#v, %v; want succeeded", completed, err)
	}
	installations, err := repository.ListInstallations(ctx, "fpcalc", platform.GOOS, platform.GOARCH)
	if err != nil || len(installations) != 1 || installations[0].ID != installation.ID {
		t.Fatalf("installations after move retry = %#v, %v; want exactly the original installation", installations, err)
	}
	ffmpegInstallations, err := repository.ListInstallations(ctx, "ffmpeg", platform.GOOS, platform.GOARCH)
	if err != nil || len(ffmpegInstallations) != 1 || ffmpegInstallations[0].ID != ffmpegInstallation.ID {
		t.Fatalf("FFmpeg installations after move retry = %#v, %v; want exactly the original installation", ffmpegInstallations, err)
	}
	activeFFmpegAfter, hasActiveFFmpegAfter, err := settingsRepository.Get(ctx, settings.ActiveFFmpegInstallationKey)
	if err != nil || hasActiveFFmpegAfter != hasActiveFFmpegBefore || activeFFmpegAfter != activeFFmpegBefore {
		t.Fatalf("active FFmpeg ID changed from %q/%v to %q/%v: %v", activeFFmpegBefore, hasActiveFFmpegBefore, activeFFmpegAfter, hasActiveFFmpegAfter, err)
	}
	activeFPCalcAfter, hasActiveFPCalcAfter, err := settingsRepository.Get(ctx, settings.ActiveFPCalcInstallationKey)
	if err != nil || hasActiveFPCalcAfter != hasActiveFPCalcBefore || activeFPCalcAfter != activeFPCalcBefore {
		t.Fatalf("active fpcalc ID changed from %q/%v to %q/%v: %v", activeFPCalcBefore, hasActiveFPCalcBefore, activeFPCalcAfter, hasActiveFPCalcAfter, err)
	}
	currentRoot, rootExists, err = runtimeSettings.GetToolsDirectory(ctx)
	if err != nil || !rootExists || currentRoot != newRoot {
		t.Fatalf("tools root after successful retry = %q, %v, %v; want %q", currentRoot, rootExists, err, newRoot)
	}
	riverSession.close(t)

	postSwitchRoot, err := settings.NormalizePath(filepath.Join(t.TempDir(), "post-switch-tools"))
	if err != nil {
		t.Fatal(err)
	}
	moveCommitBarrier := newDispatchBarrier()
	boundaryRepository := &riverBoundaryRepository{SetupManagerRepository: repository, moveCommit: moveCommitBarrier}
	boundaryOperations := service.NewOperations(boundaryRepository)
	boundaryWorker := NewInstallationWorker(boundaryRepository, boundaryOperations, &dispatchCatalog{}, runtimeSettings,
		platform, tools.NewLifecycle(&dispatchRunner{}))
	boundaryWorker.SetMoveWorker(NewMoveWorker(boundaryRepository, boundaryOperations, runtimeSettings, platform, tools.NewLifecycle(&dispatchRunner{})))
	boundarySession := newDispatchRiverSession(t, ctx, databaseURL, database, boundaryWorker)
	boundaryEvents, cancelBoundaryEvents := boundarySession.client.Subscribe(river.EventKindJobCompleted)
	defer cancelBoundaryEvents()
	postSwitchService := service.NewMoveTools(repository, runtimeSettings, platform, boundarySession.client)
	postSwitchPreflight, err := postSwitchService.Preflight(ctx, postSwitchRoot, true)
	if err != nil {
		t.Fatalf("preflight post-switch rollback fixture: %v", err)
	}
	postSwitchMove, err := postSwitchService.Start(ctx, postSwitchPreflight, nil)
	if err != nil {
		t.Fatalf("start post-switch rollback fixture: %v", err)
	}
	awaitDispatchBarrier(t, ctx, moveCommitBarrier, boundaryEvents, *postSwitchMove.RiverJobID, repository, postSwitchMove.ID)
	postSwitchSnapshot, err := repository.GetOperation(ctx, postSwitchMove.ID)
	if err != nil || postSwitchSnapshot.State != "running" || postSwitchSnapshot.Stage != "switched" {
		t.Fatalf("operation at post-switch barrier = %#v, %v; want running/switched", postSwitchSnapshot, err)
	}
	postSwitchStaging := filepath.Join(postSwitchRoot, ".staging", postSwitchMove.ID.String())
	if _, err := os.Stat(filepath.Join(postSwitchStaging, "publication.json")); err != nil {
		t.Fatalf("post-switch publication evidence missing: %v", err)
	}
	boundarySession.stopAndCancel(t, ctx)
	postSwitchRootValue, rootExists, err := runtimeSettings.GetToolsDirectory(ctx)
	if err != nil || !rootExists || postSwitchRootValue != postSwitchRoot {
		t.Fatalf("persisted tools root at post-switch barrier = %q, %v, %v; want %q", postSwitchRootValue, rootExists, err, postSwitchRoot)
	}

	if err := ReconcileInterruptedOperations(ctx, repository, service.NewOperations(repository),
		func(context.Context, *int64) (bool, error) { return false, nil }, runtimeSettings); err != nil {
		t.Fatalf("reconcile post-switch move: %v", err)
	}
	reconciledMove, err := repository.GetOperation(ctx, postSwitchMove.ID)
	if err != nil || reconciledMove.State != "failed" || reconciledMove.Stage != "switched" ||
		reconciledMove.SafeError == nil || !strings.Contains(*reconciledMove.SafeError, "Retry") {
		t.Fatalf("post-switch reconciliation = %#v, %v; want retryable failed/switched", reconciledMove, err)
	}
	if _, err := os.Stat(filepath.Join(postSwitchStaging, "publication.json")); err != nil {
		t.Fatalf("reconciliation discarded post-switch recovery evidence: %v", err)
	}

	rollbackWorkerOperations := service.NewOperations(repository)
	rollbackWorker := NewInstallationWorker(repository, rollbackWorkerOperations, &dispatchCatalog{}, runtimeSettings,
		platform, tools.NewLifecycle(&dispatchRunner{}))
	rollbackWorker.SetMoveWorker(NewMoveWorker(repository, rollbackWorkerOperations, runtimeSettings, platform, tools.NewLifecycle(&dispatchRunner{})))
	rollbackSession := newDispatchRiverSession(t, ctx, databaseURL, database, rollbackWorker)
	rollbackClient := rollbackSession.client
	rollbackEvents, cancelRollbackEvents := rollbackClient.Subscribe(river.EventKindJobCompleted)
	defer cancelRollbackEvents()
	rollbackOperations := service.NewOperationsWithRiver(repository, rollbackClient)
	rollbackRetry, err := rollbackOperations.Retry(ctx, postSwitchMove.ID)
	if err != nil {
		t.Fatalf("retry post-switch move: %v", err)
	}
	if rollbackRetry.State != "queued" || rollbackRetry.Stage != "retry:switched" {
		t.Fatalf("post-switch retry stage/state = %s/%s; want queued/retry:switched", rollbackRetry.State, rollbackRetry.Stage)
	}
	awaitRiverCompletion(t, ctx, rollbackEvents, *rollbackRetry.RiverJobID)
	rolledBack, err := repository.GetOperation(ctx, postSwitchMove.ID)
	if err != nil || rolledBack.State != "failed" || rolledBack.SafeError == nil ||
		*rolledBack.SafeError != "The tools directory move failed. The current tools directory is unchanged." {
		t.Fatalf("post-switch rollback result = %#v, %v; want failed with truthful safe error", rolledBack, err)
	}
	currentRoot, rootExists, err = runtimeSettings.GetToolsDirectory(ctx)
	if err != nil || !rootExists || currentRoot != newRoot {
		t.Fatalf("persisted tools root after rollback = %q, %v, %v; want restored old root %q", currentRoot, rootExists, err, newRoot)
	}
	if _, err := os.Lstat(postSwitchStaging); !os.IsNotExist(err) {
		t.Fatalf("post-switch staging remains after rollback: %v", err)
	}
	for _, file := range postSwitchPreflight.Snapshot.Files {
		if digest, err := tools.SHA256File(file.SourcePath); err != nil || digest != file.SHA256 {
			t.Errorf("previous-root executable not restored: %s hash=%s err=%v", file.SourcePath, digest, err)
		}
		if _, err := os.Lstat(file.TargetPath); !os.IsNotExist(err) {
			t.Errorf("new-root executable remains after rollback: %s error=%v", file.TargetPath, err)
		}
	}
	for _, test := range []struct {
		kind string
		id   uuid.UUID
	}{
		{kind: "fpcalc", id: installation.ID},
		{kind: "ffmpeg", id: ffmpegInstallation.ID},
	} {
		remaining, err := repository.ListInstallations(ctx, test.kind, platform.GOOS, platform.GOARCH)
		if err != nil || len(remaining) != 1 || remaining[0].ID != test.id {
			t.Fatalf("%s installations after rollback = %#v, %v; want original only", test.kind, remaining, err)
		}
	}
	activeFFmpegAfterRollback, hasActiveFFmpegAfterRollback, err := settingsRepository.Get(ctx, settings.ActiveFFmpegInstallationKey)
	if err != nil || !hasActiveFFmpegAfterRollback || activeFFmpegAfterRollback != activeFFmpegBefore {
		t.Fatalf("active FFmpeg ID after rollback = %q/%v, %v; want unchanged %q", activeFFmpegAfterRollback, hasActiveFFmpegAfterRollback, err, activeFFmpegBefore)
	}
	activeFPCalcAfterRollback, hasActiveFPCalcAfterRollback, err := settingsRepository.Get(ctx, settings.ActiveFPCalcInstallationKey)
	if err != nil || !hasActiveFPCalcAfterRollback || activeFPCalcAfterRollback != activeFPCalcBefore {
		t.Fatalf("active fpcalc ID after rollback = %q/%v, %v; want unchanged %q", activeFPCalcAfterRollback, hasActiveFPCalcAfterRollback, err, activeFPCalcBefore)
	}
	rollbackSession.close(t)

	partialRoot, err := settings.NormalizePath(filepath.Join(t.TempDir(), "partial-publication-tools"))
	if err != nil {
		t.Fatal(err)
	}
	conflictTarget := filepath.Join(partialRoot, installation.RelativePath, "fpcalc")
	if err := os.MkdirAll(filepath.Dir(conflictTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	operatorContents := []byte("operator-owned previous executable")
	if err := os.WriteFile(conflictTarget, operatorContents, 0o755); err != nil {
		t.Fatal(err)
	}
	partialMoveService := service.NewMoveTools(repository, runtimeSettings, platform, nil)
	partialPreflight, err := partialMoveService.Preflight(ctx, partialRoot, false)
	if err != nil {
		t.Fatalf("preflight confirmed-conflict move: %v", err)
	}
	if len(partialPreflight.Conflicts) != 1 || partialPreflight.Conflicts[0] != conflictTarget {
		t.Fatalf("confirmed-conflict preflight = %v; want %q", partialPreflight.Conflicts, conflictTarget)
	}
	partialPreflight.Snapshot.ConfirmedConflicts = append([]string(nil), partialPreflight.Conflicts...)
	partialOperationID := uuid.New()
	partialSnapshot, err := json.Marshal(partialPreflight.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	partialOperation := &persistence.Operation{
		ID: partialOperationID, Kind: "move_tools_root", State: "running", Stage: "commit_targets", InputSnapshot: partialSnapshot,
	}
	if err := repository.CreateOperation(ctx, partialOperation); err != nil {
		t.Fatalf("create partially published move operation: %v", err)
	}
	partialStaging, err := tools.EnsureOperationStaging(partialRoot, partialOperationID)
	if err != nil {
		t.Fatal(err)
	}
	partialPublication, err := newMovePublication(partialStaging, partialOperationID, partialPreflight.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	conflictIndex := -1
	for index, file := range partialPreflight.Snapshot.Files {
		payload, err := os.ReadFile(file.SourcePath)
		if err != nil {
			t.Fatal(err)
		}
		candidate := filepath.Join(partialStaging, "payload", file.RelativePath, file.Executable)
		if err := os.MkdirAll(filepath.Dir(candidate), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(candidate, payload, 0o755); err != nil {
			t.Fatal(err)
		}
		if filepath.Clean(file.TargetPath) == filepath.Clean(conflictTarget) {
			conflictIndex = index
		}
	}
	if conflictIndex < 0 {
		t.Fatal("confirmed conflict is absent from move snapshot")
	}
	if partialPublication.Files[conflictIndex].Owned {
		t.Fatal("partial publication fixture already records the target as owned")
	}
	backup := filepath.Join(partialStaging, "target-backups", fmt.Sprintf("%d", conflictIndex))
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(conflictTarget, backup); err != nil {
		t.Fatal(err)
	}
	conflictFile := partialPreflight.Snapshot.Files[conflictIndex]
	candidate := filepath.Join(partialStaging, "payload", conflictFile.RelativePath, conflictFile.Executable)
	if err := os.Link(candidate, conflictTarget); err != nil {
		t.Fatal(err)
	}
	backupContents, err := os.ReadFile(backup)
	if err != nil || !bytes.Equal(backupContents, operatorContents) {
		t.Fatalf("pre-reconciliation conflict backup = %q, %v", backupContents, err)
	}
	if err := ReconcileInterruptedOperations(ctx, repository, service.NewOperations(repository),
		func(context.Context, *int64) (bool, error) { return false, nil }, runtimeSettings); err != nil {
		t.Fatalf("reconcile partially published move: %v", err)
	}
	partialFailed, err := repository.GetOperation(ctx, partialOperationID)
	if err != nil || partialFailed.State != "failed" || partialFailed.Stage != "commit_targets" {
		t.Fatalf("partially published operation = %#v, %v; want retryable failed/commit_targets", partialFailed, err)
	}
	if afterReconcileBackup, err := os.ReadFile(backup); err != nil || !bytes.Equal(afterReconcileBackup, operatorContents) {
		t.Fatalf("reconciliation discarded operator backup: %q, %v", afterReconcileBackup, err)
	}

	partialWorkerOperations := service.NewOperations(repository)
	partialWorker := NewInstallationWorker(repository, partialWorkerOperations, &dispatchCatalog{}, runtimeSettings,
		platform, tools.NewLifecycle(&dispatchRunner{}))
	partialWorker.SetMoveWorker(NewMoveWorker(repository, partialWorkerOperations, runtimeSettings, platform, tools.NewLifecycle(&dispatchRunner{})))
	partialSession := newDispatchRiverSession(t, ctx, databaseURL, database, partialWorker)
	partialEvents, cancelPartialEvents := partialSession.client.Subscribe(river.EventKindJobCompleted)
	defer cancelPartialEvents()
	partialOperations := service.NewOperationsWithRiver(repository, partialSession.client)
	partialRetry, err := partialOperations.Retry(ctx, partialOperationID)
	if err != nil {
		t.Fatalf("retry partially published move: %v", err)
	}
	if partialRetry.Stage != "retry:commit_targets" {
		t.Fatalf("partial publication retry stage = %s; want retry:commit_targets", partialRetry.Stage)
	}
	awaitRiverCompletion(t, ctx, partialEvents, *partialRetry.RiverJobID)
	partialFinished, err := repository.GetOperation(ctx, partialOperationID)
	if err != nil || partialFinished.State != "failed" || partialFinished.SafeError == nil ||
		*partialFinished.SafeError != "The tools directory move failed. The current tools directory is unchanged." {
		t.Fatalf("partial publication rollback = %#v, %v; want truthful failed state", partialFinished, err)
	}
	partialRootValue, rootExists, err := runtimeSettings.GetToolsDirectory(ctx)
	if err != nil || !rootExists || partialRootValue != newRoot {
		t.Fatalf("tools root after partial rollback = %q, %v, %v; want unchanged %q", partialRootValue, rootExists, err, newRoot)
	}
	if restored, err := os.ReadFile(conflictTarget); err != nil || !bytes.Equal(restored, operatorContents) {
		t.Fatalf("operator target was not restored after retry: %q, %v", restored, err)
	}
	if _, err := os.Lstat(partialStaging); !os.IsNotExist(err) {
		t.Fatalf("partial publication staging remains after retry: %v", err)
	}
	for _, test := range []struct {
		kind string
		id   uuid.UUID
	}{
		{kind: "fpcalc", id: installation.ID},
		{kind: "ffmpeg", id: ffmpegInstallation.ID},
	} {
		remaining, err := repository.ListInstallations(ctx, test.kind, platform.GOOS, platform.GOARCH)
		if err != nil || len(remaining) != 1 || remaining[0].ID != test.id {
			t.Fatalf("%s installations after partial rollback = %#v, %v; want original only", test.kind, remaining, err)
		}
	}
	activeFFmpegAfterPartial, hasActiveFFmpegAfterPartial, err := settingsRepository.Get(ctx, settings.ActiveFFmpegInstallationKey)
	if err != nil || !hasActiveFFmpegAfterPartial || activeFFmpegAfterPartial != activeFFmpegBefore {
		t.Fatalf("active FFmpeg ID after partial rollback = %q/%v, %v; want unchanged", activeFFmpegAfterPartial, hasActiveFFmpegAfterPartial, err)
	}
	activeFPCalcAfterPartial, hasActiveFPCalcAfterPartial, err := settingsRepository.Get(ctx, settings.ActiveFPCalcInstallationKey)
	if err != nil || !hasActiveFPCalcAfterPartial || activeFPCalcAfterPartial != activeFPCalcBefore {
		t.Fatalf("active fpcalc ID after partial rollback = %q/%v, %v; want unchanged", activeFPCalcAfterPartial, hasActiveFPCalcAfterPartial, err)
	}
}
