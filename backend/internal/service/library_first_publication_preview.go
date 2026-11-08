package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
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
	Mode                   string                                   `json:"mode"`
	CurrentComparison      *FirstPublicationCurrentComparison       `json:"current_comparison,omitempty"`
	CoverPlan              *FirstPublicationCoverPlan               `json:"cover_plan,omitempty"`
	LyricsPlan             *FirstPublicationLyricsPlan              `json:"lyrics_plan,omitempty"`
	ConfirmationCapability *SourceRepublishConfirmationCapability   `json:"confirmation_capability,omitempty"`
}

type SourceRepublishConfirmationCapability struct {
	Kind              string    `json:"kind"`
	Status            string    `json:"status"`
	ReasonCodes       []string  `json:"reason_codes"`
	CurrentGeneration uuid.UUID `json:"current_generation"`
}

type FirstPublicationCurrentComparison struct {
	Status             string                          `json:"status"`
	Reason             string                          `json:"reason"`
	Generation         uuid.UUID                       `json:"generation"`
	TagDifferences     []FirstPublicationTagDifference `json:"tag_differences"`
	RenderedComponents []string                        `json:"rendered_components"`
	ResolvedComponents []string                        `json:"resolved_components"`
	CurrentComponents  []string                        `json:"current_components"`
	RenderedMatches    bool                            `json:"rendered_matches"`
	ResolvedMatches    bool                            `json:"resolved_matches"`
	AudioStatus        string                          `json:"audio_status"`
	CoverStatus        string                          `json:"cover_status"`
	LyricsStatus       string                          `json:"lyrics_status"`
	AudioEvidence      *FirstPublicationAudioEvidence  `json:"audio_evidence,omitempty"`
}

type FirstPublicationAudioEvidence struct {
	SourceAuthorityStatus     string    `json:"source_authority_status"`
	SourceAuthorityRevision   uuid.UUID `json:"source_authority_revision"`
	SourceAuthorityEpoch      uuid.UUID `json:"source_authority_epoch"`
	SourceAuthorityGeneration uuid.UUID `json:"source_authority_generation"`
	SourceLocationID          uuid.UUID `json:"source_location_id"`
	MediaVariantID            uuid.UUID `json:"media_variant_id"`
	CanonicalSourceSHA256     string    `json:"canonical_source_sha256,omitempty"`
	SelectedSourceLocationID  uuid.UUID `json:"selected_source_location_id"`
	SelectedMediaVariantID    uuid.UUID `json:"selected_media_variant_id"`
	SelectedSourceSHA256      string    `json:"selected_source_sha256,omitempty"`
}

type FirstPublicationTagDifference struct {
	Key            string   `json:"key"`
	Desired        []string `json:"desired"`
	DesiredPresent bool     `json:"desired_present"`
	Current        []string `json:"current"`
	CurrentPresent bool     `json:"current_present"`
}

const (
	firstPublicationPreviewModeOrdinary = "ordinary"
	firstPublicationPreviewModeForced   = "forced"
)

type FirstPublicationPreviewSource struct {
	RootID                          uuid.UUID                       `json:"root_id"`
	LocationID                      uuid.UUID                       `json:"location_id"`
	WorkID                          uuid.UUID                       `json:"work_id"`
	AssociationID                   uuid.UUID                       `json:"-"`
	Selected                        bool                            `json:"-"`
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
	return service.PreviewFirstSelectedMode(ctx, releaseID, positionID, choice, firstPublicationPreviewModeOrdinary)
}

