//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// Regression: a repeated policy submission with the now-stale catalog revision
// must not enqueue more lookup operations or modify the accepted catalog.
func TestMatchingReleaseHydrationRepeatAdmissionDoesNotDuplicateChildrenPostgreSQL(t *testing.T) {
	f := newMatchingAcquisitionGuardFixture(t)
	f.matchingAcquisitionCatalogReady()
	wakeID, claim := claimCatalogRankingWake(f)
	ids := []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}
	phase, children := matchingHydrationRequest(f, 3, ids)
	changed, err := f.repository.StartMatchingReleaseHydrationForDelivery(f.ctx, f.river, f.parentID, f.parent.Attempt, wakeID, claim, 3, phase, children)
	if err != nil || !changed {
		t.Fatalf("start hydration: changed=%v err=%v", changed, err)
	}
	var before int
	if err := f.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, f.parentID).Scan(f.ctx, &before); err != nil {
		t.Fatal(err)
	}
	changed, err = f.repository.StartMatchingReleaseHydrationForDelivery(f.ctx, f.river, f.parentID, f.parent.Attempt, wakeID, claim, 3, phase, children)
	if err != nil || changed {
		t.Fatalf("repeat hydration admission: changed=%v err=%v", changed, err)
	}
	var after, jobs int
	if err := f.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, f.parentID).Scan(f.ctx, &after); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM operation WHERE id IN (?,?)`, children[0].Operation.ID, children[1].Operation.ID).Scan(f.ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if after != before || jobs != 2 {
		t.Fatalf("repeat admission changed durable children: before=%d after=%d operations=%d", before, after, jobs)
	}
	if changed, err := f.repository.StartMatchingReleaseHydrationForDelivery(f.ctx, f.river, f.parentID, f.parent.Attempt, wakeID, claim, 3, phase, children); err != nil || changed {
		t.Fatalf("replay consumed ranking claim changed=%v err=%v", changed, err)
	}
}

func claimCatalogRankingWake(f *matchingAcquisitionGuardFixture) (int64, persistence.MatchingDeliveryClaim) {
	f.t.Helper()
	var wakeID int64
	if err := f.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL ORDER BY id DESC LIMIT 1`, f.parentID).Scan(f.ctx, &wakeID); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, wakeID).Exec(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	claim, outcome, err := f.repository.ClaimMatchingDelivery(f.ctx, f.parentID, f.parent.Attempt, wakeID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		f.t.Fatalf("claim catalog-ranking wake: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	return wakeID, claim
}

func matchingHydrationRequest(f *matchingAcquisitionGuardFixture, revision int64, ids []string) (persistence.MatchingAcquisitionPhase, []persistence.MatchingProviderRequest) {
	f.t.Helper()
	children := make([]persistence.MatchingProviderRequest, 0, len(ids))
	lookups := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		key := "release-lookup:" + id
		child := &persistence.Operation{ID: uuid.New(), Kind: "provider_release_lookup", State: "queued", Stage: "fetch",
			InputSnapshot: json.RawMessage(fmt.Sprintf(`{"schema_version":1,"mbid":%q}`, id)), ProviderSourceID: &f.source.ID,
			ProviderConfigurationIdentity: matchingCoordinatorStringPtr(f.identity), ProviderID: &f.source.ProviderID,
			ProviderEndpoint: f.source.Endpoint, ProviderCacheKey: &key}
		children = append(children, persistence.MatchingProviderRequest{Operation: child, Args: service.ProviderFetchOperationArgs{OperationID: child.ID}})
		lookups = append(lookups, map[string]any{"release_id": id, "request_id": child.ID, "cache_key": key})
	}
	in, _ := json.Marshal(map[string]any{"release_ids": ids})
	out, _ := json.Marshal(map[string]any{"lookups": lookups})
	return persistence.MatchingAcquisitionPhase{Revision: revision + 1, Phase: persistence.MatchingAcquisitionReleaseHydration,
		Input: in, Output: out, ResponseProvenance: json.RawMessage("[]")}, children
}

func (f *matchingAcquisitionGuardFixture) matchingAcquisitionCatalogReady() {
	f.t.Helper()
	f.settleInitialArtistAndClaimWake()
	f.advanceFirstCatalogPage()
	f.settleFirstBrowseAndClaimWake()
	phase, next := f.nextCatalogPage()
	if changed, err := f.repository.AdvanceMatchingAcquisitionForDelivery(f.ctx, f.river, f.parentID, f.parent.Attempt, f.parentJobID, f.claim, 1, phase, []persistence.MatchingProviderRequest{next}); err != nil || !changed {
		f.t.Fatalf("advance second page: changed=%v err=%v", changed, err)
	}
	f.browseID, f.browseKey = next.Operation.ID, *next.Operation.ProviderCacheKey
	ticket, err := f.cache.BeginRefresh(f.ctx, f.source.ID, f.browseKey, f.identity)
	if err != nil {
		f.t.Fatal(err)
	}
	ids := make([]map[string]string, 100)
	for i := range ids {
		ids[i] = map[string]string{"id": fmt.Sprintf("00000000-0000-4000-8000-%012d", i+101)}
	}
	payload, err := json.Marshal(map[string]any{"release-offset": 100, "release-count": 200, "releases": ids})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.cache.ApplySuccessfulResponse(f.ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload}); err != nil {
		f.t.Fatal(err)
	}
	var jobID int64
	if err := f.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, f.browseID).Scan(f.ctx, &jobID); err != nil {
		f.t.Fatal(err)
	}
	f.settleProviderAndClaimParentWake(f.browseID, jobID)
	accepted, err := f.cache.GetCachedResponse(f.ctx, f.source.ID, f.browseKey)
	if err != nil || accepted == nil {
		f.t.Fatalf("read final catalog response: %v", err)
	}
	digest := sha256.Sum256(accepted.Payload)
	allIDs := make([]string, 200)
	for i := range allIDs {
		allIDs[i] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
	}
	out, _ := json.Marshal(map[string]any{"artist_mbid": matchingAcquisitionGuardArtistID, "next_offset": 200, "complete": true, "catalog_count": 200, "covered_releases": 200, "release_ids": allIDs})
	in := json.RawMessage(fmt.Sprintf(`{"artist_mbid":%q,"offset":100,"limit":100}`, matchingAcquisitionGuardArtistID))
	provenance := json.RawMessage(fmt.Sprintf(`[{"request_id":%q,"cache_key":%q,"payload_sha256":%q}]`, f.browseID, f.browseKey, fmt.Sprintf("%x", digest[:])))
	final := persistence.MatchingAcquisitionPhase{Revision: 3, Phase: "catalog_ready", Input: in, Output: out, ResponseProvenance: provenance}
	if changed, err := f.repository.AdvanceMatchingAcquisitionForDelivery(f.ctx, f.river, f.parentID, f.parent.Attempt, f.parentJobID, f.claim, 2, final, nil); err != nil || !changed {
		f.t.Fatalf("accept final catalog: changed=%v err=%v", changed, err)
	}
}

