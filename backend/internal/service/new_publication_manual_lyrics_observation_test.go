package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type manualLyricsObservationRepository struct {
	*scopeRepositoryFake
	*optionalLyricsV3Repository
}

func TestManualLyricsObservationRequiresNativeCopyProof(t *testing.T) {
	if _, err := newPublicationManualLyricsObservationAuthority(context.Background(), nil, nil, nil, persistence.PublicationInputs{}); err == nil {
		t.Fatal("expected native initial-copy proof requirement")
	}
	var authority *publicationManualLyricsObservationAuthority
	if _, err := authority.Observe(context.Background()); err == nil {
		t.Fatal("expected unavailable observation authority")
	}
}

func TestManualLyricsObservationAuthorityUsesNativePositiveEvidence(t *testing.T) {
	stage, input, authority, scope, reservation := manualLyricsObservationFixture(t)
	beforeInput, err := os.ReadFile(stage.paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := authority.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if observation.PayloadSHA256 == "" || observation.ActualAudio == nil || observation.ActualMetadata == nil || observation.ActualAssets == nil {
		t.Fatalf("observation lacks native payload evidence: %#v", observation)
	}
	outputInfo, err := os.Stat(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Version != 1 || observation.RelativePath != stage.paths.RelativePath ||
		observation.Ownership.ExpectedSize == nil || *observation.Ownership.ExpectedSize != outputInfo.Size() ||
		observation.Ownership.ExpectedMTime == nil || *observation.Ownership.ExpectedMTime != outputInfo.ModTime().UnixMicro() ||
		observation.QualityPolicyVersion != input.QualityPolicyVersion {
		t.Fatalf("native ownership facts = %#v, output info = %#v", observation, outputInfo)
	}
	output, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(output)
	if observation.PayloadSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("observation payload digest does not match native output")
	}
	var audio persistence.PublicationCurrentAudio
	var tags persistence.PublicationCommitMetadata
	var assets persistence.PublicationAssetOutcomes
	if err := json.Unmarshal(observation.ActualAudio, &audio); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(observation.ActualMetadata, &tags); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(observation.ActualAssets, &assets); err != nil {
		t.Fatal(err)
	}
	if audio.Version != 1 || !reflect.DeepEqual(audio.Facts, input.Rewrite.AudioFacts) {
		t.Fatalf("audio evidence = %#v", audio)
	}
	wantTags := map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {input.ManualLyrics.Value}}
	if tags.Version != 1 || !reflect.DeepEqual(tags.Tags, wantTags) {
		t.Fatalf("metadata evidence = %#v", tags)
	}
	if err := assets.ValidateAgainstMetadata(tags.Tags, nil); err != nil {
		t.Fatalf("assets do not validate against observed metadata: %v", err)
	}
	if assets.Cover.State != persistence.PublicationAssetAbsent || assets.Lyrics.State != persistence.PublicationAssetEmbedded {
		t.Fatalf("asset evidence = %#v", assets)
	}
	afterInput, err := os.ReadFile(stage.paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeInput, afterInput) || !reflect.DeepEqual(input, authority.input) {
		t.Fatal("observation or metadata mutation changed frozen input")
	}
	scope.releaseSynchronousWork(reservation)
}

func manualLyricsObservationFixture(t *testing.T) (*preparedPublicationStage, persistence.PublicationInputs, *publicationManualLyricsObservationAuthority, *PublicationExecutionScope, *publicationSyncReservation) {
	t.Helper()
	fixture, stage, _ := preparedManualLyricsFLACStage(t)
	repository := fixture.preparer.Repository.(*optionalLyricsV3Repository)
	input := repository.input.Clone()
	input.DesiredTags = map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}
	installationID := uuid.New()
	input.Tools = []persistence.SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffmpeg", Executable: "ffmpeg", Version: "1", VersionBanner: "ffmpeg 1"},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffprobe", Executable: "ffprobe", Version: "1", VersionBanner: "ffprobe 1"},
	}
	input.ManualLyrics = &persistence.PublicationManualLyricsInput{State: "value", Value: "[00:00.00]Lyric", ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration}
	repository.input = input.Clone()
	scope, reservation := initialOutputCopyScope(t, fixture.guard)
	scope.repository = &manualLyricsObservationRepository{
		scopeRepositoryFake:        &scopeRepositoryFake{valid: true, marked: true},
		optionalLyricsV3Repository: repository,
	}
	proof, err := stage.initialOutputCopy(context.Background(), scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	// Bind authority to the copied, untouched native output before metadata changes.
	authority, err := newPublicationManualLyricsObservationAuthority(context.Background(), scope, reservation, proof, input)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := metadata.SnapshotFlacArtwork(context.Background(), stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadata.WriteFlacTagsPreservingArtwork(context.Background(), stage.paths.OutputPath, map[string][]string{
		"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {input.ManualLyrics.Value},
	}, baseline); err != nil {
		t.Fatal(err)
	}
	return stage, input, authority, scope, reservation
}

func TestManualLyricsObservationRejectsMandatoryMetadataMismatch(t *testing.T) {
	stage, _, authority, scope, reservation := manualLyricsObservationFixture(t)
	baseline, err := metadata.SnapshotFlacArtwork(context.Background(), stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadata.WriteFlacTagsPreservingArtwork(context.Background(), stage.paths.OutputPath, map[string][]string{
		"TITLE": {"Wrong"}, "ARTIST": {"Artist"}, "LYRICS": {"[00:00.00]Lyric"},
	}, baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Observe(context.Background()); err == nil {
		t.Fatal("accepted native output with mismatched mandatory TITLE")
	}
	scope.releaseSynchronousWork(reservation)
}

func TestManualLyricsObservationRejectsEncodedAudioMutation(t *testing.T) {
	stage, _, authority, scope, reservation := manualLyricsObservationFixture(t)
	original, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	mutated := append([]byte(nil), original...)
	mutated[len(mutated)-1] ^= 0x1 // FLAC audio frame, after the metadata blocks.
	if err := os.WriteFile(stage.paths.OutputPath, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Observe(context.Background()); err == nil {
		t.Fatal("accepted native output with changed encoded audio")
	}
	scope.releaseSynchronousWork(reservation)
}
