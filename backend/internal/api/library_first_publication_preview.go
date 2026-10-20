package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type LibraryFirstPublicationPreviewInput struct {
	LibraryPublicationInput
	LocationID     string `query:"location_id"`
	MediaVariantID string `query:"media_variant_id"`
	StreamIndex    string `query:"stream_index"`
}

type LibraryFirstPublicationPreviewOutput struct {
	Body LibraryFirstPublicationPreviewResponse `json:"body"`
}

// LibraryFirstPublicationPreviewResponse deliberately projects only fields
// intended for the API. In particular it does not expose the service's frozen
// publication inputs or installation/path details.
type LibraryFirstPublicationPreviewResponse struct {
	ReleaseID              uuid.UUID                         `json:"release_id"`
	PositionID             uuid.UUID                         `json:"position_id"`
	Epoch                  uuid.UUID                         `json:"epoch"`
	ContextDigest          string                            `json:"context_digest"`
	SelectedSource         *firstPublicationPreviewSource    `json:"selected_source,omitempty"`
	SourceCandidates       []firstPublicationPreviewSource   `json:"source_candidates"`
	Transform              *firstPublicationPreviewTransform `json:"transform,omitempty"`
	PathComponents         []string                          `json:"path_components,omitempty"`
	ResolvedPathComponents []string                          `json:"resolved_path_components,omitempty"`
	DesiredTags            map[string][]string               `json:"desired_tags"`
	MetadataRevisions      map[string]int64                  `json:"metadata_revisions"`
	Warnings               []string                          `json:"warnings"`
	Blockers               []string                          `json:"blockers"`
	NativeStatus           string                            `json:"native_status"`
}

type firstPublicationPreviewTransform struct {
	Action          string `json:"action"`
	OutputContainer string `json:"output_container"`
	Extension       string `json:"extension"`
	Bitrate         int    `json:"bitrate"`
}

type firstPublicationPreviewSource struct {
	RootID                          uuid.UUID `json:"root_id"`
	LocationID                      uuid.UUID `json:"location_id"`
	MediaVariantID                  uuid.UUID `json:"media_variant_id"`
	StreamIndex                     int       `json:"stream_index"`
	Codec                           string    `json:"codec"`
	Title                           string    `json:"title"`
	Language                        string    `json:"language"`
	Channels                        *int      `json:"channels,omitempty"`
	RequiresExplicitStreamSelection bool      `json:"requires_explicit_stream_selection"`
	AudioStreamCount                int       `json:"audio_stream_count"`
	UnsupportedAudioStreamCount     int       `json:"unsupported_audio_stream_count"`
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
		choice, choiceErr := previewSourceChoiceFromQuery(input.LocationID, input.MediaVariantID, input.StreamIndex)
		if choiceErr != nil {
			return nil, huma.Error400BadRequest("source selection requires location_id, media_variant_id, and stream_index")
		}
		var preview service.FirstPublicationPreview
		var err error
		if choice != nil {
			selectedService, ok := dependencies.LibraryPublications.(libraryPublicationSourceSelectionService)
			if !ok {
				return nil, huma.Error503ServiceUnavailable("explicit source selection is unavailable")
			}
			preview, err = selectedService.PreviewFirstSelected(ctx, input.ReleaseID, input.PositionID, choice)
		} else {
			preview, err = dependencies.LibraryPublications.PreviewFirst(ctx, input.ReleaseID, input.PositionID)
		}
		if err != nil {
			if errors.Is(err, persistence.ErrPublicationAdmissionConflict) {
				return nil, huma.Error409Conflict("selected source changed; refresh preview and retry")
			}
			if errors.Is(err, persistence.ErrPublicationPositionNotFound) {
				return nil, huma.Error404NotFound("library publication position not found")
			}
			return nil, huma.Error500InternalServerError("first publication preview failed")
		}
		return &LibraryFirstPublicationPreviewOutput{Body: libraryFirstPublicationPreviewResponse(preview)}, nil
	})
}

