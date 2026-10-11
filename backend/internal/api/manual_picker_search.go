package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type manualPickerSearchBody struct {
	ExpectedGroupRevision string    `json:"expected_group_revision" minLength:"1"`
	RequestID             uuid.UUID `json:"request_id"`
	Entity                string    `json:"entity" enum:"artist,release"`
	Query                 string    `json:"query"`
	ArtistID              string    `json:"artist_id,omitempty"`
	Offset                *int      `json:"offset"`
	Limit                 *int      `json:"limit"`
}

type manualPickerSearchInput struct {
	GroupID uuid.UUID              `path:"group_id"`
	Body    manualPickerSearchBody `json:"body"`
}

type manualPickerSearchOutput struct {
	Body struct {
		OperationID uuid.UUID `json:"operation_id"`
	}
}

type manualPickerSearchResultInput struct {
	GroupID     uuid.UUID `path:"group_id"`
	OperationID uuid.UUID `path:"operation_id"`
}

type manualPickerSearchResultOutput struct {
	Body struct {
		GroupID       uuid.UUID       `json:"group_id"`
		OperationID   uuid.UUID       `json:"operation_id"`
		Kind          string          `json:"kind"`
		Payload       json.RawMessage `json:"payload"`
		PayloadSHA256 string          `json:"payload_sha256"`
		CacheRevision *string         `json:"cache_revision,omitempty"`
	}
}

func registerManualPickerSearch(api huma.API, searches *service.ManualPickerSearchService) {
	huma.Register(api, huma.Operation{
		OperationID: "start-incoming-group-manual-picker-search", Method: http.MethodPost,
		Path: "/incoming-groups/{group_id}/manual-search", Summary: "Search MusicBrainz for an explicitly selected artist or release",
		Tags: []string{"Sources"}, DefaultStatus: http.StatusAccepted,
		Responses: map[string]*huma.Response{"202": {Description: "Manual picker search admitted"}},
	}, func(ctx context.Context, input *manualPickerSearchInput) (*manualPickerSearchOutput, error) {
		if searches == nil {
			return nil, huma.Error503ServiceUnavailable("manual picker search is unavailable")
		}
		if input.Body.Offset == nil || input.Body.Limit == nil {
			return nil, huma.Error422UnprocessableEntity("offset and limit are required")
		}
		kind, query := input.Body.Entity+"_search", input.Body.Query
		if input.Body.Entity == "release" && input.Body.ArtistID != "" {
			return nil, huma.Error422UnprocessableEntity("artist_id is only valid when browsing an artist's releases")
		}
		if input.Body.ArtistID != "" {
			artistID, err := parseSelectedArtist(input.Body.ArtistID)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("artist_id must be a MusicBrainz artist MBID or official artist URL")
			}
			kind, query = "artist_release_browse", artistID.String()
		}
		operation, err := searches.Start(ctx, input.GroupID, input.Body.RequestID, input.Body.ExpectedGroupRevision, kind, query, *input.Body.Offset, *input.Body.Limit)
		if errors.Is(err, service.ErrManualPickerSearchInvalid) {
			return nil, huma.Error422UnprocessableEntity("manual picker search is invalid")
		}
		if errors.Is(err, persistence.ErrIncomingMatchConflict) {
			return nil, huma.Error409Conflict("incoming group changed or request ID was reused with different search parameters")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to prepare manual picker search")
		}
		output := &manualPickerSearchOutput{}
		output.Body.OperationID = operation.ID
		return output, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-incoming-group-manual-picker-search", Method: http.MethodGet,
		Path: "/incoming-groups/{group_id}/manual-search/{operation_id}", Summary: "Read accepted results for a manual MusicBrainz picker search", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *manualPickerSearchResultInput) (*manualPickerSearchResultOutput, error) {
		if searches == nil {
			return nil, huma.Error503ServiceUnavailable("manual picker search is unavailable")
		}
		result, err := searches.Result(ctx, input.GroupID, input.OperationID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read manual picker search result")
		}
		if result == nil {
			return nil, huma.Error404NotFound("manual picker search result not found")
		}
		output := &manualPickerSearchResultOutput{}
		output.Body.GroupID, output.Body.OperationID, output.Body.Kind = result.GroupID, result.OperationID, result.Kind
		output.Body.Payload, output.Body.PayloadSHA256, output.Body.CacheRevision = result.Payload, result.PayloadSHA256, result.CacheRevision
		return output, nil
	})
}

func parseSelectedArtist(value string) (uuid.UUID, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return uuid.Nil, errors.New("invalid selected artist")
	}
	if id, err := uuid.Parse(value); err == nil && strings.EqualFold(value, id.String()) {
		return id, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "musicbrainz.org" || parsed.Host != "musicbrainz.org" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(value, "#") || strings.Contains(value, "?") || parsed.Fragment != "" || parsed.RawPath != "" {
		return uuid.Nil, errors.New("invalid MusicBrainz artist URL")
	}
	parts := strings.Split(parsed.EscapedPath(), "/")
	if len(parts) != 3 || parts[1] != "artist" || parts[2] == "" {
		return uuid.Nil, errors.New("URL is not a MusicBrainz artist")
	}
	id, err := uuid.Parse(parts[2])
	if err != nil || !strings.EqualFold(parts[2], id.String()) {
		return uuid.Nil, errors.New("invalid MusicBrainz artist MBID")
	}
	return id, nil
}
