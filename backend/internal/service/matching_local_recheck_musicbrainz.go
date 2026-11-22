package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/anyascii/go"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
)

const (
	matchingLocalRecheckArtistPageSize   = 100
	matchingLocalRecheckArtistFitMinimum = 0.70
)

// ErrMatchingLocalRecheckMusicBrainzIncomplete reports accepted MusicBrainz
// retrieval evidence that cannot yet support a layout recheck. It is distinct
// from a definitive no-result and from a provider outage.
var ErrMatchingLocalRecheckMusicBrainzIncomplete = errors.New("matching Local recheck MusicBrainz retrieval is incomplete")

// MatchingLocalRecheckMusicBrainzRetrieval is the exact MusicBrainz recovery
// evidence for one saved Local recheck. NoResult is a definitive answer for the
// artist branch; transport, malformed, or incomplete evidence is returned as an
// error instead.
type MatchingLocalRecheckMusicBrainzRetrieval struct {
	Snapshot   persistence.ProviderMatchingSnapshot
	Shortlist  []string
	CacheKeys  []string
	SearchKeys map[uuid.UUID]MatchingSearchKeys
	Evidence   []persistence.MatchingProviderLayoutEvidence
	NoResult   bool
	Reason     string
}

const (
	matchingLocalRecheckArtistNoResults  = "artist_search_no_results"
	matchingLocalRecheckArtistNotFit     = "artist_not_fit"
	matchingLocalRecheckCatalogNoResults = "catalog_no_results"
)

// RetrieveMatchingLocalRecheckMusicBrainz reruns artist-catalog or artistless
// retrieval on the saved captures. The accepted responses
// are returned through the normalized source-scoped snapshot and exact cache
// keys so the consuming transaction can recheck them. It never applies a
// layout and never treats a manual preview as authority.
func (service *ProviderFetchService) RetrieveMatchingLocalRecheckMusicBrainz(
	ctx context.Context,
	captures []persistence.IncomingGroupingCapture,
	threshold float64,
) (MatchingLocalRecheckMusicBrainzRetrieval, error) {
	if service == nil || service.registry == nil || service.cache == nil || service.factory == nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("MusicBrainz provider fetch service is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, err
	}
	members, err := MatchingLayoutRetrievalMembers(captures)
	if err != nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("prepare Local recheck retrieval members: %w", err)
	}
	query, err := matchingLocalRecheckArtistQuery(members)
	if err != nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, err
	}
	if query == "" {
		return service.retrieveArtistlessLocalRecheck(ctx, captures, members, threshold)
	}
	return service.retrieveMatchingArtistCatalog(ctx, members, query)
}

func (service *ProviderFetchService) retrieveMatchingArtistCatalog(ctx context.Context, members []persistence.MatchingAcquisitionCapturedMember, query string) (MatchingLocalRecheckMusicBrainzRetrieval, error) {
	search, err := service.SearchArtists(ctx, query, 0, 1)
	if err != nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("search MusicBrainz artist: %w", err)
	}
	searchKey := fmt.Sprintf("artist-search:0:1:%s", strings.TrimSpace(query))
	expected := map[string][]byte{searchKey: bytes.Clone(search.Raw)}
	var artists struct {
		Values *[]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artists"`
	}
	if json.Unmarshal(search.Raw, &artists) != nil || artists.Values == nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: accepted artist search is malformed", ErrMatchingLocalRecheckMusicBrainzIncomplete)
	}
	if len(*artists.Values) == 0 {
		return service.finishLocalRecheckRetrieval(ctx, nil, expected, matchingLocalRecheckArtistNoResults)
	}
	top := (*artists.Values)[0]
	if strings.TrimSpace(top.Name) == "" {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: accepted top artist has no name", ErrMatchingLocalRecheckMusicBrainzIncomplete)
	}
	artistID, err := canonicalMBID(top.ID)
	if err != nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: accepted top artist has an invalid identity", ErrMatchingLocalRecheckMusicBrainzIncomplete)
	}
	if err := canonicalArtistCursor(artistID, 0); err != nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: accepted top artist cursor is invalid", ErrMatchingLocalRecheckMusicBrainzIncomplete)
	}
	fit, available, err := MatchingArtistFit(members, top.Name)
	if err != nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("score MusicBrainz artist fit: %w", err)
	}
	if !available || fit < matchingLocalRecheckArtistFitMinimum {
		return service.finishLocalRecheckRetrieval(ctx, nil, expected, matchingLocalRecheckArtistNotFit)
	}

	headers := make([]MatchingReleaseHeader, 0)
	seen := make(map[string]struct{})
	var total *int
	for offset := 0; ; {
		browse, err := service.BrowseReleasesByArtist(ctx, artistID, offset, matchingLocalRecheckArtistPageSize)
		if err != nil {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("browse MusicBrainz artist catalog: %w", err)
		}
		key := fmt.Sprintf("artist-release-browse:%s:%d:%d", artistID, offset, matchingLocalRecheckArtistPageSize)
		expected[key] = bytes.Clone(browse.Raw)
		var page struct {
			Count    *int                                `json:"release-count"`
			Offset   *int                                `json:"release-offset"`
			Releases *[]musicBrainzMatchingReleaseHeader `json:"releases"`
		}
		if json.Unmarshal(browse.Raw, &page) != nil || page.Count == nil || page.Offset == nil || page.Releases == nil ||
			*page.Count < 0 || *page.Offset != offset || len(*page.Releases) > matchingLocalRecheckArtistPageSize ||
			len(*page.Releases) == 0 && *page.Offset < *page.Count ||
			*page.Offset+len(*page.Releases) > *page.Count {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: accepted artist catalog page is malformed", ErrMatchingLocalRecheckMusicBrainzIncomplete)
		}
		if total != nil && *total != *page.Count {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: accepted artist catalog count changed", ErrMatchingLocalRecheckMusicBrainzIncomplete)
		}
		total = page.Count
		for _, release := range *page.Releases {
			header, err := release.shortlistHeader()
			if err != nil {
				return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: %v", ErrMatchingLocalRecheckMusicBrainzIncomplete, err)
			}
			if _, duplicate := seen[header.ID]; duplicate {
				return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: accepted artist catalog contains duplicate releases", ErrMatchingLocalRecheckMusicBrainzIncomplete)
			}
			seen[header.ID] = struct{}{}
			headers = append(headers, header)
		}
		offset = *page.Offset + len(*page.Releases)
		if offset >= *page.Count {
			break
		}
	}
	if len(headers) == 0 {
		return service.finishLocalRecheckRetrieval(ctx, nil, expected, matchingLocalRecheckCatalogNoResults)
	}
	shortlist, err := RankMatchingReleaseShortlist(members, headers)
	if err != nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("rank MusicBrainz release shortlist: %w", err)
	}
	return service.finishLocalRecheckRetrieval(ctx, shortlist, expected, "")
}

