package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"go.senan.xyz/taglib"
)

func TestEqualAudioUpgradeArtworkWitnesses(t *testing.T) {
	data := []byte("opaque image")
	digest := sha256.Sum256(data)
	entries := []metadata.ArtworkInventoryEntry{{
		Index: 0, Type: "Front Cover", Description: "front", MIME: "application/octet-stream",
		Size: len(data), SHA256: digest,
	}}
	witnesses := []persistence.PublicationArtworkWitness{{
		Index: 0, Type: "Front Cover", Description: "front", MIME: "application/octet-stream",
		Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
	}}
	if !equalAudioUpgradeArtworkWitnesses(witnesses, entries) {
		t.Fatal("matching artwork witness was rejected")
	}
	witnesses[0].Description = "changed"
	if equalAudioUpgradeArtworkWitnesses(witnesses, entries) {
		t.Fatal("changed artwork descriptor was accepted")
	}
	witnesses[0].Description = "front"
	witnesses[0].SHA256 = ""
	if equalAudioUpgradeArtworkWitnesses(witnesses, entries) {
		t.Fatal("changed artwork digest was accepted")
	}
}

func audioUpgradeImage(t *testing.T, mimeType string, pixel color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.SetRGBA(x, y, pixel)
		}
	}
	var encoded bytes.Buffer
	var err error
	switch mimeType {
	case "image/jpeg":
		err = jpeg.Encode(&encoded, img, nil)
	case "image/png":
		err = png.Encode(&encoded, img)
	default:
		t.Fatalf("unsupported test image MIME %q", mimeType)
	}
	if err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestPrepareAudioUpgradePreservesCurrentAssetsAndRejectsDriftOrNoImprovement(t *testing.T) {
	ctx := context.Background()
	fixture := makeRewriteFixture(t)
	currentPath := filepath.Join(fixture.output, "current.flac")
	newAudioPath := filepath.Join(t.TempDir(), "new-audio.flac")
	contents, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	contents, err = flacFixtureWithoutPictures(contents)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{currentPath, newAudioPath} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := (metadata.Writer{}).Write(ctx, path, map[string][]string{
			"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {"[ar:Artist]\r\n[00:01.00]Words\r\n"},
		}, nil); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := (metadata.Writer{}).Write(ctx, newAudioPath, map[string][]string{
		"TITLE": {"New source title must not win"}, "LYRICS": {"New source lyrics must not win"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := taglib.WriteImageOptions(newAudioPath, audioUpgradeImage(t, "image/jpeg", color.RGBA{G: 220, A: 255}), 0, "Front Cover", "new source", "image/jpeg"); err != nil {
		t.Fatal(err)
	}

	// CURRENT carries two distinct real pictures. The contract is still exact
	// image bytes and descriptors; the bytes are valid images so the observed
	// cover MIME can be established without a decoding fixture.
	currentArtwork := metadata.ArtworkSnapshot{Images: []metadata.ArtworkSnapshotImage{
		{Index: 0, Type: "Front Cover", Description: "front", MIME: "image/jpeg", Data: audioUpgradeImage(t, "image/jpeg", color.RGBA{R: 220, A: 255})},
		{Index: 1, Type: "Back Cover", Description: "back", MIME: "image/png", Data: audioUpgradeImage(t, "image/png", color.RGBA{B: 220, A: 255})},
	}}
	for _, image := range currentArtwork.Images {
		if err := taglib.WriteImageOptions(currentPath, image.Data, image.Index, image.Type, image.Description, image.MIME); err != nil {
			t.Fatal(err)
		}
	}
	capture, err := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		t.Fatal(err)
	}
	artworkSnapshot, err := metadata.ReadArtworkSnapshot(ctx, currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(artworkSnapshot.Images) != 2 {
		t.Fatalf("CURRENT artwork count = %d, want both images", len(artworkSnapshot.Images))
	}

	currentInfo, err := os.Stat(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	currentBytes, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	currentDigest := sha256.Sum256(currentBytes)
	size, mtime := currentInfo.Size(), currentInfo.ModTime().UnixMicro()
	trueValue := true
	channels, currentDepth, currentRate := 2, 16, 44100
	newDepth, newRate := 24, 48000
	currentFacts := persistence.PersistedAudioFacts{
		StreamIndex: 0, Codec: "flac", Lossless: &trueValue, Channels: &channels,
		ChannelLayout: "stereo", BitDepth: &currentDepth, SampleRate: &currentRate, Container: "flac", SupportedSourceContainer: true,
	}
	_, tools, _ := pinnedToolFixture(t, true)
	newAudioInfo, err := os.Stat(newAudioPath)
	if err != nil {
		t.Fatal(err)
	}
	newAudio := persistence.PersistedAudioFacts{
		StreamIndex: 0, Codec: "flac", Lossless: &trueValue, Channels: &channels,
		ChannelLayout: "stereo", BitDepth: &newDepth, SampleRate: &newRate, Container: "flac", SupportedSourceContainer: true,
	}
	input := persistence.PublicationInputs{
		Version: persistence.PublicationAudioUpgradeInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "audio", SourceAuthorityMode: "automatic", Format: publication.AudioFormatSource,
		DesiredTags: capture.Tags, Tools: tools,
		Audio: &persistence.PublicationAudioInput{
			WorkID: uuid.New(), RootID: uuid.New(), LocationID: uuid.New(), MediaVariantID: uuid.New(), ProbeResultID: uuid.New(),
			ConfiguredRootPath: filepath.Dir(newAudioPath), InventoryRootPath: filepath.Dir(newAudioPath),
			RelativePath: "new-audio.flac", SizeBytes: newAudioInfo.Size(), MTime: newAudioInfo.ModTime().UTC().Truncate(time.Microsecond),
			AnalysisPolicyVersion: 1, FFProbeVersion: "test", AudioFacts: newAudio,
		},
		AudioUpgrade: &persistence.PublicationAudioUpgradeInput{SourceAuthorityRevision: uuid.New(), Current: persistence.PublicationCurrentWitness{
			Generation: uuid.New(), RelativePath: "Artist/Album/current.flac",
			Ownership:  persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(currentDigest[:])},
			AudioFacts: currentFacts, Assets: assets, Tags: capture.Tags, ArtworkInventory: audioUpgradeArtworkWitnesses(artworkSnapshot.ArtworkInventory()),
		}},
	}
	probe := func(_ context.Context, _ string, _ sourcefs.RegularFile, _ publication.AudioContainer) ([]byte, error) {
		return []byte(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","sample_fmt":"s32","channels":2,"channel_layout":"stereo","sample_rate":"48000","bits_per_raw_sample":"24"},{"index":1,"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1},"tags":{"mimetype":"image/jpeg"}},{"index":2,"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1},"tags":{"mimetype":"image/png"}}]}`), nil
	}
	preparation := PublicationSimplePreparation{
		Commands: &simplePreparationProbeCommand{}, FFmpegPath: "ffmpeg", Metadata: metadata.Writer{}, MetadataReader: metadata.Reader{},
		Validator: RealPublicationStageValidator{Tools: publicationValidatorTools{}, MetadataReader: metadata.Reader{}, Probe: probe},
	}
	outputPath := filepath.Join(t.TempDir(), "candidate.flac")
	prepare := func(candidateInput persistence.PublicationInputs, candidateCurrentPath string) error {
		outputPath = filepath.Join(t.TempDir(), "candidate.flac")
		_, err := preparation.PrepareAudioUpgrade(ctx, candidateInput, newAudioPath, candidateCurrentPath,
			filepath.Join(t.TempDir(), "temporary"), outputPath)
		return err
	}

	if err := prepare(input, currentPath); err != nil {
		t.Fatalf("valid audio upgrade preparation failed: %v", err)
	}
	firstCandidate := outputPath
	preparedCapture, err := (metadata.Reader{}).Read(ctx, outputPath, metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preparedCapture.Tags, capture.Tags) || len(preparedCapture.Tags["LYRICS"]) != 1 || preparedCapture.Tags["LYRICS"][0] != capture.Tags["LYRICS"][0] {
		t.Fatalf("prepared tags = %#v, want exact CURRENT tags %#v", preparedCapture.Tags, capture.Tags)
	}
	preparedArtwork, err := metadata.ReadArtworkSnapshot(ctx, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preparedArtwork, artworkSnapshot) {
		t.Fatalf("prepared artwork inventory differs from CURRENT: got %#v, want %#v", preparedArtwork.ArtworkInventory(), artworkSnapshot.ArtworkInventory())
	}

	t.Run("preparation retry with already-installed candidate", func(t *testing.T) {
		// Model redelivery after the first candidate was installed as CURRENT
		// but CommitPublicationResult failed. The retry still carries the original
		// frozen ownership and audio facts, while CURRENT has new audio bytes;
		// metadata and artwork remain exactly the frozen authority.
		if _, err := os.Stat(newAudioPath); err != nil {
			t.Fatalf("frozen new-audio source was not retained for retry: %v", err)
		}
		if err := prepare(input, firstCandidate); err != nil {
			t.Fatalf("audio-upgrade preparation retry rejected installed candidate: %v", err)
		}
		retryCapture, err := (metadata.Reader{}).Read(ctx, outputPath, metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(retryCapture.Tags, capture.Tags) || len(retryCapture.Tags["LYRICS"]) != 1 || retryCapture.Tags["LYRICS"][0] != capture.Tags["LYRICS"][0] {
			t.Fatalf("retry tags = %#v, want exact CURRENT tags and lyrics %#v", retryCapture.Tags, capture.Tags)
		}
		retryArtwork, err := metadata.ReadArtworkSnapshot(ctx, outputPath)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(retryArtwork, artworkSnapshot) {
			t.Fatalf("retry artwork differs from CURRENT: got %#v, want %#v", retryArtwork.ArtworkInventory(), artworkSnapshot.ArtworkInventory())
		}

		// A retry still rejects metadata drift even when CURRENT's audio already
		// matches the new candidate.
		driftPath := filepath.Join(t.TempDir(), "drift.flac")
		firstCandidateBytes, err := os.ReadFile(firstCandidate)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(driftPath, firstCandidateBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := (metadata.Writer{}).Write(ctx, driftPath, map[string][]string{"TITLE": {"Changed"}, "ARTIST": {"Artist"}, "LYRICS": {"[ar:Artist]\r\n[00:01.00]Words\r\n"}}, nil); err != nil {
			t.Fatal(err)
		}
		if err := prepare(input, driftPath); err == nil {
			t.Fatal("audio upgrade accepted changed CURRENT tags")
		}
	})

	// Equal-quality output is rejected by the real validator's quality comparison.
	equalQualityInput := input.Clone()
	equalQualityInput.Audio.AudioFacts = currentFacts
	equalQualityProbe := func(_ context.Context, _ string, _ sourcefs.RegularFile, _ publication.AudioContainer) ([]byte, error) {
		return []byte(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","sample_fmt":"s16","channels":2,"channel_layout":"stereo","sample_rate":"44100","bits_per_raw_sample":"16"},{"index":1,"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1},"tags":{"mimetype":"image/jpeg"}},{"index":2,"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1},"tags":{"mimetype":"image/png"}}]}`), nil
	}
	equalPreparation := preparation
	equalPreparation.Validator = RealPublicationStageValidator{Tools: publicationValidatorTools{}, MetadataReader: metadata.Reader{}, Probe: equalQualityProbe}
	_, err = equalPreparation.PrepareAudioUpgrade(ctx, equalQualityInput, newAudioPath, currentPath,
		filepath.Join(t.TempDir(), "temporary"), filepath.Join(t.TempDir(), "candidate.flac"))
	if err == nil {
		t.Fatal("audio upgrade accepted output that did not improve CURRENT quality")
	}
	if !strings.Contains(err.Error(), "quality is not strictly better than current") {
		t.Fatalf("equal-quality failure did not reach quality comparison: %v", err)
	}
}
