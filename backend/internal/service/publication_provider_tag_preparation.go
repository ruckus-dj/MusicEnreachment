package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// PrepareProviderTags applies only an admitted provider patch to a private copy
// of CURRENT. It neither resolves desired local metadata nor acquires assets.
func (preparation PublicationSimplePreparation) PrepareProviderTags(ctx context.Context, input persistence.PublicationInputs, currentPath, tempDir, outputPath string) (PublicationSimplePreparationResult, error) {
	if input.Version != persistence.PublicationProviderTagInputsVersion || input.ProviderTags == nil || input.Rewrite == nil ||
		inputPathInvalid(currentPath) || inputPathInvalid(tempDir) || inputPathInvalid(outputPath) ||
		preparation.Metadata == nil || preparation.MetadataReader == nil || preparation.Validator == nil ||
		preparation.Commands == nil || preparation.FFmpegPath == "" || preparation.CoverInput != nil {
		return PublicationSimplePreparationResult{}, errors.New("provider-tag preparation dependencies or input are incomplete")
	}
	if err := input.Validate(); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("validate frozen provider-tag input: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return PublicationSimplePreparationResult{}, err
	}
	if _, err := os.Lstat(outputPath); err == nil {
		return PublicationSimplePreparationResult{}, errors.New("provider-tag candidate already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return PublicationSimplePreparationResult{}, fmt.Errorf("inspect provider-tag candidate: %w", err)
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("create provider-tag temporary directory: %w", err)
	}
	privateCurrent := filepath.Join(tempDir, "provider-current"+filepath.Ext(input.Rewrite.RelativePath))
	if err := copyCurrentPublication(currentPath, privateCurrent); err != nil {
		return PublicationSimplePreparationResult{}, err
	}
	witness := input.ProviderTags.Current
	request := metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false}
	before, err := preparation.MetadataReader.Read(ctx, privateCurrent, request)
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("read provider-tag CURRENT metadata: %w", err)
	}
	// A whole-job retry may read the already installed patch while the DB still
	// owns the old generation. Only the complete old or complete intended map is
	// accepted; unrelated physical tag changes cannot be adopted.
	if !equalTags(before.Tags, witness.Tags) && !equalTags(before.Tags, input.DesiredTags) {
		return PublicationSimplePreparationResult{}, errors.New("provider-tag CURRENT metadata changed")
	}
	assets, err := ObservePublicationAssets(before)
	if err != nil || !reflect.DeepEqual(assets, witness.Assets) {
		return PublicationSimplePreparationResult{}, errors.New("provider-tag CURRENT assets changed")
	}
	artwork, err := metadata.ReadArtworkSnapshot(ctx, privateCurrent)
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("read provider-tag CURRENT artwork: %w", err)
	}
	if !equalAudioUpgradeArtworkWitnesses(witness.ArtworkInventory, artwork.ArtworkInventory()) {
		return PublicationSimplePreparationResult{}, errors.New("provider-tag CURRENT artwork changed")
	}
	beforeHash, err := publicationEncodedAudioSHA256(ctx, preparation.Commands, preparation.FFmpegPath, privateCurrent, witness.AudioFacts.Container)
	if err != nil {
		return PublicationSimplePreparationResult{}, err
	}
	if beforeHash != input.ProviderTags.AudioPayloadSHA256 {
		return PublicationSimplePreparationResult{}, errors.New("provider-tag CURRENT encoded audio changed")
	}
	if err := copyCurrentPublication(privateCurrent, outputPath); err != nil {
		return PublicationSimplePreparationResult{}, err
	}
	if _, err := preparation.Metadata.Write(ctx, outputPath, cloneTagMap(input.DesiredTags), nil); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("write provider-tag candidate metadata: %w", err)
	}
	if err := metadata.RestoreArtworkSnapshot(ctx, outputPath, artwork); err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("restore provider-tag candidate artwork: %w", err)
	}
	after, err := preparation.MetadataReader.Read(ctx, outputPath, request)
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("read provider-tag candidate metadata: %w", err)
	}
	if !equalTags(after.Tags, input.DesiredTags) {
		return PublicationSimplePreparationResult{}, errors.New("provider-tag candidate metadata does not match the admitted patch")
	}
	afterAssets, err := ObservePublicationAssets(after)
	if err != nil || !reflect.DeepEqual(afterAssets, witness.Assets) {
		return PublicationSimplePreparationResult{}, errors.New("provider-tag candidate changed CURRENT assets")
	}
	afterArtwork, err := metadata.ReadArtworkSnapshot(ctx, outputPath)
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("read provider-tag candidate artwork: %w", err)
	}
	if !equalAudioUpgradeArtworkWitnesses(witness.ArtworkInventory, afterArtwork.ArtworkInventory()) {
		return PublicationSimplePreparationResult{}, errors.New("provider-tag candidate changed CURRENT artwork")
	}
	afterHash, err := publicationEncodedAudioSHA256(ctx, preparation.Commands, preparation.FFmpegPath, outputPath, witness.AudioFacts.Container)
	if err != nil {
		return PublicationSimplePreparationResult{}, err
	}
	if afterHash != beforeHash {
		return PublicationSimplePreparationResult{}, errors.New("provider-tag candidate changed CURRENT encoded audio")
	}
	var observed PublicationStageObservation
	if validator, ok := preparation.Validator.(interface {
		ValidateProviderTags(context.Context, string, persistence.PublicationInputs) (PublicationStageObservation, error)
	}); ok {
		observed, err = validator.ValidateProviderTags(ctx, outputPath, input)
	} else {
		observed, err = preparation.Validator.Validate(ctx, outputPath, input)
	}
	if err != nil {
		return PublicationSimplePreparationResult{}, fmt.Errorf("validate provider-tag candidate: %w", err)
	}
	return PublicationSimplePreparationResult{Observation: persistence.PublicationStageObservation{
		Version: 1, RelativePath: filepath.Base(outputPath), Ownership: observed.Ownership,
		PayloadSHA256: observed.SHA256, ActualAudio: observed.Audio, ActualMetadata: observed.Metadata,
		ActualAssets: observed.Assets, QualityPolicyVersion: observed.QualityPolicyVersion,
	}, PreservedAudioSHA256: afterHash}, nil
}

