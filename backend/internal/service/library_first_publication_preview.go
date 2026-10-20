package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type firstPublicationContextReader interface {
	ReadLibraryFirstPublicationContext(context.Context, uuid.UUID, uuid.UUID) (*persistence.LibraryFirstPublicationContext, error)
}

type firstPublicationFormatSettings interface {
	ReadPublicationPlanningSettings(context.Context) (settings.RuntimeSettings, error)
}

// FirstPublicationPreview is a read-only planning result. It contains no output
// filesystem path and is not an admission or publication authorization.
type FirstPublicationPreview struct {
	ReleaseID              uuid.UUID                                `json:"release_id"`
	PositionID             uuid.UUID                                `json:"position_id"`
	Epoch                  uuid.UUID                                `json:"epoch"`
	Current                *persistence.LibraryPublicationCurrent   `json:"current,omitempty"`
	ActiveAttempt          *persistence.LibraryPublicationOperation `json:"active_attempt,omitempty"`
	ContextDigest          string                                   `json:"context_digest"`
	SelectedSource         *FirstPublicationPreviewSource           `json:"selected_source,omitempty"`
	SourceCandidates       []FirstPublicationPreviewSource          `json:"source_candidates"`
	Transform              *publication.AudioTransformPlan          `json:"transform,omitempty"`
	PathComponents         []string                                 `json:"path_components,omitempty"`
	ResolvedPathComponents []string                                 `json:"resolved_path_components,omitempty"`
	DesiredTags            publication.TagProjection                `json:"desired_tags"`
	MetadataRevisions      map[string]int64                         `json:"metadata_revisions"`
	Warnings               []string                                 `json:"warnings"`
	Blockers               []string                                 `json:"blockers"`
	NativeStatus           string                                   `json:"native_status"`
}

type FirstPublicationPreviewSource struct {
	RootID                          uuid.UUID                       `json:"root_id"`
	LocationID                      uuid.UUID                       `json:"location_id"`
	WorkID                          uuid.UUID                       `json:"work_id"`
	MediaVariantID                  uuid.UUID                       `json:"media_variant_id"`
	StreamIndex                     int                             `json:"stream_index"`
	Codec                           string                          `json:"codec"`
	Quality                         persistence.PersistedAudioFacts `json:"quality"`
	Title                           string                          `json:"title"`
	Language                        string                          `json:"language"`
	RequiresExplicitStreamSelection bool                            `json:"requires_explicit_stream_selection"`
	AudioStreamCount                int                             `json:"audio_stream_count"`
	UnsupportedAudioStreamCount     int                             `json:"unsupported_audio_stream_count"`
}

// FirstPublicationSourceChoice identifies one server-verified source stream.
type FirstPublicationSourceChoice struct {
	LocationID     uuid.UUID `json:"location_id"`
	MediaVariantID uuid.UUID `json:"media_variant_id"`
	StreamIndex    int       `json:"stream_index"`
}

// PreviewFirst constructs a preview from a coherent database snapshot and the
// current publication settings. Native comparisons use the output technical-data
// area for private probes and never create publication directories.
func (service *LibraryPublications) PreviewFirst(ctx context.Context, releaseID, positionID uuid.UUID) (FirstPublicationPreview, error) {
	return service.PreviewFirstSelected(ctx, releaseID, positionID, nil)
}

