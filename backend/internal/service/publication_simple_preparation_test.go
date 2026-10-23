package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestPublicationSimplePreparationManualLyricsNativeFLAC(t *testing.T) {
	ctx := context.Background()
	fixture := makeRewriteFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	if err := os.Remove(filepath.Join(fixture.output, "current.mp3")); err != nil {
		t.Fatal(err)
	}
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
	if _, err := (metadata.Writer{}).Write(ctx, inputPath, map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}, nil); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256(original)
	input := fixture.repo.input.Clone()
	input.Rewrite.RelativePath = "current.flac"
	input.Rewrite.Ownership = persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])}
	input.Version = persistence.PublicationInputsVersion
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	input.Format = publication.AudioFormatSource
	lossless := true
	input.Rewrite.AudioFacts = persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true}
	lyrics := "[ar:Artist]\r\n[00:01.00]Words\r\n"
	input.ManualLyrics = &persistence.PublicationManualLyricsInput{State: "value", Value: lyrics, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration}
	input.DesiredTags = map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}
	_, input.Tools, _ = pinnedToolFixture(t, true)
	outputPath := filepath.Join(t.TempDir(), "candidate.flac")
	validator := RealPublicationStageValidator{
		Tools: publicationValidatorTools{}, MetadataReader: metadata.Reader{},
		Probe: func(context.Context, string, sourcefs.RegularFile, publication.AudioContainer) ([]byte, error) {
			return []byte(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"sample_rate":"44100"}]}`), nil
		},
	}
	prepared, err := (PublicationSimplePreparation{
		Commands: simplePreparationUnexpectedCommand{}, Metadata: metadata.Writer{}, Validator: simpleManualLyricsValidator{validator: validator},
	}).Prepare(ctx, input, inputPath, filepath.Join(t.TempDir(), "temporary"), outputPath)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := (metadata.Reader{}).Read(ctx, outputPath, metadata.ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := actual.Tags["LYRICS"]; len(got) != 1 || got[0] != lyrics {
		t.Fatalf("actual LYRICS tag = %#v, want %#v", got, []string{lyrics})
	}
	unchanged, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unchanged, original) {
		t.Fatal("Prepare changed the original input bytes")
	}
	if prepared.LyricsOutcome == nil || prepared.LyricsOutcome.State != persistence.PublicationAssetEmbedded || prepared.Observation.Version != 1 {
		t.Fatalf("manual lyrics outcome/observation = %#v / %#v", prepared.LyricsOutcome, prepared.Observation)
	}
}

type simplePreparationUnexpectedCommand struct{}

func (simplePreparationUnexpectedCommand) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("unexpected command in native publication preparation")
}

type simplePreparationSettings struct{}

func (simplePreparationSettings) GetLRCLIBEnabled(context.Context) (bool, error) { return true, nil }

type simplePreparationLookup struct {
	duration float64
	found    bool
	lyrics   string
	calls    int
}

func (lookup *simplePreparationLookup) Lookup(_ context.Context, request lrclib.Metadata) (lrclib.LookupResult, error) {
	lookup.calls++
	lookup.duration = request.Duration
	return lrclib.LookupResult{Found: lookup.found, Lyrics: lookup.lyrics}, nil
}

type simplePreparationProbeCommand struct {
	calls int
}

func (command *simplePreparationProbeCommand) Run(_ context.Context, executable string, _ ...string) (PublicationCommandResult, error) {
	if executable != "ffprobe" {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("unexpected command in native initial publication preparation")
	}
	command.calls++
	return PublicationCommandResult{Output: []byte(`{"format":{"duration":"183.375"}}`), QuiescenceEstablished: true}, nil
}

func TestSimplePreparationInitialLyricsUsesPreparedAudioAndFrozenAuthority(t *testing.T) {
	ctx := context.Background()
	lyrics := "[00:01.00]provider lyrics\n"
	for _, tc := range []struct {
		name        string
		desired     *librarymetadata.FieldValue
		wantLyrics  bool
		wantLookups int
	}{
		{name: "automatic authority", wantLyrics: true, wantLookups: 1},
		{name: "manual authority suppresses automatic", desired: func() *librarymetadata.FieldValue {
			value := librarymetadata.ManualFieldValue(librarymetadata.StateValue, "[00:02.00]manual authority", true)
			return &value
		}()},
		{name: "explicit empty suppresses automatic", desired: &librarymetadata.FieldValue{State: librarymetadata.StateExplicitEmpty, Authority: librarymetadata.AuthorityManual, Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPublicationStageFixture(t)
			inputPath := fixture.source
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
			if _, err := (metadata.Writer{}).Write(ctx, inputPath, map[string][]string{"TITLE": {"Current Title"}, "ARTIST": {"Current Artist"}}, nil); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			_, tools, _ := pinnedToolFixture(t, true)
			lossless := true
			input := persistence.PublicationInputs{
				Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
				IntentKind: "audio", InitialLyrics: &persistence.PublicationInitialLyricsInput{ExpectedMetadataRevision: 7, Desired: tc.desired},
				Format: publication.AudioFormatSource, DesiredTags: map[string][]string{"TITLE": {"Current Title"}, "ARTIST": {"Current Artist"}}, Tools: tools,
				Audio: &persistence.PublicationAudioInput{
					WorkID: uuid.New(), RootID: uuid.New(), LocationID: uuid.New(), MediaVariantID: uuid.New(), ProbeResultID: uuid.New(),
					ConfiguredRootPath: filepath.Dir(filepath.Dir(inputPath)), InventoryRootPath: filepath.Dir(filepath.Dir(inputPath)),
					RelativePath: "album/track.flac", SizeBytes: info.Size(), MTime: info.ModTime().UTC().Truncate(time.Microsecond),
					AnalysisPolicyVersion: 1, FFProbeVersion: "test", AudioFacts: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true},
				},
			}
			lookup := &simplePreparationLookup{found: true, lyrics: lyrics}
			command := &simplePreparationProbeCommand{}
			validator := RealPublicationStageValidator{
				Tools: publicationValidatorTools{}, MetadataReader: metadata.Reader{},
				Probe: func(context.Context, string, sourcefs.RegularFile, publication.AudioContainer) ([]byte, error) {
					return []byte(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"sample_rate":"44100"}]}`), nil
				},
			}
			output := filepath.Join(t.TempDir(), "candidate.flac")
			prepared, err := (PublicationSimplePreparation{
				Commands: command, FFprobePath: "ffprobe", Validator: validator, Metadata: metadata.Writer{}, MetadataReader: metadata.Reader{},
				Lyrics: NewPublicationLyricsResolver(simplePreparationSettings{}, lookup),
			}).Prepare(ctx, input, inputPath, filepath.Join(t.TempDir(), "temporary"), output)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := (metadata.Reader{}).Read(ctx, output, metadata.ReadRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantLyrics {
				if got := actual.Tags["LYRICS"]; len(got) != 1 || got[0] != lyrics {
					t.Fatalf("actual candidate LYRICS=%#v, want %q", got, lyrics)
				}
				if prepared.LyricsOutcome == nil || prepared.LyricsOutcome.State != persistence.PublicationAssetEmbedded || prepared.InitialLyrics == nil || prepared.InitialLyrics.Outcome != persistence.PublicationOptionalLyricsExactRoundTrip || prepared.InitialLyrics.Source != "lrclib" || prepared.InitialLyrics.SHA256 == "" {
					t.Fatalf("initial lyrics projection/outcome = %#v / %#v", prepared.InitialLyrics, prepared.LyricsOutcome)
				}
			} else if _, exists := actual.Tags["LYRICS"]; exists {
				t.Fatalf("protected frozen authority unexpectedly acquired lyrics: %#v", actual.Tags["LYRICS"])
			} else if prepared.InitialLyrics == nil || prepared.InitialLyrics.Outcome != persistence.PublicationInitialLyricsNotRequested || prepared.InitialLyrics.Source != "none" {
				t.Fatalf("protected initial lyrics projection = %#v", prepared.InitialLyrics)
			}
			if lookup.calls != tc.wantLookups || command.calls != 1 || (tc.wantLookups > 0 && lookup.duration != 183.375) {
				t.Fatalf("resolver calls=%d duration=%v probe calls=%d, want %d, 183.375, 1", lookup.calls, lookup.duration, command.calls, tc.wantLookups)
			}
		})
	}
}