func (service *LibraryPublications) PreviewFirstSelectedMode(ctx context.Context, releaseID, positionID uuid.UUID, choice *FirstPublicationSourceChoice, mode string) (FirstPublicationPreview, error) {
	if mode != firstPublicationPreviewModeOrdinary && mode != firstPublicationPreviewModeForced {
		return FirstPublicationPreview{}, errors.New("first publication preview mode is invalid")
	}
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
	preview, err := buildFirstPublicationPreviewSelectedMode(contextSnapshot, runtimeSettings, selectedTools, releaseID, positionID, choice, mode)
	if err != nil {
		return preview, err
	}
	if contextSnapshot.Library.Current != nil {
		if err := foldFirstPublicationCurrentAudioEvidenceDigest(&preview, contextSnapshot); err != nil {
			return FirstPublicationPreview{}, err
		}
	}
	if contextSnapshot.Library.Current != nil {
		var selected *persistence.FirstPublicationSource
		if preview.SelectedSource != nil {
			for i := range contextSnapshot.Sources {
				if contextSnapshot.Sources[i].AssociationID == preview.SelectedSource.AssociationID && contextSnapshot.Sources[i].Quality.StreamIndex == preview.SelectedSource.StreamIndex {
					selected = &contextSnapshot.Sources[i]
					break
				}
			}
		}
		plan, planErr := planFirstPublicationLyrics(ctx, service.lyrics, selected, initialLyricsAuthorityInput(contextSnapshot), runtimeSettings.LRCLIBEnabled, contextSnapshot.Library.Current)
		if planErr != nil {
			return FirstPublicationPreview{}, planErr
		}
		preview.LyricsPlan = &plan
		if err := foldFirstPublicationLyricsPlanDigest(&preview, plan); err != nil {
			return FirstPublicationPreview{}, err
		}
	}
	if contextSnapshot.Library.Current != nil {
		coverPlan, err := planFirstPublicationCover(ctx, os.TempDir(), contextSnapshot.EffectiveCoverInput)
		if err != nil {
			return FirstPublicationPreview{}, fmt.Errorf("plan first publication cover: %w", err)
		}
		preview.CoverPlan = &coverPlan
		if err := foldFirstPublicationCoverPlanDigest(&preview, coverPlan); err != nil {
			return FirstPublicationPreview{}, err
		}
	}
	current := contextSnapshot.Library.Current
	canResolveNamespace := len(preview.Blockers) == 0 || current != nil && len(preview.PathComponents) != 0
	if canResolveNamespace && service.resolveNamespace != nil && current != nil {
		if _, err := finalizeFirstPublicationCurrentComparisonNamespace(ctx, contextSnapshot, runtimeSettings.OutputDirectory, runtimeSettings.PublicationPathTemplate, &preview); err != nil {
			if ctx.Err() != nil {
				return FirstPublicationPreview{}, ctx.Err()
			}
			preview.Blockers = append(preview.Blockers, "native CURRENT comparison namespace could not be resolved: "+err.Error())
			preview.NativeStatus = "native namespace unavailable"
		}
	} else if canResolveNamespace && service.resolveNamespace != nil {
		if _, err := service.resolveNamespace(ctx, contextSnapshot, runtimeSettings.OutputDirectory, runtimeSettings.PublicationPathTemplate, &preview); err != nil {
			if ctx.Err() != nil {
				return FirstPublicationPreview{}, ctx.Err()
			}
			preview.Blockers = append(preview.Blockers, "native publication namespace could not be resolved: "+err.Error())
			preview.NativeStatus = "native namespace unavailable"
		}
	}
	if current != nil {
		preview.CurrentComparison = compareFirstPublicationCurrent(preview, current, contextSnapshot)
	}
	return preview, nil
}

func foldFirstPublicationCurrentAudioEvidenceDigest(preview *FirstPublicationPreview, snapshot *persistence.LibraryFirstPublicationContext) error {
	var selected *persistence.FirstPublicationSource
	if preview.SelectedSource != nil {
		for i := range snapshot.Sources {
			source := &snapshot.Sources[i]
			if source.Selection.LocationID == preview.SelectedSource.LocationID && source.Selection.MediaVariantID == preview.SelectedSource.MediaVariantID {
				selected = source
				break
			}
		}
	}
	input := struct {
		AuthorityStatus     string    `json:"authority_status"`
		AuthorityRevision   uuid.UUID `json:"authority_revision"`
		AuthorityEpoch      uuid.UUID `json:"authority_epoch"`
		AuthorityGeneration uuid.UUID `json:"authority_generation"`
		AuthorityLocation   uuid.UUID `json:"authority_location"`
		AuthorityVariant    uuid.UUID `json:"authority_variant"`
		AuthoritySHA256     string    `json:"authority_sha256"`
		SelectedLocation    uuid.UUID `json:"selected_location"`
		SelectedVariant     uuid.UUID `json:"selected_variant"`
		SelectedSHA256      string    `json:"selected_sha256"`
	}{}
	if authority := snapshot.Library.Current.SourceAuthority; authority != nil {
		input.AuthorityStatus = authority.Status
		input.AuthorityRevision = authority.Revision
		input.AuthorityEpoch = authority.PublicationEpoch
		input.AuthorityGeneration = authority.PublicationGeneration
		input.AuthorityLocation = authority.SourceLocationID
		input.AuthorityVariant = authority.MediaVariantID
		input.AuthoritySHA256 = digestString(authority.CanonicalSourceSHA256)
	}
	if selected != nil {
		input.SelectedLocation = selected.Selection.LocationID
		input.SelectedVariant = selected.Selection.MediaVariantID
		input.SelectedSHA256 = digestString(selected.CanonicalSourceSHA256)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode first publication CURRENT audio evidence digest: %w", err)
	}
	digest := sha256.Sum256(append(append([]byte("melo:first-publication-current-audio:v1:"+preview.ContextDigest+":"), encoded...), []byte(":relative-namespace:v1")...))
	preview.ContextDigest = hex.EncodeToString(digest[:])
	return nil
}

