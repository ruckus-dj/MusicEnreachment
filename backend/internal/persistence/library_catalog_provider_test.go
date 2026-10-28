//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestLibraryCatalogEditionUsesTypedLocalMetadataAndBlocksProviderFallback(t *testing.T) {
	metadata := json.RawMessage(`{"local_metadata":{"version":1,"scope":"release","desired":{"release_date":{"state":"value","value":"2001","authority":"manual","provenance":{"kind":"manual"}},"label":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}},"catalog_number":{"state":"absent","authority":"manual","provenance":{"kind":"manual"}}}}}`)
	edition := libraryCatalogLocalEdition(metadata)
	provider := LibraryCatalogEdition{
		ReleaseDate: catalogProviderStringPointer("2020"), Labels: []string{"Provider Label"},
		CatalogNumbers: []string{"Provider Catalog"}, SecondaryTypes: []string{"EP"},
	}
	mergeLibraryCatalogEdition(&edition, provider, metadata)
	if edition.ReleaseDate == nil || *edition.ReleaseDate != "2001" {
		t.Fatalf("local release date = %#v", edition.ReleaseDate)
	}
	if len(edition.Labels) != 0 || len(edition.CatalogNumbers) != 0 {
		t.Fatalf("explicit empty/manual absence resurrected provider values: %#v", edition)
	}
	if len(edition.SecondaryTypes) != 1 || edition.SecondaryTypes[0] != "EP" {
		t.Fatalf("unmanaged provider field did not fall back: %#v", edition.SecondaryTypes)
	}
}

func TestLibraryCatalogEditionMalformedLocalMetadataDoesNotGuessProviderValues(t *testing.T) {
	metadata := json.RawMessage(`{"local_metadata":{"version":99,"scope":"release","desired":{}}}`)
	edition := libraryCatalogLocalEdition(metadata)
	mergeLibraryCatalogEdition(&edition, LibraryCatalogEdition{ReleaseDate: catalogProviderStringPointer("2020"), Labels: []string{}}, metadata)
	if edition.ReleaseDate != nil {
		t.Fatalf("malformed local metadata fell back to guessed provider value: %#v", edition.ReleaseDate)
	}
}

func catalogProviderStringPointer(value string) *string { return &value }

func TestLibraryCatalogProviderPositionCountUsesConfiguredSourcePostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	providerID, sourceA, sourceB := uuid.New(), uuid.New(), uuid.New()
	providerReleaseID, localReleaseID := uuid.New(), uuid.New()
	identity := "library-catalog-current"
	if _, err := db.NewRaw(`INSERT INTO provider(id,code) VALUES (?,'musicbrainz')`, providerID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, source := range []struct {
		id                  uuid.UUID
		namespace, identity string
	}{{sourceA, "catalog-a", identity}, {sourceB, "catalog-b", "other"}} {
		if _, err := db.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?,?,?,?)`, source.id, providerID, source.namespace, libraryCatalogMusicBrainzEndpoint, source.identity).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES ('musicbrainz_mode','public'),('musicbrainz_config_identity',?)`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_release(id,title,metadata) VALUES (?,'Local','{}')`, localReleaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	localTrackID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_track(id,title,metadata) VALUES (?,'Local track','{}')`, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO release_track(release_id,position,track_id,medium,displayed_number) VALUES (?,0,?,0,'1')`, localReleaseID, localTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'Provider')`, providerReleaseID, providerID, "release-key").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`, providerID, providerReleaseID, localReleaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, source := range []uuid.UUID{sourceA, sourceB} {
		if _, err := db.NewRaw(`INSERT INTO provider_source_entity(provider_source_id,provider_id,entity_kind,provider_key,fields,field_evidence,raw_source,authority,complete,fetch_order) VALUES (?,?, 'release','release-key','{}'::jsonb,?::jsonb,'{}'::jsonb,1,true,1)`, source, providerID, `{"__tracks":{"authority":1,"fetch_order":1}}`).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 12; index++ {
		recordingID, trackID := uuid.New(), uuid.New()
		if _, err := db.NewRaw(`INSERT INTO provider_recording(id,provider_id,provider_key,title) VALUES (?,?,?,'Recording')`, recordingID, providerID, uuid.NewString()).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewRaw(`INSERT INTO provider_release_track(id,provider_id,release_id,recording_id,medium,position,displayed_number) VALUES (?,?,?, ?,0,?,?)`, trackID, providerID, providerReleaseID, recordingID, index, "track").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewRaw(`INSERT INTO provider_source_release_track(provider_source_id,provider_id,track_id,release_id,recording_id,medium,position,displayed_number,authority,fetch_order) VALUES (?,?,?,?,?,0,?,'track',1,1)`, sourceA, providerID, trackID, providerReleaseID, recordingID, index).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if index < 2 {
			if _, err := db.NewRaw(`INSERT INTO provider_source_release_track(provider_source_id,provider_id,track_id,release_id,recording_id,medium,position,displayed_number,authority,fetch_order) VALUES (?,?,?,?,?,0,?,'track',1,1)`, sourceB, providerID, trackID, providerReleaseID, recordingID, index).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	rows := []LibraryCatalogRelease{{ID: localReleaseID}}
	if err := db.RunInTx(ctx, &sql.TxOptions{ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error { return enrichLibraryCatalogReleaseProvider(ctx, tx, rows) }); err != nil {
		t.Fatal(err)
	}
	if rows[0].Counts.ProviderPositionCount == nil || *rows[0].Counts.ProviderPositionCount != 12 {
		t.Fatalf("provider position count = %#v; wanted 12 from configured source, not 2 from the other source", rows[0].Counts.ProviderPositionCount)
	}
}
