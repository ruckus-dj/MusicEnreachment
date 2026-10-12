package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type retrievalAcquisitionRepository struct {
	*matchingAcquisitionRepositoryFixture
	capture  []persistence.MatchingAcquisitionCapturedMember
	phases   []persistence.MatchingAcquisitionPhase
	keys     []string
	started  *persistence.MatchingAcquisitionPhase
	children []persistence.MatchingProviderRequest
	claim    persistence.MatchingDeliveryClaim
	attempt  int
	jobID    int64
	current  *bool
}

func (repository *retrievalAcquisitionRepository) IsMatchingDeliveryCurrent(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim) (bool, error) {
	return repository.current == nil || *repository.current, nil
}

func (repository *retrievalAcquisitionRepository) GetMatchingAcquisitionMetadataCapture(_ context.Context, _ uuid.UUID, _ string) ([]persistence.MatchingAcquisitionCapturedMember, string, bool, error) {
	return repository.capture, "capture-digest", true, nil
}

func (repository *retrievalAcquisitionRepository) GetMatchingAcquisitionCatalogPhases(context.Context, uuid.UUID) ([]persistence.MatchingAcquisitionPhase, error) {
	return repository.phases, nil
}

func (repository *retrievalAcquisitionRepository) AdvanceMatchingAcquisitionForDelivery(_ context.Context, _ persistence.RiverInserter, _ uuid.UUID, _ int, _ int64, _ persistence.MatchingDeliveryClaim, _ int64, phase persistence.MatchingAcquisitionPhase, _ []persistence.MatchingProviderRequest) (bool, error) {
	repository.phases = append(repository.phases, phase)
	return true, nil
}

func (repository *retrievalAcquisitionRepository) GetMatchingAcquisitionCatalogCacheKeys(context.Context, uuid.UUID) ([]string, error) {
	if len(repository.keys) > 0 {
		return repository.keys, nil
	}
	return []string{"artist-release-browse:11111111-1111-4111-8111-111111111111:0:100"}, nil
}

func (repository *retrievalAcquisitionRepository) StartMatchingReleaseHydrationForDelivery(_ context.Context, _ persistence.RiverInserter, _ uuid.UUID, attempt int, jobID int64, claim persistence.MatchingDeliveryClaim, _ int64, phase persistence.MatchingAcquisitionPhase, children []persistence.MatchingProviderRequest) (bool, error) {
	repository.started = &phase
	repository.children = children
	repository.attempt = attempt
	repository.jobID = jobID
	repository.claim = claim
	return true, nil
}

type retrievalAcquisitionProvider struct {
	responses map[string][]byte
	prepared  []persistence.Operation
	readCount map[string]int
	oneShot   bool
}

func (provider *retrievalAcquisitionProvider) CachedResponse(_ context.Context, key string) (*persistence.ProviderCachedResponse, error) {
	if provider.readCount == nil {
		provider.readCount = make(map[string]int)
	}
	provider.readCount[key]++
	if provider.oneShot && provider.readCount[key] > 1 {
		return nil, nil
	}
	payload, ok := provider.responses[key]
	if !ok {
		return nil, fmt.Errorf("missing fixture response for %q", key)
	}
	return &persistence.ProviderCachedResponse{Payload: payload}, nil
}

func (provider *retrievalAcquisitionProvider) PrepareOperation(_ context.Context, kind, identity string, offset, limit int, _ bool) (*persistence.Operation, error) {
	key := "release-lookup:" + identity
	operation := persistence.Operation{ID: uuid.New(), Kind: "provider_" + kind, State: "queued", ProviderCacheKey: &key}
	provider.prepared = append(provider.prepared, operation)
	return &provider.prepared[len(provider.prepared)-1], nil
}

