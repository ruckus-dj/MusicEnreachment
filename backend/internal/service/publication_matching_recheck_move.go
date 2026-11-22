package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// PrepareMatchingRecheckMovedPublication freezes the ordinary first
// publication of one exact automatic association moved onto another library
// position. The destination has no CURRENT; its only namespace authority is the
// exact predecessor file claim supplied by the winning transaction.
//
// This method performs no database, settings or registry reads and no native
// output write: persistence supplies the transaction snapshot, and the shared
// first-publication preview builders plan from it. The predecessor leaf is the
// only exempted collision.
func (service *LibraryPublications) PrepareMatchingRecheckMovedPublication(ctx context.Context, snapshot persistence.MatchingRecheckMoveSnapshot) (persistence.PublicationAdmission, error) {
	contextSnapshot := snapshot.Context
	if service == nil || contextSnapshot == nil || contextSnapshot.Library == nil ||
		contextSnapshot.Library.ReleaseID == uuid.Nil || contextSnapshot.Library.PositionID == uuid.Nil ||
		contextSnapshot.Library.ActiveEpoch == uuid.Nil || contextSnapshot.Library.Current != nil ||
		contextSnapshot.Library.ActiveAttempt != nil {
		return persistence.PublicationAdmission{}, persistence.ErrPublicationAdmissionConflict
	}
	if len(snapshot.Settings) == 0 || len(snapshot.Installations) == 0 ||
		snapshot.SourceAssociationID == uuid.Nil || snapshot.MediaVariantID == uuid.Nil || snapshot.LocationID == uuid.Nil {
		return persistence.PublicationAdmission{}, persistence.ErrPublicationAdmissionConflict
	}
	if snapshot.Predecessor.PositionID == uuid.Nil || snapshot.Predecessor.PositionID == contextSnapshot.Library.PositionID ||
		snapshot.Predecessor.Generation == uuid.Nil || snapshot.Predecessor.RelativePath == "" ||
		snapshot.Predecessor.Epoch != contextSnapshot.Library.ActiveEpoch ||
		snapshot.PredecessorClaim.ID == uuid.Nil || snapshot.PredecessorClaim.NodeKind != "file" ||
		snapshot.PredecessorClaim.RelativePath != snapshot.Predecessor.RelativePath ||
		snapshot.PredecessorClaim.OwnerPositionID == nil || *snapshot.PredecessorClaim.OwnerPositionID != snapshot.Predecessor.PositionID {
		return persistence.PublicationAdmission{}, persistence.ErrPublicationAdmissionConflict
	}

	runtimeSettings, err := settings.ParsePublicationPlanningValues(snapshot.Settings)
	if err != nil {
		return persistence.PublicationAdmission{}, fmt.Errorf("parse moved publication settings: %w", err)
	}
	installationID, err := uuid.Parse(runtimeSettings.ActiveFFmpegInstallation)
	if err != nil || installationID == uuid.Nil {
		return persistence.PublicationAdmission{}, errors.New("active managed ffmpeg installation is unavailable")
	}
	var installation *persistence.ToolInstallation
	for i := range snapshot.Installations {
		if snapshot.Installations[i].ID == installationID {
			installation = &snapshot.Installations[i]
			break
		}
	}
	tools, err := selectedFFmpegPairFromInstallation(installation, service.platform)
	if err != nil {
		return persistence.PublicationAdmission{}, persistence.ErrPublicationAdmissionConflict
	}

	selected, choice, err := matchingRecheckMovedSource(contextSnapshot, snapshot)
	if err != nil {
		return persistence.PublicationAdmission{}, err
	}
	preview, err := buildFirstPublicationPreviewSelected(contextSnapshot, runtimeSettings, tools,
		contextSnapshot.Library.ReleaseID, contextSnapshot.Library.PositionID, &choice)
	if err != nil {
		return persistence.PublicationAdmission{}, fmt.Errorf("plan moved first publication: %w", err)
	}
	if len(preview.Blockers) != 0 || preview.SelectedSource == nil || preview.Transform == nil {
		return persistence.PublicationAdmission{}, persistence.ErrPublicationAdmissionConflict
	}
	if err := matchingRecheckMovedQuality(preview.Transform, selected, snapshot.Predecessor,
		publication.AudioPublicationFormat(runtimeSettings.PublicationFormat)); err != nil {
		return persistence.PublicationAdmission{}, err
	}
	manifest, err := finalizeFirstPublicationMovedNamespace(ctx, contextSnapshot,
		runtimeSettings.OutputDirectory, runtimeSettings.PublicationPathTemplate, &preview, snapshot.PredecessorClaim)
	if err != nil {
		return persistence.PublicationAdmission{}, fmt.Errorf("plan moved publication namespace: %w", err)
	}

	frozen := persistence.PublicationInputs{
		Version:              persistence.PublicationInputsVersion,
		QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind:           "audio",
		SourceAuthorityMode:  "automatic",
		Format:               publication.AudioPublicationFormat(runtimeSettings.PublicationFormat),
		Audio:                audioInputCopy(&selected.Input),
		DesiredTags:          cloneDesiredPublicationTags(preview.DesiredTags.Tags),
		InitialLyrics:        initialLyricsAuthorityInput(contextSnapshot),
		Tools:                append([]persistence.SourceAnalysisToolSelection(nil), tools...),
	}
	if err := frozen.Validate(); err != nil {
		return persistence.PublicationAdmission{}, fmt.Errorf("validate moved publication inputs: %w", err)
	}
	rawFrozen, err := json.Marshal(frozen)
	if err != nil {
		return persistence.PublicationAdmission{}, fmt.Errorf("encode moved publication inputs: %w", err)
	}
	sourceSelection := selected.Selection
	sourceSelection.HoldUUID = uuid.New()
	return persistence.PublicationAdmission{
		PositionID: contextSnapshot.Library.PositionID, ExpectedReleaseID: contextSnapshot.Library.ReleaseID,
		ExpectedEpoch: contextSnapshot.Library.ActiveEpoch, CandidateGeneration: uuid.New(),
		IntentKind: "audio", ToolsReadRequired: true,
		Tools:  append([]persistence.SourceAnalysisToolSelection(nil), tools...),
		Source: &sourceSelection, FrozenInputs: rawFrozen, NamespaceManifest: manifest,
	}, nil
}

