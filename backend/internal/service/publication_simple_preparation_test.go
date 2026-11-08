package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image/color"
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
	"go.senan.xyz/taglib"
)

func TestPublicationSimplePreparationManualLyricsNativeFLAC(t *testing.T) {
	ctx := context.Background()
	fixture := makeRewriteFixture(t)
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
	input := fixture.input.Clone()
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
	request  lrclib.Metadata
	duration float64
	found    bool
	lyrics   string
	calls    int
}

func (lookup *simplePreparationLookup) Lookup(_ context.Context, request lrclib.Metadata) (lrclib.LookupResult, error) {
	lookup.calls++
	lookup.request = request
	lookup.duration = request.Duration
	return lrclib.LookupResult{Found: lookup.found, Lyrics: lookup.lyrics}, nil
}

type simplePreparationProbeCommand struct {
	calls int
	args  []string
}

func (command *simplePreparationProbeCommand) Run(_ context.Context, executable string, args ...string) (PublicationCommandResult, error) {
	if executable != "ffmpeg" {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("unexpected command in initial publication preparation")
	}
	command.calls++
	command.args = append([]string(nil), args...)
	input, output := "", args[len(args)-1]
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "-i" {
			input = args[index+1]
			break
		}
	}
	if input == "" {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("ffmpeg input was not specified")
	}
	contents, err := os.ReadFile(input)
	if err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	if err := os.WriteFile(output, contents, 0o600); err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	return PublicationCommandResult{QuiescenceEstablished: true}, nil
}

func TestSimplePreparationInitialLyricsUsesFrozenAnalysisAndAuthority(t *testing.T) {
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
			fixture := newSimplePreparationFixture(t)
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
			frozenDuration := 183.456789
			input := persistence.PublicationInputs{
				Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
				IntentKind: "audio", InitialLyrics: &persistence.PublicationInitialLyricsInput{ExpectedMetadataRevision: 7, Desired: tc.desired},
				Format: publication.AudioFormatSource, DesiredTags: map[string][]string{"TITLE": {"Current Title"}, "ARTIST": {"Current Artist"}}, Tools: tools,
				Audio: &persistence.PublicationAudioInput{
					WorkID: uuid.New(), RootID: uuid.New(), LocationID: uuid.New(), MediaVariantID: uuid.New(), ProbeResultID: uuid.New(),
					ConfiguredRootPath: filepath.Dir(filepath.Dir(inputPath)), InventoryRootPath: filepath.Dir(filepath.Dir(inputPath)),
					RelativePath: "album/track.flac", SizeBytes: info.Size(), MTime: info.ModTime().UTC().Truncate(time.Microsecond),
					AnalysisPolicyVersion: 1, FFProbeVersion: "test", AudioFacts: persistence.PersistedAudioFacts{StreamIndex: 2, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true},
					AnalysedMetadata: &persistence.PublicationAnalysedMetadata{Tags: map[string][]string{"ARTIST": {"Frozen Artist"}, "TITLE": {"Frozen Title"}, "ALBUM": {"Frozen Album"}}, DurationSeconds: &frozenDuration, MetadataResultID: uuid.New(), WinningResultID: uuid.New()},
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
				Commands: command, FFmpegPath: "ffmpeg", Validator: validator, Metadata: metadata.Writer{}, MetadataReader: simpleOutputOnlyMetadataReader{allowed: output},
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
			if lookup.calls != tc.wantLookups || command.calls != 1 || (tc.wantLookups > 0 && lookup.duration != frozenDuration) {
				t.Fatalf("resolver calls=%d duration=%v transform calls=%d, want %d, %v, 1", lookup.calls, lookup.duration, command.calls, tc.wantLookups, frozenDuration)
			}
			if !containsCommandArgPair(command.args, "-map", "0:2") || !containsCommandArgPair(command.args, "-c:a", "copy") || !containsCommandArgPair(command.args, "-map_metadata", "-1") || !containsCommandArgPair(command.args, "-map_chapters", "-1") {
				t.Fatalf("source-format first audio did not use selected-stream audio copy: %v", command.args)
			}
			if tc.wantLookups > 0 && (lookup.request.Artist != "Frozen Artist" || lookup.request.Title != "Frozen Title" || lookup.request.Album != "Frozen Album") {
				t.Fatalf("lookup did not use frozen analysed tags: %+v", lookup.request)
			}
		})
	}
}