func TestMatchingAcquisitionUnsupportedPhaseIgnoresStaleDeliveryOnly(t *testing.T) {
	for _, test := range []struct {
		name      string
		current   bool
		wantError bool
	}{
		{name: "stale delivery", current: false},
		{name: "active delivery", current: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			intent := persistence.MatchingAcquisitionIntent{SchemaVersion: 1}
			repository := &retrievalAcquisitionRepository{
				matchingAcquisitionRepositoryFixture: &matchingAcquisitionRepositoryFixture{
					snapshot: persistence.MatchingAcquisitionSnapshot{Intent: intent, Revision: 1, Phase: "unsupported_phase"},
				},
				current: &test.current,
			}
			service := NewMatchingAcquisitionService(repository, &retrievalAcquisitionProvider{}, matchingAcquisitionRiverFixture{})
			changed, acquired, err := service.Advance(context.Background(), uuid.New(), 1, 1, persistence.MatchingDeliveryClaim{ExecutionEpoch: 1, RiverAttempt: 1})
			if changed || !acquired || (err != nil) != test.wantError {
				t.Fatalf("Advance() = %t, %t, %v; want false, true, error=%t", changed, acquired, err, test.wantError)
			}
			if test.wantError && err.Error() != "matching acquisition phase is unsupported" {
				t.Fatalf("Advance() error = %v, want unsupported phase error", err)
			}
		})
	}
}

func TestMatchingAcquisitionArtistFitParksWithoutBrowsing(t *testing.T) {
	const artistID = "11111111-1111-4111-8111-111111111111"
	const searchKey = "artist-search:0:100:artist"
	intent := persistence.MatchingAcquisitionIntent{SchemaVersion: 1, InitialProviderInputs: []persistence.MatchingAcquisitionProviderRequest{{
		OperationID: uuid.New(), Kind: "provider_artist_search", CacheKey: searchKey,
		Input: json.RawMessage(`{"schema_version":1,"query":"artist","offset":0,"limit":100}`),
	}}}
	repository := &retrievalAcquisitionRepository{matchingAcquisitionRepositoryFixture: &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{Intent: intent, Revision: 4, Phase: "admitted"}}, capture: []persistence.MatchingAcquisitionCapturedMember{{MemberID: uuid.New(), Tags: json.RawMessage(`{"ALBUMARTIST":["Different Artist"]}`)}}}
	provider := &retrievalAcquisitionProvider{responses: map[string][]byte{searchKey: []byte(`{"artists":[{"id":"` + artistID + `","name":"Unrelated Artist"}]}`)}}
	service := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
	changed, acquired, err := service.Advance(context.Background(), uuid.New(), 2, 55, persistence.MatchingDeliveryClaim{ExecutionEpoch: 6, RiverAttempt: 2})
	if err != nil || !changed || !acquired {
		t.Fatalf("Advance() = %t, %t, %v; want parked transition", changed, acquired, err)
	}
	if len(repository.parked) != 1 || len(provider.prepared) != 0 || len(repository.phases) != 0 {
		t.Fatalf("parked=%d prepared=%d advanced=%d; want policy park without browsing", len(repository.parked), len(provider.prepared), len(repository.phases))
	}
	var output map[string]string
	if err := json.Unmarshal(repository.parked[0].Output, &output); err != nil || output["reason"] != "artist_not_fit" {
		t.Fatalf("park output=%s err=%v; want artist_not_fit", repository.parked[0].Output, err)
	}
}

func TestMatchingAcquisitionArtistFitAllowsMatchingTopArtist(t *testing.T) {
	const artistID = "11111111-1111-4111-8111-111111111111"
	const searchKey = "artist-search:0:100:artist"
	intent := persistence.MatchingAcquisitionIntent{SchemaVersion: 1, InitialProviderInputs: []persistence.MatchingAcquisitionProviderRequest{{
		OperationID: uuid.New(), Kind: "provider_artist_search", CacheKey: searchKey,
		Input: json.RawMessage(`{"schema_version":1,"query":"artist","offset":0,"limit":100}`),
	}}}
	repository := &retrievalAcquisitionRepository{
		matchingAcquisitionRepositoryFixture: &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{Intent: intent, Revision: 4, Phase: "admitted"}},
		capture:                              []persistence.MatchingAcquisitionCapturedMember{{MemberID: uuid.New(), Tags: json.RawMessage(`{"ALBUMARTIST":["The Artist"]}`)}},
	}
	provider := &retrievalAcquisitionProvider{responses: map[string][]byte{searchKey: []byte(`{"artists":[{"id":"` + artistID + `","name":"The Artist"}]}`)}}
	service := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
	changed, acquired, err := service.Advance(context.Background(), uuid.New(), 2, 56, persistence.MatchingDeliveryClaim{ExecutionEpoch: 6, RiverAttempt: 2})
	if err != nil || !changed || !acquired {
		t.Fatalf("Advance() = %t, %t, %v; want browse transition", changed, acquired, err)
	}
	if len(repository.phases) != 1 || repository.phases[0].Phase != persistence.MatchingAcquisitionArtistCatalogPage || len(provider.prepared) != 1 || len(repository.parked) != 0 {
		t.Fatalf("advanced=%d prepared=%d parked=%d", len(repository.phases), len(provider.prepared), len(repository.parked))
	}
}

