//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const pendingGraphTestIdentity = "publication-reconciliation-pending-graph"
const pendingGraphTestReleaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

type pendingGraphFixture struct {
	f          publicationExecutionFixture
	repo       *SetupManagerRepository
	cache      *ProviderCacheRepository
	source     ProviderSource
	release    uuid.UUID
	position   uuid.UUID
	guard      PublicationReconciliationDeliveryGuard
	witness    *PublicationReconciliationNoop
	releaseKey string
}

func preparePendingGraphFixture(t *testing.T) pendingGraphFixture {
	t.Helper()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if committed, err := repo.CommitPublicationResult(ctx, f.guard, publicationResultForFixture(f)); err != nil || !committed {
		t.Fatalf("establish CURRENT publication = %v, %v", committed, err)
	}
	position := f.guard.ExpectedPositionID
	var release uuid.UUID
	if err := f.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, position).Scan(ctx, &release); err != nil {
		t.Fatal(err)
	}
	seedLocalNoopAuthority(t, ctx, f.db, f.guard.Epoch, position, f.guard.CandidateGeneration)
	if _, err := f.db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, pendingGraphTestIdentity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	provider := Provider{ID: uuid.New(), Code: "musicbrainz"}
	cache := NewProviderCacheRepository(f.db)
	if err := cache.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: pendingGraphTestIdentity, Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: pendingGraphTestIdentity}
	if err := cache.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	graph := publicationProviderTagAuthorityGraph()
	ticket, err := cache.BeginRefresh(ctx, source.ID, "release-search:0:1:pending-graph", pendingGraphTestIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[{"id":"` + pendingGraphTestReleaseKey + `"}]}`), Graph: graph}); err != nil {
		t.Fatal(err)
	}
	var providerReleaseID uuid.UUID
	if err := f.db.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, provider.ID, pendingGraphTestReleaseKey).Scan(ctx, &providerReleaseID); err != nil {
		t.Fatal(err)
	}
	var localTrack uuid.UUID
	if err := f.db.NewRaw(`SELECT track_id FROM release_track WHERE id=?`, position).Scan(ctx, &localTrack); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES(?,?,?,'manual')`, provider.ID, providerReleaseID, release).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method) VALUES(?,?,?,'manual')`, provider.ID, graph.Tracks[0].ID, localTrack).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var baselineContext *PublicationProviderTagContext
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return readPublicationProviderTagContextForPositionTx(ctx, tx, release, position, source.Endpoint, pendingGraphTestIdentity, false, &baselineContext)
	}); err != nil || baselineContext == nil || baselineContext.Position.ID != graph.Tracks[0].ID {
		t.Fatalf("valid accepted provider binding baseline = %+v, %v", baselineContext, err)
	}
	// Retain the source and all binding identities; withdraw only accepted graph
	// evidence so the strict binding lookup remains valid.
	if _, err := f.db.NewRaw(`UPDATE provider_source_entity SET field_evidence=field_evidence-'__tracks' WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, source.ID, pendingGraphTestReleaseKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	guard, _ := startPublicationReconciliationForResult(t, ctx, repo, f.guard.Epoch, position)
	decision, err := repo.EvaluatePublicationReconciliationCurrent(ctx, release, guard, source.Endpoint, pendingGraphTestIdentity)
	if err != nil || decision.Action != AllNoop || decision.Noop == nil {
		t.Fatalf("metadata-only missing-evidence baseline = %+v, %v", decision, err)
	}
	return pendingGraphFixture{f: f, repo: repo, cache: cache, source: source, release: release, position: position, guard: guard, witness: decision.Noop, releaseKey: pendingGraphTestReleaseKey}
}

func assertAcceptedRefreshWakesPendingGraph(t *testing.T, ctx context.Context, fixture pendingGraphFixture) {
	t.Helper()
	refresh, err := fixture.cache.BeginRefresh(ctx, fixture.source.ID, "release-lookup:"+fixture.releaseKey+":accepted-graph", pendingGraphTestIdentity)
	if err != nil {
		t.Fatal(err)
	}
	graph := publicationProviderTagAuthorityGraph()
	const refreshedTitle = "Refreshed provider release"
	graph.Releases[0].Title = refreshedTitle
	var raw map[string]any
	if err := json.Unmarshal(graph.Releases[0].RawSource, &raw); err != nil {
		t.Fatal(err)
	}
	raw["title"] = refreshedTitle
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	graph.Releases[0].RawSource = encoded
	for index := range graph.Entities {
		entity := &graph.Entities[index]
		if entity.Kind != "release" {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(entity.Fields, &fields); err != nil {
			t.Fatal(err)
		}
		fields["title"] = refreshedTitle
		entity.Fields, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		entity.Name, entity.RawSource = refreshedTitle, encoded
	}
	if err := fixture.cache.ApplySuccessfulResponse(ctx, ProviderSuccessfulResponse{Ticket: refresh, FetchedAt: time.Now().UTC(), Payload: encoded, Graph: graph}); err != nil {
		t.Fatal(err)
	}
	var requested int64
	var pending int
	if err := fixture.f.db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, fixture.guard.OutputEpoch, fixture.position).Scan(ctx, &requested); err != nil {
		t.Fatal(err)
	}
	if err := fixture.f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE output_epoch=? AND position_id=? AND state='pending'`, fixture.guard.OutputEpoch, fixture.position).Scan(ctx, &pending); err != nil {
		t.Fatal(err)
	}
	if requested <= fixture.guard.ObservedGeneration || pending == 0 {
		t.Fatalf("accepted refresh did not preserve a successor: requested=%d observed=%d pending=%d", requested, fixture.guard.ObservedGeneration, pending)
	}
}
