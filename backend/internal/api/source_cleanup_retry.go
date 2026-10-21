package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func registerSourceCleanupRetry(api huma.API, retries *service.SourceCleanupRetries, operations *service.Operations) {
	huma.Register(api, huma.Operation{
		OperationID: "retry-publication-source-cleanup", Method: http.MethodPost,
		Path:    "/operations/{operation_id}/source-cleanup/retry",
		Summary: "Explicitly retry a failed publication source cleanup",
		Tags:    []string{"Operations"}, DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *OperationInput) (*OperationOutput, error) {
		if retries == nil || operations == nil {
			return nil, huma.Error503ServiceUnavailable("publication source cleanup retry is unavailable")
		}
		result, err := retries.Retry(ctx, input.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error404NotFound("original publication source cleanup operation not found")
			}
			return nil, huma.Error500InternalServerError("failed to retry publication source cleanup")
		}
		if len(result.Blockers) > 0 {
			return nil, huma.Error409Conflict("publication source cleanup retry blocked: " + strings.Join(result.Blockers, "; "))
		}
		if result.OperationID == uuid.Nil {
			return nil, huma.Error500InternalServerError("cleanup retry did not return an operation")
		}
		snapshot, err := operations.Snapshot(ctx, result.OperationID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read accepted cleanup retry operation")
		}
		return operationOutput(snapshot), nil
	})
}
