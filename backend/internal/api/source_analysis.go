package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type AnalyzeSourceLocationInput struct {
	RootID     uuid.UUID `path:"source_id"`
	LocationID uuid.UUID `path:"location_id"`
	Body       AnalyzeSourceLocationBody
}

// AnalyzeSourceLocationBody carries only the identity the operator observed when
// the location was read. There is no path and no tool input: the server resolves
// the file below the registered root and the managed FFmpeg from the active
// installation, so a client cannot ask for an arbitrary file or executable.
type AnalyzeSourceLocationBody struct {
	ExpectedSizeBytes int64     `json:"expected_size_bytes" minimum:"0"`
	ExpectedMtime     time.Time `json:"expected_mtime"`
}

// registerSourceAnalysis binds the analysis start of one source location. It is
// a mutation, so it uses the same Setup and platform gates as a scan start; the
// service re-checks the root, the location identity and the managed installation
// under the database locks the enqueue runs.
func registerSourceAnalysis(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "analyze-source-location", Method: http.MethodPost,
		Path:    "/sources/{source_id}/locations/{location_id}/analyze",
		Summary: "Analyze one source location", Tags: []string{"Sources"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *AnalyzeSourceLocationInput) (*OperationOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.SourceAnalysis == nil || dependencies.Operations == nil {
			return nil, huma.Error503ServiceUnavailable("source analysis is unavailable")
		}
		operation, err := dependencies.SourceAnalysis.Start(ctx, service.SourceAnalysisStartRequest{
			RootID: input.RootID, LocationID: input.LocationID,
			ExpectedSizeBytes: input.Body.ExpectedSizeBytes, ExpectedMtime: input.Body.ExpectedMtime,
		})
		if err != nil {
			return nil, sourceAnalysisStartError(err)
		}
		snapshot, err := dependencies.Operations.Snapshot(ctx, operation.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read the started analysis")
		}
		return operationOutput(snapshot), nil
	})
}

// sourceAnalysisStartError maps the typed refusals of the analysis start. A
// conflict keeps the location and its stored result untouched, and only a
// missing dependency or an unusable platform/tool is a 503.
func sourceAnalysisStartError(err error) error {
	switch {
	case service.IsSourceRootNotFound(err):
		return huma.Error404NotFound("source root not found")
	case errors.Is(err, service.ErrSourceLocationNotFound):
		return huma.Error404NotFound("source location not found")
	case errors.Is(err, service.ErrSourceAnalysisNotReady):
		return huma.Error503ServiceUnavailable("source analysis requires a completed setup on a supported instance platform")
	case errors.Is(err, service.ErrSourceAnalysisToolUnavailable):
		return huma.Error503ServiceUnavailable("source analysis requires a ready managed ffmpeg installation")
	case errors.Is(err, service.ErrSourceAnalysisStale),
		errors.Is(err, service.ErrSourceAnalysisDisabled),
		errors.Is(err, service.ErrSourceAnalysisBusy),
		errors.Is(err, service.ErrSourceAnalysisMoveActive),
		errors.Is(err, service.ErrSourceAnalysisToolChanged):
		return huma.Error409Conflict("the source location cannot be analyzed in its current state")
	default:
		return huma.Error500InternalServerError("analysis could not be started")
	}
}
