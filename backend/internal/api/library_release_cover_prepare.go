package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryReleaseCoverPrepareInput struct {
	ReleaseID string `path:"release_id"`
	Body      struct {
		PositionID uuid.UUID `json:"position_id"`
	}
}

// libraryReleaseCoverPrepareService is the narrow preparation boundary. It
// deliberately does not expose publication admission or cover authority writes.
type libraryReleaseCoverPrepareService interface {
	PrepareFirstCover(context.Context, uuid.UUID, uuid.UUID) error
}

func registerLibraryReleaseCoverPrepare(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "prepare-library-release-cover", Method: http.MethodPost,
		Path:    "/library/releases/{release_id}/cover/prepare",
		Summary: "Prepare automatic cover authority for an eligible first publication",
		Tags:    []string{"Library"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *libraryReleaseCoverPrepareInput) (*struct{}, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.LibraryPublications == nil {
			return nil, huma.Error503ServiceUnavailable("library publication service is unavailable")
		}
		prepare, ok := dependencies.LibraryPublications.(libraryReleaseCoverPrepareService)
		if !ok {
			return nil, huma.Error503ServiceUnavailable("library release cover preparation is unavailable")
		}
		releaseID, err := uuid.Parse(input.ReleaseID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid release ID")
		}
		if input.Body.PositionID == uuid.Nil {
			return nil, huma.Error400BadRequest("position_id is required")
		}
		if err := prepare.PrepareFirstCover(ctx, releaseID, input.Body.PositionID); err != nil {
			switch {
			case errors.Is(err, persistence.ErrPublicationPositionNotFound):
				return nil, huma.Error404NotFound("library publication position not found")
			case errors.Is(err, persistence.ErrPublicationAdmissionConflict):
				return nil, huma.Error409Conflict("first publication cover preparation conflicts with the current position state")
			case errors.Is(err, service.ErrPublicationArtwork):
				return nil, huma.Error503ServiceUnavailable("release artwork is still being prepared; retry shortly")
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				return nil, huma.Error503ServiceUnavailable("library release cover preparation was interrupted")
			default:
				return nil, huma.Error500InternalServerError("failed to prepare library release cover")
			}
		}
		return nil, nil
	})
}
