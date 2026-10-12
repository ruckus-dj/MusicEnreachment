//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type failAfterMatchingWakeInsert struct {
	persistence.RiverInserter
}

type fallbackWrongKindJobArgs struct{}

func (fallbackWrongKindJobArgs) Kind() string { return "not_provider_fetch_v1" }

func (inserter failAfterMatchingWakeInsert) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := inserter.RiverInserter.InsertTx(ctx, tx, args, options)
	if err != nil {
		return result, err
	}
	if _, ok := args.(persistence.MatchingEvaluationWakeArgs); ok {
		return result, errors.New("injected failure after matching wake insert")
	}
	return result, nil
}

func TestAutomaticFallbackHydrationWaitsForEveryLookupReceiptPostgreSQL(t *testing.T) {
	testAutomaticFallbackHydrationWaitsForEveryLookupReceiptPostgreSQL(t, false)
}

func TestAutomaticFallbackQualifiedApplicabilityWaitsForPolicyPostgreSQL(t *testing.T) {
	testAutomaticFallbackHydrationWaitsForEveryLookupReceiptPostgreSQL(t, true)
}

func testAutomaticFallbackHydrationWaitsForEveryLookupReceiptPostgreSQL(t *testing.T, qualified bool) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	ctx, cancel := context.WithTimeout(fixture.ctx, 60*time.Second)
	defer cancel()
	metadataTags := `{"ALBUM":["Captured Album"],"TITLE":["Receipt Track"]}`
	if qualified {
		// The first hydrated release uses this exact title; the real calculator
		// also sees the matching track title, duration, and position.
		metadataTags = `{"ALBUM":["Album 90000000-0000-4000-8000-000000000001"],"TITLE":["Receipt Track"]}`
	}
	if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags=?::jsonb`, metadataTags).Exec(ctx); err != nil {
		t.Fatalf("set fallback metadata: %v", err)
	}
	grouping := persistence.NewIncomingGroupingRepository(fixture.db)
	if err := service.NewIncomingGroups(grouping).Refresh(ctx); err != nil {
		t.Fatalf("refresh groups: %v", err)
	}
	groups, err := grouping.List(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("read refreshed group: groups=%d err=%v", len(groups), err)
	}
	var tags []string
	if err := fixture.db.NewRaw(`SELECT observed_tags::text FROM media_metadata_result ORDER BY created_at,id`).Scan(ctx, &tags); err != nil {
		t.Fatalf("read fallback tags: %v", err)
	}
	frozen := make([]map[string][]string, len(tags))
	for i, tag := range tags {
		if err := json.Unmarshal([]byte(tag), &frozen[i]); err != nil {
			t.Fatalf("decode fallback tags: %v", err)
		}
	}
	planned, artistBearing := policy.PlanFallbackQueries(frozen)
	if artistBearing || len(planned.Stages) == 0 || planned.Stages[0].Kind != "album_track" {
		t.Fatalf("unexpected fallback plan: %+v artistBearing=%v", planned, artistBearing)
	}
	plan := policy.TypedFallbackQueryPlan{SchemaVersion: planned.SchemaVersion, Stages: planned.Stages}
	stage := plan.Stages[0]
	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.db.NewSelect().Model(&event).Where("state = ? AND delivery_claimed", "dispatched").Order("created_at DESC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read claimed reconciliation event: %v", err)
	}
	var eventEpoch int64
	if err := fixture.db.NewRaw(`SELECT delivery_epoch FROM incoming_matching_reconcile_event WHERE id=?`, event.ID).Scan(ctx, &eventEpoch); err != nil {
		t.Fatalf("read reconciliation epoch: %v", err)
	}

	releaseA, releaseB := "90000000-0000-4000-8000-000000000001", "90000000-0000-4000-8000-000000000002"
	trackID, recordingID := map[string]string{releaseA: uuid.NewString(), releaseB: uuid.NewString()}, map[string]string{releaseA: uuid.NewString(), releaseB: uuid.NewString()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 4 || parts[len(parts)-2] != "release" {
			http.Error(w, "unexpected endpoint", http.StatusNotFound)
			return
		}
		id := parts[len(parts)-1]
		w.Header().Set("Content-Type", "application/json")
		if id == releaseA || id == releaseB {
			_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Album %s","artist-credit":[{"name":"Artist %s","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Artist %s"}}],"media":[{"position":1,"tracks":[{"id":%q,"number":"1","length":180000,"recording":{"id":%q,"title":"Receipt Track","artist-credit":[{"name":"Track Artist","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Artist %s"}}]}}]}]}`, id, id, id, id, trackID[id], recordingID[id], id)
			return
		}
		http.Error(w, "unexpected release", http.StatusNotFound)
	}))
	defer server.Close()
	registry := settings.New(persistence.NewSettingsRepository(fixture.db), nil)
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", server.URL+"/ws/2"); err != nil {
		t.Fatalf("save provider config: %v", err)
	}
	config, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatalf("read provider config: %v", err)
	}
	cache := persistence.NewProviderCacheRepository(fixture.db)
	source, err := cache.ResolveMusicBrainzSource(ctx, config.BaseURL, config.Identity)
	if err != nil {
		t.Fatalf("resolve provider source: %v", err)
	}
	searchKey := "recording-search:0:10:" + stage.Query
	searchTicket, err := cache.BeginRefresh(ctx, source.ID, searchKey, config.Identity)
	if err != nil {
		t.Fatalf("reserve fallback search: %v", err)
	}
	searchPayload := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":1,"offset":0,"recordings":[{"id":"11111111-1111-4111-8111-111111111111","title":"Receipt Track","releases":[{"id":"90000000-0000-4000-8000-000000000001","title":"Captured Album"},{"id":"90000000-0000-4000-8000-000000000002","title":"Captured Album B"}]}]}`)
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: searchTicket, FetchedAt: time.Now().UTC(), Payload: searchPayload}); err != nil {
		t.Fatalf("accept fallback search fixture: %v", err)
	}
	inserter := &fallbackHandoffRiverInserter{}
	operations := service.NewOperationsWithRiver(fixture.repository, inserter)
	factory := service.NewProviderFactory(registry, nil, nil, server.Client(), nil)
	fetch := service.NewProviderFetchService(registry, factory, cache).WithDurableOperations(fixture.repository, inserter)
	providerWorker := jobs.NewProviderFetchWorker(fetch, operations)
	matchingWorker := jobs.NewMatchingEvaluationWorker(fixture.repository,
		service.NewMatchingEvaluationService(service.NewIncomingGroups(grouping), grouping, cache,
			persistence.NewIncomingMatchCandidateRepository(fixture.db), registry), operations)
	client, listenerPool, err := jobs.PrepareWithWorkers(ctx, testpostgres.URL(t, fixture.db), fixture.db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, providerWorker)
		river.AddWorker(workers, matchingWorker)
	})
	if err != nil {
		t.Fatalf("prepare River client: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	inserter.client = client
	providerKind, _, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	if !ok {
		t.Fatalf("no provider mapping for %q", stage.Kind)
	}
	searchInput, _ := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}{1, stage.Query, 0, 10})
	childID, parentID := uuid.New(), uuid.New()
	providerID, sourceID, identity := source.ProviderID, source.ID, config.Identity
	child := &persistence.Operation{ID: childID, Kind: providerKind, State: "queued", Stage: "queued", InputSnapshot: searchInput,
		ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &identity,
		ProviderEndpoint: source.Endpoint, ProviderCacheKey: &searchKey, Attempt: 1}
	admitted, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(ctx, client, persistence.AutomaticFallbackMatchingAdmission{
		Parent:     &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1},
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Provider:   persistence.MatchingProviderRequest{Operation: child, Args: service.ProviderFetchOperationArgs{OperationID: childID}},
		Source:     persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: source.ProviderID, Endpoint: source.Endpoint, ConfigurationIdentity: config.Identity},
		GroupID:    groups[0].ID, ExpectedGroupRevision: groups[0].Revision, PolicyVersion: service.MatchingPolicyVersion,
		Plan: plan, EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: event.HighestDeliveryAttempt, EventDeliveryEpoch: eventEpoch,
	})
	if err != nil {
		t.Fatalf("admit fallback run: %v", err)
	}
	child, err = fixture.repository.GetOperation(ctx, childID)
	if err != nil || child.RiverJobID == nil {
		t.Fatalf("read fallback search operation: %+v err=%v", child, err)
	}
	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *child.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: childID}}); err != nil {
		t.Fatalf("complete fallback search: %v", err)
	}
	parentID = admitted.OriginalRunID
	acquisition := service.NewMatchingAcquisitionService(fixture.repository, fetch, inserter)
	claimNextParentWake := func() (int64, persistence.MatchingDeliveryClaim) {
		t.Helper()
		var wakeID int64
		if err := fixture.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? ORDER BY river_job_id DESC LIMIT 1`, parentID).Scan(ctx, &wakeID); err != nil {
			t.Fatalf("read parent wake: %v", err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, wakeID).Exec(ctx); err != nil {
			t.Fatalf("mark parent wake running: %v", err)
		}
		claim, outcome, err := fixture.repository.ClaimMatchingDelivery(ctx, parentID, 1, wakeID, 1)
		if err != nil || outcome != persistence.MatchingDeliveryClaimed {
			t.Fatalf("claim parent wake: claim=%+v outcome=%v err=%v", claim, outcome, err)
		}
		return wakeID, claim
	}
	advance := func(wakeID int64, claim persistence.MatchingDeliveryClaim) (bool, error) {
		t.Helper()
		changed, handled, err := acquisition.Advance(ctx, parentID, 1, wakeID, claim)
		if !handled && err == nil {
			t.Fatal("hydration wake was not handled")
		}
		return changed, err
	}
	wakeID, claim := claimNextParentWake()
	changed, err := advance(wakeID, claim)
	if err != nil || !changed {
		t.Fatalf("advance fallback search: changed=%v err=%v", changed, err)
	}
	snapshot, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || snapshot.Phase != persistence.MatchingAcquisitionFallbackHydration {
		t.Fatalf("fallback hydration snapshot=%+v exists=%v err=%v", snapshot, exists, err)
	}
	var manifest struct {
		Lookups []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	if err := json.Unmarshal(snapshot.Output, &manifest); err != nil || len(manifest.Lookups) != 2 {
		t.Fatalf("expected two registered lookups: %s err=%v", snapshot.Output, err)
	}
	sort.Slice(manifest.Lookups, func(i, j int) bool { return manifest.Lookups[i].ReleaseID < manifest.Lookups[j].ReleaseID })
	for _, lookup := range manifest.Lookups {
		operation, err := fixture.repository.GetOperation(ctx, lookup.RequestID)
		if err != nil || operation.RiverJobID == nil {
			t.Fatalf("read registered lookup %s: operation=%+v err=%v", lookup.ReleaseID, operation, err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *operation.RiverJobID).Exec(ctx); err != nil {
			t.Fatalf("make lookup worker delivery deterministic: %v", err)
		}
		if lookup.RequestID == manifest.Lookups[0].RequestID {
			payload := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Cached %s","artist-credit":[],"media":[{"position":1,"tracks":[{"id":%q,"number":"1","length":180000,"recording":{"id":%q,"title":"Receipt Track"}}]}]}`, lookup.ReleaseID, lookup.ReleaseID, trackID[lookup.ReleaseID], recordingID[lookup.ReleaseID]))
			graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: payload}})
			if err != nil {
				t.Fatalf("normalize first lookup cache response: %v", err)
			}
			ticket, err := cache.BeginRefresh(ctx, source.ID, lookup.CacheKey, config.Identity)
			if err != nil {
				t.Fatalf("reserve first lookup cache response: %v", err)
			}
			if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload, Graph: graph}); err != nil {
				t.Fatalf("accept first lookup cache response: %v", err)
			}
			if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *operation.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: lookup.RequestID}}); err != nil {
				t.Fatalf("adopt cached first lookup through provider worker: %v", err)
			}
			break
		}
	}
	wakeID, claim = claimNextParentWake()
	changed, err = advance(wakeID, claim)
	if err != nil || !changed {
		t.Fatalf("first receipt wake was not consumed: changed=%v err=%v", changed, err)
	}
	stillHydrating, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || stillHydrating.Phase != persistence.MatchingAcquisitionFallbackHydration || stillHydrating.Revision != snapshot.Revision {
		t.Fatalf("first receipt advanced incomplete hydration: snapshot=%+v exists=%v err=%v", stillHydrating, exists, err)
	}
	var runState string
	var activeJobID *int64
	var deliveryState string
	if err := fixture.db.NewRaw(`SELECT run.state,run.active_river_job_id,operation.state
		FROM matching_run run JOIN operation ON operation.id=run.id WHERE run.id=?`, parentID).Scan(ctx, &runState, &activeJobID, &deliveryState); err != nil {
		t.Fatalf("read state after pending receipt wake: %v", err)
	}
	if runState != "waiting_provider" || activeJobID != nil || deliveryState != "running" {
		t.Fatalf("pending receipt wake did not clear its active claim while retaining operation state: runState=%q activeJobID=%v operationState=%q", runState, activeJobID, deliveryState)
	}
	var consumed bool
	var executionEpoch int64
	if err := fixture.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, wakeID).Scan(ctx, &consumed); err != nil {
		t.Fatalf("read consumed pending wake: %v", err)
	}
	if !consumed {
		t.Fatal("pending receipt wake was not durably consumed")
	}
	if err := fixture.db.NewRaw(`SELECT execution_epoch FROM matching_run WHERE id=?`, parentID).Scan(ctx, &executionEpoch); err != nil || executionEpoch != claim.ExecutionEpoch {
		t.Fatalf("pending receipt wake changed its execution epoch: got=%d claim=%d err=%v", executionEpoch, claim.ExecutionEpoch, err)
	}
	second := manifest.Lookups[1]
	secondOperation, err := fixture.repository.GetOperation(ctx, second.RequestID)
	if err != nil || secondOperation.RiverJobID == nil {
		t.Fatalf("read second lookup: operation=%+v err=%v", secondOperation, err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *secondOperation.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("make second lookup delivery deterministic: %v", err)
	}
	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{JobRow: &rivertype.JobRow{ID: *secondOperation.RiverJobID, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: second.RequestID}}); err != nil {
		t.Fatalf("complete second real lookup worker: %v", err)
	}
	secondOperation, err = fixture.repository.GetOperation(ctx, second.RequestID)
	if err != nil || secondOperation.State != "succeeded" {
		safeError := ""
		if secondOperation != nil && secondOperation.SafeError != nil {
			safeError = *secondOperation.SafeError
		}
		t.Fatalf("second real lookup worker did not succeed: operation=%+v safeError=%q err=%v", secondOperation, safeError, err)
	}
	secondReceiptA, err := fixture.repository.GetMatchingFallbackLookupReceipt(ctx, parentID, second.RequestID)
	if err != nil || secondReceiptA == nil {
		t.Fatalf("read accepted second lookup receipt A: receipt=%+v err=%v", secondReceiptA, err)
	}
	searchReceipt, err := fixture.repository.GetMatchingFallbackSearchReceipt(ctx, parentID, childID)
	if err != nil || searchReceipt == nil {
		t.Fatalf("read accepted search receipt: receipt=%+v err=%v", searchReceipt, err)
	}
	cacheB := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Mutable replacement B","artist-credit":[],"media":[{"position":1,"tracks":[{"id":%q,"number":"1","length":271000,"recording":{"id":%q,"title":"Mutable replacement B"}}]}]}`, second.ReleaseID, trackID[second.ReleaseID], recordingID[second.ReleaseID]))
	graphB, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: cacheB}})
	if err != nil {
		t.Fatalf("normalize mutable cache replacement B: %v", err)
	}
	cacheTicketB, err := cache.BeginRefresh(ctx, source.ID, second.CacheKey, config.Identity)
	if err != nil {
		t.Fatalf("reserve mutable cache replacement B: %v", err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: cacheTicketB, FetchedAt: time.Now().UTC(), Payload: cacheB, Graph: graphB}); err != nil {
		t.Fatalf("publish mutable cache replacement B: %v", err)
	}
	currentCacheB, err := cache.GetCachedResponse(ctx, source.ID, second.CacheKey)
	if err != nil || currentCacheB == nil || !strings.Contains(string(currentCacheB.Payload), "Mutable replacement B") || !strings.Contains(string(currentCacheB.Payload), "271000") {
		t.Fatalf("mutable provider cache did not contain B: cached=%+v err=%v", currentCacheB, err)
	}
	secondReceiptAfterB, err := fixture.repository.GetMatchingFallbackLookupReceipt(ctx, parentID, second.RequestID)
	if err != nil || secondReceiptAfterB == nil || string(secondReceiptAfterB.Payload) != string(secondReceiptA.Payload) || secondReceiptAfterB.PayloadSHA256 != secondReceiptA.PayloadSHA256 || string(secondReceiptAfterB.AcceptedProjection) != string(secondReceiptA.AcceptedProjection) || secondReceiptAfterB.ProjectionSHA256 != secondReceiptA.ProjectionSHA256 {
		t.Fatalf("mutable cache replacement changed immutable receipt A: before=%+v after=%+v err=%v", secondReceiptA, secondReceiptAfterB, err)
	}
	wakeID, claim = claimNextParentWake()
	var baselineJobs, baselineDeliveries int
	var baselineDependencies int
	var baselineOperationState string
	var baselineDeliveryIDs string
	if err := fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, parentID).Scan(ctx, &baselineOperationState); err != nil {
		t.Fatalf("read operation state before final hydration: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`).Scan(ctx, &baselineJobs); err != nil {
		t.Fatalf("count matching jobs before final hydration: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &baselineDeliveries); err != nil {
		t.Fatalf("count matching deliveries before final hydration: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &baselineDependencies); err != nil {
		t.Fatalf("count dependencies before final hydration: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT array_agg(river_job_id ORDER BY river_job_id) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &baselineDeliveryIDs); err != nil {
		t.Fatalf("capture delivery IDs before final hydration: %v", err)
	}
	failingAcquisition := service.NewMatchingAcquisitionService(fixture.repository, fetch, failAfterMatchingWakeInsert{RiverInserter: client})
	if changed, handled, err := failingAcquisition.Advance(ctx, parentID, 1, wakeID, claim); err == nil || changed || !handled {
		t.Fatalf("post-insert failure did not abort final hydration: changed=%v handled=%v err=%v", changed, handled, err)
	}
	afterFailure, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || afterFailure.Phase != stillHydrating.Phase || afterFailure.Revision != stillHydrating.Revision {
		t.Fatalf("failed final hydration changed phase/revision: snapshot=%+v exists=%v err=%v", afterFailure, exists, err)
	}
	var failedRunState string
	var failedOperationState string
	var failedActiveJob *int64
	var failedConsumed bool
	var jobsAfterFailure, deliveriesAfterFailure, dependenciesAfterFailure int
	var deliveryIDsAfterFailure string
	if err := fixture.db.NewRaw(`SELECT state,active_river_job_id FROM matching_run WHERE id=?`, parentID).Scan(ctx, &failedRunState, &failedActiveJob); err != nil {
		t.Fatalf("read run after failed final hydration: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, parentID).Scan(ctx, &failedOperationState); err != nil {
		t.Fatalf("read operation after failed final hydration: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, wakeID).Scan(ctx, &failedConsumed); err != nil {
		t.Fatalf("read delivery after failed final hydration: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`).Scan(ctx, &jobsAfterFailure); err != nil {
		t.Fatalf("count jobs after failed final hydration: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &deliveriesAfterFailure); err != nil {
		t.Fatalf("count deliveries after failed final hydration: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &dependenciesAfterFailure); err != nil {
		t.Fatalf("count dependencies after failed final hydration: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT array_agg(river_job_id ORDER BY river_job_id) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &deliveryIDsAfterFailure); err != nil {
		t.Fatalf("read delivery IDs after failed final hydration: %v", err)
	}
	if failedRunState != "running" || failedOperationState != baselineOperationState || failedActiveJob == nil || *failedActiveJob != wakeID || failedConsumed || jobsAfterFailure != baselineJobs || deliveriesAfterFailure != baselineDeliveries || dependenciesAfterFailure != baselineDependencies || deliveryIDsAfterFailure != baselineDeliveryIDs {
		t.Fatalf("post-insert rollback did not preserve active delivery state: run=%q operation=%q/%q active=%v consumed=%v jobs=%d/%d deliveries=%d/%d dependencies=%d/%d deliveryIDs=%v/%v", failedRunState, failedOperationState, baselineOperationState, failedActiveJob, failedConsumed, jobsAfterFailure, baselineJobs, deliveriesAfterFailure, baselineDeliveries, dependenciesAfterFailure, baselineDependencies, deliveryIDsAfterFailure, baselineDeliveryIDs)
	}
	var wakeCurrent bool
	if err := fixture.db.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_delivery WHERE run_id=? AND river_job_id=? AND operation_attempt=1 AND consumed_at IS NULL)`, parentID, wakeID).Scan(ctx, &wakeCurrent); err != nil || !wakeCurrent {
		t.Fatalf("failed transition lost its current wake: current=%v err=%v", wakeCurrent, err)
	}
	changed, err = advance(wakeID, claim)
	if err != nil || !changed {
		t.Fatalf("complete hydration after second receipt: changed=%v err=%v", changed, err)
	}
	complete, exists, err := fixture.repository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || complete.Phase != persistence.MatchingAcquisitionFallbackApplicability || complete.Revision != snapshot.Revision+1 {
		t.Fatalf("complete receipt set did not advance to applicability: snapshot=%+v exists=%v err=%v", complete, exists, err)
	}
	var applicabilityOutput struct {
		SearchReceipt struct {
			RequestID     uuid.UUID `json:"request_id"`
			PayloadSHA256 string    `json:"payload_sha256"`
		} `json:"search_receipt"`
		LookupReceipts []struct {
			RequestID        uuid.UUID `json:"request_id"`
			PayloadSHA256    string    `json:"payload_sha256"`
			ProjectionSHA256 string    `json:"projection_sha256"`
		} `json:"lookup_receipts"`
	}
	if err := json.Unmarshal(complete.Output, &applicabilityOutput); err != nil || applicabilityOutput.SearchReceipt.RequestID != childID || applicabilityOutput.SearchReceipt.PayloadSHA256 != searchReceipt.PayloadSHA256 || len(applicabilityOutput.LookupReceipts) != 2 {
		t.Fatalf("applicability output lost accepted search/lookup receipt references: output=%s decoded=%+v err=%v", complete.Output, applicabilityOutput, err)
	}
	for _, reference := range applicabilityOutput.LookupReceipts {
		receipt, err := fixture.repository.GetMatchingFallbackLookupReceipt(ctx, parentID, reference.RequestID)
		if err != nil || receipt == nil || reference.PayloadSHA256 != receipt.PayloadSHA256 || reference.ProjectionSHA256 != receipt.ProjectionSHA256 {
			t.Fatalf("applicability output does not reference immutable accepted receipt: reference=%+v receipt=%+v err=%v", reference, receipt, err)
		}
	}
	var newWakeID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? ORDER BY river_job_id DESC LIMIT 1`, parentID).Scan(ctx, &newWakeID); err != nil {
		t.Fatalf("read registered applicability wake: %v", err)
	}
	var newWakeKind string
	var newWakeArgs []byte
	if err := fixture.db.NewRaw(`SELECT kind,args FROM river_job WHERE id=?`, newWakeID).Scan(ctx, &newWakeKind, &newWakeArgs); err != nil {
		t.Fatalf("read applicability wake job: %v", err)
	}
	var wakeArgs persistence.MatchingEvaluationWakeArgs
	if err := json.Unmarshal(newWakeArgs, &wakeArgs); err != nil || len(newWakeArgs) == 0 || wakeArgs.OperationID != parentID {
		t.Fatalf("decode applicability wake args: args=%s decoded=%+v err=%v", newWakeArgs, wakeArgs, err)
	}
	var wakeArgFields map[string]json.RawMessage
	if err := json.Unmarshal(newWakeArgs, &wakeArgFields); err != nil || len(wakeArgFields) != 1 || newWakeID <= wakeID || newWakeKind != "matching_evaluation_v1" {
		t.Fatalf("successful retry did not register exact new applicability wake: id=%d previous=%d kind=%q args=%s err=%v", newWakeID, wakeID, newWakeKind, newWakeArgs, err)
	}
	var jobsAfterRetry, deliveriesAfterRetry int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`).Scan(ctx, &jobsAfterRetry); err != nil {
		t.Fatalf("count jobs after final hydration retry: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &deliveriesAfterRetry); err != nil {
		t.Fatalf("count deliveries after final hydration retry: %v", err)
	}
	if jobsAfterRetry != baselineJobs+1 || deliveriesAfterRetry != baselineDeliveries+1 {
		t.Fatalf("retry did not create exactly one wake and delivery: jobs=%d baseline=%d deliveries=%d baseline=%d", jobsAfterRetry, baselineJobs, deliveriesAfterRetry, baselineDeliveries)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, newWakeID).Exec(ctx); err != nil {
		t.Fatalf("mark registered applicability wake running: %v", err)
	}
	freshRepository := persistence.NewSetupManagerRepository(fixture.db)
	freshClaim, outcome, err := freshRepository.ClaimMatchingDelivery(ctx, parentID, 1, newWakeID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed || freshClaim.ExecutionEpoch <= claim.ExecutionEpoch {
		t.Fatalf("fresh repository could not claim registered applicability wake: claim=%+v outcome=%v priorEpoch=%d err=%v", freshClaim, outcome, claim.ExecutionEpoch, err)
	}
	var outcomeCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND terminal_outcome='succeeded'`, parentID).Scan(ctx, &outcomeCount); err != nil || outcomeCount != 3 {
		t.Fatalf("search plus two lookup dependencies should be succeeded: count=%d err=%v", outcomeCount, err)
	}
	var receiptCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_lookup_receipt WHERE run_id=?`, parentID).Scan(ctx, &receiptCount); err != nil || receiptCount != 2 {
		t.Fatalf("both immutable lookup receipts should exist: count=%d err=%v", receiptCount, err)
	}
	calculation, exists, err := service.PrepareFallbackApplicability(ctx, freshRepository, parentID, complete.Revision)
	if err != nil || !exists || len(calculation.AllCandidates) != 4 || (!qualified && calculation.QualifyingEdges != 0) || (qualified && calculation.QualifyingEdges == 0) {
		t.Fatalf("prepare persisted fallback applicability: exists=%v candidates=%d result=%+v err=%v", exists, len(calculation.AllCandidates), calculation, err)
	}
	// Exercise the production service conversion and current fenced PostgreSQL
	// writer using the still-running delivery claimed above.
	calculationContext, contextExists, err := freshRepository.LoadMatchingFallbackCalculationContext(ctx, parentID, complete.Revision)
	if err != nil || !contextExists {
		t.Fatalf("load writer context: exists=%v err=%v", contextExists, err)
	}
	contextJSON, err := json.Marshal(calculationContext)
	if err != nil {
		t.Fatalf("serialize writer context: %v", err)
	}
	contextDigest, err := persistence.MatchingFallbackCalculationInputDigest(calculationContext)
	if err != nil {
		t.Fatalf("digest writer context: %v", err)
	}
	// A caller can make a forged context internally self-consistent by changing
	// even an ignored manifest field and recomputing the V2 digest. The writer
	// must bind the supplied bytes to authority reconstructed inside its tx.
	forgedContext := calculationContext
	forgedContext.LookupReceipts = append([]persistence.MatchingFallbackLookupReceipt(nil), calculationContext.LookupReceipts...)
	forgedLookup := &forgedContext.LookupReceipts[0]
	var forgedProjection persistence.ProviderMatchingSnapshot
	if err := json.Unmarshal(forgedLookup.AcceptedProjection, &forgedProjection); err != nil || len(forgedProjection.Releases) == 0 || len(forgedProjection.Releases[0].Tracks) == 0 {
		t.Fatalf("decode accepted projection for forged context: err=%v", err)
	}
	forgedProjection.Releases[0].Tracks[0].RecordingTitle += " forged"
	forgedLookup.AcceptedProjection, err = json.Marshal(forgedProjection)
	if err != nil {
		t.Fatalf("encode forged accepted projection: %v", err)
	}
	projectionDigest := sha256.Sum256(forgedLookup.AcceptedProjection)
	forgedLookup.ProjectionSHA256 = hex.EncodeToString(projectionDigest[:])
	forgedContextJSON, err := json.Marshal(forgedContext)
	if err != nil {
		t.Fatalf("serialize forged context: %v", err)
	}
	forgedDigest, err := persistence.MatchingFallbackCalculationInputDigest(forgedContext)
	if err != nil {
		t.Fatalf("digest forged context: %v", err)
	}
	type calculationEdge struct {
		MemberID            uuid.UUID `json:"member_id"`
		WorkID              uuid.UUID `json:"work_id"`
		MetadataSHA         string    `json:"metadata_sha256"`
		ReleaseID           string    `json:"release_id"`
		RecordingID         string    `json:"recording_id"`
		ProviderTrackID     uuid.UUID `json:"provider_track_id"`
		Medium              int       `json:"medium"`
		Position            int       `json:"position"`
		Profile             string    `json:"profile"`
		Confidence          float64   `json:"confidence"`
		LookupRequestID     uuid.UUID `json:"lookup_request_id"`
		LookupPayloadSHA    string    `json:"lookup_payload_sha256"`
		LookupProjectionSHA string    `json:"lookup_projection_sha256"`
	}
	forgedEdges := make([]calculationEdge, 0, len(calculation.AllCandidates))
	for _, candidate := range calculation.AllCandidates {
		var requestID uuid.UUID
		for _, lookup := range calculationContext.Lookups {
			if lookup.ReleaseID == candidate.ReleaseID {
				requestID = lookup.RequestID
				break
			}
		}
		projectionSHA := candidate.LookupProjectionSHA
		if requestID == forgedLookup.RequestID {
			projectionSHA = forgedLookup.ProjectionSHA256
		}
		forgedEdges = append(forgedEdges, calculationEdge{MemberID: candidate.MemberID, WorkID: candidate.FrozenWorkID,
			MetadataSHA: candidate.MetadataInputSHA, ReleaseID: candidate.ReleaseID, RecordingID: candidate.RecordingID,
			ProviderTrackID: candidate.ProviderTrackUUID, Medium: candidate.Medium, Position: candidate.Position,
			Profile: string(candidate.Score.Profile), Confidence: candidate.Score.Confidence, LookupRequestID: requestID,
			LookupPayloadSHA: candidate.LookupPayloadSHA, LookupProjectionSHA: projectionSHA})
	}
	forgedEdgesJSON, err := json.Marshal(forgedEdges)
	if err != nil {
		t.Fatalf("serialize forged context edges: %v", err)
	}
	if recorded, err := freshRepository.RecordMatchingFallbackCalculationForDelivery(ctx, parentID, 1, newWakeID, freshClaim, complete.Revision,
		persistence.MatchingFallbackCalculationReceipt{RunID: parentID, AcquisitionRevision: complete.Revision,
			InputDigest: forgedDigest, Context: forgedContextJSON, Edges: forgedEdgesJSON}); err == nil || recorded {
		t.Fatalf("internally consistent forged context was accepted: recorded=%v err=%v", recorded, err)
	}
	var forgedReceiptCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=?`, parentID, complete.Revision).Scan(ctx, &forgedReceiptCount); err != nil || forgedReceiptCount != 0 {
		t.Fatalf("forged context wrote a receipt: count=%d err=%v", forgedReceiptCount, err)
	}
	if recorded, err := freshRepository.RecordMatchingFallbackCalculationForDelivery(ctx, parentID, 1, newWakeID, freshClaim, complete.Revision,
		persistence.MatchingFallbackCalculationReceipt{RunID: parentID, AcquisitionRevision: complete.Revision, InputDigest: contextDigest, Context: contextJSON, Edges: json.RawMessage(`[]`)}); err == nil || recorded {
		t.Fatalf("omitted edge universe was accepted: recorded=%v err=%v", recorded, err)
	}
	var prewriteReceiptCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=?`, parentID, complete.Revision).Scan(ctx, &prewriteReceiptCount); err != nil || prewriteReceiptCount != 0 {
		t.Fatalf("invalid omission wrote a receipt: count=%d err=%v", prewriteReceiptCount, err)
	}
	receiptResult, exists, err := service.PrepareFallbackApplicabilityReceipt(ctx, freshRepository, freshRepository, parentID, 1, newWakeID, freshClaim, complete.Revision)
	if err != nil || !exists || len(receiptResult.AllCandidates) != 4 {
		t.Fatalf("record complete applicability receipt: exists=%v candidates=%d err=%v", exists, len(receiptResult.AllCandidates), err)
	}
	if qualified {
		qualifying := false
		for _, candidate := range receiptResult.AllCandidates {
			qualifying = qualifying || candidate.Score.Confidence >= 0.70
		}
		if !qualifying || len(receiptResult.AllCandidates) != 4 {
			t.Fatalf("expected a genuine qualified edge among four calculated edges: %+v", receiptResult.AllCandidates)
		}
	}
	var receiptCountAfterWrite int
	var receiptContext, receiptEdges []byte
	if err := fixture.db.NewRaw(`SELECT count(*),context,edge_calculations FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=? GROUP BY context,edge_calculations`, parentID, complete.Revision).Scan(ctx, &receiptCountAfterWrite, &receiptContext, &receiptEdges); err != nil || receiptCountAfterWrite != 1 {
		t.Fatalf("read written calculation receipt: count=%d err=%v", receiptCountAfterWrite, err)
	}
	var writtenEdges []json.RawMessage
	if err := json.Unmarshal(receiptEdges, &writtenEdges); err != nil || len(writtenEdges) != 4 {
		t.Fatalf("receipt did not store all four edges: edges=%s err=%v", receiptEdges, err)
	}
	var beforeWriteRevision int64
	var beforeWritePhase string
	var beforeWriteConsumed bool
	var beforeWriteOperationState string
	var beforeWriteJobs, beforeWriteDependencies int
	if err := fixture.db.NewRaw(`SELECT revision,current_phase FROM matching_acquisition WHERE run_id=?`, parentID).Scan(ctx, &beforeWriteRevision, &beforeWritePhase); err != nil {
		t.Fatalf("read acquisition after receipt write: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, parentID).Scan(ctx, &beforeWriteOperationState); err != nil {
		t.Fatalf("read operation after receipt write: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`).Scan(ctx, &beforeWriteJobs); err != nil {
		t.Fatalf("count matching jobs after receipt write: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &beforeWriteDependencies); err != nil {
		t.Fatalf("count matching dependencies after receipt write: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, newWakeID).Scan(ctx, &beforeWriteConsumed); err != nil || beforeWriteConsumed {
		t.Fatalf("receipt writer consumed active delivery: consumed=%v err=%v", beforeWriteConsumed, err)
	}
	if _, exists, err := service.PrepareFallbackApplicabilityReceipt(ctx, freshRepository, freshRepository, parentID, 1, newWakeID, freshClaim, complete.Revision); err != nil || !exists {
		t.Fatalf("exact calculation receipt replay failed: exists=%v err=%v", exists, err)
	}
	var replayCount int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=?`, parentID, complete.Revision).Scan(ctx, &replayCount); err != nil || replayCount != 1 {
		t.Fatalf("exact replay inserted another receipt: count=%d err=%v", replayCount, err)
	}
	var changedEdges []map[string]any
	if err := json.Unmarshal(receiptEdges, &changedEdges); err != nil {
		t.Fatalf("decode receipt edges for conflict: %v", err)
	}
	changedEdges[0]["confidence"] = 0.01
	conflictingEdges, _ := json.Marshal(changedEdges)
	var storedContext persistence.MatchingFallbackCalculationContext
	if err := json.Unmarshal(receiptContext, &storedContext); err != nil {
		t.Fatalf("decode stored calculation context: %v", err)
	}
	digest, err := persistence.MatchingFallbackCalculationInputDigest(storedContext)
	if err != nil {
		t.Fatalf("digest stored calculation context: %v", err)
	}
	_, err = freshRepository.RecordMatchingFallbackCalculationForDelivery(ctx, parentID, 1, newWakeID, freshClaim, complete.Revision,
		persistence.MatchingFallbackCalculationReceipt{RunID: parentID, AcquisitionRevision: complete.Revision, InputDigest: digest, Context: receiptContext, Edges: conflictingEdges})
	if err == nil {
		t.Fatal("conflicting valid-confidence receipt replay was accepted")
	}
	staleClaim := freshClaim
	staleClaim.ExecutionEpoch++
	if recorded, err := freshRepository.RecordMatchingFallbackCalculationForDelivery(ctx, parentID, 1, newWakeID, staleClaim, complete.Revision,
		persistence.MatchingFallbackCalculationReceipt{RunID: parentID, AcquisitionRevision: complete.Revision, InputDigest: digest, Context: receiptContext, Edges: receiptEdges}); err != nil || recorded {
		t.Fatalf("stale delivery was not rejected before receipt deduplication: recorded=%v err=%v", recorded, err)
	}
	var afterWriteRevision int64
	var afterWritePhase string
	var afterWriteConsumed bool
	var afterWriteOperationState string
	var afterWriteJobs, afterWriteDependencies int
	if err := fixture.db.NewRaw(`SELECT revision,current_phase FROM matching_acquisition WHERE run_id=?`, parentID).Scan(ctx, &afterWriteRevision, &afterWritePhase); err != nil {
		t.Fatalf("read acquisition after replay checks: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, newWakeID).Scan(ctx, &afterWriteConsumed); err != nil {
		t.Fatalf("read delivery after replay checks: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT state FROM operation WHERE id=?`, parentID).Scan(ctx, &afterWriteOperationState); err != nil {
		t.Fatalf("read operation after replay checks: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`).Scan(ctx, &afterWriteJobs); err != nil {
		t.Fatalf("count matching jobs after replay checks: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &afterWriteDependencies); err != nil {
		t.Fatalf("count matching dependencies after replay checks: %v", err)
	}
	if beforeWriteRevision != afterWriteRevision || beforeWritePhase != afterWritePhase || beforeWriteOperationState != afterWriteOperationState || beforeWriteJobs != afterWriteJobs || beforeWriteDependencies != afterWriteDependencies || afterWriteConsumed {
		t.Fatalf("receipt writes changed operation/acquisition/delivery: revision=%d/%d phase=%q/%q operation=%q/%q jobs=%d/%d dependencies=%d/%d consumed=%v", beforeWriteRevision, afterWriteRevision, beforeWritePhase, afterWritePhase, beforeWriteOperationState, afterWriteOperationState, beforeWriteJobs, afterWriteJobs, beforeWriteDependencies, afterWriteDependencies, afterWriteConsumed)
	}
	if qualified {
		var operationsBefore, jobsBefore, dependenciesBefore, registrationsBefore, phasesBefore, deliveriesBefore int
		if err := fixture.db.NewRaw(`SELECT
			(SELECT count(*) FROM operation),
			(SELECT count(*) FROM river_job),
			(SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?),
			(SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?),
			(SELECT count(*) FROM matching_acquisition_phase WHERE run_id=?),
			(SELECT count(*) FROM matching_delivery WHERE run_id=?)`,
			parentID, parentID, parentID, parentID).
			Scan(ctx, &operationsBefore, &jobsBefore, &dependenciesBefore, &registrationsBefore, &phasesBefore, &deliveriesBefore); err != nil {
			t.Fatalf("read state before qualified advancement: %v", err)
		}
		advanced, err := freshRepository.AdvanceFallbackApplicabilityForDelivery(ctx, client, parentID, 1, newWakeID, freshClaim, complete.Revision, nil)
		if err != nil || !advanced {
			t.Fatalf("qualified calculation did not advance to policy wait: advanced=%v err=%v", advanced, err)
		}
		qualifiedSnapshot, exists, err := freshRepository.GetMatchingAcquisition(ctx, parentID)
		if err != nil || !exists || qualifiedSnapshot.Phase != "pending_seal" || qualifiedSnapshot.Revision != complete.Revision+1 {
			t.Fatalf("qualified calculation did not enter pending_seal: snapshot=%+v exists=%v err=%v", qualifiedSnapshot, exists, err)
		}
		sealedContext, found, err := freshRepository.LoadMatchingFallbackSealedCalculationContext(ctx, parentID)
		if err != nil || !found {
			t.Fatalf("sealed calculation context was not readable after cursor advance: found=%v err=%v", found, err)
		}
		calculationReceipt, err := freshRepository.GetMatchingFallbackCalculationReceipt(ctx, parentID, complete.Revision)
		var acceptedContext persistence.MatchingFallbackCalculationContext
		unmarshalErr := error(nil)
		if calculationReceipt != nil {
			unmarshalErr = json.Unmarshal(calculationReceipt.Context, &acceptedContext)
		}
		if err != nil || unmarshalErr != nil || calculationReceipt == nil || !reflect.DeepEqual(sealedContext, acceptedContext) {
			t.Fatalf("sealed calculation context differs from accepted calculation: receipt=%+v unmarshal_err=%v err=%v", calculationReceipt, unmarshalErr, err)
		}
		sealAuthority, authorityExists, err := freshRepository.BuildMatchingFallbackSealAuthority(ctx, parentID, qualifiedSnapshot.Revision)
		if err != nil || !authorityExists || sealAuthority.Disposition != "qualified" || sealAuthority.SelectedCalculation == nil ||
			sealAuthority.SelectedCalculation.AcquisitionRevision != complete.Revision || len(sealAuthority.FullEvaluationContext) == 0 || sealAuthority.Digest == "" {
			t.Fatalf("qualified seal authority was not derived from the saved calculation: authority=%+v exists=%v err=%v", sealAuthority, authorityExists, err)
		}
		var disposition struct {
			Disposition string `json:"disposition"`
			Reason      string `json:"reason"`
		}
		if err := json.Unmarshal(qualifiedSnapshot.Output, &disposition); err != nil || disposition.Disposition != "waiting_policy" || disposition.Reason != "qualified_candidate" {
			t.Fatalf("qualified disposition=%+v output=%s err=%v", disposition, qualifiedSnapshot.Output, err)
		}
		var finalRunState, finalOperationStage string
		var finalActiveJob *int64
		var consumed bool
		if err := fixture.db.NewRaw(`SELECT run.state,run.active_river_job_id,operation.stage,
			(SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?)
			FROM matching_run run JOIN operation ON operation.id=run.id WHERE run.id=?`, parentID, newWakeID, parentID).
			Scan(ctx, &finalRunState, &finalActiveJob, &finalOperationStage, &consumed); err != nil {
			t.Fatalf("read qualified delivery disposition: %v", err)
		}
		if finalRunState != "waiting_policy" || finalActiveJob != nil || finalOperationStage != "pending_seal" || !consumed {
			t.Fatalf("qualified disposition did not park and consume delivery: run=%q active=%v stage=%q consumed=%v", finalRunState, finalActiveJob, finalOperationStage, consumed)
		}
		var operationsAfter, jobsAfter, dependenciesAfter, registrationsAfter, phasesAfter, deliveriesAfter int
		if err := fixture.db.NewRaw(`SELECT
			(SELECT count(*) FROM operation),
			(SELECT count(*) FROM river_job),
			(SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?),
			(SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?),
			(SELECT count(*) FROM matching_acquisition_phase WHERE run_id=?),
			(SELECT count(*) FROM matching_delivery WHERE run_id=?)`,
			parentID, parentID, parentID, parentID).
			Scan(ctx, &operationsAfter, &jobsAfter, &dependenciesAfter, &registrationsAfter, &phasesAfter, &deliveriesAfter); err != nil {
			t.Fatalf("read state after qualified advancement: %v", err)
		}
		if operationsAfter != operationsBefore || jobsAfter != jobsBefore || dependenciesAfter != dependenciesBefore || registrationsAfter != registrationsBefore ||
			phasesAfter != phasesBefore+1 || deliveriesAfter != deliveriesBefore {
			t.Fatalf("qualified disposition admitted unexpected work: operations=%d/%d jobs=%d/%d dependencies=%d/%d registrations=%d/%d phases=%d/%d deliveries=%d/%d",
				operationsBefore, operationsAfter, jobsBefore, jobsAfter, dependenciesBefore, dependenciesAfter, registrationsBefore, registrationsAfter,
				phasesBefore, phasesAfter, deliveriesBefore, deliveriesAfter)
		}
		var priorJobs, priorDeliveries, priorDependencies int
		if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`).Scan(ctx, &priorJobs); err != nil {
			t.Fatalf("count jobs before stale qualified replay: %v", err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &priorDeliveries); err != nil {
			t.Fatalf("count deliveries before stale qualified replay: %v", err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &priorDependencies); err != nil {
			t.Fatalf("count dependencies before stale qualified replay: %v", err)
		}
		if replayed, err := freshRepository.AdvanceFallbackApplicabilityForDelivery(ctx, client, parentID, 1, newWakeID, freshClaim, complete.Revision, nil); err != nil || replayed {
			t.Fatalf("stale qualified delivery replay was not a no-op: changed=%v err=%v", replayed, err)
		}
		var finalJobs, finalDeliveries, finalDependencies int
		if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`).Scan(ctx, &finalJobs); err != nil {
			t.Fatalf("count jobs after stale qualified replay: %v", err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &finalDeliveries); err != nil {
			t.Fatalf("count deliveries after stale qualified replay: %v", err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &finalDependencies); err != nil {
			t.Fatalf("count dependencies after stale qualified replay: %v", err)
		}
		if priorJobs != finalJobs || priorDeliveries != finalDeliveries || priorDependencies != finalDependencies {
			t.Fatalf("stale qualified replay created duplicate work: jobs=%d/%d deliveries=%d/%d dependencies=%d/%d", priorJobs, finalJobs, priorDeliveries, finalDeliveries, priorDependencies, finalDependencies)
		}
		return
	}
	var tamperedInput struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}
	if err := json.Unmarshal(child.InputSnapshot, &tamperedInput); err != nil {
		t.Fatalf("decode original search operation input: %v", err)
	}
	tamperedInput.Query += " tampered"
	tamperedSearchInput, err := json.Marshal(tamperedInput)
	if err != nil {
		t.Fatalf("encode tampered search operation input: %v", err)
	}
	wrongSearchKind := "provider_release_search"
	if child.Kind == wrongSearchKind {
		wrongSearchKind = "provider_recording_search"
	}
	for _, mutation := range []struct {
		name  string
		query string
		args  []any
	}{
		{name: "input", query: `UPDATE operation SET input_snapshot=?::jsonb WHERE id=?`, args: []any{string(tamperedSearchInput), childID}},
		{name: "kind", query: `UPDATE operation SET kind=? WHERE id=?`, args: []any{wrongSearchKind, childID}},
	} {
		t.Run("tampered_search_operation_"+mutation.name, func(t *testing.T) {
			if _, err := fixture.db.NewRaw(mutation.query, mutation.args...).Exec(ctx); err != nil {
				t.Fatalf("tamper search operation: %v", err)
			}
			result, exists, err := service.PrepareFallbackApplicability(ctx, freshRepository, parentID, complete.Revision)
			if err == nil || exists || len(result.AllCandidates) != 0 {
				t.Fatalf("tampered search operation was accepted: exists=%v candidates=%d err=%v", exists, len(result.AllCandidates), err)
			}
			var restoreQuery string
			var restoreArgs []any
			switch mutation.name {
			case "input":
				restoreQuery = `UPDATE operation SET input_snapshot=?::jsonb WHERE id=?`
				restoreArgs = []any{string(child.InputSnapshot), childID}
			case "kind":
				restoreQuery = `UPDATE operation SET kind=? WHERE id=?`
				restoreArgs = []any{child.Kind, childID}
			}
			if _, err := fixture.db.NewRaw(restoreQuery, restoreArgs...).Exec(ctx); err != nil {
				t.Fatalf("restore search operation: %v", err)
			}
		})
	}
	if _, exists, err := service.PrepareFallbackApplicability(ctx, freshRepository, parentID, complete.Revision-1); err != nil || exists {
		t.Fatalf("stale applicability revision was not rejected: exists=%v err=%v", exists, err)
	}
	if len(plan.Stages) < 2 {
		t.Fatal("fallback fixture must include a next query stage")
	}
	nextStage := plan.Stages[1]
	nextKind, _, ok := policy.ProviderKindForFallbackStage(nextStage.Kind)
	if !ok {
		t.Fatalf("next fallback stage %q has no provider kind", nextStage.Kind)
	}
	prepared, err := fetch.PrepareOperation(ctx, strings.TrimPrefix(nextKind, "provider_"), nextStage.Query, 0, 10, false)
	if err != nil {
		t.Fatalf("prepare immutable next-stage search: %v", err)
	}
	nextSearch := persistence.MatchingProviderRequest{Operation: prepared, Args: service.ProviderFetchOperationArgs{OperationID: prepared.ID}}
	for _, invalid := range []struct {
		name string
		args river.JobArgs
	}{
		{name: "wrong_operation_id", args: service.ProviderFetchOperationArgs{OperationID: uuid.New()}},
		{name: "wrong_kind", args: fallbackWrongKindJobArgs{}},
	} {
		t.Run("reject_prepared_search_"+invalid.name, func(t *testing.T) {
			var before struct {
				Operations, Dependencies, Registrations, Jobs, Phases int
				Revision                                              int64
				Consumed                                              bool
			}
			if err := fixture.db.NewRaw(`SELECT
				(SELECT count(*) FROM operation),
				(SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?),
				(SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?),
				(SELECT count(*) FROM river_job),
				(SELECT count(*) FROM matching_acquisition_phase WHERE run_id=?),
				(SELECT revision FROM matching_acquisition WHERE run_id=?),
				(SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?)`,
				parentID, parentID, parentID, parentID, parentID, newWakeID).
				Scan(ctx, &before.Operations, &before.Dependencies, &before.Registrations, &before.Jobs, &before.Phases, &before.Revision, &before.Consumed); err != nil {
				t.Fatalf("read state before rejected advance: %v", err)
			}
			bad := nextSearch
			bad.Args = invalid.args
			if changed, err := freshRepository.AdvanceFallbackApplicabilityForDelivery(ctx, client, parentID, 1, newWakeID, freshClaim, complete.Revision, &bad); err == nil || changed {
				t.Fatalf("invalid prepared search arguments were accepted: changed=%v err=%v", changed, err)
			}
			var after struct {
				Operations, Dependencies, Registrations, Jobs, Phases int
				Revision                                              int64
				Consumed                                              bool
			}
			if err := fixture.db.NewRaw(`SELECT
				(SELECT count(*) FROM operation),
				(SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?),
				(SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?),
				(SELECT count(*) FROM river_job),
				(SELECT count(*) FROM matching_acquisition_phase WHERE run_id=?),
				(SELECT revision FROM matching_acquisition WHERE run_id=?),
				(SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?)`,
				parentID, parentID, parentID, parentID, parentID, newWakeID).
				Scan(ctx, &after.Operations, &after.Dependencies, &after.Registrations, &after.Jobs, &after.Phases, &after.Revision, &after.Consumed); err != nil {
				t.Fatalf("read state after rejected advance: %v", err)
			}
			if before != after {
				t.Fatalf("rejected advance changed durable state: before=%+v after=%+v", before, after)
			}
		})
	}
	advanced, err := freshRepository.AdvanceFallbackApplicabilityForDelivery(ctx, client, parentID, 1, newWakeID, freshClaim, complete.Revision, &nextSearch)
	if err != nil || !advanced {
		t.Fatalf("advance saved zero-confidence calculation to next stage: advanced=%v err=%v", advanced, err)
	}
	stageOne, exists, err := freshRepository.GetMatchingAcquisition(ctx, parentID)
	if err != nil || !exists || stageOne.Phase != persistence.MatchingAcquisitionFallbackSearch || stageOne.Revision != complete.Revision+1 {
		t.Fatalf("next-stage phase was not committed: snapshot=%+v exists=%v err=%v", stageOne, exists, err)
	}
	registeredNext, err := freshRepository.GetOperation(ctx, prepared.ID)
	if err != nil || registeredNext.RiverJobID == nil {
		t.Fatalf("next-stage provider operation was not enqueued: operation=%+v err=%v", registeredNext, err)
	}
	var nextDependency, nextRegistration, oldConsumed bool
	if err := fixture.db.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_provider_dependency WHERE parent_run_id=? AND request_id=? AND cache_key=?),
		EXISTS(SELECT 1 FROM matching_fallback_request_registration WHERE run_id=? AND stage_index=1 AND role='search' AND request_id=?),
		(SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?)`, parentID, prepared.ID, *prepared.ProviderCacheKey,
		parentID, prepared.ID, parentID, newWakeID).Scan(ctx, &nextDependency, &nextRegistration, &oldConsumed); err != nil {
		t.Fatalf("verify stage-one dependency, registration, and parent consumption: %v", err)
	}
	if !nextDependency || !nextRegistration || !oldConsumed {
		t.Fatalf("next-stage transaction incomplete: dependency=%v registration=%v oldWakeConsumed=%v", nextDependency, nextRegistration, oldConsumed)
	}
	var beforeReplayJobs, beforeReplayDeliveries int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`).Scan(ctx, &beforeReplayJobs); err != nil {
		t.Fatalf("count jobs before stale replay: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &beforeReplayDeliveries); err != nil {
		t.Fatalf("count deliveries before stale replay: %v", err)
	}
	if replayed, err := freshRepository.AdvanceFallbackApplicabilityForDelivery(ctx, client, parentID, 1, newWakeID, freshClaim, complete.Revision, &nextSearch); err != nil || replayed {
		t.Fatalf("stale applicability delivery replay was not a no-op: changed=%v err=%v", replayed, err)
	}
	var afterReplayJobs, afterReplayDeliveries int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='matching_evaluation_v1'`).Scan(ctx, &afterReplayJobs); err != nil {
		t.Fatalf("count jobs after stale replay: %v", err)
	}
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &afterReplayDeliveries); err != nil {
		t.Fatalf("count deliveries after stale replay: %v", err)
	}
	if beforeReplayJobs != afterReplayJobs || beforeReplayDeliveries != afterReplayDeliveries {
		t.Fatalf("stale replay created duplicate work: jobs=%d/%d deliveries=%d/%d", beforeReplayJobs, afterReplayJobs, beforeReplayDeliveries, afterReplayDeliveries)
	}
}
