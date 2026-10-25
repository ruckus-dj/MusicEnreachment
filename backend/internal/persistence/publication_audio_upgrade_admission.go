package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

func (repository *SetupManagerRepository) validateAudioUpgradeAdmission(ctx context.Context, tx bun.Tx, request PublicationAdmission, input PublicationInputs, epoch uuid.UUID) error {
	frozen := input.AudioUpgrade
	if frozen == nil || request.Source == nil || request.ExpectedGeneration == nil || input.Audio == nil {
		return ErrPublicationAdmissionConflict
	}
	for _, table := range []string{"app_setting", "local_artist", "local_recording", "local_release", "local_track", "media_variant", "provider", "provider_artist", "provider_local_artist", "release_artist", "release_track", "source_analysis_step", "source_analysis_work", "source_location", "source_root", "source_work_variant_identity", "tool_installation", "track_artist", "track_source"} {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE "+table+" IN SHARE MODE"); err != nil {
			return fmt.Errorf("admit audio upgrade: guard %s: %w", table, err)
		}
	}
	var configuredFormat, configuredInstallation string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_format'`).Scan(ctx, &configuredFormat); err != nil {
		return fmt.Errorf("admit audio upgrade: read publication format: %w", err)
	}
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='active_ffmpeg_installation_id'`).Scan(ctx, &configuredInstallation); err != nil {
		return fmt.Errorf("admit audio upgrade: read active ffmpeg: %w", err)
	}
	if configuredFormat != string(input.Format) || len(input.Tools) == 0 || input.Tools[0].InstallationID.String() != configuredInstallation {
		return fmt.Errorf("admit audio upgrade: planning settings changed: %w", ErrPublicationAdmissionConflict)
	}
	plan, err := publication.PlanAudioTransform(input.Format, input.Audio.AudioFacts.AudioSourceFacts())
	if err != nil {
		return err
	}
	expectedPath := strings.TrimSuffix(frozen.Current.RelativePath, path.Ext(frozen.Current.RelativePath)) + "." + plan.Extension
	fileTargets := 0
	for _, node := range request.NamespaceManifest {
		if node.NodeKind == "file" {
			fileTargets++
			if node.RelativePath != expectedPath {
				return ErrPublicationAdmissionConflict
			}
		}
	}
	if fileTargets != 1 {
		return ErrPublicationAdmissionConflict
	}
	var current TrackPublication
	if err := tx.NewSelect().Model(&current).Where("position_id = ?", request.PositionID).For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("admit audio upgrade: lock current publication: %w", ErrPublicationAdmissionConflict)
	}
	if current.Epoch != epoch || current.Generation != frozen.Current.Generation || current.RelativePath != frozen.Current.RelativePath {
		return fmt.Errorf("admit audio upgrade: current publication changed: %w", ErrPublicationAdmissionConflict)
	}
	var ownership PublicationCurrentOwnership
	var audio PublicationCurrentAudio
	if decodePublicationCurrentContract(current.Ownership, &ownership) != nil || decodePublicationCurrentContract(current.ActualAudio, &audio) != nil ||
		!reflect.DeepEqual(ownership.Identity, frozen.Current.Ownership) || !reflect.DeepEqual(audio.Facts, frozen.Current.AudioFacts) {
		return fmt.Errorf("admit audio upgrade: frozen current facts changed: %w", ErrPublicationAdmissionConflict)
	}
	assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	if err != nil || !reflect.DeepEqual(assets, frozen.Current.Assets) {
		return fmt.Errorf("admit audio upgrade: frozen current assets changed: %w", ErrPublicationAdmissionConflict)
	}
	var authority PositionSourceAuthority
	if err := tx.NewSelect().Model(&authority).Where("position_id = ?", request.PositionID).For("UPDATE").Scan(ctx); err != nil || authority.Mode != "automatic" || authority.Revision != frozen.SourceAuthorityRevision || authority.PublicationEpoch != epoch || authority.PublicationGeneration != current.Generation {
		return fmt.Errorf("admit audio upgrade: automatic source authority changed: %w", ErrPublicationAdmissionConflict)
	}
	var tagWitness struct {
		Tags map[string][]string `json:"tags"`
	}
	if err := json.Unmarshal(current.ActualMetadata, &tagWitness); err != nil || !reflect.DeepEqual(tagWitness.Tags, frozen.Current.Tags) {
		return fmt.Errorf("admit audio upgrade: frozen current tags changed: %w", ErrPublicationAdmissionConflict)
	}
	var contextSnapshot *LibraryFirstPublicationContext
	if err := readLibraryFirstPublicationContextTx(ctx, tx, request.ExpectedReleaseID, request.PositionID, &contextSnapshot); err != nil {
		return fmt.Errorf("admit audio upgrade: read source candidate universe: %w", ErrPublicationAdmissionConflict)
	}
	if contextSnapshot.Library == nil || contextSnapshot.Library.Current == nil {
		return ErrPublicationAdmissionConflict
	}
	candidates := make([]publication.QualityCandidate, 0, len(contextSnapshot.Sources))
	for _, source := range contextSnapshot.Sources {
		if source.RequiresExplicitStreamSelection || source.AudioStreamCount != 1 {
			continue
		}
		if _, err := publication.PlanAudioTransform(input.Format, source.Quality.AudioSourceFacts()); err != nil {
			continue
		}
		facts := source.Quality
		candidates = append(candidates, publication.QualityCandidate{VariantUUID: source.Selection.MediaVariantID.String(), LocationUUID: source.Selection.LocationID.String(), StreamIndex: facts.StreamIndex,
			Lossless: facts.Lossless, Codec: facts.Codec, Channels: facts.Channels, Layout: source.ChannelLayout, BitDepth: facts.BitDepth, SampleRate: facts.SampleRate, Bitrate: facts.Bitrate})
	}
	currentCandidate := publication.QualityCandidate{VariantUUID: uuid.Nil.String(), LocationUUID: uuid.Nil.String(), StreamIndex: frozen.Current.AudioFacts.StreamIndex,
		Lossless: frozen.Current.AudioFacts.Lossless, Codec: frozen.Current.AudioFacts.Codec, Channels: frozen.Current.AudioFacts.Channels,
		Layout: frozen.Current.AudioFacts.ChannelLayout, BitDepth: frozen.Current.AudioFacts.BitDepth, SampleRate: frozen.Current.AudioFacts.SampleRate, Bitrate: frozen.Current.AudioFacts.Bitrate}
	selection := publication.SelectQuality(candidates, &currentCandidate)
	if !selection.ReplacesCurrent || selection.Candidate == nil || selection.Candidate.VariantUUID != input.Audio.MediaVariantID.String() ||
		selection.Candidate.LocationUUID != input.Audio.LocationID.String() || selection.Candidate.StreamIndex != input.Audio.AudioFacts.StreamIndex || !reflect.DeepEqual(input.Audio.AudioFacts, sourceFactsForUpgrade(contextSnapshot.Sources, *request.Source, input.Audio.AudioFacts.StreamIndex)) {
		return fmt.Errorf("admit audio upgrade: selected source is not a strictly better candidate: %w", ErrPublicationAdmissionConflict)
	}
	return nil
}

func sourceFactsForUpgrade(sources []FirstPublicationSource, selected PublicationSourceSelection, stream int) PersistedAudioFacts {
	for _, source := range sources {
		if source.Selection.LocationID == selected.LocationID && source.Selection.MediaVariantID == selected.MediaVariantID && source.Input.WorkID == selected.WorkID && source.Quality.StreamIndex == stream {
			return source.Quality
		}
	}
	return PersistedAudioFacts{}
}

func (repository *SetupManagerRepository) applyAutomaticAudioUpgradeSourceSelection(ctx context.Context, tx bun.Tx, request PublicationAdmission, input PublicationInputs) (uuid.UUID, bool, error) {
	var snapshot *LibraryFirstPublicationContext
	if err := readLibraryFirstPublicationContextTx(ctx, tx, request.ExpectedReleaseID, request.PositionID, &snapshot); err != nil {
		return uuid.Nil, false, fmt.Errorf("admit audio upgrade: read source association witness: %w", ErrPublicationAdmissionConflict)
	}
	if snapshot == nil || input.Audio == nil || request.Source == nil {
		return uuid.Nil, false, ErrPublicationAdmissionConflict
	}
	var associationID uuid.UUID
	for _, source := range snapshot.Sources {
		if source.Input.WorkID == request.Source.WorkID && source.Selection.LocationID == request.Source.LocationID &&
			source.Selection.MediaVariantID == request.Source.MediaVariantID && source.Quality.StreamIndex == input.Audio.AudioFacts.StreamIndex &&
			!source.RequiresExplicitStreamSelection && source.AudioStreamCount == 1 {
			associationID = source.AssociationID
			break
		}
	}
	if associationID == uuid.Nil {
		return uuid.Nil, false, fmt.Errorf("admit audio upgrade: winning source association changed: %w", ErrPublicationAdmissionConflict)
	}
	trackID, changed, err := applyTrackSourceSelection(ctx, tx, request.PositionID, *request.Source, *input.Audio,
		FirstPublicationSourceSelection{AssociationID: associationID, Associations: snapshot.SourceAssociations}, "admit audio upgrade")
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := validateAndLockPublicationSource(ctx, tx, request.PositionID, *request.Source, *input.Audio); err != nil {
		return uuid.Nil, false, err
	}
	return trackID, changed, nil
}
