//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type manualCacheReuseRiverInserter struct {
	client persistence.RiverInserter
}

func (inserter *manualCacheReuseRiverInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if inserter.client == nil {
		return nil, fmt.Errorf("manual cache reuse River client is not ready")
	}
	return inserter.client.InsertTx(ctx, tx, args, options)
}

func TestManualLookupCacheReuseRunsProviderWorkerAndSealsExactResponsePostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newIncomingMatchFixture(t, ctx, db)
	selected := uuid.MustParse(fixture.releaseKeys[0])

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + selected.String() + `","title":"unexpected provider fetch"}`))
	}))
	defer server.Close()

	repository := persistence.NewSetupManagerRepository(db)
	cache := persistence.NewProviderCacheRepository(db)
	registry := settings.New(persistence.NewSettingsRepository(db), nil)
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", server.URL+"/ws/2"); err != nil {
		t.Fatal(err)
	}
	config, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := cache.ResolveMusicBrainzSource(ctx, server.URL+"/ws/2", config.Identity)
	if err != nil {
		t.Fatalf("resolve configured MusicBrainz source: %v", err)
	}
	cacheKey := "release-lookup:" + selected.String()
	payloadA := json.RawMessage(`{"id":"` + selected.String() + `","title":"Fixture Album","artist-credit":[{"name":"Fixture Artist","artist":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"Fixture Artist"}}],"media":[{"position":1,"tracks":[{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","number":"1","recording":{"id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","title":"Fixture Track"}}]}]}`)
	ticket, err := cache.BeginRefresh(ctx, source.ID, cacheKey, config.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payloadA, Graph: fixture.graph,
	}); err != nil {
		t.Fatalf("prepopulate complete accepted release response: %v", err)
	}
	acceptedResponse, err := cache.GetCurrentCachedResponse(ctx, source.ID, cacheKey, config.Identity)
	if err != nil || acceptedResponse == nil {
		t.Fatalf("read accepted cache response: response=%+v err=%v", acceptedResponse, err)
	}
	payloadA = acceptedResponse.Payload

	grouping := persistence.NewIncomingGroupingRepository(db)
	state, err := grouping.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	group := fixture.group
	requestID, childID := uuid.New(), uuid.New()
	identity := config.Identity
	child := persistence.MatchingProviderRequest{
		Operation: &persistence.Operation{
			ID: childID, Kind: "provider_release_lookup", State: "queued", Stage: "queued",
			InputSnapshot:    json.RawMessage(`{"schema_version":1,"mbid":"` + selected.String() + `"}`),
			ProviderSourceID: &source.ID, ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint,
			ProviderConfigurationIdentity: &identity, ProviderCacheKey: &cacheKey,
		},
		Args: service.ProviderFetchOperationArgs{OperationID: childID},
	}
	plan := persistence.ManualMatchingRunPlan{
		GroupingEpoch: state.Revision, PolicyVersion: service.MatchingPolicyVersion,
		Plan:          json.RawMessage(`{"schema_version":1,"mode":"manual_review"}`),
		GroupContext:  json.RawMessage(`{"group_id":"` + group.ID.String() + `","revision":"` + group.Revision + `"}`),
		SourceContext: json.RawMessage(`{"provider_id":"` + source.ProviderID.String() + `","provider_source_id":"` + source.ID.String() + `","endpoint":"` + source.Endpoint + `","configuration_identity":"` + identity + `"}`),
	}
	riverInserter := &manualCacheReuseRiverInserter{}
	operations := service.NewOperationsWithRiver(repository, riverInserter)
	factory := service.NewProviderFactory(registry, nil, nil, server.Client(), nil)
	fetch := service.NewProviderFetchService(registry, factory, cache)
	providerWorker := jobs.NewProviderFetchWorker(fetch, operations)
	matchingWorker := jobs.NewMatchingEvaluationWorker(repository,
		service.NewMatchingEvaluationService(service.NewIncomingGroups(grouping), grouping, cache,
			persistence.NewIncomingMatchCandidateRepository(db), registry), operations)
	client, listenerPool, err := jobs.PrepareWithWorkers(ctx, testpostgres.URL(t, db), db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, providerWorker)
		river.AddWorker(workers, matchingWorker)
	})
	if err != nil {
		t.Fatalf("prepare registered provider worker: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	riverInserter.client = client
	fetch.WithDurableOperations(repository, client)
	parent, err := repository.AdmitManualReleaseMatchingRun(ctx, client, requestID, group.ID, group.Revision, nil, selected, child, plan)
	if err != nil {
		t.Fatalf("admit manual release lookup: %v", err)
	}
	if parent.RiverJobID == nil || child.Operation.RiverJobID == nil {
		t.Fatal("manual admission did not enqueue parent and lookup jobs")
	}

	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{
		JobRow: &rivertype.JobRow{ID: *child.Operation.RiverJobID, Attempt: 1},
		Args:   service.ProviderFetchOperationArgs{OperationID: childID},
	}); err != nil {
		t.Fatalf("run admitted cache-hit provider job: %v", err)
	}
	completed, err := repository.GetOperation(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "succeeded" || completed.Stage != "provider_cache_reused" || completed.FinishedAt == nil || completed.FinishedAt.IsZero() || completed.FinishedAt.Location() != time.UTC {
		t.Fatalf("cache reuse operation completion = state %q stage %q finished_at %v; want successful UTC completion", completed.State, completed.Stage, completed.FinishedAt)
	}
	if requests.Load() != 0 {
		t.Fatalf("cache-hit delivery made %d provider HTTP requests, want 0", requests.Load())
	}

	var sealedDigest []byte
	if err := db.NewRaw(`SELECT payload_sha256 FROM manual_matching_lookup_seal WHERE request_id=? AND sealed_at IS NOT NULL`, requestID).Scan(ctx, &sealedDigest); err != nil {
		t.Fatalf("read captured cache receipt: %v", err)
	}
	wantDigest := sha256.Sum256(payloadA)
	if string(sealedDigest) != string(wantDigest[:]) {
		t.Fatalf("sealed payload digest %x, want accepted cached response A %x", sealedDigest, wantDigest)
	}
}
