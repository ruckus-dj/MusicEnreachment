package jobs

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type workerToolsDirectory struct {
	path string
	ok   bool
	err  error
}

func (directory workerToolsDirectory) GetToolsDirectory(context.Context) (string, bool, error) {
	return directory.path, directory.ok, directory.err
}

func TestSourceAnalysisWorkerPreparerConfigPinsOnlyRequestedTool(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	work := &persistence.SourceAnalysisWork{SizeBytes: 17}
	probeID, fpcalcID := uuid.New(), uuid.New()
	snapshot := persistence.SourceAnalysisOperationSnapshot{Tools: []persistence.SourceAnalysisToolSelection{
		{InstallationID: probeID, Executable: "ffprobe", RelativePath: "ffmpeg/8.0", Version: "8.0"},
		{InstallationID: fpcalcID, Executable: "fpcalc", RelativePath: "chromaprint/1.2.3", Version: "1.2.3", VersionBanner: "fpcalc version 1.2.3"},
	}}
	worker := &SourceAnalysisWorker{toolsDirectory: workerToolsDirectory{path: root, ok: true},
		platform: settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}}

	sha, err := worker.preparerConfig(ctx, snapshot, work, uuid.New(), 1, 10, persistence.SourceStepSHA256)
	if err != nil {
		t.Fatalf("SHA config: %v", err)
	}
	if sha.ProbeExecutable != "" || sha.FPCalcExecutable != "" || sha.Hold != nil {
		t.Fatalf("SHA config unexpectedly requires tools: %+v", sha)
	}

	probe, err := worker.preparerConfig(ctx, snapshot, work, uuid.New(), 1, 10, persistence.SourceStepProbe)
	if err != nil {
		t.Fatalf("probe config: %v", err)
	}
	if want := filepath.Join(root, "ffmpeg", "8.0", "ffprobe"); probe.ProbeExecutable != want {
		t.Fatalf("probe executable = %q, want %q", probe.ProbeExecutable, want)
	}
	if probe.FPCalcExecutable != "" || probe.FFProbeVersion != "8.0" {
		t.Fatalf("probe config selected unrelated tool or wrong version: %+v", probe)
	}

	fingerprint, err := worker.preparerConfig(ctx, snapshot, work, uuid.New(), 1, 10, persistence.SourceStepFingerprint)
	if err != nil {
		t.Fatalf("fingerprint config: %v", err)
	}
	if want := filepath.Join(root, "chromaprint", "1.2.3", "fpcalc"); fingerprint.FPCalcExecutable != want {
		t.Fatalf("fpcalc executable = %q, want %q", fingerprint.FPCalcExecutable, want)
	}
	if fingerprint.ProbeExecutable != "" || fingerprint.FPCalcVersion.Version != "1.2.3" {
		t.Fatalf("fingerprint config selected unrelated tool or wrong version: %+v", fingerprint)
	}
}

