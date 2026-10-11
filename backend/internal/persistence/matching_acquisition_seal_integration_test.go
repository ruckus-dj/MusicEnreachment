//go:build integration

package persistence_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestMatchingAcquisitionLegacySealRejectsActiveCatalogWakePostgreSQL(t *testing.T) {
	fixture := newMatchingAcquisitionGuardFixture(t)
	fixture.settleInitialArtistAndClaimWake()
	fixture.advanceFirstCatalogPage()
	fixture.settleFirstBrowseAndClaimWake()
	phase, nextChild := fixture.nextCatalogPage()
	changed, err := fixture.repository.AdvanceMatchingAcquisitionForDelivery(fixture.ctx, fixture.river, fixture.parentID,
		fixture.parent.Attempt, fixture.parentJobID, fixture.claim, 1, phase, []persistence.MatchingProviderRequest{nextChild})
	if err != nil || !changed {
		t.Fatalf("advance second catalog page: changed=%v err=%v", changed, err)
	}

	pageKey := *nextChild.Operation.ProviderCacheKey
	ticket, err := fixture.cache.BeginRefresh(fixture.ctx, fixture.source.ID, pageKey, fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	releases := make([]map[string]string, 100)
	for index := range releases {
		releases[index] = map[string]string{"id": fmt.Sprintf("00000000-0000-4000-8000-%012d", index+101)}
	}
	payload, err := json.Marshal(map[string]any{"release-offset": 100, "release-count": 200, "releases": releases})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.cache.ApplySuccessfulResponse(fixture.ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var childJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, nextChild.Operation.ID).Scan(fixture.ctx, &childJobID); err != nil {
		t.Fatal(err)
	}
	fixture.settleProviderAndClaimParentWake(nextChild.Operation.ID, childJobID)

	accepted, err := fixture.cache.GetCachedResponse(fixture.ctx, fixture.source.ID, pageKey)
	if err != nil || accepted == nil {
		t.Fatalf("read accepted final browse response: response=%+v err=%v", accepted, err)
	}
	payloadSHA := sha256.Sum256(accepted.Payload)
	allIDs := make([]string, 200)
	for index := range allIDs {
		allIDs[index] = fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1)
	}
	output, err := json.Marshal(map[string]any{"artist_mbid": matchingAcquisitionGuardArtistID, "next_offset": 200, "complete": true,
		"catalog_count": 200, "covered_releases": 200, "release_ids": allIDs})
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(fmt.Sprintf(`{"artist_mbid":"%s","offset":100,"limit":100}`, matchingAcquisitionGuardArtistID))
	provenance, _ := json.Marshal([]map[string]string{{"request_id": nextChild.Operation.ID.String(), "cache_key": pageKey, "payload_sha256": fmt.Sprintf("%x", payloadSHA[:])}})
	finalPhase := persistence.MatchingAcquisitionPhase{Revision: 3, Phase: "catalog_ready", Input: input, Output: output, ResponseProvenance: provenance}
	changed, err = fixture.repository.AdvanceMatchingAcquisitionForDelivery(fixture.ctx, fixture.river, fixture.parentID,
		fixture.parent.Attempt, fixture.parentJobID, fixture.claim, 2, finalPhase, nil)
	if err != nil || !changed {
		t.Fatalf("complete accepted catalog: changed=%v err=%v", changed, err)
	}
	wakeID, claim := claimCatalogRankingWake(fixture)

	var manifest []struct {
		Revision   int64     `json:"revision"`
		RequestID  uuid.UUID `json:"request_id"`
		CacheKey   string    `json:"cache_key"`
		PayloadSHA string    `json:"payload_sha256"`
	}
	rows, err := fixture.db.QueryContext(fixture.ctx, `SELECT revision, response_provenance FROM matching_acquisition_phase WHERE run_id=? AND revision BETWEEN 1 AND 3 ORDER BY revision`, fixture.parentID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var revision int64
		var raw []byte
		if err := rows.Scan(&revision, &raw); err != nil {
			t.Fatal(err)
		}
		var records []struct {
			RequestID  uuid.UUID `json:"request_id"`
			CacheKey   string    `json:"cache_key"`
			PayloadSHA string    `json:"payload_sha256"`
		}
		if err := json.Unmarshal(raw, &records); err != nil || len(records) != 1 {
			t.Fatalf("decode phase %d provenance: %v", revision, err)
		}
		manifest = append(manifest, struct {
			Revision   int64     `json:"revision"`
			RequestID  uuid.UUID `json:"request_id"`
			CacheKey   string    `json:"cache_key"`
			PayloadSHA string    `json:"payload_sha256"`
		}{revision, records[0].RequestID, records[0].CacheKey, records[0].PayloadSHA})
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 3 {
		t.Fatalf("catalog provenance manifest entries=%d, want 3", len(manifest))
	}
	var groups []uuid.UUID
	if err := fixture.db.NewRaw(`SELECT group_id FROM matching_run_target WHERE run_id=? ORDER BY group_id`, fixture.parentID).Scan(fixture.ctx, &groups); err != nil {
		t.Fatal(err)
	}
	inputSeal := persistence.MatchingAcquisitionEvaluationInput{SchemaVersion: 1, GroupIDs: groups, ReleaseIDs: []string{allIDs[0], allIDs[1]}}
	sealRequest := persistence.MatchingAcquisitionSealRequest{ExpectedRevision: 3, Input: inputSeal, Delivery: &claim, RiverJobID: wakeID, Attempt: fixture.parent.Attempt}
	if _, _, err := fixture.repository.SealMatchingAcquisitionEvaluation(fixture.ctx, fixture.river, fixture.parentID, sealRequest); err == nil {
		t.Fatal("legacy seal accepted while the catalog-ranking wake remained active")
	}
	var sealCount, deliveryCount int
	if err := fixture.db.NewRaw(`SELECT count(*),(SELECT count(*) FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL) FROM matching_acquisition_seal WHERE run_id=?`, fixture.parentID, fixture.parentID).Scan(fixture.ctx, &sealCount, &deliveryCount); err != nil {
		t.Fatal(err)
	}
	if sealCount != 0 || deliveryCount != 1 {
		t.Fatalf("durable seal/active ranking wake counts = %d/%d, want 0/1", sealCount, deliveryCount)
	}
}
