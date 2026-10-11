package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingAcquisitionRepositoryFixture struct {
	snapshot persistence.MatchingAcquisitionSnapshot
	phases   []persistence.MatchingAcquisitionPhase
	children []int
	parked   []persistence.MatchingAcquisitionPhase
}

func (repository *matchingAcquisitionRepositoryFixture) GetMatchingAcquisitionMetadataCapture(context.Context, uuid.UUID, string) ([]persistence.MatchingAcquisitionCapturedMember, string, bool, error) {
	return []persistence.MatchingAcquisitionCapturedMember{{MemberID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"), Tags: json.RawMessage(`{"ALBUMARTIST":["Fixture Artist"]}`)}}, "fixture-capture-digest", true, nil
}

func (repository *matchingAcquisitionRepositoryFixture) ParkMatchingAcquisitionForDelivery(_ context.Context, _ uuid.UUID, _ int, _ int64, _ persistence.MatchingDeliveryClaim, _ int64, phase persistence.MatchingAcquisitionPhase) (bool, error) {
	repository.parked = append(repository.parked, phase)
	return true, nil
}

func (repository *matchingAcquisitionRepositoryFixture) GetMatchingAcquisition(context.Context, uuid.UUID) (persistence.MatchingAcquisitionSnapshot, bool, error) {
	return repository.snapshot, true, nil
}

func (repository *matchingAcquisitionRepositoryFixture) AdvanceMatchingAcquisitionForDelivery(_ context.Context, _ persistence.RiverInserter, _ uuid.UUID, _ int, _ int64, _ persistence.MatchingDeliveryClaim, _ int64, phase persistence.MatchingAcquisitionPhase, children []persistence.MatchingProviderRequest) (bool, error) {
	repository.phases = append(repository.phases, phase)
	repository.children = append(repository.children, len(children))
	repository.snapshot.Revision, repository.snapshot.Phase = phase.Revision, phase.Phase
	repository.snapshot.Input, repository.snapshot.Output = phase.Input, phase.Output
	return true, nil
}

type matchingAcquisitionProviderFixture struct {
	responses map[string][]byte
	prepared  []persistence.Operation
}

func (provider *matchingAcquisitionProviderFixture) CachedResponse(_ context.Context, key string) (*persistence.ProviderCachedResponse, error) {
	payload, exists := provider.responses[key]
	if !exists {
		return nil, fmt.Errorf("no fixture response for %s", key)
	}
	return &persistence.ProviderCachedResponse{Payload: payload}, nil
}

func (provider *matchingAcquisitionProviderFixture) PrepareOperation(_ context.Context, kind, artist string, offset, limit int, _ bool) (*persistence.Operation, error) {
	key := fmt.Sprintf("artist-release-browse:%s:%d:%d", artist, offset, limit)
	operation := persistence.Operation{
		ID: uuid.New(), Kind: "provider_" + kind, State: "queued", ProviderCacheKey: &key,
		InputSnapshot: json.RawMessage(fmt.Sprintf(`{"schema_version":1,"mbid":%q,"offset":%d,"limit":%d}`, artist, offset, limit)),
	}
	provider.prepared = append(provider.prepared, operation)
	return &provider.prepared[len(provider.prepared)-1], nil
}

type matchingAcquisitionRiverFixture struct{}

func (matchingAcquisitionRiverFixture) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, nil
}

func TestMatchingAcquisitionPaginatesAcceptedCatalogToCatalogReady(t *testing.T) {
	artistID := "11111111-1111-4111-8111-111111111111"
	initialKey := "artist-search:0:100:artist"
	intent := persistence.MatchingAcquisitionIntent{SchemaVersion: 1, InitialProviderInputs: []persistence.MatchingAcquisitionProviderRequest{{
		OperationID: uuid.New(), Kind: "provider_artist_search", CacheKey: initialKey,
		Input: json.RawMessage(`{"schema_version":1,"query":"artist","offset":0,"limit":100}`),
	}}}
	provider := &matchingAcquisitionProviderFixture{responses: map[string][]byte{
		initialKey: []byte(`{"artists":[{"id":"` + artistID + `","name":"Fixture Artist"}],"count":1,"offset":0}`),
	}}
	for page := 0; page < 3; page++ {
		offset := page * matchingCatalogPageSize
		count := 201
		length := matchingCatalogPageSize
		if page == 2 {
			length = 1
		}
		releases := make([]map[string]string, length)
		for index := range releases {
			releases[index] = map[string]string{"id": uuid.NewString()}
		}
		payload, err := json.Marshal(map[string]any{"release-count": count, "release-offset": offset, "releases": releases})
		if err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("artist-release-browse:%s:%d:%d", artistID, offset, matchingCatalogPageSize)
		provider.responses[key] = payload
	}
	repository := &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{
		Intent: intent, Phase: "admitted", Input: json.RawMessage(`{"intent_digest":"test"}`), Output: json.RawMessage(`{"state":"admitted"}`),
	}}
	service := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
	runID := uuid.New()
	claim := persistence.MatchingDeliveryClaim{ExecutionEpoch: 1, RiverAttempt: 1}
	for step := 0; step < 4; step++ {
		changed, acquired, err := service.Advance(context.Background(), runID, 1, int64(step+1), claim)
		if err != nil || !acquired || !changed {
			t.Fatalf("advance step %d: changed=%t acquired=%t err=%v", step, changed, acquired, err)
		}
		// The service is reconstructed between deliveries to model process restart.
		service = NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
	}
	if repository.snapshot.Phase != "catalog_ready" || repository.snapshot.Revision != 4 {
		t.Fatalf("terminal acquisition phase/revision=%q/%d, want catalog_ready/4", repository.snapshot.Phase, repository.snapshot.Revision)
	}
	if fmt.Sprint(repository.children) != "[1 1 1 0]" {
		t.Fatalf("children per page=%v, want [1 1 1 0]", repository.children)
	}
	var output struct {
		CatalogCount    int      `json:"catalog_count"`
		CoveredReleases int      `json:"covered_releases"`
		ReleaseIDs      []string `json:"release_ids"`
		Complete        bool     `json:"complete"`
	}
	if err := json.Unmarshal(repository.snapshot.Output, &output); err != nil {
		t.Fatal(err)
	}
	if !output.Complete || output.CatalogCount != 201 || output.CoveredReleases != 201 || len(output.ReleaseIDs) != 201 {
		t.Fatalf("catalog manifest=%+v, want complete coverage of all 201 releases", output)
	}
}

