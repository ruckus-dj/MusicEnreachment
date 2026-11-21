package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

// musicBrainzMatchingReleaseHeader is the release entity shared by artist
// browse, release search, and the releases nested under a recording hit.
// Nested media may contain only the medium on which the recording was found.
// Only a release-level track-count is a usable total.
type musicBrainzMatchingReleaseHeader struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Disambiguation string `json:"disambiguation"`
	Date           string `json:"date"`
	TrackCount     *int   `json:"track-count"`
	ArtistCredit   []struct {
		Name       string `json:"name"`
		JoinPhrase string `json:"joinphrase"`
	} `json:"artist-credit"`
	LabelInfo []struct {
		CatalogNumber string `json:"catalog-number"`
	} `json:"label-info"`
}

func (release musicBrainzMatchingReleaseHeader) shortlistHeader() (MatchingReleaseHeader, error) {
	id, err := canonicalMBID(release.ID)
	if err != nil {
		return MatchingReleaseHeader{}, fmt.Errorf("release header has an invalid MusicBrainz identity")
	}
	var caption strings.Builder
	credits := make([]string, 0, len(release.ArtistCredit))
	for _, item := range release.ArtistCredit {
		if item.Name != "" {
			credits = append(credits, item.Name)
		}
		caption.WriteString(item.Name)
		caption.WriteString(item.JoinPhrase)
	}
	catalogNumbers := make([]string, 0, len(release.LabelInfo))
	for _, label := range release.LabelInfo {
		catalogNumbers = append(catalogNumbers, label.CatalogNumber)
	}
	count := release.TrackCount
	if count != nil && *count < 0 {
		return MatchingReleaseHeader{}, fmt.Errorf("release header has a negative MusicBrainz track count")
	}
	return MatchingReleaseHeader{
		MatchingRetrievalCandidate: MatchingRetrievalCandidate{
			ID: id, Title: release.Title, Disambiguation: release.Disambiguation, Date: release.Date,
			ArtistCredit: caption.String(), ArtistCredits: credits, CatalogNumbers: catalogNumbers,
		},
		PositionCount: count,
	}, nil
}

// matchingMusicBrainzSearchHeaders projects only the release headers present in
// the immutable accepted search envelope. Multiple recording hits can mention
// the same release. Keep the first occurrence in provider response order, as
// the accepted response itself is the frozen evidence, not the mutable graph.
func matchingMusicBrainzSearchHeaders(kind string, payload json.RawMessage) ([]MatchingReleaseHeader, error) {
	var envelope struct {
		Releases   []musicBrainzMatchingReleaseHeader `json:"releases"`
		Recordings []struct {
			Releases []musicBrainzMatchingReleaseHeader `json:"releases"`
		} `json:"recordings"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode MusicBrainz search headers: %w", err)
	}
	releases := envelope.Releases
	switch kind {
	case "provider_release_search":
		if releases == nil {
			return nil, fmt.Errorf("release search is missing its release list")
		}
	case "provider_recording_search":
		if envelope.Recordings == nil {
			return nil, fmt.Errorf("recording search is missing its recording list")
		}
		releases = nil
		for _, hit := range envelope.Recordings {
			releases = append(releases, hit.Releases...)
		}
	default:
		return nil, fmt.Errorf("unsupported MusicBrainz search kind %q", kind)
	}
	headers := make([]MatchingReleaseHeader, 0, len(releases))
	seen := make(map[string]bool)
	for _, release := range releases {
		header, err := release.shortlistHeader()
		if err != nil {
			return nil, err
		}
		if !seen[header.ID] {
			seen[header.ID] = true
			headers = append(headers, header)
		}
	}
	return headers, nil
}
