//go:build integration

package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestLocalFallbackAndMusicBrainzRecoveryPostgreSQL(t *testing.T) {
	for _, name := range []string{"without CURRENT", "same position", "different position"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runLocalFallbackAndMusicBrainzRecovery(t, name)
		})
	}
}

func runLocalFallbackAndMusicBrainzRecovery(t *testing.T, scenario string) {
	t.Helper()
	fixture := newAnalysisDispatchFixtureWithMetadata(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	analysis := fixture.startWithMetadata(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *analysis.RiverJobID)
	stopRiverClient(t, fixture.client)
	if scenario != "same position" {
		prepareMatchingPublicationAdmissionFixture(t, ctx, fixture)
	}
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
	fallback := &captureLocalFallbackCommit{IncomingMatchCandidateRepository: candidates}
	inserter := &incomingMatchingReconcileInserterSlot{}
	operations := service.NewOperationsWithRiver(fixture.setup, inserter)
	reconcile := service.NewIncomingMatchingReconciliation(groups, grouping, fetch, fixture.setup, inserter, fixture.inventory).
		WithLocalFallback(fallback, service.NewMatchingLocalCatalogSnapshotService(persistence.NewMatchingLocalCatalogRepository(db)))
	reconcileWorker := NewIncomingMatchingReconcileWorker(fixture.inventory, reconcile)
	evaluator := service.NewMatchingEvaluationService(groups, grouping, providerCache, candidates, fixture.registry)
	matchingWorker := NewMatchingEvaluationWorker(fixture.setup, evaluator, operations).
		WithAcquisition(service.NewMatchingAcquisitionService(fixture.setup, fetch, inserter))
	providerWorker := NewProviderFetchWorker(fetch, operations)
	client, pool, err := PrepareWithWorkers(ctx, fixture.databaseURL, db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, reconcileWorker)
		river.AddWorker(workers, matchingWorker)
		river.AddWorker(workers, providerWorker)
		river.AddWorker(workers, &PublicationWorker{})
		river.AddWorker(workers, &PublicationAutoAlbumWorker{})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	inserter.inserter = client
	fetch.WithDurableOperations(fixture.setup, client)
	fixture.setup.SetRiverInserter(client)
	candidates.WithAutomaticAlbumPublication(client).
		WithAutomaticAlbumPublicationScorer(service.ScorePublicationMatchingLayoutCapture)
	fixture.setup.SetPublicationMatchingScorer(service.ScorePublicationMatchingCapture)
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
	if scenario == "without CURRENT" {
		// The qualifying Local fallback must observe the setting before it
		// commits its automatic album handoff.
		if err := fixture.settings.Set(ctx, settings.PublicationAutoCompleteAlbumsKey, "true"); err != nil {
			t.Fatal(err)
		}
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
	var recheckID, associationID, position, localApplyID, localReleaseID, providerTrackID uuid.UUID
	if err := db.NewRaw(`SELECT r.id,(r.incumbent_snapshot->'sources'->0->>'track_source_id')::uuid,
		(r.incumbent_snapshot->'sources'->0->>'release_track_id')::uuid,r.local_apply_operation_id,
		(r.incumbent_snapshot->>'local_release_id')::uuid,
		(r.incumbent_snapshot->'sources'->0->>'provider_track_id')::uuid FROM matching_local_recheck r
		JOIN matching_local_recheck_source s ON s.recheck_id=r.id WHERE s.work_id=? AND s.source_location_id=? AND r.state='waiting'`,
		fixture.work.ID, fixture.track.ID).Scan(ctx, &recheckID, &associationID, &position, &localApplyID, &localReleaseID, &providerTrackID); err != nil {
		t.Fatalf("read applied Local fallback and exact source veto: %v", err)
	}
	var automaticAlbumWakeID int64
	if scenario == "without CURRENT" {
		// The temporary Local assignment is the automatic-album owner.
		album, err := fixture.setup.ReadPublicationAutoAlbum(ctx, localApplyID)
		if err != nil || album == nil {
			t.Fatalf("Local apply did not store a readable automatic album request: %+v, %v", album, err)
		}
		var epoch uuid.UUID
		if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
			t.Fatal(err)
		}
		if album.ApplyOperationID != localApplyID || album.ReleaseID != localReleaseID || album.OutputEpoch != epoch ||
			len(album.Scope) != 1 || album.Scope[0].PositionID != position ||
			album.Scope[0].ProviderTrackID != providerTrackID ||
			!slices.Equal(album.Scope[0].WorkIDs, []uuid.UUID{fixture.work.ID}) {
			t.Fatalf("Local automatic album request=%+v; want apply=%s release=%s epoch=%s position=%s provider_track=%s works=[%s]",
				album, localApplyID, localReleaseID, epoch, position, providerTrackID, fixture.work.ID)
		}
		if _, err := db.NewRaw(`UPDATE publication_automatic_album SET local_layout=jsonb_set(local_layout,'{sources}','[null]'::jsonb)
			WHERE apply_operation_id=?`, localApplyID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "publication_automatic_album_local_layout_check") {
			t.Fatalf("malformed Local album evidence was not rejected by its constraint: %v", err)
		}
		var albumRows, albumWakes int
		if err := db.NewRaw(`SELECT count(*) FROM publication_automatic_album WHERE apply_operation_id=?`, localApplyID).
			Scan(ctx, &albumRows); err != nil || albumRows != 1 {
			t.Fatalf("Local automatic album rows=%d err=%v; want exactly one", albumRows, err)
		}
		if err := db.NewRaw(`SELECT count(*),COALESCE(min(id),0) FROM river_job
			WHERE kind='publication_auto_album_v1' AND queue='matching' AND (args->>'apply_operation_id')::uuid=?`,
			localApplyID).Scan(ctx, &albumWakes, &automaticAlbumWakeID); err != nil || albumWakes != 1 {
			t.Fatalf("Local automatic album wakes=%d err=%v; want exactly one", albumWakes, err)
		}
		replay, err := candidates.ApplyAutomaticLocalFallback(ctx, fallback.commit)
		if err != nil || !replay.Replayed || replay.Applied || replay.RecheckID != recheckID {
			t.Fatalf("exact outage replay did not reuse its committed result: %+v, %v", replay, err)
		}
		replayedAlbum, err := fixture.setup.ReadPublicationAutoAlbum(ctx, localApplyID)
		if err != nil || replayedAlbum == nil ||
			replayedAlbum.ApplyOperationID != album.ApplyOperationID || replayedAlbum.ReleaseID != album.ReleaseID ||
			replayedAlbum.OutputEpoch != album.OutputEpoch || len(replayedAlbum.Scope) != 1 ||
			replayedAlbum.Scope[0].PositionID != album.Scope[0].PositionID ||
			replayedAlbum.Scope[0].ProviderTrackID != album.Scope[0].ProviderTrackID ||
			!slices.Equal(replayedAlbum.Scope[0].WorkIDs, album.Scope[0].WorkIDs) {
			t.Fatalf("exact outage replay changed its automatic album request: %+v, %v", replayedAlbum, err)
		}
		var replayedAlbumRows, replayedAlbumWakes int
		var replayedAlbumWakeID int64
		if err := db.NewRaw(`SELECT count(*) FROM publication_automatic_album WHERE apply_operation_id=?`, localApplyID).
			Scan(ctx, &replayedAlbumRows); err != nil || replayedAlbumRows != 1 {
			t.Fatalf("exact outage replay regenerated automatic album rows: %d, %v", replayedAlbumRows, err)
		}
		if err := db.NewRaw(`SELECT count(*),COALESCE(min(id),0) FROM river_job
			WHERE kind='publication_auto_album_v1' AND queue='matching' AND (args->>'apply_operation_id')::uuid=?`,
			localApplyID).Scan(ctx, &replayedAlbumWakes, &replayedAlbumWakeID); err != nil ||
			replayedAlbumWakes != 1 || replayedAlbumWakeID != automaticAlbumWakeID {
			t.Fatalf("exact outage replay regenerated automatic album wakes: count=%d id=%d err=%v; want %d",
				replayedAlbumWakes, replayedAlbumWakeID, err, automaticAlbumWakeID)
		}
		var associations int
		if err := db.NewRaw(`SELECT count(*) FROM track_source
			WHERE media_variant_id=(SELECT media_variant_id FROM track_source WHERE id=?)`, associationID).
			Scan(ctx, &associations); err != nil || associations != 1 {
			t.Fatalf("exact outage replay changed source associations: count=%d, err=%v", associations, err)
		}
		verifyLocalAlbumCanAdmitDuringOutage(t, ctx, fixture, fetch, client, automaticAlbumWakeID, *album, artistMBID)
	} else {
		// The same Local apply with the setting off must leave no grant.
		album, err := fixture.setup.ReadPublicationAutoAlbum(ctx, localApplyID)
		if err != nil || album != nil {
			t.Fatalf("disabled automatic album setting produced a Local grant: %+v, %v", album, err)
		}
		var albumRows int
		if err := db.NewRaw(`SELECT count(*) FROM publication_automatic_album WHERE apply_operation_id=?`, localApplyID).
			Scan(ctx, &albumRows); err != nil || albumRows != 0 {
			t.Fatalf("disabled automatic album setting stored rows=%d err=%v", albumRows, err)
		}
	}
	if _, err := db.NewRaw(`UPDATE matching_local_recheck SET next_recheck_at=now() WHERE id=?`, recheckID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	healthy.Store(true)
	coordinator := publication.NewCoordinator()
	publications := service.NewLibraryPublications(fixture.setup, fixture.registry, client,
		tools.Platform{GOOS: fixture.platform.Platform.GOOS, GOARCH: fixture.platform.Platform.GOARCH}).
		WithPublicationCoordinator(coordinator).WithPublicationCommands(matchingRecheckHashCommands{})
	var outputPath, outputHash string
	if scenario != "without CURRENT" {
		outputPath, outputHash = seedLocalRecheckCurrent(t, ctx, fixture, position)
		switch scenario {
		case "same position":
			if _, err := fetch.LookupRelease(ctx, releaseMBID); err != nil {
				t.Fatal(err)
			}
			bindRecheckPositionToMusicBrainz(t, ctx, db, position, releaseMBID)
		case "different position":
			grantMatchingMovePredecessorSource(t, ctx, fixture, associationID, position)
		}
	}
	runner := service.NewMatchingLocalRecheckService(candidates, fetch).WithPublicationAdmission(publications, client)
	worker := NewMatchingLocalRecheckWorker(runner)
	if err := worker.Work(ctx, &river.Job[MatchingLocalRecheckArgs]{}); err != nil {
		t.Fatal(err)
	}
	switch scenario {
	case "same position":
		verifyMatchingRecheckSamePositionPublication(t, ctx, fixture, coordinator, recheckID, associationID, position, outputPath, outputHash)
	case "different position":
		verifyMatchingMoveAdmission(t, ctx, fixture, recheckID, position, outputHash)
	}
	if scenario == "without CURRENT" {
		// A winning MusicBrainz recheck must invalidate the Local automatic
		// album grant. A retained Local assignment would legitimately keep it.
		grant, err := fixture.setup.ReadPublicationAutoAlbum(ctx, localApplyID)
		if err != nil || grant != nil {
			t.Fatalf("winning MusicBrainz recheck retained the Local automatic album grant: %+v, %v", grant, err)
		}
		var albumRows int
		if err := db.NewRaw(`SELECT count(*) FROM publication_automatic_album WHERE apply_operation_id=?`, localApplyID).
			Scan(ctx, &albumRows); err != nil || albumRows != 0 {
			t.Fatalf("winning MusicBrainz recheck retained automatic album rows=%d err=%v", albumRows, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("Local outage/recovery modified the original file: %v", err)
	}
	if scenario != "without CURRENT" {
		return
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
}

func verifyLocalAlbumCanAdmitDuringOutage(
	t *testing.T, ctx context.Context, fixture analysisDispatchFixture, fetch *service.ProviderFetchService,
	client persistence.RiverInserter, jobID int64, album persistence.PublicationAutoAlbumRequest, artistMBID string,
) {
	t.Helper()
	injected := errors.New("roll back first publication after real River insert")
	inserter := &abortLocalAlbumAdmission{RiverInserter: client, abort: injected}
	publications := service.NewLibraryPublications(fixture.setup, fixture.registry, inserter,
		tools.Platform{GOOS: fixture.platform.Platform.GOOS, GOARCH: fixture.platform.Platform.GOARCH}).
		WithPublicationCommands(matchingRecheckHashCommands{}).
		WithArtistDiscography(fetch)
	worker := NewPublicationAutoAlbumWorker(fixture.setup, publications)
	job := &river.Job[persistence.PublicationAutoAlbumArgs]{
		JobRow: &rivertype.JobRow{ID: jobID, Attempt: 1}, Args: persistence.PublicationAutoAlbumArgs{ApplyOperationID: album.ApplyOperationID},
	}
	// Roll back after proving the real first admission reached enqueue. This
	// preserves the no-CURRENT baseline needed by the MusicBrainz recheck below.
	if err := worker.Work(ctx, job); !errors.Is(err, injected) || inserter.inserts != 1 {
		preview, previewErr := publications.PreviewFirst(ctx, album.ReleaseID, album.Scope[0].PositionID)
		t.Fatalf("Local album did not reach first publication admission during the outage: inserts=%d err=%v; preview=%+v err=%v",
			inserter.inserts, err, preview, previewErr)
	}

	providerArtistID := uuid.New()
	result, err := fixture.database.NewRaw(`WITH artist AS (
		INSERT INTO provider_artist(id,provider_id,provider_key,name)
		SELECT ?,id,?,'Fixture Artist' FROM provider WHERE code='musicbrainz' RETURNING id,provider_id
	) INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method)
	SELECT artist.provider_id,artist.id,credit.artist_id,'manual' FROM artist
	JOIN release_artist credit ON credit.release_id=? AND credit.position=0`,
		providerArtistID, artistMBID, album.ReleaseID).Exec(ctx)
	if err != nil {
		t.Fatalf("link the existing primary artist to MusicBrainz: %v", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("link the existing primary artist to MusicBrainz: rows=%d err=%v", rows, err)
	}
	var snooze *river.JobSnoozeError
	if err := worker.Work(ctx, job); !errors.As(err, &snooze) || inserter.inserts != 1 {
		t.Fatalf("linked artist without a complete discography did not wait: inserts=%d err=%v", inserter.inserts, err)
	}
	if durable, err := fixture.setup.ReadPublicationAutoAlbum(ctx, album.ApplyOperationID); err != nil || durable == nil {
		t.Fatalf("discography wait lost the Local album request: request=%+v err=%v", durable, err)
	}
	if _, err := fixture.database.NewRaw(`DELETE FROM provider_local_artist WHERE provider_artist_id=?`, providerArtistID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	if err := fixture.settings.Set(ctx, settings.MatchingPairThresholdKey, "1"); err != nil {
		t.Fatal(err)
	}
	if err := publications.AdmitAutomaticAlbum(ctx, album); err != nil || inserter.inserts != 1 {
		t.Fatalf("stale Local threshold reached publication enqueue: inserts=%d err=%v", inserter.inserts, err)
	}
	// Restore the prior absent override for the winning MusicBrainz recheck.
	if _, err := fixture.database.NewRaw(`DELETE FROM app_setting WHERE setting_name=?`, settings.MatchingPairThresholdKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

type abortLocalAlbumAdmission struct {
	persistence.RiverInserter
	abort   error
	inserts int
}

func (inserter *abortLocalAlbumAdmission) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := inserter.RiverInserter.InsertTx(ctx, tx, args, opts)
	if _, isPublication := args.(publication.TrackArgs); err != nil || !isPublication {
		return result, err
	}
	inserter.inserts++
	return result, inserter.abort
}
