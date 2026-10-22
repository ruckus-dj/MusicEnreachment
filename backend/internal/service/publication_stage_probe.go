package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type privateManualLyricsVersionRunner struct {
	scope *PublicationExecutionScope
	work  *privateManualLyricsWork
	stage *preparedPublicationStage
}

func (runner privateManualLyricsVersionRunner) Run(ctx context.Context, path string, args ...string) ([]byte, error) {
	if len(args) != 1 || args[0] != "-version" {
		return nil, errors.New("private manual lyrics tool verification permits only -version")
	}
	result, err := runner.scope.runAuthorized(ctx, path, args, func() bool {
		return runner.scope.validPrivateManualLyricsWorkLocked(runner.work, runner.stage) && !runner.scope.initialCopy && runner.scope.preInputCleanup == nil
	})
	return result.Output, err
}

// ScopeBoundPublicationStageProbe routes stage-file probing through the
// publication execution authority. Its fields are private so callers cannot
// substitute a different execution scope or executable resolver.
type ScopeBoundPublicationStageProbe struct {
	scope       *PublicationExecutionScope
	pinnedTools *GuardedPublicationPinnedTools
	selections  []persistence.SourceAnalysisToolSelection
}

// NewScopeBoundPublicationStageProbe binds probing to the exact scope already
// used by pinned-tool resolution.
func NewScopeBoundPublicationStageProbe(scope *PublicationExecutionScope, pinnedTools *GuardedPublicationPinnedTools, selections []persistence.SourceAnalysisToolSelection) (*ScopeBoundPublicationStageProbe, error) {
	if scope == nil || !scope.CanExecute() || pinnedTools == nil || pinnedTools.scope != scope || pinnedTools.resolver == nil || pinnedTools.resolver.scope != scope || pinnedTools.guard != scope.guard {
		return nil, errors.New("publication stage probe requires matching scope-bound pinned tools")
	}
	return &ScopeBoundPublicationStageProbe{scope: scope, pinnedTools: pinnedTools, selections: append([]persistence.SourceAnalysisToolSelection(nil), selections...)}, nil
}

// Probe resolves ffprobe from the frozen selections itself; executable is only
// checked against that fresh resolution and is never used to launch a command.
func (probe *ScopeBoundPublicationStageProbe) Probe(ctx context.Context, executable string, input sourcefs.RegularFile, container publication.AudioContainer) ([]byte, error) {
	return probe.probe(ctx, probe.selections, executable, input, container)
}

// probePrivateManualLyrics permits only the version checks and file probe
// needed to inspect the exact staged artifact under its live private work.
func (probe *ScopeBoundPublicationStageProbe) probePrivateManualLyrics(ctx context.Context, work *privateManualLyricsWork, stage *preparedPublicationStage, input sourcefs.RegularFile) ([]byte, error) {
	if probe == nil || probe.scope == nil || probe.pinnedTools == nil || input == nil || work == nil || stage == nil ||
		work.scope != probe.scope || work.stage != stage || stage.manualProbe != probe || stage.guard != probe.scope.guard ||
		probe.pinnedTools.scope != probe.scope || probe.pinnedTools.guard != probe.scope.guard || probe.pinnedTools.resolver == nil || probe.pinnedTools.resolver.scope != probe.scope ||
		!stage.privatePrepMode || !stage.rewrite || !probe.scope.validPrivateManualLyricsLaunch(work, stage) {
		return nil, errors.New("private manual lyrics probe requires its exact live work capability")
	}
	resolver := *probe.pinnedTools.resolver
	resolver.runner = privateManualLyricsVersionRunner{scope: probe.scope, work: work, stage: stage}
	pair, valid, err := resolver.Resolve(ctx, probe.pinnedTools.guard, probe.selections)
	if err != nil {
		return nil, fmt.Errorf("resolve private manual lyrics ffprobe: %w", err)
	}
	if !valid || pair.FFprobe == "" {
		return nil, errors.New("private manual lyrics ffprobe is unavailable")
	}
	demuxer, _ := publication.InputDemuxer(publication.AudioContainerMKA)
	request, err := tools.BuildPublicationFileCommandRequest(pair.FFprobe, demuxer, nil)
	if err != nil {
		return nil, err
	}
	result, err := probe.scope.runFileAuthorized(ctx, request.Executable, request.Args, input, func() bool {
		return probe.scope.validPrivateManualLyricsWorkLocked(work, stage) && !probe.scope.initialCopy && probe.scope.preInputCleanup == nil
	})
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}

func (probe *ScopeBoundPublicationStageProbe) probe(ctx context.Context, selections []persistence.SourceAnalysisToolSelection, executable string, input sourcefs.RegularFile, container publication.AudioContainer) ([]byte, error) {
	if probe == nil || probe.scope == nil || probe.pinnedTools == nil || input == nil {
		return nil, errors.New("scope-bound publication stage probe is unavailable")
	}
	expected, err := probe.pinnedTools.Executable(ctx, selections, "ffprobe")
	if err != nil {
		return nil, fmt.Errorf("resolve scope-bound publication ffprobe: %w", err)
	}
	if executable != expected {
		return nil, errors.New("publication stage probe executable does not match pinned ffprobe")
	}
	demuxer, _ := publication.InputDemuxer(container)
	request, err := tools.BuildPublicationFileCommandRequest(expected, demuxer, nil)
	if err != nil {
		return nil, err
	}
	result, err := probe.scope.RunFile(ctx, request.Executable, request.Args, input)
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}
