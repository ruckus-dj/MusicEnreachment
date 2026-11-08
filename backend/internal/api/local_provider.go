package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type localProviderListInput struct {
	Query    string `query:"q"`
	Provider string `query:"provider" default:"local"`
	Limit    int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
}

type localProviderIDInput struct {
	ID string `path:"id"`
}
type localProviderCreditInput struct {
	ArtistID   uuid.UUID `json:"artist_id"`
	Name       string    `json:"credit_name"`
	JoinPhrase string    `json:"join_phrase"`
}
type localProviderTrackInput struct {
	ID                 uuid.UUID `json:"id,omitempty"`
	RecordingID        uuid.UUID `json:"recording_id"`
	TitleOverride      *string   `json:"title_override,omitempty"`
	DurationOverrideMS *int64    `json:"duration_override_ms,omitempty"`
}
type localProviderWrite struct {
	Name              string                     `json:"name,omitempty"`
	SortName          *string                    `json:"sort_name,omitempty"`
	Title             string                     `json:"title,omitempty"`
	ReleaseGroupTitle string                     `json:"release_group_title,omitempty"`
	DurationMS        *int64                     `json:"duration_ms,omitempty"`
	PrimaryType       *string                    `json:"release_group_primary_type,omitempty"`
	SecondaryTypes    []string                   `json:"release_group_secondary_types,omitempty"`
	Language          *string                    `json:"language,omitempty"`
	Script            *string                    `json:"script,omitempty"`
	Genres            []service.GenreCatalogItem `json:"genres,omitempty"`
	Tags              []string                   `json:"tags,omitempty"`
	Credits           []localProviderCreditInput `json:"credits,omitempty"`
	Tracks            []localProviderTrackInput  `json:"tracks,omitempty"`
}
type localProviderCreateInput struct {
	Body localProviderWrite `json:"body"`
}
type localProviderUpdateInput struct {
	ID   string             `path:"id"`
	Body localProviderWrite `json:"body"`
}
type localProviderItemOutput struct {
	Body service.LocalProviderItem `json:"body"`
}
type localProviderPageOutput struct {
	Body service.LocalProviderPage `json:"body"`
}

func registerLocalProviderCatalog(api huma.API, catalog *service.LocalProviderCatalog) {
	for _, route := range []struct{ kind, plural string }{{"artist", "artists"}, {"recording", "recordings"}, {"release", "releases"}} {
		kind, path := route.kind, "/provider-catalog/"+route.plural
		huma.Register(api, huma.Operation{OperationID: "list-local-provider-" + route.plural, Method: http.MethodGet, Path: path, Summary: "Search provider catalog records", Tags: []string{"Provider Catalog"}}, func(ctx context.Context, input *localProviderListInput) (*localProviderPageOutput, error) {
			if catalog == nil {
				return nil, huma.Error503ServiceUnavailable("provider catalog is unavailable")
			}
			page, err := catalog.List(ctx, kind, input.Query, input.Provider, input.Limit, input.Offset)
			if err != nil {
				return nil, localProviderError(err)
			}
			return &localProviderPageOutput{Body: page}, nil
		})
		huma.Register(api, huma.Operation{OperationID: "create-local-provider-" + route.kind, Method: http.MethodPost, Path: path, Summary: "Create a provider catalog record", Tags: []string{"Provider Catalog"}}, func(ctx context.Context, input *localProviderCreateInput) (*localProviderItemOutput, error) {
			if catalog == nil {
				return nil, huma.Error503ServiceUnavailable("provider catalog is unavailable")
			}
			item, err := catalog.Create(ctx, kind, localProviderWriteItem(input.Body))
			if err != nil {
				return nil, localProviderError(err)
			}
			return &localProviderItemOutput{Body: item}, nil
		})
		huma.Register(api, huma.Operation{OperationID: "read-local-provider-" + route.kind, Method: http.MethodGet, Path: path + "/{id}", Summary: "Read a provider catalog record", Tags: []string{"Provider Catalog"}}, func(ctx context.Context, input *localProviderIDInput) (*localProviderItemOutput, error) {
			if catalog == nil {
				return nil, huma.Error503ServiceUnavailable("provider catalog is unavailable")
			}
			id, err := uuid.Parse(input.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("invalid catalog item ID")
			}
			item, err := catalog.Read(ctx, kind, id)
			if err != nil {
				return nil, localProviderError(err)
			}
			return &localProviderItemOutput{Body: item}, nil
		})
		huma.Register(api, huma.Operation{OperationID: "update-local-provider-" + route.kind, Method: http.MethodPut, Path: path + "/{id}", Summary: "Update a provider catalog record", Tags: []string{"Provider Catalog"}}, func(ctx context.Context, input *localProviderUpdateInput) (*localProviderItemOutput, error) {
			if catalog == nil {
				return nil, huma.Error503ServiceUnavailable("provider catalog is unavailable")
			}
			id, err := uuid.Parse(input.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("invalid catalog item ID")
			}
			item, err := catalog.Update(ctx, kind, id, localProviderWriteItem(input.Body))
			if err != nil {
				return nil, localProviderError(err)
			}
			return &localProviderItemOutput{Body: item}, nil
		})
	}
}

func localProviderWriteItem(input localProviderWrite) service.LocalProviderItem {
	item := service.LocalProviderItem{Name: input.Name, SortName: input.SortName, Title: input.Title, ReleaseGroupTitle: input.ReleaseGroupTitle, DurationMS: input.DurationMS, PrimaryType: input.PrimaryType, SecondaryTypes: input.SecondaryTypes, Language: input.Language, Script: input.Script, Genres: service.LocalProviderGenreReferences(input.Genres), Tags: input.Tags}
	for _, credit := range input.Credits {
		item.Credits = append(item.Credits, service.LocalProviderCredit{ArtistID: credit.ArtistID, Name: credit.Name, JoinPhrase: credit.JoinPhrase})
	}
	for _, track := range input.Tracks {
		item.Tracks = append(item.Tracks, service.LocalProviderTrack{ID: track.ID, RecordingID: track.RecordingID, TitleOverride: track.TitleOverride, DurationOverrideMS: track.DurationOverrideMS})
	}
	return item
}

func localProviderError(err error) error {
	switch {
	case errors.Is(err, service.ErrLocalProviderInvalid):
		return huma.Error400BadRequest("invalid provider catalog item")
	case errors.Is(err, sql.ErrNoRows):
		return huma.Error404NotFound("provider catalog item not found")
	default:
		return huma.Error500InternalServerError("provider catalog request failed")
	}
}
