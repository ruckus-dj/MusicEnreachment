package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type genreCatalogStatusOutput struct {
	Body service.GenreCatalogStatus `json:"body"`
}

type genreCatalogSearchInput struct {
	Query string `query:"query" required:"true" minLength:"1" pattern:"\\S"`
	Limit int    `query:"limit" default:"20" minimum:"1" maximum:"100"`
}

type genreCatalogSearchBody struct {
	Items []service.GenreCatalogItem `json:"items"`
}

type genreCatalogSearchOutput struct {
	Body genreCatalogSearchBody `json:"body"`
}

type genreCatalogRefreshOutput struct {
	Body OperationResponse `json:"body"`
}

func registerGenreCatalog(api huma.API, catalog *service.GenreCatalogService) {
	huma.Register(api, huma.Operation{
		OperationID: "get-genre-catalog", Method: http.MethodGet, Path: "/genres/catalog",
		Summary: "Get the current MusicBrainz genre catalog", Tags: []string{"Genres"},
	}, func(ctx context.Context, _ *struct{}) (*genreCatalogStatusOutput, error) {
		if catalog == nil {
			return nil, huma.Error503ServiceUnavailable("genre catalog is unavailable")
		}
		status, err := catalog.Status(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read genre catalog")
		}
		return &genreCatalogStatusOutput{Body: status}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "search-genre-catalog", Method: http.MethodGet, Path: "/genres/catalog/search",
		Summary: "Search the current MusicBrainz genre catalog", Tags: []string{"Genres"},
	}, func(ctx context.Context, input *genreCatalogSearchInput) (*genreCatalogSearchOutput, error) {
		if catalog == nil {
			return nil, huma.Error503ServiceUnavailable("genre catalog is unavailable")
		}
		items, err := catalog.Search(ctx, input.Query, input.Limit)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to search genre catalog")
		}
		return &genreCatalogSearchOutput{Body: genreCatalogSearchBody{Items: items}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "refresh-genre-catalog", Method: http.MethodPost, Path: "/genres/catalog/refresh",
		Summary: "Queue a MusicBrainz genre catalog refresh", Tags: []string{"Genres"},
	}, func(ctx context.Context, _ *struct{}) (*genreCatalogRefreshOutput, error) {
		if catalog == nil {
			return nil, huma.Error503ServiceUnavailable("genre catalog is unavailable")
		}
		snapshot, err := catalog.StartRefresh(ctx)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("failed to queue genre catalog refresh")
		}
		return &genreCatalogRefreshOutput{Body: operationResponse(snapshot)}, nil
	})
}
