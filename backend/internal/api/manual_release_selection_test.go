package api

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseSelectedReleaseAcceptsReleaseMBIDAndOfficialURL(t *testing.T) {
	id := uuid.MustParse("63f8a3a4-1bb8-4d2f-a3fd-2c0d1dc8bb0d")
	for _, value := range []string{
		id.String(),
		"https://musicbrainz.org/release/" + id.String(),
	} {
		got, err := parseSelectedRelease(value)
		if err != nil || got != id {
			t.Fatalf("parseSelectedRelease(%q) = %s, %v", value, got, err)
		}
	}
}

func TestParseSelectedReleaseRejectsNonReleaseAndUnsafeURLs(t *testing.T) {
	id := "63f8a3a4-1bb8-4d2f-a3fd-2c0d1dc8bb0d"
	for _, value := range []string{
		"https://musicbrainz.org/release-group/" + id,
		"https://musicbrainz.org/artist/" + id,
		"https://musicbrainz.org/release/" + id + "?x=1",
		"https://musicbrainz.org/release/" + id + "#fragment",
		"https://user@musicbrainz.org/release/" + id,
		"https://musicbrainz.org:443/release/" + id,
		"http://musicbrainz.org/release/" + id,
		"https://musicbrainz.org.evil.example/release/" + id,
	} {
		if got, err := parseSelectedRelease(value); err == nil {
			t.Errorf("parseSelectedRelease(%q) unexpectedly accepted %s", value, got)
		}
	}
}
