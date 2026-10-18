package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// The replacement entry point must reject incomplete execution state before
// opening a stage or touching the published target. This guards the public
// workflow boundary (the lower-level replacer has separate native tests).
func TestRunReplacementPublicationRejectsMissingScopeWithoutTouchingFilesystem(t *testing.T) {
	fixture := makeRewriteFixture(t)
	prepared, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	stage := prepared.(*preparedPublicationStage)
	t.Cleanup(func() { _ = stage.Close() })
	before, err := os.ReadFile(filepath.Join(fixture.output, "current.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = (PublicationStagingRunner{}).RunReplacementPublication(
		context.Background(), nil, fixture.repo.input, stage, nil,
		persistence.PublicationExposureIntent{Expectation: "expected_current"}, nil,
		fixture.preparer.OutputParent, fixture.preparer.OutputParent, fixture.preparer.OutputParent,
		stage.directoryName, "current.mp3",
	)
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("RunReplacementPublication error = %v, want dependency validation error", err)
	}
	after, readErr := os.ReadFile(filepath.Join(fixture.output, "current.mp3"))
	if readErr != nil || string(after) != string(before) {
		t.Fatalf("published target changed: bytes=%q err=%v", after, readErr)
	}
}

type replacementFlowRepo struct {
	*firstAbsentFlowRepo
	commitCalls int
	settled     bool
	witness     persistence.PublicationReplacementCleanupWitness
}

type replacementFlowCommand struct{ events *[]string }

func (c replacementFlowCommand) Run(ctx context.Context, name string, args ...string) (PublicationCommandResult, error) {
	if len(args) == 1 && args[0] == "-version" {
		return firstAbsentFlowCommand(c).Run(ctx, name, args...)
	}
	*c.events = append(*c.events, "ffmpeg")
	if len(args) == 0 {
		return PublicationCommandResult{QuiescenceEstablished: true}, nil
	}
	if err := os.WriteFile(args[len(args)-1], []byte("new encoded audio payload"), 0o600); err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	return PublicationCommandResult{QuiescenceEstablished: true}, nil
}

func (r *replacementFlowRepo) CommitReplacementPublication(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	r.commitCalls++
	*r.events = append(*r.events, "commit")
	return true, nil
}

func (*replacementFlowRepo) RecordPublicationReplacement(_ context.Context, _ persistence.PublicationDeliveryGuard, evidence persistence.PublicationReplacementEvidence) (persistence.PublicationReplacementOutcome, error) {
	return persistence.PublicationReplacementRecorded, nil
}

func (r *replacementFlowRepo) BeginPublicationReplacementCleanup(_ context.Context, _ persistence.PublicationDeliveryGuard, witness persistence.PublicationReplacementCleanupWitness) (persistence.PublicationReplacementCleanupOutcome, error) {
	r.witness = witness
	return persistence.PublicationReplacementCleanupRecorded, nil
}

func (r *replacementFlowRepo) BeginPublicationReplacementDirectoryCleanup(_ context.Context, _ persistence.PublicationDeliveryGuard, witness persistence.PublicationReplacementCleanupWitness) (persistence.PublicationReplacementCleanupOutcome, error) {
	if witness != r.witness {
		return persistence.PublicationReplacementCleanupStale, nil
	}
	return persistence.PublicationReplacementCleanupRecorded, nil
}

func (r *replacementFlowRepo) RecordPublicationReplacementCleanupResult(_ context.Context, _ persistence.PublicationDeliveryGuard, witness persistence.PublicationReplacementCleanupWitness, _, _ string) (persistence.PublicationReplacementCleanupOutcome, error) {
	if witness != r.witness {
		return persistence.PublicationReplacementCleanupStale, nil
	}
	return persistence.PublicationReplacementCleanupRecorded, nil
}

func (r *replacementFlowRepo) SettleReplacementPublication(_ context.Context, _ persistence.PublicationDeliveryGuard, witness persistence.PublicationReplacementCleanupWitness, acknowledged bool) (bool, error) {
	r.settled = acknowledged && witness == r.witness
	return r.settled, nil
}

