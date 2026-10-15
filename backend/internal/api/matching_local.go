package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type matchingLocalPreviewBody struct {
	LocalReleaseID            *uuid.UUID                    `json:"local_release_id,omitempty"`
	ExpectedGroupRevision     string                        `json:"expected_group_revision"`
	ExpectedAuthorityRevision int64                         `json:"expected_authority_revision"`
	ReleaseTitle              string                        `json:"release_title"`
	ReleaseArtists            []service.MatchingLocalArtist `json:"release_artists"`
	Tracks                    []service.MatchingLocalTrack  `json:"tracks"`
}

type matchingLocalConfirmBody struct {
	LocalReleaseID            *uuid.UUID                    `json:"local_release_id,omitempty"`
	ExpectedGroupRevision     string                        `json:"expected_group_revision"`
	ExpectedAuthorityRevision int64                         `json:"expected_authority_revision"`
	ReleaseTitle              string                        `json:"release_title"`
	ReleaseArtists            []service.MatchingLocalArtist `json:"release_artists"`
	Tracks                    []service.MatchingLocalTrack  `json:"tracks"`
	PreviewDigest             string                        `json:"preview_digest"`
	RequestID                 uuid.UUID                     `json:"request_id"`
}

type matchingLocalPreviewInput struct {
	GroupID uuid.UUID                `path:"group_id"`
	Body    matchingLocalPreviewBody `json:"body"`
}

type matchingLocalConfirmInput struct {
	GroupID uuid.UUID                `path:"group_id"`
	Body    matchingLocalConfirmBody `json:"body"`
}

type matchingLocalOutput struct{ Body service.MatchingLocalPreview }
type matchingLocalCatalogOutput struct{ Body service.MatchingLocalCatalog }

func registerMatchingLocal(api huma.API, local *service.MatchingLocalService) {
	huma.Register(api, huma.Operation{OperationID: "list-local-releases", Method: http.MethodGet,
		Path: "/local-releases", Summary: "List pure local releases", Tags: []string{"Sources"}},
		func(ctx context.Context, _ *struct{}) (*matchingLocalCatalogOutput, error) {
			if local == nil {
				return nil, huma.Error503ServiceUnavailable("local matching is unavailable")
			}
			result, err := local.List(ctx)
			if err != nil {
				return nil, matchingLocalHTTPError(err)
			}
			return &matchingLocalCatalogOutput{Body: result}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "preview-incoming-group-local-matching", Method: http.MethodPost,
		Path: "/incoming-groups/{group_id}/matching/local/preview", Summary: "Preview an explicit local release for an incoming group", Tags: []string{"Sources"}},
		func(ctx context.Context, input *matchingLocalPreviewInput) (*matchingLocalOutput, error) {
			if local == nil {
				return nil, huma.Error503ServiceUnavailable("local matching is unavailable")
			}
			result, err := local.Preview(ctx, input.GroupID, localMatchingIntent(input.Body))
			if err != nil {
				return nil, matchingLocalHTTPError(err)
			}
			return &matchingLocalOutput{Body: result}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "confirm-incoming-group-local-matching", Method: http.MethodPost,
		Path: "/incoming-groups/{group_id}/matching/local/confirm", Summary: "Confirm an explicit local release for an incoming group", Tags: []string{"Sources"}},
		func(ctx context.Context, input *matchingLocalConfirmInput) (*matchingLocalOutput, error) {
			if local == nil {
				return nil, huma.Error503ServiceUnavailable("local matching is unavailable")
			}
			result, err := local.Confirm(ctx, input.GroupID, localMatchingIntent(matchingLocalPreviewBody{
				ExpectedGroupRevision: input.Body.ExpectedGroupRevision, ExpectedAuthorityRevision: input.Body.ExpectedAuthorityRevision,
				LocalReleaseID: input.Body.LocalReleaseID, ReleaseTitle: input.Body.ReleaseTitle, ReleaseArtists: input.Body.ReleaseArtists, Tracks: input.Body.Tracks,
			}), input.Body.PreviewDigest, input.Body.RequestID)
			if err != nil {
				return nil, matchingLocalHTTPError(err)
			}
			return &matchingLocalOutput{Body: result}, nil
		})
}

func localMatchingIntent(body matchingLocalPreviewBody) service.MatchingLocalIntent {
	return service.MatchingLocalIntent{ExpectedGroupRevision: body.ExpectedGroupRevision, ExpectedAuthorityRevision: body.ExpectedAuthorityRevision,
		LocalReleaseID: body.LocalReleaseID, ReleaseTitle: body.ReleaseTitle, ReleaseArtists: body.ReleaseArtists, Tracks: body.Tracks}
}

func matchingLocalHTTPError(err error) error {
	switch {
	case errors.Is(err, service.ErrMatchingLocalInvalid):
		return huma.Error422UnprocessableEntity("invalid local matching request")
	case errors.Is(err, service.ErrMatchingLocalConflict):
		return huma.Error409Conflict("incoming group or matching authority changed")
	default:
		return huma.Error500InternalServerError("local matching failed")
	}
}
