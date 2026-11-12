package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type LibraryDeletionReleaseInput struct {
	ReleaseID uuid.UUID `path:"release_id"`
}

type LibraryDeletionPublicationInput struct {
	ReleaseID  uuid.UUID `path:"release_id"`
	PositionID uuid.UUID `path:"position_id"`
}

type LibraryDeletionConfirmationInput struct {
	Body struct {
		ConfirmationDigest string `json:"confirmation_digest" pattern:"^[0-9a-f]{64}$" minLength:"64" maxLength:"64"`
	}
}

type LibraryDeletionPreviewOutput struct {
	Body LibraryDeletionPreviewResponse `json:"body"`
}

type LibraryDeletionPreviewResponse struct {
	Kind               string                           `json:"kind"`
	ReleaseID          uuid.UUID                        `json:"release_id"`
	Title              string                           `json:"title"`
	PositionIDs        []uuid.UUID                      `json:"position_ids"`
	RecordingIDs       []uuid.UUID                      `json:"recording_ids"`
	RecordingCount     int                              `json:"recording_count" minimum:"0"`
	Outputs            []LibraryDeletionOutputResponse  `json:"outputs"`
	Blockers           []LibraryDeletionBlockerResponse `json:"blockers"`
	ConfirmationDigest string                           `json:"confirmation_digest"`
}

type LibraryDeletionOutputResponse struct {
	PositionID   uuid.UUID `json:"position_id"`
	RelativePath string    `json:"relative_path"`
	Capturable   bool      `json:"capturable"`
}

type LibraryDeletionBlockerResponse struct {
	PositionID uuid.UUID `json:"position_id,omitempty"`
	Code       string    `json:"code"`
	Message    string    `json:"message"`
}

func registerLibraryDeletion(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "preview-library-release-deletion", Method: http.MethodGet,
		Path: "/library/releases/{release_id}/deletion/preview", Summary: "Preview a library release deletion",
		Tags: []string{"Library"},
	}, func(ctx context.Context, input *LibraryDeletionReleaseInput) (*LibraryDeletionPreviewOutput, error) {
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.LibraryDeletion == nil {
			return nil, huma.Error503ServiceUnavailable("library deletion is unavailable")
		}
		preview, err := dependencies.LibraryDeletion.Preview(ctx, "release", input.ReleaseID, uuid.Nil)
		if err != nil {
			return nil, libraryDeletionPreviewError(err)
		}
		return &LibraryDeletionPreviewOutput{Body: libraryDeletionPreviewResponse(preview)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "preview-library-publication-deletion", Method: http.MethodGet,
		Path:    "/library/releases/{release_id}/positions/{position_id}/deletion/preview",
		Summary: "Preview a library publication deletion", Tags: []string{"Library"},
	}, func(ctx context.Context, input *LibraryDeletionPublicationInput) (*LibraryDeletionPreviewOutput, error) {
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.LibraryDeletion == nil {
			return nil, huma.Error503ServiceUnavailable("library deletion is unavailable")
		}
		preview, err := dependencies.LibraryDeletion.Preview(ctx, "publication", input.ReleaseID, input.PositionID)
		if err != nil {
			return nil, libraryDeletionPreviewError(err)
		}
		return &LibraryDeletionPreviewOutput{Body: libraryDeletionPreviewResponse(preview)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "confirm-library-deletion", Method: http.MethodPost,
		Path: "/library/deletion/confirm", Summary: "Confirm a previewed library deletion",
		Tags: []string{"Library"}, DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *LibraryDeletionConfirmationInput) (*OperationOutput, error) {
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.LibraryDeletion == nil || dependencies.Operations == nil {
			return nil, huma.Error503ServiceUnavailable("library deletion is unavailable")
		}
		operationID, err := dependencies.LibraryDeletion.Confirm(ctx, input.Body.ConfirmationDigest)
		if err != nil {
			return nil, libraryDeletionConfirmationError(err)
		}
		snapshot, err := dependencies.Operations.Snapshot(ctx, operationID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read confirmed deletion operation")
		}
		return operationOutput(snapshot), nil
	})
}

func libraryDeletionPreviewResponse(preview *service.LibraryDeletionPreview) LibraryDeletionPreviewResponse {
	response := LibraryDeletionPreviewResponse{
		Kind: preview.Kind, ReleaseID: preview.ReleaseID, Title: preview.Title,
		PositionIDs:        append([]uuid.UUID{}, preview.PublicationIDs...),
		RecordingIDs:       append([]uuid.UUID{}, preview.RecordingIDs...),
		RecordingCount:     preview.RecordingCount,
		Outputs:            make([]LibraryDeletionOutputResponse, 0, len(preview.OutputFacts)),
		Blockers:           make([]LibraryDeletionBlockerResponse, 0, len(preview.Blockers)),
		ConfirmationDigest: preview.ConfirmationDigest,
	}
	for _, output := range preview.OutputFacts {
		response.Outputs = append(response.Outputs, LibraryDeletionOutputResponse{
			PositionID: output.PositionID, RelativePath: output.RelativePath, Capturable: output.Capturable,
		})
	}
	for _, blocker := range preview.Blockers {
		response.Blockers = append(response.Blockers, LibraryDeletionBlockerResponse{
			PositionID: blocker.PositionID, Code: blocker.Code, Message: blocker.Message,
		})
	}
	return response
}

func libraryDeletionPreviewError(err error) error {
	switch {
	case errors.Is(err, service.ErrLibraryDeletionReleaseNotFound):
		return huma.Error404NotFound("library deletion target not found")
	case errors.Is(err, service.ErrLibraryDeletionConflict), errors.Is(err, service.ErrLibraryDeletionBlocked):
		return huma.Error409Conflict("library deletion scope changed or is blocked")
	default:
		return huma.Error500InternalServerError("library deletion preview failed")
	}
}

func libraryDeletionConfirmationError(err error) error {
	switch {
	case errors.Is(err, service.ErrLibraryDeletionConfirmationUnavailable):
		return huma.Error409Conflict("library deletion confirmation is unavailable or expired")
	case errors.Is(err, service.ErrLibraryDeletionReleaseNotFound):
		return huma.Error404NotFound("library deletion target not found")
	case errors.Is(err, service.ErrLibraryDeletionConflict), errors.Is(err, service.ErrLibraryDeletionBlocked):
		return huma.Error409Conflict("library deletion scope changed or is blocked")
	default:
		return huma.Error500InternalServerError("library deletion confirmation failed")
	}
}

func libraryDeletionRetryError(err error) error {
	switch {
	case errors.Is(err, service.ErrLibraryDeletionReleaseNotFound):
		return huma.Error404NotFound("library deletion target not found")
	case errors.Is(err, service.ErrLibraryDeletionConflict), errors.Is(err, service.ErrLibraryDeletionBlocked):
		return huma.Error409Conflict("retained library deletion scope is no longer eligible")
	default:
		return huma.Error409Conflict("library deletion could not be retried")
	}
}
