package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func publicationProviderTagFixture(t *testing.T) PublicationInputs {
	t.Helper()
	current := publicationAudioUpgradeFixture(t).AudioUpgrade.Current
	current.Tags["TITLE"] = current.Tags["title"]
	delete(current.Tags, "title")
	input := publicationInputsFixture(t, "rewrite")
	input.Version = PublicationProviderTagInputsVersion
	input.Format = publication.AudioFormatSource
	input.Rewrite = &PublicationRewriteInput{
		CurrentGeneration: current.Generation, RelativePath: current.RelativePath,
		Ownership: current.Ownership, AudioFacts: current.AudioFacts,
	}
	input.ProviderTags = &PublicationProviderTagInput{
		Current: current, AudioPayloadSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ProviderSourceID: uuid.New(), Endpoint: "https://musicbrainz.org/ws/2",
		ConfigurationIdentity: "musicbrainz-public", AuthorityRevision: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Patch: map[string][]string{"TITLE": {"Provider title"}}, ProtectedKeys: []string{},
	}
	input.DesiredTags = map[string][]string{"TITLE": {"Provider title"}, "LYRICS": {"[00:01.00]current lyrics"}}
	return input
}

func TestPublicationProviderTagInputsValidateRoundTripExclusively(t *testing.T) {
	input := publicationProviderTagFixture(t)
	if err := input.Validate(); err != nil {
		t.Fatalf("valid provider tag rewrite rejected: %v", err)
	}
	if !input.SupportsExecution() {
		t.Fatal("validated provider tag rewrite is not executable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePublicationInputs(raw)
	if err != nil {
		t.Fatalf("decode valid provider tag rewrite: %v", err)
	}
	if decoded.ProviderTags.ProtectedKeys == nil || len(decoded.ProviderTags.ProtectedKeys) != 0 {
		t.Fatal("explicit empty protected-key decision was not preserved")
	}
	if decoded.ProviderTags.AudioPayloadSHA256 != input.ProviderTags.AudioPayloadSHA256 {
		t.Fatal("audio payload digest did not survive round trip")
	}
}

func TestPublicationProviderTagInputsRejectInvalidAuthority(t *testing.T) {
	tests := []struct {
		name   string
		change func(*PublicationInputs)
	}{
		{"desired tags mismatch", func(in *PublicationInputs) { in.DesiredTags["TITLE"][0] = "wrong" }},
		{"changed protected keys", func(in *PublicationInputs) { in.ProviderTags.ProtectedKeys = []string{"TITLE"} }},
		{"other authority", func(in *PublicationInputs) { in.ManualCover = &PublicationManualCoverInput{} }},
		{"witness mismatch", func(in *PublicationInputs) { in.Rewrite.RelativePath = "other/path.mka" }},
		{"empty patch", func(in *PublicationInputs) { in.ProviderTags.Patch = map[string][]string{} }},
		{"nil protected keys", func(in *PublicationInputs) { in.ProviderTags.ProtectedKeys = nil }},
		{"noncanonical patch", func(in *PublicationInputs) { in.ProviderTags.Patch = map[string][]string{" title": {"x"}} }},
		{"lyrics patch", func(in *PublicationInputs) { in.ProviderTags.Patch = map[string][]string{"LYRICS": {"new"}} }},
		{"missing audio digest", func(in *PublicationInputs) { in.ProviderTags.AudioPayloadSHA256 = "" }},
		{"invalid audio digest", func(in *PublicationInputs) { in.ProviderTags.AudioPayloadSHA256 = "not-a-digest" }},
		{"no-op patch", func(in *PublicationInputs) {
			in.ProviderTags.Patch = map[string][]string{"TITLE": {"Current"}}
			in.DesiredTags["TITLE"][0] = "Current"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := publicationProviderTagFixture(t)
			if err := input.Validate(); err != nil {
				t.Fatalf("valid provider tag rewrite rejected: %v", err)
			}
			test.change(&input)
			if err := input.Validate(); err == nil {
				t.Fatal("invalid provider tag rewrite accepted")
			}
		})
	}
}

func TestPublicationProviderTagInputsCloneDetachesNestedWitness(t *testing.T) {
	input := publicationProviderTagFixture(t)
	if err := input.Validate(); err != nil {
		t.Fatalf("valid provider tag rewrite rejected: %v", err)
	}
	clone := input.Clone()
	if err := clone.Validate(); err != nil {
		t.Fatalf("cloned provider tag rewrite rejected: %v", err)
	}
	clone.ProviderTags.Patch["TITLE"][0] = "mutated"
	clone.ProviderTags.ProtectedKeys = append(clone.ProviderTags.ProtectedKeys, "COMMENT")
	clone.ProviderTags.Current.Tags["TITLE"][0] = "mutated"
	*clone.ProviderTags.Current.Ownership.SizeBytes = 1
	*clone.ProviderTags.Current.AudioFacts.Channels = 1
	*clone.ProviderTags.Current.Assets.Cover.SizeBytes = 1
	clone.ProviderTags.Current.ArtworkInventory[0].Description = "mutated"
	if input.ProviderTags.Patch["TITLE"][0] != "Provider title" || len(input.ProviderTags.ProtectedKeys) != 0 || input.ProviderTags.Current.Tags["TITLE"][0] != "Current" || *input.ProviderTags.Current.Ownership.SizeBytes != 4096 || *input.ProviderTags.Current.AudioFacts.Channels != 2 || *input.ProviderTags.Current.Assets.Cover.SizeBytes != 2048 || input.ProviderTags.Current.ArtworkInventory[0].Description != "" {
		t.Fatal("provider tag clone shares mutable authority data")
	}
}
