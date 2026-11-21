package service

import (
	"encoding/json"
	"testing"
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
