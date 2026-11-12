package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

var errSourceRepublishUnchanged = errors.New("source republish has no content changes to apply")

// SourceRepublishConfirmation is an internal confirmation contract. It is not
// generated from or exposed by an HTTP request.
type SourceRepublishConfirmation struct {
	PreviewDigest      string
	ExpectedEpoch      uuid.UUID
	ExpectedGeneration uuid.UUID
	Mode               string
}

type sourceRepublishAdmissionRepository interface {
	firstPublicationContextReader
	ReadPositionSourceAuthority(context.Context, uuid.UUID) (*persistence.PositionSourceAuthority, error)
	AdmitSourceRepublishRetainingSelection(context.Context, persistence.SourceRepublishRetainAdmission, persistence.RiverInserter) (uuid.UUID, error)
}

type retainedSourceRepublishPreparation struct {
	Repository         sourceRepublishAdmissionRepository
	Snapshot           *persistence.LibraryFirstPublicationContext
	RuntimeSettings    settings.RuntimeSettings
	Authority          *persistence.PositionSourceAuthority
	SelectedTools      []persistence.SourceAnalysisToolSelection
	Selected           *persistence.FirstPublicationSource
	Choice             FirstPublicationSourceChoice
	Preview            FirstPublicationPreview
	Manifest           []persistence.PublicationNamespaceNode
	LyricsAuthority    *persistence.PublicationInitialLyricsInput
	LyricsPlan         FirstPublicationLyricsPlan
	CoverPlan          FirstPublicationCoverPlan
	Input              persistence.PublicationInputs
	FrozenInputs       []byte
	BasePlanningDigest string
}

func (service *LibraryPublications) PreviewSourceRepublishRetainingSelection(
	ctx context.Context,
	releaseID, positionID uuid.UUID,
	mode string,
) (FirstPublicationPreview, error) {
	if service == nil || service.repository == nil || service.settings == nil || releaseID == uuid.Nil || positionID == uuid.Nil ||
		(mode != firstPublicationPreviewModeOrdinary && mode != firstPublicationPreviewModeForced) {
		return blockedSourceRepublishPreview(FirstPublicationPreview{}, releaseID, positionID, mode, "source_republish_not_eligible"), nil
	}
	prepared, err := service.prepareRetainedSourceRepublish(ctx, releaseID, positionID, mode)
	if err != nil {
		if errors.Is(err, persistence.ErrPublicationAdmissionConflict) {
			return blockedSourceRepublishPreview(prepared.Preview, releaseID, positionID, mode, "source_republish_not_eligible"), nil
		}
		return FirstPublicationPreview{}, err
	}
	return prepared.Preview, nil
}

func blockedSourceRepublishPreview(preview FirstPublicationPreview, releaseID, positionID uuid.UUID, mode, reason string) FirstPublicationPreview {
	if preview.ReleaseID == uuid.Nil {
		preview.ReleaseID = releaseID
	}
	if preview.PositionID == uuid.Nil {
		preview.PositionID = positionID
	}
	if preview.Mode == "" {
		preview.Mode = mode
	}
	if preview.Blockers == nil {
		preview.Blockers = []string{}
	}
	if preview.Warnings == nil {
		preview.Warnings = []string{}
	}
	if preview.SourceCandidates == nil {
		preview.SourceCandidates = []FirstPublicationPreviewSource{}
	}
	preview.ConfirmationCapability = &SourceRepublishConfirmationCapability{
		Kind: "retained_same_path", Status: "blocked", ReasonCodes: []string{reason},
		CurrentGeneration: currentRepublishGeneration(preview.Current),
	}
	return preview
}

func currentRepublishGeneration(current *persistence.LibraryPublicationCurrent) uuid.UUID {
	if current == nil {
		return uuid.Nil
	}
	return current.Record.Generation
}

func sourceRepublishEvidencePreview(
	snapshot *persistence.LibraryFirstPublicationContext,
	releaseID, positionID uuid.UUID,
	mode string,
) FirstPublicationPreview {
	preview := FirstPublicationPreview{
		ReleaseID: releaseID, PositionID: positionID, Mode: mode,
		MetadataRevisions: map[string]int64{}, Warnings: []string{}, Blockers: []string{}, SourceCandidates: []FirstPublicationPreviewSource{},
	}
	if snapshot == nil || snapshot.Library == nil {
		return preview
	}
	preview.Epoch = snapshot.Library.ActiveEpoch
	preview.Current = snapshot.Library.Current
	preview.ActiveAttempt = snapshot.Library.ActiveAttempt
	preview.MetadataRevisions = map[string]int64{
		"release": snapshot.Metadata.Release.Revision, "track": snapshot.Metadata.Track.Revision,
		"recording": snapshot.Metadata.Recording.Revision, "position": snapshot.Metadata.Position.Revision,
	}
	addArtistMetadataRevisions(preview.MetadataRevisions, snapshot.ArtistMetadataRevisions)
	preview.Blockers = append(preview.Blockers, snapshot.Blockers...)
	preview.SourceCandidates = make([]FirstPublicationPreviewSource, 0, len(snapshot.Sources))
	for _, source := range snapshot.Sources {
		preview.SourceCandidates = append(preview.SourceCandidates, FirstPublicationPreviewSource{
			RootID: source.Selection.RootID, LocationID: source.Selection.LocationID, WorkID: source.Selection.WorkID,
			AssociationID: source.AssociationID, Selected: source.Selected,
			MediaVariantID: source.Selection.MediaVariantID, StreamIndex: source.Quality.StreamIndex,
			Codec: source.Quality.Codec, Quality: source.Quality, Title: source.StreamTitle, Language: source.StreamLanguage,
			RequiresExplicitStreamSelection: source.RequiresExplicitStreamSelection,
			AudioStreamCount:                source.AudioStreamCount, UnsupportedAudioStreamCount: source.UnsupportedAudioStreamCount,
		})
	}
	return preview
}

