package publication

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

func TestProjectTagsPreservesCreditsAndOrderedArtistIDs(t *testing.T) {
	idOne, idTwo := uuid.NewString(), uuid.NewString()
	result, err := ProjectTags(TagProjectionInput{
		Release: ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{
			{ArtistName: "A", CreditName: "A", JoinPhrase: " & ", MBID: &idOne},
			{ArtistName: "B", CreditName: "Featured B", JoinPhrase: "", MBID: &idTwo},
		}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{
			{ArtistName: "A", CreditName: "The A", JoinPhrase: " feat. ", MBID: &idOne},
			{ArtistName: "B", CreditName: "B credited", JoinPhrase: "", MBID: &idTwo},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"ARTIST": {"The A feat. B credited"}, "ARTISTS": {"A", "B"}, "MUSICBRAINZ_ARTISTID": {idOne, idTwo},
		"ALBUMARTIST": {"A & Featured B"}, "ALBUMARTISTS": {"A", "B"}, "MUSICBRAINZ_ALBUMARTISTID": {idOne, idTwo},
		"ALBUM": {"Album"}, "TITLE": {"Song"},
	}
	if !reflect.DeepEqual(result.Tags, want) {
		t.Fatalf("tags = %#v, want %#v", result.Tags, want)
	}
}

func TestProjectTagsUsesDistinctRecordingAndReleaseTrackIDs(t *testing.T) {
	recording, releaseTrack := uuid.New(), uuid.New()
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, RecordingID: &recording, ReleaseTrackID: &releaseTrack},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Tags["MUSICBRAINZ_TRACKID"]; !reflect.DeepEqual(got, []string{recording.String()}) {
		t.Fatalf("recording ID = %#v", got)
	}
	if got := result.Tags["MUSICBRAINZ_RELEASETRACKID"]; !reflect.DeepEqual(got, []string{releaseTrack.String()}) {
		t.Fatalf("release-track ID = %#v", got)
	}
}

func TestProjectTagsExplicitEmptyFalseCompilationAndPartialArtistIDs(t *testing.T) {
	falseValue := false
	a, b := uuid.NewString(), uuid.NewString()
	result, err := ProjectTags(TagProjectionInput{
		Release: ReleaseTags{
			Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Compilation: &falseValue,
			Desired: &librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: map[string]librarymetadata.FieldValue{
				"genre":       librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false),
				"compilation": librarymetadata.ManualFieldValue(librarymetadata.StateValue, falseValue, true),
			}},
		},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{
			{ArtistName: "A", MBID: &a}, {ArtistName: "B", MBID: nil}, {ArtistName: "C", MBID: &b},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Tags["GENRE"]; got == nil || len(got) != 0 {
		t.Fatalf("explicit empty genre = %#v", got)
	}
	if got := result.Tags["COMPILATION"]; !reflect.DeepEqual(got, []string{"0"}) {
		t.Fatalf("false compilation = %#v", got)
	}
	if _, exists := result.Tags["MUSICBRAINZ_ARTISTID"]; exists {
		t.Fatalf("partial IDs emitted a misaligned ID list: %#v", result.Tags["MUSICBRAINZ_ARTISTID"])
	}
	if result.Fields["artist_credit.mbids"].Status != "unsupported" {
		t.Fatalf("missing MBID status = %#v", result.Fields["artist_credit.mbids"])
	}
}

func TestProjectTagsRequiresA1Fields(t *testing.T) {
	_, err := ProjectTags(TagProjectionInput{Release: ReleaseTags{Title: "Album"}, Position: PositionTags{Title: "Song"}})
	if err == nil {
		t.Fatal("expected required artists to block projection")
	}
}

func TestProjectTagsSupportsDecodedEnvelopeValues(t *testing.T) {
	release, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"release","desired":{"genre":{"state":"value","value":["Jazz","Fusion"],"authority":"manual","provenance":{"kind":"manual"}},"release_secondary_types":{"state":"value","value":["Live","Compilation"],"authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	position, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"position","desired":{"bpm":{"state":"value","value":123.450,"authority":"manual","provenance":{"kind":"manual"}},"disc_number":{"state":"value","value":2,"authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"GENRE": {"Jazz", "Fusion"}, "RELEASETYPES": {"Live", "Compilation"},
		"BPM": {"123.450"}, "DISCNUMBER": {"2"},
	}
	for key, values := range want {
		if !reflect.DeepEqual(result.Tags[key], values) {
			t.Errorf("%s = %#v, want %#v", key, result.Tags[key], values)
		}
	}
}

func TestProjectTagsDoesNotPublishUnconfirmedSyncedLyrics(t *testing.T) {
	position := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
		Desired: map[string]librarymetadata.FieldValue{
			"synced_lyrics": librarymetadata.ManualFieldValue(librarymetadata.StateValue, "[00:01.00]hello", true),
		},
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := result.Tags["LYRICS"]; exists {
		t.Fatalf("unconfirmed synced lyrics leaked into tags: %#v", result.Tags["LYRICS"])
	}
	if result.Fields["position.synced_lyrics"].Status != "unsupported" {
		t.Fatalf("synced lyrics status = %#v, want unsupported", result.Fields["position.synced_lyrics"])
	}
}

func TestProjectTagsMapsArtistMBIDByScope(t *testing.T) {
	trackArtistID, releaseArtistID := uuid.NewString(), uuid.NewString()
	release := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
		Desired: map[string]librarymetadata.FieldValue{
			"artist_mbid": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{releaseArtistID}, true),
		},
	}
	position := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition,
		Desired: map[string]librarymetadata.FieldValue{
			"artist_mbid": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []string{trackArtistID}, true),
		},
	}
	result, err := ProjectTags(TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Album Artist"}}, Desired: &release},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Track Artist"}}, Desired: &position},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Tags["MUSICBRAINZ_ALBUMARTISTID"]; !reflect.DeepEqual(got, []string{releaseArtistID}) {
		t.Fatalf("release artist MBID = %#v", got)
	}
	if got := result.Tags["MUSICBRAINZ_ARTISTID"]; !reflect.DeepEqual(got, []string{trackArtistID}) {
		t.Fatalf("track artist MBID = %#v", got)
	}
}

