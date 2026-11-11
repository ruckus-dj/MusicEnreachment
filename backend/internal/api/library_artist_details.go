package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryArtistDetailsInput struct {
	ArtistID string `path:"artist_id"`
}

type libraryArtistDetailsPatchInput struct {
	ArtistID string `path:"artist_id"`
	Body     struct {
		ExpectedRevision int64   `json:"expected_revision"`
		Name             *string `json:"name,omitempty"`
		Disambiguation   *string `json:"disambiguation,omitempty"`
	} `json:"body"`
}

type libraryArtistDetailsOutput struct {
	Body service.LibraryArtistDetails `json:"body"`
}

func registerLibraryArtistDetails(api huma.API, metadata *service.LibraryMetadataService) {
	read := func(ctx context.Context, artistID uuid.UUID) (*libraryArtistDetailsOutput, error) {
		if metadata == nil {
			return nil, huma.Error503ServiceUnavailable("library artist details are unavailable")
		}
		result, err := metadata.ReadArtistDetails(ctx, artistID)
		if errors.Is(err, service.ErrLibraryMetadataNotFound) {
			return nil, huma.Error404NotFound("library artist not found")
		}
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("library artist details are unavailable")
		}
		return &libraryArtistDetailsOutput{Body: result}, nil
	}
	update := func(ctx context.Context, input *libraryArtistDetailsPatchInput) (*libraryArtistDetailsOutput, error) {
		if metadata == nil {
			return nil, huma.Error503ServiceUnavailable("library artist details are unavailable")
		}
		artistID, err := uuid.Parse(input.ArtistID)
		if err != nil || artistID == uuid.Nil {
			return nil, huma.Error400BadRequest("library artist ID is invalid")
		}
		result, err := metadata.UpdateArtistDetails(ctx, artistID, input.Body.ExpectedRevision, input.Body.Name, input.Body.Disambiguation)
		switch {
		case errors.Is(err, service.ErrLibraryMetadataInvalidRequest):
			return nil, huma.Error400BadRequest("library artist details request is invalid")
		case errors.Is(err, service.ErrLibraryMetadataNotFound):
			return nil, huma.Error404NotFound("library artist not found")
		case errors.Is(err, service.ErrLibraryMetadataConflict):
			return nil, huma.Error409Conflict("library artist details revision is stale")
		case err != nil:
			return nil, huma.Error503ServiceUnavailable("library artist details are unavailable")
		default:
			return &libraryArtistDetailsOutput{Body: result}, nil
		}
	}

	huma.Register(api, huma.Operation{OperationID: "get-library-artist-details", Method: http.MethodGet, Path: "/library/artists/{artist_id}/details", Summary: "Read local artist details", Tags: []string{"Library"}}, func(ctx context.Context, input *libraryArtistDetailsInput) (*libraryArtistDetailsOutput, error) {
		artistID, err := uuid.Parse(input.ArtistID)
		if err != nil || artistID == uuid.Nil {
			return nil, huma.Error400BadRequest("library artist ID is invalid")
		}
		return read(ctx, artistID)
	})
	huma.Register(api, huma.Operation{OperationID: "patch-library-artist-details", Method: http.MethodPatch, Path: "/library/artists/{artist_id}/details", Summary: "Update local artist details", Tags: []string{"Library"}}, update)
}
