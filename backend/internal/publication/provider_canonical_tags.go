package publication

import (
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

// ProviderCanonicalTagValues contains only canonical values whose accepted
// source and field evidence were established by persistence.
type ProviderCanonicalTagValues struct {
	ReleaseTitle     *string
	ReleaseCredits   []librarymetadata.ArtistCredit
	PositionTitle    *string
	PositionCredits  []librarymetadata.ArtistCredit
	ReleaseDate      string
	ReleaseMBID      string
	ReleaseGroupMBID string
	OriginalDate     string
	ReleaseType      string
	ReleaseTypes     []string
	Barcode          string
	ReleaseCountry   string
	ReleaseStatus    string
	Labels           []string
	CatalogNumbers   []string
	Genres           []string
	RecordingMBID    string
	ISRCs            []string
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
	setProviderStringTag(tags, "DATE", values.ReleaseDate)
	setProviderStringTag(tags, "MUSICBRAINZ_ALBUMID", values.ReleaseMBID)
	setProviderStringTag(tags, "MUSICBRAINZ_RELEASEGROUPID", values.ReleaseGroupMBID)
	setProviderStringTag(tags, "ORIGINALDATE", values.OriginalDate)
	setProviderStringTag(tags, "RELEASETYPE", values.ReleaseType)
	setProviderStringTag(tags, "BARCODE", values.Barcode)
	setProviderStringTag(tags, "RELEASECOUNTRY", values.ReleaseCountry)
	setProviderStringTag(tags, "RELEASESTATUS", values.ReleaseStatus)
	setProviderStringTag(tags, "MUSICBRAINZ_TRACKID", values.RecordingMBID)
	setProviderListTag(tags, "RELEASETYPES", values.ReleaseTypes)
	setProviderListTag(tags, "LABEL", values.Labels)
	setProviderListTag(tags, "CATALOGNUMBER", values.CatalogNumbers)
	setProviderListTag(tags, "GENRE", values.Genres)
	setProviderListTag(tags, "ISRC", values.ISRCs)
	if len(values.ReleaseCredits) != 0 {
		setCredits(tags, fields, "album_artist_credit", "ALBUMARTIST", "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID", values.ReleaseCredits)
	}
	if len(values.PositionCredits) != 0 {
		setCredits(tags, fields, "artist_credit", "ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID", values.PositionCredits)
	}
	return tags
}

func setProviderStringTag(tags map[string][]string, key, value string) {
	if strings.TrimSpace(value) != "" {
		tags[key] = []string{value}
	}
}

func setProviderListTag(tags map[string][]string, key string, values []string) {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			filtered = append(filtered, value)
		}
	}
	if len(filtered) != 0 {
		tags[key] = filtered
	}
}
