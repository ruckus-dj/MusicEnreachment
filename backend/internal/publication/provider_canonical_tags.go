package publication

import "github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"

// ProviderCanonicalTagValues contains only canonical values whose accepted
// source and field evidence were established by persistence.
type ProviderCanonicalTagValues struct {
	ReleaseTitle    *string
	ReleaseCredits  []librarymetadata.ArtistCredit
	PositionTitle   *string
	PositionCredits []librarymetadata.ArtistCredit
}

// RenderProviderCanonicalTagValues renders supplied canonical values through
// the same credit formatter used by ordinary publication tag projection.
func RenderProviderCanonicalTagValues(values ProviderCanonicalTagValues) map[string][]string {
	tags := make(map[string][]string)
	fields := make(map[string]TagFieldResult)
	if values.ReleaseTitle != nil && *values.ReleaseTitle != "" {
		tags["ALBUM"] = []string{*values.ReleaseTitle}
	}
	if values.PositionTitle != nil && *values.PositionTitle != "" {
		tags["TITLE"] = []string{*values.PositionTitle}
	}
	if len(values.ReleaseCredits) != 0 {
		setCredits(tags, fields, "album_artist_credit", "ALBUMARTIST", "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID", values.ReleaseCredits)
	}
	if len(values.PositionCredits) != 0 {
		setCredits(tags, fields, "artist_credit", "ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID", values.PositionCredits)
	}
	return tags
}
