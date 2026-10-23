package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// FirstPublicationConfirmation binds an admission to the preview the user
// confirmed. A preview is not itself an authorization: all its inputs are
// checked again under the admission transaction's database guards.
type FirstPublicationConfirmation struct {
	PreviewDigest string
	ExpectedEpoch uuid.UUID
	SourceChoice  *FirstPublicationSourceChoice
}

type firstPublicationAdmissionRepository interface {
	firstPublicationContextReader
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
	AdmitFirstPublicationFromPreview(context.Context, persistence.FirstPublicationAdmission, persistence.RiverInserter) (uuid.UUID, error)
}

type automaticFirstPublicationAdmissionRepository interface {
	AdmitAutomaticFirstPublicationFromPreview(context.Context, persistence.FirstPublicationAdmission, persistence.PublicationReconciliationDeliveryGuard, persistence.RiverInserter) (uuid.UUID, error)
}

// AdmitFirst admits the exact source and plan displayed by PreviewFirst. The
// transaction callback reconstructs the preview from its authoritative
// snapshot before it permits namespace reservation or publication admission.
func (service *LibraryPublications) AdmitFirst(ctx context.Context, releaseID, positionID uuid.UUID, confirmation FirstPublicationConfirmation) (uuid.UUID, error) {
	return service.admitFirst(ctx, releaseID, positionID, confirmation.PreviewDigest, confirmation.ExpectedEpoch, confirmation.SourceChoice, nil)
}