// publicationEncodedAudioSHA256 hashes the selected encoded packet data, not
// decoded PCM or container metadata. The input is always a private local copy.
func publicationEncodedAudioSHA256(ctx context.Context, commands PublicationCommandRunner, ffmpegPath, inputPath string, container publication.AudioContainer) (string, error) {
	demuxer, mov := publication.InputDemuxer(container)
	if commands == nil || ffmpegPath == "" || !filepath.IsAbs(inputPath) || demuxer == "" {
		return "", errors.New("encoded audio hashing dependencies are incomplete")
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file", "-f", demuxer}
	if mov {
		args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	args = append(args, "-i", inputPath, "-map", "0:a:0", "-c:a", "copy", "-vn", "-sn", "-dn", "-f", "hash", "-hash", "sha256", "-")
	result, err := commands.Run(ctx, ffmpegPath, args...)
	if !result.QuiescenceEstablished {
		return "", errors.Join(err, errors.New("encoded audio hashing did not establish command quiescence"))
	}
	if err != nil {
		return "", fmt.Errorf("hash CURRENT encoded audio: %w", err)
	}
	value, ok := strings.CutPrefix(strings.TrimSpace(string(result.Output)), "SHA256=")
	decoded, decodeErr := hex.DecodeString(value)
	if !ok || decodeErr != nil || len(decoded) != 32 || value != strings.ToLower(value) {
		return "", errors.New("encoded audio hashing returned an invalid SHA-256 digest")
	}
	return value, nil
}

// publicationDecodedAudioSHA256 proves that a lossless candidate decodes to the
// same selected PCM stream as its private source.
func publicationDecodedAudioSHA256(ctx context.Context, commands PublicationCommandRunner, ffmpegPath, inputPath string, container publication.AudioContainer, streamIndex int) (string, error) {
	demuxer, mov := publication.InputDemuxer(container)
	if commands == nil || ffmpegPath == "" || !filepath.IsAbs(inputPath) || demuxer == "" || streamIndex < 0 {
		return "", errors.New("decoded audio proof dependencies are incomplete")
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file", "-f", demuxer}
	if mov {
		args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	args = append(args, "-xerror", "-i", inputPath, "-map", fmt.Sprintf("0:%d", streamIndex), "-vn", "-sn", "-dn", "-c:a", "pcm_s32le", "-f", "hash", "-hash", "sha256", "-")
	result, err := commands.Run(ctx, ffmpegPath, args...)
	if !result.QuiescenceEstablished {
		return "", errors.Join(err, errors.New("decoded audio proof did not establish command quiescence"))
	}
	if err != nil {
		return "", fmt.Errorf("decode audio for lossless proof: %w", err)
	}
	value, ok := strings.CutPrefix(strings.TrimSpace(string(result.Output)), "SHA256=")
	decoded, decodeErr := hex.DecodeString(value)
	if !ok || decodeErr != nil || len(decoded) != 32 || value != strings.ToLower(value) {
		return "", errors.New("decoded audio proof returned an invalid SHA-256 digest")
	}
	return value, nil
}

func publicationALACTechnicalAnalysis(ctx context.Context, commands PublicationCommandRunner, ffprobePath, inputPath string, container publication.AudioContainer) (SourceTechnicalAnalysis, error) {
	demuxer, mov := publication.InputDemuxer(container)
	if commands == nil || !filepath.IsAbs(ffprobePath) || !filepath.IsAbs(inputPath) || demuxer == "" {
		return SourceTechnicalAnalysis{}, errors.New("ALAC technical proof dependencies are incomplete")
	}
	args := []string{"-v", "error", "-protocol_whitelist", "file"}
	if mov {
		args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	args = append(args, "-f", demuxer, "-show_format", "-show_streams", "-of", "json", inputPath)
	result, err := commands.Run(ctx, ffprobePath, args...)
	if !result.QuiescenceEstablished {
		return SourceTechnicalAnalysis{}, errors.Join(err, errors.New("ALAC technical probe did not establish command quiescence"))
	}
	if err != nil {
		return SourceTechnicalAnalysis{}, fmt.Errorf("probe ALAC candidate audio facts: %w", err)
	}
	analysis, err := ParseSourceTechnicalAnalysis(result.Output)
	if err != nil {
		return SourceTechnicalAnalysis{}, fmt.Errorf("parse ALAC technical proof: %w", err)
	}
	if len(analysis.Streams) == 0 {
		return SourceTechnicalAnalysis{}, errors.New("ALAC technical proof found no audio stream")
	}
	return analysis, nil
}