// AdmitSourceRepublishRetainingSelection freezes a same-path CURRENT republish
// from the already-selected source and admits it through the typed persistence
// capability. Native reads and optional-asset planning finish before the
// transaction callback, which only compares captured authoritative facts.
func (service *LibraryPublications) AdmitSourceRepublishRetainingSelection(
	ctx context.Context,
	releaseID, positionID uuid.UUID,
	confirmation SourceRepublishConfirmation,
) (uuid.UUID, error) {
	if service == nil || service.repository == nil || service.settings == nil || service.river == nil ||
		releaseID == uuid.Nil || positionID == uuid.Nil || confirmation.ExpectedEpoch == uuid.Nil || confirmation.ExpectedGeneration == uuid.Nil ||
		!validFirstPublicationPreviewDigest(confirmation.PreviewDigest) ||
		(confirmation.Mode != firstPublicationPreviewModeOrdinary && confirmation.Mode != firstPublicationPreviewModeForced) {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	prepared, err := service.prepareRetainedSourceRepublish(ctx, releaseID, positionID, confirmation.Mode)
	if err != nil {
		return uuid.Nil, err
	}
	if prepared.Preview.ConfirmationCapability == nil || prepared.Preview.ConfirmationCapability.Status != "ready" {
		if prepared.Preview.ConfirmationCapability != nil && prepared.Preview.ConfirmationCapability.Status == "unchanged" {
			return uuid.Nil, fmt.Errorf("admit unchanged source republish: %w", errors.Join(persistence.ErrPublicationAdmissionConflict, errSourceRepublishUnchanged))
		}
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	if prepared.Snapshot == nil || prepared.Snapshot.Library == nil || prepared.Snapshot.Library.Current == nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	if prepared.Snapshot.Library.ActiveEpoch != confirmation.ExpectedEpoch ||
		prepared.Snapshot.Library.Current.Record.Generation != confirmation.ExpectedGeneration ||
		prepared.Preview.ContextDigest != confirmation.PreviewDigest {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	repository := prepared.Repository
	snapshot := prepared.Snapshot
	runtimeSettings := prepared.RuntimeSettings
	authority := prepared.Authority
	selectedTools := prepared.SelectedTools
	selected := prepared.Selected
	choice := prepared.Choice
	preview := prepared.Preview
	lyricsAuthority := prepared.LyricsAuthority
	lyricsPlan := prepared.LyricsPlan
	coverPlan := prepared.CoverPlan
	input := prepared.Input
	lyricsAuthorityValue := persistence.PublicationInitialLyricsInput{}
	if lyricsAuthority != nil {
		lyricsAuthorityValue = *lyricsAuthority
	}
	lookup := publicationRepublishLyricsLookup(selected.Input.AnalysedMetadata, runtimeSettings.LRCLIBEnabled)
	current := snapshot.Library.Current
	frozenInputs := prepared.FrozenInputs
	basePlanningDigest := prepared.BasePlanningDigest
	holdID := uuid.New()
	selection := selected.Selection
	selection.HoldUUID = holdID
	generation := current.Record.Generation
	request := persistence.PublicationAdmission{
		PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: prepared.Snapshot.Library.ActiveEpoch,
		ExpectedGeneration: &generation, CandidateGeneration: uuid.New(), IntentKind: "audio", ToolsReadRequired: true,
		Tools: append([]persistence.SourceAnalysisToolSelection(nil), selectedTools...), Source: &selection,
		FrozenInputs: frozenInputs, NamespaceManifest: prepared.Manifest,
	}
	prepare := func(ctx context.Context, authoritative persistence.FirstPublicationAdmissionSnapshot, actual persistence.PublicationAdmission) ([]persistence.PublicationNamespaceNode, error) {
		if authoritative.Context == nil || authoritative.Context.Library == nil || authoritative.Context.Library.Current == nil ||
			authoritative.Context.Library.ActiveEpoch != prepared.Snapshot.Library.ActiveEpoch || actual.PositionID != positionID ||
			actual.ExpectedReleaseID != releaseID || actual.ExpectedEpoch != confirmation.ExpectedEpoch ||
			actual.ExpectedGeneration == nil || *actual.ExpectedGeneration != generation ||
			*actual.ExpectedGeneration != confirmation.ExpectedGeneration ||
			actual.Source == nil || actual.CandidateGeneration == uuid.Nil {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		if !bytes.Equal(actual.FrozenInputs, frozenInputs) {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		var admittedInputs persistence.PublicationInputs
		if err := json.Unmarshal(actual.FrozenInputs, &admittedInputs); err != nil {
			return nil, fmt.Errorf("decode admitted source-republish inputs: %w", err)
		}
		if admittedInputs.SourceRepublish == nil || admittedInputs.SourceRepublish.Mode != confirmation.Mode {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		transactionSettings, settingsMatch, err := matchSourceRepublishPlanningSettings(runtimeSettings, authoritative.Settings)
		if err != nil {
			return nil, fmt.Errorf("parse authoritative source-republish settings: %w", err)
		}
		if !settingsMatch {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		var installation *persistence.ToolInstallation
		for i := range authoritative.Installations {
			if authoritative.Installations[i].ID.String() == transactionSettings.ActiveFFmpegInstallation {
				installation = &authoritative.Installations[i]
				break
			}
		}
		transactionTools, err := selectedFFmpegPairFromInstallation(installation, service.platform)
		if err != nil || !reflect.DeepEqual(transactionTools, selectedTools) {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		transactionContext := authoritative.Context
		transactionContext.NamespaceClaims = append([]persistence.PublicationNamespaceClaim(nil), authoritative.Claims...)
		if transactionContext.Library.Current.Record.Generation != current.Record.Generation ||
			transactionContext.Library.Current.Record.RelativePath != current.Record.RelativePath ||
			transactionContext.Library.Current.Record.Epoch != current.Record.Epoch ||
			!reflect.DeepEqual(transactionContext.Library.Current.Ownership.Identity, input.SourceRepublish.Current.Ownership) ||
			!reflect.DeepEqual(transactionContext.Library.Current.Audio.Facts, input.SourceRepublish.Current.AudioFacts) ||
			!reflect.DeepEqual(transactionContext.Library.Current.Metadata.Tags, input.SourceRepublish.Current.Tags) ||
			!reflect.DeepEqual(transactionContext.Library.Current.Assets, input.SourceRepublish.Current.Assets) ||
			!sameInitialLyricsAuthority(transactionContext, &lyricsAuthorityValue) ||
			!sameRepublishSourceAuthority(transactionContext.Library.Current.SourceAuthority, authority) ||
			!sameRepublishCoverAuthority(transactionContext.EffectiveCoverInput, snapshot.EffectiveCoverInput) {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		var transactionSource *persistence.FirstPublicationSource
		for i := range transactionContext.Sources {
			candidate := &transactionContext.Sources[i]
			if candidate.Selected && candidate.AssociationID == selected.AssociationID &&
				candidate.Selection.LocationID == selected.Selection.LocationID && candidate.Selection.MediaVariantID == selected.Selection.MediaVariantID &&
				candidate.Quality.StreamIndex == selected.Quality.StreamIndex {
				transactionSource = candidate
				break
			}
		}
		var transactionTags publication.TagProjection
		var tagErr error
		if transactionSource != nil {
			transactionPlan, planErr := publication.PlanAudioTransform(publication.AudioPublicationFormat(transactionSettings.PublicationFormat), transactionSource.Quality.AudioSourceFacts())
			if planErr != nil {
				tagErr = planErr
			} else {
				tagInput := transactionContext.Tags
				tagInput.OutputContainer = transactionPlan.OutputContainer
				transactionTags, tagErr = publication.ProjectTags(tagInput)
				if tagErr == nil {
					var sourceTags map[string][]string
					if transactionSource.Input.AnalysedMetadata != nil {
						sourceTags = transactionSource.Input.AnalysedMetadata.Tags
					}
					transactionTags.Tags = publication.ProjectSourceEncoderTags(transactionTags.Tags, sourceTags, transactionPlan.Action)
				}
			}
		}
		if transactionSource == nil || tagErr != nil || !reflect.DeepEqual(audioInputCopy(&transactionSource.Input), input.Audio) ||
			!reflect.DeepEqual(cloneDesiredPublicationTags(transactionTags.Tags), input.DesiredTags) {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		if !sameRepublishLyricsLookup(lookup, transactionSource.Input.AnalysedMetadata, transactionSettings.LRCLIBEnabled) ||
			!sameRepublishLyricsAuthority(transactionContext, input.SourceRepublish.LyricsAuthority) {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		purePreview, err := buildFirstPublicationRepublishPreviewSelectedMode(transactionContext, transactionSettings, transactionTools, releaseID, positionID, &choice, confirmation.Mode)
		if err != nil || purePreview.ContextDigest != basePlanningDigest || len(purePreview.Blockers) != 0 || purePreview.SelectedSource == nil || purePreview.Transform == nil ||
			purePreview.SelectedSource.LocationID != choice.LocationID || purePreview.SelectedSource.MediaVariantID != choice.MediaVariantID ||
			purePreview.SelectedSource.StreamIndex != choice.StreamIndex || !reflect.DeepEqual(purePreview.Transform, preview.Transform) ||
			!reflect.DeepEqual(purePreview.DesiredTags.Tags, input.DesiredTags) {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		if err := foldFirstPublicationCurrentAudioEvidenceDigest(&purePreview, transactionContext); err != nil {
			return nil, err
		}
		if err := foldFirstPublicationLyricsPlanDigest(&purePreview, lyricsPlan); err != nil {
			return nil, err
		}
		if err := foldFirstPublicationCoverPlanDigest(&purePreview, coverPlan); err != nil {
			return nil, err
		}
		purePreview.LyricsPlan = &lyricsPlan
		purePreview.CoverPlan = &coverPlan
		transactionManifest, err := finalizeFirstPublicationCurrentComparisonNamespace(
			ctx,
			transactionContext,
			transactionSettings.OutputDirectory,
			transactionSettings.PublicationPathTemplate,
			&purePreview,
		)
		if err != nil {
			return nil, fmt.Errorf("resolve admitted source-republish namespace: %w", err)
		}
		pureComparison := compareFirstPublicationCurrent(purePreview, transactionContext.Library.Current, transactionContext)
		purePreview.CurrentComparison = pureComparison
		capability := &SourceRepublishConfirmationCapability{
			Kind: "retained_same_path", Status: "ready", ReasonCodes: []string{}, CurrentGeneration: current.Record.Generation,
		}
		unchanged := sourceRepublishIsUnchanged(
			purePreview,
			transactionSource,
			transactionContext.Library.Current,
			publication.AudioPublicationFormat(transactionSettings.PublicationFormat),
		)
		if unchanged {
			capability.Status = "unchanged"
			capability.ReasonCodes = []string{"content_unchanged"}
		}
		purePreview.ConfirmationCapability = capability
		confirmationDigest, err := retainedSourceRepublishDigest(
			purePreview,
			basePlanningDigest,
			actual.FrozenInputs,
			transactionManifest,
			transactionSettings,
			transactionTools,
		)
		if err != nil {
			return nil, fmt.Errorf("digest admitted source-republish confirmation: %w", err)
		}
		if confirmationDigest != confirmation.PreviewDigest {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		if unchanged {
			return nil, fmt.Errorf("admit unchanged source republish: %w", errors.Join(persistence.ErrPublicationAdmissionConflict, errSourceRepublishUnchanged))
		}
		return append([]persistence.PublicationNamespaceNode(nil), transactionManifest...), nil
	}
	return repository.AdmitSourceRepublishRetainingSelection(ctx, persistence.SourceRepublishRetainAdmission{
		PublicationAdmission: request, Prepare: prepare,
	}, service.river)
}

func sourceRepublishIsUnchanged(
	preview FirstPublicationPreview,
	selected *persistence.FirstPublicationSource,
	current *persistence.LibraryPublicationCurrent,
	format publication.AudioPublicationFormat,
) bool {
	if preview.Mode != firstPublicationPreviewModeOrdinary || preview.CurrentComparison == nil {
		return false
	}
	comparison := preview.CurrentComparison
	audio := comparison.AudioEvidence
	return comparison.Generation != uuid.Nil && audio != nil && audio.SourceAuthorityStatus == "present" &&
		audio.SourceAuthorityRevision != uuid.Nil && audio.SourceAuthorityEpoch != uuid.Nil && audio.SourceAuthorityGeneration == comparison.Generation &&
		audio.SourceLocationID != uuid.Nil && audio.MediaVariantID != uuid.Nil &&
		audio.SelectedSourceLocationID == audio.SourceLocationID && audio.SelectedMediaVariantID == audio.MediaVariantID &&
		sourceRepublishCopyAudioMatches(preview.Transform, selected, current, format) &&
		comparison.AudioStatus == "matches" && comparison.TagDifferences != nil && len(comparison.TagDifferences) == 0 && comparison.CoverStatus == "matches" &&
		comparison.LyricsStatus == "matches" && comparison.RenderedMatches && comparison.ResolvedMatches &&
		len(comparison.RenderedComponents) > 0 && len(comparison.ResolvedComponents) > 0 && len(comparison.CurrentComponents) > 0
}

func sourceRepublishOutputQualityComparison(
	transform *publication.AudioTransformPlan,
	selected *persistence.FirstPublicationSource,
	current *persistence.LibraryPublicationCurrent,
	format publication.AudioPublicationFormat,
) publication.QualityComparison {
	if transform == nil || selected == nil || current == nil {
		return publication.QualityUnknown
	}
	planned, err := publication.PlanAudioTransform(format, selected.Quality.AudioSourceFacts())
	if err != nil || !reflect.DeepEqual(planned, *transform) {
		return publication.QualityUnknown
	}
	output := selected.Quality
	switch transform.Action {
	case publication.AudioCodecCopy:
	case publication.AudioCodecALAC:
		lossless := true
		output.Codec, output.Lossless = "alac", &lossless
	case publication.AudioCodecAAC:
		lossless, bitrate := false, transform.Bitrate
		output.Codec, output.Lossless, output.Bitrate = "aac", &lossless, &bitrate
	default:
		return publication.QualityUnknown
	}
	return publication.CompareQuality(audioUpgradeQualityCandidate(output), audioUpgradeQualityCandidate(current.Audio.Facts))
}

func sourceRepublishQualityWarningCode(comparison publication.QualityComparison) string {
	if comparison == publication.QualityWorse {
		return "output_quality_would_downgrade"
	}
	return ""
}

func retainedSourceRepublishDigest(
	preview FirstPublicationPreview,
	planningDigest string,
	frozenInputs []byte,
	manifest []persistence.PublicationNamespaceNode,
	runtimeSettings settings.RuntimeSettings,
	tools []persistence.SourceAnalysisToolSelection,
) (string, error) {
	semanticNamespace := make([]semanticNamespaceNode, 0, len(manifest))
	for _, node := range manifest {
		kind := node.NodeKind
		if kind == "dir" {
			kind = "directory"
		}
		semanticNamespace = append(semanticNamespace, semanticNamespaceNode{
			Path: node.RelativePath, Kind: kind, Scope: node.DirectoryScope,
		})
	}
	sort.Slice(semanticNamespace, func(i, j int) bool {
		if semanticNamespace[i].Path != semanticNamespace[j].Path {
			return semanticNamespace[i].Path < semanticNamespace[j].Path
		}
		if semanticNamespace[i].Kind != semanticNamespace[j].Kind {
			return semanticNamespace[i].Kind < semanticNamespace[j].Kind
		}
		return semanticNamespace[i].Scope < semanticNamespace[j].Scope
	})
	payload, err := json.Marshal(struct {
		Domain         string
		Mode           string
		PlanningDigest string
		FrozenInputs   json.RawMessage
		Namespace      []semanticNamespaceNode
		Settings       settings.RuntimeSettings
		Tools          []persistence.SourceAnalysisToolSelection
		Transform      *publication.AudioTransformPlan
		Comparison     *FirstPublicationCurrentComparison
		Capability     *SourceRepublishConfirmationCapability
	}{
		Domain: "melo:source-republish:retained-same-path:confirmation:v1", Mode: preview.Mode,
		PlanningDigest: planningDigest, FrozenInputs: frozenInputs, Namespace: semanticNamespace,
		Settings: runtimeSettings, Tools: tools, Transform: preview.Transform,
		Comparison: preview.CurrentComparison, Capability: preview.ConfirmationCapability,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func matchSourceRepublishPlanningSettings(expected settings.RuntimeSettings, values map[string]string) (settings.RuntimeSettings, bool, error) {
	actual, err := settings.ParsePublicationPlanningValues(values)
	if err != nil {
		return settings.RuntimeSettings{}, false, err
	}
	return actual, reflect.DeepEqual(actual, expected), nil
}

func sourceRepublishCopyAudioMatches(
	transform *publication.AudioTransformPlan,
	selected *persistence.FirstPublicationSource,
	current *persistence.LibraryPublicationCurrent,
	format publication.AudioPublicationFormat,
) bool {
	if transform == nil || selected == nil || current == nil || transform.Action != publication.AudioCodecCopy {
		return false
	}
	planned, err := publication.PlanAudioTransform(format, selected.Quality.AudioSourceFacts())
	if err != nil || !reflect.DeepEqual(planned, *transform) {
		return false
	}
	source, actual := selected.Quality, current.Audio.Facts
	return source.Codec != "" && source.Codec == actual.Codec && source.Container != "" && source.Container == transform.OutputContainer && actual.Container == transform.OutputContainer &&
		source.Lossless != nil && actual.Lossless != nil && *source.Lossless == *actual.Lossless &&
		source.Channels != nil && actual.Channels != nil && *source.Channels == *actual.Channels &&
		source.SampleRate != nil && actual.SampleRate != nil && *source.SampleRate == *actual.SampleRate &&
		source.BitDepth != nil && actual.BitDepth != nil && *source.BitDepth == *actual.BitDepth &&
		source.ChannelLayout != "" && source.ChannelLayout == actual.ChannelLayout &&
		source.Bitrate != nil && actual.Bitrate != nil && *source.Bitrate == *actual.Bitrate
}

func (service *LibraryPublications) prepareRetainedSourceRepublish(
	ctx context.Context,
	releaseID, positionID uuid.UUID,
	mode string,
) (retainedSourceRepublishPreparation, error) {
	repository, ok := service.repository.(sourceRepublishAdmissionRepository)
	if !ok {
		return retainedSourceRepublishPreparation{}, errors.New("source-republish admission is unavailable")
	}
	planningSettings, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return retainedSourceRepublishPreparation{}, errors.New("source-republish settings are unavailable")
	}
	snapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("read source-republish context: %w", err)
	}
	runtimeSettings, err := planningSettings.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("read source-republish settings: %w", err)
	}
	if snapshot == nil || snapshot.Library == nil || snapshot.Library.Current == nil ||
		snapshot.Library.ReleaseID != releaseID || snapshot.Library.PositionID != positionID ||
		snapshot.Library.ActiveEpoch == uuid.Nil || snapshot.Library.ActiveAttempt != nil {
		return retainedSourceRepublishPreparation{Preview: sourceRepublishEvidencePreview(snapshot, releaseID, positionID, mode)}, fmt.Errorf("validate retained source-republish library context: %w", persistence.ErrPublicationAdmissionConflict)
	}
	evidencePreview := sourceRepublishEvidencePreview(snapshot, releaseID, positionID, mode)
	current := snapshot.Library.Current
	if current.Record.Epoch != snapshot.Library.ActiveEpoch || current.Record.Generation == uuid.Nil {
		return retainedSourceRepublishPreparation{Preview: evidencePreview}, fmt.Errorf("validate retained source-republish CURRENT identity: %w", persistence.ErrPublicationAdmissionConflict)
	}
	authority, err := repository.ReadPositionSourceAuthority(ctx, positionID)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("read source-republish baseline authority: %w", err)
	}
	if authority == nil || authority.Revision == uuid.Nil || authority.PublicationEpoch != current.Record.Epoch ||
		authority.PublicationGeneration != current.Record.Generation || authority.SourceLocationID == uuid.Nil || authority.MediaVariantID == uuid.Nil ||
		current.SourceAuthority == nil || current.SourceAuthority.Status != "present" ||
		current.SourceAuthority.Revision != authority.Revision || current.SourceAuthority.PublicationEpoch != authority.PublicationEpoch ||
		current.SourceAuthority.PublicationGeneration != authority.PublicationGeneration ||
		current.SourceAuthority.SourceLocationID != authority.SourceLocationID || current.SourceAuthority.MediaVariantID != authority.MediaVariantID {
		return retainedSourceRepublishPreparation{Preview: evidencePreview}, fmt.Errorf("validate retained source-republish CURRENT authority: %w", persistence.ErrPublicationAdmissionConflict)
	}
	selectedTools, err := service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("select verified source-republish tools: %w", err)
	}
	choice := FirstPublicationSourceChoice{LocationID: authority.SourceLocationID, MediaVariantID: authority.MediaVariantID}
	var selected *persistence.FirstPublicationSource
	for i := range snapshot.Sources {
		source := &snapshot.Sources[i]
		if source.Selected && source.AssociationID != uuid.Nil && source.Selection.LocationID == choice.LocationID && source.Selection.MediaVariantID == choice.MediaVariantID {
			if selected != nil {
				return retainedSourceRepublishPreparation{Preview: evidencePreview}, fmt.Errorf("resolve retained source-republish selection: multiple selected associations match the authoritative source: %w", persistence.ErrPublicationAdmissionConflict)
			}
			selected = source
		}
	}
	if selected == nil || selected.AssociationID == uuid.Nil || selected.Quality.StreamIndex < 0 {
		return retainedSourceRepublishPreparation{Preview: evidencePreview}, fmt.Errorf("resolve retained source-republish selection: authoritative source association is unavailable: %w", persistence.ErrPublicationAdmissionConflict)
	}
	choice.StreamIndex = selected.Quality.StreamIndex
	preview, err := buildFirstPublicationRepublishPreviewSelectedMode(snapshot, runtimeSettings, selectedTools, releaseID, positionID, &choice, mode)
	if err != nil {
		return retainedSourceRepublishPreparation{Preview: evidencePreview}, fmt.Errorf("build retained source-republish preview: %w", err)
	}
	if len(preview.Blockers) != 0 || preview.SelectedSource == nil || preview.Transform == nil {
		blocked := blockedSourceRepublishPreview(preview, releaseID, positionID, mode, "source_republish_not_eligible")
		return retainedSourceRepublishPreparation{Preview: blocked}, nil
	}
	basePlanningDigest := preview.ContextDigest
	if err := foldFirstPublicationCurrentAudioEvidenceDigest(&preview, snapshot); err != nil {
		return retainedSourceRepublishPreparation{}, err
	}
	lyricsAuthority := initialLyricsAuthorityInput(snapshot)
	lyricsPlan, err := planFirstPublicationLyrics(ctx, service.lyrics, selected, lyricsAuthority, runtimeSettings.LRCLIBEnabled, current)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("plan retained source-republish lyrics: %w", err)
	}
	if lyricsPlan.State == "unknown" || lyricsPlan.State == "unprocessable" {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("validate retained source-republish lyrics plan state %q", lyricsPlan.State)
	}
	preview.LyricsPlan = &lyricsPlan
	if err := foldFirstPublicationLyricsPlanDigest(&preview, lyricsPlan); err != nil {
		return retainedSourceRepublishPreparation{}, err
	}
	coverPlan, err := planFirstPublicationCover(ctx, os.TempDir(), snapshot.EffectiveCoverInput)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("plan source-republish cover: %w", err)
	}
	preview.CoverPlan = &coverPlan
	if err := foldFirstPublicationCoverPlanDigest(&preview, coverPlan); err != nil {
		return retainedSourceRepublishPreparation{}, err
	}
	manifest, err := finalizeFirstPublicationCurrentComparisonNamespace(ctx, snapshot, runtimeSettings.OutputDirectory, runtimeSettings.PublicationPathTemplate, &preview)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("resolve source-republish CURRENT namespace: %w", err)
	}
	preview.CurrentComparison = compareFirstPublicationCurrent(preview, current, snapshot)
	if preview.CurrentComparison == nil || preview.CurrentComparison.Generation != current.Record.Generation || len(preview.ResolvedPathComponents) == 0 {
		return retainedSourceRepublishPreparation{Preview: preview}, fmt.Errorf("validate retained source-republish CURRENT comparison (comparison=%t, generation_matches=%t, resolved_components=%d): %w",
			preview.CurrentComparison != nil,
			preview.CurrentComparison != nil && preview.CurrentComparison.Generation == current.Record.Generation,
			len(preview.ResolvedPathComponents),
			persistence.ErrPublicationAdmissionConflict)
	}
	claimID := currentPublicationNamespaceClaim(snapshot.Library, current.Record.RelativePath)
	if claimID == uuid.Nil {
		return retainedSourceRepublishPreparation{Preview: preview}, fmt.Errorf("resolve retained source-republish CURRENT claim: %w", persistence.ErrPublicationAdmissionConflict)
	}
	outputRoot, err := sourcefs.NewOutputOpener().OpenRoot(ctx, runtimeSettings.OutputDirectory)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("open source-republish output root: %w", err)
	}
	defer func() { _ = outputRoot.Close() }()
	before, err := captureObsoletePublicationOutput(ctx, outputRoot, runtimeSettings.OutputDirectory, current.Record, claimID)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("capture source-republish CURRENT: %w", err)
	}
	capture, err := (metadata.Reader{}).Read(ctx, before.NativePath, metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false})
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("read source-republish CURRENT metadata: %w", err)
	}
	artwork, err := metadata.ReadArtworkSnapshot(ctx, before.NativePath)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("read source-republish CURRENT artwork: %w", err)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("observe retained source-republish CURRENT assets: %w", err)
	}
	if !reflect.DeepEqual(capture.Tags, current.Metadata.Tags) || !reflect.DeepEqual(assets, current.Assets) {
		return retainedSourceRepublishPreparation{Preview: preview}, fmt.Errorf("validate retained source-republish CURRENT witness: %w", persistence.ErrPublicationAdmissionConflict)
	}
	after, err := captureObsoletePublicationOutput(ctx, outputRoot, runtimeSettings.OutputDirectory, current.Record, claimID)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("recapture source-republish CURRENT: %w", err)
	}
	if before.FileSize != after.FileSize || before.ModifiedAt != after.ModifiedAt ||
		!reflect.DeepEqual(before.SHA256, after.SHA256) || before.NativePath != after.NativePath {
		return retainedSourceRepublishPreparation{Preview: preview}, fmt.Errorf("validate stable retained source-republish CURRENT witness: %w", persistence.ErrPublicationAdmissionConflict)
	}
	currentWitness := persistence.PublicationCurrentWitness{
		Generation: current.Record.Generation, RelativePath: current.Record.RelativePath,
		Ownership:  persistence.PublicationFileIdentity{SizeBytes: sourceRepublishInt64Pointer(before.FileSize), MTime: sourceRepublishInt64Pointer(before.ModifiedAt.UnixMicro()), SHA256: hex.EncodeToString(before.SHA256)},
		AudioFacts: current.Audio.Facts, Assets: assets.Clone(), Tags: cloneDesiredPublicationTags(capture.Tags),
		ArtworkInventory: audioUpgradeArtworkWitnesses(artwork.ArtworkInventory()),
	}
	lyrics := persistence.PublicationRepublishLyrics{State: "absent", Origin: "none"}
	if lyricsPlan.State == "value" {
		lyrics = persistence.PublicationRepublishLyrics{State: "value", Origin: lyricsPlan.Origin, Text: lyricsPlan.Lyrics, SHA256: lyricsPlan.SHA256}
	}
	lyricsAuthorityValue := persistence.PublicationInitialLyricsInput{}
	if lyricsAuthority != nil {
		lyricsAuthorityValue = *lyricsAuthority
	}
	lookup := publicationRepublishLyricsLookup(selected.Input.AnalysedMetadata, runtimeSettings.LRCLIBEnabled)
	cover, err := publicationRepublishCoverInput(snapshot.EffectiveCoverInput, coverPlan)
	if err != nil {
		return retainedSourceRepublishPreparation{}, err
	}
	sourceAuthority := persistence.PublicationSourceAuthority{
		Present: true, PositionID: positionID, Mode: authority.Mode, Revision: authority.Revision,
		PublicationEpoch: authority.PublicationEpoch, PublicationGeneration: authority.PublicationGeneration,
		SourceLocationID: authority.SourceLocationID, MediaVariantID: authority.MediaVariantID,
	}
	input := persistence.PublicationInputs{
		Version: persistence.PublicationSourceRepublishInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "audio", Format: publication.AudioPublicationFormat(runtimeSettings.PublicationFormat),
		Audio: audioInputCopy(&selected.Input), DesiredTags: cloneDesiredPublicationTags(preview.DesiredTags.Tags),
		Tools: append([]persistence.SourceAnalysisToolSelection(nil), selectedTools...),
		SourceRepublish: &persistence.PublicationSourceRepublishInput{
			Current: currentWitness, Mode: mode, SourceAuthority: sourceAuthority,
			Association: persistence.PublicationRepublishAssociation{ID: selected.AssociationID, LocationID: selected.Selection.LocationID, MediaVariantID: selected.Selection.MediaVariantID},
			Lyrics:      lyrics, LyricsAuthority: lyricsAuthorityValue, LyricsLookup: lookup, Cover: cover,
		},
	}
	if err := input.Validate(); err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("validate source-republish frozen inputs: %w", err)
	}
	frozenInputs, err := json.Marshal(input)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("encode source-republish frozen inputs: %w", err)
	}
	format := publication.AudioPublicationFormat(runtimeSettings.PublicationFormat)
	qualityComparison := sourceRepublishOutputQualityComparison(preview.Transform, selected, current, format)
	capability := &SourceRepublishConfirmationCapability{
		Kind: "retained_same_path", Status: "ready", ReasonCodes: []string{}, CurrentGeneration: current.Record.Generation,
	}
	if warning := sourceRepublishQualityWarningCode(qualityComparison); warning != "" {
		preview.Warnings = append(preview.Warnings, warning)
	}
	if sourceRepublishIsUnchanged(preview, selected, current, format) {
		capability.Status = "unchanged"
		capability.ReasonCodes = []string{"content_unchanged"}
	}
	preview.ConfirmationCapability = capability
	preview.ContextDigest, err = retainedSourceRepublishDigest(preview, basePlanningDigest, frozenInputs, manifest, runtimeSettings, selectedTools)
	if err != nil {
		return retainedSourceRepublishPreparation{}, fmt.Errorf("digest retained source-republish confirmation: %w", err)
	}
	return retainedSourceRepublishPreparation{
		Repository: repository, Snapshot: snapshot, RuntimeSettings: runtimeSettings, Authority: authority,
		SelectedTools: selectedTools, Selected: selected, Choice: choice, Preview: preview, Manifest: manifest,
		LyricsAuthority: lyricsAuthority, LyricsPlan: lyricsPlan, CoverPlan: coverPlan, Input: input, FrozenInputs: frozenInputs, BasePlanningDigest: basePlanningDigest,
	}, nil
}

