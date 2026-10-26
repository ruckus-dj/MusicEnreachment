//go:build integration

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestFirstAudioM4AAttachedCoverPassesRealProbeAndReadback(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is unavailable")
	}
	ctx := context.Background()
	stage := filepath.Join(t.TempDir(), "candidate.m4a")
	fixture := filepath.Join("..", "integrations", "metadata", "testdata", "eg.m4a")
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, data, 0o600); err != nil {
		t.Fatal(err)
	}

	reader := metadata.Reader{}
	initial, err := reader.Read(ctx, stage, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatalf("read real M4A fixture: %v", err)
	}
	cover := simpleCoverPNG(t, color.RGBA{G: 255, A: 255})
	if _, err := (metadata.Writer{}).Write(ctx, stage, initial.Tags, &metadata.Cover{Data: cover, MIME: "image/png"}); err != nil {
		t.Fatalf("write cover with TagLib: %v", err)
	}
	capture, err := reader.Read(ctx, stage, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatalf("read back real M4A cover: %v", err)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		t.Fatalf("observe actual publication assets: %v", err)
	}
	if capture.CoverInspection.Cover == nil || assets.Cover.State != persistence.PublicationAssetEmbedded || assets.Cover.MIME != "image/png" || assets.Cover.SizeBytes == nil || *assets.Cover.SizeBytes != int64(len(capture.CoverInspection.Cover.Data)) {
		t.Fatalf("read-back cover/assets = %#v / %#v", capture.CoverInspection, assets.Cover)
	}
	digest := sha256.Sum256(capture.CoverInspection.Cover.Data)
	if assets.Cover.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("observed cover hash = %q, want hash of actual read-back bytes", assets.Cover.SHA256)
	}

	output, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_streams", "-show_chapters", "-of", "json", stage).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe real M4A candidate: %v: %s", err, output)
	}
	var topology struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Chapters []json.RawMessage `json:"chapters"`
	}
	if err := json.Unmarshal(output, &topology); err != nil {
		t.Fatalf("decode ffprobe output: %v", err)
	}
	streamTypes := map[string]int{}
	audioCodec := ""
	for _, stream := range topology.Streams {
		streamTypes[stream.CodecType]++
		if stream.CodecType == "audio" {
			audioCodec = stream.CodecName
		}
	}
	if len(topology.Streams) != 2 || streamTypes["audio"] != 1 || streamTypes["video"] != 1 || audioCodec != "aac" {
		t.Fatalf("real M4A stream topology = %#v, want audio plus attached picture", topology.Streams)
	}
	if err := validateFirstAudioCoverTopology(output, publication.AudioContainerM4A, assets.Cover); err != nil {
		t.Fatalf("validate real M4A attached cover: %v", err)
	}
}
