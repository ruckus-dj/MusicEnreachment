//go:build integration

package jobs

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestLocalFallbackAndMusicBrainzRecoveryWithoutCurrentPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAnalysisDispatchFixtureWithMetadata(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	analysis := fixture.startWithMetadata(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *analysis.RiverJobID)
	stopRiverClient(t, fixture.client)
	path := filepath.Join(fixture.source, "album", "track.flac")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const artistMBID = "33333333-3333-4333-8333-333333333333"
	const releaseMBID = "44444444-4444-4444-8444-444444444444"
	var healthy atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimSuffix(r.URL.Path, "/") {
		case "/ws/2/artist":
			_, _ = fmt.Fprintf(w, `{"count":1,"offset":0,"created":"2026-01-01T00:00:00Z","artists":[{"id":%q,"name":"Fixture Artist"}]}`, artistMBID)
		case "/ws/2/release":
			_, _ = fmt.Fprintf(w, `{"release-count":1,"release-offset":0,"releases":[{"id":%q,"title":"Fixture Album"}]}`, releaseMBID)
		case "/ws/2/release/" + releaseMBID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Fixture Album","artist-credit":[{"name":"Fixture Artist","artist":{"id":%q,"name":"Fixture Artist"}}],"media":[{"position":1,"tracks":[{"id":"55555555-5555-4555-8555-555555555555","number":"1","title":"Fixture Track","recording":{"id":"66666666-6666-4666-8666-666666666666","title":"Fixture Track","artist-credit":[{"name":"Fixture Artist","artist":{"id":%q,"name":"Fixture Artist"}}]}}]}]}`, releaseMBID, artistMBID, artistMBID)
		default:
			t.Errorf("unexpected MusicBrainz request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if err := fixture.registry.SetMusicBrainzConfig(ctx, "self-hosted", server.URL+"/ws/2"); err != nil {
		t.Fatal(err)
	}
	db := fixture.database
	catalog := service.NewLocalProviderCatalog(persistence.NewLocalProviderRepository(db), nil)
	artist, err := catalog.Create(ctx, "artist", service.LocalProviderItem{Name: "Fixture Artist"})
	if err != nil {
		t.Fatal(err)
	}
	credits := []service.LocalProviderCredit{{ArtistID: artist.ID, Name: "Fixture Artist"}}
	// Local is eligible, but the exact MusicBrainz track title scores higher.
	recording, err := catalog.Create(ctx, "recording", service.LocalProviderItem{Title: "Fixture Tracks", Credits: credits})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Create(ctx, "release", service.LocalProviderItem{
		Title: "Fixture Album", ReleaseGroupTitle: "Fixture Album", Credits: credits,
		Tracks: []service.LocalProviderTrack{{RecordingID: recording.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	grouping := persistence.NewIncomingGroupingRepository(db)
	groups := service.NewIncomingGroups(grouping)
	if err := groups.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	providerCache := persistence.NewProviderCacheRepository(db)
	providerFactory := service.NewProviderFactory(fixture.registry, nil, nil, server.Client(), nil)
	fetch := service.NewProviderFetchService(fixture.registry, providerFactory, providerCache)
	candidates := persistence.NewIncomingMatchCandidateRepository(db)
	inserter := &incomingMatchingReconcileInserterSlot{}
	operations := service.NewOperationsWithRiver(fixture.setup, inserter)
	reconcile := service.NewIncomingMatchingReconciliation(groups, grouping, fetch, fixture.setup, inserter, fixture.inventory).
		WithLocalFallback(candidates, service.NewMatchingLocalCatalogSnapshotService(persistence.NewMatchingLocalCatalogRepository(db)))
	reconcileWorker := NewIncomingMatchingReconcileWorker(fixture.inventory, reconcile)
	evaluator := service.NewMatchingEvaluationService(groups, grouping, providerCache, candidates, fixture.registry)
	matchingWorker := NewMatchingEvaluationWorker(fixture.setup, evaluator, operations).
		WithAcquisition(service.NewMatchingAcquisitionService(fixture.setup, fetch, inserter))
	providerWorker := NewProviderFetchWorker(fetch, operations)
	client, pool, err := PrepareWithWorkers(ctx, fixture.databaseURL, db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, reconcileWorker)
		river.AddWorker(workers, matchingWorker)
		river.AddWorker(workers, providerWorker)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	inserter.inserter = client
	fetch.WithDurableOperations(fixture.setup, client)
	fixture.setup.SetRiverInserter(client)
	deliverReconciliation := func(eventID uuid.UUID) {
		t.Helper()
		if err := fixture.inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
			t.Fatal(err)
		}
		event, err := fixture.inventory.GetIncomingMatchingReconciliationEvent(ctx, eventID)
		if err != nil || event == nil || event.RegisteredJobID == nil {
			t.Fatalf("read exact reconciliation delivery: %v", err)
		}
		jobID := *event.RegisteredJobID
		setMatchingRiverJobRunning(t, ctx, db, jobID)
		if err := reconcileWorker.Work(ctx, &river.Job[persistence.IncomingMatchingReconcileJobArgs]{
			JobRow: &rivertype.JobRow{ID: jobID, Attempt: 1}, Args: persistence.IncomingMatchingReconcileJobArgs{EventID: eventID},
		}); err != nil {
			t.Fatal(err)
		}
	}
	var eventID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM incoming_matching_reconcile_event WHERE reason='analysis_success' AND source_operation_id=?`,
		analysis.ID).Scan(ctx, &eventID); err != nil {
		t.Fatal(err)
	}
	deliverReconciliation(eventID)
	var parentID, childID uuid.UUID
	var childJob int64
	if err := db.NewRaw(`SELECT d.parent_run_id,d.request_id,o.river_job_id FROM matching_provider_dependency d
		JOIN operation o ON o.id=d.request_id WHERE o.kind='provider_artist_search'`).Scan(ctx, &parentID, &childID, &childJob); err != nil {
		t.Fatal(err)
	}
	setMatchingRiverJobRunning(t, ctx, db, childJob)
	if err := providerWorker.Work(ctx, &river.Job[service.ProviderFetchOperationArgs]{
		JobRow: &rivertype.JobRow{ID: childJob, Attempt: 1}, Args: service.ProviderFetchOperationArgs{OperationID: childID},
	}); err != nil {
		t.Fatal(err)
	}
	child, err := fixture.setup.GetOperation(ctx, childID)
	if err != nil || child.State != "failed" || child.Stage != persistence.MusicBrainzUnavailableStage {
		t.Fatalf("provider did not settle a terminal outage: operation=%+v err=%v", child, err)
	}
	var matchingJob int64
	if err := db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL
		ORDER BY river_job_id DESC LIMIT 1`, parentID).Scan(ctx, &matchingJob); err != nil {
		t.Fatal(err)
	}
	if matchingJob < 1 {
		t.Fatalf("outage did not wake matching run %s (job %d)", parentID, matchingJob)
	}
	setMatchingRiverJobRunning(t, ctx, db, matchingJob)
	if err := matchingWorker.Work(ctx, &river.Job[service.MatchingEvaluationOperationArgs]{
		JobRow: &rivertype.JobRow{ID: matchingJob, Attempt: 1}, Args: service.MatchingEvaluationOperationArgs{OperationID: parentID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT id FROM incoming_matching_reconcile_event WHERE reason='current' AND state='pending'`).Scan(ctx, &eventID); err != nil {
		t.Fatalf("find terminal outage wake: %v", err)
	}
	deliverReconciliation(eventID)
	var recheckID, associationID uuid.UUID
	if err := db.NewRaw(`SELECT r.id,(r.incumbent_snapshot->'sources'->0->>'track_source_id')::uuid FROM matching_local_recheck r
		JOIN matching_local_recheck_source s ON s.recheck_id=r.id WHERE s.work_id=? AND s.source_location_id=? AND r.state='waiting'`,
		fixture.work.ID, fixture.track.ID).Scan(ctx, &recheckID, &associationID); err != nil {
		t.Fatalf("read applied Local fallback and exact source veto: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE matching_local_recheck SET next_recheck_at=now() WHERE id=?`, recheckID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	healthy.Store(true)
	runner := service.NewMatchingLocalRecheckService(candidates, fetch)
	worker := NewMatchingLocalRecheckWorker(runner)
	if err := worker.Work(ctx, &river.Job[MatchingLocalRecheckArgs]{}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Local, MusicBrainz, Recheck, Veto, Attempt, Published bool
	}
	if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM track_source WHERE id=?) AS local,
		EXISTS(SELECT 1 FROM track_source s
			JOIN provider_local_track binding ON binding.local_track_id=s.track_id
			JOIN provider p ON p.id=binding.provider_id AND p.code='musicbrainz'
			JOIN source_location location ON location.media_variant_id=s.media_variant_id
			WHERE s.decision_method='automatic' AND location.id=?) AS music_brainz,
		EXISTS(SELECT 1 FROM matching_local_recheck WHERE id=?) AS recheck,
		EXISTS(SELECT 1 FROM matching_local_recheck_source WHERE work_id=?) AS veto,
		EXISTS(SELECT 1 FROM track_publication_attempt) AS attempt,
		EXISTS(SELECT 1 FROM track_publication) AS published`, associationID, fixture.track.ID, recheckID, fixture.work.ID).Scan(ctx, &result); err != nil {
		t.Fatal(err)
	}
	if result.Local || !result.MusicBrainz || result.Recheck || result.Veto || result.Attempt || result.Published {
		t.Fatalf("strictly better MusicBrainz result did not transfer the assignment without publishing: %+v", result)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("Local outage/recovery modified the original file: %v", err)
	}
}
