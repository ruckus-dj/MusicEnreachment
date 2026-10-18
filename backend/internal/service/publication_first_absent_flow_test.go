package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type firstAbsentFlowRepo struct {
	pubStageRepo
	absentInstallerRepositoryFake
	pinnedToolRepo
	events    *[]string
	commit    bool
	commitErr error
	release   bool
	cleanup   persistence.PublicationFirstAbsentCleanupWitness
}

func (r *firstAbsentFlowRepo) CheckPublicationSourceHoldForDelivery(context.Context, persistence.PublicationDeliveryGuard, uuid.UUID) (persistence.PublicationSourceHold, bool, error) {
	return persistence.PublicationSourceHold{}, true, nil
}

func (*firstAbsentFlowRepo) PublicationPreparationInputs(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
	return persistence.PublicationInputs{}, false, nil
}

func (*firstAbsentFlowRepo) CheckPublicationRewriteForDelivery(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationRewritePreparationFacts, bool, error) {
	return persistence.PublicationRewritePreparationFacts{}, false, nil
}

func (*firstAbsentFlowRepo) RecordPublicationPreparation(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationPreparationRecord) (persistence.PublicationPreparationOutcome, error) {
	return persistence.PublicationPreparationStale, nil
}

func (r *firstAbsentFlowRepo) BeginPublicationFirstAbsentCleanup(_ context.Context, _ persistence.PublicationDeliveryGuard, witness persistence.PublicationFirstAbsentCleanupWitness) (persistence.PublicationFirstAbsentCleanupOutcome, error) {
	*r.events = append(*r.events, "input-intent")
	r.cleanup = witness
	return persistence.PublicationFirstAbsentCleanupRecorded, nil
}

func (r *firstAbsentFlowRepo) BeginPublicationFirstAbsentDirectoryCleanup(_ context.Context, _ persistence.PublicationDeliveryGuard, witness persistence.PublicationFirstAbsentCleanupWitness) (persistence.PublicationFirstAbsentCleanupOutcome, error) {
	*r.events = append(*r.events, "directory-intent")
	if witness != r.cleanup {
		return persistence.PublicationFirstAbsentCleanupStale, nil
	}
	return persistence.PublicationFirstAbsentCleanupRecorded, nil
}

func (r *firstAbsentFlowRepo) RecordPublicationFirstAbsentCleanupResult(_ context.Context, _ persistence.PublicationDeliveryGuard, witness persistence.PublicationFirstAbsentCleanupWitness, name, result string) (persistence.PublicationFirstAbsentCleanupOutcome, error) {
	*r.events = append(*r.events, name+"-result:"+result)
	if witness != r.cleanup || (name != "input" && name != "directory") {
		return persistence.PublicationFirstAbsentCleanupStale, nil
	}
	return persistence.PublicationFirstAbsentCleanupRecorded, nil
}

func (r *firstAbsentFlowRepo) SettleFirstAbsentPublication(_ context.Context, _ persistence.PublicationDeliveryGuard, witness persistence.PublicationFirstAbsentCleanupWitness, acknowledged bool) (bool, error) {
	*r.events = append(*r.events, "settle")
	return acknowledged && witness == r.cleanup, nil
}

func (r *firstAbsentFlowRepo) PublicationToolsForDelivery(ctx context.Context, guard persistence.PublicationDeliveryGuard) ([]persistence.SourceAnalysisToolSelection, *persistence.ToolInstallation, string, bool, error) {
	return r.pinnedToolRepo.PublicationToolsForDelivery(ctx, guard)
}

func (r *firstAbsentFlowRepo) CommitFirstPublication(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	*r.events = append(*r.events, "commit")
	return r.commit, r.commitErr
}

func (r *firstAbsentFlowRepo) ReleasePublicationInputHolds(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	*r.events = append(*r.events, "release")
	return r.release, nil
}

func (r *firstAbsentFlowRepo) CheckPublicationDelivery(ctx context.Context, guard persistence.PublicationDeliveryGuard) (bool, error) {
	return r.pinnedToolRepo.CheckPublicationDelivery(ctx, guard)
}

func (r *firstAbsentFlowRepo) BeginPublicationExposure(ctx context.Context, guard persistence.PublicationDeliveryGuard, intent persistence.PublicationExposureIntent, stage persistence.PublicationStageObservation) (persistence.PublicationExposureOutcome, error) {
	*r.events = append(*r.events, "begin-exposure")
	return r.absentInstallerRepositoryFake.BeginPublicationExposure(ctx, guard, intent, stage)
}

func (r *firstAbsentFlowRepo) RecordPublicationCandidateInstalled(ctx context.Context, guard persistence.PublicationDeliveryGuard, intent persistence.PublicationExposureIntent, stage persistence.PublicationStageObservation, installed persistence.PublicationCandidateInstalledObservation) (persistence.PublicationCandidateInstalledOutcome, error) {
	*r.events = append(*r.events, "record-installed")
	return r.absentInstallerRepositoryFake.RecordPublicationCandidateInstalled(ctx, guard, intent, stage, installed)
}