func publicationRepublishLyricsLookup(metadata *persistence.PublicationAnalysedMetadata, enabled bool) persistence.PublicationRepublishLyricsLookup {
	lookup := persistence.PublicationRepublishLyricsLookup{Enabled: enabled}
	if metadata == nil {
		return lookup
	}
	lookup.MetadataResultID, lookup.WinningResultID = metadata.MetadataResultID, metadata.WinningResultID
	lookup.Artist = sourceRepublishSingleTag(metadata.Tags, "ARTIST")
	lookup.Album = sourceRepublishSingleTag(metadata.Tags, "ALBUM")
	lookup.Track = sourceRepublishSingleTag(metadata.Tags, "TITLE")
	if metadata.DurationSeconds != nil {
		duration := *metadata.DurationSeconds
		lookup.DurationSeconds = &duration
	}
	return lookup
}

func publicationRepublishCoverInput(input persistence.PublicationAttemptCoverInput, plan FirstPublicationCoverPlan) (persistence.PublicationRepublishCover, error) {
	cover := persistence.PublicationRepublishCover{
		ReleaseID: input.ReleaseID, AuthorityRevision: input.Revision, AuthorityMode: input.Mode,
		Derivation: plan.Derivation,
	}
	if plan.AuthorityGeneration != "" {
		generation, err := uuid.Parse(plan.AuthorityGeneration)
		if err != nil {
			return persistence.PublicationRepublishCover{}, fmt.Errorf("decode source-republish cover generation: %w", err)
		}
		cover.SelectedGeneration = generation
	}
	if plan.Original != nil {
		cover.OriginalSHA256, cover.OriginalMIME = plan.Original.SHA256, plan.Original.MIME
		size := plan.Original.Size
		cover.OriginalSizeBytes = &size
	}
	if plan.Derivative != nil {
		cover.PNG = append([]byte(nil), plan.DerivativeBytes...)
		cover.SHA256, cover.MIME, cover.SizeBytes = plan.Derivative.SHA256, plan.Derivative.MIME, plan.Derivative.Size
	}
	if plan.Derivation == "optional_failure" {
		cover.Failure = "derivative_failed"
	}
	return cover, nil
}

