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

type LibraryFirstPublicationAdmissionInput struct {
	LibraryPublicationInput
	Body struct {
		PreviewDigest  string     `json:"preview_digest" pattern:"^[0-9a-fA-F]{64}$" minLength:"64" maxLength:"64"`
		ExpectedEpoch  uuid.UUID  `json:"expected_epoch"`
		LocationID     *uuid.UUID `json:"location_id,omitempty"`
		MediaVariantID *uuid.UUID `json:"media_variant_id,omitempty"`
		StreamIndex    *int       `json:"stream_index,omitempty" minimum:"0"`
	}
}

func registerLibraryFirstPublicationAdmission(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "start-library-first-publication", Method: http.MethodPost,
		Path:    "/library/releases/{release_id}/positions/{position_id}/publication",
		Summary: "Start first publication for a library position", Tags: []string{"Library"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *LibraryFirstPublicationAdmissionInput) (*OperationOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.LibraryPublications == nil || dependencies.Operations == nil {
			return nil, huma.Error503ServiceUnavailable("library publication service is unavailable")
		}
		choice, choiceErr := previewSourceChoice(input.Body.LocationID, input.Body.MediaVariantID, input.Body.StreamIndex)
		if choiceErr != nil {
			return nil, huma.Error400BadRequest("source selection requires location_id, media_variant_id, and stream_index")
		}
		id, err := dependencies.LibraryPublications.AdmitFirst(ctx, input.ReleaseID, input.PositionID, service.FirstPublicationConfirmation{
			PreviewDigest: input.Body.PreviewDigest,
			ExpectedEpoch: input.Body.ExpectedEpoch,
			SourceChoice:  choice,
		})
		if err != nil {
			return nil, firstPublicationAdmissionError(err)
		}
		snapshot, err := dependencies.Operations.Snapshot(ctx, id)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read queued first publication")
		}
		return operationOutput(snapshot), nil
	})
}

func firstPublicationAdmissionError(err error) error {
	switch {
	case errors.Is(err, persistence.ErrPublicationPositionNotFound):
		return huma.Error404NotFound("library publication position not found")
	case errors.Is(err, service.ErrLibraryPublicationConflict), errors.Is(err, persistence.ErrPublicationAdmissionConflict):
		return huma.Error409Conflict("library publication changed; refresh and retry")
	case errors.Is(err, service.ErrLibraryPublicationUnavailable):
		return huma.Error409Conflict("first publication is temporarily unavailable")
	default:
		return huma.Error500InternalServerError("first publication admission failed")
	}
}
