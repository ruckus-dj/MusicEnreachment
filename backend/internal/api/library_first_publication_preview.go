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

type LibraryFirstPublicationPreviewInput struct {
	LibraryPublicationInput
}

type LibraryFirstPublicationPreviewOutput struct {
	Body LibraryFirstPublicationPreviewResponse `json:"body"`
}

// LibraryFirstPublicationPreviewResponse deliberately projects only fields
// intended for the API. In particular it does not expose the service's frozen
// publication inputs or installation/path details.
type LibraryFirstPublicationPreviewResponse struct {
	ReleaseID         uuid.UUID                         `json:"release_id"`
	PositionID        uuid.UUID                         `json:"position_id"`
	Epoch             uuid.UUID                         `json:"epoch"`
	ContextDigest     string                            `json:"context_digest"`
	SelectedSource    *firstPublicationPreviewSource    `json:"selected_source,omitempty"`
	Transform         *firstPublicationPreviewTransform `json:"transform,omitempty"`
	PathComponents    []string                          `json:"path_components,omitempty"`
	MetadataRevisions map[string]int64                  `json:"metadata_revisions"`
	Warnings          []string                          `json:"warnings"`
	Blockers          []string                          `json:"blockers"`
	NativeStatus      string                            `json:"native_status"`
}

type firstPublicationPreviewTransform struct {
	Action          string `json:"action"`
	OutputContainer string `json:"output_container"`
	Extension       string `json:"extension"`
	Bitrate         int    `json:"bitrate"`
}

type firstPublicationPreviewSource struct {
	RootID         uuid.UUID `json:"root_id"`
	LocationID     uuid.UUID `json:"location_id"`
	MediaVariantID uuid.UUID `json:"media_variant_id"`
	StreamIndex    int       `json:"stream_index"`
	Codec          string    `json:"codec"`
}

func registerLibraryFirstPublicationPreview(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "get-library-first-publication-preview", Method: http.MethodGet,
		Path:    "/library/releases/{release_id}/positions/{position_id}/publication/preview",
		Summary: "Preview first publication for a library position", Tags: []string{"Library"},
	}, func(ctx context.Context, input *LibraryFirstPublicationPreviewInput) (*LibraryFirstPublicationPreviewOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.LibraryPublications == nil {
			return nil, huma.Error503ServiceUnavailable("library publication service is unavailable")
		}
		preview, err := dependencies.LibraryPublications.PreviewFirst(ctx, input.ReleaseID, input.PositionID)
		if err != nil {
			if errors.Is(err, persistence.ErrPublicationPositionNotFound) {
				return nil, huma.Error404NotFound("library publication position not found")
			}
			return nil, huma.Error500InternalServerError("first publication preview failed")
		}
		return &LibraryFirstPublicationPreviewOutput{Body: libraryFirstPublicationPreviewResponse(preview)}, nil
	})
}

func libraryFirstPublicationPreviewResponse(preview service.FirstPublicationPreview) LibraryFirstPublicationPreviewResponse {
	response := LibraryFirstPublicationPreviewResponse{
		ReleaseID: preview.ReleaseID, PositionID: preview.PositionID, Epoch: preview.Epoch,
		ContextDigest: preview.ContextDigest, PathComponents: append([]string(nil), preview.PathComponents...),
		MetadataRevisions: preview.MetadataRevisions, Warnings: append([]string{}, preview.Warnings...),
		Blockers: append([]string{}, preview.Blockers...), NativeStatus: preview.NativeStatus,
	}
	if preview.SelectedSource != nil {
		response.SelectedSource = &firstPublicationPreviewSource{
			RootID: preview.SelectedSource.RootID, LocationID: preview.SelectedSource.LocationID,
			MediaVariantID: preview.SelectedSource.MediaVariantID,
			StreamIndex:    preview.SelectedSource.StreamIndex, Codec: preview.SelectedSource.Codec,
		}
	}
	if preview.Transform != nil {
		response.Transform = &firstPublicationPreviewTransform{
			Action: string(preview.Transform.Action), OutputContainer: string(preview.Transform.OutputContainer),
			Extension: preview.Transform.Extension, Bitrate: preview.Transform.Bitrate,
		}
	}
	return response
}
