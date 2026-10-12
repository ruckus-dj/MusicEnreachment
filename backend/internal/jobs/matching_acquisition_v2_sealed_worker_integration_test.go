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

// This drives a real version-2 automatic run through durable provider and
// matching deliveries against a local MusicBrainz-compatible HTTP server.
func TestMatchingAcquisitionV2SealedWorkerPublishesPostgreSQL(t *testing.T) {
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
		case strings.TrimSuffix(r.URL.Path, "/") == "/ws/2/artist" && r.URL.Query().Get("query") == "artist:Fixture Artist":
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
		case strings.HasPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/ws/2/release/"):
			releaseID := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/ws/2/release/")
			if releaseID != "00000001-4444-4444-8444-000000000001" {
				_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Unrelated Release","artist-credit":[{"name":"Unrelated Artist","artist":{"id":%q,"name":"Unrelated Artist"}}],"media":[]}`, releaseID, artistID)
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Fixture Album","artist-credit":[{"name":"Fixture Artist","artist":{"id":%q,"name":"Fixture Artist"}}],"media":[{"position":1,"tracks":[{"id":"55555555-5555-4555-8555-555555555555","number":"1","title":"Fixture Track","recording":{"id":"66666666-6666-4666-8666-666666666666","title":"Fixture Track","artist-credit":[{"name":"Fixture Artist","artist":{"id":%q,"name":"Fixture Artist"}}]}}]}]}`, releaseID, artistID, artistID)
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
	applyWorker := NewMatchingApplyWorker(repository, operations)
	client, listenerPool, err := PrepareWithWorkers(ctx, fixture.databaseURL, database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, &MatchingEvaluationWorker{})
		river.AddWorker(workers, applyWorker)
		river.AddWorker(workers, providerWorker)
		river.AddWorker(workers, &matchingReconciliationRegistrationWorker{})
	})
	if err != nil {
		t.Fatalf("prepare River with matching and provider workers: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	operations = service.NewOperationsWithRiver(repository, client)
	repository.SetRiverInserter(client)
	providerWorker.operations = operations
	applyWorker.operations = operations

	// Use source-analysis output to form the real staged group, as production
	// matching does; no hand-written source rows or provider graph fixtures.
	analysis := fixture.startWithMetadata(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *analysis.RiverJobID)
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
	var groupingEpoch int64
	if err := database.NewRaw(`SELECT revision FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &groupingEpoch); err != nil {
		t.Fatal(err)
	}

	// The reconciliation event is dispatched and claimed through its real
	// persistence fences before automatic admission.
	fetch = fetch.WithDurableOperations(repository, client)
	providerWorker.fetch = fetch
	inventory := persistence.NewSourceInventoryRepository(database)
	var event persistence.IncomingMatchingReconcileEvent
	if err := database.NewSelect().Model(&event).Where("reason = ?", "analysis_success").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read source-analysis reconciliation event: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch source-analysis reconciliation event: %v", err)
	}
	eventPtr, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil || eventPtr == nil || eventPtr.RegisteredJobID == nil {
		t.Fatalf("read dispatched reconciliation event: event=%+v err=%v", eventPtr, err)
	}
	event = *eventPtr
	eventEpoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *event.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim source-analysis reconciliation event: claimed=%v err=%v", claimed, err)
	}

	artistSearch, err := fetch.PrepareOperation(ctx, "artist_search", "artist:Fixture Artist", 0, 1, false)
	if err != nil {
		t.Fatalf("prepare initial artist search: %v", err)
	}
	queryDescriptor := append(json.RawMessage(nil), artistSearch.InputSnapshot...)
	parentID := uuid.New()
	parent := &persistence.Operation{ID: parentID, Kind: (service.MatchingEvaluationOperationArgs{}).Kind(), State: "queued", Stage: "waiting_provider", Attempt: 1}
	providerID, sourceID := *artistSearch.ProviderID, *artistSearch.ProviderSourceID
	artistSearch.ProviderID = &providerID
	artistSearch.ProviderSourceID = &sourceID
	admitted, err := repository.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Provider: persistence.MatchingProviderRequest{Operation: artistSearch, Args: service.ProviderFetchOperationArgs{OperationID: artistSearch.ID}},
		GroupID:  group.ID, ExpectedGroupRevision: group.Revision, GroupingEpoch: groupingEpoch,
		PolicyVersion: service.MatchingPolicyVersion, ArtistQueryPlanVersion: 1, QueryDescriptor: queryDescriptor,
		EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: eventEpoch,
	})
	if err != nil {
		t.Fatalf("admit automatic version-2 matching run: %v", err)
	}
	if admitted.AlreadyAdmitted || admitted.Operation == nil || admitted.Operation.RiverJobID == nil || artistSearch.RiverJobID == nil {
		t.Fatalf("automatic matching admission did not persist initial deliveries: %+v", admitted)
	}
	parentID = admitted.OriginalRunID
	var persistedParentInput struct {
		SchemaVersion int `json:"schema_version"`
		Plan          struct {
			SchemaVersion int    `json:"schema_version"`
			Intent        string `json:"intent"`
			Mode          string `json:"mode"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(admitted.Operation.InputSnapshot, &persistedParentInput); err != nil || persistedParentInput.SchemaVersion != 1 ||
		persistedParentInput.Plan.SchemaVersion != 4 || persistedParentInput.Plan.Intent != "automatic_artist_acquisition" || persistedParentInput.Plan.Mode != "automatic" {
		t.Fatalf("admitted parent routing snapshot=%s err=%v; want repository-generated plan-4 envelope", admitted.Operation.InputSnapshot, err)
	}
	var admissionCaptureDigest []byte
	if err := database.NewRaw(`SELECT target_capture_digest FROM matching_run_target WHERE run_id=?`, parentID).Scan(ctx, &admissionCaptureDigest); err != nil || len(admissionCaptureDigest) == 0 {
		t.Fatalf("read admitted target capture digest: digest=%x err=%v", admissionCaptureDigest, err)
	}
	if _, err := database.NewRaw(`UPDATE incoming_grouping_state SET revision=revision+1,needs_refresh=false WHERE singleton=true`).Exec(ctx); err != nil {
		t.Fatalf("advance unrelated grouping epoch after admission: %v", err)
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
			safeError := ""
			if operation.SafeError != nil {
				safeError = *operation.SafeError
			}
			t.Fatalf("parent state/stage=%s/%s safe_error=%q; want nonterminal stage %s", operation.State, operation.Stage, safeError, stage)
		}
	}

	// The first matching delivery consumes its already-pending provider wake and
	// must not begin acquisition before the artist search has been accepted.
	runMatchingDelivery(*admitted.Operation.RiverJobID)
	assertParentWaiting("waiting_provider")
	var initialConsumed bool
	if err := database.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, *admitted.Operation.RiverJobID).Scan(ctx, &initialConsumed); err != nil || !initialConsumed {
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
	// The provider's terminal state can become visible before the competing
	// River worker has consumed the parent wake and committed its continuation.
	// Wait for the durable child rather than assuming those transactions finish
	// synchronously with the provider delivery.
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
	assertParentWaiting("ranking_pending")
	// The catalog completion wake is itself the durable ranking trigger. Drive it
	// through the real worker so ranking and its selected lookup children are
	// exercised before any caller-authored shortlist can be considered.
	runMatchingDelivery(nextWake())
	acquisitionSnapshot, exists, err = repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || acquisitionSnapshot.Phase != persistence.MatchingAcquisitionReleaseHydration {
		operation, operationErr := repository.GetOperation(ctx, parentID)
		var latestWake int64
		var safeError string
		if operation.SafeError != nil {
			safeError = *operation.SafeError
		}
		_ = database.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL ORDER BY river_job_id DESC LIMIT 1`, parentID).Scan(ctx, &latestWake)
		t.Fatalf("after ranking acquisition phase=%q exists=%t err=%v operation=%+v safeError=%q operationErr=%v latestWake=%d", acquisitionSnapshot.Phase, exists, err, operation, safeError, operationErr, latestWake)
	}
	var rankedHydration struct {
		ReleaseIDs []string `json:"release_ids"`
	}
	if err := json.Unmarshal(acquisitionSnapshot.Input, &rankedHydration); err != nil || len(rankedHydration.ReleaseIDs) != 10 {
		t.Fatalf("ranked hydration input=%s err=%v; want ten selected catalog releases", acquisitionSnapshot.Input, err)
	}
	releaseKeys := make([]string, 101)
	for index := range releaseKeys {
		releaseKeys[index] = fmt.Sprintf("%08x-4444-4444-8444-%012x", index+1, index+1)
	}
	var capturedRunGroupingEpoch int64
	if err := database.NewRaw(`SELECT grouping_epoch FROM matching_run WHERE id=?`, parentID).Scan(ctx, &capturedRunGroupingEpoch); err != nil || capturedRunGroupingEpoch != groupingEpoch {
		t.Fatalf("version-2 run grouping epoch=%d err=%v; want admission epoch %d", capturedRunGroupingEpoch, err, groupingEpoch)
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
	graph, err := cache.GetMusicBrainzMatchingSnapshot(ctx, server.URL+"/ws/2", configuration.Identity, releaseKeys, nil)
	if err != nil || len(graph.Releases) != 101 {
		t.Fatalf("accepted sparse provider graph releases=%d err=%v; want 101 persisted releases", len(graph.Releases), err)
	}

	// Fetch each ranked lookup using the real provider worker. Every settlement
	// wakes the matching worker; the final wake seals the complete ranked set.
	var lookupIDs []uuid.UUID
	if err := database.NewRaw(`SELECT operation.id FROM matching_provider_dependency dependency JOIN operation ON operation.id=dependency.request_id WHERE dependency.parent_run_id=? AND operation.kind='provider_release_lookup' ORDER BY operation.id`, parentID).Scan(ctx, &lookupIDs); err != nil {
		t.Fatalf("read ranked release lookup operations: %v", err)
	}
	if len(lookupIDs) != len(rankedHydration.ReleaseIDs) {
		t.Fatalf("ranked release lookup count=%d; want %d", len(lookupIDs), len(rankedHydration.ReleaseIDs))
	}
	for _, lookupID := range lookupIDs {
		runProviderDelivery(lookupID)
		runMatchingDelivery(nextWake())
	}
	parentAfterSeal, err := repository.GetOperation(ctx, parentID)
	if err != nil || parentAfterSeal.State != "queued" || parentAfterSeal.Stage != "evaluation_queued" || parentAfterSeal.RiverJobID == nil {
		t.Fatalf("hydration did not seal and queue evaluation: operation=%+v err=%v", parentAfterSeal, err)
	}
	evaluationJobID := *parentAfterSeal.RiverJobID
	sealed, exists, err := acquisition.EvaluationSeal(ctx, parentID)
	if err != nil || !exists || sealed.SchemaVersion != 2 || len(sealed.ReleaseIDs) != len(rankedHydration.ReleaseIDs) || len(sealed.ReleaseLookupCacheKeys) != len(rankedHydration.ReleaseIDs) {
		t.Fatalf("read persisted evaluation seal: input=%+v exists=%v err=%v", sealed, exists, err)
	}
	setMatchingRiverJobRunning(t, ctx, database, evaluationJobID)
	if err := matchingWorker.Work(ctx, &river.Job[service.MatchingEvaluationOperationArgs]{
		JobRow: &rivertype.JobRow{ID: evaluationJobID, Attempt: 1},
		Args:   service.MatchingEvaluationOperationArgs{OperationID: parentID},
	}); err != nil {
		t.Fatalf("run sealed matching evaluation delivery: %v", err)
	}
	completed, err := repository.GetOperation(ctx, parentID)
	if err != nil || completed.State != "succeeded" {
		t.Fatalf("sealed matching parent state=%v err=%v; want succeeded", completed, err)
	}
	var header *persistence.IncomingMatchEvaluationHeader
	var candidates []persistence.IncomingMatchCandidate
	header, candidates, err = persistence.NewIncomingMatchCandidateRepository(database).Get(ctx, group.ID)
	if err != nil || header == nil || header.PolicyVersion != service.MatchingPolicyVersion || header.FenceVersion != 2 ||
		header.TargetRunID != parentID || !bytes.Equal(header.TargetCaptureDigest, admissionCaptureDigest) {
		t.Fatalf("sealed matching publication header run=%s fence=%d digest=%x; want run=%s fence=2 digest=%x; candidates=%d err=%v", header.TargetRunID, header.FenceVersion, header.TargetCaptureDigest, parentID, admissionCaptureDigest, len(candidates), err)
	}
	if len(candidates) != 1 || candidates[0].Score < .7 || len(candidates[0].Positions) != 1 || candidates[0].Positions[0].Medium != 0 || candidates[0].Positions[0].Position != 0 {
		t.Fatalf("hydrated release candidate=%+v; want a confident assignment at actual medium/track position", candidates)
	}
	var currentGroupingEpoch int64
	if err := database.NewRaw(`SELECT revision FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &currentGroupingEpoch); err != nil || currentGroupingEpoch == groupingEpoch {
		t.Fatalf("unrelated grouping epoch=%d err=%v; expected advance from %d", currentGroupingEpoch, err, groupingEpoch)
	}

	// Apply is target-scoped: advancing the unrelated grouping epoch again must
	// not invalidate this sealed run's unchanged group, generation, or authority.
	if _, err := database.NewRaw(`UPDATE incoming_grouping_state SET revision=revision+1,needs_refresh=false WHERE singleton=true`).Exec(ctx); err != nil {
		t.Fatalf("advance unrelated grouping epoch before matching apply: %v", err)
	}
	var apply struct {
		OperationID uuid.UUID `bun:"id"`
		RiverJobID  int64     `bun:"river_job_id"`
		Attempt     int       `bun:"attempt"`
	}
	if err := database.NewRaw(`SELECT operation.id,operation.river_job_id,operation.attempt
		FROM matching_apply_request request JOIN operation ON operation.id=request.apply_operation_id
		WHERE request.group_id=? AND request.parent_run_id=?`, group.ID, parentID).Scan(ctx, &apply); err != nil {
		t.Fatalf("read durable matching apply child: %v", err)
	}
	if apply.OperationID == uuid.Nil || apply.RiverJobID < 1 || apply.Attempt < 1 {
		t.Fatalf("durable matching apply child=%+v; want operation, River delivery, and attempt", apply)
	}
	if err := applyWorker.Work(ctx, &river.Job[persistence.MatchingApplyOperationArgs]{
		JobRow: &rivertype.JobRow{ID: apply.RiverJobID, Attempt: apply.Attempt},
		Args:   persistence.MatchingApplyOperationArgs{OperationID: apply.OperationID},
	}); err != nil {
		t.Fatalf("run real matching apply worker: %v", err)
	}
	applied, err := repository.GetOperation(ctx, apply.OperationID)
	if err != nil || applied.State != "succeeded" || applied.Stage != "applied" {
		t.Fatalf("matching apply child state/stage=%s/%s err=%v; want succeeded/applied", applied.State, applied.Stage, err)
	}
	var receiptOutcome string
	if err := database.NewRaw(`SELECT outcome FROM matching_apply_receipt WHERE apply_operation_id=? AND group_id=?`, apply.OperationID, group.ID).Scan(ctx, &receiptOutcome); err != nil || receiptOutcome != "applied" {
		t.Fatalf("matching apply receipt outcome=%q err=%v; want applied", receiptOutcome, err)
	}
	var materialized struct {
		Count          int    `bun:"count"`
		Selected       bool   `bun:"selected"`
		Compatible     bool   `bun:"compatible"`
		DecisionMethod string `bun:"decision_method"`
	}
	if err := database.NewRaw(`SELECT count(*) AS count, bool_and(selected) AS selected, bool_and(compatible) AS compatible,
		min(decision_method) AS decision_method FROM track_source WHERE media_variant_id=?`, group.Members[0]).Scan(ctx, &materialized); err != nil {
		t.Fatalf("read automatically materialized source association: %v", err)
	}
	if materialized.Count != 1 || materialized.Selected || !materialized.Compatible || materialized.DecisionMethod != "automatic" {
		t.Fatalf("automatically materialized source association=%+v; want exactly one compatible, unselected automatic association", materialized)
	}
	var afterApply struct {
		EvaluationRevision string    `bun:"evaluation_revision"`
		TargetRunID        uuid.UUID `bun:"target_run_id"`
		FenceVersion       int       `bun:"fence_version"`
		AuthorityMode      string    `bun:"matching_authority_mode"`
		AuthorityRevision  int64     `bun:"matching_authority_revision"`
		CaptureDigest      []byte    `bun:"target_capture_digest"`
	}
	if err := database.NewRaw(`SELECT evaluation_revision,target_run_id,fence_version,matching_authority_mode,
		matching_authority_revision,target_capture_digest FROM incoming_match_evaluation WHERE group_id=?`, group.ID).Scan(ctx, &afterApply); err != nil {
		t.Fatalf("read evaluation authority after apply: %v", err)
	}
	if afterApply.EvaluationRevision != header.EvaluationRevision || afterApply.TargetRunID != parentID || afterApply.FenceVersion != 2 ||
		afterApply.AuthorityMode != header.MatchingAuthorityMode || afterApply.AuthorityRevision != header.MatchingAuthorityRevision ||
		!bytes.Equal(afterApply.CaptureDigest, admissionCaptureDigest) {
		t.Fatalf("evaluation header authority changed during apply: %+v; original=%+v", afterApply, header)
	}
	var runAuthority struct {
		TargetGeneration    int64 `bun:"target_generation"`
		AuthorityRevision   int64 `bun:"authority_revision"`
		GenerationHighWater int64 `bun:"high_water"`
	}
	if err := database.NewRaw(`SELECT target.target_generation,target.authority_revision,generation.high_water
		FROM matching_run_target target JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
		WHERE target.run_id=? AND target.group_id=? AND generation.current_run_id=target.run_id`, parentID, group.ID).Scan(ctx, &runAuthority); err != nil {
		t.Fatalf("read current matching run generation after apply: %v", err)
	}
	if runAuthority.TargetGeneration != runAuthority.GenerationHighWater || runAuthority.AuthorityRevision != header.MatchingAuthorityRevision {
		t.Fatalf("matching run authority/generation after apply=%+v; want unchanged current target", runAuthority)
	}
	var pendingMatchingDeliveries int
	if err := database.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL`, parentID).Scan(ctx, &pendingMatchingDeliveries); err != nil || pendingMatchingDeliveries != 0 {
		t.Fatalf("pending matching deliveries for completed parent=%d err=%v; want none (provider wakes are settled and consumed)", pendingMatchingDeliveries, err)
	}
	if _, err := database.NewRaw(`UPDATE matching_run SET immutable_plan=jsonb_set(immutable_plan,'{schema_version}','99'::jsonb) WHERE id=?`, parentID).Exec(ctx); err != nil {
		t.Fatalf("corrupt completed run plan version for negative fence check: %v", err)
	}
	if _, exists, err := repository.GetMatchingRunFence(ctx, parentID); err == nil || exists {
		t.Fatalf("unknown matching run fence exists=%t err=%v; want a validation error", exists, err)
	}
}

// matchingReconciliationRegistrationWorker lets the integration fixture insert
// the durable outbox job; the test explicitly drives the claimed admission path.
type matchingReconciliationRegistrationWorker struct {
	river.WorkerDefaults[persistence.IncomingMatchingReconcileJobArgs]
}

func (*matchingReconciliationRegistrationWorker) Work(context.Context, *river.Job[persistence.IncomingMatchingReconcileJobArgs]) error {
	return nil
}