func (service *LibraryPublications) PreviewFirstSelected(ctx context.Context, releaseID, positionID uuid.UUID, choice *FirstPublicationSourceChoice) (FirstPublicationPreview, error) {
	if service == nil || service.repository == nil || releaseID == uuid.Nil || positionID == uuid.Nil {
		return FirstPublicationPreview{}, errors.New("first publication preview is unavailable")
	}
	repository, ok := service.repository.(firstPublicationContextReader)
	if !ok {
		return FirstPublicationPreview{}, errors.New("first publication context is unavailable")
	}
	planningSettings, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return FirstPublicationPreview{}, errors.New("first publication settings are unavailable")
	}
	contextSnapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return FirstPublicationPreview{}, fmt.Errorf("read first publication context: %w", err)
	}
	runtimeSettings, err := planningSettings.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return FirstPublicationPreview{}, fmt.Errorf("read publication planning settings: %w", err)
	}
	if contextSnapshot == nil || contextSnapshot.Library == nil {
		return FirstPublicationPreview{}, errors.New("first publication context is incomplete")
	}
	selectedTools, _ := service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
	preview, err := buildFirstPublicationPreviewSelected(contextSnapshot, runtimeSettings, selectedTools, releaseID, positionID, choice)
	if err != nil || len(preview.Blockers) != 0 {
		return preview, err
	}
	if service.resolveNamespace == nil {
		return preview, nil
	}
	if _, err := service.resolveNamespace(ctx, contextSnapshot, runtimeSettings.OutputDirectory, runtimeSettings.PublicationPathTemplate, &preview); err != nil {
		if ctx.Err() != nil {
			return FirstPublicationPreview{}, ctx.Err()
		}
		preview.Blockers = append(preview.Blockers, "native publication namespace could not be resolved: "+err.Error())
		preview.NativeStatus = "native namespace unavailable"
	}
	return preview, nil
}

// buildFirstPublicationPreview performs deterministic planning from captured
// inputs only. Callers that admit a publication can use the same builder with
// the tool selections captured by their transaction.
func buildFirstPublicationPreview(contextSnapshot *persistence.LibraryFirstPublicationContext, runtimeSettings settings.RuntimeSettings, selectedTools []persistence.SourceAnalysisToolSelection, releaseID, positionID uuid.UUID) (FirstPublicationPreview, error) {
	return buildFirstPublicationPreviewSelected(contextSnapshot, runtimeSettings, selectedTools, releaseID, positionID, nil)
}