func containsCommandArgPair(args []string, key, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key && args[index+1] == value {
			return true
		}
	}
	return false
}

type simpleOutputOnlyMetadataReader struct{ allowed string }

func (reader simpleOutputOnlyMetadataReader) Read(ctx context.Context, path string, request metadata.ReadRequest) (metadata.Capture, error) {
	if filepath.Clean(path) != filepath.Clean(reader.allowed) {
		return metadata.Capture{}, errors.New("publication attempted late source metadata extraction")
	}
	return (metadata.Reader{}).Read(ctx, path, request)
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

type sourceRepublishOptionalWriter struct {
	metadata.Writer
	lyricsFallback bool
	coverFailure   bool
	calls          int
}

func (writer *sourceRepublishOptionalWriter) WriteWithOptionalAssets(ctx context.Context, path string, tags map[string][]string, cover *metadata.Cover) (metadata.OptionalAssetsWriteResult, error) {
	writer.calls++
	if writer.coverFailure {
		result, err := (metadata.Writer{}).WriteWithOptionalAssets(ctx, path, tags, nil)
		if err != nil {
			return result, err
		}
		result.CoverStatus = metadata.OptionalCoverWriteFailed
		return result, nil
	}
	result, err := (metadata.Writer{}).WriteWithOptionalAssets(ctx, path, tags, cover)
	if err == nil && writer.lyricsFallback && writer.calls == 1 {
		result.LyricsStatus = metadata.OptionalLyricsAbsent
	}
	return result, err
}

func TestPrepareSourceRepublishBuildsFromSelectedSourceAndHandlesOptionalAssets(t *testing.T) {
	ctx := context.Background()
	fixture := makeRewriteFixture(t)
	currentPath := filepath.Join(fixture.output, "current.flac")
	sourcePath := filepath.Join(t.TempDir(), "selected-source.flac")
	contents, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	contents, err = flacFixtureWithoutPictures(contents)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{currentPath, sourcePath} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	currentTags := map[string][]string{"TITLE": {"CURRENT title"}, "ARTIST": {"CURRENT artist"}, "LYRICS": {"[00:02.00]stale current lyrics"}}
	if _, err := (metadata.Writer{}).Write(ctx, currentPath, currentTags, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := (metadata.Writer{}).Write(ctx, sourcePath, map[string][]string{"TITLE": {"source title must not win"}, "ARTIST": {"source artist must not win"}}, nil); err != nil {
		t.Fatal(err)
	}
	currentOriginalCover := audioUpgradeImage(t, "image/jpeg", color.RGBA{R: 220, A: 255})
	if err := taglib.WriteImageOptions(currentPath, currentOriginalCover, 0, "Front Cover", "current", "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	currentCapture, err := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false})
	if err != nil {
		t.Fatal(err)
	}
	currentAssets, err := ObservePublicationAssets(currentCapture)
	if err != nil {
		t.Fatal(err)
	}
	currentArtwork, err := metadata.ReadArtworkSnapshot(ctx, currentPath)
	if err != nil {
		t.Fatal(err)
	}
	currentInfo, err := os.Stat(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	currentBytes, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	currentHash := sha256.Sum256(currentBytes)
	currentSize, currentMTime := currentInfo.Size(), currentInfo.ModTime().UnixMicro()
	currentLossless, currentChannels, currentDepth, currentRate := true, 2, 16, 44100
	currentFacts := persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &currentLossless, Channels: &currentChannels, ChannelLayout: "stereo", BitDepth: &currentDepth, SampleRate: &currentRate, Container: "flac", SupportedSourceContainer: true}
	currentWitness := persistence.PublicationCurrentWitness{
		Generation: uuid.New(), RelativePath: "Artist/Album/current.flac",
		Ownership:  persistence.PublicationFileIdentity{SizeBytes: &currentSize, MTime: &currentMTime, SHA256: hex.EncodeToString(currentHash[:])},
		AudioFacts: currentFacts, Assets: currentAssets, Tags: currentCapture.Tags,
		ArtworkInventory: audioUpgradeArtworkWitnesses(currentArtwork.ArtworkInventory()),
	}

	lyrics := "[ar:Selected Artist]\r\n[00:01.00]selected source lyrics\r\n"
	lyricsHash := sha256.Sum256([]byte(lyrics))
	derivative := audioUpgradeImage(t, "image/png", color.RGBA{B: 230, A: 255})
	derivativeHash := sha256.Sum256(derivative)
	originalHash := sha256.Sum256(currentOriginalCover)
	originalCoverSize := int64(len(currentOriginalCover))
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	sourceLossless, sourceChannels, sourceDepth, sourceRate := true, 2, 16, 44100
	_, tools, _ := pinnedToolFixture(t, true)
	metadataResultID, winningResultID := uuid.New(), uuid.New()
	duration := 183.456789
	input := persistence.PublicationInputs{
		Version: persistence.PublicationSourceRepublishInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "audio", Format: publication.AudioFormatSource,
		DesiredTags: map[string][]string{"TITLE": {"Frozen desired title"}, "ARTIST": {"Frozen desired artist"}, "ALBUM": {"Frozen desired album"}}, Tools: tools,
		Audio: &persistence.PublicationAudioInput{
			WorkID: uuid.New(), RootID: uuid.New(), LocationID: uuid.New(), MediaVariantID: uuid.New(), ProbeResultID: uuid.New(),
			ConfiguredRootPath: filepath.Dir(sourcePath), InventoryRootPath: filepath.Dir(sourcePath), RelativePath: filepath.Base(sourcePath),
			SizeBytes: sourceInfo.Size(), MTime: sourceInfo.ModTime().UTC().Truncate(time.Microsecond), AnalysisPolicyVersion: 1, FFProbeVersion: "test",
			AudioFacts:       persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &sourceLossless, Channels: &sourceChannels, ChannelLayout: "stereo", BitDepth: &sourceDepth, SampleRate: &sourceRate, Container: "flac", SupportedSourceContainer: true},
			AnalysedMetadata: &persistence.PublicationAnalysedMetadata{Tags: map[string][]string{"ARTIST": {"Selected Artist"}, "TITLE": {"Selected Title"}, "ALBUM": {"Selected Album"}, "LYRICS": {lyrics}}, DurationSeconds: &duration, MetadataResultID: metadataResultID, WinningResultID: winningResultID},
		},
		SourceRepublish: &persistence.PublicationSourceRepublishInput{
			Current: currentWitness, Mode: "ordinary", Association: persistence.PublicationRepublishAssociation{ID: uuid.New(), LocationID: uuid.New(), MediaVariantID: uuid.New()},
			Lyrics:          persistence.PublicationRepublishLyrics{State: "value", Origin: "source", Text: lyrics, SHA256: hex.EncodeToString(lyricsHash[:])},
			LyricsAuthority: persistence.PublicationInitialLyricsInput{ExpectedMetadataRevision: 3},
			Cover:           persistence.PublicationRepublishCover{ReleaseID: uuid.New(), AuthorityRevision: 4, AuthorityMode: "manual", SelectedGeneration: uuid.New(), OriginalSHA256: hex.EncodeToString(originalHash[:]), OriginalMIME: "image/jpeg", OriginalSizeBytes: &originalCoverSize, Derivation: "ready", PNG: derivative, SHA256: hex.EncodeToString(derivativeHash[:]), MIME: "image/png", SizeBytes: int64(len(derivative))},
		},
	}
	input.SourceRepublish.Association.LocationID = input.Audio.LocationID
	input.SourceRepublish.Association.MediaVariantID = input.Audio.MediaVariantID
	probe := func(ctx context.Context, _ string, candidate sourcefs.RegularFile, _ publication.AudioContainer) ([]byte, error) {
		var candidatePath string
		if err := candidate.Borrow(ctx, func(file *os.File) error {
			candidatePath = file.Name()
			return nil
		}); err != nil {
			return nil, err
		}
		capture, err := (metadata.Reader{}).Read(ctx, candidatePath, metadata.ReadRequest{CoverInspection: true})
		if err != nil {
			return nil, err
		}
		assets, err := ObservePublicationAssets(capture)
		if err != nil {
			return nil, err
		}
		// Report the actual candidate topology: the built artifact only carries
		// the cover when this run's optional cover write was verified.
		streams := `{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"sample_rate":"44100","bits_per_raw_sample":"16"}`
		if assets.Cover.State == persistence.PublicationAssetEmbedded {
			streams += `,{"index":1,"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}}`
		}
		return []byte(`{"format":{"format_name":"flac"},"streams":[` + streams + `]}`), nil
	}
	commands := &simplePreparationProbeCommand{}
	validator := RealPublicationStageValidator{Tools: publicationValidatorTools{}, MetadataReader: metadata.Reader{}, Probe: probe}
	run := func(writer PublicationMetadataWriter, output string) (PublicationSimplePreparationResult, error) {
		return (PublicationSimplePreparation{Commands: commands, FFmpegPath: "ffmpeg", Metadata: writer, Validator: validator}).PrepareSourceRepublish(ctx, input, sourcePath, filepath.Join(t.TempDir(), "temporary"), output)
	}
	readCandidate := func(path string) (metadata.Capture, metadata.ArtworkSnapshot) {
		t.Helper()
		capture, err := (metadata.Reader{}).Read(ctx, path, metadata.ReadRequest{})
		if err != nil {
			t.Fatal(err)
		}
		artwork, err := metadata.ReadArtworkSnapshot(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		return capture, artwork
	}
	assertFrozenTagsAndCover := func(capture metadata.Capture, artwork metadata.ArtworkSnapshot, wantLyrics bool) {
		if capture.Tags["TITLE"][0] != "Frozen desired title" || capture.Tags["ARTIST"][0] != "Frozen desired artist" {
			t.Fatalf("prepared selected-source tags = %#v", capture.Tags)
		}
		if wantLyrics {
			if got := capture.Tags["LYRICS"]; len(got) != 1 || got[0] != lyrics {
				t.Fatalf("prepared frozen lyrics = %#v, want exact lyrics", got)
			}
		} else if _, exists := capture.Tags["LYRICS"]; exists {
			t.Fatalf("optional lyrics fallback retained LYRICS: %#v", capture.Tags["LYRICS"])
		}
		if len(artwork.Images) != 1 || !bytes.Equal(artwork.Images[0].Data, derivative) || artwork.Images[0].MIME != "image/png" {
			t.Fatalf("prepared cover = %#v, want exact frozen PNG derivative", artwork.ArtworkInventory())
		}
	}

	// Successful optional-asset embedding proves the frozen text and derivative
	// are used instead of metadata and pictures carried by CURRENT or SOURCE.
	commands.calls = 0
	output := filepath.Join(t.TempDir(), "candidate.flac")
	prepared, err := run(metadata.Writer{}, output)
	if err != nil {
		t.Fatal(err)
	}
	capture, artwork := readCandidate(output)
	assertFrozenTagsAndCover(capture, artwork, true)
	if commands.calls != 1 || prepared.LyricsOutcome == nil || prepared.LyricsOutcome.State != persistence.PublicationAssetEmbedded || prepared.SourceRepublishCoverWarning != "" ||
		prepared.SourceRepublishOptionalResult == nil || *prepared.SourceRepublishOptionalResult != (persistence.PublicationSourceRepublishOptionalResult{
		LyricsOutcome: persistence.PublicationSourceRepublishLyricsExactRoundTrip,
		CoverOutcome:  persistence.PublicationSourceRepublishCoverVerified,
	}) {
		t.Fatalf("successful preparation builds=%d lyrics=%#v cover warning=%q", commands.calls, prepared.LyricsOutcome, prepared.SourceRepublishCoverWarning)
	}

	// A simulated lyrics readback fallback must rebuild from the selected source
	// while retaining the successfully written frozen derivative.
	lyricsFallbackWriter := &sourceRepublishOptionalWriter{lyricsFallback: true}
	commands.calls = 0
	output = filepath.Join(t.TempDir(), "candidate.flac")
	prepared, err = run(lyricsFallbackWriter, output)
	if err != nil {
		t.Fatal(err)
	}
	if lyricsFallbackWriter.calls != 2 || commands.calls != 2 {
		t.Fatalf("optional writer calls=%d source builds=%d, want 2 each", lyricsFallbackWriter.calls, commands.calls)
	}
	actual, artwork := readCandidate(output)
	assertFrozenTagsAndCover(actual, artwork, false)
	if prepared.LyricsOutcome == nil || prepared.LyricsOutcome.State != persistence.PublicationAssetAbsent || prepared.SourceRepublishCoverWarning != "" ||
		prepared.SourceRepublishOptionalResult == nil || *prepared.SourceRepublishOptionalResult != (persistence.PublicationSourceRepublishOptionalResult{
		LyricsOutcome: persistence.PublicationSourceRepublishLyricsRebuiltWithoutLyrics,
		CoverOutcome:  persistence.PublicationSourceRepublishCoverVerified,
	}) {
		t.Fatalf("optional outcomes after lyrics fallback = lyrics %#v cover warning %q", prepared.LyricsOutcome, prepared.SourceRepublishCoverWarning)
	}

	// Cover write failure is independent: it is accepted only with no cover,
	// while the exact mandatory tags and optional lyrics remain on the candidate.
	coverFailureWriter := &sourceRepublishOptionalWriter{coverFailure: true}
	commands.calls = 0
	output = filepath.Join(t.TempDir(), "cover-failure.flac")
	prepared, err = run(coverFailureWriter, output)
	if err != nil {
		t.Fatalf("optional cover failure was not accepted as absent: %v", err)
	}
	actual, err = (metadata.Reader{}).Read(ctx, output, metadata.ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if actual.Tags["TITLE"][0] != "Frozen desired title" || actual.Tags["LYRICS"][0] != lyrics {
		t.Fatalf("cover-failure candidate tags = %#v", actual.Tags)
	}
	artwork, err = metadata.ReadArtworkSnapshot(ctx, output)
	if err != nil {
		t.Fatal(err)
	}
	if len(artwork.Images) != 0 || prepared.SourceRepublishCoverWarning == "" || prepared.LyricsOutcome == nil || prepared.LyricsOutcome.State != persistence.PublicationAssetEmbedded ||
		prepared.SourceRepublishOptionalResult == nil || *prepared.SourceRepublishOptionalResult != (persistence.PublicationSourceRepublishOptionalResult{
		LyricsOutcome: persistence.PublicationSourceRepublishLyricsExactRoundTrip,
		CoverOutcome:  persistence.PublicationSourceRepublishCoverWriteFailed,
	}) {
		t.Fatalf("cover failure accepted with artwork=%d warning=%q lyrics=%#v", len(artwork.Images), prepared.SourceRepublishCoverWarning, prepared.LyricsOutcome)
	}

	// A frozen preparation failure is distinct from a writer failure for a ready derivative.
	input.SourceRepublish.Cover.Derivation = "optional_failure"
	input.SourceRepublish.Cover.PNG = nil
	input.SourceRepublish.Cover.SHA256 = ""
	input.SourceRepublish.Cover.MIME = ""
	input.SourceRepublish.Cover.SizeBytes = 0
	input.SourceRepublish.Cover.Failure = "derivative_failed"
	output = filepath.Join(t.TempDir(), "preparation-failure.flac")
	prepared, err = run(metadata.Writer{}, output)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.SourceRepublishOptionalResult == nil || *prepared.SourceRepublishOptionalResult != (persistence.PublicationSourceRepublishOptionalResult{
		LyricsOutcome: persistence.PublicationSourceRepublishLyricsExactRoundTrip,
		CoverOutcome:  persistence.PublicationSourceRepublishCoverPreparationFailed,
	}) {
		t.Fatalf("frozen cover preparation failure receipt = %#v", prepared.SourceRepublishOptionalResult)
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
