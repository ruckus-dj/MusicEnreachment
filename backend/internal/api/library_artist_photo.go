package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryArtistPhotoInput struct {
	ArtistID string `path:"artist_id"`
}

type libraryArtistPhotoOutput struct {
	Body service.LibraryArtistPhotoResponse `json:"body"`
}

type libraryArtistPhotoImageInput struct {
	ArtistID string `path:"artist_id"`
}

type libraryArtistPhotoImageOutput struct {
	CacheControl       string `header:"Cache-Control"`
	ContentType        string `header:"Content-Type"`
	XContentTypeOption string `header:"X-Content-Type-Options"`
	Body               []byte
}

func registerLibraryArtistPhoto(api huma.API, photos *service.LibraryArtistPhotos) {
	huma.Register(api, huma.Operation{
		OperationID: "get-library-artist-photo", Method: http.MethodGet,
		Path: "/library/artists/{artist_id}/photo", Summary: "Read an optional artist photo", Tags: []string{"Library"},
	}, func(ctx context.Context, input *libraryArtistPhotoInput) (*libraryArtistPhotoOutput, error) {
		if photos == nil {
			return nil, huma.Error503ServiceUnavailable("library artist photos are unavailable")
		}
		artistID, err := uuid.Parse(input.ArtistID)
		if err != nil || artistID == uuid.Nil {
			return nil, huma.Error400BadRequest("library artist ID is invalid")
		}
		result, err := photos.Read(ctx, artistID)
		switch {
		case err == nil:
			if result.Photo != nil {
				result.Photo.ImageURL = libraryArtistPhotoImageURL(artistID)
			}
			return &libraryArtistPhotoOutput{Body: result}, nil
		case errors.Is(err, service.ErrLibraryArtistNotFound):
			return nil, huma.Error404NotFound("library artist not found")
		case service.IsLibraryArtistPhotoUnavailable(err):
			return nil, huma.Error503ServiceUnavailable("artist photo is temporarily unavailable")
		default:
			return nil, huma.Error503ServiceUnavailable("library artist photo is unavailable")
		}
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-library-artist-photo-image", Method: http.MethodGet,
		Path: "/library/artists/{artist_id}/photo/image", Summary: "Read a saved artist photo thumbnail", Tags: []string{"Library"},
		Responses: map[string]*huma.Response{"200": {Description: "Artist photo thumbnail", Content: map[string]*huma.MediaType{
			"image/png": {}, "image/jpeg": {}, "image/webp": {}, "image/gif": {},
		}}},
	}, func(ctx context.Context, input *libraryArtistPhotoImageInput) (*libraryArtistPhotoImageOutput, error) {
		if photos == nil {
			return nil, huma.Error503ServiceUnavailable("library artist photos are unavailable")
		}
		artistID, err := uuid.Parse(input.ArtistID)
		if err != nil || artistID == uuid.Nil {
			return nil, huma.Error400BadRequest("library artist ID is invalid")
		}
		image, err := photos.Image(ctx, artistID)
		switch {
		case err == nil:
			return &libraryArtistPhotoImageOutput{
				CacheControl: "private, max-age=300", ContentType: image.MIME,
				XContentTypeOption: "nosniff", Body: image.Data,
			}, nil
		case errors.Is(err, service.ErrLibraryArtistNotFound), service.IsLibraryArtistPhotoNotFound(err):
			return nil, huma.Error404NotFound("artist photo not found")
		case service.IsLibraryArtistPhotoUnavailable(err):
			return nil, huma.Error503ServiceUnavailable("artist photo is temporarily unavailable")
		default:
			return nil, huma.Error503ServiceUnavailable("library artist photo is unavailable")
		}
	})
}

func libraryArtistPhotoImageURL(artistID uuid.UUID) string {
	return "/api/library/artists/" + artistID.String() + "/photo/image"
}