type firstAbsentFlowCommand struct{ events *[]string }

func (c firstAbsentFlowCommand) Run(_ context.Context, name string, args ...string) (PublicationCommandResult, error) {
	if len(args) == 1 && args[0] == "-version" {
		*c.events = append(*c.events, "version")
		banner := "ffmpeg version 7.0 build\n"
		if strings.TrimSuffix(filepath.Base(name), ".exe") == "ffprobe" {
			banner = "ffprobe version 7.0 build\n"
		}
		return PublicationCommandResult{Output: []byte(banner), QuiescenceEstablished: true}, nil
	}
	*c.events = append(*c.events, "ffmpeg")
	return PublicationCommandResult{QuiescenceEstablished: true}, nil
}

type firstAbsentFlowValidator struct{}

func (firstAbsentFlowValidator) Validate(_ context.Context, path string, _ persistence.PublicationInputs) (PublicationStageObservation, error) {
	info, err := os.Stat(path)
	if err != nil {
		return PublicationStageObservation{}, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return PublicationStageObservation{}, err
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256(contents)
	return PublicationStageObservation{Ownership: persistence.ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime}, Audio: []byte(`{}`), Metadata: []byte(`{}`), SHA256: hex.EncodeToString(digest[:])}, nil
}

func TestRunFirstAbsentPublicationCompletesExposureCommitAndInputReleaseInOrder(t *testing.T) {
	for _, test := range []struct {
		name      string
		commit    bool
		commitErr error
		wantErr   bool
	}{
		{name: "acknowledged commit", commit: true},
		{name: "commit not acknowledged", wantErr: true},
		{name: "commit error", commitErr: errors.New("commit failed"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAbsentInstallerFixture(t, persistence.PublicationExposureRecorded, nil)
			_, selections, pinnedRepo := pinnedToolFixture(t, true)
			platform := tools.Platform{GOOS: pinnedRepo.installation.PlatformGOOS, GOARCH: pinnedRepo.installation.PlatformGOARCH}
			events := []string{}
			guard := persistence.PublicationDeliveryGuard{DeliveryToken: "first-absent-flow"}
			repo := &firstAbsentFlowRepo{
				absentInstallerRepositoryFake: *fixture.repo,
				pinnedToolRepo:                *pinnedRepo,
				events:                        &events,
				commit:                        test.commit,
				commitErr:                     test.commitErr,
				release:                       true,
			}
			repo.pubStageRepo.events = &events
			repo.begin = persistence.PublicationExposureRecorded
			repo.record = persistence.PublicationCandidateInstalledRecorded
			repo.selections = selections
			repo.valid = true
			commands := firstAbsentFlowCommand{events: &events}
			scope, err := NewPublicationExecutionScope(context.Background(), publication.NewCoordinator(), []publication.ResourceLock{{Key: "first-absent-flow", Mode: publication.Exclusive}}, guard, func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
				return persistence.PublicationDeliveryAcquired, nil
			}, commands, repo)
			if err != nil {
				t.Fatal(err)
			}
			fixture.scope = scope
			_, _, input, _, _ := pubStageSetup(t, &pubStageRepo{}, pubStageCommand{}, nil, nil)
			input.Tools = selections
			for _, selection := range selections {
				if selection.Executable == tools.ExpectedExecutables(tools.PackageFFmpeg, platform.GOOS)[1] {
					if input.Audio != nil {
						input.Audio.FFProbeVersion = selection.Version
					}
				}
			}
			inputPath := filepath.Join(fixture.stagePath, "input")
			if err := os.WriteFile(inputPath, []byte("source"), 0o600); err != nil {
				t.Fatal(err)
			}
			stageIdentity, err := fixture.stageDir.Identity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			parentIdentity, err := fixture.root.Identity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			sourceFileInfo, err := os.Stat(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			stage := &preparedPublicationStage{
				paths:              PublicationStagePaths{InputPath: inputPath, OutputPath: filepath.Join(fixture.stagePath, "candidate"), RelativePath: "stage/candidate"},
				parent:             fixture.root,
				directory:          fixture.stageDir,
				directoryName:      filepath.Base(fixture.stagePath),
				identity:           persistence.PublicationDirectoryIdentity{Platform: stageIdentity.Platform, Volume: stageIdentity.Volume, FileID: stageIdentity.FileID},
				parentIdentity:     parentIdentity,
				repository:         repo,
				opener:             sourcefs.NewOpener(),
				sourceRootPath:     fixture.rootPath,
				sourceRelativePath: filepath.Join(filepath.Base(fixture.stagePath), "input"),
				sourceSize:         sourceFileInfo.Size(),
				sourceMTime:        sourceFileInfo.ModTime(),
				sourceFileInfo:     sourceFileInfo,
				inputPath:          fixture.stagePath,
				inputName:          "input",
				guard:              guard,
			}
			stageRunner := PublicationStagingRunner{
				Repository: repo,
				Metadata:   firstAbsentFlowWriter{events: &events},
				Validator:  firstAbsentFlowValidator{},
			}
			pinnedTools, err := NewScopeBoundPublicationPinnedTools(repo, scope, guard, platform)
			if err != nil {
				t.Fatal(err)
			}
			stageRunner.PinnedTools = pinnedTools
			_, err = stageRunner.RunFirstAbsentPublication(context.Background(), scope, input, stage, repo, fixture.intent, fixture.root, fixture.stageDir, fixture.root, "candidate", "installed.bin")
			if (err != nil) != test.wantErr {
				t.Fatalf("RunFirstAbsentPublication() error = %v, wantErr %v", err, test.wantErr)
			}
			if test.wantErr {
				if !containsPubStage(events, "commit") {
					t.Fatalf("commit was not attempted: events=%v", events)
				}
				if !scope.unknown || containsPubStage(events, "release") {
					t.Fatalf("uncertain commit did not quarantine without releasing holds: events=%v scope=%+v", events, scope)
				}
				return
			}
			if !containsPubStage(events, "commit") || !containsPubStage(events, "settle") || indexPubStage(events, "record-installed") > indexPubStage(events, "commit") || indexPubStage(events, "commit") > indexPubStage(events, "input-intent") || indexPubStage(events, "input-intent") > indexPubStage(events, "directory-intent") || indexPubStage(events, "directory-intent") > indexPubStage(events, "settle") {
				t.Fatalf("exposure/commit/release order is wrong: %v", events)
			}
			if _, err := os.Stat(filepath.Join(fixture.rootPath, "installed.bin")); err != nil {
				t.Fatalf("installed target missing: %v", err)
			}
		})
	}
}

