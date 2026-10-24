package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryReleaseCoverInput struct {
	ReleaseID string `path:"release_id"`
}

type libraryReleaseCoverImageInput struct {
	ReleaseID  string `path:"release_id"`
	Generation string `path:"generation"`
}

type libraryReleaseCoverSaveBody struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Image            []byte `json:"image"`
	MIME             string `json:"mime"`
}

type libraryReleaseCoverSaveInput struct {
	ReleaseID string `path:"release_id"`
	Body      libraryReleaseCoverSaveBody
}

type libraryReleaseCoverOutput struct {
	Body service.ReleaseCoverSummary `json:"body"`
}

type libraryReleaseCoverImageOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

func registerLibraryReleaseCover(api huma.API, covers *service.ReleaseCovers) {
	huma.Register(api, huma.Operation{
		OperationID: "get-library-release-cover", Method: http.MethodGet,
		Path: "/library/releases/{release_id}/cover", Summary: "Read release cover authority", Tags: []string{"Library"},
	}, func(ctx context.Context, input *libraryReleaseCoverInput) (*libraryReleaseCoverOutput, error) {
		if covers == nil {
			return nil, huma.Error503ServiceUnavailable("library release cover service is unavailable")
		}
		releaseID, err := uuid.Parse(input.ReleaseID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid release ID")
		}
		result, err := covers.Read(ctx, releaseID)
		if err != nil {
			return nil, libraryReleaseCoverError(err)
		}
		return &libraryReleaseCoverOutput{Body: result}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "save-library-release-cover", Method: http.MethodPatch,
		Path: "/library/releases/{release_id}/cover", Summary: "Save a pending release cover", Tags: []string{"Library"},
		// Encoded artwork legitimately exceeds Huma's 1 MiB default; the service
		// still enforces the 10000-pixel decoded dimension limit.
		MaxBodyBytes: -1,
	}, func(ctx context.Context, input *libraryReleaseCoverSaveInput) (*libraryReleaseCoverOutput, error) {
		if covers == nil {
			return nil, huma.Error503ServiceUnavailable("library release cover service is unavailable")
		}
		releaseID, err := uuid.Parse(input.ReleaseID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid release ID")
		}
		result, err := covers.Save(ctx, releaseID, input.Body.ExpectedRevision, input.Body.Image, input.Body.MIME)
		if err != nil {
			return nil, libraryReleaseCoverError(err)
		}
		return &libraryReleaseCoverOutput{Body: result}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-library-release-cover-image", Method: http.MethodGet,
		Path: "/library/releases/{release_id}/cover/images/{generation}", Summary: "Read a selected or pending release cover image", Tags: []string{"Library"},
		Responses: map[string]*huma.Response{"200": {Description: "Release cover image", Content: map[string]*huma.MediaType{"image/png": {}, "image/jpeg": {}}}},
	}, func(ctx context.Context, input *libraryReleaseCoverImageInput) (*libraryReleaseCoverImageOutput, error) {
		if covers == nil {
			return nil, huma.Error503ServiceUnavailable("library release cover service is unavailable")
		}
		releaseID, err := uuid.Parse(input.ReleaseID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid release ID")
		}
		generation, err := uuid.Parse(input.Generation)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid cover generation")
		}
		image, err := covers.Image(ctx, releaseID, generation)
		if err != nil {
			return nil, libraryReleaseCoverError(err)
		}
		return &libraryReleaseCoverImageOutput{ContentType: image.MIME, Body: image.Data}, nil
	})
}

func libraryReleaseCoverError(err error) error {
	switch {
	case service.IsLibraryReleaseCoverNotFound(err):
		return huma.Error404NotFound("library release cover not found")
	case service.IsLibraryReleaseCoverConflict(err):
		return huma.Error409Conflict("library release cover revision conflict")
	case service.IsLibraryReleaseCoverInvalid(err):
		return huma.Error422UnprocessableEntity("library release cover image is invalid")
	default:
		return huma.Error500InternalServerError("failed to process library release cover")
	}
}
