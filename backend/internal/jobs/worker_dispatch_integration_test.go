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
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestInstallationWorkerRiverDispatchPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigratedWithRiver(t)
	databaseURL := testpostgres.URL(t, database)
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

func TestMoveWorkerRiverRecoversPublishedTargetBeforeOwnershipJournalPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigratedWithRiver(t)
	databaseURL := testpostgres.URL(t, database)
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
		RelativePath: testInstallationRelativePath(platform.GOOS, "fpcalc", "1.6.1"), State: "preparing",
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
	base := filepath.Base(executable)
	for _, logicalName := range []string{"ffmpeg", "ffprobe", "fpcalc"} {
		if strings.Contains(base, logicalName) {
			return []byte(logicalName + " version " + filepath.Base(filepath.Dir(executable))), nil
		}
	}
	return nil, fmt.Errorf("unexpected executable %q", base)
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

func (repository *riverBoundaryRepository) TransitionOperationForDelivery(ctx context.Context, id uuid.UUID, attempt int, riverJobID int64, transition func(*persistence.Operation) error) (bool, error) {
	var gate *dispatchBarrier
	changed, err := repository.SetupManagerRepository.TransitionOperationForDelivery(ctx, id, attempt, riverJobID, func(operation *persistence.Operation) error {
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
	if err == nil && changed && gate != nil {
		err = gate.pause(ctx)
	}
	return changed, err
}

func (repository *riverBoundaryRepository) FinalizeInstallation(ctx context.Context, operationID uuid.UUID, attempt int, riverJobID int64, installationID uuid.UUID, packageKind, goos, goarch, activeSetting string, verification tools.VerificationResult, verifiedAt time.Time) (bool, error) {
	finalized, err := repository.SetupManagerRepository.FinalizeInstallation(ctx, operationID, attempt, riverJobID, installationID, packageKind, goos, goarch, activeSetting, verification, verifiedAt)
	if err != nil || !finalized {
		return finalized, err
	}
	if repository.readyGate != nil {
		err = repository.readyGate.pause(ctx)
	} else if repository.successGate != nil {
		err = repository.successGate.pause(ctx)
	}
	return finalized, err
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
	started      bool
	stopped      bool
}

func newDispatchRiverSession(t *testing.T, ctx context.Context, databaseURL string, database *bun.DB, worker river.Worker[service.OperationJobArgs]) *dispatchRiverSession {
	t.Helper()
	session := newPreparedDispatchRiverSession(t, ctx, databaseURL, database, worker)
	session.start(t, ctx)
	return session
}

func newPreparedDispatchRiverSession(t *testing.T, ctx context.Context, databaseURL string, database *bun.DB,
	worker river.Worker[service.OperationJobArgs]) *dispatchRiverSession {
	t.Helper()
	client, listenerPool, err := PrepareWithWorkers(ctx, databaseURL, database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, worker)
	})
	if err != nil {
		t.Fatalf("prepare River worker client with production dispatcher: %v", err)
	}
	session := &dispatchRiverSession{client: client, listenerPool: listenerPool}
	t.Cleanup(func() { session.close(t) })
	return session
}

func (session *dispatchRiverSession) start(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := session.client.Start(ctx); err != nil {
		t.Fatalf("start prepared River worker client: %v", err)
	}
	session.started = true
}

func (session *dispatchRiverSession) stopAndCancel(t *testing.T, ctx context.Context) {
	t.Helper()
	if session.stopped || !session.started {
		return
	}
	err := session.client.StopAndCancel(ctx)
	session.stopped = true
	session.started = false
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
	if session.started {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := session.client.StopAndCancel(ctx); err != nil {
			t.Errorf("stop River client: %v", err)
		}
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

func startDispatchRiver(t *testing.T, databaseURL string, database *bun.DB,
	worker river.Worker[service.OperationJobArgs]) (*river.Client[*sql.Tx], *pgxpool.Pool) {
	t.Helper()
	client, listenerPool, err := StartWithWorkers(context.Background(), databaseURL, database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, worker)
	})
	if err != nil {
		t.Fatalf("start River worker client with production dispatcher: %v", err)
	}
	return client, listenerPool
}

type postWorkBarrier struct {
	*InstallationWorker
	gate *dispatchBarrier
}

func (worker *postWorkBarrier) Work(ctx context.Context, job *river.Job[service.OperationJobArgs]) error {
	if err := worker.InstallationWorker.Work(ctx, job); err != nil {
		return err
	}
	return worker.gate.pause(ctx)
}

func stopRiverClient(t *testing.T, client *river.Client[*sql.Tx]) {
	t.Helper()
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := client.Stop(ctx)
	t.Logf("testhelper.phase river=stop duration=%s error=%v", time.Since(started), err)
	if err != nil {
		t.Errorf("stop River worker client: %v", err)
	}
}

func enqueueInstall(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository,
	client *river.Client[*sql.Tx], toolsRoot, release string) *persistence.Operation {
	t.Helper()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: release, RelativePath: testInstallationRelativePath("linux", "fpcalc", release),
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
	started := time.Now()
	for {
		select {
		case event := <-events:
			if event != nil && event.Job != nil && event.Job.ID == id {
				t.Logf("testhelper.phase river=await job_id=%d duration=%s", id, time.Since(started))
				return
			}
		case <-ctx.Done():
			t.Logf("testhelper.phase river=await job_id=%d duration=%s error=%v", id, time.Since(started), ctx.Err())
			t.Fatalf("River job %d did not complete before deadline: %v", id, ctx.Err())
			return
		}
	}
}

func setRuntimeRoots(t *testing.T, ctx context.Context, repository *persistence.SettingsRepository, root string) {
	t.Helper()
	normalized, err := settings.NormalizePath(root)
	if err != nil {
		t.Fatalf("normalize fixture tools directory: %v", err)
	}
	root = normalized
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