// admitAutomaticFirstPublication is intentionally internal: no runtime producer
// calls it until an authority producer is integrated.
func (service *LibraryPublications) admitAutomaticFirstPublication(ctx context.Context, releaseID, positionID uuid.UUID, guard persistence.PublicationReconciliationDeliveryGuard) (uuid.UUID, error) {
	if service == nil || service.repository == nil || service.settings == nil || service.river == nil ||
		releaseID == uuid.Nil || positionID == uuid.Nil || guard.OutputEpoch == uuid.Nil || guard.PositionID != positionID {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	repository, ok := service.repository.(firstPublicationContextReader)
	if !ok {
		return uuid.Nil, errors.New("first publication context is unavailable")
	}
	planningSettings, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return uuid.Nil, errors.New("first publication settings are unavailable")
	}
	snapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read first publication context: %w", err)
	}
	runtimeSettings, err := planningSettings.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read publication planning settings: %w", err)
	}
	if snapshot == nil || snapshot.Library == nil || snapshot.Library.ActiveEpoch != guard.OutputEpoch {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	selectedTools, err := service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
	if err != nil {
		return uuid.Nil, fmt.Errorf("select verified publication tools: %w", err)
	}
	choice, err := automaticFirstSourceChoice(snapshot, publication.AudioPublicationFormat(runtimeSettings.PublicationFormat))
	if err != nil {
		return uuid.Nil, err
	}
	preview, err := buildFirstPublicationPreviewSelected(snapshot, runtimeSettings, selectedTools, releaseID, positionID, &choice)
	if err != nil || len(preview.Blockers) != 0 || preview.Transform == nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	if _, err := finalizeFirstPublicationNamespace(ctx, snapshot, runtimeSettings.OutputDirectory, runtimeSettings.PublicationPathTemplate, &preview); err != nil {
		if ctx.Err() != nil {
			return uuid.Nil, ctx.Err()
		}
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	return service.admitFirst(ctx, releaseID, positionID, preview.ContextDigest, guard.OutputEpoch, &choice, &guard)
}

func automaticFirstSourceChoice(snapshot *persistence.LibraryFirstPublicationContext, format publication.AudioPublicationFormat) (FirstPublicationSourceChoice, error) {
	choice, _, err := automaticFirstSourceChoiceWithReason(snapshot, format)
	return choice, err
}

func automaticFirstSourceChoiceWithReason(snapshot *persistence.LibraryFirstPublicationContext, format publication.AudioPublicationFormat) (FirstPublicationSourceChoice, string, error) {
	if snapshot == nil {
		return FirstPublicationSourceChoice{}, "", persistence.ErrPublicationAdmissionConflict
	}
	if len(snapshot.DefaultSourceBlockers) != 0 {
		return FirstPublicationSourceChoice{}, "", persistence.ErrPublicationAdmissionConflict
	}
	candidates := make([]publication.QualityCandidate, 0, len(snapshot.Sources))
	hasExplicitStream := false
	for _, source := range snapshot.Sources {
		if !source.Selected {
			continue
		}
		// Automatic admission does not make a stream choice inside a multi-audio file.
		requiresExplicitStream := source.RequiresExplicitStreamSelection || source.AudioStreamCount != 1
		if _, err := publication.PlanAudioTransform(format, source.Quality.AudioSourceFacts()); err != nil {
			continue
		}
		if requiresExplicitStream {
			if source.RequiresExplicitStreamSelection || source.AudioStreamCount > 1 {
				hasExplicitStream = true
			}
			continue
		}
		lossless := source.Quality.Lossless
		candidate := publication.QualityCandidate{VariantUUID: source.Selection.MediaVariantID.String(), LocationUUID: source.Selection.LocationID.String(), StreamIndex: source.Quality.StreamIndex,
			Lossless: lossless, Codec: source.Quality.Codec, Channels: source.Quality.Channels, Layout: source.ChannelLayout,
			BitDepth: source.Quality.BitDepth, SampleRate: source.Quality.SampleRate, Bitrate: source.Quality.Bitrate}
		candidates = append(candidates, candidate)
	}
	selection := publication.SelectQuality(candidates, nil)
	if selection.RequiresManual || selection.Candidate == nil {
		if len(candidates) > 0 {
			return FirstPublicationSourceChoice{}, "ambiguous_quality", persistence.ErrPublicationAdmissionConflict
		}
		if hasExplicitStream {
			return FirstPublicationSourceChoice{}, "manual_stream_selection", persistence.ErrPublicationAdmissionConflict
		}
		return FirstPublicationSourceChoice{}, "", persistence.ErrPublicationAdmissionConflict
	}
	for _, source := range snapshot.Sources {
		if source.Selected && source.Selection.MediaVariantID.String() == selection.Candidate.VariantUUID && source.Selection.LocationID.String() == selection.Candidate.LocationUUID && source.Quality.StreamIndex == selection.Candidate.StreamIndex {
			return FirstPublicationSourceChoice{LocationID: source.Selection.LocationID, MediaVariantID: source.Selection.MediaVariantID, StreamIndex: source.Quality.StreamIndex}, "", nil
		}
	}
	return FirstPublicationSourceChoice{}, "", persistence.ErrPublicationAdmissionConflict
}

func (service *LibraryPublications) admitFirst(ctx context.Context, releaseID, positionID uuid.UUID, previewDigest string, expectedEpoch uuid.UUID, sourceChoice *FirstPublicationSourceChoice, guard *persistence.PublicationReconciliationDeliveryGuard) (uuid.UUID, error) {
	if service == nil || service.repository == nil || service.settings == nil || service.river == nil ||
		releaseID == uuid.Nil || positionID == uuid.Nil || expectedEpoch == uuid.Nil ||
		!validFirstPublicationPreviewDigest(previewDigest) {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	repository, ok := service.repository.(firstPublicationAdmissionRepository)
	if !ok {
		return uuid.Nil, errors.New("first publication admission is unavailable")
	}
	planningSettings, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return uuid.Nil, errors.New("first publication settings are unavailable")
	}

	// Capture one coherent view for choosing the immutable source and tool pair.
	snapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read first publication context: %w", err)
	}
	runtimeSettings, err := planningSettings.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read publication planning settings: %w", err)
	}
	if snapshot == nil || snapshot.Library == nil || snapshot.Library.ActiveEpoch != expectedEpoch {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	selectedTools, err := service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
	if err != nil {
		return uuid.Nil, fmt.Errorf("select verified publication tools: %w", err)
	}
	preview, err := buildFirstPublicationPreviewSelected(snapshot, runtimeSettings, selectedTools, releaseID, positionID, sourceChoice)
	if err != nil {
		return uuid.Nil, fmt.Errorf("plan first publication: %w", err)
	}
	if len(preview.Blockers) != 0 || preview.SelectedSource == nil || preview.Transform == nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	initialManifest, err := finalizeFirstPublicationNamespace(ctx, snapshot, runtimeSettings.OutputDirectory, runtimeSettings.PublicationPathTemplate, &preview)
	if err != nil || preview.ContextDigest != previewDigest {
		if ctx.Err() != nil {
			return uuid.Nil, ctx.Err()
		}
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}

	var selected *persistence.FirstPublicationSource
	for i := range snapshot.Sources {
		source := &snapshot.Sources[i]
		if source.Selection.RootID == preview.SelectedSource.RootID && source.Selection.LocationID == preview.SelectedSource.LocationID &&
			source.Selection.MediaVariantID == preview.SelectedSource.MediaVariantID && source.Quality.StreamIndex == preview.SelectedSource.StreamIndex {
			selected = source
			break
		}
	}
	if selected == nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}

	frozen := persistence.PublicationInputs{
		Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "audio", SourceAuthorityMode: "automatic", Format: publication.AudioPublicationFormat(runtimeSettings.PublicationFormat),
		Audio: audioInputCopy(&selected.Input), DesiredTags: cloneDesiredPublicationTags(preview.DesiredTags.Tags),
		InitialLyrics: initialLyricsAuthorityInput(snapshot),
		Tools:         append([]persistence.SourceAnalysisToolSelection(nil), selectedTools...),
	}
	if sourceChoice != nil && guard == nil {
		frozen.SourceAuthorityMode = "manual"
	}
	if err := frozen.Validate(); err != nil {
		return uuid.Nil, fmt.Errorf("validate first publication inputs: %w", err)
	}
	rawFrozen, err := json.Marshal(frozen)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode first publication inputs: %w", err)
	}
	candidateID := uuid.New()
	sourceSelection := selected.Selection
	sourceSelection.HoldUUID = uuid.New()
	request := persistence.PublicationAdmission{
		PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: expectedEpoch,
		CandidateGeneration: candidateID, IntentKind: "audio", ToolsReadRequired: true,
		Tools:  append([]persistence.SourceAnalysisToolSelection(nil), selectedTools...),
		Source: &sourceSelection, FrozenInputs: rawFrozen,
		NamespaceManifest: initialManifest,
	}
	var associationIntent *persistence.FirstPublicationSourceSelection
	if sourceChoice != nil && guard == nil {
		if selected.AssociationID == uuid.Nil || len(snapshot.SourceAssociations) == 0 {
			return uuid.Nil, persistence.ErrPublicationAdmissionConflict
		}
		associationIntent = &persistence.FirstPublicationSourceSelection{
			AssociationID: selected.AssociationID,
			Associations:  append([]persistence.PublicationSourceAssociation(nil), snapshot.SourceAssociations...),
		}
	}
	first := persistence.FirstPublicationAdmission{
		PublicationAdmission: request, PreviewDigest: previewDigest, SourceSelection: associationIntent,
		PrepareFirst: func(ctx context.Context, authoritative persistence.FirstPublicationAdmissionSnapshot, actual persistence.PublicationAdmission) ([]persistence.PublicationNamespaceNode, error) {
			if authoritative.Context == nil || authoritative.Context.Library == nil ||
				authoritative.Context.Library.ActiveEpoch != expectedEpoch ||
				actual.CandidateGeneration != candidateID || actual.ExpectedReleaseID != releaseID || actual.PositionID != positionID || actual.Source == nil {
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			if !sameInitialLyricsAuthority(authoritative.Context, frozen.InitialLyrics) {
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			transactionSettings, err := settings.ParsePublicationPlanningValues(authoritative.Settings)
			if err != nil {
				return nil, fmt.Errorf("parse transaction publication settings: %w", err)
			}
			var installation *persistence.ToolInstallation
			for i := range authoritative.Installations {
				if authoritative.Installations[i].ID.String() == transactionSettings.ActiveFFmpegInstallation {
					installation = &authoritative.Installations[i]
					break
				}
			}
			transactionTools, err := selectedFFmpegPairFromInstallation(installation, service.platform)
			if err != nil {
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			choice := sourceChoice
			if guard != nil {
				freshChoice, selectErr := automaticFirstSourceChoice(authoritative.Context, publication.AudioPublicationFormat(transactionSettings.PublicationFormat))
				if selectErr != nil || choice == nil || freshChoice != *choice {
					return nil, persistence.ErrPublicationAdmissionConflict
				}
				choice = &freshChoice
			}
			transactionPreview, err := buildFirstPublicationPreviewSelected(authoritative.Context, transactionSettings, transactionTools, releaseID, positionID, choice)
			if err != nil || len(transactionPreview.Blockers) != 0 ||
				transactionPreview.SelectedSource == nil || transactionPreview.Transform == nil || !reflect.DeepEqual(transactionTools, actual.Tools) {
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			var transactionSource *persistence.FirstPublicationSource
			for i := range authoritative.Context.Sources {
				candidate := &authoritative.Context.Sources[i]
				if candidate.Selection.LocationID == transactionPreview.SelectedSource.LocationID && candidate.Selection.MediaVariantID == transactionPreview.SelectedSource.MediaVariantID && candidate.Quality.StreamIndex == transactionPreview.SelectedSource.StreamIndex {
					transactionSource = candidate
					break
				}
			}
			if transactionSource == nil {
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			transactionManifest, err := finalizeFirstPublicationNamespace(ctx, authoritative.Context, transactionSettings.OutputDirectory, transactionSettings.PublicationPathTemplate, &transactionPreview)
			if err != nil || transactionPreview.ContextDigest != previewDigest {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			if !reflect.DeepEqual(transactionPreview.DesiredTags.Tags, frozen.DesiredTags) ||
				transactionPreview.SelectedSource.RootID != actual.Source.RootID || transactionPreview.SelectedSource.LocationID != actual.Source.LocationID ||
				transactionPreview.SelectedSource.MediaVariantID != actual.Source.MediaVariantID ||
				transactionPreview.SelectedSource.StreamIndex != transactionSource.Quality.StreamIndex {
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			var actualFrozen persistence.PublicationInputs
			if err := json.Unmarshal(actual.FrozenInputs, &actualFrozen); err != nil {
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			expectedFrozen := persistence.PublicationInputs{
				Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
				IntentKind: "audio", SourceAuthorityMode: frozen.SourceAuthorityMode, Format: publication.AudioPublicationFormat(transactionSettings.PublicationFormat),
				Audio: audioInputCopy(&transactionSource.Input), DesiredTags: cloneDesiredPublicationTags(transactionPreview.DesiredTags.Tags),
				InitialLyrics: initialLyricsAuthorityInput(authoritative.Context), Tools: append([]persistence.SourceAnalysisToolSelection(nil), transactionTools...),
			}
			if !reflect.DeepEqual(actualFrozen, expectedFrozen) {
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			return transactionManifest, nil
		},
	}
	if guard != nil {
		automaticRepository, ok := service.repository.(automaticFirstPublicationAdmissionRepository)
		if !ok {
			return uuid.Nil, errors.New("automatic first publication admission is unavailable")
		}
		return automaticRepository.AdmitAutomaticFirstPublicationFromPreview(ctx, first, *guard, service.river)
	}
	return repository.AdmitFirstPublicationFromPreview(ctx, first, service.river)
}

func initialLyricsAuthorityInput(snapshot *persistence.LibraryFirstPublicationContext) *persistence.PublicationInitialLyricsInput {
	if snapshot == nil {
		return nil
	}
	input := &persistence.PublicationInitialLyricsInput{ExpectedMetadataRevision: snapshot.Metadata.Position.Revision}
	if desired, exists := snapshot.Metadata.Position.Envelope.Desired["synced_lyrics"]; exists {
		copy := desired
		input.Desired = &copy
	}
	return input
}

func sameInitialLyricsAuthority(snapshot *persistence.LibraryFirstPublicationContext, expected *persistence.PublicationInitialLyricsInput) bool {
	return snapshot != nil && expected != nil && reflect.DeepEqual(initialLyricsAuthorityInput(snapshot), expected)
}

func validFirstPublicationPreviewDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, char := range digest {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}

func audioInputCopy(input *persistence.PublicationAudioInput) *persistence.PublicationAudioInput {
	if input == nil {
		return nil
	}
	copy := *input
	if input.AnalysedMetadata != nil {
		metadata := *input.AnalysedMetadata
		if input.AnalysedMetadata.DurationSeconds != nil {
			duration := *input.AnalysedMetadata.DurationSeconds
			metadata.DurationSeconds = &duration
		}
		if input.AnalysedMetadata.Tags != nil {
			metadata.Tags = cloneDesiredPublicationTags(input.AnalysedMetadata.Tags)
		}
		copy.AnalysedMetadata = &metadata
	}
	return &copy
}

func cloneDesiredPublicationTags(tags map[string][]string) map[string][]string {
	clone := make(map[string][]string, len(tags))
	for name, values := range tags {
		clone[name] = make([]string, len(values))
		copy(clone[name], values)
	}
	return clone
}
