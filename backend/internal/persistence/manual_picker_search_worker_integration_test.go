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

type manualPickerSearchRiverInserter struct {
	client persistence.RiverInserter
}

func (inserter *manualPickerSearchRiverInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if inserter.client == nil {
		return nil, fmt.Errorf("manual picker search River client is not ready")
	}
	return inserter.client.InsertTx(ctx, tx, args, options)
}

func TestManualPickerSearchWorkerFreezesAcceptedArtistResultsPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newIncomingMatchFixture(t, ctx, db)

	artistA := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	artistB := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	responseA := []byte(fmt.Sprintf(`{"created":"2026-01-02T03:04:05Z","count":2,"offset":0,"artists":[{"id":%q,"name":"Artist A","sort-name":"Artist A","type":"Person","score":100},{"id":%q,"name":"Artist B","sort-name":"Artist B","type":"Group","score":91}]}`, artistA.String(), artistB.String()))
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/ws/2/artist/" || request.URL.Query().Get("query") != "artist" {
			t.Errorf("provider request URL = %s, want artist search query", request.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(responseA)
	}))
	defer server.Close()

	repository := persistence.NewSetupManagerRepository(db)
	cache := persistence.NewProviderCacheRepository(db)
	registry := settings.New(persistence.NewSettingsRepository(db), nil)
	endpoint := server.URL + "/ws/2"
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", endpoint); err != nil {
		t.Fatal(err)
	}
	config, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	group := fixture.group
	riverInserter := &manualPickerSearchRiverInserter{}
	operations := service.NewOperationsWithRiver(repository, riverInserter)
	factory := service.NewProviderFactory(registry, nil, nil, server.Client(), nil)
	fetch := service.NewProviderFetchService(registry, factory, cache)
	worker := jobs.NewProviderFetchWorker(fetch, operations)
	client, listenerPool, err := jobs.PrepareWithWorkers(ctx, testpostgres.URL(t, db), db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, worker)
	})
	if err != nil {
		t.Fatalf("prepare provider worker: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	riverInserter.client = client
	fetch.WithDurableOperations(repository, client)
	searches := service.NewManualPickerSearchService(repository, fetch, client)
	operation, err := searches.Start(ctx, group.ID, uuid.New(), group.Revision, "artist_search", "artist", 0, 2)
	if err != nil {
		t.Fatalf("admit manual artist search: %v", err)
	}
	if operation.RiverJobID == nil || operation.Attempt != 1 {
		t.Fatalf("manual artist search operation = %+v; want enqueued first-attempt operation", operation)
	}
	setManualMatchingRiverJobRunning(t, ctx, db, *operation.RiverJobID)
	if err := worker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{
		JobRow: &rivertype.JobRow{ID: *operation.RiverJobID, Attempt: operation.Attempt},
		Args:   service.ProviderFetchOperationArgs{OperationID: operation.ID},
	}); err != nil {
		t.Fatalf("run admitted manual artist search: %v", err)
	}
	completed, err := repository.GetOperation(ctx, operation.ID)
	if err != nil || completed.State != "succeeded" {
		t.Fatalf("manual artist search operation = %+v err=%v; want succeeded", completed, err)
	}
	if requests != 1 {
		t.Fatalf("provider HTTP requests = %d; want one", requests)
	}

	frozen, err := repository.GetManualPickerSearchResult(ctx, group.ID, operation.ID)
	if err != nil || frozen == nil {
		t.Fatalf("read frozen manual artist search result: result=%+v err=%v", frozen, err)
	}
	var payloadA struct {
		Count   int `json:"count"`
		Offset  int `json:"offset"`
		Artists []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artists"`
	}
	if err := json.Unmarshal(frozen.Payload, &payloadA); err != nil {
		t.Fatalf("decode frozen artist search payload: %v", err)
	}
	if frozen.Kind != "artist_search" || payloadA.Count != 2 || payloadA.Offset != 0 || len(payloadA.Artists) != 2 ||
		payloadA.Artists[0].ID != artistA.String() || payloadA.Artists[0].Name != "Artist A" ||
		payloadA.Artists[1].ID != artistB.String() || payloadA.Artists[1].Name != "Artist B" {
		t.Fatalf("frozen artist search result kind=%q payload=%+v; want both provider candidates", frozen.Kind, payloadA)
	}
	serialized, err := json.Marshal(frozen.Payload)
	if err != nil {
		t.Fatalf("serialize frozen API payload: %v", err)
	}
	digest := sha256.Sum256(serialized)
	if fmt.Sprintf("%x", digest) != frozen.PayloadSHA256 {
		t.Fatalf("frozen payload digest = %s; want %x for serialized payload", frozen.PayloadSHA256, digest)
	}

	cacheKey := "artist-search:0:2:artist"
	source, err := cache.ResolveMusicBrainzSource(ctx, endpoint, config.Identity)
	if err != nil {
		t.Fatalf("resolve configured MusicBrainz source: %v", err)
	}
	payloadB := json.RawMessage(`{"created":"2026-01-02T03:04:05Z","count":1,"offset":0,"artists":[{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"Replacement Artist","sort-name":"Replacement Artist","type":"Person","score":88}]}`)
	ticket, err := cache.BeginRefresh(ctx, source.ID, cacheKey, config.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payloadB,
	}); err != nil {
		t.Fatalf("replace artist search cache with response B: %v", err)
	}
	current, err := cache.GetCurrentCachedResponse(ctx, source.ID, cacheKey, config.Identity)
	if err != nil || current == nil {
		t.Fatalf("current cache response = %+v err=%v; want replacement response B", current, err)
	}
	var replacement struct {
		Count   int `json:"count"`
		Artists []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artists"`
	}
	if err := json.Unmarshal(current.Payload, &replacement); err != nil {
		t.Fatalf("decode replacement cache response: %v", err)
	}
	if replacement.Count != 1 || len(replacement.Artists) != 1 || replacement.Artists[0].ID != "cccccccc-cccc-4ccc-8ccc-cccccccccccc" || replacement.Artists[0].Name != "Replacement Artist" {
		t.Fatalf("cache did not contain response B: %+v", replacement)
	}
	stillFrozen, err := searches.Result(ctx, group.ID, operation.ID)
	if err != nil || stillFrozen == nil || stillFrozen.PayloadSHA256 != frozen.PayloadSHA256 || string(stillFrozen.Payload) != string(frozen.Payload) {
		t.Fatalf("manual result changed after cache replacement: result=%+v err=%v", stillFrozen, err)
	}
	wrongGroup, err := repository.GetManualPickerSearchResult(ctx, uuid.New(), operation.ID)
	if err != nil || wrongGroup != nil {
		t.Fatalf("wrong-group manual search result = %+v err=%v; want not found", wrongGroup, err)
	}
}