// matchingAcquisitionCatalogFixture builds an admitted snapshot plus a provider
// whose initial artist search is valid, and returns the browse cache key that
// the first advance will request.
func matchingAcquisitionCatalogFixture(artistID string, browsePayload []byte) (*matchingAcquisitionRepositoryFixture, *matchingAcquisitionProviderFixture, string) {
	initialKey := "artist-search:0:100:artist"
	intent := persistence.MatchingAcquisitionIntent{SchemaVersion: 1, InitialProviderInputs: []persistence.MatchingAcquisitionProviderRequest{{
		OperationID: uuid.New(), Kind: "provider_artist_search", CacheKey: initialKey,
		Input: json.RawMessage(`{"schema_version":1,"query":"artist","offset":0,"limit":100}`),
	}}}
	browseKey := fmt.Sprintf("artist-release-browse:%s:0:%d", artistID, matchingCatalogPageSize)
	provider := &matchingAcquisitionProviderFixture{responses: map[string][]byte{
		initialKey: []byte(`{"artists":[{"id":"` + artistID + `","name":"Fixture Artist"}],"count":1,"offset":0}`),
		browseKey:  browsePayload,
	}}
	repository := &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{
		Intent: intent, Phase: "admitted", Input: json.RawMessage(`{"intent_digest":"test"}`), Output: json.RawMessage(`{"state":"admitted"}`),
	}}
	return repository, provider, browseKey
}

func TestMatchingAcquisitionRejectsMalformedCatalogEnvelope(t *testing.T) {
	artistID := "11111111-1111-4111-8111-111111111111"
	releaseID := uuid.NewString()
	cases := []struct {
		name    string
		payload string
	}{
		{name: "search shaped envelope", payload: `{"count":1,"offset":0,"releases":[{"id":"` + releaseID + `"}]}`},
		{name: "missing release-count", payload: `{"release-offset":0,"releases":[]}`},
		{name: "null release-offset", payload: `{"release-count":1,"release-offset":null,"releases":[]}`},
		{name: "negative release-count", payload: `{"release-count":-1,"release-offset":0,"releases":[]}`},
		{name: "missing releases", payload: `{"release-count":1,"release-offset":0}`},
		{name: "null releases", payload: `{"release-count":1,"release-offset":0,"releases":null}`},
		{name: "page overruns count", payload: `{"release-count":0,"release-offset":0,"releases":[{"id":"` + releaseID + `"}]}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository, provider, _ := matchingAcquisitionCatalogFixture(artistID, []byte(testCase.payload))
			service := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
			runID := uuid.New()
			claim := persistence.MatchingDeliveryClaim{ExecutionEpoch: 1, RiverAttempt: 1}
			if _, acquired, err := service.Advance(context.Background(), runID, 1, 1, claim); err != nil || !acquired {
				t.Fatalf("start artist browse: acquired=%t err=%v", acquired, err)
			}
			service = NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
			changed, acquired, err := service.Advance(context.Background(), runID, 1, 2, claim)
			if err == nil || !acquired || changed {
				t.Fatalf("malformed catalog envelope: changed=%t acquired=%t err=%v, want rejection", changed, acquired, err)
			}
		})
	}
}

func TestMatchingAcquisitionAcceptsEmptyCatalog(t *testing.T) {
	artistID := "11111111-1111-4111-8111-111111111111"
	repository, provider, _ := matchingAcquisitionCatalogFixture(artistID, []byte(`{"release-count":0,"release-offset":0,"releases":[]}`))
	service := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
	runID := uuid.New()
	claim := persistence.MatchingDeliveryClaim{ExecutionEpoch: 1, RiverAttempt: 1}
	for step := 0; step < 2; step++ {
		changed, acquired, err := service.Advance(context.Background(), runID, 1, int64(step+1), claim)
		if err != nil || !acquired || !changed {
			t.Fatalf("advance step %d: changed=%t acquired=%t err=%v", step, changed, acquired, err)
		}
		service = NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
	}
	if repository.snapshot.Phase != "catalog_ready" {
		t.Fatalf("empty catalog phase=%q, want catalog_ready", repository.snapshot.Phase)
	}
	if fmt.Sprint(repository.children) != "[1 0]" {
		t.Fatalf("children per page=%v, want [1 0]", repository.children)
	}
	var output struct {
		CatalogCount    int      `json:"catalog_count"`
		CoveredReleases int      `json:"covered_releases"`
		ReleaseIDs      []string `json:"release_ids"`
		Complete        bool     `json:"complete"`
	}
	if err := json.Unmarshal(repository.snapshot.Output, &output); err != nil {
		t.Fatal(err)
	}
	if !output.Complete || output.CatalogCount != 0 || output.CoveredReleases != 0 || len(output.ReleaseIDs) != 0 {
		t.Fatalf("empty catalog manifest=%+v, want complete zero coverage", output)
	}
}