func TestSourceAnalysisWorkerPreparerConfigRejectsInvalidPinnedTool(t *testing.T) {
	ctx := context.Background()
	worker := &SourceAnalysisWorker{toolsDirectory: workerToolsDirectory{path: t.TempDir(), ok: true},
		platform: settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}}
	work := &persistence.SourceAnalysisWork{}
	operationID := uuid.New()
	cases := []struct {
		name    string
		tool    persistence.SourceAnalysisToolSelection
		wantErr bool
	}{
		{name: "path escapes managed root", tool: persistence.SourceAnalysisToolSelection{Executable: "ffprobe", RelativePath: "../../outside", Version: "8.0"}, wantErr: true},
		{name: "malformed fpcalc banner", tool: persistence.SourceAnalysisToolSelection{Executable: "fpcalc", RelativePath: "fpcalc/1.2.3", Version: "1.2.3", VersionBanner: "not a version"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := persistence.SourceStepProbe
			if tc.tool.Executable == "fpcalc" {
				step = persistence.SourceStepFingerprint
			}
			_, err := worker.preparerConfig(ctx, persistence.SourceAnalysisOperationSnapshot{Tools: []persistence.SourceAnalysisToolSelection{tc.tool}}, work, operationID, 1, 2, step)
			if (err != nil) != tc.wantErr {
				t.Fatalf("preparerConfig error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
	worker.toolsDirectory = workerToolsDirectory{}
	if _, err := worker.preparerConfig(ctx, persistence.SourceAnalysisOperationSnapshot{}, work, operationID, 1, 2, persistence.SourceStepProbe); err == nil {
		t.Fatal("missing managed tools directory accepted")
	}
}

type workerGroupRepository struct {
	analysisWorkerRepository
	mode         string
	claims       int
	shaApplies   int
	probeApplies int
	fpApplies    int
	failures     []persistence.SourceStepFailure
	applyMu      sync.Mutex
	probeApplied chan struct{}
}

func (repository *workerGroupRepository) ClaimSourceAnalysisStep(context.Context, persistence.SourceStepClaim) (int, error) {
	repository.claims++
	return repository.claims, nil
}

func (repository *workerGroupRepository) GetSourceAnalysisWorkExecution(_ context.Context, fence persistence.SourceAnalysisArtifactFence) (*persistence.SourceAnalysisWorkExecution, error) {
	return &persistence.SourceAnalysisWorkExecution{WorkID: fence.WorkID, ProcessingMode: repository.mode}, nil
}

func (repository *workerGroupRepository) ApplySourceSHA256(_ context.Context, apply persistence.SourceSHA256Apply) (*persistence.SourceMediaVariant, error) {
	repository.shaApplies++
	return &persistence.SourceMediaVariant{SourceSHA256: append([]byte(nil), apply.SHA256...)}, nil
}

func (repository *workerGroupRepository) ApplySourceProbe(context.Context, persistence.SourceProbeApply) (*persistence.SourceMediaVariant, error) {
	repository.applyMu.Lock()
	repository.probeApplies++
	repository.applyMu.Unlock()
	if repository.probeApplied != nil {
		close(repository.probeApplied)
	}
	return &persistence.SourceMediaVariant{}, nil
}

func (repository *workerGroupRepository) ApplySourceFingerprint(context.Context, persistence.SourceFingerprintApply) (*persistence.SourceFingerprintResult, error) {
	repository.fpApplies++
	return &persistence.SourceFingerprintResult{}, nil
}

func (repository *workerGroupRepository) FailSourceAnalysisStep(_ context.Context, failure persistence.SourceStepFailure) error {
	repository.failures = append(repository.failures, failure)
	return nil
}

func (repository *workerGroupRepository) CheckNormalizedSourceAnalysisToolHold(context.Context, uuid.UUID, uuid.UUID, int, int64, string) error {
	return nil
}

type workerInputRepository struct {
	workerGroupRepository
	work     *persistence.SourceAnalysisWork
	location *persistence.SourceLocation
	root     *persistence.SourceRoot
}

func (repository *workerInputRepository) GetNormalizedSourceAnalysisWork(context.Context, uuid.UUID) (*persistence.SourceAnalysisWork, *persistence.SourceLocation, error) {
	return repository.work, repository.location, nil
}

func (repository *workerInputRepository) GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error) {
	return repository.root, nil
}

type recordingWorkerInputPreparer struct {
	preparer *service.SourceAnalysisInputPreparer
	modes    []string
	lastErr  error
}

func (preparer *recordingWorkerInputPreparer) PrepareMode(ctx context.Context, fence persistence.SourceAnalysisArtifactFence, mode string) (*service.SourceAnalysisPreparedInput, error) {
	preparer.modes = append(preparer.modes, mode)
	input, err := preparer.preparer.PrepareMode(ctx, fence, mode)
	preparer.lastErr = err
	return input, err
}

func newWorkerGroup(t *testing.T, repo *workerInputRepository, runner service.SourceAnalysisPreparing) (*SourceAnalysisWorker, persistence.SourceAnalysisOperationSnapshot, *persistence.Operation, []persistence.SourceAnalysisExecution, *recordingWorkerInputPreparer) {
	t.Helper()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rootPath, "source.bin")
	contents := []byte("worker input bytes")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rootID, workID, locationID := uuid.New(), uuid.New(), uuid.New()
	repo.mode = "in_place"
	repo.work = &persistence.SourceAnalysisWork{ID: workID, LocationID: locationID, SourceRootID: rootID,
		ConfiguredPath: rootPath, InventoryPath: rootPath, RelativePath: filepath.Base(path), SizeBytes: info.Size(), Mtime: info.ModTime()}
	repo.location = &persistence.SourceLocation{ID: locationID, SourceRootID: rootID, RelativePath: filepath.Base(path), SizeBytes: info.Size(), Mtime: info.ModTime()}
	repo.root = &persistence.SourceRoot{ID: rootID, ConfiguredPath: rootPath, InventoryPath: &rootPath, Enabled: true}
	input := &recordingWorkerInputPreparer{preparer: service.NewSourceAnalysisInputPreparer(repo, nil, nil, nil, nil)}
	worker := &SourceAnalysisWorker{repository: repo, inputPreparer: input, preparer: runner,
		toolsDirectory: workerToolsDirectory{}, platform: settings.PlatformState{Platform: settings.Platform{GOOS: "darwin", GOARCH: "arm64"}}}
	operation := &persistence.Operation{ID: uuid.New(), Attempt: 1, RerunTarget: false}
	steps := []persistence.SourceAnalysisExecution{}
	for _, step := range []persistence.SourceStepName{persistence.SourceStepSHA256, persistence.SourceStepProbe, persistence.SourceStepFingerprint} {
		steps = append(steps, persistence.SourceAnalysisExecution{Work: *repo.work, Step: persistence.SourceAnalysisStep{Step: string(step)}})
	}
	cacheOnly := true
	return worker, persistence.SourceAnalysisOperationSnapshot{Mode: persistence.SourceAnalysisModeBatch, CacheOnlyReuse: &cacheOnly}, operation, steps, input
}

func TestSourceAnalysisWorkerWithInputPreparerSharesOneInputAndAppliesFastProbe(t *testing.T) {
	probeApplied := make(chan struct{})
	repo := &workerInputRepository{workerGroupRepository: workerGroupRepository{probeApplied: probeApplied}}
	releaseFingerprint := make(chan struct{})
	runner := workerGroupPreparingFunc(func(_ context.Context, request service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
		if request.Targets&service.SourceAnalysisTargetSHA256 != 0 {
			return preparedSHA()
		}
		if request.Targets&service.SourceAnalysisTargetProbe != 0 {
			return preparedProbe()
		}
		<-releaseFingerprint
		return preparedFingerprint()
	})
	worker, snapshot, operation, executions, input := newWorkerGroup(t, repo, runner)
	done := make(chan error, 1)
	doneReceived := false
	releaseFingerprintOnce := sync.Once{}
	release := func() { releaseFingerprintOnce.Do(func() { close(releaseFingerprint) }) }
	t.Cleanup(func() {
		release()
		if !doneReceived {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("worker did not exit after releasing fingerprint runner")
			}
		}
	})
	go func() {
		done <- worker.runWorkGroup(context.Background(), operation, snapshot, 9, executions, "staged")
	}()
	select {
	case <-probeApplied:
	case err := <-done:
		doneReceived = true
		release()
		t.Fatalf("worker returned before probe apply: %v (input preparation: %v)", err, input.lastErr)
	case <-time.After(2 * time.Second):
		release()
		select {
		case <-done:
			doneReceived = true
		case <-time.After(2 * time.Second):
			t.Fatal("worker did not exit after releasing fingerprint runner")
		}
		t.Fatal("probe was not applied while fingerprint was blocked")
	}
	if repo.shaApplies != 1 || repo.probeApplies != 1 || repo.fpApplies != 0 {
		t.Fatalf("applies before fingerprint release: sha=%d probe=%d fingerprint=%d", repo.shaApplies, repo.probeApplies, repo.fpApplies)
	}
	release()
	select {
	case err := <-done:
		doneReceived = true
		if err != nil {
			t.Fatalf("run work group: %v (input preparation: %v)", err, input.lastErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish after releasing fingerprint runner")
	}
	if len(input.modes) != 1 || input.modes[0] != "in_place" {
		t.Fatalf("input preparation modes = %v", input.modes)
	}
	if repo.fpApplies != 1 {
		t.Fatalf("fingerprint apply count = %d, want 1", repo.fpApplies)
	}
}

func TestSourceAnalysisWorkerWithInputPreparerOmitsSHAWhenNotSelected(t *testing.T) {
	repo := &workerInputRepository{}
	var requests []service.SourceAnalysisPrepareRequest
	var requestMu sync.Mutex
	runner := workerGroupPreparingFunc(func(_ context.Context, request service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
		requestMu.Lock()
		requests = append(requests, request)
		requestMu.Unlock()
		return preparedProbe()
	})
	worker, snapshot, operation, executions, input := newWorkerGroup(t, repo, runner)
	executions = executions[1:2]
	if err := worker.runWorkGroup(context.Background(), operation, snapshot, 9, executions, "staged"); err != nil {
		t.Fatalf("run work group: %v (input preparation: %v)", err, input.lastErr)
	}
	if repo.shaApplies != 0 || len(requests) != 1 || requests[0].Targets != service.SourceAnalysisTargetProbe || requests[0].ExistingSHA256 != nil {
		t.Fatalf("SHA unexpectedly requested/applied: sha=%d requests=%+v", repo.shaApplies, requests)
	}
	if len(input.modes) != 1 || input.modes[0] != "in_place" {
		t.Fatalf("input preparation modes = %v", input.modes)
	}
}

func TestSourceAnalysisWorkerRunsSHAWhenToolConfigurationIsUnavailable(t *testing.T) {
	repo := &workerInputRepository{}
	runner := workerGroupPreparingFunc(func(_ context.Context, request service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
		if request.Targets != service.SourceAnalysisTargetSHA256 || request.ExistingSHA256 != nil {
			t.Fatalf("unexpected preparation request despite unavailable tools: %+v", request)
		}
		return preparedSHA()
	})
	worker, snapshot, operation, executions, _ := newWorkerGroup(t, repo, runner)
	cacheOnly := false
	snapshot.CacheOnlyReuse = &cacheOnly
	executions = executions[:2]
	err := worker.runWorkGroup(context.Background(), operation, snapshot, 9, executions, "staged")
	var stepFailure *sourceAnalysisStepFailure
	if !errors.As(err, &stepFailure) {
		t.Fatalf("group error = %v, want tool step failure", err)
	}
	if repo.shaApplies != 1 || len(repo.failures) != 1 || repo.failures[0].Step != persistence.SourceStepProbe {
		t.Fatalf("SHA/tool outcomes: SHA applies=%d failures=%+v", repo.shaApplies, repo.failures)
	}
}

func TestSourceAnalysisWorkerFailsClosedWithoutInputPreparerForRegisteredExecution(t *testing.T) {
	repo := &workerInputRepository{}
	worker, _, operation, executions, _ := newWorkerGroup(t, repo, workerGroupPreparingFunc(func(context.Context, service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
		t.Fatal("analysis runner called without an input preparer")
		return service.SourceAnalysisPreparation{}
	}))
	worker.inputPreparer = nil
	err := worker.runWorkGroup(context.Background(), operation, persistence.SourceAnalysisOperationSnapshot{}, 9, executions[:1], "in_place")
	if err == nil || err.Error() != "source analysis input preparer is unavailable" {
		t.Fatalf("missing input preparer error = %v", err)
	}
}

func TestSourceAnalysisWorkerJoinsCanceledRunnerBeforeReturning(t *testing.T) {
	repo := &workerInputRepository{}
	started, release := make(chan struct{}), make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	runner := workerGroupPreparingFunc(func(ctx context.Context, request service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
		if request.Targets == service.SourceAnalysisTargetSHA256 {
			return preparedSHA()
		}
		startedOnce.Do(func() { close(started) })
		<-ctx.Done()
		<-release
		return service.SourceAnalysisPreparation{}
	})
	worker, snapshot, operation, executions, _ := newWorkerGroup(t, repo, runner)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	done := make(chan error, 1)
	doneReceived := false
	t.Cleanup(func() {
		unlock()
		cancel()
		if !doneReceived {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("worker did not exit after releasing canceled runner")
			}
		}
	})
	go func() { done <- worker.runWorkGroup(ctx, operation, snapshot, 9, executions, "ignored") }()
	select {
	case <-started:
	case err := <-done:
		doneReceived = true
		cancel()
		unlock()
		t.Fatalf("worker returned before runner started: %v", err)
	case <-time.After(2 * time.Second):
		cancel()
		unlock()
		select {
		case err := <-done:
			doneReceived = true
			t.Fatalf("runner did not start; worker returned: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("runner did not start and worker did not exit")
		}
	}
	cancel()
	select {
	case err := <-done:
		doneReceived = true
		t.Fatalf("group returned before canceled runner exited: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-done:
		doneReceived = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled group error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not return after canceled runner was released")
	}
}

type workerGroupPreparingFunc func(context.Context, service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation

func (prepare workerGroupPreparingFunc) Prepare(ctx context.Context, request service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
	return prepare(ctx, request)
}

func preparedSHA() service.SourceAnalysisPreparation {
	return service.SourceAnalysisPreparation{SHA256: service.SourceAnalysisSHA256Outcome{SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisSucceeded}, Digest: sha256.Sum256([]byte("worker input bytes"))}}
}

func preparedProbe() service.SourceAnalysisPreparation {
	policy, version, inspected := persistence.SourceAnalysisPolicyVersion, "8.0", time.Now().UTC()
	return service.SourceAnalysisPreparation{Probe: service.SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisSucceeded}, Result: &persistence.SourceMediaVariant{AnalysisPolicyVersion: &policy, FFProbeVersion: &version, InspectedAt: &inspected}}}
}

func preparedFingerprint() service.SourceAnalysisPreparation {
	return service.SourceAnalysisPreparation{Fingerprint: service.SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisSucceeded}, Result: &persistence.SourceFingerprintResult{FPCalcVersion: "1.0", Fingerprint: "AQID"}}}
}
