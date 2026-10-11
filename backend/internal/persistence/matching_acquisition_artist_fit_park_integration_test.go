//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestMatchingAcquisitionArtistNotFitParksThroughServicePostgres(t *testing.T) {
	t.Parallel()
	fixture := newMatchingAcquisitionGuardFixture(t)
	prepareMatchingAcquisitionFitSearch(t, fixture, "Completely Unrelated Artist")
	provider := matchingAcquisitionAcceptedCache{cache: fixture.cache, sourceID: fixture.source.ID}
	acquisition := service.NewMatchingAcquisitionService(fixture.repository, provider, fixture.river)
	changed, settled, err := acquisition.Advance(fixture.ctx, fixture.parentID, fixture.parent.Attempt, fixture.parentJobID, fixture.claim)
	if err != nil || !changed || !settled {
		t.Fatalf("service park nonfit accepted artist: changed=%v settled=%v err=%v", changed, settled, err)
	}
	state := matchingAcquisitionGuardState(t, fixture)
	if state.current != "waiting_policy" || state.phases != 2 || state.delivery != 1 {
		t.Fatalf("service did not park and consume the delivery: %+v", state)
	}
	var browseOps int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM operation child JOIN matching_provider_dependency dependency ON dependency.request_id=child.id
		WHERE dependency.parent_run_id=? AND child.kind='provider_artist_release_browse'`, fixture.parentID).Scan(fixture.ctx, &browseOps); err != nil {
		t.Fatal(err)
	}
	if browseOps != 0 {
		t.Fatalf("artist-fit wait scheduled %d browse operations", browseOps)
	}
}

func TestParkMatchingAcquisitionRejectsFittingArtistWithNotFitReasonPostgres(t *testing.T) {
	t.Parallel()
	fixture := newMatchingAcquisitionGuardFixture(t)
	artistName := prepareMatchingAcquisitionFitSearch(t, fixture, "")
	accepted, err := fixture.cache.GetCachedResponse(fixture.ctx, fixture.source.ID, fixture.artistKey)
	if err != nil || accepted == nil {
		t.Fatalf("read accepted artist response: response=%+v err=%v", accepted, err)
	}
	digest := sha256.Sum256(accepted.Payload)
	snapshot, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, fixture.parentID)
	if err != nil || !exists || len(snapshot.Intent.InitialProviderInputs) != 1 {
		t.Fatalf("read saved acquisition intent: exists=%v err=%v", exists, err)
	}
	provenance := json.RawMessage(fmt.Sprintf(`[{"request_id":%q,"cache_key":%q,"payload_sha256":"%x"}]`, fixture.artistID.String(), fixture.artistKey, digest[:]))
	phase := persistence.MatchingAcquisitionPhase{Revision: 1, Phase: "waiting_policy", Input: snapshot.Intent.InitialProviderInputs[0].Input,
		Output: json.RawMessage(`{"state":"waiting_policy","reason":"artist_not_fit"}`), ResponseProvenance: provenance}
	before := matchingAcquisitionGuardState(t, fixture)
	changed, err := fixture.repository.ParkMatchingAcquisitionForDelivery(fixture.ctx, fixture.parentID, fixture.parent.Attempt,
		fixture.parentJobID, fixture.claim, 0, phase)
	if err == nil || changed {
		t.Fatalf("fitting artist %q was parked as nonfit: changed=%v err=%v", artistName, changed, err)
	}
	if after := matchingAcquisitionGuardState(t, fixture); before != after {
		t.Fatalf("rejected nonfit changed durable state: before=%+v after=%+v", before, after)
	}
}

type matchingAcquisitionAcceptedCache struct {
	cache    *persistence.ProviderCacheRepository
	sourceID uuid.UUID
}

func (provider matchingAcquisitionAcceptedCache) CachedResponse(ctx context.Context, key string) (*persistence.ProviderCachedResponse, error) {
	return provider.cache.GetCachedResponse(ctx, provider.sourceID, key)
}

func (matchingAcquisitionAcceptedCache) PrepareOperation(context.Context, string, string, int, int, bool) (*persistence.Operation, error) {
	return nil, fmt.Errorf("unexpected provider preparation")
}

func prepareMatchingAcquisitionFitSearch(t *testing.T, fixture *matchingAcquisitionGuardFixture, artistName string) string {
	t.Helper()
	if artistName == "" {
		snapshot, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, fixture.parentID)
		if err != nil || !exists || len(snapshot.Intent.CapturedMembers) == 0 {
			t.Fatalf("read captured metadata for fitting artist: exists=%v err=%v", exists, err)
		}
		var tags map[string][]string
		if err := json.Unmarshal(snapshot.Intent.CapturedMembers[0].Tags, &tags); err != nil {
			t.Fatal(err)
		}
		for _, values := range tags {
			for _, candidate := range values {
				fit, available, fitErr := service.MatchingArtistFit(snapshot.Intent.CapturedMembers, candidate)
				if fitErr == nil && available && fit >= 1 {
					artistName = candidate
					break
				}
			}
			if artistName != "" {
				break
			}
		}
		if artistName == "" {
			t.Fatal("fixture capture has no comparable artist tag")
		}
	}
	ticket, err := fixture.cache.BeginRefresh(fixture.ctx, fixture.source.ID, fixture.artistKey, fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"artists": []map[string]string{{"id": fixture.artistID.String(), "name": artistName}}, "count": 1, "offset": 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.cache.ApplySuccessfulResponse(fixture.ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var childJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, fixture.artistID).Scan(fixture.ctx, &childJobID); err != nil {
		t.Fatal(err)
	}
	fixture.settleProviderAndClaimParentWake(fixture.artistID, childJobID)
	return artistName
}
