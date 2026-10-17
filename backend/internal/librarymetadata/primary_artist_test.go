package librarymetadata

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestResolvePrimaryArtistDeduplicatesOrderedCreditsByLocalIdentity(t *testing.T) {
	artistID := uuid.New()
	mbid := uuid.New()
	credits := []CreditedArtist{
		{ArtistID: artistID, ArtistName: "Artist", MBID: &mbid, CreditName: "Artist", JoinPhrase: " feat. "},
		{ArtistID: artistID, ArtistName: "Artist", MBID: &mbid, CreditName: "Artist guest credit", JoinPhrase: ""},
	}
	resolved, err := ResolvePrimaryArtist(credits, nil)
	if err != nil {
		t.Fatalf("ResolvePrimaryArtist() error = %v", err)
	}
	if resolved.Artist == nil || resolved.RequiresSelection {
		t.Fatalf("resolution = %#v, want one resolved artist", resolved)
	}
	if resolved.Artist.ArtistID != artistID || resolved.Artist.Name != "Artist" || resolved.Artist.MBID == nil || *resolved.Artist.MBID != mbid {
		t.Fatalf("resolved identity = %#v, want one identity from the credit", resolved.Artist)
	}
}

func TestResolvePrimaryArtistRequiresChoiceForSameNameDifferentIDs(t *testing.T) {
	firstID, secondID := uuid.New(), uuid.New()
	credits := []CreditedArtist{
		{ArtistID: firstID, ArtistName: "Same Name"},
		{ArtistID: secondID, ArtistName: "Same Name"},
	}
	resolved, err := ResolvePrimaryArtist(credits, nil)
	if !errors.Is(err, ErrPrimaryArtistRequired) || !resolved.RequiresSelection || resolved.Artist != nil {
		t.Fatalf("ResolvePrimaryArtist() = %#v, %v; want required explicit selection", resolved, err)
	}
	selected, err := ResolvePrimaryArtist(credits, &secondID)
	if err != nil {
		t.Fatalf("ResolvePrimaryArtist(selected) error = %v", err)
	}
	if selected.Artist == nil || selected.Artist.ArtistID != secondID || selected.Artist.Name != "Same Name" || selected.Artist.MBID != nil {
		t.Fatalf("selected resolution = %#v, want second local identity", selected)
	}
}

func TestResolvePrimaryArtistRemovedSelectionIsInvalidAndSticky(t *testing.T) {
	removedID := uuid.New()
	remainingID := uuid.New()
	credits := []CreditedArtist{{ArtistID: remainingID, ArtistName: "Remaining"}}
	resolved, err := ResolvePrimaryArtist(credits, &removedID)
	if !errors.Is(err, ErrPrimaryArtistInvalid) || resolved.Artist != nil {
		t.Fatalf("ResolvePrimaryArtist() = %#v, %v; want invalid stale selection", resolved, err)
	}
}

func TestResolvePrimaryArtistRejectsAmbiguousMBID(t *testing.T) {
	mbid := uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	credits := []CreditedArtist{
		{ArtistID: firstID, ArtistName: "First", MBID: &mbid},
		{ArtistID: secondID, ArtistName: "Second", MBID: &mbid},
	}
	_, err := ResolvePrimaryArtist(credits, &firstID)
	if !errors.Is(err, ErrAmbiguousArtistMBID) {
		t.Fatalf("ResolvePrimaryArtist() error = %v, want ambiguous MBID", err)
	}
}

func TestResolvePrimaryArtistRejectsMissingAndConflictingIdentity(t *testing.T) {
	if _, err := ResolvePrimaryArtist([]CreditedArtist{{ArtistName: "No ID"}}, nil); err == nil {
		t.Fatal("ResolvePrimaryArtist() accepted a credit with missing local ID")
	}
	artistID := uuid.New()
	credits := []CreditedArtist{
		{ArtistID: artistID, ArtistName: "First"},
		{ArtistID: artistID, ArtistName: "Different"},
	}
	if _, err := ResolvePrimaryArtist(credits, nil); err == nil {
		t.Fatal("ResolvePrimaryArtist() accepted conflicting data for one local identity")
	}
}
