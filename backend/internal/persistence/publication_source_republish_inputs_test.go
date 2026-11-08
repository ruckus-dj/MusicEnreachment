package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestPublicationSourceRepublishInputsRoundTripAndClone(t *testing.T) {
	input := publicationAudioUpgradeFixture(t)
	current := clonePublicationCurrentWitness(input.AudioUpgrade.Current)
	input.Version = PublicationSourceRepublishInputsVersion
	input.SourceAuthorityMode = ""
	input.AudioUpgrade = nil
	input.InitialLyrics = nil
	input.SourceRepublish = &PublicationSourceRepublishInput{
		Current: current,
		Mode:    "ordinary", SourceAuthority: PublicationSourceAuthority{}, Association: PublicationRepublishAssociation{
			ID: uuid.New(), LocationID: input.Audio.LocationID, MediaVariantID: input.Audio.MediaVariantID,
		},
		Lyrics:          PublicationRepublishLyrics{State: "absent", Origin: "none"},
		LyricsAuthority: PublicationInitialLyricsInput{ExpectedMetadataRevision: 1},
		Cover:           PublicationRepublishCover{ReleaseID: uuid.New(), AuthorityRevision: 1, AuthorityMode: "manual", Derivation: "absent"},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate source republish inputs: %v", err)
	}
	if input.SupportsExecution() {
		t.Fatal("version 4 must not advertise generic execution")
	}
	invalid := input.Clone()
	invalid.SourceRepublish.Cover.SelectedGeneration = uuid.New()
	if err := invalid.Validate(); err == nil {
		t.Fatal("absent cover with selected generation was accepted")
	}
	invalid = input.Clone()
	text := "[00:01.00]line"
	digest := sha256.Sum256([]byte(text))
	invalid.SourceRepublish.Lyrics = PublicationRepublishLyrics{State: "value", Origin: "manual", Text: text, SHA256: hex.EncodeToString(digest[:])}
	if err := invalid.Validate(); err == nil {
		t.Fatal("manual lyrics without a matching saved receipt were accepted")
	}
	invalid = input.Clone()
	invalid.SourceRepublish.Lyrics = PublicationRepublishLyrics{State: "value", Origin: "lrclib", Text: text, SHA256: hex.EncodeToString(digest[:])}
	if err := invalid.Validate(); err == nil {
		t.Fatal("LRCLIB lyrics without enabled lookup were accepted")
	}
	invalid = input.Clone()
	invalid.SourceRepublish.Cover.Derivation = "ready"
	invalid.SourceRepublish.Cover.SelectedGeneration = uuid.New()
	invalid.SourceRepublish.Cover.PNG = []byte("not a PNG")
	if err := invalid.Validate(); err == nil {
		t.Fatal("ready derivative without original authority and valid PNG was accepted")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePublicationInputs(raw)
	if err != nil {
		t.Fatalf("decode source republish inputs: %v", err)
	}
	clone := decoded.Clone()
	clone.SourceRepublish.Current.Tags["title"][0] = "changed"
	if decoded.SourceRepublish.Current.Tags["title"][0] == "changed" {
		t.Fatal("clone shares current tags")
	}
	authority := PublicationSourceAuthority{
		Present: true, PositionID: uuid.New(), Mode: "automatic", Revision: uuid.New(), PublicationEpoch: uuid.New(),
		PublicationGeneration: current.Generation, SourceLocationID: uuid.New(), MediaVariantID: uuid.New(),
	}
	if err := authority.Validate(current.Generation); err != nil {
		t.Fatalf("valid baseline source authority for a replaced source rejected: %v", err)
	}
	authority.Revision = uuid.Nil
	if err := authority.Validate(current.Generation); err == nil {
		t.Fatal("present source authority without UUID revision was accepted")
	}
	authority.Revision = uuid.New()
	authority.SourceLocationID = uuid.Nil
	if err := authority.Validate(current.Generation); err == nil {
		t.Fatal("present source authority without baseline location was accepted")
	}
	authority.SourceLocationID = uuid.New()
	authority.MediaVariantID = uuid.Nil
	if err := authority.Validate(current.Generation); err == nil {
		t.Fatal("present source authority without baseline variant was accepted")
	}
	authority.Present = false
	if err := authority.Validate(current.Generation); err == nil {
		t.Fatal("absent source authority with identity fields was accepted")
	}
}

func TestPublicationRepublishLyricsRequiresExactTextDigest(t *testing.T) {
	text := "[00:01.00]line"
	digest := sha256.Sum256([]byte(text))
	lyrics := PublicationRepublishLyrics{State: "value", Origin: "source", Text: text, SHA256: hex.EncodeToString(digest[:])}
	if err := lyrics.Validate(); err != nil {
		t.Fatalf("valid exact lyrics rejected: %v", err)
	}
	lyrics.Text += "\n"
	if err := lyrics.Validate(); err == nil {
		t.Fatal("mutated lyric text accepted with stale digest")
	}
}