func buildFirstPublicationPreviewSelected(contextSnapshot *persistence.LibraryFirstPublicationContext, runtimeSettings settings.RuntimeSettings, selectedTools []persistence.SourceAnalysisToolSelection, releaseID, positionID uuid.UUID, choice *FirstPublicationSourceChoice) (FirstPublicationPreview, error) {
	format, pathTemplate := runtimeSettings.PublicationFormat, runtimeSettings.PublicationPathTemplate
	if contextSnapshot == nil || contextSnapshot.Library == nil {
		return FirstPublicationPreview{}, errors.New("first publication context is incomplete")
	}
	library := contextSnapshot.Library
	preview := FirstPublicationPreview{
		ReleaseID: releaseID, PositionID: positionID, Epoch: library.ActiveEpoch,
		Current: library.Current, ActiveAttempt: library.ActiveAttempt,
		MetadataRevisions: map[string]int64{
			"release":   contextSnapshot.Metadata.Release.Revision,
			"track":     contextSnapshot.Metadata.Track.Revision,
			"recording": contextSnapshot.Metadata.Recording.Revision,
			"position":  contextSnapshot.Metadata.Position.Revision,
		},
		Warnings: []string{}, Blockers: append([]string(nil), contextSnapshot.Blockers...),
		NativeStatus: "native verification required",
	}
	if library.Current != nil {
		preview.Blockers = append(preview.Blockers, "a current publication exists; this preview is only for first publication")
	}
	if library.ActiveAttempt != nil {
		preview.Blockers = append(preview.Blockers, "a publication attempt is already active")
	}
	if len(selectedTools) == 0 {
		preview.Blockers = append(preview.Blockers, "verified managed FFmpeg executables are unavailable")
	}
	preview.SourceCandidates = make([]FirstPublicationPreviewSource, 0, len(contextSnapshot.Sources))
	for _, source := range contextSnapshot.Sources {
		preview.SourceCandidates = append(preview.SourceCandidates, FirstPublicationPreviewSource{
			RootID: source.Selection.RootID, LocationID: source.Selection.LocationID, WorkID: source.Selection.WorkID,
			MediaVariantID: source.Selection.MediaVariantID, StreamIndex: source.Quality.StreamIndex,
			Codec: source.Quality.Codec, Quality: source.Quality, Title: source.StreamTitle, Language: source.StreamLanguage,
			RequiresExplicitStreamSelection: source.RequiresExplicitStreamSelection,
			AudioStreamCount:                source.AudioStreamCount, UnsupportedAudioStreamCount: source.UnsupportedAudioStreamCount,
		})
	}
	var source *persistence.FirstPublicationSource
	if choice != nil {
		for i := range contextSnapshot.Sources {
			candidate := &contextSnapshot.Sources[i]
			if candidate.Selection.LocationID == choice.LocationID && candidate.Selection.MediaVariantID == choice.MediaVariantID && candidate.Quality.StreamIndex == choice.StreamIndex {
				source = candidate
				break
			}
		}
		if source == nil {
			return FirstPublicationPreview{}, persistence.ErrPublicationAdmissionConflict
		}
	} else if len(contextSnapshot.Sources) == 1 && !contextSnapshot.Sources[0].RequiresExplicitStreamSelection {
		source = &contextSnapshot.Sources[0]
	}
	if source == nil {
		preview.Blockers = append(preview.Blockers, "explicit source and audio stream selection is required")
	} else {
		preview.SelectedSource = &FirstPublicationPreviewSource{
			RootID: source.Selection.RootID, LocationID: source.Selection.LocationID,
			WorkID: source.Selection.WorkID, MediaVariantID: source.Selection.MediaVariantID,
			StreamIndex: source.Quality.StreamIndex, Codec: source.Quality.Codec, Quality: source.Quality,
			Title: source.StreamTitle, Language: source.StreamLanguage,
			RequiresExplicitStreamSelection: source.RequiresExplicitStreamSelection,
			AudioStreamCount:                source.AudioStreamCount, UnsupportedAudioStreamCount: source.UnsupportedAudioStreamCount,
		}
		plan, planErr := publication.PlanAudioTransform(publication.AudioPublicationFormat(format), source.Quality.AudioSourceFacts())
		if planErr != nil {
			preview.Blockers = append(preview.Blockers, "selected source cannot satisfy the configured publication format: "+planErr.Error())
		} else {
			preview.Transform = &plan
		}
	}
	projection, projectionErr := publication.ProjectTags(contextSnapshot.Tags)
	if projectionErr != nil {
		preview.Blockers = append(preview.Blockers, "desired tags are incomplete: "+projectionErr.Error())
	} else {
		preview.DesiredTags = projection
	}
	parsed, parseErr := publication.ParseTemplate(pathTemplate)
	if parseErr != nil {
		preview.Blockers = append(preview.Blockers, "publication path template is invalid: "+parseErr.Error())
	} else if components, renderErr := parsed.Render(func(token string) publication.TokenValue {
		if token == "EXT" {
			if preview.Transform == nil {
				return publication.TokenValue{}
			}
			return publication.TokenValue{Value: preview.Transform.Extension, Present: preview.Transform.Extension != ""}
		}
		if value, handled := firstPublicationPrimaryArtistToken(contextSnapshot, token); handled {
			return value
		}
		value, exists := contextSnapshot.TemplateTokens[token]
		return publication.TokenValue{Value: value, Present: exists}
	}); renderErr != nil {
		preview.Blockers = append(preview.Blockers, "publication path cannot be safely rendered: "+renderErr.Error())
	} else {
		preview.PathComponents = components
		if _, namespaceErr := firstPublicationNamespaceCandidates(contextSnapshot, pathTemplate, components, releaseID, positionID); namespaceErr != nil {
			preview.Blockers = append(preview.Blockers, "publication namespace layout is unsupported: "+namespaceErr.Error())
		}
	}
	boundChoice := choice
	if boundChoice == nil && preview.SelectedSource != nil {
		boundChoice = &FirstPublicationSourceChoice{LocationID: preview.SelectedSource.LocationID, MediaVariantID: preview.SelectedSource.MediaVariantID, StreamIndex: preview.SelectedSource.StreamIndex}
	}
	digestInput := struct {
		Context                    *persistence.LibraryFirstPublicationContext `json:"context"`
		Format                     string                                      `json:"format"`
		Template                   string                                      `json:"template"`
		OutputDirectory            string                                      `json:"output_directory"`
		OutputCaseSensitive        *bool                                       `json:"output_case_sensitive"`
		OutputUnicodeNormalization string                                      `json:"output_unicode_normalization"`
		FFmpeg                     string                                      `json:"ffmpeg_installation"`
		FPCalc                     string                                      `json:"fpcalc_installation"`
		SelectedTools              []persistence.SourceAnalysisToolSelection   `json:"selected_tools"`
		Choice                     *FirstPublicationSourceChoice               `json:"selected_source_choice"`
	}{contextSnapshot, format, pathTemplate, runtimeSettings.OutputDirectory, runtimeSettings.OutputCaseSensitive,
		runtimeSettings.OutputUnicodeNormalization, runtimeSettings.ActiveFFmpegInstallation, runtimeSettings.ActiveFPCalcInstallation, selectedTools, boundChoice}
	encoded, err := json.Marshal(digestInput)
	if err != nil {
		return FirstPublicationPreview{}, fmt.Errorf("encode first publication context digest: %w", err)
	}
	digest := sha256.Sum256(encoded)
	preview.ContextDigest = hex.EncodeToString(digest[:])
	return preview, nil
}

