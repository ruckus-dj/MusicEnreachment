package service

import (
	"context"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
)

// This exercises the staging runner's real synchronous-work reservation: a
// concurrent finalizer must not close the stage or release its coordinator
// ownership while the metadata writer is still using it.
type blockingPublicationMetadataWriter struct {
	entered chan struct{}
	resume  chan struct{}
}

func (w blockingPublicationMetadataWriter) Write(context.Context, string, map[string][]string, *metadata.Cover) (metadata.WriteResult, error) {
	close(w.entered)
	<-w.resume
	return metadata.WriteResult{}, nil
}

func TestPublicationStagingConcurrentFinalizeWaitsForMetadataWriter(t *testing.T) {
	repo := &pubStageRepo{}
	runner, scope, input, stage, events := pubStageSetup(t, repo, pubStageCommand{}, nil, nil)
	entered, resume := make(chan struct{}), make(chan struct{})
	runner.Metadata = blockingPublicationMetadataWriter{entered: entered, resume: resume}

	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), scope, input, stage)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata writer was not entered")
	}

	finalizeDone := make(chan error, 1)
	go func() { finalizeDone <- scope.Finalize(context.Background(), stage.Close) }()
	select {
	case err := <-finalizeDone:
		if err == nil {
			t.Fatal("Finalize succeeded while synchronous metadata work was active")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Finalize blocked instead of rejecting active staging work")
	}
	if containsPubStage(*events, "close") || containsPubStage(*events, "quiescent") {
		t.Fatalf("stage finalized during metadata write: events=%v", *events)
	}

	close(resume)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("staging run failed after writer resumed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("staging run did not finish after writer resumed")
	}
	if !containsPubStage(*events, "close") || !containsPubStage(*events, "quiescent") {
		t.Fatalf("deferred finalization did not clean up after staging: events=%v", *events)
	}
}