func TestMatchingAcquisitionCatalogReadyRanksAndAdmitsClaimedTopTen(t *testing.T) {
	const artistID = "11111111-1111-4111-8111-111111111111"
	const searchKey = "artist-release-browse:11111111-1111-4111-8111-111111111111:0:100"
	const initialSearchKey = "artist-search:0:100:Artist"
	const releaseIDFormat = "22222222-2222-4222-8222-%012x"
	releases := make([]map[string]any, 11)
	ids := make([]string, 11)
	for index := range releases {
		ids[index] = fmt.Sprintf(releaseIDFormat, index+1)
		releases[index] = map[string]any{"id": ids[index], "title": "Album", "artist-credit": []map[string]string{{"name": "Artist", "joinphrase": " & "}}, "date": "2001-01-01", "label-info": []map[string]string{{"catalog-number": "CAT-1"}}}
	}
	payload, err := json.Marshal(map[string]any{"release-count": 11, "release-offset": 0, "releases": releases})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	phaseProvenance, _ := json.Marshal([]map[string]string{{"request_id": uuid.New().String(), "cache_key": searchKey, "payload_sha256": fmt.Sprintf("%x", digest[:])}})
	initialSearchProvenance, _ := json.Marshal([]map[string]string{{"request_id": uuid.New().String(), "cache_key": initialSearchKey, "payload_sha256": fmt.Sprintf("%064x", 1)}})
	output, _ := json.Marshal(map[string]any{"artist_mbid": artistID, "complete": true, "catalog_count": 11, "covered_releases": 11, "release_ids": ids})
	phase := persistence.MatchingAcquisitionPhase{Revision: 8, Phase: "catalog_ready", Output: output, Input: json.RawMessage(`{"artist_mbid":"` + artistID + `","offset":0,"limit":100}`), ResponseProvenance: phaseProvenance}
	initialSearch := persistence.MatchingAcquisitionPhase{Revision: 1, Phase: persistence.MatchingAcquisitionArtistCatalogPage, ResponseProvenance: initialSearchProvenance}
	intent := persistence.MatchingAcquisitionIntent{SchemaVersion: 1,
		InitialProviderInputs: []persistence.MatchingAcquisitionProviderRequest{{OperationID: uuid.New(), Kind: "provider_artist_search", CacheKey: initialSearchKey}},
		Targets:               []persistence.MatchingTarget{{GroupID: uuid.New()}}, CapturedMembers: []persistence.MatchingAcquisitionCapturedMember{{MemberID: uuid.New(), Tags: json.RawMessage(`{"ALBUM":["Album"],"ALBUMARTIST":["Artist & Other"],"DATE":["2001-01-01"],"CATALOGNUMBER":["CAT-1"]}`)}}}
	repository := &retrievalAcquisitionRepository{
		matchingAcquisitionRepositoryFixture: &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{Intent: intent, Revision: 8, Phase: "catalog_ready", Output: output}},
		capture:                              intent.CapturedMembers, phases: []persistence.MatchingAcquisitionPhase{initialSearch, phase},
		keys: []string{initialSearchKey, searchKey},
	}
	provider := &retrievalAcquisitionProvider{responses: map[string][]byte{searchKey: payload}, oneShot: true}
	service := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
	claim := persistence.MatchingDeliveryClaim{ExecutionEpoch: 9, RiverAttempt: 3}
	changed, acquired, err := service.Advance(context.Background(), uuid.New(), 3, 71, claim)
	if err != nil || !changed || !acquired {
		t.Fatalf("Advance() = %t, %t, %v; want ranked hydration transition", changed, acquired, err)
	}
	if repository.started == nil || repository.started.Phase != persistence.MatchingAcquisitionReleaseHydration || repository.started.Revision != 9 {
		t.Fatalf("hydration phase=%+v", repository.started)
	}
	if len(repository.children) != 10 || len(provider.prepared) != 10 || repository.attempt != 3 || repository.jobID != 71 || repository.claim != claim {
		t.Fatalf("children=%d prepared=%d delivery=%d/%d/%+v", len(repository.children), len(provider.prepared), repository.attempt, repository.jobID, repository.claim)
	}
	if provider.readCount[searchKey] != 1 {
		t.Fatalf("catalog cache reads for %q = %d, want exactly one", searchKey, provider.readCount[searchKey])
	}
	var hydration struct {
		ReleaseIDs []string `json:"release_ids"`
	}
	if err := json.Unmarshal(repository.started.Input, &hydration); err != nil || len(hydration.ReleaseIDs) != 10 {
		t.Fatalf("hydration input=%s err=%v", repository.started.Input, err)
	}
}

