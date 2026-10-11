//go:build integration

package persistence_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestParkMatchingAcquisitionForDeliveryAcceptsEmptyInitialSearchPostgres(t *testing.T) {
	t.Parallel()
	fixture := newMatchingAcquisitionGuardFixture(t)
	phase := matchingAcquisitionEmptyArtistPhase(t, fixture)
	changed, err := fixture.repository.ParkMatchingAcquisitionForDelivery(fixture.ctx, fixture.parentID, fixture.parent.Attempt,
		fixture.parentJobID, fixture.claim, 0, phase)
	if err != nil || !changed {
		t.Fatalf("park empty artist search: changed=%v err=%v", changed, err)
	}

	var result struct {
		Revision   int64  `bun:"revision"`
		Phase      string `bun:"current_phase"`
		RunState   string `bun:"run_state"`
		Operation  string `bun:"operation_state"`
		Stage      string `bun:"stage"`
		ActiveJob  *int64 `bun:"active_river_job_id"`
		ActiveTry  *int   `bun:"active_river_attempt"`
		ActiveExec *int64 `bun:"active_execution_epoch"`
		Delivery   int    `bun:"consumed_deliveries"`
		PhaseCount int    `bun:"phase_count"`
		Deps       int    `bun:"dependencies"`
		BrowseOps  int    `bun:"browse_operations"`
	}
	if err := fixture.db.NewRaw(`SELECT acquisition.revision, acquisition.current_phase, run.state AS run_state,
		operation.state AS operation_state, operation.stage, run.active_river_job_id, run.active_river_attempt,
		run.active_execution_epoch,
		(SELECT count(*) FROM matching_delivery WHERE run_id=acquisition.run_id AND river_job_id=? AND consumed_at IS NOT NULL) AS consumed_deliveries,
		(SELECT count(*) FROM matching_acquisition_phase WHERE run_id=acquisition.run_id) AS phase_count,
		(SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=acquisition.run_id) AS dependencies,
		(SELECT count(*) FROM operation child JOIN matching_provider_dependency dependency ON dependency.request_id=child.id
		 WHERE dependency.parent_run_id=acquisition.run_id AND child.kind='provider_artist_release_browse') AS browse_operations
		FROM matching_acquisition acquisition JOIN matching_run run ON run.id=acquisition.run_id
		JOIN operation ON operation.id=acquisition.run_id WHERE acquisition.run_id=?`, fixture.parentJobID, fixture.parentID).
		Scan(fixture.ctx, &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision != 1 || result.Phase != "waiting_policy" || result.RunState != "waiting_policy" ||
		result.Operation != "running" || result.Stage != "manual_review" || result.ActiveJob != nil || result.ActiveTry != nil || result.ActiveExec != nil ||
		result.Delivery != 1 || result.PhaseCount != 2 || result.Deps != 1 || result.BrowseOps != 0 {
		t.Fatalf("unexpected parked empty-search state: %+v", result)
	}
}

func TestParkMatchingAcquisitionForDeliveryRejectsStaleClaimPostgres(t *testing.T) {
	t.Parallel()
	fixture := newMatchingAcquisitionGuardFixture(t)
	phase := matchingAcquisitionEmptyArtistPhase(t, fixture)
	before := matchingAcquisitionGuardState(t, fixture)
	claim := fixture.claim
	claim.ExecutionEpoch++
	changed, err := fixture.repository.ParkMatchingAcquisitionForDelivery(fixture.ctx, fixture.parentID, fixture.parent.Attempt,
		fixture.parentJobID, claim, 0, phase)
	if err != nil || changed {
		t.Fatalf("stale park claim accepted: changed=%v err=%v", changed, err)
	}
	if after := matchingAcquisitionGuardState(t, fixture); before != after {
		t.Fatalf("stale claim changed durable state: before=%+v after=%+v", before, after)
	}
}

func TestParkMatchingAcquisitionForDeliveryRejectsChangedConfigurationPostgres(t *testing.T) {
	t.Parallel()
	fixture := newMatchingAcquisitionGuardFixture(t)
	phase := matchingAcquisitionEmptyArtistPhase(t, fixture)
	if _, err := fixture.db.NewRaw(`UPDATE app_setting SET setting_value='changed-matching-config' WHERE setting_name='musicbrainz_config_identity'`).Exec(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	before := matchingAcquisitionGuardState(t, fixture)
	changed, err := fixture.repository.ParkMatchingAcquisitionForDelivery(fixture.ctx, fixture.parentID, fixture.parent.Attempt,
		fixture.parentJobID, fixture.claim, 0, phase)
	if err != nil || changed {
		t.Fatalf("changed provider configuration accepted: changed=%v err=%v", changed, err)
	}
	if after := matchingAcquisitionGuardState(t, fixture); before != after {
		t.Fatalf("changed configuration modified acquisition state: before=%+v after=%+v", before, after)
	}
}

func matchingAcquisitionEmptyArtistPhase(t *testing.T, fixture *matchingAcquisitionGuardFixture) persistence.MatchingAcquisitionPhase {
	t.Helper()
	ticket, err := fixture.cache.BeginRefresh(fixture.ctx, fixture.source.ID, fixture.artistKey, fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"artists":[],"count":0,"offset":0}`)
	if err := fixture.cache.ApplySuccessfulResponse(fixture.ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	var childJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, fixture.artistID).Scan(fixture.ctx, &childJobID); err != nil {
		t.Fatal(err)
	}
	fixture.settleProviderAndClaimParentWake(fixture.artistID, childJobID)
	accepted, err := fixture.cache.GetCachedResponse(fixture.ctx, fixture.source.ID, fixture.artistKey)
	if err != nil || accepted == nil {
		t.Fatalf("read accepted empty artist response: response=%+v err=%v", accepted, err)
	}
	digest := sha256.Sum256(accepted.Payload)
	snapshot, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, fixture.parentID)
	if err != nil || !exists || len(snapshot.Intent.InitialProviderInputs) != 1 {
		t.Fatalf("read saved acquisition intent: exists=%v err=%v", exists, err)
	}
	provenance := json.RawMessage(fmt.Sprintf(`[ {"request_id":%q,"cache_key":%q,"payload_sha256":"%x"} ]`, fixture.artistID.String(), fixture.artistKey, digest[:]))
	return persistence.MatchingAcquisitionPhase{
		Revision: 1, Phase: "waiting_policy", Input: append(json.RawMessage(nil), snapshot.Intent.InitialProviderInputs[0].Input...),
		Output: json.RawMessage(`{"state":"waiting_policy","reason":"artist_search_no_results"}`), ResponseProvenance: provenance,
	}
}