func TestCopyCurrentPublicationFailureLeavesInputUnchanged(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "current.mka")
	want := []byte("current publication bytes")
	if err := os.WriteFile(input, want, 0o600); err != nil {
		t.Fatal(err)
	}
	// A destination directory forces candidate-input creation to fail before
	// any command runs, while demonstrating that preparation never mutates the
	// resolved current publication.
	destination := filepath.Join(root, "temporary-input")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyCurrentPublication(input, destination); err == nil {
		t.Fatal("expected temporary input creation failure")
	}
	got, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("current publication changed: got %q, want %q", got, want)
	}
}

func TestSimplePreparationAutomaticLyricsUsesExactCurrentDurationAndExcludesCurrentLyrics(t *testing.T) {
	lookup := &simplePreparationLookup{found: false}
	resolver := NewPublicationLyricsResolver(simplePreparationSettings{}, lookup)
	current := metadata.Capture{Tags: map[string][]string{
		"ARTIST": {"Current Artist"}, "TITLE": {"Current Title"}, "LYRICS": {"stale current lyrics"},
	}}
	tags, request := automaticLyricsRetryLookupInputs(current, 183.375)
	if _, exists := tags["LYRICS"]; exists {
		t.Fatal("current lyrics were passed to automatic lookup")
	}
	resolved, err := resolver.Resolve(context.Background(), PublicationLyricsResolutionRequest{SelectedSourceTags: tags, Metadata: request})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != "none" || lookup.duration != 183.375 {
		t.Fatalf("resolution=%+v duration=%.3f, want no result at exact duration 183.375", resolved, lookup.duration)
	}
}
