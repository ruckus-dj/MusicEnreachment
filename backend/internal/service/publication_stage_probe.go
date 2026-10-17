package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

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
func (probe *ScopeBoundPublicationStageProbe) Probe(ctx context.Context, executable string, input sourcefs.RegularFile) ([]byte, error) {
	return probe.probe(ctx, probe.selections, executable, input)
}

func (probe *ScopeBoundPublicationStageProbe) probe(ctx context.Context, selections []persistence.SourceAnalysisToolSelection, executable string, input sourcefs.RegularFile) ([]byte, error) {
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
	request := tools.BuildTechnicalFileCommandRequest(expected, nil)
	result, err := probe.scope.RunFile(ctx, request.Executable, request.Args, input)
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}
