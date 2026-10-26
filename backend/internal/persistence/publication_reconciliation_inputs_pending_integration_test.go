//go:build integration

package persistence

import (
	"context"
	"database/sql"
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
	witness    *PublicationReconciliationInputsPending
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
	if err != nil || decision.Action != InputsPending || decision.InputsPending == nil {
		t.Fatalf("accepted graph missing-evidence baseline = %+v, %v", decision, err)
	}
	return pendingGraphFixture{f: f, repo: repo, cache: cache, source: source, release: release, position: position, guard: guard, witness: decision.InputsPending, releaseKey: pendingGraphTestReleaseKey}
}

func TestPublicationReconciliationParksOnlyPendingAcceptedGraphPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := preparePendingGraphFixture(t)
	var strictContext *PublicationProviderTagContext
	if err := fixture.f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return readPublicationProviderTagContextForPositionTx(ctx, tx, fixture.release, fixture.position, fixture.source.Endpoint, pendingGraphTestIdentity, false, &strictContext)
	}); err == nil {
		t.Fatal("strict provider-tag reader accepted a release without accepted track evidence")
	}
	parked, err := fixture.repo.ParkPublicationReconciliationInputsPending(ctx, fixture.witness)
	if err != nil || !parked {
		t.Fatalf("park pending accepted graph = %v, %v", parked, err)
	}
	var state, stage, safeError string
	if err := fixture.f.db.NewRaw(`SELECT state,stage,safe_error FROM operation WHERE id=?`, fixture.guard.OperationID).Scan(ctx, &state, &stage, &safeError); err != nil {
		t.Fatal(err)
	}
	var dirty, completed int
	if err := fixture.f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, fixture.guard.OutputEpoch, fixture.position).Scan(ctx, &dirty); err != nil {
		t.Fatal(err)
	}
	if err := fixture.f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE operation_id=? AND state='completed'`, fixture.guard.OperationID).Scan(ctx, &completed); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || stage != "waiting" || safeError != PublicationReconciliationAcceptedGraphPendingReason || dirty == 0 || completed != 1 {
		t.Fatalf("parked operation/event/dirty = %s/%s/%q completed=%d dirty=%d", state, stage, safeError, completed, dirty)
	}
	assertAcceptedRefreshWakesPendingGraph(t, ctx, fixture)
}

func TestPublicationReconciliationPendingGraphProofRejectsChangesPostgreSQL(t *testing.T) {
	for _, change := range []string{"current_generation", "configuration", "request_generation"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := preparePendingGraphFixture(t)
			var baselineGeneration uuid.UUID
			if err := fixture.f.db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, fixture.position).Scan(ctx, &baselineGeneration); err != nil {
				t.Fatal(err)
			}
			expectedGeneration := baselineGeneration
			switch change {
			case "current_generation":
				var original uuid.UUID
				if err := fixture.f.db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, fixture.position).Scan(ctx, &original); err != nil {
					t.Fatal(err)
				}
				changed := uuid.New()
				if err := fixture.f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
					if _, err := tx.NewRaw(`UPDATE track_publication SET generation=? WHERE position_id=?`, changed, fixture.position).Exec(ctx); err != nil {
						return err
					}
					_, err := tx.NewRaw(`UPDATE position_source_authority SET publication_generation=? WHERE position_id=?`, changed, fixture.position).Exec(ctx)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				expectedGeneration = changed
				parked, err := fixture.repo.ParkPublicationReconciliationInputsPending(ctx, fixture.witness)
				if err != nil || parked {
					t.Fatalf("changed CURRENT generation parked = %v, %v", parked, err)
				}
			case "configuration":
				if _, err := fixture.f.db.NewRaw(`UPDATE app_setting SET setting_value='changed-identity' WHERE setting_name='musicbrainz_config_identity'`).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				if parked, err := fixture.repo.ParkPublicationReconciliationInputsPending(ctx, fixture.witness); err == nil || parked {
					t.Fatalf("changed configuration parked = %v, %v", parked, err)
				}
			case "request_generation":
				if err := fixture.f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
					return fixture.repo.RequestPublicationReconciliationWith(ctx, tx, fixture.guard.OutputEpoch, []uuid.UUID{fixture.position}, []string{"successor_request"})
				}); err != nil {
					t.Fatal(err)
				}
				if parked, err := fixture.repo.ParkPublicationReconciliationInputsPending(ctx, fixture.witness); err != nil || parked {
					t.Fatalf("successor generation parked stale proof = %v, %v", parked, err)
				}
			}
			var dirty, active, completed int
			if err := fixture.f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, fixture.guard.OutputEpoch, fixture.position).Scan(ctx, &dirty); err != nil {
				t.Fatal(err)
			}
			// A rejected proof leaves the original delivery live. The fixture's
			// claimed delivery is dispatched, not pending, so an obligation is
			// retained by any active pending or dispatched event.
			if err := fixture.f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE output_epoch=? AND position_id=? AND state IN ('pending','dispatched')`, fixture.guard.OutputEpoch, fixture.position).Scan(ctx, &active); err != nil {
				t.Fatal(err)
			}
			if err := fixture.f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE output_epoch=? AND position_id=? AND state='completed'`, fixture.guard.OutputEpoch, fixture.position).Scan(ctx, &completed); err != nil {
				t.Fatal(err)
			}
			var evaluated sql.NullInt64
			var parkedReason, retryNotBefore sql.NullString
			if err := fixture.f.db.NewRaw(`SELECT evaluated_generation,parked_reason,retry_not_before::text FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, fixture.guard.OutputEpoch, fixture.position).Scan(ctx, &evaluated, &parkedReason, &retryNotBefore); err != nil {
				t.Fatal(err)
			}
			var currentGeneration uuid.UUID
			if err := fixture.f.db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, fixture.position).Scan(ctx, &currentGeneration); err != nil {
				t.Fatal(err)
			}
			var eventState, operationState, jobState string
			var claimed bool
			var token uuid.UUID
			var jobID int64
			var riverAttempt int
			if err := fixture.f.db.NewRaw(`SELECT state,delivery_claimed,claim_token,registered_job_id FROM publication_reconciliation_event WHERE operation_id=?`, fixture.guard.OperationID).Scan(ctx, &eventState, &claimed, &token, &jobID); err != nil {
				t.Fatal(err)
			}
			if err := fixture.f.db.NewRaw(`SELECT state FROM operation WHERE id=?`, fixture.guard.OperationID).Scan(ctx, &operationState); err != nil {
				t.Fatal(err)
			}
			if err := fixture.f.db.NewRaw(`SELECT state,attempt FROM river_job WHERE id=?`, fixture.guard.RiverJobID).Scan(ctx, &jobState, &riverAttempt); err != nil {
				t.Fatal(err)
			}
			if dirty == 0 || active == 0 || completed != 0 {
				t.Fatalf("rejected proof lost obligation: dirty=%d active=%d completed=%d", dirty, active, completed)
			}
			if evaluated.Valid || parkedReason.Valid || retryNotBefore.Valid {
				t.Fatalf("rejected proof parked an evaluated generation: evaluated=%v reason=%v retry=%v", evaluated, parkedReason, retryNotBefore)
			}
			if currentGeneration != expectedGeneration {
				t.Fatalf("rejected proof changed CURRENT: %s want %s", currentGeneration, expectedGeneration)
			}
			// The claimed delivery keeps its own live River job, so the dispatcher
			// cannot hand the work to another worker.
			if eventState != "dispatched" || !claimed || token != fixture.guard.ClaimToken || jobID != fixture.guard.RiverJobID || operationState != "running" || jobState != "running" || riverAttempt != fixture.guard.RiverAttempt {
				t.Fatalf("rejected proof lost delivery ownership: event=%s claimed=%v token=%s job=%d operation=%s river=%s/%d", eventState, claimed, token, jobID, operationState, jobState, riverAttempt)
			}
		})
	}
}