func (service *ProviderFetchService) finishLocalRecheckRetrieval(ctx context.Context, shortlist []string, expected map[string][]byte, noResult string) (MatchingLocalRecheckMusicBrainzRetrieval, error) {
	shortlist = append([]string(nil), shortlist...)
	sort.Strings(shortlist)
	keys := make([]string, 0, len(expected)+len(shortlist))
	for key := range expected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	evidence := persistence.MatchingProviderLayoutEvidence{SchemaVersion: persistence.MatchingProviderLayoutEvidenceVersion}
	for _, key := range keys {
		if strings.HasPrefix(key, "artist-release-browse:") {
			evidence.PageKeys = append(evidence.PageKeys, key)
		} else {
			evidence.QueryKeys = append(evidence.QueryKeys, key)
		}
	}
	for _, releaseID := range shortlist {
		lookup, err := service.LookupRelease(ctx, releaseID)
		if err != nil {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("look up MusicBrainz release %s: %w", releaseID, err)
		}
		key := "release-lookup:" + releaseID
		keys = append(keys, key)
		evidence.HydrationKeys = append(evidence.HydrationKeys, key)
		expected[key] = bytes.Clone(lookup.Raw)
	}
	config, err := service.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("read MusicBrainz provider configuration: %w", err)
	}
	if config.Identity == "" {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, ErrMusicBrainzConfigurationMissing
	}
	snapshot, err := service.cache.GetMusicBrainzMatchingSnapshot(
		ctx, musicBrainzEndpoint(config), config.Identity, shortlist, keys,
	)
	if err != nil {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("read MusicBrainz recovery snapshot: %w", err)
	}
	for key, want := range expected {
		response := snapshot.Responses[key]
		var actual []byte
		if response != nil {
			actual, err = canonicalFallbackJSON(response.Payload)
		}
		frozen, frozenErr := canonicalFallbackJSON(want)
		if response == nil || err != nil || frozenErr != nil || !bytes.Equal(actual, frozen) {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: accepted response %q changed", ErrMatchingLocalRecheckMusicBrainzIncomplete, key)
		}
	}
	if len(snapshot.Releases) != len(shortlist) {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: shortlisted releases have no complete hydrated tracklist", ErrMatchingLocalRecheckMusicBrainzIncomplete)
	}
	hydrated := make(map[string]struct{}, len(snapshot.Releases))
	for _, release := range snapshot.Releases {
		if !release.Complete {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: shortlisted release %q is incomplete", ErrMatchingLocalRecheckMusicBrainzIncomplete, release.ProviderKey)
		}
		hydrated[release.ProviderKey] = struct{}{}
	}
	for _, releaseID := range shortlist {
		if _, ok := hydrated[releaseID]; !ok {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, fmt.Errorf("%w: shortlisted release %q is missing", ErrMatchingLocalRecheckMusicBrainzIncomplete, releaseID)
		}
	}
	evidence.ProviderSourceID, evidence.ProviderID = snapshot.Source.ID, snapshot.Source.ProviderID
	evidence.Namespace, evidence.Endpoint = snapshot.Source.Namespace, snapshot.Source.Endpoint
	evidence.ConfigurationIdentity, evidence.AcceptedProjectionRevision = config.Identity, snapshot.AcceptedProjectionRevision
	evidence.ReleaseKeys = shortlist
	return MatchingLocalRecheckMusicBrainzRetrieval{
		Snapshot: snapshot, Shortlist: shortlist, CacheKeys: keys, NoResult: noResult != "",
		Reason: noResult, Evidence: []persistence.MatchingProviderLayoutEvidence{evidence},
	}, nil
}

