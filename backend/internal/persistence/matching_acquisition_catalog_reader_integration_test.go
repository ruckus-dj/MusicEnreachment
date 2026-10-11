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

func TestGetMatchingAcquisitionCatalogPhasesUsesImmutableLedgerPostgres(t *testing.T) {
	fixture := newMatchingAcquisitionGuardFixture(t)
	fixture.matchingAcquisitionCatalogReady()

	phases, err := fixture.repository.GetMatchingAcquisitionCatalogPhases(fixture.ctx, fixture.parentID)
	if err != nil {
		t.Fatalf("read acquisition catalog phases: %v", err)
	}
	if len(phases) != 3 || phases[0].Revision != 1 || phases[0].Phase != persistence.MatchingAcquisitionArtistCatalogPage ||
		phases[1].Revision != 2 || phases[1].Phase != persistence.MatchingAcquisitionArtistCatalogPage ||
		phases[2].Revision != 3 || phases[2].Phase != "catalog_ready" {
		t.Fatalf("catalog phases = %+v; want original catalog pages and completion phase", phases)
	}
	var ready struct {
		Complete bool     `json:"complete"`
		Count    int      `json:"catalog_count"`
		Covered  int      `json:"covered_releases"`
		IDs      []string `json:"release_ids"`
	}
	if err := json.Unmarshal(phases[2].Output, &ready); err != nil || !ready.Complete || ready.Count != 200 || ready.Covered != 200 || len(ready.IDs) != 200 {
		t.Fatalf("completion manifest = %+v, err=%v", ready, err)
	}

	var provenance []struct {
		CacheKey string `json:"cache_key"`
		Digest   string `json:"payload_sha256"`
	}
	if err := json.Unmarshal(phases[2].ResponseProvenance, &provenance); err != nil || len(provenance) != 1 || provenance[0].CacheKey != fixture.browseKey || provenance[0].Digest == "" {
		t.Fatalf("completion provenance = %+v, err=%v", provenance, err)
	}
	accepted, err := fixture.cache.GetCachedResponse(fixture.ctx, fixture.source.ID, fixture.browseKey)
	if err != nil || accepted == nil {
		t.Fatalf("read cached catalog response: response=%+v err=%v", accepted, err)
	}
	originalDigest := sha256.Sum256(accepted.Payload)
	if provenance[0].Digest != fmt.Sprintf("%x", originalDigest[:]) {
		t.Fatalf("stored digest = %q, want original accepted payload %x", provenance[0].Digest, originalDigest)
	}

	// Replace the mutable cache entry after the phase was accepted. The reader
	// must still return the immutable ledger bytes and digest, not reconstruct
	// phases from the current cache contents.
	ticket, err := fixture.cache.BeginRefresh(fixture.ctx, fixture.source.ID, fixture.browseKey, fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	changedPayload := []byte(`{"release-offset":100,"release-count":200,"releases":[]}`)
	if err := fixture.cache.ApplySuccessfulResponse(fixture.ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: changedPayload}); err != nil {
		t.Fatal(err)
	}
	changedDigest := sha256.Sum256(changedPayload)
	if provenance[0].Digest == fmt.Sprintf("%x", changedDigest[:]) {
		t.Fatal("test replacement payload unexpectedly has the accepted digest")
	}
	after, err := fixture.repository.GetMatchingAcquisitionCatalogPhases(fixture.ctx, fixture.parentID)
	if err != nil {
		t.Fatalf("reread acquisition catalog phases after cache replacement: %v", err)
	}
	if len(after) != len(phases) || string(after[2].ResponseProvenance) != string(phases[2].ResponseProvenance) || string(after[2].Output) != string(phases[2].Output) {
		t.Fatalf("catalog phase ledger changed with cache replacement: before=%+v after=%+v", phases, after)
	}
}
