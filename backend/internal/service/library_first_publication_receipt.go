package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type matchingReceiptPublicationSourceReader interface {
	ReadMatchingReceiptPublicationSource(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, persistence.MatchingReceiptPublicationSource) error
}

// PreviewFirstFromMatchingReceipt plans ordinary first publication only from
// the exact association recorded by a saved matching confirmation. The receipt
// does not authorize CURRENT replacement; a position with a current
// publication is rejected instead of entering the republish path.
func (service *LibraryPublications) PreviewFirstFromMatchingReceipt(ctx context.Context, releaseID, positionID uuid.UUID, receipt persistence.MatchingReceiptPublicationSource, choice *FirstPublicationSourceChoice) (FirstPublicationPreview, error) {
	if !validMatchingReceiptPublicationSource(receipt) {
		return FirstPublicationPreview{}, persistence.ErrPublicationAdmissionConflict
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
	if contextSnapshot == nil || contextSnapshot.Library == nil {
		return FirstPublicationPreview{}, errors.New("first publication context is incomplete")
	}
	if contextSnapshot.Library.Current != nil {
		return FirstPublicationPreview{}, persistence.ErrPublicationAdmissionConflict
	}
	contextSnapshot, variantID, err := scopeFirstPublicationSnapshotToMatchingReceipt(contextSnapshot, receipt)
	if err != nil {
		return FirstPublicationPreview{}, err
	}
	proof, ok := service.repository.(matchingReceiptPublicationSourceReader)
	if !ok {
		return FirstPublicationPreview{}, errors.New("matching receipt publication source is unavailable")
	}
	if err := proof.ReadMatchingReceiptPublicationSource(ctx, releaseID, positionID, variantID, receipt); err != nil {
		return FirstPublicationPreview{}, err
	}
	runtimeSettings, err := planningSettings.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return FirstPublicationPreview{}, fmt.Errorf("read publication planning settings: %w", err)
	}
	selectedTools, _ := service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
	if choice == nil {
		choice = firstPublicationSoleReceiptChoice(contextSnapshot, publication.AudioPublicationFormat(runtimeSettings.PublicationFormat))
	}
	return service.previewFirstCapturedWithReceipt(ctx, contextSnapshot, runtimeSettings, selectedTools, releaseID, positionID, choice, firstPublicationPreviewModeOrdinary, &receipt)
}

// scopeFirstPublicationSnapshotToMatchingReceipt reduces the planner's source
// candidates to the exact confirmed association. The association witnesses in
// SourceAssociations are deliberately left untouched: selection admission still
// compares the complete association set before changing track_source.
func scopeFirstPublicationSnapshotToMatchingReceipt(snapshot *persistence.LibraryFirstPublicationContext, receipt persistence.MatchingReceiptPublicationSource) (*persistence.LibraryFirstPublicationContext, uuid.UUID, error) {
	variantID, err := matchingReceiptVariantID(snapshot, receipt.TrackSourceID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	scoped := *snapshot
	scoped.Sources = make([]persistence.FirstPublicationSource, 0)
	for _, source := range snapshot.Sources {
		if source.AssociationID != receipt.TrackSourceID {
			continue
		}
		if source.Selection.MediaVariantID != variantID {
			return nil, uuid.Nil, persistence.ErrPublicationAdmissionConflict
		}
		scoped.Sources = append(scoped.Sources, source)
	}
	return &scoped, variantID, nil
}

func matchingReceiptVariantID(snapshot *persistence.LibraryFirstPublicationContext, associationID uuid.UUID) (uuid.UUID, error) {
	if snapshot == nil || associationID == uuid.Nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	for _, association := range snapshot.SourceAssociations {
		if association.ID == associationID && association.Compatible && association.MediaVariantID != uuid.Nil {
			return association.MediaVariantID, nil
		}
	}
	return uuid.Nil, persistence.ErrPublicationAdmissionConflict
}

// firstPublicationSoleReceiptChoice resolves the only unambiguous audio stream
// of the receipt-scoped association. Multiple eligible streams stay unresolved
// so the preview presents the constrained candidates and requires confirmation.
func firstPublicationSoleReceiptChoice(snapshot *persistence.LibraryFirstPublicationContext, format publication.AudioPublicationFormat) *FirstPublicationSourceChoice {
	var choice *FirstPublicationSourceChoice
	for _, source := range snapshot.Sources {
		if _, err := publication.PlanAudioTransform(format, source.Quality.AudioSourceFacts()); err != nil {
			continue
		}
		if choice != nil || source.RequiresExplicitStreamSelection || source.AudioStreamCount != 1 {
			return nil
		}
		candidate := FirstPublicationSourceChoice{LocationID: source.Selection.LocationID, MediaVariantID: source.Selection.MediaVariantID, StreamIndex: source.Quality.StreamIndex}
		choice = &candidate
	}
	return choice
}

// foldFirstPublicationReceiptDigest binds the preview to the receipt selector
// and its exact variant. It runs after namespace planning so a receipt cannot
// be swapped between a preview and its confirmation.
func foldFirstPublicationReceiptDigest(preview *FirstPublicationPreview, receipt persistence.MatchingReceiptPublicationSource, variantID uuid.UUID) error {
	if preview == nil || variantID == uuid.Nil || !validMatchingReceiptPublicationSource(receipt) {
		return persistence.ErrPublicationAdmissionConflict
	}
	encoded, err := json.Marshal(struct {
		Version        string    `json:"version"`
		Previous       string    `json:"previous"`
		Kind           string    `json:"kind"`
		RequestID      uuid.UUID `json:"request_id"`
		TrackSourceID  uuid.UUID `json:"track_source_id"`
		MediaVariantID uuid.UUID `json:"media_variant_id"`
	}{"melo:first-publication-matching-receipt:v1", preview.ContextDigest, receipt.Kind, receipt.RequestID, receipt.TrackSourceID, variantID})
	if err != nil {
		return fmt.Errorf("encode first publication matching receipt digest: %w", err)
	}
	digest := sha256.Sum256(encoded)
	preview.ContextDigest = hex.EncodeToString(digest[:])
	return nil
}

func validMatchingReceiptPublicationSource(receipt persistence.MatchingReceiptPublicationSource) bool {
	return receipt.RequestID != uuid.Nil && receipt.TrackSourceID != uuid.Nil &&
		(receipt.Kind == persistence.MatchingReceiptKindManual || receipt.Kind == persistence.MatchingReceiptKindLocal)
}
