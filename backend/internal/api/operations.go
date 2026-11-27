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
	// ActiveSourceScans selects the Sources discovery read: only queued or
	// running scan_source operations that own a source root. When set, state is
	// ignored and no other operation is listed.
	ActiveSourceScans bool `query:"active_source_scans" doc:"Return only queued or running source scans that own a source root; state is ignored."`
	// ToolDiscovery selects managed-tool discovery: only failed, queued, or
	// running installations, optionally widened with tools-root moves. An
	// explicit state narrows that fixed set. ActiveSourceScans takes priority
	// when both selectors are present.
	ToolDiscovery string `query:"tool_discovery" enum:"install,install_and_move" doc:"Return only queued, running, or failed managed-tool operations. install lists installations; install_and_move also lists tools-root moves. An explicit state narrows that fixed set; active_source_scans takes priority."`
}

type OperationsOutput struct {
	Body OperationsBody `json:"body"`
}

type OperationsBody struct {
	Operations []OperationResponse `json:"operations"`
}

type CurrentOperationsInput struct {
	Status    string `query:"status" default:"attention" enum:"attention,all,failed,running,queued"`
	Kind      string `query:"kind" required:"false" enum:"publication,analysis,removal,matching"`
	Query     string `query:"q" maxLength:"200"`
	Offset    int    `query:"offset" minimum:"0" maximum:"1000000"`
	Sort      string `query:"sort" default:"created" enum:"target,operation,state,created" doc:"Column used to order the whole filtered set before pagination."`
	Direction string `query:"direction" default:"desc" enum:"asc,desc" doc:"Ordering direction of the selected sort column."`
	Locale    string `query:"locale" default:"en" enum:"en,ru" doc:"Locale of the operation wording used by the operation sort."`
}

type CurrentOperationsOutput struct {
	CacheControl string                `header:"Cache-Control"`
	Body         CurrentOperationsBody `json:"body"`
}

type CurrentOperationsBody struct {
	Operations []OperationResponse `json:"operations"`
	Total      int                 `json:"total"`
}

type OperationInput struct {
	ID uuid.UUID `path:"operation_id"`
}

type OperationOutput struct {
	Body OperationResponse `json:"body"`
}

type OperationResponse struct {
	ID                     uuid.UUID                                         `json:"id"`
	Kind                   string                                            `json:"kind"`
	State                  string                                            `json:"state"`
	Stage                  string                                            `json:"stage"`
	TargetInstallationID   *uuid.UUID                                        `json:"target_installation_id,omitempty"`
	TargetSourceRootID     *uuid.UUID                                        `json:"target_source_root_id,omitempty"`
	TargetSourceLocationID *uuid.UUID                                        `json:"target_source_location_id,omitempty"`
	TargetIdentity         string                                            `json:"target_identity,omitempty"`
	TargetLabel            string                                            `json:"target_label,omitempty"`
	TargetRef              *service.OperationTargetRef                       `json:"target_ref,omitempty"`
	MatchingRecheckMember  bool                                              `json:"matching_recheck_member,omitempty"`
	BytesCompleted         int64                                             `json:"bytes_completed"`
	BytesTotal             *int64                                            `json:"bytes_total,omitempty"`
	SafeError              *string                                           `json:"safe_error,omitempty"`
	CreatedAt              time.Time                                         `json:"created_at"`
	StartedAt              *time.Time                                        `json:"started_at,omitempty"`
	FinishedAt             *time.Time                                        `json:"finished_at,omitempty"`
	UpdatedAt              time.Time                                         `json:"updated_at"`
	CleanupResults         []service.SourceAnalysisArtifactCleanupItemResult `json:"cleanup_results,omitempty"`
}

