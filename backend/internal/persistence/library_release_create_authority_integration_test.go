//go:build integration

package persistence_test

import (
	"context"

	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"

	"github.com/uptrace/bun"
)

type createProviderRecordingFixture struct {
	providerID, sourceID, recordingID, artistID uuid.UUID
	identity, recordingKey, artistKey           string
}

func setupCreateProviderRecordingFixture(t *testing.T, db *bun.DB, withArtist bool) createProviderRecordingFixture {
	t.Helper()
	ctx := context.Background()
	f := createProviderRecordingFixture{
		providerID: uuid.New(), sourceID: uuid.New(), recordingID: uuid.New(), artistID: uuid.New(),
		identity: "create-provider-config", recordingKey: uuid.NewString(), artistKey: uuid.NewString(),
	}
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, f.identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider(id,code) VALUES (?,'musicbrainz')`, f.providerID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?, 'musicbrainz','https://musicbrainz.org/ws/2',?)`, f.sourceID, f.providerID, f.identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	artistCredits := "[]"
	if withArtist {
		artistCredits = `[{"name":"Credit Name","joinphrase":" & ","artist":{"id":"` + f.artistKey + `"}}]`
		if _, err := db.NewRaw(`INSERT INTO provider_artist(id,provider_id,provider_key,name) VALUES (?,?,?,'Accepted Artist')`, f.artistID, f.providerID, f.artistKey).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.NewRaw(`INSERT INTO provider_recording(id,provider_id,provider_key,title) VALUES (?,?,?,'Accepted Recording')`, f.recordingID, f.providerID, f.recordingKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fields := `{"title":"Accepted Recording","artist-credit":` + artistCredits + `}`
	evidence := `{"title":{"authority":3,"fetch_order":7},"artist-credit":{"authority":3,"fetch_order":7}}`
	if _, err := db.NewRaw(`INSERT INTO provider_source_entity(provider_source_id,provider_id,entity_kind,provider_key,name,fields,field_evidence,raw_source,authority,complete,fetch_order) VALUES (?,?, 'recording',?,'Accepted Recording',?::jsonb,?::jsonb,'{}'::jsonb,3,true,7)`, f.sourceID, f.providerID, f.recordingKey, fields, evidence).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if withArtist {
		if _, err := db.NewRaw(`INSERT INTO provider_source_entity(provider_source_id,provider_id,entity_kind,provider_key,name,fields,field_evidence,raw_source,authority,complete,fetch_order) VALUES (?,?, 'artist',?,'Accepted Artist','{"name":"Accepted Artist"}'::jsonb,'{"name":{"authority":3,"fetch_order":7}}'::jsonb,'{}'::jsonb,3,true,7)`, f.sourceID, f.providerID, f.artistKey).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func providerCreateRequest(f createProviderRecordingFixture, cover *persistence.LibraryReleaseCreateCover) persistence.LibraryReleaseCreateRequest {
	return persistence.LibraryReleaseCreateRequest{
		Title: "Standalone",
		Media: []persistence.LibraryReleaseCreateMedium{{Key: "disc", Positions: []persistence.LibraryReleaseCreatePosition{{
			Key: "position", Title: "Position title", RecordingRef: &persistence.LibraryReleaseCreateRecordingRef{
				Kind: "provider", ProviderID: f.providerID, ProviderSourceID: f.sourceID,
				ProviderRecordingID: f.recordingID, ConfigurationIdentity: f.identity,
			},
		}}}},
		Cover: cover,
	}
}
