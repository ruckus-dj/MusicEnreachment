package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestMatchingAcquisitionParksAcceptedEmptyArtistSearch(t *testing.T) {
	searchID := uuid.New()
	const cacheKey = "artist-search:0:100:unknown"
	intent := persistence.MatchingAcquisitionIntent{SchemaVersion: 1, InitialProviderInputs: []persistence.MatchingAcquisitionProviderRequest{{
		OperationID: searchID, Kind: "provider_artist_search", CacheKey: cacheKey,
		Input: json.RawMessage(`{"schema_version":1,"query":"unknown","offset":0,"limit":100}`),
	}}}
	repository := &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{Intent: intent, Revision: 0, Phase: "admitted"}}
	provider := &matchingAcquisitionProviderFixture{responses: map[string][]byte{cacheKey: []byte(`{"artists":[],"count":0,"offset":0}`)}}
	acquisition := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})

	changed, acquired, err := acquisition.Advance(context.Background(), uuid.New(), 1, 3, persistence.MatchingDeliveryClaim{ExecutionEpoch: 1, RiverAttempt: 1})
	if err != nil || !changed || !acquired {
		t.Fatalf("advance: changed=%t acquired=%t err=%v", changed, acquired, err)
	}
	if len(repository.parked) != 1 || len(repository.phases) != 0 || len(provider.prepared) != 0 {
		t.Fatalf("parked=%d advanced=%d child requests=%d; expected policy wait without provider calls", len(repository.parked), len(repository.phases), len(provider.prepared))
	}
	if repository.parked[0].Phase != "waiting_policy" || len(repository.parked[0].ResponseProvenance) == 0 {
		t.Fatalf("parked phase=%+v; expected waiting_policy with accepted-response provenance", repository.parked[0])
	}
}

func TestMatchingAcquisitionClassifiesMalformedAcceptedSearch(t *testing.T) {
	const cacheKey = "artist-search:0:100:malformed"
	intent := persistence.MatchingAcquisitionIntent{SchemaVersion: 1, InitialProviderInputs: []persistence.MatchingAcquisitionProviderRequest{{
		OperationID: uuid.New(), Kind: "provider_artist_search", CacheKey: cacheKey,
		Input: json.RawMessage(`{"schema_version":1,"query":"malformed","offset":0,"limit":100}`),
	}}}
	repository := &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{Intent: intent, Phase: "admitted"}}
	provider := &matchingAcquisitionProviderFixture{responses: map[string][]byte{cacheKey: []byte(`{"artists":[`)}}
	acquisition := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})

	_, _, err := acquisition.Advance(context.Background(), uuid.New(), 1, 3, persistence.MatchingDeliveryClaim{ExecutionEpoch: 1, RiverAttempt: 1})
	if !errors.Is(err, ErrInvalidMatchingAcquisitionResponse) {
		t.Fatalf("advance error=%v; expected invalid accepted-response sentinel", err)
	}
}
