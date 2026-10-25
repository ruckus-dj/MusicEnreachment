package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"go.senan.xyz/taglib"
)

type providerTagHashCommand struct {
	result PublicationCommandResult
	err    error
	args   []string
	path   string
}

func TestPrepareProviderTagsPreservesCurrentAssetsAndAcceptsInstalledRetry(t *testing.T) {
	ctx := context.Background()
	current := filepath.Join(t.TempDir(), "current.flac")
	data, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	data, err = flacFixtureWithoutPictures(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, data, 0o600); err != nil {
		t.Fatal(err)
	}
	tags := map[string][]string{"TITLE": {"CURRENT title"}, "ARTIST": {"Protected artist"}, "LYRICS": {"[00:00.00]Unchanged CURRENT lyrics\n"}}
	if _, err := (metadata.Writer{}).Write(ctx, current, tags, nil); err != nil {
		t.Fatal(err)
	}
	for _, image := range []metadata.ArtworkSnapshotImage{
		{Index: 0, Type: "Front Cover", Description: "front", MIME: "image/jpeg", Data: audioUpgradeImage(t, "image/jpeg", color.RGBA{R: 220, A: 255})},
		{Index: 1, Type: "Back Cover", Description: "back", MIME: "image/png", Data: audioUpgradeImage(t, "image/png", color.RGBA{B: 220, A: 255})},
	} {
		if err := taglib.WriteImageOptions(current, image.Data, image.Index, image.Type, image.Description, image.MIME); err != nil {
			t.Fatal(err)
		}
	}
	capture, err := (metadata.Reader{}).Read(ctx, current, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		t.Fatal(err)
	}
	artwork, err := metadata.ReadArtworkSnapshot(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(current)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256(original)
	lossless, channels, depth, rate := true, 2, 16, 44100
	facts := persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels,
		ChannelLayout: "stereo", BitDepth: &depth, SampleRate: &rate, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true}
	witness := persistence.PublicationCurrentWitness{Generation: uuid.New(), RelativePath: "current.flac",
		Ownership:  persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])},
		AudioFacts: facts, Assets: assets, Tags: capture.Tags, ArtworkInventory: audioUpgradeArtworkWitnesses(artwork.ArtworkInventory())}
	patch := map[string][]string{"TITLE": {"Provider title"}, "ARTIST": {"Must not overwrite protected CURRENT artist"}}
	desired, err := publication.ApplyProviderTagPatch(capture.Tags, patch, []string{"ARTIST"})
	if err != nil {
		t.Fatal(err)
	}
	_, tools, _ := pinnedToolFixture(t, true)
	const audioHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	input := persistence.PublicationInputs{Version: persistence.PublicationProviderTagInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "rewrite", Format: publication.AudioFormatSource, DesiredTags: desired, Tools: tools,
		Rewrite: &persistence.PublicationRewriteInput{CurrentGeneration: witness.Generation, RelativePath: witness.RelativePath, Ownership: witness.Ownership, AudioFacts: facts},
		ProviderTags: &persistence.PublicationProviderTagInput{Current: witness, AudioPayloadSHA256: audioHash, ProviderSourceID: uuid.New(),
			Endpoint: "https://musicbrainz.org/ws/2", ConfigurationIdentity: "test-configuration", AuthorityRevision: strings.Repeat("a", 64), Patch: patch, ProtectedKeys: []string{"ARTIST"}},
	}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	// The hash command is a boundary stub; metadata and all-image restoration are
	// native. Actual encoded-audio identity needs the real-media acceptance run.
	runner := &providerTagHashCommand{result: PublicationCommandResult{Output: []byte("SHA256=" + audioHash + "\n"), QuiescenceEstablished: true}}
	preparation := PublicationSimplePreparation{Commands: runner, FFmpegPath: "ffmpeg", Metadata: metadata.Writer{}, MetadataReader: metadata.Reader{},
		Validator: RealPublicationStageValidator{Tools: publicationValidatorTools{}, MetadataReader: metadata.Reader{},
			Probe: func(context.Context, string, sourcefs.RegularFile, publication.AudioContainer) ([]byte, error) {
				return []byte(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"channel_layout":"stereo","bits_per_raw_sample":"16","sample_rate":"44100"},{"index":1,"codec_type":"video","disposition":{"attached_pic":1},"tags":{"mimetype":"image/jpeg"}},{"index":2,"codec_type":"video","disposition":{"attached_pic":1},"tags":{"mimetype":"image/png"}}]}`), nil
			}},
	}
	prepare := func(input persistence.PublicationInputs, current string) (PublicationSimplePreparationResult, string, error) {
		output := filepath.Join(t.TempDir(), "candidate.flac")
		result, err := preparation.PrepareProviderTags(ctx, input, current, filepath.Join(t.TempDir(), "private"), output)
		return result, output, err
	}
	result, output, err := prepare(input, current)
	if err != nil {
		t.Fatalf("prepare admitted provider patch: %v", err)
	}
	if result.PreservedAudioSHA256 != audioHash || result.InitialLyrics != nil || result.CoverEmbedding != nil || result.LyricsOutcome != nil || result.LyricsResolution != nil {
		t.Fatalf("provider patch preparation evidence = %+v", result)
	}
	var actualMetadata persistence.PublicationCommitMetadata
	if err := json.Unmarshal(result.Observation.ActualMetadata, &actualMetadata); err != nil || !reflect.DeepEqual(actualMetadata.Tags, desired) {
		t.Fatalf("provider patch readback: %+v, %v", actualMetadata, err)
	}
	if finalArtwork, err := metadata.ReadArtworkSnapshot(ctx, output); err != nil || !reflect.DeepEqual(finalArtwork, artwork) {
		t.Fatalf("provider patch changed artwork: %+v, %v", finalArtwork.ArtworkInventory(), err)
	}
	if _, _, err := prepare(input, output); err != nil {
		t.Fatalf("repeat preparation with already installed provider tags: %v", err)
	}
	if unchanged, err := os.ReadFile(current); err != nil || !reflect.DeepEqual(unchanged, original) {
		t.Fatalf("provider preparation changed CURRENT input: %v", err)
	}
	changed := input.Clone()
	changed.ProviderTags.Current.ArtworkInventory[1].Description = "unauthorized change"
	if _, _, err := prepare(changed, current); err == nil {
		t.Fatal("provider preparation accepted artwork drift")
	}
	changed = input.Clone()
	changed.ProviderTags.AudioPayloadSHA256 = strings.Repeat("b", 64)
	if _, _, err := prepare(changed, current); err == nil {
		t.Fatal("provider preparation accepted changed encoded audio")
	}
	preparation.CoverInput = &persistence.PublicationAttemptCoverInput{}
	if _, _, err := prepare(input, current); err == nil {
		t.Fatal("provider preparation accepted pending cover authority")
	}
}

func (runner *providerTagHashCommand) Run(_ context.Context, path string, args ...string) (PublicationCommandResult, error) {
	runner.path = path
	runner.args = append([]string(nil), args...)
	return runner.result, runner.err
}

func TestPublicationEncodedAudioSHA256CommandAndDigest(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	runner := &providerTagHashCommand{result: PublicationCommandResult{Output: []byte("SHA256=" + digest + "\n"), QuiescenceEstablished: true}}
	input := filepath.Join(t.TempDir(), "current.m4a")
	got, err := publicationEncodedAudioSHA256(context.Background(), runner, "ffmpeg", input, publication.AudioContainerM4A)
	if err != nil {
		t.Fatal(err)
	}
	if got != digest || runner.path != "ffmpeg" {
		t.Fatalf("digest/path = %q/%q", got, runner.path)
	}
	joined := strings.Join(runner.args, " ")
	for _, required := range []string{"-map 0:a:0", "-c:a copy", "-f hash", "-hash sha256"} {
		if !strings.Contains(joined, required) {
			t.Errorf("hash command missing %q: %s", required, joined)
		}
	}
	if !containsCommandArgPair(runner.args, "-i", input) {
		t.Fatal("hash command did not retain the absolute input path as one argument")
	}
	if strings.Contains(joined, "-c:a copy ") == false {
		t.Fatalf("encoded stream must be copied, not transformed: %s", joined)
	}
}

func TestPublicationEncodedAudioSHA256RejectsInvalidCommandResults(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	commandErr := errors.New("command failed")
	tests := []struct {
		name   string
		result PublicationCommandResult
		err    error
	}{
		{name: "runner error", result: PublicationCommandResult{QuiescenceEstablished: true}, err: commandErr},
		{name: "unknown quiescence", result: PublicationCommandResult{Output: []byte("SHA256=" + digest)}},
		{name: "missing prefix", result: PublicationCommandResult{Output: []byte(digest), QuiescenceEstablished: true}},
		{name: "short digest", result: PublicationCommandResult{Output: []byte("SHA256=abcd"), QuiescenceEstablished: true}},
		{name: "uppercase digest", result: PublicationCommandResult{Output: []byte("SHA256=" + strings.ToUpper(digest)), QuiescenceEstablished: true}},
		{name: "non-hex digest", result: PublicationCommandResult{Output: []byte("SHA256=" + strings.Repeat("z", 64)), QuiescenceEstablished: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &providerTagHashCommand{result: test.result, err: test.err}
			got, err := publicationEncodedAudioSHA256(context.Background(), runner, "ffmpeg", filepath.Join(t.TempDir(), "current.flac"), publication.AudioContainerFLAC)
			if err == nil || got != "" {
				t.Fatalf("got digest %q, error %v; want failure", got, err)
			}
		})
	}
}

func TestPublicationEncodedAudioSHA256RejectsIncompleteDependencies(t *testing.T) {
	runner := &providerTagHashCommand{result: PublicationCommandResult{QuiescenceEstablished: true}}
	for _, test := range []struct {
		name, tool, path string
		runner           PublicationCommandRunner
		container        publication.AudioContainer
	}{
		{name: "nil runner", tool: "/tools/ffmpeg", path: "/private/audio.flac", container: publication.AudioContainerFLAC},
		{name: "empty tool", tool: "", path: "/private/audio.flac", runner: runner, container: publication.AudioContainerFLAC},
		{name: "relative input", tool: "/tools/ffmpeg", path: "audio.flac", runner: runner, container: publication.AudioContainerFLAC},
		{name: "unknown container", tool: "/tools/ffmpeg", path: "/private/audio", runner: runner, container: publication.AudioContainer("unknown")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := publicationEncodedAudioSHA256(context.Background(), test.runner, test.tool, test.path, test.container); err == nil {
				t.Fatal("expected incomplete dependency error")
			}
		})
	}
}
