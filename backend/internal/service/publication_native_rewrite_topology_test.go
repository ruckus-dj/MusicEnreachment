package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestRealValidatorAcceptsNativeRewriteArtworkButRejectsOtherStreams(t *testing.T) {
	fixture := makeRewriteFixture(t)
	input := fixture.input
	lossless := true
	input.Rewrite.AudioFacts = persistence.PersistedAudioFacts{
		StreamIndex: 0, Codec: "flac", Lossless: &lossless,
		Container: publication.AudioContainerFLAC, SupportedSourceContainer: true,
	}
	input.ManualLyrics = &persistence.PublicationManualLyricsInput{
		ExpectedMetadataRevision: 1, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration,
		State: "explicit_empty",
	}
	input.DesiredTags = map[string][]string{"TITLE": {"Native artwork rewrite"}}
	if err := input.Validate(); err != nil {
		t.Fatalf("manual source rewrite input invalid: %v", err)
	}

	stage := filepath.Join(t.TempDir(), "stage.flac")
	contents, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (metadata.Writer{}).Write(context.Background(), stage, input.DesiredTags, nil); err != nil {
		t.Fatal(err)
	}
	reader := metadata.Reader{}
	capture, err := reader.Read(context.Background(), stage, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil || assets.Cover.State != persistence.PublicationAssetEmbedded {
		t.Fatalf("native fixture must contain read-back artwork: assets=%+v err=%v", assets.Cover, err)
	}
	codec := "mjpeg"
	if assets.Cover.MIME == "image/png" {
		codec = "png"
	} else if assets.Cover.MIME != "image/jpeg" {
		t.Fatalf("unsupported fixture cover MIME %q", assets.Cover.MIME)
	}
	probeJSON := func(extra string) []byte {
		return []byte(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"sample_rate":"44100"},{"index":1,"codec_type":"video","codec_name":"` + codec + `","disposition":{"attached_pic":1}}` + extra + `]}`)
	}
	validator := RealPublicationStageValidator{
		Tools: publicationValidatorTools{}, MetadataReader: reader,
		Probe: func(context.Context, string, sourcefs.RegularFile, publication.AudioContainer) ([]byte, error) {
			return probeJSON(""), nil
		},
	}
	// The simple manual-lyrics validator delegates audio/tag validation after
	// separating manual authority from generic execution support.
	validationInput := input.Clone()
	validationInput.ManualLyrics = nil
	if _, err := validator.Validate(context.Background(), stage, validationInput); err != nil {
		t.Fatalf("real validator rejected native attached artwork: %v", err)
	}
	validator.Probe = func(context.Context, string, sourcefs.RegularFile, publication.AudioContainer) ([]byte, error) {
		return probeJSON(`,{"index":2,"codec_type":"subtitle","codec_name":"srt"}`), nil
	}
	if _, err := validator.Validate(context.Background(), stage, validationInput); err == nil {
		t.Fatal("real validator accepted an unexpected subtitle stream")
	}
}

func TestValidateNativeRewriteTopologyAllowsOnlyReadBackAttachedPicture(t *testing.T) {
	image := []byte("read-back JPEG cover")
	digest := sha256.Sum256(image)
	size := int64(len(image))
	cover := persistence.PublicationCoverOutcome{
		State: persistence.PublicationAssetEmbedded, SHA256: hex.EncodeToString(digest[:]),
		MIME: "image/jpeg", SizeBytes: &size,
	}
	valid := `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}]}`
	if err := validateNativeRewriteTopology([]byte(valid), cover); err != nil {
		t.Fatalf("native attached picture rejected: %v", err)
	}

	for _, raw := range []string{
		`{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"mjpeg"}]}`,
		`{"streams":[{"codec_type":"audio"},{"codec_type":"subtitle","disposition":{"attached_pic":1}}]}`,
		`{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}},{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}]}`,
		`{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}],"chapters":[{}]}`,
	} {
		if err := validateNativeRewriteTopology([]byte(raw), cover); err == nil {
			t.Errorf("unauthorized native rewrite topology accepted: %s", raw)
		}
	}
	if err := validateNativeRewriteTopology([]byte(valid), persistence.PublicationCoverOutcome{State: persistence.PublicationAssetAbsent}); err == nil {
		t.Fatal("attached picture without read-back cover was accepted")
	}
}

func TestValidatePublicationTopologyRemainsStrictForGenericAudio(t *testing.T) {
	raw := []byte(`{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}]}`)
	if err := validatePublicationTopology(raw); err == nil {
		t.Fatal("generic publication topology accepted an attached picture")
	}
}
