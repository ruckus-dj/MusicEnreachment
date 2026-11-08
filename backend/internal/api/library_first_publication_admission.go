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
		Workflow           string     `json:"workflow,omitempty"`
		Mode               string     `json:"mode,omitempty"`
		PreviewDigest      string     `json:"preview_digest" pattern:"^[0-9a-fA-F]{64}$" minLength:"64" maxLength:"64"`
		ExpectedEpoch      uuid.UUID  `json:"expected_epoch"`
		ExpectedGeneration *uuid.UUID `json:"expected_generation,omitempty"`
		LocationID         *uuid.UUID `json:"location_id,omitempty"`
		MediaVariantID     *uuid.UUID `json:"media_variant_id,omitempty"`
		StreamIndex        *int       `json:"stream_index,omitempty" minimum:"0"`
	}
}

type librarySourceRepublishAdmissionService interface {
	AdmitSourceRepublishRetainingSelection(context.Context, uuid.UUID, uuid.UUID, service.SourceRepublishConfirmation) (uuid.UUID, error)
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
		workflow := input.Body.Workflow
		if workflow != "" && workflow != "first" && workflow != "retained_same_path" {
			return nil, huma.Error400BadRequest("workflow must be first or retained_same_path")
		}
		if input.Body.Mode != "" && input.Body.Mode != "ordinary" && input.Body.Mode != "forced" {
			return nil, huma.Error400BadRequest("mode must be ordinary or forced")
		}
		if workflow == "retained_same_path" {
			if input.Body.Mode == "" || input.Body.ExpectedEpoch == uuid.Nil || input.Body.ExpectedGeneration == nil || *input.Body.ExpectedGeneration == uuid.Nil {
				return nil, huma.Error400BadRequest("retained_same_path workflow requires mode, expected_epoch, and expected_generation")
			}
			if input.Body.LocationID != nil || input.Body.MediaVariantID != nil || input.Body.StreamIndex != nil {
				return nil, huma.Error400BadRequest("retained_same_path workflow does not accept source selection")
			}
			retainedService, ok := dependencies.LibraryPublications.(librarySourceRepublishAdmissionService)
			if !ok {
				return nil, huma.Error503ServiceUnavailable("retained source republish admission is unavailable")
			}
			id, err := retainedService.AdmitSourceRepublishRetainingSelection(ctx, input.ReleaseID, input.PositionID, service.SourceRepublishConfirmation{
				PreviewDigest: input.Body.PreviewDigest, ExpectedEpoch: input.Body.ExpectedEpoch,
				ExpectedGeneration: *input.Body.ExpectedGeneration, Mode: input.Body.Mode,
			})
			if err != nil {
				return nil, firstPublicationAdmissionError(err)
			}
			return queuedPublicationOperation(ctx, dependencies.Operations, id)
		}
		if workflow == "first" && input.Body.Mode == "forced" {
			return nil, huma.Error400BadRequest("forced mode is not available for the first workflow")
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
		return queuedPublicationOperation(ctx, dependencies.Operations, id)
	})
}

func queuedPublicationOperation(ctx context.Context, operations *service.Operations, id uuid.UUID) (*OperationOutput, error) {
	snapshot, err := operations.Snapshot(ctx, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to read queued first publication")
	}
	return operationOutput(snapshot), nil
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
