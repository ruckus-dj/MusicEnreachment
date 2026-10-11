package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type matchingManualAssignmentBody struct {
	FileMemberID    uuid.UUID `json:"file_member_id"`
	ProviderTrackID uuid.UUID `json:"provider_track_id"`
	Medium          int       `json:"medium"`
	Position        int       `json:"position"`
}

type matchingManualBody struct {
	ExpectedEvaluationRevision string                         `json:"expected_evaluation_revision"`
	ProviderReleaseID          uuid.UUID                      `json:"provider_release_id"`
	Assignments                []matchingManualAssignmentBody `json:"assignments"`
}

type matchingManualPreviewInput struct {
	GroupID uuid.UUID          `path:"group_id"`
	Body    matchingManualBody `json:"body"`
}

type matchingManualConfirmBody struct {
	ExpectedEvaluationRevision string                         `json:"expected_evaluation_revision"`
	ProviderReleaseID          uuid.UUID                      `json:"provider_release_id"`
	Assignments                []matchingManualAssignmentBody `json:"assignments"`
	PreviewDigest              string                         `json:"preview_digest"`
	RequestID                  uuid.UUID                      `json:"request_id"`
}

type matchingManualConfirmInput struct {
	GroupID uuid.UUID                 `path:"group_id"`
	Body    matchingManualConfirmBody `json:"body"`
}

type matchingManualOutput struct{ Body service.MatchingManualPreview }

func registerMatchingManual(api huma.API, manual *service.MatchingManualService) {
	huma.Register(api, huma.Operation{OperationID: "preview-incoming-group-manual-matching", Method: http.MethodPost,
		Path: "/incoming-groups/{group_id}/matching/preview", Summary: "Preview explicit incoming group matching assignments", Tags: []string{"Sources"}},
		func(ctx context.Context, input *matchingManualPreviewInput) (*matchingManualOutput, error) {
			if manual == nil {
				return nil, huma.Error503ServiceUnavailable("manual matching is unavailable")
			}
			result, err := manual.Preview(ctx, input.GroupID, input.Body.ExpectedEvaluationRevision, input.Body.ProviderReleaseID, matchingManualAssignments(input.Body.Assignments))
			if err != nil {
				return nil, matchingManualHTTPError(err)
			}
			return &matchingManualOutput{Body: result}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "confirm-incoming-group-manual-matching", Method: http.MethodPost,
		Path: "/incoming-groups/{group_id}/matching/confirm", Summary: "Confirm explicit incoming group matching assignments", Tags: []string{"Sources"}},
		func(ctx context.Context, input *matchingManualConfirmInput) (*matchingManualOutput, error) {
			if manual == nil {
				return nil, huma.Error503ServiceUnavailable("manual matching is unavailable")
			}
			result, err := manual.Confirm(ctx, input.GroupID, input.Body.ExpectedEvaluationRevision, input.Body.ProviderReleaseID,
				matchingManualAssignments(input.Body.Assignments), input.Body.PreviewDigest, input.Body.RequestID)
			if err != nil {
				return nil, matchingManualHTTPError(err)
			}
			return &matchingManualOutput{Body: result}, nil
		})
}

func matchingManualAssignments(values []matchingManualAssignmentBody) []service.MatchingManualAssignment {
	result := make([]service.MatchingManualAssignment, 0, len(values))
	for _, value := range values {
		result = append(result, service.MatchingManualAssignment{FileMemberID: value.FileMemberID, ProviderTrackID: value.ProviderTrackID, Medium: value.Medium, Position: value.Position})
	}
	return result
}

func matchingManualHTTPError(err error) error {
	switch {
	case errors.Is(err, service.ErrMatchingManualInvalid):
		return huma.Error422UnprocessableEntity("invalid manual matching request")
	case errors.Is(err, service.ErrMatchingManualConflict):
		return huma.Error409Conflict("matching evaluation or target changed")
	default:
		return huma.Error500InternalServerError("manual matching failed")
	}
}