func previewSourceChoiceFromQuery(locationID, variantID, streamIndex string) (*service.FirstPublicationSourceChoice, error) {
	if locationID == "" && variantID == "" && streamIndex == "" {
		return nil, nil
	}
	location, locationErr := uuid.Parse(locationID)
	variant, variantErr := uuid.Parse(variantID)
	index, indexErr := strconv.Atoi(streamIndex)
	if locationErr != nil || variantErr != nil || indexErr != nil || location == uuid.Nil || variant == uuid.Nil || index < 0 {
		return nil, errors.New("invalid source selection")
	}
	return &service.FirstPublicationSourceChoice{LocationID: location, MediaVariantID: variant, StreamIndex: index}, nil
}

func previewSourceChoice(locationID, variantID *uuid.UUID, streamIndex *int) (*service.FirstPublicationSourceChoice, error) {
	count := 0
	if locationID != nil {
		count++
	}
	if variantID != nil {
		count++
	}
	if streamIndex != nil {
		count++
	}
	if count == 0 {
		return nil, nil
	}
	if count != 3 || *locationID == uuid.Nil || *variantID == uuid.Nil || *streamIndex < 0 {
		return nil, errors.New("incomplete source selection")
	}
	return &service.FirstPublicationSourceChoice{LocationID: *locationID, MediaVariantID: *variantID, StreamIndex: *streamIndex}, nil
}

func libraryFirstPublicationPreviewResponse(preview service.FirstPublicationPreview) LibraryFirstPublicationPreviewResponse {
	response := LibraryFirstPublicationPreviewResponse{
		ReleaseID: preview.ReleaseID, PositionID: preview.PositionID, Epoch: preview.Epoch,
		ContextDigest: preview.ContextDigest, PathComponents: append([]string(nil), preview.PathComponents...),
		ResolvedPathComponents: append([]string(nil), preview.ResolvedPathComponents...),
		DesiredTags:            clonePreviewTags(preview.DesiredTags.Tags),
		MetadataRevisions:      preview.MetadataRevisions, Warnings: append([]string{}, preview.Warnings...),
		Blockers: append([]string{}, preview.Blockers...), NativeStatus: preview.NativeStatus,
	}
	response.SourceCandidates = make([]firstPublicationPreviewSource, 0, len(preview.SourceCandidates))
	for _, candidate := range preview.SourceCandidates {
		response.SourceCandidates = append(response.SourceCandidates, firstPublicationPreviewSource{
			RootID: candidate.RootID, LocationID: candidate.LocationID, MediaVariantID: candidate.MediaVariantID,
			StreamIndex: candidate.StreamIndex, Codec: candidate.Codec, Title: candidate.Title, Language: candidate.Language,
			Channels:                        candidate.Quality.Channels,
			RequiresExplicitStreamSelection: candidate.RequiresExplicitStreamSelection,
			AudioStreamCount:                candidate.AudioStreamCount,
			UnsupportedAudioStreamCount:     candidate.UnsupportedAudioStreamCount,
		})
	}
	if preview.SelectedSource != nil {
		response.SelectedSource = &firstPublicationPreviewSource{
			RootID: preview.SelectedSource.RootID, LocationID: preview.SelectedSource.LocationID,
			MediaVariantID: preview.SelectedSource.MediaVariantID,
			StreamIndex:    preview.SelectedSource.StreamIndex, Codec: preview.SelectedSource.Codec,
			Title: preview.SelectedSource.Title, Language: preview.SelectedSource.Language,
			Channels:                        preview.SelectedSource.Quality.Channels,
			RequiresExplicitStreamSelection: preview.SelectedSource.RequiresExplicitStreamSelection,
			AudioStreamCount:                preview.SelectedSource.AudioStreamCount,
			UnsupportedAudioStreamCount:     preview.SelectedSource.UnsupportedAudioStreamCount,
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

func clonePreviewTags(tags map[string][]string) map[string][]string {
	clone := make(map[string][]string, len(tags))
	for key, values := range tags {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}