// buildFirstPublicationPreview performs deterministic planning from captured
// inputs only. Callers that admit a publication can use the same builder with
// the tool selections captured by their transaction.
func buildFirstPublicationPreview(contextSnapshot *persistence.LibraryFirstPublicationContext, runtimeSettings settings.RuntimeSettings, selectedTools []persistence.SourceAnalysisToolSelection, releaseID, positionID uuid.UUID) (FirstPublicationPreview, error) {
	return buildFirstPublicationPreviewSelected(contextSnapshot, runtimeSettings, selectedTools, releaseID, positionID, nil)
}

func buildFirstPublicationPreviewSelected(contextSnapshot *persistence.LibraryFirstPublicationContext, runtimeSettings settings.RuntimeSettings, selectedTools []persistence.SourceAnalysisToolSelection, releaseID, positionID uuid.UUID, choice *FirstPublicationSourceChoice) (FirstPublicationPreview, error) {
	return buildFirstPublicationPreviewSelectedMode(contextSnapshot, runtimeSettings, selectedTools, releaseID, positionID, choice, firstPublicationPreviewModeOrdinary)
}

func buildFirstPublicationPreviewSelectedMode(contextSnapshot *persistence.LibraryFirstPublicationContext, runtimeSettings settings.RuntimeSettings, selectedTools []persistence.SourceAnalysisToolSelection, releaseID, positionID uuid.UUID, choice *FirstPublicationSourceChoice, mode string) (FirstPublicationPreview, error) {
	return buildFirstPublicationPreviewSelectedModeWithCurrentPolicy(contextSnapshot, runtimeSettings, selectedTools, releaseID, positionID, choice, mode, false)
}

func buildFirstPublicationRepublishPreviewSelectedMode(contextSnapshot *persistence.LibraryFirstPublicationContext, runtimeSettings settings.RuntimeSettings, selectedTools []persistence.SourceAnalysisToolSelection, releaseID, positionID uuid.UUID, choice *FirstPublicationSourceChoice, mode string) (FirstPublicationPreview, error) {
	if contextSnapshot == nil || contextSnapshot.Library == nil || contextSnapshot.Library.Current == nil {
		return FirstPublicationPreview{}, persistence.ErrPublicationAdmissionConflict
	}
	return buildFirstPublicationPreviewSelectedModeWithCurrentPolicy(contextSnapshot, runtimeSettings, selectedTools, releaseID, positionID, choice, mode, true)
}

