package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryMusicBrainzRefreshInput struct {
	ReleaseID string `path:"release_id"`
}

func registerLibraryMusicBrainzRefresh(api huma.API, refresh *service.LibraryProviderRefresh, operations *service.Operations) {
	huma.Register(api, huma.Operation{
		OperationID: "refresh-library-release-musicbrainz", Method: http.MethodPost,
		Path:    "/library/releases/{release_id}/musicbrainz-refresh",
		Summary: "Refresh a locally bound release from MusicBrainz", Tags: []string{"Library"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *libraryMusicBrainzRefreshInput) (*OperationOutput, error) {
		if refresh == nil || operations == nil {
			return nil, huma.Error503ServiceUnavailable("library MusicBrainz refresh is unavailable")
		}
		releaseID, err := uuid.Parse(input.ReleaseID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid release ID")
		}
		operationID, err := refresh.Start(ctx, releaseID)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrLibraryProviderRefreshNotFound):
				return nil, huma.Error404NotFound("library release not found")
			case errors.Is(err, service.ErrLibraryProviderRefreshConflict):
				return nil, huma.Error409Conflict("release has no current MusicBrainz binding; refresh the release detail")
			default:
				return nil, huma.Error503ServiceUnavailable("library MusicBrainz refresh could not be admitted")
			}
		}
		snapshot, err := operations.Snapshot(ctx, operationID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read admitted operation")
		}
		operations.Notify(operationID)
		return operationOutput(snapshot), nil
	})
}
