package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type matchingRecheckAbandonmentService interface {
	PreviewMatchingRecheckAbandonment(context.Context, uuid.UUID) (persistence.MatchingRecheckAbandonmentPreview, error)
	AbandonMatchingRecheckPublications(context.Context, uuid.UUID, string) error
}

type matchingRecheckAbandonmentPreviewOutput struct {
	CacheControl string                                        `header:"Cache-Control"`
	Body         persistence.MatchingRecheckAbandonmentPreview `json:"body"`
}

type abandonMatchingRecheckInput struct {
	OperationInput
	Body struct {
		Revision string `json:"revision" pattern:"^[0-9a-f]{64}$"`
	}
}

func registerMatchingRecheckAbandonment(api huma.API, dependencies Dependencies) {
	publications, available := dependencies.LibraryPublications.(matchingRecheckAbandonmentService)
	huma.Register(api, huma.Operation{
		OperationID: "preview-matching-recheck-abandonment", Method: http.MethodGet,
		Path: "/operations/{operation_id}/matching-recheck-abandonment", Summary: "Preview abandonment of unfinished matching republications",
		Tags: []string{"Operations"},
	}, func(ctx context.Context, input *OperationInput) (*matchingRecheckAbandonmentPreviewOutput, error) {
		if !available {
			return nil, huma.Error503ServiceUnavailable("matching republication abandonment is unavailable")
		}
		preview, err := publications.PreviewMatchingRecheckAbandonment(ctx, input.ID)
		if err != nil {
			return nil, matchingRecheckAbandonmentError(err)
		}
		return &matchingRecheckAbandonmentPreviewOutput{CacheControl: "no-store", Body: preview}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "abandon-matching-recheck-publications", Method: http.MethodPost,
		Path: "/operations/{operation_id}/matching-recheck-abandonment", Summary: "Abandon unfinished matching republications",
		Tags: []string{"Operations"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *abandonMatchingRecheckInput) (*struct{}, error) {
		if !available {
			return nil, huma.Error503ServiceUnavailable("matching republication abandonment is unavailable")
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := publications.AbandonMatchingRecheckPublications(ctx, input.ID, input.Body.Revision); err != nil {
			return nil, matchingRecheckAbandonmentError(err)
		}
		return nil, nil
	})
}

func matchingRecheckAbandonmentError(err error) error {
	if errors.Is(err, persistence.ErrPublicationAdmissionConflict) {
		return huma.Error409Conflict("matching republication changed or is not safe to abandon")
	}
	return huma.Error500InternalServerError("failed to read or abandon matching republication")
}

var _ matchingRecheckAbandonmentService = (*service.LibraryPublications)(nil)
