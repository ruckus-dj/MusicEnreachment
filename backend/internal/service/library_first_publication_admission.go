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

// AdmitFirst admits the exact source and plan displayed by PreviewFirst. The
// transaction callback reconstructs the preview from its authoritative
// snapshot before it permits namespace reservation or publication admission.
func (service *LibraryPublications) AdmitFirst(ctx context.Context, releaseID, positionID uuid.UUID, confirmation FirstPublicationConfirmation) (uuid.UUID, error) {
	if service == nil || service.repository == nil || service.settings == nil || service.river == nil ||
		releaseID == uuid.Nil || positionID == uuid.Nil || confirmation.ExpectedEpoch == uuid.Nil ||
		!validFirstPublicationPreviewDigest(confirmation.PreviewDigest) {
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
	if snapshot == nil || snapshot.Library == nil || snapshot.Library.ActiveEpoch != confirmation.ExpectedEpoch {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	selectedTools, err := service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
	if err != nil {
		return uuid.Nil, fmt.Errorf("select verified publication tools: %w", err)
	}
	preview, err := buildFirstPublicationPreviewSelected(snapshot, runtimeSettings, selectedTools, releaseID, positionID, confirmation.SourceChoice)
	if err != nil {
		return uuid.Nil, fmt.Errorf("plan first publication: %w", err)
	}
	if len(preview.Blockers) != 0 || preview.SelectedSource == nil || preview.Transform == nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	initialManifest, err := finalizeFirstPublicationNamespace(ctx, snapshot, runtimeSettings.OutputDirectory, runtimeSettings.PublicationPathTemplate, &preview)
	if err != nil || preview.ContextDigest != confirmation.PreviewDigest {
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
		IntentKind: "audio", Format: publication.AudioPublicationFormat(runtimeSettings.PublicationFormat),
		Audio: audioInputCopy(&selected.Input), DesiredTags: cloneDesiredPublicationTags(preview.DesiredTags.Tags),
		Tools: append([]persistence.SourceAnalysisToolSelection(nil), selectedTools...),
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
		PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: confirmation.ExpectedEpoch,
		CandidateGeneration: candidateID, IntentKind: "audio", ToolsReadRequired: true,
		Tools:  append([]persistence.SourceAnalysisToolSelection(nil), selectedTools...),
		Source: &sourceSelection, FrozenInputs: rawFrozen,
		NamespaceManifest: initialManifest,
	}
	return repository.AdmitFirstPublicationFromPreview(ctx, persistence.FirstPublicationAdmission{
		PublicationAdmission: request, PreviewDigest: confirmation.PreviewDigest,
		PrepareFirst: func(ctx context.Context, authoritative persistence.FirstPublicationAdmissionSnapshot, actual persistence.PublicationAdmission) ([]persistence.PublicationNamespaceNode, error) {
			if authoritative.Context == nil || authoritative.Context.Library == nil ||
				authoritative.Context.Library.ActiveEpoch != confirmation.ExpectedEpoch ||
				actual.CandidateGeneration != candidateID || actual.ExpectedReleaseID != releaseID || actual.PositionID != positionID || actual.Source == nil {
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
			transactionPreview, err := buildFirstPublicationPreviewSelected(authoritative.Context, transactionSettings, transactionTools, releaseID, positionID, confirmation.SourceChoice)
			if err != nil || len(transactionPreview.Blockers) != 0 ||
				transactionPreview.SelectedSource == nil || transactionPreview.Transform == nil || !reflect.DeepEqual(transactionTools, actual.Tools) {
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			transactionManifest, err := finalizeFirstPublicationNamespace(ctx, authoritative.Context, transactionSettings.OutputDirectory, transactionSettings.PublicationPathTemplate, &transactionPreview)
			if err != nil || transactionPreview.ContextDigest != confirmation.PreviewDigest {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			if !reflect.DeepEqual(transactionPreview.DesiredTags.Tags, frozen.DesiredTags) ||
				transactionPreview.SelectedSource.RootID != actual.Source.RootID || transactionPreview.SelectedSource.LocationID != actual.Source.LocationID ||
				transactionPreview.SelectedSource.MediaVariantID != actual.Source.MediaVariantID ||
				transactionPreview.SelectedSource.StreamIndex != selected.Quality.StreamIndex {
				return nil, persistence.ErrPublicationAdmissionConflict
			}
			return transactionManifest, nil
		},
	}, service.river)
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
