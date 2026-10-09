package jobs

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type ProviderFetchWorker struct {
	river.WorkerDefaults[service.ProviderFetchOperationArgs]
	fetch      *service.ProviderFetchService
	operations *service.Operations
}

func NewProviderFetchWorker(fetch *service.ProviderFetchService, operations *service.Operations) *ProviderFetchWorker {
	return &ProviderFetchWorker{fetch: fetch, operations: operations}
}

func (worker *ProviderFetchWorker) Work(ctx context.Context, job *river.Job[service.ProviderFetchOperationArgs]) error {
	if worker.fetch == nil || worker.operations == nil || job == nil || job.Args.OperationID == uuid.Nil {
		return fmt.Errorf("provider fetch worker is not configured")
	}
	return worker.fetch.ExecuteDurableOperation(ctx, worker.operations, job.Args.OperationID, job.ID)
}

var _ river.Worker[service.ProviderFetchOperationArgs] = (*ProviderFetchWorker)(nil)
