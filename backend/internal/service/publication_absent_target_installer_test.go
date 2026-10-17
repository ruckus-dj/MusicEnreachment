package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestInspectRegularRecordsStableIdentity(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rootPath, "candidate")
	const content = "candidate payload"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := sourcefs.NewOutputOpener().OpenRoot(context.Background(), filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Errorf("close test root: %v", err)
		}
	}()
	file, err := root.(sourcefs.OutputMutation).OpenExistingRegular(context.Background(), filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close test file: %v", err)
		}
	}()
	identity, err := inspectRegular(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expectedSize := int64(len(content))
	expectedHash := sha256.Sum256([]byte(content))
	if identity.SizeBytes == nil || *identity.SizeBytes != expectedSize || identity.MTime == nil || *identity.MTime != info.ModTime().UnixMicro() || identity.SHA256 != hex.EncodeToString(expectedHash[:]) {
		t.Fatalf("unexpected candidate identity: %+v", identity)
	}
}

type absentInstallerRepositoryFake struct {
	begin                   persistence.PublicationExposureOutcome
	beginErr                error
	record                  persistence.PublicationCandidateInstalledOutcome
	recordErr               error
	beginCalls, recordCalls int
	installed               persistence.PublicationCandidateInstalledObservation
}

func (r *absentInstallerRepositoryFake) BeginPublicationExposure(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationExposureIntent, persistence.PublicationStageObservation) (persistence.PublicationExposureOutcome, error) {
	r.beginCalls++
	return r.begin, r.beginErr
}
func (r *absentInstallerRepositoryFake) RecordPublicationCandidateInstalled(_ context.Context, _ persistence.PublicationDeliveryGuard, _ persistence.PublicationExposureIntent, _ persistence.PublicationStageObservation, installed persistence.PublicationCandidateInstalledObservation) (persistence.PublicationCandidateInstalledOutcome, error) {
	r.recordCalls++
	r.installed = installed
	return r.record, r.recordErr
}

type absentInstallerStage struct {
	path       PublicationStagePaths
	closeCalls int
}

func (s *absentInstallerStage) Paths() PublicationStagePaths { return s.path }
func (*absentInstallerStage) Validate(context.Context) error { return nil }
func (s *absentInstallerStage) Close() error                 { s.closeCalls++; return nil }

const absentInstallerPayload = "verified stage bytes"

type absentInstallerFixture struct {
	rootPath  string
	stagePath string
	root      sourcefs.OutputDirectory
	stageDir  sourcefs.OutputDirectory
	repo      *absentInstallerRepositoryFake
	stage     *absentInstallerStage
	scope     *PublicationExecutionScope
	installer *absentTargetInstaller
	intent    persistence.PublicationExposureIntent
}

func newAbsentInstallerFixture(t *testing.T, begin persistence.PublicationExposureOutcome, commands PublicationCommandRunner) *absentInstallerFixture {
	t.Helper()
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(base, "output")
	stagePath := filepath.Join(rootPath, "stage")
	if err := os.MkdirAll(stagePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagePath, "candidate"), []byte(absentInstallerPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := sourcefs.NewOutputOpener()
	root, err := opener.OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close test root: %v", err)
		}
	})
	stageDir, err := root.OpenOrCreateDir(ctx, "stage")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stageDir.Close(); err != nil {
			t.Errorf("close test stage directory: %v", err)
		}
	})
	rootID, err := root.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetID, err := root.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(stagePath, "candidate"))
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256([]byte(absentInstallerPayload))
	validated := persistence.PublicationStageObservation{Version: 1, RelativePath: "stage/candidate", PayloadSHA256: hex.EncodeToString(digest[:]), Ownership: persistence.ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime}, ActualAudio: []byte(`{}`), ActualMetadata: []byte(`{}`)}
	intent := persistence.PublicationExposureIntent{StagedPath: "stage/candidate", TargetRelativePath: "installed.bin", Expectation: "absent", OutputRootParent: persistence.PublicationDirectoryIdentity{Platform: rootID.Platform, Volume: rootID.Volume, FileID: rootID.FileID}, TargetParent: persistence.PublicationDirectoryIdentity{Platform: targetID.Platform, Volume: targetID.Volume, FileID: targetID.FileID}}
	repo := &absentInstallerRepositoryFake{begin: begin, record: persistence.PublicationCandidateInstalledRecorded}
	guard := persistence.PublicationDeliveryGuard{}
	scope := &PublicationExecutionScope{guard: guard, commands: commands}
	stage := &absentInstallerStage{path: PublicationStagePaths{RelativePath: "stage/candidate"}}
	installer, err := newAbsentTargetInstaller(scope, repo, guard, intent, validated, stage, root, stageDir, root, "candidate", "installed.bin")
	if err != nil {
		t.Fatal(err)
	}
	return &absentInstallerFixture{rootPath: rootPath, stagePath: stagePath, root: root, stageDir: stageDir, repo: repo, stage: stage, scope: scope, installer: installer, intent: intent}
}