// Regression: a River enqueue failure while admitting release-lookup children
// must roll the whole hydration transaction back. The failure is a missing
// writer input, not invalid metadata, so retrying the same valid request with a
// real inserter must then succeed.
func TestMatchingReleaseHydrationEnqueueFailureRollsBackAdmissionPostgreSQL(t *testing.T) {
	f := newMatchingAcquisitionGuardFixture(t)
	f.matchingAcquisitionCatalogReady()
	wakeID, claim := claimCatalogRankingWake(f)
	ids := []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}
	phase, children := matchingHydrationRequest(f, 3, ids)
	before := matchingAcquisitionGuardState(t, f)
	changed, err := f.repository.StartMatchingReleaseHydrationForDelivery(f.ctx, matchingHydrationFailingInserter{}, f.parentID, f.parent.Attempt, wakeID, claim, 3, phase, children)
	if err == nil || changed {
		t.Fatalf("hydration with failing enqueue: changed=%v err=%v", changed, err)
	}
	after := matchingAcquisitionGuardState(t, f)
	if before != after {
		t.Fatalf("failed hydration changed durable state: before=%+v after=%+v", before, after)
	}
	var childOps, childJobs int
	if err := f.db.NewRaw(`SELECT count(*) FROM operation WHERE id IN (?,?)`, children[0].Operation.ID, children[1].Operation.ID).Scan(f.ctx, &childOps); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE args->>'operation_id' IN (?,?)`, children[0].Operation.ID, children[1].Operation.ID).Scan(f.ctx, &childJobs); err != nil {
		t.Fatal(err)
	}
	if childOps != 0 || childJobs != 0 {
		t.Fatalf("failed hydration left durable children: operations=%d river_jobs=%d", childOps, childJobs)
	}
	if children[0].Operation.RiverJobID != nil || children[1].Operation.RiverJobID != nil {
		t.Fatalf("failed hydration assigned in-memory River job ids")
	}
	changed, err = f.repository.StartMatchingReleaseHydrationForDelivery(f.ctx, f.river, f.parentID, f.parent.Attempt, wakeID, claim, 3, phase, children)
	if err != nil || !changed {
		t.Fatalf("retry hydration with real inserter: changed=%v err=%v", changed, err)
	}
	var deps, ops int
	if err := f.db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND request_id IN (?,?)`, f.parentID, children[0].Operation.ID, children[1].Operation.ID).Scan(f.ctx, &deps); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM operation WHERE id IN (?,?)`, children[0].Operation.ID, children[1].Operation.ID).Scan(f.ctx, &ops); err != nil {
		t.Fatal(err)
	}
	if deps != 2 || ops != 2 {
		t.Fatalf("retry hydration durable children: dependencies=%d operations=%d", deps, ops)
	}
}

// matchingHydrationFailingInserter fails every in-transaction enqueue, modelling
// a missing writer input rather than rejected metadata.
type matchingHydrationFailingInserter struct{}

func (matchingHydrationFailingInserter) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("injected hydration enqueue failure")
}