func sameRepublishSourceAuthority(current *persistence.CurrentSourceAuthority, authority *persistence.PositionSourceAuthority) bool {
	return current != nil && authority != nil && current.Status == "present" && current.Revision == authority.Revision &&
		current.PublicationEpoch == authority.PublicationEpoch && current.PublicationGeneration == authority.PublicationGeneration &&
		current.SourceLocationID == authority.SourceLocationID && current.MediaVariantID == authority.MediaVariantID
}

func sameRepublishCoverAuthority(actual, expected persistence.PublicationAttemptCoverInput) bool {
	return actual.ReleaseID == expected.ReleaseID && actual.Revision == expected.Revision && actual.Mode == expected.Mode &&
		actual.State == expected.State && reflect.DeepEqual(actual.Generation, expected.Generation) &&
		reflect.DeepEqual(actual.MIME, expected.MIME) && reflect.DeepEqual(actual.Size, expected.Size) &&
		reflect.DeepEqual(actual.SHA256, expected.SHA256) && reflect.DeepEqual(actual.Bytes, expected.Bytes)
}

func sameRepublishLyricsAuthority(snapshot *persistence.LibraryFirstPublicationContext, expected persistence.PublicationInitialLyricsInput) bool {
	actual := initialLyricsAuthorityInput(snapshot)
	return actual != nil && reflect.DeepEqual(*actual, expected)
}

func sameRepublishLyricsLookup(expected persistence.PublicationRepublishLyricsLookup, metadata *persistence.PublicationAnalysedMetadata, enabled bool) bool {
	return reflect.DeepEqual(expected, publicationRepublishLyricsLookup(metadata, enabled))
}

func sourceRepublishInt64Pointer(value int64) *int64 {
	return &value
}

func sourceRepublishSingleTag(tags map[string][]string, key string) string {
	if len(tags[key]) != 1 {
		return ""
	}
	return tags[key][0]
}
