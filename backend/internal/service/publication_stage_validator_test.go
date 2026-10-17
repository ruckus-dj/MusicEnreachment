package service

import (
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

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
	facts, err := persistedPublicationStageAudioFacts(0, "mp3", lossless, &channels, &bitrate, nil, publication.AudioContainerMP3)
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
