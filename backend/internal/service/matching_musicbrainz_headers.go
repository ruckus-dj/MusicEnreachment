package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
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

// MatchingSearchScopeFromAcceptedFallback adapts one frozen accepted fallback
// search payload to the whole-layout consumer without changing the legacy
// acquisition path. It validates the payload through both the accepted manifest
// and the release header projection, requires them to describe exactly the same
// release identities, and then ranks the complete header set against the frozen
// group captures. The returned slices are independent of the inputs.
func MatchingSearchScopeFromAcceptedFallback(
	kind string, payload json.RawMessage, captures []persistence.IncomingGroupingCapture,
) (persistence.MatchingAcceptedShortlist, error) {
	manifestIDs, err := fallbackSearchReleaseKeys(kind, payload)
	if err != nil {
		return persistence.MatchingAcceptedShortlist{}, fmt.Errorf("validate accepted fallback search manifest: %w", err)
	}
	headers, err := matchingMusicBrainzSearchHeaders(kind, payload)
	if err != nil {
		return persistence.MatchingAcceptedShortlist{}, fmt.Errorf("project accepted fallback search headers: %w", err)
	}
	if err := validateMatchingSearchHeaderKeys(manifestIDs, headers); err != nil {
		return persistence.MatchingAcceptedShortlist{}, err
	}
	members, err := MatchingLayoutRetrievalMembers(captures)
	if err != nil {
		return persistence.MatchingAcceptedShortlist{}, fmt.Errorf("project matching layout retrieval members: %w", err)
	}
	ranked, err := RankMatchingReleaseShortlist(members, headers)
	if err != nil {
		return persistence.MatchingAcceptedShortlist{}, fmt.Errorf("rank accepted fallback search shortlist: %w", err)
	}
	canonical, err := canonicalFallbackJSON(payload)
	if err != nil {
		return persistence.MatchingAcceptedShortlist{}, fmt.Errorf("canonicalize accepted fallback search: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return persistence.MatchingAcceptedShortlist{
		ReleaseIDs:          manifestIDs,
		RankedReleaseIDs:    ranked,
		SearchReceiptDigest: hex.EncodeToString(sum[:]),
	}, nil
}

// validateMatchingSearchHeaderKeys requires the release header projection to
// cover exactly the manifest's accepted release identities. Neither side may
// silently drop a nested recording release or invent an extra release.
func validateMatchingSearchHeaderKeys(manifestIDs []string, headers []MatchingReleaseHeader) error {
	accepted := make(map[string]struct{}, len(manifestIDs))
	for _, id := range manifestIDs {
		accepted[id] = struct{}{}
	}
	if len(accepted) != len(manifestIDs) || len(accepted) != len(headers) {
		return fmt.Errorf("accepted fallback search headers do not match its release identities")
	}
	for _, header := range headers {
		if _, ok := accepted[header.ID]; !ok {
			return fmt.Errorf("accepted fallback search headers do not match its release identities")
		}
		delete(accepted, header.ID)
	}
	return nil
}
