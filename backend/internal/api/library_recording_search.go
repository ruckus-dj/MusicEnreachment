package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryRecordingSearchBody struct {
	Query  string `json:"query" minLength:"1"`
	Offset int    `json:"offset" minimum:"0"`
	Limit  int    `json:"limit" minimum:"1" maximum:"100"`
}

type libraryRecordingSearchStartInput struct {
	Body libraryRecordingSearchBody `json:"body"`
}

type libraryRecordingSearchOutput struct {
	Body OperationResponse `json:"body"`
}

type libraryRecordingSearchResultInput struct {
	OperationID uuid.UUID `path:"operation_id"`
}

type libraryRecordingSearchArtistOutput struct {
	Name             string  `json:"name"`
	JoinPhrase       string  `json:"join_phrase"`
	ProviderArtistID *string `json:"provider_artist_id" nullable:"true"`
}

type libraryRecordingSearchItemOutput struct {
	ProviderID          uuid.UUID                            `json:"provider_id"`
	ProviderSourceID    uuid.UUID                            `json:"provider_source_id"`
	ProviderRecordingID uuid.UUID                            `json:"provider_recording_id"`
	ProviderKey         string                               `json:"provider_key"`
	Title               string                               `json:"title"`
	Artists             []libraryRecordingSearchArtistOutput `json:"artists" nullable:"false"`
	DurationMS          *int64                               `json:"duration_ms" nullable:"true"`
	LocalRecordingID    *string                              `json:"local_recording_id" nullable:"true"`
}

type libraryRecordingSearchResultOutput struct {
	Body struct {
		OperationID           uuid.UUID                          `json:"operation_id"`
		ProviderSourceID      uuid.UUID                          `json:"provider_source_id"`
		ConfigurationIdentity string                             `json:"configuration_identity"`
		Total                 int                                `json:"total"`
		Offset                int                                `json:"offset"`
		Limit                 int                                `json:"limit"`
		Items                 []libraryRecordingSearchItemOutput `json:"items" nullable:"false"`
	} `json:"body"`
}

func registerLibraryRecordingSearch(api huma.API, searches *service.LibraryRecordingSearch, operations *service.Operations) {
	huma.Register(api, huma.Operation{
		OperationID: "start-library-recording-search", Method: http.MethodPost,
		Path: "/library/recordings/search", Summary: "Search local and MusicBrainz recordings", Tags: []string{"Library"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *libraryRecordingSearchStartInput) (*libraryRecordingSearchOutput, error) {
		if searches == nil || operations == nil {
			return nil, huma.Error503ServiceUnavailable("library recording search is unavailable")
		}
		operation, err := searches.Start(ctx, input.Body.Query, input.Body.Offset, input.Body.Limit)
		if errors.Is(err, service.ErrLibraryRecordingSearchInvalid) {
			return nil, huma.Error422UnprocessableEntity("recording search parameters are invalid")
		}
		if errors.Is(err, service.ErrLibraryRecordingSearchUnavailable) {
			return nil, huma.Error503ServiceUnavailable("library recording search is unavailable")
		}
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("recording search could not be admitted")
		}
		snapshot, err := operations.Snapshot(ctx, operation.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read admitted operation")
		}
		operations.Notify(operation.ID)
		return &libraryRecordingSearchOutput{Body: operationResponse(snapshot)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-library-recording-search", Method: http.MethodGet,
		Path: "/library/recordings/search/{operation_id}", Summary: "Read accepted recording search results", Tags: []string{"Library"},
	}, func(ctx context.Context, input *libraryRecordingSearchResultInput) (*libraryRecordingSearchResultOutput, error) {
		if searches == nil {
			return nil, huma.Error503ServiceUnavailable("library recording search is unavailable")
		}
		result, err := searches.Result(ctx, input.OperationID)
		switch {
		case errors.Is(err, service.ErrLibraryRecordingSearchNotFound):
			return nil, huma.Error404NotFound("recording search result not found")
		case errors.Is(err, service.ErrLibraryRecordingSearchConflict), errors.Is(err, service.ErrLibraryRecordingSearchConfigurationChanged):
			return nil, huma.Error409Conflict("recording search result is not current")
		case errors.Is(err, service.ErrLibraryRecordingSearchUnavailable):
			return nil, huma.Error503ServiceUnavailable("library recording search is unavailable")
		case err != nil:
			return nil, huma.Error500InternalServerError("failed to read recording search result")
		}
		output := &libraryRecordingSearchResultOutput{}
		output.Body.OperationID = result.OperationID
		output.Body.ProviderSourceID = result.ProviderSourceID
		output.Body.ConfigurationIdentity = result.ConfigurationIdentity
		output.Body.Total, output.Body.Offset, output.Body.Limit = result.Total, result.Offset, result.Limit
		output.Body.Items = make([]libraryRecordingSearchItemOutput, 0, len(result.Items))
		for _, item := range result.Items {
			artists := make([]libraryRecordingSearchArtistOutput, 0, len(item.Artists))
			for _, artist := range item.Artists {
				artists = append(artists, libraryRecordingSearchArtistOutput{Name: artist.Name, JoinPhrase: artist.JoinPhrase, ProviderArtistID: artist.ProviderArtistID})
			}
			output.Body.Items = append(output.Body.Items, libraryRecordingSearchItemOutput{
				ProviderID: item.ProviderID, ProviderSourceID: item.ProviderSourceID,
				ProviderRecordingID: item.ProviderRecordingID, ProviderKey: item.ProviderKey,
				Title: item.Title, Artists: artists, DurationMS: item.DurationMS,
				LocalRecordingID: apiUUIDString(item.LocalRecordingID),
			})
		}
		return output, nil
	})
}