func registerOperations(api huma.API, operations *service.Operations, setup *service.SetupService, cleanup *service.SourceAnalysisArtifactCleanup, libraryDeletion LibraryDeletionAPI) {
	huma.Register(api, huma.Operation{
		OperationID: "list-operations", Method: http.MethodGet, Path: "/operations",
		Summary: "List current operation snapshots", Tags: []string{"Operations"},
	}, func(ctx context.Context, input *OperationsInput) (*OperationsOutput, error) {
		if operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		if input.ActiveSourceScans {
			snapshots, err := operations.ListActiveSourceScanSnapshots(ctx)
			if err != nil {
				return nil, huma.Error500InternalServerError("failed to list active source scans")
			}
			return &OperationsOutput{Body: OperationsBody{Operations: operationResponses(snapshots)}}, nil
		}
		if input.ToolDiscovery != "" {
			snapshots, err := operations.ListToolDiscoverySnapshots(ctx, input.ToolDiscovery == "install_and_move", input.State)
			if err != nil {
				return nil, huma.Error500InternalServerError("failed to list tool operations")
			}
			return &OperationsOutput{Body: OperationsBody{Operations: operationResponses(snapshots)}}, nil
		}
		states := []string(nil)
		if input.State != "" {
			states = append(states, input.State)
		}
		snapshots, err := operations.ListSnapshots(ctx, states...)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list operations")
		}
		return &OperationsOutput{Body: OperationsBody{Operations: operationResponses(snapshots)}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-current-operations", Method: http.MethodGet, Path: "/operations/current",
		Summary: "List current operation snapshots with attention filter", Tags: []string{"Operations"},
	}, func(ctx context.Context, input *CurrentOperationsInput) (*CurrentOperationsOutput, error) {
		if operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		snapshots, total, err := operations.ListCurrentSnapshots(ctx, service.CurrentOperationsQuery{
			Status: input.Status, Kind: input.Kind, Query: input.Query,
			Sort: input.Sort, Direction: input.Direction, Locale: input.Locale,
			Offset: input.Offset,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list current operations")
		}
		result := make([]OperationResponse, 0, len(snapshots))
		for _, snapshot := range snapshots {
			result = append(result, operationResponse(snapshot))
		}
		return &CurrentOperationsOutput{
			CacheControl: "no-store",
			Body:         CurrentOperationsBody{Operations: result, Total: total},
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-operation", Method: http.MethodGet, Path: "/operations/{operation_id}",
		Summary: "Get an operation snapshot", Tags: []string{"Operations"},
	}, func(ctx context.Context, input *OperationInput) (*OperationOutput, error) {
		if operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		detail, err := operations.Detail(ctx, input.ID)
		if err != nil {
			return nil, huma.Error404NotFound("operation not found")
		}
		output := operationOutput(detail.Snapshot)
		if detail.Snapshot.Kind == service.SourceAnalysisArtifactCleanupOperationKind && cleanup != nil {
			output.Body.CleanupResults = detail.CleanupResults
		}
		return output, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "retry-operation", Method: http.MethodPost, Path: "/operations/{operation_id}/retry",
		Summary: "Retry a failed operation", Tags: []string{"Operations"},
	}, func(ctx context.Context, input *OperationInput) (*OperationOutput, error) {
		if operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		if err := requireSupportedPlatform(setup); err != nil {
			return nil, err
		}
		if snapshot, err := operations.Snapshot(ctx, input.ID); err == nil && snapshot.Kind == service.LibraryDeletionOperationKind {
			if libraryDeletion == nil {
				return nil, huma.Error503ServiceUnavailable("library deletion retry is unavailable")
			}
			retriedID, err := libraryDeletion.Retry(ctx, input.ID)
			if err != nil {
				return nil, libraryDeletionRetryError(err)
			}
			retried, err := operations.Snapshot(ctx, retriedID)
			if err != nil {
				return nil, huma.Error500InternalServerError("failed to read retried deletion operation")
			}
			return operationOutput(retried), nil
		}
		retried, err := operations.Retry(ctx, input.ID)
		if err != nil {
			return nil, huma.Error409Conflict("operation could not be retried")
		}
		snapshot, err := operations.Snapshot(ctx, retried.ID)
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
		if err := requireSupportedPlatform(setup); err != nil {
			return nil, err
		}
		if err := operations.Dismiss(ctx, input.ID); err != nil {
			return nil, huma.Error409Conflict("only a failed operation can be dismissed")
		}
		return nil, nil
	})
}

func operationResponses(snapshots []service.OperationSnapshot) []OperationResponse {
	result := make([]OperationResponse, 0, len(snapshots))
	for _, snapshot := range snapshots {
		result = append(result, operationResponse(snapshot))
	}
	return result
}

func operationResponse(snapshot service.OperationSnapshot) OperationResponse {
	return OperationResponse{
		ID: snapshot.ID, Kind: snapshot.Kind, State: snapshot.State, Stage: snapshot.Stage,
		TargetInstallationID: snapshot.TargetInstallationID, TargetSourceRootID: snapshot.TargetSourceRootID,
		TargetSourceLocationID: snapshot.TargetSourceLocationID,
		TargetIdentity:         snapshot.TargetIdentity,
		TargetLabel:            snapshot.TargetLabel,
		TargetRef:              snapshot.TargetRef,
		MatchingRecheckMember:  snapshot.MatchingRecheckMember,
		BytesCompleted:         snapshot.BytesCompleted, BytesTotal: snapshot.BytesTotal,
		SafeError: snapshot.SafeError, CreatedAt: snapshot.CreatedAt,
		StartedAt: snapshot.StartedAt, FinishedAt: snapshot.FinishedAt, UpdatedAt: snapshot.UpdatedAt,
	}
}

func operationOutput(snapshot service.OperationSnapshot) *OperationOutput {
	return &OperationOutput{Body: operationResponse(snapshot)}
}
