package service

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type firstAbsentRepositoryFake struct{ absentInstallerRepositoryFake }

func (*firstAbsentRepositoryFake) CommitFirstPublication(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	return false, nil
}

func (*firstAbsentRepositoryFake) ReleasePublicationInputHolds(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	return false, nil
}

func TestRunFirstAbsentPublicationRejectsUnboundPinnedToolsBeforeWork(t *testing.T) {
	repo := &pubStageRepo{}
	runner, scope, input, stage, events := pubStageSetup(t, repo, pubStageCommand{}, nil, nil)
	firstRepo := &firstAbsentRepositoryFake{}
	guard := scope.guard
	resolver := NewPublicationPinnedToolsResolver(repo, nil, tools.Platform{})
	runner.PinnedTools = NewGuardedPublicationPinnedTools(resolver, guard)
	if runner.PinnedTools == nil {
		t.Fatal("expected legacy guarded adapter to remain constructible")
	}

	_, err := runner.RunFirstAbsentPublication(
		context.Background(), scope, input, stage, firstRepo,
		persistence.PublicationExposureIntent{Expectation: "absent"},
		nil, nil, nil, "", "",
	)
	if err == nil {
		t.Fatal("expected first-absent publication to reject legacy unbound pinned tools")
	}
	if len(*events) != 0 || firstRepo.beginCalls != 0 || firstRepo.recordCalls != 0 {
		t.Fatalf("unbound tools reached publication work: events=%v repo=%+v", *events, firstRepo)
	}
	if scope.guard != guard || scope.unknown || scope.finalized {
		t.Fatalf("rejected before execution but scope changed: %+v", scope)
	}
}

func TestRunFirstAbsentPublicationRejectsForeignScopePinnedTools(t *testing.T) {
	repo := &pubStageRepo{}
	runner, scope, input, stage, events := pubStageSetup(t, repo, pubStageCommand{}, nil, nil)
	firstRepo := &firstAbsentRepositoryFake{}
	foreign := &PublicationExecutionScope{guard: scope.guard, commands: pubStageScopedCommand{pubStageCommand{events: events}}}
	resolver := NewPublicationPinnedToolsResolver(repo, nil, tools.Platform{})
	resolver.scope = foreign
	pinned := NewGuardedPublicationPinnedTools(resolver, scope.guard)
	if pinned == nil {
		t.Fatal("expected guarded adapter for foreign scope")
	}
	pinned.scope = scope
	runner.PinnedTools = pinned

	_, err := runner.RunFirstAbsentPublication(
		context.Background(), scope, input, stage, firstRepo,
		persistence.PublicationExposureIntent{Expectation: "absent"},
		nil, nil, nil, "", "",
	)
	if err == nil {
		t.Fatal("expected foreign resolver scope to be rejected")
	}
	if len(*events) != 0 || firstRepo.beginCalls != 0 || firstRepo.recordCalls != 0 {
		t.Fatalf("foreign scope reached publication work: events=%v repo=%+v", *events, firstRepo)
	}
}