func firstPublicationPrimaryArtistToken(snapshot *persistence.LibraryFirstPublicationContext, token string) (publication.TokenValue, bool) {
	prefix, suffix, ok := strings.Cut(token, "_PRIMARY_ARTIST")
	if !ok || (prefix != "ALBUM" && prefix != "TRACK") {
		return publication.TokenValue{}, false
	}
	var credits []librarymetadata.ArtistCredit
	var selected *uuid.UUID
	if prefix == "ALBUM" {
		credits, selected = snapshot.ReleasePrimaryCredits, snapshot.SelectedReleasePrimaryArtistID
	} else {
		credits, selected = snapshot.PositionPrimaryCredits, snapshot.SelectedPositionPrimaryArtistID
	}
	converted := make([]librarymetadata.CreditedArtist, 0, len(credits))
	for _, credit := range credits {
		artistID, err := uuid.Parse(credit.ArtistID)
		if err != nil {
			return publication.TokenValue{Err: fmt.Errorf("invalid current artist identity: %w", err)}, true
		}
		artist := librarymetadata.CreditedArtist{
			ArtistID: artistID, ArtistName: credit.ArtistName, CreditName: credit.CreditName, JoinPhrase: credit.JoinPhrase,
		}
		if credit.MBID != nil {
			mbid, err := uuid.Parse(*credit.MBID)
			if err != nil {
				return publication.TokenValue{Err: fmt.Errorf("invalid current artist MBID: %w", err)}, true
			}
			if mbid != uuid.Nil {
				artist.MBID = &mbid
			}
		}
		converted = append(converted, artist)
	}
	resolution, err := librarymetadata.ResolvePrimaryArtist(converted, selected)
	if err != nil {
		return publication.TokenValue{Err: err}, true
	}
	if resolution.Artist == nil {
		return publication.TokenValue{}, true
	}
	artist := resolution.Artist
	switch suffix {
	case "":
		return publication.TokenValue{Value: artist.Name, Present: true}, true
	case "_ID":
		return publication.TokenValue{Value: artist.ArtistID.String(), Present: true}, true
	case "_MBID":
		if artist.MBID == nil {
			return publication.TokenValue{}, true
		}
		return publication.TokenValue{Value: artist.MBID.String(), Present: true}, true
	case "_MBID_PART":
		if artist.MBID == nil {
			return publication.TokenValue{}, true
		}
		return publication.TokenValue{Value: artist.MBID.String()[:8], Present: true}, true
	default:
		return publication.TokenValue{}, false
	}
}
