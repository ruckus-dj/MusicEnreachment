package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type RetrySourceAnalysisStepInput struct {
	RootID     uuid.UUID `path:"source_id"`
	LocationID uuid.UUID `path:"location_id"`
	Body       RetrySourceAnalysisStepBody
}

type RetrySourceAnalysisStepBody struct {
	Step              string    `json:"step" enum:"sha256,probe,fingerprint"`
	ExpectedSizeBytes int64     `json:"expected_size_bytes" minimum:"0"`
	ExpectedMtime     time.Time `json:"expected_mtime"`
}

type RerunSourceFingerprintInput struct {
	RootID     uuid.UUID `path:"source_id"`
	LocationID uuid.UUID `path:"location_id"`
	Body       RerunSourceFingerprintBody
}

type RerunSourceFingerprintBody struct {
	ExpectedSizeBytes int64     `json:"expected_size_bytes" minimum:"0"`
	ExpectedMtime     time.Time `json:"expected_mtime"`
}

func registerSourceAnalysis(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "retry-source-analysis-step", Method: http.MethodPost,
		Path:    "/sources/{source_id}/locations/{location_id}/retry",
		Summary: "Retry one failed source analysis step", Tags: []string{"Sources"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *RetrySourceAnalysisStepInput) (*OperationOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.SourceAnalysis == nil || dependencies.Operations == nil {
			return nil, huma.Error503ServiceUnavailable("source analysis is unavailable")
		}
		operation, err := dependencies.SourceAnalysis.RetryStep(ctx, service.SourceAnalysisStepRequest{
			RootID: input.RootID, LocationID: input.LocationID,
			Step:              persistence.SourceStepName(input.Body.Step),
			ExpectedSizeBytes: input.Body.ExpectedSizeBytes, ExpectedMtime: input.Body.ExpectedMtime,
		})
		if err != nil {
			return nil, sourceAnalysisActionError(err)
		}
		return sourceAnalysisOperationOutput(ctx, dependencies.Operations, operation.ID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "rerun-source-fingerprint", Method: http.MethodPost,
		Path:    "/sources/{source_id}/locations/{location_id}/fingerprint/rerun",
		Summary: "Rerun source fingerprint calculation", Tags: []string{"Sources"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *RerunSourceFingerprintInput) (*OperationOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.SourceAnalysis == nil || dependencies.Operations == nil {
			return nil, huma.Error503ServiceUnavailable("source analysis is unavailable")
		}
		operation, err := dependencies.SourceAnalysis.RerunFingerprint(ctx, service.SourceAnalysisRerunRequest{
			RootID: input.RootID, LocationID: input.LocationID,
			ExpectedSizeBytes: input.Body.ExpectedSizeBytes, ExpectedMtime: input.Body.ExpectedMtime,
		})
		if err != nil {
			return nil, sourceAnalysisActionError(err)
		}
		return sourceAnalysisOperationOutput(ctx, dependencies.Operations, operation.ID)
	})
}

func sourceAnalysisOperationOutput(ctx context.Context, operations *service.Operations, id uuid.UUID) (*OperationOutput, error) {
	snapshot, err := operations.Snapshot(ctx, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to read the queued source analysis")
	}
	return operationOutput(snapshot), nil
}

func sourceAnalysisActionError(err error) error {
	switch {
	case service.IsSourceRootNotFound(err):
		return huma.Error404NotFound("source root not found")
	case errors.Is(err, service.ErrSourceLocationNotFound):
		return huma.Error404NotFound("source location not found")
	case errors.Is(err, service.ErrSourceAnalysisNotReady):
		return huma.Error503ServiceUnavailable("source analysis requires completed setup on a supported instance platform")
	case errors.Is(err, service.ErrSourceAnalysisToolUnavailable):
		return huma.Error503ServiceUnavailable("the required managed source-analysis tool is unavailable")
	case errors.Is(err, service.ErrSourceAnalysisStale),
		errors.Is(err, service.ErrSourceAnalysisDisabled),
		errors.Is(err, service.ErrSourceAnalysisBusy),
		errors.Is(err, service.ErrSourceAnalysisMoveActive),
		errors.Is(err, service.ErrSourceAnalysisToolChanged):
		return huma.Error409Conflict("the source analysis step cannot run in its current state")
	default:
		return huma.Error500InternalServerError("source analysis could not be queued")
	}
}