func buildFirstPublicationPreviewSelectedModeWithCurrentPolicy(contextSnapshot *persistence.LibraryFirstPublicationContext, runtimeSettings settings.RuntimeSettings, selectedTools []persistence.SourceAnalysisToolSelection, releaseID, positionID uuid.UUID, choice *FirstPublicationSourceChoice, mode string, allowExistingCurrent bool) (FirstPublicationPreview, error) {
	format, pathTemplate := runtimeSettings.PublicationFormat, runtimeSettings.PublicationPathTemplate
	if contextSnapshot == nil || contextSnapshot.Library == nil {
		return FirstPublicationPreview{}, errors.New("first publication context is incomplete")
	}
	library := contextSnapshot.Library
	automaticChoice := choice == nil
	automaticReason := ""
	if automaticChoice {
		selected, reason, err := automaticFirstSourceChoiceWithReason(contextSnapshot, publication.AudioPublicationFormat(format))
		if err == nil {
			choice = &selected
		} else {
			automaticReason = reason
		}
	}
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
		Mode:         mode,
	}
	if automaticChoice && choice == nil {
		preview.Blockers = append(preview.Blockers, contextSnapshot.DefaultSourceBlockers...)
		switch automaticReason {
		case "manual_stream_selection":
			preview.Blockers = append(preview.Blockers, "explicit source and audio stream selection is required")
		case "ambiguous_quality":
			preview.Blockers = append(preview.Blockers, "automatic source quality is ambiguous")
		}
	}
	if library.Current != nil && mode != firstPublicationPreviewModeForced && !allowExistingCurrent {
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
			AssociationID: source.AssociationID, Selected: source.Selected,
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
	} else {
		selectedSources := make([]*persistence.FirstPublicationSource, 0, len(contextSnapshot.Sources))
		for i := range contextSnapshot.Sources {
			if contextSnapshot.Sources[i].Selected {
				selectedSources = append(selectedSources, &contextSnapshot.Sources[i])
			}
		}
		if len(selectedSources) == 1 && !selectedSources[0].RequiresExplicitStreamSelection {
			source = selectedSources[0]
		}
	}
	if source == nil {
		preview.Blockers = append(preview.Blockers, "explicit source and audio stream selection is required")
	} else {
		preview.SelectedSource = &FirstPublicationPreviewSource{
			RootID: source.Selection.RootID, LocationID: source.Selection.LocationID,
			WorkID: source.Selection.WorkID, MediaVariantID: source.Selection.MediaVariantID,
			AssociationID: source.AssociationID, Selected: source.Selected,
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
		AbandonedNamespaceClaims   []uuid.UUID                                 `json:"abandoned_namespace_claims"`
		Format                     string                                      `json:"format"`
		Template                   string                                      `json:"template"`
		OutputDirectory            string                                      `json:"output_directory"`
		OutputCaseSensitive        *bool                                       `json:"output_case_sensitive"`
		OutputUnicodeNormalization string                                      `json:"output_unicode_normalization"`
		FFmpeg                     string                                      `json:"ffmpeg_installation"`
		FPCalc                     string                                      `json:"fpcalc_installation"`
		SelectedTools              []persistence.SourceAnalysisToolSelection   `json:"selected_tools"`
		Choice                     *FirstPublicationSourceChoice               `json:"selected_source_choice"`
		Mode                       string                                      `json:"mode"`
	}{contextSnapshot, contextSnapshot.Library.AbandonedNamespaceClaims, format, pathTemplate, runtimeSettings.OutputDirectory, runtimeSettings.OutputCaseSensitive,
		runtimeSettings.OutputUnicodeNormalization, runtimeSettings.ActiveFFmpegInstallation, runtimeSettings.ActiveFPCalcInstallation, selectedTools, boundChoice, mode}
	encoded, err := json.Marshal(digestInput)
	if err != nil {
		return FirstPublicationPreview{}, fmt.Errorf("encode first publication context digest: %w", err)
	}
	digest := sha256.Sum256(encoded)
	preview.ContextDigest = hex.EncodeToString(digest[:])
	return preview, nil
}

