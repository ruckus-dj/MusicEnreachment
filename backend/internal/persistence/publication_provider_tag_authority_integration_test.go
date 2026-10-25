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
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// This exercises only the provider-authority prefix validator. It does not
// exercise publication admission, result delivery, or River enqueueing.
func TestValidatePublicationProviderTagAuthorityTxPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	const identity = "provider-tag-authority"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES('musicbrainz_config_identity', ?) ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	provider := Provider{ID: uuid.New(), Code: "musicbrainz"}
	cache := NewProviderCacheRepository(db)
	if err := cache.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "provider-tag-authority", Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: identity}
	if err := cache.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	graph := publicationProviderTagAuthorityGraph()
	ticket, err := cache.BeginRefresh(ctx, source.ID, "release-search:0:1:authority", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[{"id":"` + releaseKey + `"}]}`), Graph: graph}); err != nil {
		t.Fatal(err)
	}

	localReleaseID, localTrackID, positionID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_release(id,title,metadata) VALUES(?,?,'{}'::jsonb)`, localReleaseID, "Local album").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_track(id,title,metadata) VALUES(?,?, '{}'::jsonb)`, localTrackID, "Local track").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO release_track(id,release_id,medium,position,track_id,displayed_number) VALUES(?,?,0,0,?,'1')`, positionID, localReleaseID, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var providerReleaseID, providerTrackID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, provider.ID, releaseKey).Scan(ctx, &providerReleaseID); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT id FROM provider_release_track WHERE id=?`, graph.Tracks[0].ID).Scan(ctx, &providerTrackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES(?,?,?,'manual')`, provider.ID, providerReleaseID, localReleaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method) VALUES(?,?,?,'manual')`, provider.ID, providerTrackID, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	load := func() (*PublicationProviderTagContext, PublicationInputs) {
		t.Helper()
		var accepted *PublicationProviderTagContext
		if err := db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
			return readPublicationProviderTagContextForPositionTx(ctx, tx, localReleaseID, positionID, source.Endpoint, identity, false, &accepted)
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
		return db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
			return validatePublicationProviderTagAuthorityTx(ctx, tx, localReleaseID, positionID, candidate)
		})
	}
	if err := validate(input); err != nil {
		t.Fatalf("valid provider authority rejected: %v", err)
	}

	// A real local-owner revision/metadata change invalidates the frozen digest.
	if _, err := db.NewRaw(`UPDATE local_track SET metadata=?::jsonb WHERE id=?`, `{"manual_matching_override":{"title":"keep"}}`, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := validate(input); err == nil || !strings.Contains(err.Error(), ErrPublicationAdmissionConflict.Error()) {
		t.Fatalf("stale provider authority error = %v", err)
	}
	refreshed, freshInput := load()
	if refreshed.Digest == accepted.Digest {
		t.Fatal("local owner mutation did not advance authority digest")
	}
	if err := validate(freshInput); err != nil {
		t.Fatalf("rebuilt provider authority rejected: %v", err)
	}
}

func publicationProviderTagAuthorityGraph() ProviderGraph {
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const artistKey = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const recordingKey = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	const trackKey = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	releaseID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:release:"+releaseKey))
	artistID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:artist:"+artistKey))
	recordingID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:recording:"+recordingKey))
	trackID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:track:"+trackKey))
	raw := json.RawMessage(`{"id":"` + releaseKey + `","title":"Release","artist-credit":[{"name":"Artist","artist":{"id":"` + artistKey + `","name":"Artist"}}],"media":[{"position":1,"tracks":[{"id":"` + trackKey + `","number":"1","recording":{"id":"` + recordingKey + `","title":"Track"}}]}]}`)
	artistRaw := json.RawMessage(`{"id":"` + artistKey + `","name":"Artist"}`)
	recordingRaw := json.RawMessage(`{"id":"` + recordingKey + `","title":"Track"}`)
	fields := json.RawMessage(`{"title":"Release","artist-credit":[{"name":"Artist","artist":{"id":"` + artistKey + `","name":"Artist"}}],"normalized_tracks":[{"id":"` + trackID.String() + `","recording_id":"` + recordingID.String() + `","recording_provider_key":"` + recordingKey + `","medium":0,"position":0,"displayed_number":"1"}]}`)
	evidence := map[string]ProviderFieldEvidence{
		"title": {Authority: 3}, "artist-credit": {Authority: 3}, "normalized_tracks": {Authority: 3},
	}
	return ProviderGraph{
		Complete:   true,
		Artists:    []ProviderArtist{{ID: artistID, ProviderKey: artistKey, Name: "Artist", RawSource: artistRaw}},
		Releases:   []ProviderRelease{{ID: releaseID, ProviderKey: releaseKey, Title: "Release", RawSource: raw}},
		Recordings: []ProviderRecording{{ID: recordingID, ProviderKey: recordingKey, Title: "Track", RawSource: recordingRaw}},
		Credits:    []ProviderReleaseArtistCredit{{ReleaseID: releaseID, Position: 0, ArtistID: artistID, Name: "Artist"}},
		Tracks:     []ProviderReleaseTrack{{ID: trackID, ReleaseID: releaseID, RecordingID: recordingID, Medium: 0, Position: 0, DisplayedNumber: "1", RecordingProviderKey: recordingKey}},
		Entities: []ProviderSourceEntity{
			{Kind: "release", ProviderKey: releaseKey, Name: "Release", Fields: fields, RawSource: raw, Authority: 3, FieldEvidence: evidence, Complete: true},
			{Kind: "artist", ProviderKey: artistKey, Name: "Artist", Fields: json.RawMessage(`{}`), RawSource: artistRaw, Authority: 3, FieldEvidence: map[string]ProviderFieldEvidence{"name": {Authority: 3}}, Complete: true},
			{Kind: "recording", ProviderKey: recordingKey, Name: "Track", Fields: json.RawMessage(`{"title":"Track"}`), RawSource: recordingRaw, Authority: 3, FieldEvidence: map[string]ProviderFieldEvidence{"title": {Authority: 3}}, Complete: true},
		},
	}
}