func TestFinishFirstAbsentPublicationUsesNativeCleanupAndSettlesInOrder(t *testing.T) {
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parentPath := filepath.Join(root, "stage")
	if err := os.Mkdir(parentPath, 0o700); err != nil {
		t.Fatal(err)
	}
	parent, err := sourcefs.NewOutputOpener().OpenRoot(ctx, parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := parent.Close(); err != nil {
			t.Errorf("close test parent: %v", err)
		}
	}()
	dir, err := parent.CreateDirExclusive(ctx, "attempt")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := dir.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	parentIdentity, err := parent.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inputContents := []byte("private source bytes")
	inputPath := filepath.Join(parentPath, "attempt", "input")
	if err := os.WriteFile(inputPath, inputContents, 0o600); err != nil {
		t.Fatal(err)
	}
	mutation := dir.(sourcefs.OutputMutation)
	inputHandle, err := mutation.OpenExistingRegular(ctx, "input")
	if err != nil {
		t.Fatal(err)
	}
	inputInfo, err := inputHandle.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := inputHandle.Close(); err != nil {
		t.Fatal(err)
	}
	targetContents := []byte("published target")
	targetPath := filepath.Join(root, "installed.bin")
	if err := os.WriteFile(targetPath, targetContents, 0o600); err != nil {
		t.Fatal(err)
	}
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	repo := &firstAbsentFlowRepo{events: &events}
	scope := &PublicationExecutionScope{firstAbsent: true, finalizing: true, release: func() {}}
	stage := &preparedPublicationStage{
		paths:          PublicationStagePaths{OutputPath: filepath.Join(parentPath, "attempt", "candidate")},
		parent:         parent,
		directory:      dir,
		directoryName:  "attempt",
		identity:       persistence.PublicationDirectoryIdentity{Platform: identity.Platform, Volume: identity.Volume, FileID: identity.FileID},
		parentIdentity: parentIdentity,
		sourceFileInfo: inputInfo,
		inputName:      "input",
	}
	if err := finishFirstAbsentPublication(ctx, scope, stage, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private input remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parentPath, "attempt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private attempt directory remains: %v", err)
	}
	contents, err := os.ReadFile(targetPath)
	if err != nil || string(contents) != string(targetContents) {
		t.Fatalf("published target changed: contents=%q err=%v", contents, err)
	}
	currentTargetInfo, err := os.Stat(targetPath)
	if err != nil || !os.SameFile(targetInfo, currentTargetInfo) {
		t.Fatalf("published target identity changed: %v", err)
	}
	if got := events; !equalStrings(got, []string{"input-intent", "input-result:removed", "directory-intent", "directory-result:removed", "settle"}) {
		t.Fatalf("cleanup/settlement acknowledgement order = %v", got)
	}
	if !scope.firstAbsent || !scope.finalized || scope.finalizing {
		t.Fatalf("scope was not acknowledged as first-absent finalized: %+v", scope)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type firstAbsentFlowWriter struct{ events *[]string }

func (w firstAbsentFlowWriter) Write(context.Context, string, map[string][]string, *metadata.Cover) (metadata.WriteResult, error) {
	*w.events = append(*w.events, "metadata")
	return metadata.WriteResult{}, nil
}