func compareFirstPublicationCurrent(preview FirstPublicationPreview, current *persistence.LibraryPublicationCurrent, snapshot *persistence.LibraryFirstPublicationContext) *FirstPublicationCurrentComparison {
	comparison := &FirstPublicationCurrentComparison{
		Status: "blocked", Reason: "comparison is read-only; typed source replacement and native proof of embedded assets are not available",
		Generation: current.Record.Generation, RenderedComponents: append([]string(nil), preview.PathComponents...),
		ResolvedComponents: append([]string(nil), preview.ResolvedPathComponents...),
		CurrentComponents:  strings.Split(current.Record.RelativePath, "/"), AudioStatus: "unavailable", CoverStatus: "unavailable", LyricsStatus: "unavailable",
	}
	var selected *persistence.FirstPublicationSource
	if preview.SelectedSource != nil && snapshot != nil {
		for i := range snapshot.Sources {
			source := &snapshot.Sources[i]
			if source.Selection.LocationID == preview.SelectedSource.LocationID && source.Selection.MediaVariantID == preview.SelectedSource.MediaVariantID {
				selected = source
				break
			}
		}
	}
	if authority := current.SourceAuthority; authority != nil {
		evidence := &FirstPublicationAudioEvidence{
			SourceAuthorityStatus: authority.Status, SourceAuthorityRevision: authority.Revision,
			SourceAuthorityEpoch: authority.PublicationEpoch, SourceAuthorityGeneration: authority.PublicationGeneration,
			SourceLocationID: authority.SourceLocationID, MediaVariantID: authority.MediaVariantID,
			CanonicalSourceSHA256: digestString(authority.CanonicalSourceSHA256),
		}
		if selected != nil {
			evidence.SelectedSourceLocationID = selected.Selection.LocationID
			evidence.SelectedMediaVariantID = selected.Selection.MediaVariantID
			evidence.SelectedSourceSHA256 = digestString(selected.CanonicalSourceSHA256)
		}
		comparison.AudioEvidence = evidence
		if authority.Status == "present" && selected != nil {
			switch {
			case authority.MediaVariantID != uuid.Nil &&
				selected.Selection.MediaVariantID != uuid.Nil &&
				authority.MediaVariantID == selected.Selection.MediaVariantID:
				comparison.AudioStatus = "matches"
			case len(authority.CanonicalSourceSHA256) == sha256.Size && len(selected.CanonicalSourceSHA256) == sha256.Size:
				if string(authority.CanonicalSourceSHA256) == string(selected.CanonicalSourceSHA256) {
					comparison.AudioStatus = "matches"
				} else {
					comparison.AudioStatus = "differs"
				}
			}
		}
	}
	if preview.LyricsPlan != nil {
		comparison.LyricsStatus = preview.LyricsPlan.CurrentStatus
	}
	comparison.RenderedMatches = strings.Join(comparison.RenderedComponents, "/") == current.Record.RelativePath
	comparison.ResolvedMatches = strings.Join(comparison.ResolvedComponents, "/") == current.Record.RelativePath
	if preview.CoverPlan != nil {
		plan := preview.CoverPlan
		asset := current.Assets.Cover
		switch plan.Derivation {
		case "absent":
			if asset.State == persistence.PublicationAssetAbsent {
				comparison.CoverStatus = "matches"
			} else {
				comparison.CoverStatus = "differs"
			}
		case "ready":
			facts := plan.Derivative
			if asset.State == persistence.PublicationAssetEmbedded && facts != nil && asset.SHA256 == facts.SHA256 && asset.MIME == facts.MIME && asset.SizeBytes != nil && *asset.SizeBytes == facts.Size {
				comparison.CoverStatus = "matches"
			} else if asset.State == persistence.PublicationAssetEmbedded {
				comparison.CoverStatus = "differs"
			} else {
				comparison.CoverStatus = "differs"
			}
		case "optional_failure":
			comparison.CoverStatus = "unavailable"
		}
	}
	desired := preview.DesiredTags.Tags
	actual := current.Metadata.Tags
	keys := make(map[string]struct{}, len(desired)+len(actual))
	for key := range desired {
		if preview.LyricsPlan != nil && strings.EqualFold(strings.TrimSpace(key), "LYRICS") {
			continue
		}
		keys[key] = struct{}{}
	}
	for key := range actual {
		if preview.LyricsPlan != nil && strings.EqualFold(strings.TrimSpace(key), "LYRICS") {
			continue
		}
		keys[key] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	for _, key := range sorted {
		desiredValues, desiredPresent := desired[key]
		currentValues, currentPresent := actual[key]
		if desiredPresent != currentPresent || !reflect.DeepEqual(desiredValues, currentValues) {
			comparison.TagDifferences = append(comparison.TagDifferences, FirstPublicationTagDifference{
				Key: key, Desired: cloneTagValues(desiredValues), DesiredPresent: desiredPresent,
				Current: cloneTagValues(currentValues), CurrentPresent: currentPresent,
			})
		}
	}
	if comparison.TagDifferences == nil {
		comparison.TagDifferences = []FirstPublicationTagDifference{}
	}
	return comparison
}

func digestString(digest []byte) string {
	if len(digest) == 0 {
		return ""
	}
	return hex.EncodeToString(digest)
}

func foldFirstPublicationCoverPlanDigest(preview *FirstPublicationPreview, plan FirstPublicationCoverPlan) error {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode first publication cover plan digest: %w", err)
	}
	digest := sha256.Sum256(append(append([]byte("melo:first-publication-cover-plan:v1:"+preview.ContextDigest+":"), encoded...), []byte(":relative-namespace:v1")...))
	preview.ContextDigest = hex.EncodeToString(digest[:])
	return nil
}

func cloneTagValues(values []string) []string {
	if values == nil {
		return nil
	}
	return append(make([]string, 0, len(values)), values...)
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
