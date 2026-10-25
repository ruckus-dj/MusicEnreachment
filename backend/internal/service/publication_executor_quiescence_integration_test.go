//go:build integration

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

type unknownQuiescencePublicationRunner struct {
	audioDeliveryTestCommands
	unknown bool
	fail    bool
	calls   int
}

func (runner *unknownQuiescencePublicationRunner) Run(ctx context.Context, name string, args ...string) (PublicationCommandResult, error) {
	runner.calls++
	if runner.unknown && len(args) > 0 && args[len(args)-1] == "-version" {
		return PublicationCommandResult{}, errors.New("version command did not establish quiescence")
	}
	if runner.fail && len(args) == 1 && args[0] == "-version" {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("known quiescent version failure")
	}
	return runner.audioDeliveryTestCommands.Run(ctx, name, args...)
}

func TestPublicationExecutorUnknownVersionQuiescenceRetainsCoordinatorPostgreSQL(t *testing.T) {
	var runner *unknownQuiescencePublicationRunner
	testPublicationSourceExecution(t, "Track.flac", true, false, publicationDeliveryScenario{run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, operationID uuid.UUID, riverJobID int64, _, _, _ string, _ []byte) {
		runner = &unknownQuiescencePublicationRunner{unknown: true}
		executor.commands = runner
		if err := executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 1); err == nil || !strings.Contains(err.Error(), "quiescence") {
			t.Fatalf("unknown version-command delivery error = %v", err)
		}
		attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
		if err != nil || attempt == nil {
			t.Fatalf("delivery claim was not retained: attempt=%+v err=%v", attempt, err)
		}
		if runner.calls == 0 {
			t.Fatal("version command was not invoked")
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := executor.coordinator.Acquire(cancelled, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}}); !errors.Is(err, context.Canceled) {
			t.Fatalf("acquire under canceled context = %v, want context canceled while resource remains retained", err)
		}
		before := runner.calls
		if err := executor.ExecutePublicationDelivery(cancelled, operationID, riverJobID, 2); err == nil {
			t.Fatal("second real delivery with canceled context unexpectedly succeeded")
		}
		if runner.calls != before {
			t.Fatalf("backend calls after canceled second delivery = %d, want unchanged %d", runner.calls, before)
		}
	}})
}

func TestPublicationExecutorKnownQuiescentFailureReleasesCoordinatorPostgreSQL(t *testing.T) {
	testPublicationSourceExecution(t, "Track.flac", true, false, publicationDeliveryScenario{run: func(ctx context.Context, _ *bun.DB, _ *persistence.SetupManagerRepository, executor *PublicationExecutor, operationID uuid.UUID, riverJobID int64, _, _, _ string, _ []byte) {
		runner := &unknownQuiescencePublicationRunner{fail: true}
		executor.commands = runner
		// The backend reports a known-quiescent command failure; it must not
		// strand the process-wide mutation reservation.
		if err := executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 1); err == nil {
			t.Fatal("delivery with the fixture's rejected output unexpectedly succeeded")
		}
		acquireCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		release, err := executor.coordinator.Acquire(acquireCtx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
		if err != nil {
			t.Fatalf("coordinator remained held after quiescent failure: %v", err)
		}
		release()
	}})
}
