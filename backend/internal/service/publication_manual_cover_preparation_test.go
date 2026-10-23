//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestPublicationSimplePreparationManualCoverSourceFreeRewrite(t *testing.T) {
	ctx := context.Background()
	fixture := makeRewriteFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	_ = os.Remove(filepath.Join(fixture.output, "current.mp3"))
	inputPath := filepath.Join(fixture.output, "current.flac")
	contents, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	contents, err = flacFixtureWithoutPictures(contents)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	lyrics := "[ar:Artist]\r\n[00:01.00]Words\r\n"
	tags := map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {lyrics}}
	imageA := simpleCoverPNG(t, color.RGBA{R: 255, A: 255})
	if _, err := (metadata.Writer{}).Write(ctx, inputPath, tags, &metadata.Cover{Data: imageA, MIME: "image/png"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256(mustRead(t, inputPath))
	input := fixture.repo.input.Clone()
	input.Rewrite.RelativePath = "current.flac"
	input.Rewrite.Ownership = persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])}
	input.Version, input.QualityPolicyVersion, input.Format = persistence.PublicationInputsVersion, publication.QualityPolicyVersion, publication.AudioFormatSource
	lossless := true
	input.Rewrite.AudioFacts = persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true}
	input.DesiredTags = tags
	_, input.Tools, _ = pinnedToolFixture(t, true)
	releaseID, generation := uuid.New(), uuid.New()
	input.ManualCover = &persistence.PublicationManualCoverInput{ReleaseID: releaseID, AuthorityRevision: 4, SelectedGeneration: generation}
	imageB := simpleCoverPNG(t, color.RGBA{B: 255, A: 255})
	mime := "image/png"
	coverDigest := sha256.Sum256(imageB)
	coverInput := &persistence.PublicationAttemptCoverInput{State: "value", ReleaseID: releaseID, Revision: 4, Mode: "manual", Generation: &generation, Bytes: imageB, MIME: &mime, SHA256: coverDigest[:]}
	stale := input.Clone()
	stale.DesiredTags["TITLE"] = []string{"Changed after freeze"}
	guardWriter := &manualCoverOnlyWriter{}
	_, err = (PublicationSimplePreparation{Commands: simplePreparationUnexpectedCommand{}, Metadata: guardWriter, MetadataReader: metadata.Reader{}, Validator: manualCoverObservationValidator{}, CoverInput: coverInput}).Prepare(ctx, stale, inputPath, filepath.Join(t.TempDir(), "stale-temp"), filepath.Join(t.TempDir(), "stale.flac"))
	if err == nil || guardWriter.calls != 0 {
		t.Fatalf("stale frozen tags error/calls = %v/%d, want pre-write rejection", err, guardWriter.calls)
	}
	output := filepath.Join(t.TempDir(), "candidate.flac")
	prepared, err := (PublicationSimplePreparation{Commands: simplePreparationUnexpectedCommand{}, Metadata: metadata.Writer{}, MetadataReader: metadata.Reader{}, Validator: manualCoverObservationValidator{}, CoverInput: coverInput}).Prepare(ctx, input, inputPath, filepath.Join(t.TempDir(), "temporary"), output)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := (metadata.Reader{}).Read(ctx, output, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Tags["LYRICS"][0] != lyrics || capture.Tags["TITLE"][0] != "Track" {
		t.Fatalf("tags changed: %#v", capture.Tags)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		t.Fatal(err)
	}
	if assets.Cover.State != persistence.PublicationAssetEmbedded || assets.Cover.SHA256 != hex.EncodeToString(coverDigest[:]) {
		t.Fatalf("cover = %+v", assets.Cover)
	}
	if prepared.CoverEmbedding == nil || prepared.CoverEmbedding.Outcome != "verified" || prepared.CoverEmbedding.RequestedGeneration != generation {
		t.Fatalf("cover projection = %+v", prepared.CoverEmbedding)
	}
	actual := mustRead(t, output)
	if !bytes.Equal(simpleCoverFLACFrames(t, actual), simpleCoverFLACFrames(t, mustRead(t, inputPath))) {
		t.Fatal("native rewrite changed encoded audio frames")
	}
	faultOutput := filepath.Join(t.TempDir(), "fault-candidate.flac")
	faultWriter := manualCoverFaultWriter{}
	_, err = (PublicationSimplePreparation{Commands: simplePreparationUnexpectedCommand{}, Metadata: faultWriter, MetadataReader: metadata.Reader{}, Validator: manualCoverObservationValidator{}, CoverInput: coverInput}).Prepare(ctx, input, inputPath, filepath.Join(t.TempDir(), "fault-temp"), faultOutput)
	if err != nil {
		t.Fatal(err)
	}
	faultCapture, err := (metadata.Reader{}).Read(ctx, faultOutput, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	faultAssets, err := ObservePublicationAssets(faultCapture)
	if err != nil {
		t.Fatal(err)
	}
	baselineDigest := sha256.Sum256(imageA)
	if faultAssets.Cover.State != persistence.PublicationAssetEmbedded || faultAssets.Cover.SHA256 != hex.EncodeToString(baselineDigest[:]) || faultCapture.Tags["LYRICS"][0] != lyrics {
		t.Fatalf("failed optional cover did not restore baseline artwork/tags: %+v %#v", faultAssets.Cover, faultCapture.Tags)
	}
}

type manualCoverOnlyWriter struct {
	metadata.Writer
	calls int
}

type manualCoverFaultWriter struct{ metadata.Writer }

func (writer manualCoverFaultWriter) WriteCoverOnly(ctx context.Context, path string, cover *metadata.Cover) (metadata.OptionalAssetsWriteResult, error) {
	result, err := writer.Writer.WriteCoverOnly(ctx, path, cover)
	if err == nil {
		result.CoverStatus = metadata.OptionalCoverMismatch
	}
	return result, err
}

func (writer *manualCoverOnlyWriter) WriteCoverOnly(context.Context, string, *metadata.Cover) (metadata.OptionalAssetsWriteResult, error) {
	writer.calls++
	return metadata.OptionalAssetsWriteResult{}, nil
}

type manualCoverObservationValidator struct{}

func (manualCoverObservationValidator) Validate(ctx context.Context, path string, input persistence.PublicationInputs) (PublicationStageObservation, error) {
	capture, err := (metadata.Reader{}).Read(ctx, path, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		return PublicationStageObservation{}, err
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return PublicationStageObservation{}, err
	}
	return PublicationStageObservation{
		Audio:    json.RawMessage(`{"version":1,"facts":{"stream_index":0,"codec":"flac","lossless":true,"container":"flac","supported_source_container":true}}`),
		Metadata: manualCoverJSON(persistence.PublicationCommitMetadata{Version: 1, Tags: capture.Tags}), Assets: manualCoverJSON(assets), QualityPolicyVersion: publication.QualityPolicyVersion,
		SHA256: func() string { b, _ := os.ReadFile(path); sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }(),
	}, nil
}

func manualCoverJSON(value any) json.RawMessage { data, _ := json.Marshal(value); return data }

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
