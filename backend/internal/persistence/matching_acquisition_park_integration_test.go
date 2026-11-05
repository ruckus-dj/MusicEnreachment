//go:build integration

package persistence_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"testing"
	"time"
)

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