func (fixture *absentInstallerFixture) assertSourceAndTarget(t *testing.T, sourceExists, targetExists bool) {
	t.Helper()
	_, sourceErr := os.Stat(filepath.Join(fixture.stagePath, "candidate"))
	_, targetErr := os.Stat(filepath.Join(fixture.rootPath, "installed.bin"))
	if got := sourceErr == nil; got != sourceExists {
		t.Fatalf("source existence = %v, want %v (err %v)", got, sourceExists, sourceErr)
	}
	if sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
		t.Fatalf("stat source: %v", sourceErr)
	}
	if got := targetErr == nil; got != targetExists {
		t.Fatalf("target existence = %v, want %v (err %v)", got, targetExists, targetErr)
	}
	if targetErr != nil && !errors.Is(targetErr, os.ErrNotExist) {
		t.Fatalf("stat target: %v", targetErr)
	}
}

func TestAbsentTargetInstallerRenamesAndRecordsObservedCandidate(t *testing.T) {
	ctx := context.Background()
	fixture := newAbsentInstallerFixture(t, persistence.PublicationExposureRecorded, nil)
	if err := fixture.installer.Install(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.assertSourceAndTarget(t, false, true)
	got, err := os.ReadFile(filepath.Join(fixture.rootPath, "installed.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != absentInstallerPayload || fixture.repo.beginCalls != 1 || fixture.repo.recordCalls != 1 || fixture.repo.installed.TargetRelativePath != fixture.intent.TargetRelativePath || fixture.repo.installed.Identity.SHA256 != intentDigest(absentInstallerPayload) {
		t.Fatalf("unexpected install: bytes=%q repository=%+v", got, fixture.repo)
	}
}

func TestAbsentTargetInstallerRequiresNewExposureRecord(t *testing.T) {
	tests := []struct {
		name     string
		outcome  persistence.PublicationExposureOutcome
		beginErr error
	}{
		{name: "already recorded", outcome: persistence.PublicationExposureAlreadyRecorded},
		{name: "stale", outcome: persistence.PublicationExposureStale},
		{name: "recorded with error", outcome: persistence.PublicationExposureRecorded, beginErr: errors.New("begin failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAbsentInstallerFixture(t, test.outcome, nil)
			fixture.repo.beginErr = test.beginErr
			if err := fixture.installer.Install(context.Background()); err == nil {
				t.Fatal("Install succeeded without a clean newly-recorded outcome")
			}
			fixture.assertSourceAndTarget(t, true, false)
			if fixture.repo.beginCalls != 1 || fixture.repo.recordCalls != 0 || fixture.stage.closeCalls != 0 {
				t.Fatalf("unexpected pre-exposure calls: repo=%+v stage=%+v", fixture.repo, fixture.stage)
			}
		})
	}
}

func TestAbsentTargetInstallerRejectsCandidateHashBeforeBegin(t *testing.T) {
	fixture := newAbsentInstallerFixture(t, persistence.PublicationExposureRecorded, nil)
	fixture.installer.validated.PayloadSHA256 = intentDigest("different payload")
	if err := fixture.installer.Install(context.Background()); err == nil {
		t.Fatal("Install accepted candidate bytes that differ from validated hash")
	}
	fixture.assertSourceAndTarget(t, true, false)
	if fixture.repo.beginCalls != 0 || fixture.repo.recordCalls != 0 || fixture.stage.closeCalls != 0 {
		t.Fatalf("candidate mismatch reached exposure: repo=%+v stage=%+v", fixture.repo, fixture.stage)
	}
}

func TestAbsentTargetInstallerNeverOverwritesExistingTarget(t *testing.T) {
	fixture := newAbsentInstallerFixture(t, persistence.PublicationExposureRecorded, nil)
	const existing = "existing target"
	target := filepath.Join(fixture.rootPath, "installed.bin")
	if err := os.WriteFile(target, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fixture.installer.Install(context.Background()); err == nil {
		t.Fatal("Install replaced an existing target")
	}
	fixture.assertSourceAndTarget(t, true, true)
	got, err := os.ReadFile(target)
	if err != nil || string(got) != existing {
		t.Fatalf("existing target changed: bytes=%q err=%v", got, err)
	}
	assertAbsentInstallerQuarantined(t, fixture)
}

func TestAbsentTargetInstallerRecordFailureRetainsExposedTargetAndQuarantines(t *testing.T) {
	fixture := newAbsentInstallerFixture(t, persistence.PublicationExposureRecorded, nil)
	fixture.repo.record = persistence.PublicationCandidateInstalledStale
	fixture.repo.recordErr = errors.New("record failed")
	if err := fixture.installer.Install(context.Background()); err == nil {
		t.Fatal("Install succeeded when installed candidate recording failed")
	}
	fixture.assertSourceAndTarget(t, false, true)
	got, err := os.ReadFile(filepath.Join(fixture.rootPath, "installed.bin"))
	if err != nil || string(got) != absentInstallerPayload {
		t.Fatalf("exposed target was reversed or changed: bytes=%q err=%v", got, err)
	}
	if fixture.repo.beginCalls != 1 || fixture.repo.recordCalls != 1 {
		t.Fatalf("unexpected repository calls: %+v", fixture.repo)
	}
	assertAbsentInstallerQuarantined(t, fixture)
}

func TestAbsentTargetInstallerWaitsForActiveCommandReservation(t *testing.T) {
	backend := &blockingAbsentInstallerBackend{started: make(chan struct{}), release: make(chan struct{})}
	fixture := newAbsentInstallerFixture(t, persistence.PublicationExposureRecorded, backend)
	runDone := make(chan error, 1)
	go func() {
		result, err := fixture.scope.Run(context.Background(), "fake-command")
		if err == nil && !result.QuiescenceEstablished {
			err = errors.New("fake command did not establish quiescence")
		}
		runDone <- err
	}()
	select {
	case <-backend.started:
	case <-time.After(5 * time.Second):
		t.Fatal("command backend did not start")
	}
	if err := fixture.installer.Install(context.Background()); err == nil {
		t.Fatal("Install proceeded while command execution was unresolved")
	}
	fixture.assertSourceAndTarget(t, true, false)
	if fixture.repo.beginCalls != 0 || fixture.stage.closeCalls != 0 {
		t.Fatalf("unresolved command reached exposure: repo=%+v stage=%+v", fixture.repo, fixture.stage)
	}
	close(backend.release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if err := fixture.installer.Install(context.Background()); err != nil {
		t.Fatalf("Install did not proceed after command quiescence: %v", err)
	}
	fixture.assertSourceAndTarget(t, false, true)
	if fixture.repo.beginCalls != 1 || fixture.repo.recordCalls != 1 {
		t.Fatalf("unexpected post-quiescence repository calls: %+v", fixture.repo)
	}
}

type blockingAbsentInstallerBackend struct {
	started chan struct{}
	release chan struct{}
}

func (backend *blockingAbsentInstallerBackend) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	close(backend.started)
	<-backend.release
	return PublicationCommandResult{QuiescenceEstablished: true}, nil
}

func assertAbsentInstallerQuarantined(t *testing.T, fixture *absentInstallerFixture) {
	t.Helper()
	fixture.scope.mu.Lock()
	unknown, stageClose := fixture.scope.unknown, fixture.scope.stageClose
	fixture.scope.mu.Unlock()
	quarantinedPublicationScopes.Lock()
	_, quarantined := quarantinedPublicationScopes.scopes[fixture.scope]
	delete(quarantinedPublicationScopes.scopes, fixture.scope)
	quarantinedPublicationScopes.Unlock()
	if !unknown || !quarantined || stageClose == nil || fixture.stage.closeCalls != 0 {
		t.Fatalf("uncertain exposure was not retained safely: unknown=%v quarantined=%v stageClose=%v closeCalls=%d", unknown, quarantined, stageClose != nil, fixture.stage.closeCalls)
	}
}

func intentDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
