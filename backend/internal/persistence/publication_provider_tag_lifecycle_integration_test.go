//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// Exercise the live provider-authority lifecycle against the same position
// shape used by publication result delivery: accepted evidence authorizes the
// rewrite, and a real owner change invalidates that authority.
func TestPublicationProviderTagAuthorityLifecyclePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	const identity = "provider-tag-lifecycle"
	const endpoint = "https://musicbrainz.example/ws/2"
	if _, err := f.db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES('musicbrainz_config_identity', ?) ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	provider := Provider{ID: uuid.New(), Code: "musicbrainz"}
	cache := NewProviderCacheRepository(f.db)
	if err := cache.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "provider-tag-lifecycle", Endpoint: endpoint, ConfigurationIdentity: identity}
	if err := cache.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	graph := publicationProviderTagAuthorityGraph()
	ticket, err := cache.BeginRefresh(ctx, source.ID, "release-search:0:1:lifecycle", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[{"id":"` + releaseKey + `"}]}`), Graph: graph,
	}); err != nil {
		t.Fatal(err)
	}
	var localTrackID, releaseID uuid.UUID
	if err := f.db.NewRaw(`SELECT track_id,release_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &localTrackID, &releaseID); err != nil {
		t.Fatal(err)
	}
	var providerReleaseID uuid.UUID
	if err := f.db.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, provider.ID, releaseKey).Scan(ctx, &providerReleaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES(?,?,?,'manual')`, provider.ID, providerReleaseID, releaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method) VALUES(?,?,?,'manual')`, provider.ID, graph.Tracks[0].ID, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	load := func() (*PublicationProviderTagContext, PublicationInputs) {
		t.Helper()
		var accepted *PublicationProviderTagContext
		if err := f.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
			return readPublicationProviderTagContextForPositionTx(ctx, tx, releaseID, f.guard.ExpectedPositionID, endpoint, identity, false, &accepted)
		}); err != nil {
			t.Fatal(err)
		}
		input := publicationProviderTagFixture(t)
		input.ProviderTags.ProviderSourceID = accepted.Source.ID
		input.ProviderTags.Endpoint = accepted.Source.Endpoint
		input.ProviderTags.ConfigurationIdentity = accepted.Source.ConfigurationIdentity
		input.ProviderTags.AuthorityRevision = accepted.Digest
		input.ProviderTags.Patch, err = BuildPublicationProviderCanonicalTagPatch(*accepted)
		if err != nil {
			t.Fatal(err)
		}
		protection, err := BuildPublicationProviderTagProtection(accepted)
		if err != nil {
			t.Fatal(err)
		}
		input.ProviderTags.ProtectedKeys = protection.Keys
		input.DesiredTags, err = publication.ApplyProviderTagPatch(input.ProviderTags.Current.Tags, input.ProviderTags.Patch, protection.Keys)
		if err != nil {
			t.Fatal(err)
		}
		return accepted, input
	}
	accepted, input := load()
	validate := func(candidate PublicationInputs) error {
		t.Helper()
		return f.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
			return validatePublicationProviderTagAuthorityTx(ctx, tx, releaseID, f.guard.ExpectedPositionID, candidate)
		})
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate frozen provider-tag contract: %v", err)
	}
	if err := validate(input); err != nil {
		t.Fatalf("live accepted provider authority rejected: %v", err)
	}

	// Changing the real local owner after freezing the contract must invalidate
	// its authority revision; a newly derived contract must then pass.
	if _, err := f.db.NewRaw(`UPDATE local_track SET metadata=?::jsonb WHERE id=?`, `{"manual_matching_override":{"title":"keep"}}`, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := validate(input); err == nil || !strings.Contains(err.Error(), ErrPublicationAdmissionConflict.Error()) {
		t.Fatalf("stale provider authority error = %v", err)
	}
	refreshed, freshInput := load()
	if refreshed.Digest == accepted.Digest {
		t.Fatal("local owner mutation did not advance provider authority revision")
	}
	if err := validate(freshInput); err != nil {
		t.Fatalf("rebuilt provider authority rejected: %v", err)
	}
}
