package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type SourceArtifactCleanupCandidatesOutput struct {
	Body SourceArtifactCleanupCandidatesBody `json:"body"`
}

type SourceArtifactCleanupCandidatesBody struct {
	Count      int                                              `json:"count"`
	Candidates []service.SourceAnalysisArtifactCleanupCandidate `json:"candidates"`
}

type SourceArtifactCleanupInput struct {
	Body SourceArtifactCleanupBody
}

type SourceArtifactCleanupBody struct {
	ArtifactIDs []uuid.UUID `json:"artifact_ids" minItems:"1" maxItems:"1000"`
}

func registerSourceArtifactCleanup(api huma.API, cleanup *service.SourceAnalysisArtifactCleanup, operations *service.Operations, setup *service.SetupService) {
	huma.Register(api, huma.Operation{
		OperationID: "list-source-analysis-artifact-cleanup-candidates", Method: http.MethodGet,
		Path: "/source-analysis/artifacts/cleanup", Summary: "List staged analysis artifacts eligible for explicit cleanup",
		Tags: []string{"Source Analysis"},
	}, func(ctx context.Context, _ *struct{}) (*SourceArtifactCleanupCandidatesOutput, error) {
		if cleanup == nil {
			return nil, huma.Error503ServiceUnavailable("source artifact cleanup is unavailable")
		}
		candidates, err := cleanup.Candidates(ctx, 1000)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list cleanup candidates")
		}
		return &SourceArtifactCleanupCandidatesOutput{Body: SourceArtifactCleanupCandidatesBody{Count: len(candidates), Candidates: candidates}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "cleanup-source-analysis-artifacts", Method: http.MethodPost,
		Path: "/source-analysis/artifacts/cleanup", Summary: "Explicitly clean selected staged analysis artifacts",
		Tags: []string{"Source Analysis"}, DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *SourceArtifactCleanupInput) (*OperationOutput, error) {
		if err := requireSetupComplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if cleanup == nil {
			return nil, huma.Error503ServiceUnavailable("source artifact cleanup is unavailable")
		}
		if operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		operation, err := cleanup.Admit(ctx, input.Body.ArtifactIDs)
		if err != nil {
			return nil, huma.Error409Conflict("selected artifacts could not be admitted for cleanup")
		}
		snapshot, err := operations.Snapshot(ctx, operation.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read admitted cleanup operation")
		}
		return operationOutput(snapshot), nil
	})
}
