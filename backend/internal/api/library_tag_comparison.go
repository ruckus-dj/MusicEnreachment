package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryTagComparisonInput struct {
	ReleaseID  string `path:"release_id" format:"uuid"`
	PositionID string `path:"position_id" format:"uuid"`
}

type LibraryTagComparisonProviderResponse struct {
	Name   string              `json:"name,omitempty"`
	Status string              `json:"status" enum:"unlinked,unavailable,saved"`
	Tags   map[string][]string `json:"tags,omitempty"`
}

type LibraryTagComparisonResponse struct {
	Provider  LibraryTagComparisonProviderResponse `json:"provider"`
	Overrides map[string][]string                  `json:"overrides"`
}

type libraryTagComparisonOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         LibraryTagComparisonResponse
}

func registerLibraryTagComparison(api huma.API, comparisons *service.LibraryTagComparisons) {
	huma.Register(api, huma.Operation{
		OperationID: "get-library-position-tag-comparison", Method: http.MethodGet,
		Path:    "/library/releases/{release_id}/positions/{position_id}/tag-comparison",
		Summary: "Read saved provider tags and explicit local overrides for a position", Tags: []string{"Library"},
	}, func(ctx context.Context, input *libraryTagComparisonInput) (*libraryTagComparisonOutput, error) {
		releaseID, err := uuid.Parse(input.ReleaseID)
		if err != nil || releaseID == uuid.Nil {
			return nil, huma.Error400BadRequest("invalid release ID")
		}
		positionID, err := uuid.Parse(input.PositionID)
		if err != nil || positionID == uuid.Nil {
			return nil, huma.Error400BadRequest("invalid position ID")
		}
		evidence, err := comparisons.Read(ctx, releaseID, positionID)
		switch {
		case errors.Is(err, service.ErrLibraryPositionNotFound):
			return nil, huma.Error404NotFound("library release position not found")
		case err != nil:
			return nil, huma.Error503ServiceUnavailable("position tag comparison is unavailable")
		default:
			return &libraryTagComparisonOutput{CacheControl: "no-store", Body: LibraryTagComparisonResponse{
				Provider: LibraryTagComparisonProviderResponse{
					Name: evidence.ProviderName, Status: evidence.ProviderStatus, Tags: evidence.ProviderTags,
				},
				Overrides: evidence.Overrides,
			}}, nil
		}
	})
}
