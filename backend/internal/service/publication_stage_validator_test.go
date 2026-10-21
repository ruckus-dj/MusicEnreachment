package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type publicationValidatorTools struct{}

func (publicationValidatorTools) Executable(context.Context, []persistence.SourceAnalysisToolSelection, string) (string, error) {
	return "ffprobe", nil
}

type publicationValidatorMetadataReader struct {
	capture metadata.Capture
	err     error
	request metadata.ReadRequest
}

func (reader *publicationValidatorMetadataReader) Read(_ context.Context, _ string, request metadata.ReadRequest) (metadata.Capture, error) {
	reader.request = request
	return reader.capture, reader.err
}

func TestRealPublicationStageValidatorCapturesCurrentAssetEvidence(t *testing.T) {
	_, _, input, _, _ := pubStageSetup(t, &pubStageRepo{}, pubStageCommand{}, nil, nil)
	input.Version = persistence.PublicationInputsVersion
	input.QualityPolicyVersion = publication.QualityPolicyVersion

	stage := filepath.Join(t.TempDir(), "stage.flac")
	if err := os.WriteFile(stage, []byte("staged payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := &publicationValidatorMetadataReader{capture: metadata.Capture{
		Tags: input.DesiredTags, CoverInspection: &metadata.CoverInspection{Observed: true},
	}}
	validator := RealPublicationStageValidator{
		Tools: publicationValidatorTools{}, MetadataReader: reader,
		Probe: func(context.Context, string, sourcefs.RegularFile, publication.AudioContainer) ([]byte, error) {
			return []byte(`{"format":{"format_name":"mp3"},"streams":[{"index":0,"codec_type":"audio","codec_name":"mp3","channels":2}]}`), nil
		},
	}
	got, err := validator.Validate(context.Background(), stage, input)
	if err != nil {
		t.Fatal(err)
	}
	if !reader.request.CoverInspection {
		t.Fatal("metadata read did not request cover inspection")
	}
	var assets persistence.PublicationAssetOutcomes
	if err := json.Unmarshal(got.Assets, &assets); err != nil {
		t.Fatalf("decode actual assets: %v", err)
	}
	if assets.Cover.State != persistence.PublicationAssetAbsent || assets.Lyrics.State != persistence.PublicationAssetAbsent {
		t.Fatalf("assets = %#v, want observed absent cover and lyrics", assets)
	}
	if got.QualityPolicyVersion != publication.QualityPolicyVersion {
		t.Fatalf("quality policy = %d", got.QualityPolicyVersion)
	}
}

func TestRealPublicationStageValidatorRejectsManualLyricsBeforeOpeningStageOrDependencies(t *testing.T) {
	_, _, input, _, _ := pubStageSetup(t, &pubStageRepo{}, pubStageCommand{}, nil, nil)
	input.Version = persistence.PublicationInputsVersion
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	input.IntentKind, input.Audio = "rewrite", nil
	input.InitialLyrics = nil
	input.Format = publication.AudioFormatSource
	fixture := makeRewriteFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	input.Rewrite = fixture.repo.input.Rewrite
	input.ManualLyrics = &persistence.PublicationManualLyricsInput{
		ExpectedMetadataRevision: 1, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration,
		State: "explicit_empty",
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("manual-lyrics rewrite is invalid: %v", err)
	}
	stage := filepath.Join(t.TempDir(), "stage.flac")
	if err := os.WriteFile(stage, []byte("stage"), 0o600); err != nil {
		t.Fatal(err)
	}
	validator := RealPublicationStageValidator{
		Tools:          panicPublicationValidatorTools{},
		MetadataReader: panicPublicationMetadataReader{},
		Probe: func(context.Context, string, sourcefs.RegularFile, publication.AudioContainer) ([]byte, error) {
			panic("unsupported manual-lyrics rewrite reached probe")
		},
	}
	if _, err := validator.Validate(context.Background(), stage, input); err == nil || !strings.Contains(err.Error(), "does not support execution") {
		t.Fatalf("Validate error = %v, want unsupported execution", err)
	}
}

type panicPublicationValidatorTools struct{}

func (panicPublicationValidatorTools) Executable(context.Context, []persistence.SourceAnalysisToolSelection, string) (string, error) {
	panic("unsupported manual-lyrics rewrite resolved tools")
}

type panicPublicationMetadataReader struct{}

func (panicPublicationMetadataReader) Read(context.Context, string, metadata.ReadRequest) (metadata.Capture, error) {
	panic("unsupported manual-lyrics rewrite read metadata")
}