func TestProjectTagsValidatesManuallyConstructedEnvelopesAndFinalRequiredTags(t *testing.T) {
	base := TagProjectionInput{
		Release:  ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Album Artist"}}},
		Position: PositionTags{Title: "Song", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Track Artist"}}},
	}
	badScope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{}}
	base.Release.Desired = &badScope
	if _, err := ProjectTags(base); err == nil {
		t.Fatal("expected mismatched envelope scope to be rejected")
	}
	invalidEnvelopes := []librarymetadata.Envelope{
		{
			Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
			Desired: map[string]librarymetadata.FieldValue{
				"genre": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []any{"Jazz", 1}, true),
			},
		},
		{
			Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
			Desired: map[string]librarymetadata.FieldValue{
				"unregistered": librarymetadata.ManualFieldValue(librarymetadata.StateValue, "value", true),
			},
		},
	}
	for _, envelope := range invalidEnvelopes {
		base.Release.Desired = &envelope
		if _, err := ProjectTags(base); err == nil {
			t.Fatal("expected invalid manually constructed envelope to be rejected")
		}
	}
	base.Release.Desired = &librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
		Desired: map[string]librarymetadata.FieldValue{
			"title": librarymetadata.ManualFieldValue(librarymetadata.StateValue, " \t", true),
		},
	}
	if _, err := ProjectTags(base); err == nil {
		t.Fatal("expected an explicitly blank final album title to be rejected")
	}
	base.Release.Desired = nil
	base.Position.Artist = []librarymetadata.ArtistCredit{{ArtistName: " \t", CreditName: " "}}
	if _, err := ProjectTags(base); err == nil {
		t.Fatal("expected blank artist credits to be rejected")
	}
}
