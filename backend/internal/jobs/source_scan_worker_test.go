package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type incomingGroupsRefreshSpy struct {
	calls int
	err   error
	order *[]string
}

func (spy *incomingGroupsRefreshSpy) RefreshIfNeeded(context.Context) error {
	spy.calls++
	if spy.order != nil {
		*spy.order = append(*spy.order, "refresh")
	}
	return spy.err
}

type scanFinishSpy struct {
	scanWorkerRepository
	order *[]string
}

func (spy *scanFinishSpy) FinishSourceScanDelivery(context.Context, uuid.UUID, int, int64, string, string, string) error {
	*spy.order = append(*spy.order, "settle")
	return nil
}

func TestSourceScanFinishRefreshesAfterSettlementAndIgnoresRefreshError(t *testing.T) {
	order := []string{}
	refresher := &incomingGroupsRefreshSpy{err: errors.New("refresh failed"), order: &order}
	worker := &SourceScanWorker{repository: &scanFinishSpy{order: &order}, incomingGroups: refresher}
	operation := &persistence.Operation{ID: uuid.New(), Attempt: 1}
	if err := worker.finishAndDispatch(context.Background(), operation, uuid.New()); err != nil {
		t.Fatalf("finishAndDispatch() error = %v", err)
	}
	if refresher.calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refresher.calls)
	}
	if len(order) != 2 || order[0] != "settle" || order[1] != "refresh" {
		t.Fatalf("call order = %v, want [settle refresh]", order)
	}
}