func TestPublicationStageAudioPlanPreservesRewriteCodecAndContainer(t *testing.T) {
	lossless := false
	channels := 2
	facts := persistence.PersistedAudioFacts{
		Codec: "mp3", Lossless: &lossless, Channels: &channels,
		Container: publication.AudioContainerMP3, SupportedSourceContainer: true,
	}
	rewrite := persistence.PublicationInputs{
		IntentKind: "rewrite", Rewrite: &persistence.PublicationRewriteInput{},
		Format: publication.AudioFormatM4A,
	}
	plan, err := publicationStageAudioPlan(rewrite, facts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != publication.AudioCodecCopy || plan.OutputContainer != publication.AudioContainerMP3 {
		t.Fatalf("rewrite plan = %#v, want MP3 codec-copy", plan)
	}

	audio := persistence.PublicationInputs{IntentKind: "audio", Audio: &persistence.PublicationAudioInput{}, Format: publication.AudioFormatM4A}
	plan, err = publicationStageAudioPlan(audio, facts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != publication.AudioCodecAAC || plan.OutputContainer != publication.AudioContainerM4A {
		t.Fatalf("audio plan = %#v, want AAC in M4A", plan)
	}
}

func TestValidatePublicationTopology(t *testing.T) {
	tests := []struct {
		name  string
		json  string
		valid bool
	}{
		{name: "single audio", json: `{"streams":[{"codec_type":"audio"}]}`, valid: true},
		{name: "video", json: `{"streams":[{"codec_type":"audio"},{"codec_type":"video"}]}`},
		{name: "attached picture", json: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","disposition":{"attached_pic":1}}]}`},
		{name: "multiple audio", json: `{"streams":[{"codec_type":"audio"},{"codec_type":"audio"}]}`},
		{name: "chapters", json: `{"streams":[{"codec_type":"audio"}],"chapters":[{}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePublicationTopology([]byte(test.json))
			if (err == nil) != test.valid {
				t.Fatalf("validatePublicationTopology() error = %v, valid %v", err, test.valid)
			}
		})
	}
}

func TestEqualTagsChecksEveryRequestedValueAndDeletion(t *testing.T) {
	wanted := map[string][]string{"artist": {"one", "two"}, "title": {"track"}}
	if !equalTags(map[string][]string{"artist": {"one", "two"}, "title": {"track"}}, wanted) {
		t.Fatal("matching tags did not validate")
	}
	if equalTags(map[string][]string{"artist": {"one"}, "title": {"track"}}, wanted) {
		t.Fatal("dropped repeated value was accepted")
	}
	if equalTags(map[string][]string{"artist": {"one", "two"}}, wanted) {
		t.Fatal("dropped requested tag was accepted")
	}
	if equalTags(map[string][]string{"artist": {"one", "two"}, "title": {"track"}, "extra": {"unexpected"}}, wanted) {
		t.Fatal("unexpected tag was accepted")
	}
	if !equalTags(map[string][]string{"artist": {"one", "two"}}, map[string][]string{"artist": {"one", "two"}, "title": {}}) {
		t.Fatal("deleted explicitly empty requested tag was rejected")
	}
	if !equalTags(map[string][]string{"artist": {"one", "two"}, "title": {}}, map[string][]string{"artist": {"one", "two"}, "title": {}}) {
		t.Fatal("empty explicitly requested tag was rejected")
	}
	if equalTags(map[string][]string{"artist": {"one", "two"}, "title": {}, "extra": {"unexpected"}}, map[string][]string{"artist": {"one", "two"}, "title": {}}) {
		t.Fatal("unexpected non-empty tag was accepted")
	}
}

func TestValidatedPublicationAudioFactsCanPlanSourceCopy(t *testing.T) {
	lossless := false
	channels := int64(2)
	bitrate := int64(192000)
	facts, err := persistedPublicationStageAudioFacts(0, "mp3", lossless, &channels, &bitrate, nil, nil, publication.AudioContainerMP3)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.SupportedSourceContainer {
		t.Fatal("validated MP3 output was not marked as source-copy supported")
	}
	plan, err := publication.PlanAudioTransform(publication.AudioFormatSource, facts.AudioSourceFacts())
	if err != nil {
		t.Fatalf("PlanAudioTransform(source) error = %v", err)
	}
	if plan.Action != publication.AudioCodecCopy || plan.OutputContainer != publication.AudioContainerMP3 {
		t.Fatalf("source plan = %#v, want MP3 codec-copy", plan)
	}
}

func TestPublicationStageSampleRateEvidence(t *testing.T) {
	want := 44100
	equal := int64(44100)
	wrong := int64(48000)
	for _, test := range []struct {
		name     string
		expected *int
		actual   *int64
		valid    bool
	}{
		{name: "copy equal", expected: &want, actual: &equal, valid: true},
		{name: "rewrite AAC mismatch", expected: &want, actual: &wrong},
		{name: "rewrite ALAC missing", expected: &want},
		{name: "unknown optional", actual: &wrong, valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := publicationStageSampleRateMatches(test.expected, test.actual); got != test.valid {
				t.Fatalf("sample-rate match = %v, want %v", got, test.valid)
			}
		})
	}
	observed, err := persistedPublicationStageAudioFacts(0, "aac", false, nil, nil, nil, &equal, publication.AudioContainerM4A)
	if err != nil {
		t.Fatal(err)
	}
	if observed.SampleRate == nil || *observed.SampleRate != 44100 {
		t.Fatalf("persisted observed sample rate = %v, want 44100", observed.SampleRate)
	}
}

func TestPublicationCodecLosslessnessFailsClosedForAmbiguousCodecs(t *testing.T) {
	for _, codec := range []string{"wavpack", "dts"} {
		if lossless, known := publicationCodecLossless(codec); known || lossless {
			t.Errorf("publicationCodecLossless(%q) = (%v, %v), want (false, false)", codec, lossless, known)
		}
	}
}

func TestPublicationContainerMatchesAliases(t *testing.T) {
	for _, test := range []struct {
		observed string
		expected string
		valid    bool
	}{
		{"matroska,webm", "mka", true},
		{"mov,mp4,m4a,3gp,3g2,mj2", "m4a", true},
		{"flac", "flac", true},
		{"matroska", "m4a", false},
	} {
		if got := publicationContainerMatches(test.observed, publication.AudioContainer(test.expected)); got != test.valid {
			t.Errorf("publicationContainerMatches(%q, %q) = %v, want %v", test.observed, test.expected, got, test.valid)
		}
	}
}
