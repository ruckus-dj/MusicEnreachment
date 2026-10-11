package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type matchingReviewInput struct {
	GroupID uuid.UUID `path:"group_id"`
}

type matchingReviewOutput struct {
	Body service.MatchingReview
}

func registerMatchingReview(api huma.API, reviews *service.MatchingReviewService) {
	huma.Register(api, huma.Operation{OperationID: "get-incoming-group-matching", Method: http.MethodGet,
		Path: "/incoming-groups/{group_id}/matching", Summary: "Read persisted incoming group matching review", Tags: []string{"Sources"}},
		func(ctx context.Context, input *matchingReviewInput) (*matchingReviewOutput, error) {
			if reviews == nil {
				return nil, huma.Error503ServiceUnavailable("matching review is unavailable")
			}
			result, err := reviews.Get(ctx, input.GroupID)
			if err != nil {
				return nil, huma.Error500InternalServerError("failed to read matching review")
			}
			return &matchingReviewOutput{Body: result}, nil
		})
}
