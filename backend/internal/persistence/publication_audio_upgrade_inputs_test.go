package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func publicationAudioUpgradeFixture(t *testing.T) PublicationInputs {
	t.Helper()
	input := publicationInputsFixture(t, "audio")
	input.Version = PublicationAudioUpgradeInputsVersion
	input.SourceAuthorityMode = "automatic"
	input.InitialLyrics = nil
	size, mtime, coverSize := int64(4096), int64(1234), int64(2048)
	input.AudioUpgrade = &PublicationAudioUpgradeInput{
		SourceAuthorityRevision: uuid.New(),
		Current: PublicationCurrentWitness{
			Generation: uuid.New(), RelativePath: "Artist/Album/current.mka",
			Ownership:  PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
			AudioFacts: input.Audio.AudioFacts,
			Assets: PublicationAssetOutcomes{Version: PublicationAssetOutcomesVersion,
				Cover:  PublicationCoverOutcome{State: PublicationAssetEmbedded, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MIME: "image/jpeg", SizeBytes: &coverSize},
				Lyrics: PublicationLyricsOutcome{State: PublicationAssetAbsent}},
			Tags:             map[string][]string{"title": {"Current"}, "LYRICS": {"[00:01.00]current lyrics"}},
			ArtworkInventory: []PublicationArtworkWitness{{Index: 0, MIME: "image/jpeg", Size: coverSize, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		},
	}
	input.DesiredTags = input.Clone().AudioUpgrade.Current.Tags
	return input
}

func TestPublicationAudioUpgradeInputsValidateRoundTripAndExecutionSupport(t *testing.T) {
	input := publicationAudioUpgradeFixture(t)
	if err := input.Validate(); err != nil {
		t.Fatalf("valid audio upgrade rejected: %v", err)
	}
	if !input.SupportsExecution() {
		t.Fatal("valid audio upgrade is not executable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePublicationInputs(raw)
	if err != nil {
		t.Fatalf("decode valid audio upgrade: %v", err)
	}
	if decoded.AudioUpgrade.Current.Tags["LYRICS"][0] != "[00:01.00]current lyrics" {
		t.Fatal("CURRENT lyrics witness did not survive round trip")
	}
}

func TestPublicationAudioUpgradeEncoderDescriptionsFollowAudioTransform(t *testing.T) {
	input := publicationAudioUpgradeFixture(t)
	input.AudioUpgrade.Current.Tags["ENCODEDBY"] = []string{"old source encoder"}
	input.Audio.AnalysedMetadata = &PublicationAnalysedMetadata{
		Tags:             map[string][]string{"ENCODEDBY": {"new source encoder"}, "ENCODERSETTINGS": {"new parameters"}},
		MetadataResultID: uuid.New(), WinningResultID: uuid.New(),
	}

	input.DesiredTags = publication.ProjectSourceEncoderTags(
		input.AudioUpgrade.Current.Tags, input.Audio.AnalysedMetadata.Tags, publication.AudioCodecCopy,
	)
	if err := input.Validate(); err != nil {
		t.Fatalf("copy audio with selected source descriptions rejected: %v", err)
	}
	if input.DesiredTags["ENCODEDBY"][0] != "new source encoder" || input.DesiredTags["ENCODERSETTINGS"][0] != "new parameters" {
		t.Fatalf("copy did not select source descriptions: %#v", input.DesiredTags)
	}

	input.Format = publication.AudioFormatM4A
	input.DesiredTags = publication.ProjectSourceEncoderTags(
		input.AudioUpgrade.Current.Tags, input.Audio.AnalysedMetadata.Tags, publication.AudioCodecALAC,
	)
	if err := input.Validate(); err != nil {
		t.Fatalf("transcode with current musical metadata rejected: %v", err)
	}
	if input.DesiredTags["title"][0] != "Current" || input.DesiredTags["LYRICS"][0] != "[00:01.00]current lyrics" || input.DesiredTags["ENCODEDBY"] != nil {
		t.Fatalf("transcode did not preserve only current musical metadata: %#v", input.DesiredTags)
	}
}

func TestPublicationAudioUpgradeInputsRejectVersionDiscriminantsAndInvalidWitness(t *testing.T) {
	tests := []struct {
		name   string
		change func(*PublicationInputs)
	}{
		{"v1 extension", func(input *PublicationInputs) { input.Version = PublicationInputsVersion }},
		{"missing upgrade", func(input *PublicationInputs) { input.AudioUpgrade = nil }},
		{"non-automatic authority", func(input *PublicationInputs) { input.SourceAuthorityMode = "manual" }},
		{"lyrics authority", func(input *PublicationInputs) { input.InitialLyrics = &PublicationInitialLyricsInput{} }},
		{"rewrite discriminator", func(input *PublicationInputs) { input.IntentKind = "rewrite" }},
		{"missing current generation", func(input *PublicationInputs) { input.AudioUpgrade.Current.Generation = uuid.Nil }},
		{"missing source authority revision", func(input *PublicationInputs) { input.AudioUpgrade.SourceAuthorityRevision = uuid.Nil }},
		{"missing ownership evidence", func(input *PublicationInputs) { input.AudioUpgrade.Current.Ownership.SizeBytes = nil }},
		{"missing ownership digest", func(input *PublicationInputs) { input.AudioUpgrade.Current.Ownership.SHA256 = "" }},
		{"changed desired tags", func(input *PublicationInputs) { input.DesiredTags["title"][0] = "Pending metadata" }},
		{"changed current musical metadata", func(input *PublicationInputs) { input.DesiredTags["LYRICS"][0] = "[00:02.00]not current" }},
		{"missing current tags", func(input *PublicationInputs) { input.AudioUpgrade.Current.Tags = nil }},
		{"invalid assets", func(input *PublicationInputs) { input.AudioUpgrade.Current.Assets.Version = 0 }},
		{"missing artwork inventory", func(input *PublicationInputs) { input.AudioUpgrade.Current.ArtworkInventory = nil }},
		{"invalid artwork order", func(input *PublicationInputs) { input.AudioUpgrade.Current.ArtworkInventory[0].Index = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := publicationAudioUpgradeFixture(t)
			test.change(&input)
			if err := input.Validate(); err == nil {
				t.Fatal("invalid audio upgrade accepted")
			}
		})
	}
}

func TestPublicationAudioUpgradeCloneDetachesWitness(t *testing.T) {
	input := publicationAudioUpgradeFixture(t)
	clone := input.Clone()
	current := &clone.AudioUpgrade.Current
	*current.Ownership.SizeBytes = 1
	*current.AudioFacts.Channels = 1
	*current.Assets.Cover.SizeBytes = 1
	current.Tags["title"][0] = "Changed"
	current.ArtworkInventory[0].Description = "Changed"
	current.Tags["new"] = []string{}
	if *input.AudioUpgrade.Current.Ownership.SizeBytes != 4096 || *input.AudioUpgrade.Current.AudioFacts.Channels != 2 || *input.AudioUpgrade.Current.Assets.Cover.SizeBytes != 2048 || input.AudioUpgrade.Current.Tags["title"][0] != "Current" || input.AudioUpgrade.Current.Tags["new"] != nil || input.AudioUpgrade.Current.ArtworkInventory[0].Description != "" {
		t.Fatal("audio upgrade clone shares mutable witness data")
	}
}
