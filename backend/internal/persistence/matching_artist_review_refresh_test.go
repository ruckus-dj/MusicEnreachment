package persistence

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestArtistMatchingReviewProviderDriftKeepsTheTenReleaseScope(t *testing.T) {
	previous := IncomingMatchProviderFence{
		ProviderID: uuid.New(), ProviderSourceID: uuid.New(),
		Namespace: "musicbrainz", Endpoint: "https://musicbrainz.org",
		ConfigurationIdentity: "saved", AcceptedProjectionRevision: "before",
		CacheRevisions: make(map[string]*string),
	}
	next := previous
	next.CacheRevisions = make(map[string]*string)
	for index := range 10 {
		release := uuid.NewString()
		key := "release-lookup:" + release
		previous.ReleaseKeys = append(previous.ReleaseKeys, release)
		revision := fmt.Sprintf("revision-%d", index)
		previous.CacheRevisions[key] = &revision
		next.CacheRevisions[key] = &revision
		if index == 1 || index == 8 {
			fresh := revision + "-refreshed"
			next.CacheRevisions[key] = &fresh
		}
	}
	next.ReleaseKeys = append([]string(nil), previous.ReleaseKeys...)
	if drifted, err := artistMatchingReviewProviderDrifted(previous, next); err != nil || !drifted {
		t.Fatalf("two changed lookups: drifted=%v, error=%v", drifted, err)
	}
	if drifted, err := artistMatchingReviewProviderDrifted(next, next); err != nil || drifted {
		t.Fatalf("rebased lookup fence: drifted=%v, error=%v", drifted, err)
	}
	next.ReleaseKeys[1] = uuid.NewString()
	if _, err := artistMatchingReviewProviderDrifted(previous, next); err == nil {
		t.Fatal("an edition outside the saved shortlist was accepted")
	}
}
