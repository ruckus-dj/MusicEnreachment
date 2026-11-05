//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"time"
)

// Regression: a repeated policy submission with the now-stale catalog revision
// must not enqueue more lookup operations or modify the accepted catalog.

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

// matchingHydrationFailingInserter fails every in-transaction enqueue, modelling
// a missing writer input rather than rejected metadata.
type matchingHydrationFailingInserter struct{}

func (matchingHydrationFailingInserter) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("injected hydration enqueue failure")
}
