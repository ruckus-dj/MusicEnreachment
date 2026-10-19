package persistence

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

func TestSetReleaseMBIDTemplateTokensUsesEightCharacterPart(t *testing.T) {
	mbid := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	tokens := map[string]string{}
	setReleaseMBIDTemplateTokens(tokens, &mbid)
	if got := tokens["ALBUM_MBID"]; got != mbid.String() {
		t.Fatalf("ALBUM_MBID = %q, want full %q", got, mbid.String())
	}
	if got := tokens["ALBUM_MBID_PART"]; got != "aaaaaaaa" {
		t.Fatalf("ALBUM_MBID_PART = %q, want first eight characters", got)
	}
}

func TestSetReleaseMBIDTemplateTokensOmitsMissingOrZeroMBID(t *testing.T) {
	zero := uuid.Nil
	for name, mbid := range map[string]*uuid.UUID{"nil": nil, "zero": &zero} {
		tokens := map[string]string{}
		setReleaseMBIDTemplateTokens(tokens, mbid)
		if len(tokens) != 0 {
			t.Fatalf("%s MBID produced tokens %#v", name, tokens)
		}
	}
}

func TestSetPrimaryArtistTemplateTokensUsesEightCharacterPart(t *testing.T) {
	artistID := uuid.New()
	mbid := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	tokens := map[string]string{}
	err := setPrimaryArtistTemplateTokens(tokens, "ALBUM", []librarymetadata.ArtistCredit{{
		ArtistID: artistID.String(), ArtistName: "Artist", MBID: shortMBIDStringPointer(mbid.String()),
	}}, &artistID)
	if err != nil {
		t.Fatal(err)
	}
	if got := tokens["ALBUM_PRIMARY_ARTIST_MBID"]; got != mbid.String() {
		t.Fatalf("ALBUM_PRIMARY_ARTIST_MBID = %q, want full %q", got, mbid.String())
	}
	if got := tokens["ALBUM_PRIMARY_ARTIST_MBID_PART"]; got != "11111111" {
		t.Fatalf("ALBUM_PRIMARY_ARTIST_MBID_PART = %q, want first eight characters", got)
	}
}

func TestSetPrimaryArtistTemplateTokensRejectsInvalidMBID(t *testing.T) {
	artistID := uuid.New()
	tokens := map[string]string{}
	err := setPrimaryArtistTemplateTokens(tokens, "ALBUM", []librarymetadata.ArtistCredit{{
		ArtistID: artistID.String(), ArtistName: "Artist", MBID: shortMBIDStringPointer("not-a-uuid"),
	}}, &artistID)
	if err == nil {
		t.Fatal("invalid artist MBID was accepted")
	}
}

func shortMBIDStringPointer(value string) *string { return &value }
