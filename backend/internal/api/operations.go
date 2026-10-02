package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type OperationsInput struct {
	State string `query:"state" enum:"queued,running,failed"`
}

type OperationsOutput struct {
	Body OperationsBody `json:"body"`
}

type OperationsBody struct {
	Operations []OperationResponse `json:"operations"`
}

type OperationInput struct {
	ID uuid.UUID `path:"operation_id"`
}

type OperationOutput struct {
	Body OperationResponse `json:"body"`
}

type OperationResponse struct {
	ID                     uuid.UUID  `json:"id"`
	Kind                   string     `json:"kind"`
	State                  string     `json:"state"`
	Stage                  string     `json:"stage"`
	TargetInstallationID   *uuid.UUID `json:"target_installation_id,omitempty"`
	TargetSourceRootID     *uuid.UUID `json:"target_source_root_id,omitempty"`
	TargetSourceLocationID *uuid.UUID `json:"target_source_location_id,omitempty"`
	TargetIdentity         string     `json:"target_identity,omitempty"`
	BytesCompleted         int64      `json:"bytes_completed"`
	BytesTotal             *int64     `json:"bytes_total,omitempty"`
	SafeError              *string    `json:"safe_error,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	StartedAt              *time.Time `json:"started_at,omitempty"`
	FinishedAt             *time.Time `json:"finished_at,omitempty"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

func registerOperations(api huma.API, operations *service.Operations, setup *service.SetupService) {
	huma.Register(api, huma.Operation{
		OperationID: "list-operations", Method: http.MethodGet, Path: "/operations",
		Summary: "List current operation snapshots", Tags: []string{"Operations"},
	}, func(ctx context.Context, input *OperationsInput) (*OperationsOutput, error) {
		if operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		states := []string(nil)
		if input.State != "" {
			states = append(states, input.State)
		}
		snapshots, err := operations.ListSnapshots(ctx, states...)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list operations")
		}
		result := make([]OperationResponse, 0, len(snapshots))
		for _, snapshot := range snapshots {
			result = append(result, operationResponse(snapshot))
		}
		return &OperationsOutput{Body: OperationsBody{Operations: result}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-operation", Method: http.MethodGet, Path: "/operations/{operation_id}",
		Summary: "Get an operation snapshot", Tags: []string{"Operations"},
	}, func(ctx context.Context, input *OperationInput) (*OperationOutput, error) {
		if operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		snapshot, err := operations.Snapshot(ctx, input.ID)
		if err != nil {
			return nil, huma.Error404NotFound("operation not found")
		}
		return operationOutput(snapshot), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "retry-operation", Method: http.MethodPost, Path: "/operations/{operation_id}/retry",
		Summary: "Retry a failed operation", Tags: []string{"Operations"},
	}, func(ctx context.Context, input *OperationInput) (*OperationOutput, error) {
		if operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if _, err := operations.Retry(ctx, input.ID); err != nil {
			return nil, huma.Error409Conflict("operation could not be retried")
		}
		snapshot, err := operations.Snapshot(ctx, input.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read retried operation")
		}
		return operationOutput(snapshot), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "dismiss-operation", Method: http.MethodDelete, Path: "/operations/{operation_id}",
		Summary: "Dismiss a failed operation", Tags: []string{"Operations"},
	}, func(ctx context.Context, input *OperationInput) (*struct{}, error) {
		if operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := operations.Dismiss(ctx, input.ID); err != nil {
			return nil, huma.Error409Conflict("only a failed operation can be dismissed")
		}
		return nil, nil
	})
}

func operationResponse(snapshot service.OperationSnapshot) OperationResponse {
	return OperationResponse{
		ID: snapshot.ID, Kind: snapshot.Kind, State: snapshot.State, Stage: snapshot.Stage,
		TargetInstallationID: snapshot.TargetInstallationID, TargetSourceRootID: snapshot.TargetSourceRootID,
		TargetSourceLocationID: snapshot.TargetSourceLocationID,
		TargetIdentity:         snapshot.TargetIdentity,
		BytesCompleted:         snapshot.BytesCompleted, BytesTotal: snapshot.BytesTotal,
		SafeError: snapshot.SafeError, CreatedAt: snapshot.CreatedAt,
		StartedAt: snapshot.StartedAt, FinishedAt: snapshot.FinishedAt, UpdatedAt: snapshot.UpdatedAt,
	}
}

func operationOutput(snapshot service.OperationSnapshot) *OperationOutput {
	return &OperationOutput{Body: operationResponse(snapshot)}
}
