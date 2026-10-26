package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// PrepareAudioUpgrade builds a private candidate from frozen new audio while
// carrying forward only the exact metadata and artwork observed on CURRENT.
func (preparation PublicationSimplePreparation) PrepareAudioUpgrade(
	ctx context.Context,
	input persistence.PublicationInputs,
	newAudioPath, currentPath, tempDir, outputPath string,
) (PublicationSimplePreparationResult, error) {
	if input.Version != persistence.PublicationAudioUpgradeInputsVersion || input.AudioUpgrade == nil ||
		input.Audio == nil ||
		inputPathInvalid(newAudioPath) || inputPathInvalid(currentPath) || inputPathInvalid(tempDir) || inputPathInvalid(outputPath) ||
		preparation.Commands == nil || preparation.Validator == nil || preparation.Metadata == nil || preparation.MetadataReader == nil || preparation.FFmpegPath == "" || preparation.CoverInput != nil {
		return PublicationSimplePreparationResult{}, errors.New("audio-upgrade preparation dependencies or input are incomplete")
	}
	if input.InitialLyrics != nil || input.ManualLyrics != nil || input.AutomaticLyricsRetry != nil || input.ManualCover != nil || input.Rewrite != nil {
		return PublicationSimplePreparationResult{}, errors.New("audio-upgrade preparation cannot carry other publication authorities")
	}
	if err := input.Validate(); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("validate frozen audio-upgrade input: %w", err)
	}
	current := input.AudioUpgrade.Current
	if !equalTags(input.DesiredTags, current.Tags) {
		return PublicationSimplePreparationResult{}, errors.New("audio-upgrade desired tags differ from current publication")
	}
	if err := ctx.Err(); err != nil {
		return PublicationSimplePreparationResult{}, err
	}
	if _, err := os.Lstat(outputPath); err == nil {
		return PublicationSimplePreparationResult{}, errors.New("publication candidate output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return PublicationSimplePreparationResult{}, fmt.Errorf("inspect publication candidate output: %w", err)
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("create publication temporary directory: %w", err)
	}
	privateCurrent := filepath.Join(tempDir, "current-inspection"+filepath.Ext(current.RelativePath))
	if err := copyCurrentPublication(currentPath, privateCurrent); err != nil {
		return PublicationSimplePreparationResult{}, err
	}
	request := metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false}
	capture, err := preparation.MetadataReader.Read(ctx, privateCurrent, request)
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("read current publication metadata: %w", err)
	}
	if !equalTags(capture.Tags, current.Tags) {
		return PublicationSimplePreparationResult{}, errors.New("current publication tags changed before audio upgrade")
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("observe current publication assets: %w", err)
	}
	if !reflect.DeepEqual(assets, current.Assets) {
		return PublicationSimplePreparationResult{}, errors.New("current publication assets changed before audio upgrade")
	}
	artworkSnapshot, err := metadata.ReadArtworkSnapshot(ctx, privateCurrent)
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("read current publication artwork: %w", err)
	}
	if !equalAudioUpgradeArtworkWitnesses(current.ArtworkInventory, artworkSnapshot.ArtworkInventory()) {
		return PublicationSimplePreparationResult{}, errors.New("current publication artwork inventory changed before audio upgrade")
	}

	sourceFacts := inputAudioFacts(input).AudioSourceFacts()
	if input.Format == publication.AudioFormatM4A && sourceFacts.Lossless != nil && *sourceFacts.Lossless && sourceFacts.Codec != "alac" && preparation.FFprobePath == "" {
		return PublicationSimplePreparationResult{}, errors.New("ALAC preservation proof requires pinned ffprobe")
	}
	privateSource := filepath.Join(tempDir, "new-audio."+string(sourceFacts.Container))
	if err := copyCurrentPublication(newAudioPath, privateSource); err != nil {
		return PublicationSimplePreparationResult{}, err
	}
	if err := buildSimpleAudioCandidate(ctx, preparation.Commands, preparation.FFmpegPath, input.Format, sourceFacts, privateSource, outputPath); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("transform audio-upgrade candidate: %w", err)
	}
	if _, err := preparation.Metadata.Write(ctx, outputPath, cloneTagMap(current.Tags), nil); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("restore current publication metadata: %w", err)
	}
	if err := metadata.RestoreArtworkSnapshot(ctx, outputPath, artworkSnapshot); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("restore current publication artwork: %w", err)
	}
	finalCapture, err := preparation.MetadataReader.Read(ctx, outputPath, request)
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("read audio-upgrade candidate metadata: %w", err)
	}
	if !equalTags(finalCapture.Tags, current.Tags) {
		return PublicationSimplePreparationResult{}, errors.New("audio-upgrade candidate metadata does not match current publication")
	}
	finalAssets, err := ObservePublicationAssets(finalCapture)
	if err != nil || !reflect.DeepEqual(finalAssets, current.Assets) {
		return PublicationSimplePreparationResult{}, errors.New("audio-upgrade candidate assets do not match current publication")
	}
	finalArtwork, err := metadata.ReadArtworkSnapshot(ctx, outputPath)
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("read audio-upgrade candidate artwork: %w", err)
	}
	if !equalAudioUpgradeArtworkWitnesses(current.ArtworkInventory, finalArtwork.ArtworkInventory()) {
		return PublicationSimplePreparationResult{}, errors.New("audio-upgrade candidate artwork inventory does not match current publication")
	}
	if input.Format == publication.AudioFormatM4A && sourceFacts.Lossless != nil && *sourceFacts.Lossless && sourceFacts.Codec != "alac" {
		if err := proveALACCandidate(ctx, preparation.Commands, preparation.FFmpegPath, preparation.FFprobePath, privateSource, outputPath, input.Audio.AudioFacts); err != nil {
			return PublicationSimplePreparationResult{}, err
		}
	}
	observed, err := preparation.Validator.Validate(ctx, outputPath, input)
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("validate audio-upgrade candidate: %w", err)
	}
	var actual struct {
		Version int                             `json:"version"`
		Facts   persistence.PersistedAudioFacts `json:"facts"`
	}
	if err := json.Unmarshal(observed.Audio, &actual); err != nil || actual.Version != 1 || actual.Facts.Validate() != nil {
		return PublicationSimplePreparationResult{}, errors.New("audio-upgrade validator returned invalid audio facts")
	}
	if publication.CompareQuality(audioUpgradeQualityCandidate(actual.Facts), audioUpgradeQualityCandidate(current.AudioFacts)) != publication.QualityBetter {
		return PublicationSimplePreparationResult{}, errors.New("audio-upgrade candidate quality is not strictly better than current publication")
	}
	return PublicationSimplePreparationResult{Observation: persistence.PublicationStageObservation{
		Version: 1, RelativePath: filepath.Base(outputPath), Ownership: observed.Ownership,
		PayloadSHA256: observed.SHA256, ActualAudio: observed.Audio, ActualMetadata: observed.Metadata,
		ActualAssets: observed.Assets, QualityPolicyVersion: observed.QualityPolicyVersion,
	}}, nil
}

func audioUpgradeQualityCandidate(facts persistence.PersistedAudioFacts) publication.QualityCandidate {
	return publication.QualityCandidate{StreamIndex: facts.StreamIndex, Codec: facts.Codec, Layout: facts.ChannelLayout,
		Lossless: facts.Lossless, Channels: facts.Channels, BitDepth: facts.BitDepth, SampleRate: facts.SampleRate, Bitrate: facts.Bitrate}
}

func inputPathInvalid(path string) bool {
	return path == "" || !filepath.IsAbs(path)
}

func equalAudioUpgradeArtworkWitnesses(witnesses []persistence.PublicationArtworkWitness, entries []metadata.ArtworkInventoryEntry) bool {
	if len(witnesses) != len(entries) {
		return false
	}
	for i, witness := range witnesses {
		entry := entries[i]
		if witness.Index != entry.Index || witness.Type != entry.Type || witness.Description != entry.Description || witness.MIME != entry.MIME ||
			witness.Size != int64(entry.Size) || witness.SHA256 != hex.EncodeToString(entry.SHA256[:]) {
			return false
		}
	}
	return true
}
