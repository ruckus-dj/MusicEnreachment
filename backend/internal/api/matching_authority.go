package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type matchingReturnAutomaticBody struct {
	ExpectedGroupRevision     string    `json:"expected_group_revision"`
	ExpectedAuthorityRevision int64     `json:"expected_authority_revision"`
	RequestID                 uuid.UUID `json:"request_id"`
}

type matchingReturnAutomaticInput struct {
	GroupID uuid.UUID                   `path:"group_id"`
	Body    matchingReturnAutomaticBody `json:"body"`
}

func registerMatchingAuthority(api huma.API, authority *service.MatchingAuthorityService) {
	huma.Register(api, huma.Operation{OperationID: "return-incoming-group-matching-to-automatic", Method: http.MethodPost,
		Path: "/incoming-groups/{group_id}/matching/return-automatic", Summary: "Return incoming group matching authority to automatic", Tags: []string{"Sources"}},
		func(ctx context.Context, input *matchingReturnAutomaticInput) (*matchingReturnAutomaticOutput, error) {
			if authority == nil {
				return nil, huma.Error503ServiceUnavailable("matching authority is unavailable")
			}
			result, err := authority.ReturnAutomatic(ctx, input.GroupID, input.Body.ExpectedGroupRevision, input.Body.ExpectedAuthorityRevision, input.Body.RequestID)
			if err != nil {
				return nil, matchingAuthorityHTTPError(err)
			}
			return &matchingReturnAutomaticOutput{Body: result}, nil
		})
}

type matchingReturnAutomaticOutput struct {
	Body service.MatchingReturnAutomaticResult
}

func matchingAuthorityHTTPError(err error) error {
	switch {
	case errors.Is(err, service.ErrMatchingAuthorityInvalid):
		return huma.Error422UnprocessableEntity("invalid matching authority request")
	case errors.Is(err, service.ErrMatchingAuthorityConflict):
		return huma.Error409Conflict("matching authority or group changed")
	default:
		return huma.Error500InternalServerError("matching authority operation failed")
	}
}
