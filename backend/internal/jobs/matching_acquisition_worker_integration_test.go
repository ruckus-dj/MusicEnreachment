//go:build integration

package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// This drives real durable provider and matching deliveries against a local
// MusicBrainz-compatible HTTP server. In particular, browse pagination is
// driven by accepted cache responses and persisted delivery wakes, not by a
// fake provider or an in-memory acquisition fixture.
func TestMatchingAcquisitionWorkerPaginatesArtistCatalogPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAnalysisDispatchFixtureWithMetadata(t)
	database := fixture.database
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const artistID = "33333333-3333-4333-8333-333333333333"
	var requestMu sync.Mutex
	var artistSearches int
	var browseOffsets []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.TrimSuffix(r.URL.Path, "/") == "/ws/2/artist" && r.URL.Query().Get("query") == "artist:fixture":
			requestMu.Lock()
			artistSearches++
			requestMu.Unlock()
			_, _ = w.Write([]byte(`{"count":1,"offset":0,"created":"2026-01-01T00:00:00Z","artists":[{"id":"` + artistID + `","name":"Fixture Artist","disambiguation":"fixture","unknown_artist_field":"preserve-artist-search"}]}`))
		case strings.TrimSuffix(r.URL.Path, "/") == "/ws/2/release" && r.URL.Query().Get("artist") == artistID:
			offset := 0
			if _, err := fmt.Sscanf(r.URL.Query().Get("offset"), "%d", &offset); err != nil {
				t.Errorf("parse artist browse offset %q: %v", r.URL.Query().Get("offset"), err)
			}
			limit := 0
			if _, err := fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit); err != nil {
				t.Errorf("parse artist browse limit %q: %v", r.URL.Query().Get("limit"), err)
			}
			requestMu.Lock()
			browseOffsets = append(browseOffsets, offset)
			requestMu.Unlock()
			if limit != 100 || offset != 0 && offset != 100 {
				t.Errorf("artist browse requested offset=%d limit=%d; want offset 0 or 100 with limit 100", offset, limit)
			}
			count := 101
			start, end := offset+1, offset+100
			if offset == 100 {
				count, start, end = 101, 101, 101
			}
			var releases strings.Builder
			for index := start; index <= end; index++ {
				if index > start {
					releases.WriteByte(',')
				}
				releaseID := fmt.Sprintf("%08x-4444-4444-8444-%012x", index, index)
				fmt.Fprintf(&releases, `{"id":%q,"title":"Fixture Release %03d","date":"2001","disambiguation":"sparse catalog entry","unknown_release_field":"preserve-release-%03d"}`, releaseID, index, index)
			}
			_, _ = fmt.Fprintf(w, `{"release-count":%d,"release-offset":%d,"releases":[%s],"unknown_browse_field":"preserve-artist-release-browse-%d"}`, count, offset, releases.String(), offset)
		default:
			t.Errorf("unexpected MusicBrainz request: %s", r.URL.String())
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	repository := persistence.NewSetupManagerRepository(database)
	registry := settings.New(persistence.NewSettingsRepository(database), nil)
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", server.URL+"/ws/2"); err != nil {
		t.Fatalf("save MusicBrainz test endpoint: %v", err)
	}
	cache := persistence.NewProviderCacheRepository(database)
	factory := service.NewProviderFactory(registry, nil, nil, &http.Client{Timeout: 5 * time.Second}, nil)
	fetch := service.NewProviderFetchService(registry, factory, cache)
	operations := service.NewOperations(repository)
	providerWorker := NewProviderFetchWorker(fetch, operations)
	client, listenerPool, err := PrepareWithWorkers(ctx, fixture.databaseURL, database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, &MatchingEvaluationWorker{})
		river.AddWorker(workers, providerWorker)
	})
	if err != nil {
		t.Fatalf("prepare River with matching and provider workers: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	operations = service.NewOperationsWithRiver(repository, client)
	providerWorker.operations = operations

	// Use source-analysis output to form the real staged group, as production
	// matching does; no hand-written source rows or provider graph fixtures.
	analysis := fixture.startWithMetadata(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *analysis.RiverJobID)
	// The analysis fixture's River client also consumes the default and matching
	// queues. Stop it so it cannot claim the provider and matching deliveries this
	// test drives manually below; the helper is graceful and idempotent, so the
	// fixture's own cleanup stop and later listener-pool close remain safe.
	stopRiverClient(t, fixture.client)
	grouping := persistence.NewIncomingGroupingRepository(database)
	groups := service.NewIncomingGroups(grouping)
	if err := groups.Refresh(ctx); err != nil {
		t.Fatalf("refresh groups from analyzed source: %v", err)
	}
	groupSnapshot, err := groups.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groupSnapshot.Groups) != 1 || len(groupSnapshot.Groups[0].Members) != 1 {
		t.Fatalf("staged groups = %+v; want one ready group with one member", groupSnapshot.Groups)
	}
	group := groupSnapshot.Groups[0]
	memberID := group.Members[0]
	var groupingEpoch int64
	if err := database.NewRaw(`SELECT revision FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &groupingEpoch); err != nil {
		t.Fatal(err)
	}

	// Prepare the exact finite-plan cache identities through the production
	// provider service. They are present only for the legacy plan contract; the
	// acquisition path must park before finite evaluation is reached.
	fetch = fetch.WithDurableOperations(repository, client)
	providerWorker.fetch = fetch
	artistSearch, err := fetch.PrepareOperation(ctx, "artist_search", "artist:fixture", 0, 1, false)
	if err != nil {
		t.Fatalf("prepare initial artist search: %v", err)
	}
	releaseSearch, err := fetch.PrepareOperation(ctx, "release_search", "release:fixture", 0, 100, false)
	if err != nil {
		t.Fatalf("prepare finite-plan release cache identity: %v", err)
	}
	recordingSearch, err := fetch.PrepareOperation(ctx, "recording_search", "recording:fixture", 0, 100, false)
	if err != nil {
		t.Fatalf("prepare finite-plan recording cache identity: %v", err)
	}
	planProviderRequest, err := json.Marshal(struct {
		Kind                          string          `json:"kind"`
		Snapshot                      json.RawMessage `json:"snapshot"`
		CacheKey                      string          `json:"cache_key"`
		ExplicitRefresh               bool            `json:"explicit_refresh"`
		ProviderSourceID              uuid.UUID       `json:"provider_source_id"`
		EndpointConfigurationIdentity string          `json:"endpoint_configuration_identity"`
		ProviderID                    uuid.UUID       `json:"provider_id"`
	}{artistSearch.Kind, artistSearch.InputSnapshot, *artistSearch.ProviderCacheKey, false, *artistSearch.ProviderSourceID, *artistSearch.ProviderConfigurationIdentity, *artistSearch.ProviderID})
	if err != nil {
		t.Fatal(err)
	}
	targets := []persistence.MatchingTarget{{GroupID: group.ID, Revision: group.Revision}}
	queryScope := service.MatchingEvaluationQueryScope{
		GroupIDs:          []uuid.UUID{group.ID},
		ReleaseKeysByFile: map[uuid.UUID][]string{memberID: {}},
		SearchKeysByFile: map[uuid.UUID]service.MatchingSearchKeys{memberID: {
			Release: *releaseSearch.ProviderCacheKey, Recording: *recordingSearch.ProviderCacheKey,
		}},
	}
	plan, err := json.Marshal(struct {
		SchemaVersion        int                                  `json:"schema_version"`
		GroupingEpoch        int64                                `json:"grouping_epoch"`
		Targets              []persistence.MatchingTarget         `json:"targets"`
		PolicyVersion        string                               `json:"policy_version"`
		EvaluationQueryScope service.MatchingEvaluationQueryScope `json:"evaluation_query_scope"`
		ProviderRequests     []json.RawMessage                    `json:"provider_requests"`
	}{2, groupingEpoch, targets, service.MatchingPolicyVersion, queryScope, []json.RawMessage{planProviderRequest}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(struct {
		SchemaVersion int             `json:"schema_version"`
		Plan          json.RawMessage `json:"plan"`
	}{1, plan})
	if err != nil {
		t.Fatal(err)
	}
	parentID := uuid.New()
	parent := &persistence.Operation{ID: parentID, Kind: (service.MatchingEvaluationOperationArgs{}).Kind(), State: "queued", Stage: "waiting_provider", InputSnapshot: snapshot}
	admitted, err := repository.AdmitMatchingRun(ctx, client, persistence.MatchingAdmission{
		AdmissionKey: "matching-acquisition-http-paginated-worker", GroupingEpoch: groupingEpoch,
		Targets: targets, Plan: plan, PolicyVersion: service.MatchingPolicyVersion,
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Providers:                []persistence.MatchingProviderRequest{{Operation: artistSearch, Args: service.ProviderFetchOperationArgs{OperationID: artistSearch.ID}}},
		AcquisitionIntentVersion: 1,
	})
	if err != nil {
		t.Fatalf("admit matching acquisition run: %v", err)
	}
	if admitted.RiverJobID == nil || artistSearch.RiverJobID == nil {
		t.Fatal("matching admission did not persist initial River deliveries")
	}

	evaluator := service.NewMatchingEvaluationService(groups, grouping, cache,
		persistence.NewIncomingMatchCandidateRepository(database), registry)
	acquisition := service.NewMatchingAcquisitionService(repository, fetch, client)
	matchingWorker := NewMatchingEvaluationWorker(repository, evaluator, operations).WithAcquisition(acquisition)

	runMatchingDelivery := func(jobID int64) {
		t.Helper()
		setMatchingRiverJobRunning(t, ctx, database, jobID)
		if err := matchingWorker.Work(ctx, &river.Job[service.MatchingEvaluationOperationArgs]{
			JobRow: &rivertype.JobRow{ID: jobID, Attempt: 1},
			Args:   service.MatchingEvaluationOperationArgs{OperationID: parentID},
		}); err != nil {
			t.Fatalf("run matching delivery %d: %v", jobID, err)
		}
	}
	runProviderDelivery := func(operationID uuid.UUID) *persistence.Operation {
		t.Helper()
		operation, err := repository.GetOperation(ctx, operationID)
		if err != nil {
			t.Fatal(err)
		}
		if operation.RiverJobID == nil {
			t.Fatalf("provider operation %s has no River delivery", operationID)
		}
		if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{
			JobRow: &rivertype.JobRow{ID: *operation.RiverJobID, Attempt: operation.Attempt},
			Args:   service.ProviderFetchOperationArgs{OperationID: operationID},
		}); err != nil {
			t.Fatalf("run provider delivery %s: %v", operationID, err)
		}
		completed, err := repository.GetOperation(ctx, operationID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.State != "succeeded" || completed.Stage != "provider_response_saved" {
			t.Fatalf("provider operation %s state/stage=%s/%s; want succeeded/provider_response_saved", operationID, completed.State, completed.Stage)
		}
		return completed
	}
	nextWake := func() int64 {
		t.Helper()
		var jobID int64
		if err := database.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL ORDER BY river_job_id DESC LIMIT 1`, parentID).Scan(ctx, &jobID); err != nil {
			t.Fatalf("read pending matching wake: %v", err)
		}
		return jobID
	}
	assertParentWaiting := func(stage string) {
		t.Helper()
		operation, err := repository.GetOperation(ctx, parentID)
		if err != nil {
			t.Fatal(err)
		}
		if operation.State == "succeeded" || operation.State == "failed" || operation.Stage != stage {
			t.Fatalf("parent state/stage=%s/%s; want nonterminal stage %s", operation.State, operation.Stage, stage)
		}
	}

	// The first matching delivery consumes its already-pending provider wake and
	// must not begin acquisition before the artist search has been accepted.
	runMatchingDelivery(*admitted.RiverJobID)
	assertParentWaiting("waiting_provider")
	var initialConsumed bool
	if err := database.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, *admitted.RiverJobID).Scan(ctx, &initialConsumed); err != nil || !initialConsumed {
		t.Fatalf("initial pending delivery consumed=%t err=%v; want consumed", initialConsumed, err)
	}
	runProviderDelivery(artistSearch.ID)
	runMatchingDelivery(nextWake())
	assertParentWaiting("waiting_provider")

	acquisitionSnapshot, exists, err := repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || acquisitionSnapshot.Phase != persistence.MatchingAcquisitionArtistCatalogPage {
		t.Fatalf("after artist settlement acquisition phase=%q exists=%t err=%v; want artist catalog page", acquisitionSnapshot.Phase, exists, err)
	}
	// Read the first browse child from its persisted acquisition input, not from
	// assumptions about generated IDs or in-memory service state.
	var firstBrowseID uuid.UUID
	if err := database.NewRaw(`SELECT id FROM operation WHERE kind='provider_artist_release_browse' AND id IN (SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?) ORDER BY created_at LIMIT 1`, parentID).Scan(ctx, &firstBrowseID); err != nil {
		t.Fatalf("read first browse operation: %v", err)
	}
	runProviderDelivery(firstBrowseID)
	// Simulate a worker restart after the first page fetch. The continuation must
	// come entirely from the persisted cursor and the newly registered child.
	matchingWorker = NewMatchingEvaluationWorker(repository, evaluator, operations).
		WithAcquisition(service.NewMatchingAcquisitionService(repository, fetch, client))
	runMatchingDelivery(nextWake())
	assertParentWaiting("waiting_provider")

	acquisitionSnapshot, exists, err = repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || acquisitionSnapshot.Phase != persistence.MatchingAcquisitionArtistCatalogPage {
		t.Fatalf("after first browse page acquisition phase=%q exists=%t err=%v", acquisitionSnapshot.Phase, exists, err)
	}
	var secondBrowseID uuid.UUID
	// No competing matching worker remains: the fixture consumer is stopped and
	// the manual delivery above commits the continuation synchronously. The
	// bounded wait is retained only as persistence synchronization safety, not
	// because another worker races this delivery to commit the continuation.
	deadline := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	for {
		err := database.NewRaw(`SELECT id FROM operation WHERE kind='provider_artist_release_browse' AND id IN (SELECT request_id FROM matching_provider_dependency WHERE parent_run_id=?) AND id<>?`, parentID, firstBrowseID).Scan(ctx, &secondBrowseID)
		if err == nil {
			break
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("read second browse operation admitted by first page: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for second browse operation admitted by first page: %v", ctx.Err())
		case <-deadline.C:
		}
	}
	secondBrowse, err := repository.GetOperation(ctx, secondBrowseID)
	if err != nil {
		t.Fatal(err)
	}
	var secondIntent struct {
		Offset int `json:"offset"`
		Limit  int `json:"limit"`
	}
	if err := json.Unmarshal(secondBrowse.InputSnapshot, &secondIntent); err != nil || secondIntent.Offset != 100 || secondIntent.Limit != 100 {
		t.Fatalf("next-page browse intent=%+v err=%v; want offset 100 limit 100", secondIntent, err)
	}
	runProviderDelivery(secondBrowseID)
	runMatchingDelivery(nextWake())

	acquisitionSnapshot, exists, err = repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || acquisitionSnapshot.Phase != "catalog_ready" {
		t.Fatalf("final acquisition phase=%q exists=%t err=%v; want catalog_ready", acquisitionSnapshot.Phase, exists, err)
	}
	finalOperation, err := repository.GetOperation(ctx, parentID)
	if err != nil {
		t.Fatal(err)
	}
	if finalOperation.State != "running" || finalOperation.Stage != "ranking_pending" {
		t.Fatalf("parent state/stage=%s/%s; want running/ranking_pending", finalOperation.State, finalOperation.Stage)
	}
	var rankingWakeCount int
	if err := database.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL`, parentID).Scan(ctx, &rankingWakeCount); err != nil || rankingWakeCount != 1 {
		t.Fatalf("catalog completion registered %d pending wakes; want exactly one, err=%v", rankingWakeCount, err)
	}
	matchHeader, matchCandidates, err := persistence.NewIncomingMatchCandidateRepository(database).Get(ctx, group.ID)
	if err != nil || matchHeader != nil || len(matchCandidates) != 0 {
		t.Fatalf("finite evaluation published header=%+v candidates=%d err=%v; acquisition should park before scoring", matchHeader, len(matchCandidates), err)
	}
	var consumedDeliveries int
	if err := database.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND consumed_at IS NOT NULL`, parentID).Scan(ctx, &consumedDeliveries); err != nil {
		t.Fatal(err)
	}
	var pendingDeliveries int
	if err := database.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL`, parentID).Scan(ctx, &pendingDeliveries); err != nil {
		t.Fatal(err)
	}
	if consumedDeliveries != 4 || pendingDeliveries != 1 {
		t.Fatalf("matching delivery counts consumed=%d pending=%d; want 4 consumed and one ranking wake", consumedDeliveries, pendingDeliveries)
	}
	var finalGroupingEpoch int64
	if err := database.NewRaw(`SELECT grouping_epoch FROM matching_run WHERE id=?`, parentID).Scan(ctx, &finalGroupingEpoch); err != nil || finalGroupingEpoch != groupingEpoch {
		t.Fatalf("matching run grouping epoch=%d err=%v; want unchanged %d", finalGroupingEpoch, err, groupingEpoch)
	}
	requestMu.Lock()
	gotArtistSearches := artistSearches
	gotBrowseOffsets := append([]int(nil), browseOffsets...)
	requestMu.Unlock()
	if gotArtistSearches != 1 || len(gotBrowseOffsets) != 2 || gotBrowseOffsets[0] != 0 || gotBrowseOffsets[1] != 100 {
		t.Fatalf("HTTP requests artist searches=%d browse offsets=%v; want one search and pages [0 100]", gotArtistSearches, gotBrowseOffsets)
	}
	artistResponse, err := fetch.CachedResponse(ctx, *artistSearch.ProviderCacheKey)
	if err != nil || artistResponse == nil || !bytes.Contains(artistResponse.Payload, []byte("preserve-artist-search")) {
		t.Fatalf("accepted artist response=%+v err=%v; want retained raw response", artistResponse, err)
	}
	firstBrowse, err := repository.GetOperation(ctx, firstBrowseID)
	if err != nil || firstBrowse.ProviderCacheKey == nil {
		t.Fatalf("read first browse cache identity: operation=%+v err=%v", firstBrowse, err)
	}
	firstResponse, err := fetch.CachedResponse(ctx, *firstBrowse.ProviderCacheKey)
	if err != nil || firstResponse == nil || !bytes.Contains(firstResponse.Payload, []byte("preserve-artist-release-browse-0")) {
		t.Fatalf("accepted first browse response=%+v err=%v; want retained raw browse envelope", firstResponse, err)
	}
	secondResponse, err := fetch.CachedResponse(ctx, *secondBrowse.ProviderCacheKey)
	if err != nil || secondResponse == nil || !bytes.Contains(secondResponse.Payload, []byte("preserve-artist-release-browse-100")) {
		t.Fatalf("accepted second browse response=%+v err=%v; want retained raw browse envelope", secondResponse, err)
	}
	configuration, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	releaseKeys := make([]string, 101)
	for index := range releaseKeys {
		releaseKeys[index] = fmt.Sprintf("%08x-4444-4444-8444-%012x", index+1, index+1)
	}
	graph, err := cache.GetMusicBrainzMatchingSnapshot(ctx, server.URL+"/ws/2", configuration.Identity, releaseKeys, nil)
	if err != nil || len(graph.Releases) != 101 {
		t.Fatalf("accepted sparse provider graph releases=%d err=%v; want 101 persisted releases", len(graph.Releases), err)
	}
}
