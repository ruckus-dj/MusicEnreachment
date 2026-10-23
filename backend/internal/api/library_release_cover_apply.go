package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryReleaseCoverApplyInput struct {
	ReleaseID string `path:"release_id"`
	Body      struct {
		ExpectedEpoch              uuid.UUID `json:"expected_epoch"`
		ExpectedRevision           int64     `json:"expected_revision" minimum:"1"`
		ExpectedPendingGeneration  uuid.UUID `json:"expected_pending_generation,omitempty"`
		ExpectedSelectedGeneration uuid.UUID `json:"expected_selected_generation,omitempty"`
	}
}

type libraryReleaseCoverBatchOutput struct {
	Body service.ReleaseCoverBatch `json:"body"`
}

func registerLibraryReleaseCoverApply(api huma.API, covers *service.ReleaseCovers, operations *service.Operations) {
	register := func(path, operationID string, retry bool) {
		huma.Register(api, huma.Operation{OperationID: operationID, Method: http.MethodPost, Path: path, Summary: "Apply release cover to published positions", Tags: []string{"Library"}, DefaultStatus: http.StatusAccepted}, func(ctx context.Context, input *libraryReleaseCoverApplyInput) (*libraryReleaseCoverBatchOutput, error) {
			if covers == nil || operations == nil {
				return nil, huma.Error503ServiceUnavailable("library release cover service is unavailable")
			}
			releaseID, err := uuid.Parse(input.ReleaseID)
			if err != nil {
				return nil, huma.Error400BadRequest("invalid release ID")
			}
			request := service.ReleaseCoverApplyRequest{ExpectedEpoch: input.Body.ExpectedEpoch, ExpectedRevision: input.Body.ExpectedRevision, ExpectedPendingGeneration: input.Body.ExpectedPendingGeneration, ExpectedSelectedGeneration: input.Body.ExpectedSelectedGeneration}
			var batch service.ReleaseCoverBatch
			if retry {
				batch, err = covers.Retry(ctx, releaseID, request)
			} else {
				batch, err = covers.Apply(ctx, releaseID, request)
			}
			if err != nil {
				return nil, libraryReleaseCoverApplyError(err)
			}
			for _, admitted := range batch.Operations {
				operations.Notify(admitted.OperationID)
			}
			return &libraryReleaseCoverBatchOutput{Body: batch}, nil
		})
	}
	register("/library/releases/{release_id}/cover/apply", "apply-library-release-cover", false)
	register("/library/releases/{release_id}/cover/retry", "retry-library-release-cover", true)
}

func libraryReleaseCoverApplyError(err error) error {
	switch {
	case service.IsLibraryReleaseCoverNotFound(err):
		return huma.Error404NotFound("library release or publication position not found")
	case service.IsLibraryReleaseCoverConflict(err):
		return huma.Error409Conflict("library release cover or publication changed; refresh and retry")
	case service.IsLibraryReleaseCoverInvalid(err):
		return huma.Error422UnprocessableEntity("library release cover request is invalid")
	default:
		return huma.Error500InternalServerError("failed to admit release cover publication")
	}
}
