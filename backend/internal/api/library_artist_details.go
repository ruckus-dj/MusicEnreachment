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
	huma.Register(api, huma.Operation{OperationID: "get-library-artist-details", Method: http.MethodGet, Path: "/library/artists/{artist_id}/details", Summary: "Read local artist details", Tags: []string{"Library"}}, func(ctx context.Context, input *libraryArtistDetailsInput) (*libraryArtistDetailsOutput, error) {
		artistID, err := uuid.Parse(input.ArtistID)
		if err != nil || artistID == uuid.Nil {
			return nil, huma.Error400BadRequest("library artist ID is invalid")
		}
		return read(ctx, artistID)
	})
}
