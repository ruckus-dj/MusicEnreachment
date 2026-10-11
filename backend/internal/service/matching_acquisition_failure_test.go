package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// TestMatchingAcquisitionFencesFailedProviderDependency verifies that a saved
// snapshot whose registered provider child settled with a terminal failure is
// fenced before any provider cache read, child preparation, or acquisition
// advance. The failed dependency must never be read as a successful step.
func TestMatchingAcquisitionFencesFailedProviderDependency(t *testing.T) {
	repository := &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{
		Revision: 1, Phase: "admitted", FailedProviderDependency: true,
	}}
	provider := &matchingAcquisitionProviderFixture{responses: map[string][]byte{}}
	acquisition := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})

	changed, acquired, err := acquisition.Advance(context.Background(), uuid.New(), 1, 1, persistence.MatchingDeliveryClaim{ExecutionEpoch: 1, RiverAttempt: 1})
	if changed || !acquired || !errors.Is(err, ErrMatchingAcquisitionProviderFailed) {
		t.Fatalf("failed dependency: changed=%t acquired=%t err=%v, want provider-failed sentinel", changed, acquired, err)
	}
	if len(repository.phases) != 0 || len(repository.children) != 0 || len(repository.parked) != 0 || len(provider.prepared) != 0 {
		t.Fatalf("failed dependency performed provider work: phases=%d children=%d parked=%d prepared=%d, want none",
			len(repository.phases), len(repository.children), len(repository.parked), len(provider.prepared))
	}
}

// TestMatchingAcquisitionClassifiesMalformedCatalogPageSentinel pins the
// invalid-response sentinel for catalog pages that the existing envelope test
// only checked for a non-nil error: missing envelope keys, a count that changed
// during pagination, duplicate release ids, and a non-UUID release id. The
// worker relies on this sentinel to fail the delivery with a safe error.
func TestMatchingAcquisitionClassifiesMalformedCatalogPageSentinel(t *testing.T) {
	artistID := "11111111-1111-4111-8111-111111111111"
	duplicateID := "22222222-2222-4222-8222-222222222222"
	cases := []struct {
		name         string
		payload      string
		catalogCount *int
	}{
		{name: "missing envelope keys", payload: `{"releases":[]}`},
		{name: "count changed during pagination", payload: `{"release-count":0,"release-offset":0,"releases":[]}`, catalogCount: matchingAcquisitionFailureIntPtr(5)},
		{name: "duplicate release id", payload: `{"release-count":2,"release-offset":0,"releases":[{"id":"` + duplicateID + `"},{"id":"` + duplicateID + `"}]}`, catalogCount: matchingAcquisitionFailureIntPtr(2)},
		{name: "invalid release id", payload: `{"release-count":1,"release-offset":0,"releases":[{"id":"not-a-uuid"}]}`, catalogCount: matchingAcquisitionFailureIntPtr(1)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository, provider := matchingAcquisitionFailureCatalogFixture(artistID, testCase.catalogCount, []byte(testCase.payload))
			acquisition := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
			changed, acquired, err := acquisition.Advance(context.Background(), uuid.New(), 1, 1, persistence.MatchingDeliveryClaim{ExecutionEpoch: 1, RiverAttempt: 1})
			if changed || !acquired || !errors.Is(err, ErrInvalidMatchingAcquisitionResponse) {
				t.Fatalf("malformed catalog page: changed=%t acquired=%t err=%v, want invalid-response sentinel", changed, acquired, err)
			}
		})
	}
}

func matchingAcquisitionFailureIntPtr(value int) *int { return &value }

// matchingAcquisitionFailureCatalogFixture builds a saved artist_catalog_page
// cursor whose cached browse page is the supplied raw payload, so a single
// advance reads and classifies exactly that page.
func matchingAcquisitionFailureCatalogFixture(artistID string, catalogCount *int, payload []byte) (*matchingAcquisitionRepositoryFixture, *matchingAcquisitionProviderFixture) {
	cacheKey := fmt.Sprintf("artist-release-browse:%s:0:%d", artistID, matchingCatalogPageSize)
	output := map[string]any{
		"artist_mbid":     artistID,
		"next_offset":     0,
		"complete":        false,
		"next_request_id": uuid.New(),
		"next_cache_key":  cacheKey,
	}
	if catalogCount != nil {
		output["catalog_count"] = *catalogCount
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	repository := &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{
		Revision: 1,
		Phase:    persistence.MatchingAcquisitionArtistCatalogPage,
		Input:    json.RawMessage(fmt.Sprintf(`{"artist_mbid":%q,"offset":0,"limit":%d}`, artistID, matchingCatalogPageSize)),
		Output:   encoded,
	}}
	provider := &matchingAcquisitionProviderFixture{responses: map[string][]byte{cacheKey: payload}}
	return repository, provider
}