func TestRunReplacementPublicationCompletesNativeUnixReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native replacement is unsupported on Windows")
	}
	fixture := makeRewriteFixture(t)
	guard := persistence.PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New(), DeliveryToken: "replacement-flow"}
	fixture.guard = guard
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	prepared, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	stage := prepared.(*preparedPublicationStage)
	t.Cleanup(func() { _ = stage.Close() })

	_, selections, pinnedRepo := pinnedToolFixture(t, true)
	platform := tools.Platform{GOOS: pinnedRepo.installation.PlatformGOOS, GOARCH: pinnedRepo.installation.PlatformGOARCH}
	events := []string{}
	flow := &firstAbsentFlowRepo{events: &events, commit: true, release: true}
	flow.pubStageRepo.events = &events
	flow.pinnedToolRepo = *pinnedRepo
	flow.selections = selections
	flow.valid = true
	repo := &replacementFlowRepo{firstAbsentFlowRepo: flow}
	coordinator := publication.NewCoordinator()
	scope, err := NewPublicationExecutionScope(context.Background(), coordinator, []publication.ResourceLock{{Key: "replacement-flow", Mode: publication.Exclusive}}, guard, func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
		return persistence.PublicationDeliveryAcquired, nil
	}, replacementFlowCommand{events: &events}, repo)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.repo.input
	input.Version = persistence.PublicationInputsVersion
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	input.DesiredTags = map[string][]string{}
	input.Tools = selections
	stageRunner := PublicationStagingRunner{Repository: repo, Metadata: firstAbsentFlowWriter{events: &events}, Validator: firstAbsentFlowValidator{}}
	pinnedTools, err := NewScopeBoundPublicationPinnedTools(repo, scope, guard, platform)
	if err != nil {
		t.Fatal(err)
	}
	stageRunner.PinnedTools = pinnedTools
	rootID, err := fixture.preparer.OutputParent.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	intent := persistence.PublicationExposureIntent{
		Expectation: "expected_current", StagedPath: filepath.ToSlash(stage.Paths().RelativePath),
		TargetRelativePath: "current.mp3",
		OutputRootParent:   persistence.PublicationDirectoryIdentity{Platform: rootID.Platform, Volume: rootID.Volume, FileID: rootID.FileID},
		TargetParent:       persistence.PublicationDirectoryIdentity{Platform: rootID.Platform, Volume: rootID.Volume, FileID: rootID.FileID},
	}
	generation := fixture.repo.input.Rewrite.CurrentGeneration
	result, err := stageRunner.RunReplacementPublication(context.Background(), scope, input, stage, repo, intent, &generation, fixture.preparer.OutputParent, stage.directory, fixture.preparer.OutputParent, stage.directoryName, "current.mp3")
	if err != nil {
		t.Fatalf("RunReplacementPublication() error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(fixture.output, "current.mp3"))
	if err != nil || string(contents) != "new encoded audio payload" {
		t.Fatalf("published target = %q, %v", contents, err)
	}
	if result.SHA256 == "" || repo.commitCalls != 1 || !repo.settled || !scope.finalized || scope.unknown {
		t.Fatalf("replacement completion: result=%+v commits=%d settled=%v scope=%+v", result, repo.commitCalls, repo.settled, scope)
	}
	if _, err := os.Stat(filepath.Join(fixture.output, stage.directoryName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private attempt directory remains: %v", err)
	}
	release, err := coordinator.Acquire(context.Background(), []publication.ResourceLock{{Key: "replacement-flow", Mode: publication.Exclusive}})
	if err != nil {
		t.Fatalf("coordinator resource was not released after acknowledgement: %v", err)
	}
	release()
	if !containsPubStage(events, "commit") || indexPubStage(events, "commit") >= len(events) {
		t.Fatalf("replacement commit was not observed: %v", events)
	}
}