func TestMatchingAcquisitionEmptyCatalogParksFromVerifiedPage(t *testing.T) {
	const artistID = "11111111-1111-4111-8111-111111111111"
	const catalogKey = "artist-release-browse:11111111-1111-4111-8111-111111111111:0:100"
	const initialSearchKey = "artist-search:0:100:Artist"
	payload := []byte(`{"release-count":0,"release-offset":0,"releases":[]}`)
	digest := sha256.Sum256(payload)
	pageProvenance, _ := json.Marshal([]map[string]string{{"request_id": uuid.New().String(), "cache_key": catalogKey, "payload_sha256": fmt.Sprintf("%x", digest[:])}})
	searchProvenance, _ := json.Marshal([]map[string]string{{"request_id": uuid.New().String(), "cache_key": initialSearchKey, "payload_sha256": fmt.Sprintf("%064x", 1)}})
	output, _ := json.Marshal(map[string]any{"artist_mbid": artistID, "complete": true, "catalog_count": 0, "covered_releases": 0, "release_ids": []string{}})
	searchPhase := persistence.MatchingAcquisitionPhase{Revision: 1, Phase: persistence.MatchingAcquisitionArtistCatalogPage, ResponseProvenance: searchProvenance}
	catalogPhase := persistence.MatchingAcquisitionPhase{Revision: 8, Phase: "catalog_ready", Output: output,
		Input: json.RawMessage(`{"artist_mbid":"` + artistID + `","offset":0,"limit":100}`), ResponseProvenance: pageProvenance}
	intent := persistence.MatchingAcquisitionIntent{SchemaVersion: 1,
		InitialProviderInputs: []persistence.MatchingAcquisitionProviderRequest{{OperationID: uuid.New(), Kind: "provider_artist_search", CacheKey: initialSearchKey}},
		Targets:               []persistence.MatchingTarget{{GroupID: uuid.New()}},
		CapturedMembers:       []persistence.MatchingAcquisitionCapturedMember{{MemberID: uuid.New(), Tags: json.RawMessage(`{"ALBUM":["Album"],"ALBUMARTIST":["Artist"]}`)}}}
	repository := &retrievalAcquisitionRepository{
		matchingAcquisitionRepositoryFixture: &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{Intent: intent, Revision: 8, Phase: "catalog_ready", Output: output}},
		capture:                              intent.CapturedMembers,
		phases:                               []persistence.MatchingAcquisitionPhase{searchPhase, catalogPhase},
		keys:                                 []string{initialSearchKey, catalogKey},
	}
	provider := &retrievalAcquisitionProvider{responses: map[string][]byte{catalogKey: payload}, oneShot: true}
	service := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
	claim := persistence.MatchingDeliveryClaim{ExecutionEpoch: 9, RiverAttempt: 3}
	changed, acquired, err := service.Advance(context.Background(), uuid.New(), 3, 72, claim)
	if err != nil || !changed || !acquired {
		t.Fatalf("Advance() = %t, %t, %v; want parked no-candidates transition", changed, acquired, err)
	}
	if len(repository.parked) != 1 || len(repository.children) != 0 || len(provider.prepared) != 0 || provider.readCount[catalogKey] != 1 {
		t.Fatalf("parked=%d children=%d prepared=%d reads=%d; want one park and one verified read", len(repository.parked), len(repository.children), len(provider.prepared), provider.readCount[catalogKey])
	}
	var waiting struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(repository.parked[0].Output, &waiting); err != nil || waiting.State != "waiting_policy" || waiting.Reason != "catalog_no_results" {
		t.Fatalf("park output=%s err=%v; want catalog_no_results manual wait", repository.parked[0].Output, err)
	}
}
