package jobs

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type incomingGroupsRefreshRepository struct {
	analysisWorkerRepository
	events *[]string
	err    error
}

func (repository *incomingGroupsRefreshRepository) SettleNormalizedSourceAnalysisDelivery(context.Context, uuid.UUID, persistence.SourceAnalysisOperationDelivery, string, string, string) error {
	*repository.events = append(*repository.events, "settle")
	return repository.err
}

type incomingGroupsRefreshRecorder struct {
	events *[]string
	err    error
	ctxErr error
}

func (recorder *incomingGroupsRefreshRecorder) RefreshIfNeeded(ctx context.Context) error {
	*recorder.events = append(*recorder.events, "refresh")
	recorder.ctxErr = ctx.Err()
	return recorder.err
}

func TestSourceAnalysisWorkerRefreshesIncomingGroupsAfterSettlement(t *testing.T) {
	for _, state := range []string{"succeeded", "failed"} {
		t.Run(state, func(t *testing.T) {
			events := []string{}
			repository := &incomingGroupsRefreshRepository{events: &events}
			refresher := &incomingGroupsRefreshRecorder{events: &events, err: errors.New("refresh unavailable")}
			worker := &SourceAnalysisWorker{repository: repository, operations: service.NewOperations(nil)}
			worker.SetIncomingGroupsRefresher(refresher)

			operation := &persistence.Operation{ID: uuid.New(), Attempt: 2}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			safeError := ""
			if state == "failed" {
				safeError = "analysis failed"
			}
			if err := worker.settleDelivery(ctx, operation, 17, state, "applying", safeError); err != nil {
				t.Fatalf("settleDelivery: %v", err)
			}
			if !reflect.DeepEqual(events, []string{"settle", "refresh"}) {
				t.Fatalf("events = %v, want settlement followed by refresh", events)
			}
			if refresher.ctxErr != nil {
				t.Fatalf("refresh context error = %v, want cancellation removed", refresher.ctxErr)
			}
		})
	}
}

func TestSourceAnalysisWorkerDoesNotRefreshAfterFailedSettlement(t *testing.T) {
	events := []string{}
	repository := &incomingGroupsRefreshRepository{events: &events, err: errors.New("settlement failed")}
	refresher := &incomingGroupsRefreshRecorder{events: &events}
	worker := &SourceAnalysisWorker{repository: repository, operations: service.NewOperations(nil)}
	worker.SetIncomingGroupsRefresher(refresher)

	err := worker.settleDelivery(context.Background(), &persistence.Operation{ID: uuid.New(), Attempt: 1}, 3, "failed", "queued", "analysis failed")
	if err == nil {
		t.Fatal("settleDelivery succeeded after repository settlement failed")
	}
	if !reflect.DeepEqual(events, []string{"settle"}) {
		t.Fatalf("events = %v, want settlement only", events)
	}
}
