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
		Workflow           string                      `json:"workflow,omitempty"`
		Mode               string                      `json:"mode,omitempty"`
		PreviewDigest      string                      `json:"preview_digest" pattern:"^[0-9a-fA-F]{64}$" minLength:"64" maxLength:"64"`
		ExpectedEpoch      uuid.UUID                   `json:"expected_epoch"`
		ExpectedGeneration *uuid.UUID                  `json:"expected_generation,omitempty"`
		LocationID         *uuid.UUID                  `json:"location_id,omitempty"`
		MediaVariantID     *uuid.UUID                  `json:"media_variant_id,omitempty"`
		StreamIndex        *int                        `json:"stream_index,omitempty" minimum:"0"`
		Receipt            *LibraryMatchingReceiptBody `json:"receipt,omitempty"`
	}
}

// LibraryMatchingReceiptBody selects the saved manual or Local confirmation
// receipt that must bind the exact source association being admitted.
type LibraryMatchingReceiptBody struct {
	Kind          string    `json:"kind"`
	RequestID     uuid.UUID `json:"request_id"`
	TrackSourceID uuid.UUID `json:"track_source_id"`
}

func (receipt *LibraryMatchingReceiptBody) publicationSource() (*persistence.MatchingReceiptPublicationSource, error) {
	if receipt.Kind != persistence.MatchingReceiptKindManual && receipt.Kind != persistence.MatchingReceiptKindLocal {
		return nil, errors.New("receipt kind must be manual or local")
	}
	if receipt.RequestID == uuid.Nil || receipt.TrackSourceID == uuid.Nil {
		return nil, errors.New("receipt request_id and track_source_id must be nonzero UUIDs")
	}
	return &persistence.MatchingReceiptPublicationSource{Kind: receipt.Kind, RequestID: receipt.RequestID, TrackSourceID: receipt.TrackSourceID}, nil
}

type librarySourceRepublishAdmissionService interface {
	AdmitSourceRepublishRetainingSelection(context.Context, uuid.UUID, uuid.UUID, service.SourceRepublishConfirmation) (uuid.UUID, error)
}

type libraryMatchingReceiptAdmissionService interface {
	AdmitFromMatchingReceipt(context.Context, uuid.UUID, uuid.UUID, service.FirstPublicationConfirmation) (uuid.UUID, error)
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
		if err := requireSupportedPlatform(dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.LibraryPublications == nil || dependencies.Operations == nil {
			return nil, huma.Error503ServiceUnavailable("library publication service is unavailable")
		}
		workflow := input.Body.Workflow
		if workflow != "" && workflow != "first" && workflow != "retained_same_path" && workflow != "matching_receipt" {
			return nil, huma.Error400BadRequest("workflow must be first, retained_same_path, or matching_receipt")
		}
		if input.Body.Mode != "" && input.Body.Mode != "ordinary" && input.Body.Mode != "forced" {
			return nil, huma.Error400BadRequest("mode must be ordinary or forced")
		}
		var receipt *persistence.MatchingReceiptPublicationSource
		if input.Body.Receipt != nil {
			if workflow != "matching_receipt" {
				return nil, huma.Error400BadRequest("matching receipt is only accepted with the matching_receipt workflow")
			}
			parsed, parseErr := input.Body.Receipt.publicationSource()
			if parseErr != nil {
				return nil, huma.Error400BadRequest(parseErr.Error())
			}
			receipt = parsed
		}
		if workflow == "matching_receipt" {
			if input.Body.Mode != "" && input.Body.Mode != "ordinary" {
				return nil, huma.Error400BadRequest("matching_receipt workflow only supports ordinary mode")
			}
			if receipt == nil {
				return nil, huma.Error400BadRequest("matching_receipt workflow requires a receipt")
			}
			if input.Body.ExpectedEpoch == uuid.Nil {
				return nil, huma.Error400BadRequest("matching_receipt workflow requires expected_epoch")
			}
			if input.Body.ExpectedGeneration != nil && *input.Body.ExpectedGeneration == uuid.Nil {
				return nil, huma.Error400BadRequest("expected_generation must be a nonzero UUID when provided")
			}
			choice, choiceErr := previewSourceChoice(input.Body.LocationID, input.Body.MediaVariantID, input.Body.StreamIndex)
			if choiceErr != nil || choice == nil {
				return nil, huma.Error400BadRequest("matching_receipt workflow requires location_id, media_variant_id, and stream_index")
			}
			receiptService, ok := dependencies.LibraryPublications.(libraryMatchingReceiptAdmissionService)
			if !ok {
				return nil, huma.Error503ServiceUnavailable("matching receipt publication admission is unavailable")
			}
			id, err := receiptService.AdmitFromMatchingReceipt(ctx, input.ReleaseID, input.PositionID, service.FirstPublicationConfirmation{
				PreviewDigest:      input.Body.PreviewDigest,
				ExpectedEpoch:      input.Body.ExpectedEpoch,
				ExpectedGeneration: input.Body.ExpectedGeneration,
				SourceChoice:       choice,
				Receipt:            receipt,
			})
			if err != nil {
				return nil, firstPublicationAdmissionError(err)
			}
			return queuedPublicationOperation(ctx, dependencies.Operations, id)
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
	case errors.Is(err, service.ErrPublicationArtistDiscography):
		return huma.Error503ServiceUnavailable("first publication requires the primary artist's complete discography; verify the primary artist and MusicBrainz, then retry")
	default:
		return huma.Error500InternalServerError("first publication admission failed")
	}
}