func (service *ProviderFetchService) retrieveArtistlessLocalRecheck(ctx context.Context, captures []persistence.IncomingGroupingCapture, members []persistence.MatchingAcquisitionCapturedMember, threshold float64) (MatchingLocalRecheckMusicBrainzRetrieval, error) {
	tags := make([]map[string][]string, 0, len(members))
	for _, member := range members {
		var values map[string][]string
		if err := json.Unmarshal(member.Tags, &values); err != nil {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, err
		}
		tags = append(tags, values)
	}
	plan, artistBearing := policy.PlanFallbackQueries(tags)
	if artistBearing {
		return MatchingLocalRecheckMusicBrainzRetrieval{}, ErrMatchingLocalRecheckMusicBrainzIncomplete
	}
	history := make([]persistence.MatchingProviderLayoutEvidence, 0, len(plan.Stages))
	for _, stage := range plan.Stages {
		kind, prefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
		if !ok {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, ErrMatchingLocalRecheckMusicBrainzIncomplete
		}
		search := service.SearchReleases
		if kind == "provider_recording_search" {
			search = service.SearchRecordings
		}
		response, err := search(ctx, stage.Query, stage.Offset, stage.Limit)
		if err != nil {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, err
		}
		key := fmt.Sprintf("%s:%d:%d:%s", prefix, stage.Offset, stage.Limit, stage.Query)
		scope, err := MatchingSearchScopeFromAcceptedFallback(kind, response.Raw, captures)
		if err != nil {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, err
		}
		result, err := service.finishLocalRecheckRetrieval(ctx, scope.RankedReleaseIDs, map[string][]byte{key: response.Raw}, "")
		if err != nil {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, err
		}
		history = append(history, result.Evidence...)
		result.SearchKeys = make(map[uuid.UUID]MatchingSearchKeys, len(members))
		for _, member := range members {
			if kind == "provider_recording_search" {
				result.SearchKeys[member.MemberID] = MatchingSearchKeys{Recording: key}
			} else {
				result.SearchKeys[member.MemberID] = MatchingSearchKeys{Release: key}
			}
		}
		layout, err := CalculateMatchingLayoutStage(ctx, captures, result.Shortlist, result.Snapshot, result.SearchKeys, nil, threshold)
		if err != nil {
			return MatchingLocalRecheckMusicBrainzRetrieval{}, err
		}
		switch layout.Decision.Status {
		case MatchingLayoutSelectionIndeterminate:
			return MatchingLocalRecheckMusicBrainzRetrieval{}, ErrMatchingLocalRecheckMusicBrainzIncomplete
		case MatchingLayoutSelectionSelected:
			result.Evidence = history
			return result, nil
		}
	}
	return MatchingLocalRecheckMusicBrainzRetrieval{NoResult: true, Reason: "artistless_search_exhausted", Evidence: history}, nil
}

// matchingLocalRecheckArtistQuery derives the same album-artist-first query
// terms used by automatic reconciliation, after the saved encoding corrections
// already applied by MatchingLayoutRetrievalMembers.
func matchingLocalRecheckArtistQuery(members []persistence.MatchingAcquisitionCapturedMember) (string, error) {
	terms := make([]string, 0)
	for _, member := range members {
		var tags map[string][]string
		if err := json.Unmarshal(member.Tags, &tags); err != nil {
			return "", fmt.Errorf("decode Local recheck capture tags: %w", err)
		}
		albumArtist, present := sourceTagValues(tags, "ALBUMARTIST", "ALBUM ARTIST", "ALBUM_ARTIST")
		values := albumArtist
		if !present {
			values, _ = sourceTagValues(tags, "ARTIST")
		}
		for _, value := range values {
			if !hasNonWhitespaceRune(value) {
				continue
			}
			terms = append(terms, value, canonicalMatchingText(value), canonicalMatchingText(anyascii.Transliterate(value)))
		}
	}
	terms = stableUniqueMatchingValues(terms)
	if len(terms) == 0 {
		return "", nil
	}
	quoted := make([]string, len(terms))
	for i, term := range terms {
		quoted[i] = `artist:"` + escapeLucenePhrase(term) + `"`
	}
	if len(quoted) == 1 {
		return quoted[0], nil
	}
	return "(" + strings.Join(quoted, " OR ") + ")", nil
}
