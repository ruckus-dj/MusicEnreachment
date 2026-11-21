package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestMatchingMusicBrainzSearchHeadersUseReleaseEvidence(t *testing.T) {
	headers, err := matchingMusicBrainzSearchHeaders("provider_recording_search", json.RawMessage(`{
		"recordings":[
			{"id":"00000000-0000-0000-0000-000000000003"},
			{"artist-credit":[{"name":"Singer"}],"releases":[
				{"id":"00000000-0000-0000-0000-000000000001","title":"Album",
				 "artist-credit":[{"name":"Various Artists"}],"date":"2024-02-03",
				 "track-count":25,"media":[{"track-count":17}]},
				{"id":"00000000-0000-0000-0000-000000000002","title":"Bonus",
				 "media":[{"track-count":17}]}
			]},
			{"releases":[{"id":"00000000-0000-0000-0000-000000000001","title":"Album",
				"artist-credit":[{"name":"Various Artists"}],"date":"2024-02-03",
				"track-count":25,"media":[{"track-count":8}]}]}
		]
	}`))
	if err != nil || len(headers) != 2 {
		t.Fatalf("expected two distinct release headers, got %+v, %v", headers, err)
	}
	album, bonus := headers[0], headers[1]
	if album.PositionCount == nil || *album.PositionCount != 25 || album.ArtistCredit != "Various Artists" || album.Date != "2024-02-03" {
		t.Fatalf("release-level evidence not preserved: %+v", album)
	}
	if bonus.PositionCount != nil || bonus.ArtistCredit != "" {
		t.Fatalf("unexpected inference from recording or partial media: %+v", bonus)
	}
}

func TestMatchingSearchScopeFromAcceptedFallbackKeepsFullReleaseUniverse(t *testing.T) {
	root := uuid.New()
	capture := func(title string) persistence.IncomingGroupingCapture {
		tags, err := json.Marshal(map[string][]string{
			"ALBUM": {"Alpha"}, "ALBUMARTIST": {"Artist"}, "TITLE": {title},
		})
		if err != nil {
			t.Fatal(err)
		}
		analysisID := uuid.New()
		return persistence.IncomingGroupingCapture{
			VariantID: uuid.New(), WorkID: uuid.New(), LocationID: uuid.New(), RootID: root,
			CaptureAnalysisID: &analysisID, Ready: true,
			RelativePath: title + ".flac", ConfiguredPath: "/music", InventoryPath: "/music",
			Tags: tags,
		}
	}
	captures := []persistence.IncomingGroupingCapture{capture("One"), capture("Two")}
	releaseID := func(index int) string {
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
	}
	releases := make([]map[string]any, 0, 14)
	add := func(index, count int, title string, known bool) {
		release := map[string]any{"id": releaseID(index), "title": title}
		if known {
			release["track-count"] = count
		}
		releases = append(releases, release)
	}
	// Twelve releases share the exact album text. The first has an exact known
	// count, the next two keep an unknown count, and the rest are progressively
	// wider. The final two do not match the captured album text at all.
	add(1, 2, "Alpha", true)
	add(2, 0, "Alpha", false)
	add(3, 0, "Alpha", false)
	for index := 4; index <= 12; index++ {
		add(index, index-1, "Alpha", true)
	}
	add(13, 2, "Elsewhere", true)
	add(14, 2, "Elsewhere", true)
	payload, err := json.Marshal(map[string]any{
		"count": 1, "offset": 0,
		"recordings": []any{map[string]any{"id": releaseID(900), "releases": releases}},
	})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := MatchingSearchScopeFromAcceptedFallback("provider_recording_search", payload, captures)
	if err != nil {
		t.Fatal(err)
	}
	allIDs := make([]string, 14)
	for index := range allIDs {
		allIDs[index] = releaseID(index + 1)
	}
	if !slices.Equal(scope.ReleaseIDs, allIDs) || !slices.IsSorted(scope.ReleaseIDs) {
		t.Fatalf("accepted release universe = %v, want complete sorted %v", scope.ReleaseIDs, allIDs)
	}
	wantShortlist := allIDs[:MatchingReleaseShortlistLimit]
	if len(scope.RankedReleaseIDs) != MatchingReleaseShortlistLimit ||
		!slices.Equal(scope.RankedReleaseIDs, wantShortlist) {
		t.Fatalf("ranked shortlist = %v, want %v", scope.RankedReleaseIDs, wantShortlist)
	}
	if len(scope.SearchReceiptDigest) != 64 {
		t.Fatalf("search receipt digest = %q", scope.SearchReceiptDigest)
	}
	repeated, err := MatchingSearchScopeFromAcceptedFallback("provider_recording_search", payload, captures)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repeated.ReleaseIDs, scope.ReleaseIDs) ||
		!slices.Equal(repeated.RankedReleaseIDs, scope.RankedReleaseIDs) ||
		repeated.SearchReceiptDigest != scope.SearchReceiptDigest {
		t.Fatalf("accepted search scope is not stable: %+v / %+v", scope, repeated)
	}
}