// matchingRecheckMovedSource resolves exactly one stream of the exact moved
// association. It never selects another association, media variant or
// location: no source, several streams or a conflicting association fail
// closed.
func matchingRecheckMovedSource(snapshot *persistence.LibraryFirstPublicationContext, move persistence.MatchingRecheckMoveSnapshot) (*persistence.FirstPublicationSource, FirstPublicationSourceChoice, error) {
	if snapshot == nil || snapshot.Library == nil {
		return nil, FirstPublicationSourceChoice{}, persistence.ErrPublicationAdmissionConflict
	}
	var selected *persistence.FirstPublicationSource
	for i := range snapshot.Sources {
		source := &snapshot.Sources[i]
		if source.AssociationID != move.SourceAssociationID {
			continue
		}
		if source.Selection.MediaVariantID != move.MediaVariantID || source.Selection.LocationID != move.LocationID {
			return nil, FirstPublicationSourceChoice{}, fmt.Errorf("moved publication association resolves to another source: %w", persistence.ErrPublicationAdmissionConflict)
		}
		if selected != nil {
			return nil, FirstPublicationSourceChoice{}, fmt.Errorf("moved publication association has several audio streams: %w", persistence.ErrPublicationAdmissionConflict)
		}
		selected = source
	}
	if selected == nil || selected.Selection.RootID == uuid.Nil || selected.Selection.WorkID == uuid.Nil ||
		selected.Quality.StreamIndex < 0 || selected.Input.MediaVariantID != move.MediaVariantID ||
		selected.Input.LocationID != move.LocationID {
		return nil, FirstPublicationSourceChoice{}, fmt.Errorf("moved publication association is unavailable: %w", persistence.ErrPublicationAdmissionConflict)
	}
	choice := FirstPublicationSourceChoice{
		LocationID: selected.Selection.LocationID, MediaVariantID: selected.Selection.MediaVariantID,
		StreamIndex: selected.Quality.StreamIndex,
	}
	return selected, choice, nil
}

// matchingRecheckMovedQuality reuses the source-republish output quality
// comparison against the exact predecessor CURRENT. An unavailable predecessor
// witness, an unknown comparison or a would-be downgrade fails closed.
func matchingRecheckMovedQuality(transform *publication.AudioTransformPlan, selected *persistence.FirstPublicationSource, predecessor persistence.TrackPublication, format publication.AudioPublicationFormat) error {
	if transform == nil || selected == nil || predecessor.PositionID == uuid.Nil || predecessor.Generation == uuid.Nil ||
		len(predecessor.ActualAudio) == 0 {
		return persistence.ErrPublicationAdmissionConflict
	}
	var currentAudio persistence.PublicationCurrentAudio
	if err := json.Unmarshal(predecessor.ActualAudio, &currentAudio); err != nil ||
		currentAudio.Version != 1 || currentAudio.Facts.Validate() != nil {
		return fmt.Errorf("read predecessor publication audio: %w", persistence.ErrPublicationAdmissionConflict)
	}
	current := &persistence.LibraryPublicationCurrent{Record: predecessor, Audio: currentAudio}
	comparison := sourceRepublishOutputQualityComparison(transform, selected, current, format)
	if comparison == publication.QualityWorse || comparison == publication.QualityUnknown {
		return fmt.Errorf("moved publication would reduce predecessor quality: %w", persistence.ErrPublicationAdmissionConflict)
	}
	return nil
}
