package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// LibraryPublicationSelectionPreviewInput is a bounded read-only request. It
// accepts no source token, admission identity, or job input.
type LibraryPublicationSelectionPreviewInput struct {
	Body struct {
		Mode      string                                       `json:"mode,omitempty"`
		Positions []LibraryPublicationSelectionPreviewPosition `json:"positions" minItems:"1" maxItems:"100"`
	}
}

type LibraryPublicationSelectionPreviewPosition struct {
	ReleaseID  uuid.UUID `json:"release_id"`
	PositionID uuid.UUID `json:"position_id"`
}

type LibraryPublicationSelectionPreviewOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         LibraryPublicationSelectionPreviewResponse
}

type LibraryPublicationSelectionPreviewResponse struct {
	Mode  string                                           `json:"mode"`
	Items []LibraryPublicationSelectionPreviewItemResponse `json:"items" nullable:"false"`
}

type LibraryPublicationSelectionPreviewItemResponse struct {
	ReleaseID          uuid.UUID                                             `json:"release_id"`
	PositionID         uuid.UUID                                             `json:"position_id"`
	Disposition        string                                                `json:"disposition"`
	Reason             string                                                `json:"reason,omitempty"`
	ResolvedPath       string                                                `json:"resolved_path,omitempty"`
	PathClash          bool                                                  `json:"path_clash"`
	PathClashPositions []LibraryPublicationSelectionPreviewReferenceResponse `json:"path_clash_positions,omitempty"`
}

type LibraryPublicationSelectionPreviewReferenceResponse struct {
	ReleaseID  uuid.UUID `json:"release_id"`
	PositionID uuid.UUID `json:"position_id"`
}

type libraryPublicationSelectionPreviewService interface {
	PreviewPublicationSelection(context.Context, service.LibraryPublicationSelectionPreviewRequest) (service.LibraryPublicationSelectionPreview, error)
}

func registerLibraryPublicationSelectionPreview(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "preview-library-publication-selection", Method: http.MethodPost,
		Path:    "/library/publication/preview",
		Summary: "Preview publication dispositions for a library selection", Tags: []string{"Library"},
	}, func(ctx context.Context, input *LibraryPublicationSelectionPreviewInput) (*LibraryPublicationSelectionPreviewOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.LibraryPublications == nil {
			return nil, huma.Error503ServiceUnavailable("library publication service is unavailable")
		}
		previewService, ok := dependencies.LibraryPublications.(libraryPublicationSelectionPreviewService)
		if !ok {
			return nil, huma.Error503ServiceUnavailable("library publication selection preview is unavailable")
		}
		mode := input.Body.Mode
		if mode == "" {
			mode = "ordinary"
		}
		if mode != "ordinary" && mode != "forced" {
			return nil, huma.Error400BadRequest("mode must be ordinary or forced")
		}
		request := service.LibraryPublicationSelectionPreviewRequest{Mode: mode}
		request.Positions = make([]service.LibraryPublicationSelectionPosition, 0, len(input.Body.Positions))
		seen := make(map[service.LibraryPublicationSelectionPosition]struct{}, len(input.Body.Positions))
		for _, position := range input.Body.Positions {
			selected := service.LibraryPublicationSelectionPosition{ReleaseID: position.ReleaseID, PositionID: position.PositionID}
			if selected.ReleaseID == uuid.Nil || selected.PositionID == uuid.Nil {
				return nil, huma.Error400BadRequest("positions must be nonzero")
			}
			if _, duplicate := seen[selected]; duplicate {
				return nil, huma.Error400BadRequest("positions must be distinct")
			}
			seen[selected] = struct{}{}
			request.Positions = append(request.Positions, selected)
		}
		preview, err := previewService.PreviewPublicationSelection(ctx, request)
		if err != nil {
			if errors.Is(err, service.ErrLibraryPublicationSelectionPreviewInvalid) {
				return nil, huma.Error400BadRequest("positions must contain distinct, nonzero release and position identities")
			}
			return nil, huma.Error500InternalServerError("library publication selection preview failed")
		}
		return &LibraryPublicationSelectionPreviewOutput{CacheControl: "no-store", Body: libraryPublicationSelectionPreviewResponse(preview)}, nil
	})
}

func libraryPublicationSelectionPreviewResponse(preview service.LibraryPublicationSelectionPreview) LibraryPublicationSelectionPreviewResponse {
	response := LibraryPublicationSelectionPreviewResponse{
		Mode:  preview.Mode,
		Items: make([]LibraryPublicationSelectionPreviewItemResponse, 0, len(preview.Items)),
	}
	for _, item := range preview.Items {
		projected := LibraryPublicationSelectionPreviewItemResponse{
			ReleaseID: item.ReleaseID, PositionID: item.PositionID,
			Disposition: item.Disposition, Reason: item.Reason, ResolvedPath: item.ResolvedPath,
			PathClash: item.PathClash,
		}
		if len(item.PathClashPositions) != 0 {
			projected.PathClashPositions = make([]LibraryPublicationSelectionPreviewReferenceResponse, 0, len(item.PathClashPositions))
			for _, reference := range item.PathClashPositions {
				projected.PathClashPositions = append(projected.PathClashPositions, LibraryPublicationSelectionPreviewReferenceResponse{
					ReleaseID: reference.ReleaseID, PositionID: reference.PositionID,
				})
			}
		}
		response.Items = append(response.Items, projected)
	}
	return response
}
