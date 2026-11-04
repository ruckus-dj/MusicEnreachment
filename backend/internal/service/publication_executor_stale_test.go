package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type staleResultPreparationCommand struct {
	calls int
}

func (runner *staleResultPreparationCommand) Run(_ context.Context, name string, _ ...string) (PublicationCommandResult, error) {
	runner.calls++
	executable := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	if executable != "ffmpeg" && executable != "ffprobe" {
		return PublicationCommandResult{}, fmt.Errorf("unexpected publication executable %q", executable)
	}
	return PublicationCommandResult{
		Output:                []byte(executable + " version 7.0"),
		QuiescenceEstablished: true,
	}, nil
}

func (runner *staleResultPreparationCommand) RunFile(_ context.Context, request tools.FileCommandRequest) (PublicationCommandResult, error) {
	if strings.TrimSuffix(filepath.Base(request.Executable), ".exe") != "ffprobe" || request.Input == nil {
		return PublicationCommandResult{}, fmt.Errorf("unexpected publication file command %+v", request)
	}
	return PublicationCommandResult{
		Output:                []byte(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}`),
		QuiescenceEstablished: true,
	}, nil
}

// The persistence callback is deliberately a denial rather than a simulated
// SQL implementation. This exercises the real executor's late-result guard:
// native preparation runs, but an obsolete result must not be committed.
func TestPublicationExecutorRejectsStaleResultAfterPreparation(t *testing.T) {
	commands := &staleResultPreparationCommand{}
	fixture := newQuiescenceExecutorFixture(t, commands)
	fixture.repository.obsoleteOutput = func(context.Context, persistence.PublicationDeliveryGuard, string) (*persistence.PublicationObsoleteOutputCandidate, error) {
		return nil, nil
	}
	resultCurrentCalls := 0
	fixture.repository.resultCurrent = func(_ context.Context, _ persistence.PublicationDeliveryGuard) (bool, error) {
		resultCurrentCalls++
		return false, nil
	}
	fixture.repository.commit = func(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationResult) (bool, error) {
		t.Fatal("stale publication result was committed")
		return false, nil
	}

	err := fixture.executor.ExecutePublicationDelivery(context.Background(), fixture.operationID, fixture.riverJobID, 1)
	if err == nil {
		t.Fatal("stale publication delivery unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "current generation changed before installation") {
		t.Fatalf("stale publication delivery failed before its late currentness check: %v", err)
	}
	if resultCurrentCalls != 1 {
		t.Fatalf("result-current checks = %d, want exactly one", resultCurrentCalls)
	}
	if fixture.repository.committedResult != nil {
		t.Fatalf("stale publication produced a committed result: %+v", fixture.repository.committedResult)
	}
	if commands.calls == 0 {
		t.Fatal("pinned native tools were not verified before the result guard")
	}
	if len(fixture.repository.guards) == 0 || fixture.repository.guards[len(fixture.repository.guards)-1].OperationID != fixture.operationID {
		t.Fatalf("delivery guard was not propagated through result checking: %+v", fixture.repository.guards)
	}
}