func TestPublicationReconciliationAcceptedRefreshBeforePendingGraphParkPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := preparePendingGraphFixture(t)
	assertAcceptedRefreshWakesPendingGraph(t, ctx, fixture)
	parked, err := fixture.repo.ParkPublicationReconciliationInputsPending(ctx, fixture.witness)
	if err != nil || parked {
		t.Fatalf("accepted-refresh stale pending proof parked = %v, %v", parked, err)
	}
	var dirty, pending int
	if err := fixture.f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, fixture.guard.OutputEpoch, fixture.position).Scan(ctx, &dirty); err != nil {
		t.Fatal(err)
	}
	if err := fixture.f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE output_epoch=? AND position_id=? AND state='pending'`, fixture.guard.OutputEpoch, fixture.position).Scan(ctx, &pending); err != nil {
		t.Fatal(err)
	}
	if dirty == 0 || pending == 0 {
		t.Fatalf("accepted-refresh race lost obligation: dirty=%d pending=%d", dirty, pending)
	}
}

func assertAcceptedRefreshWakesPendingGraph(t *testing.T, ctx context.Context, fixture pendingGraphFixture) {
	t.Helper()
	refresh, err := fixture.cache.BeginRefresh(ctx, fixture.source.ID, "release-lookup:"+fixture.releaseKey+":accepted-graph", pendingGraphTestIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.cache.ApplySuccessfulResponse(ctx, ProviderSuccessfulResponse{Ticket: refresh, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"id":"` + fixture.releaseKey + `"}`), Graph: publicationProviderTagAuthorityGraph()}); err != nil {
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
