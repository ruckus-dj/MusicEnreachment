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

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
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
			inputPath := filepath.Join(fixture.rootPath, "input")
			if err := os.WriteFile(inputPath, []byte("source"), 0o600); err != nil {
				t.Fatal(err)
			}
			stage := &pubStagePrepared{paths: PublicationStagePaths{InputPath: inputPath, OutputPath: filepath.Join(fixture.stagePath, "candidate"), RelativePath: "stage/candidate"}, events: &events}
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
			if !containsPubStage(events, "commit") || !containsPubStage(events, "release") || indexPubStage(events, "record-installed") > indexPubStage(events, "commit") || indexPubStage(events, "commit") > indexPubStage(events, "release") {
				t.Fatalf("exposure/commit/release order is wrong: %v", events)
			}
			if _, err := os.Stat(filepath.Join(fixture.rootPath, "installed.bin")); err != nil {
				t.Fatalf("installed target missing: %v", err)
			}
		})
	}
}

type firstAbsentFlowWriter struct{ events *[]string }

func (w firstAbsentFlowWriter) Write(context.Context, string, map[string][]string, *metadata.Cover) (metadata.WriteResult, error) {
	*w.events = append(*w.events, "metadata")
	return metadata.WriteResult{}, nil
}
