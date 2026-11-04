//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
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

func TestPublicationReconciliationAcknowledgesMetadataOnlyChangesPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := preparePendingGraphFixture(t)
	var strictContext *PublicationProviderTagContext
	if err := fixture.f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return readPublicationProviderTagContextForPositionTx(ctx, tx, fixture.release, fixture.position, fixture.source.Endpoint, pendingGraphTestIdentity, false, &strictContext)
	}); err == nil {
		t.Fatal("strict provider-tag reader accepted a release without accepted track evidence")
	}
	var before TrackPublication
	if err := fixture.f.db.NewSelect().Model(&before).Where("position_id=?", fixture.position).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	acknowledged, err := fixture.repo.AcknowledgePublicationReconciliationNoop(ctx, fixture.guard, fixture.witness)
	if err != nil || !acknowledged {
		t.Fatalf("acknowledge metadata-only change = %v, %v", acknowledged, err)
	}
	var after TrackPublication
	if err := fixture.f.db.NewSelect().Model(&after).Where("position_id=?", fixture.position).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var completed int
	if err := fixture.f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE operation_id=? AND state='completed'`, fixture.guard.OperationID).Scan(ctx, &completed); err != nil {
		t.Fatal(err)
	}
	if before.Generation != after.Generation || string(before.ActualMetadata) != string(after.ActualMetadata) || string(before.ActualAssets) != string(after.ActualAssets) || completed != 1 {
		t.Fatalf("metadata-only reconciliation changed CURRENT or failed to complete: generation %s/%s metadata=%t assets=%t completed=%d", before.Generation, after.Generation, string(before.ActualMetadata) == string(after.ActualMetadata), string(before.ActualAssets) == string(after.ActualAssets), completed)
	}
	assertAcceptedRefreshWakesPendingGraph(t, ctx, fixture)
}

func TestPublicationReconciliationMetadataRefreshCreatesNoRewritePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := preparePendingGraphFixture(t)
	if acknowledged, err := fixture.repo.AcknowledgePublicationReconciliationNoop(ctx, fixture.guard, fixture.witness); err != nil || !acknowledged {
		t.Fatalf("acknowledge initial pending-graph metadata-only event = %v, %v", acknowledged, err)
	}
	var before TrackPublication
	if err := fixture.f.db.NewSelect().Model(&before).Where("position_id=?", fixture.position).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	assertAcceptedRefreshWakesPendingGraph(t, ctx, fixture)
	var providerContext *PublicationProviderTagContext
	if err := fixture.f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return readPublicationProviderTagContextForPositionTx(ctx, tx, fixture.release, fixture.position, fixture.source.Endpoint, pendingGraphTestIdentity, false, &providerContext)
	}); err != nil {
		t.Fatal(err)
	}
	patch, err := BuildPublicationProviderCanonicalTagPatch(*providerContext)
	if err != nil {
		t.Fatal(err)
	}
	protection, err := BuildPublicationProviderTagProtection(providerContext)
	if err != nil {
		t.Fatal(err)
	}
	var metadata PublicationCommitMetadata
	if err := strictMetadata(before.ActualMetadata, &metadata); err != nil {
		t.Fatal(err)
	}
	desired, err := publication.ApplyProviderTagPatch(metadata.Tags, patch, protection.Keys)
	if err != nil || reflect.DeepEqual(desired, metadata.Tags) {
		t.Fatalf("refreshed provider must offer a real unprotected CURRENT tag change: desired=%v current=%v err=%v", desired, metadata.Tags, err)
	}
	inserter := new(reconciliationDeliveryInserter)
	if dispatched, err := fixture.repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || dispatched != 1 {
		t.Fatalf("dispatch provider-refresh reconciliation = %d, %v", dispatched, err)
	}
	jobID, operationID := inserter.ids[0], inserter.args[0].OperationID
	if _, err := fixture.f.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	guard, outcome, err := fixture.repo.ClaimPublicationReconciliationDelivery(ctx, operationID, jobID, 1)
	if err != nil || outcome != PublicationReconciliationClaimed {
		t.Fatalf("claim provider-refresh reconciliation = %s, %v", outcome, err)
	}
	decision, err := fixture.repo.EvaluatePublicationReconciliationCurrent(ctx, fixture.release, guard, fixture.source.Endpoint, pendingGraphTestIdentity)
	if err != nil || decision.Action != AllNoop || decision.Noop == nil {
		t.Fatalf("provider-refresh-only reconciliation decision = %+v, %v", decision, err)
	}
	if acknowledged, err := fixture.repo.AcknowledgePublicationReconciliationNoop(ctx, guard, decision.Noop); err != nil || !acknowledged {
		t.Fatalf("acknowledge provider-refresh-only event = %v, %v", acknowledged, err)
	}
	var after TrackPublication
	if err := fixture.f.db.NewSelect().Model(&after).Where("position_id=?", fixture.position).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var pendingEvents int
	if err := fixture.f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE output_epoch=? AND position_id=? AND state='pending'`, fixture.guard.OutputEpoch, fixture.position).Scan(ctx, &pendingEvents); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := fixture.f.db.NewRaw(`SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, operationID).Scan(ctx, &attempts); err != nil {
		t.Fatal(err)
	}
	if before.Generation != after.Generation || string(before.ActualMetadata) != string(after.ActualMetadata) || string(before.ActualAssets) != string(after.ActualAssets) || pendingEvents != 0 || attempts != 0 {
		t.Fatalf("provider refresh reconciliation rewrote CURRENT or failed to complete: generation %s/%s metadata=%t assets=%t pending=%d attempts=%d", before.Generation, after.Generation, string(before.ActualMetadata) == string(after.ActualMetadata), string(before.ActualAssets) == string(after.ActualAssets), pendingEvents, attempts)
	}
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
