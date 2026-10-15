package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type interruptedDeliveryRepository struct {
	analysisWorkerRepository
	operation *persistence.Operation
	delivery  persistence.SourceAnalysisOperationDelivery
	recovered bool
	settled   bool
}

func (repository *interruptedDeliveryRepository) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return repository.operation, nil
}

func (repository *interruptedDeliveryRepository) RecoverNormalizedSourceAnalysisDelivery(_ context.Context, _ uuid.UUID, delivery persistence.SourceAnalysisOperationDelivery, _ string) error {
	repository.delivery = delivery
	repository.recovered = true
	repository.operation.State = "failed"
	repository.operation.Stage = "recovered"
	repository.operation.TargetSourceRootID = nil
	return nil
}

func (repository *interruptedDeliveryRepository) SettleNormalizedSourceAnalysisDelivery(context.Context, uuid.UUID, persistence.SourceAnalysisOperationDelivery, string, string, string) error {
	repository.settled = true
	return errors.New("unexpected terminal settlement")
}

type recoveredRootDispatcher struct {
	rootID uuid.UUID
	calls  int
}

func (dispatcher *recoveredRootDispatcher) AdmitPending(_ context.Context, rootID uuid.UUID) (*persistence.Operation, error) {
	dispatcher.rootID = rootID
	dispatcher.calls++
	return nil, nil
}

func TestSourceAnalysisWorkerRecoversSameRunningDeliveryWithoutTerminalSettlement(t *testing.T) {
	rootID := uuid.New()
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: "running", Attempt: 4,
		RiverJobID: int64Pointer(82), TargetSourceRootID: &rootID,
		SourceAnalysisMode: persistence.SourceAnalysisModeBatch,
		InputSnapshot:      mustAnalysisRecoverySnapshot(t),
	}
	repository := &interruptedDeliveryRepository{operation: operation}
	dispatcher := &recoveredRootDispatcher{}
	worker := NewSourceAnalysisWorker(repository, service.NewOperations(nil), nil, nil, settings.PlatformState{})
	worker.SetPendingDispatcher(dispatcher)
	job := &river.Job[service.SourceAnalysisJobArgs]{JobRow: &rivertype.JobRow{ID: 82}, Args: service.SourceAnalysisJobArgs{OperationID: operation.ID}}

	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("recover same running delivery: %v", err)
	}
	if !repository.recovered || repository.delivery != (persistence.SourceAnalysisOperationDelivery{Attempt: 4, JobID: 82}) {
		t.Fatalf("recovery did not use the captured delivery fence: %+v", repository)
	}
	if repository.settled {
		t.Fatal("running redelivery was settled as a terminal failure")
	}
	if dispatcher.calls != 1 || dispatcher.rootID != rootID {
		t.Fatalf("pending dispatcher = (%d calls, %s), want one admission for %s", dispatcher.calls, dispatcher.rootID, rootID)
	}
}

func mustAnalysisRecoverySnapshot(t *testing.T) json.RawMessage {
	t.Helper()
	rerun := false
	workID := uuid.New()
	raw, err := json.Marshal(persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          persistence.SourceAnalysisModeBatch,
		WorkIDs:       []uuid.UUID{workID},
		RerunTarget:   &rerun,
		SelectedSteps: []persistence.SourceAnalysisStepSelection{{WorkID: workID, Step: persistence.SourceStepSHA256}},
	})
	if err != nil {
		t.Fatalf("encode interrupted analysis snapshot: %v", err)
	}
	return raw
}

func int64Pointer(value int64) *int64 { return &value }
