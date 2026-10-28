//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
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

func TestCreateStandaloneReleaseProviderRecordingAcceptedAuthorityPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	f := setupCreateProviderRecordingFixture(t, db, true)
	result, err := persistence.NewLibraryRepository(db).CreateStandaloneRelease(ctx, providerCreateRequest(f, nil))
	if err != nil {
		t.Fatalf("create provider-backed standalone release: %v", err)
	}
	if len(result.Positions) != 1 || result.Positions[0].RecordingID == nil {
		t.Fatalf("created position = %+v", result.Positions)
	}
	localRecordingID := *result.Positions[0].RecordingID
	var title string
	var metadata []byte
	if err := db.NewRaw(`SELECT title,metadata FROM local_recording WHERE id=?`, localRecordingID).Scan(ctx, &title, &metadata); err != nil || title != "Accepted Recording" {
		t.Fatalf("recording title = %q, %v", title, err)
	}
	var localMetadata map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &localMetadata); err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		SourceID       uuid.UUID                  `json:"source_id"`
		ProviderID     uuid.UUID                  `json:"provider_id"`
		SourceEntityID string                     `json:"source_entity_id"`
		ProjectionRev  string                     `json:"projection_revision"`
		FieldEvidence  map[string]json.RawMessage `json:"field_evidence"`
	}
	if err := json.Unmarshal(localMetadata["manual_matching"], &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.SourceID != f.sourceID || provenance.ProviderID != f.providerID || provenance.SourceEntityID != f.recordingKey || provenance.ProjectionRev == "" || provenance.ProjectionRev == f.identity || provenance.FieldEvidence["title"] == nil {
		t.Fatalf("recording provenance = %+v", provenance)
	}
	var artistCount, providerReleaseBindings, providerTrackBindings int
	if err := db.NewRaw(`SELECT count(*) FROM recording_artist WHERE recording_id=?`, localRecordingID).Scan(ctx, &artistCount); err != nil || artistCount != 1 {
		t.Fatalf("recording artist credits = %d, %v", artistCount, err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM provider_local_release`).Scan(ctx, &providerReleaseBindings); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM provider_local_track`).Scan(ctx, &providerTrackBindings); err != nil {
		t.Fatal(err)
	}
	if providerReleaseBindings != 0 || providerTrackBindings != 0 {
		t.Fatalf("provider release/track bindings were created: %d/%d", providerReleaseBindings, providerTrackBindings)
	}
}

func TestCreateStandaloneReleaseProviderRecordingStaleConfigurationRejectsMappedAndUnmappedPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, mapped := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmapped", true: "mapped"}[mapped], func(t *testing.T) {
			db := testpostgres.OpenMigrated(t)
			ctx := context.Background()
			f := setupCreateProviderRecordingFixture(t, db, false)
			var localID uuid.UUID
			if mapped {
				localID = uuid.New()
				if _, err := db.NewRaw(`INSERT INTO local_recording(id,title,metadata) VALUES (?, 'Existing local title','{}'::jsonb)`, localID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := db.NewRaw(`INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method,policy_version) VALUES (?,?,?,'automatic','policy')`, f.providerID, f.recordingID, localID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.NewRaw(`UPDATE app_setting SET setting_value='rotated-config' WHERE setting_name='musicbrainz_config_identity'`).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			var beforeLocal, beforeMappings int
			_ = db.NewRaw(`SELECT count(*) FROM local_recording`).Scan(ctx, &beforeLocal)
			_ = db.NewRaw(`SELECT count(*) FROM provider_local_recording`).Scan(ctx, &beforeMappings)
			_, err := persistence.NewLibraryRepository(db).CreateStandaloneRelease(ctx, providerCreateRequest(f, nil))
			if !errors.Is(err, persistence.ErrLibraryReleaseCreateProviderConflict) {
				t.Fatalf("stale config create error = %v", err)
			}
			var afterLocal, afterMappings, methodCount int
			if err := db.NewRaw(`SELECT count(*) FROM local_recording`).Scan(ctx, &afterLocal); err != nil {
				t.Fatal(err)
			}
			if err := db.NewRaw(`SELECT count(*) FROM provider_local_recording`).Scan(ctx, &afterMappings); err != nil {
				t.Fatal(err)
			}
			if afterLocal != beforeLocal || afterMappings != beforeMappings {
				t.Fatalf("stale authority mutated rows: local recordings %d->%d, mappings %d->%d", beforeLocal, afterLocal, beforeMappings, afterMappings)
			}
			var sourceIdentity string
			if err := db.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=?`, f.sourceID).Scan(ctx, &sourceIdentity); err != nil || sourceIdentity != f.identity {
				t.Fatalf("provider source identity = %q, %v", sourceIdentity, err)
			}
			for _, table := range []string{"local_release", "local_track", "release_medium", "release_track", "provider_local_release", "provider_local_track"} {
				var count int
				if err := db.NewRaw(`SELECT count(*) FROM `+table).Scan(ctx, &count); err != nil || count != 0 {
					t.Fatalf("stale config %s count = %d, %v", table, count, err)
				}
			}
			if mapped {
				if err := db.NewRaw(`SELECT count(*) FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=? AND method='automatic' AND policy_version='policy'`, f.providerID, f.recordingID).Scan(ctx, &methodCount); err != nil || methodCount != 1 {
					t.Fatalf("existing mapping authority changed: count=%d err=%v", methodCount, err)
				}
				var title string
				if err := db.NewRaw(`SELECT title FROM local_recording WHERE id=?`, localID).Scan(ctx, &title); err != nil || title != "Existing local title" {
					t.Fatalf("existing local recording title = %q, %v", title, err)
				}
			}
		})
	}
}

func TestCreateStandaloneReleaseSavedCoverDigestFailureRollsBackProviderRecordingAuthorityPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	f := setupCreateProviderRecordingFixture(t, db, true)
	coverID := uuid.New()
	coverBytes := []byte("stale saved cover")
	digest := sha256.Sum256(coverBytes)
	if _, err := db.NewRaw(`INSERT INTO media_metadata_result(id,observed_tags,provenance,observed_at,winning_result_id,applied_operation_id,cover_observed,cover_data,cover_mime,cover_sha256,cover_size_bytes) VALUES (?, '{}'::jsonb,'{}'::jsonb,now(),?,?,true,?,'image/png',?,?)`, coverID, uuid.New(), uuid.New(), coverBytes, digest[:], len(coverBytes)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var beforeRecordings, beforeMappings, beforeArtists int
	_ = db.NewRaw(`SELECT count(*) FROM local_recording`).Scan(ctx, &beforeRecordings)
	_ = db.NewRaw(`SELECT count(*) FROM provider_local_recording`).Scan(ctx, &beforeMappings)
	_ = db.NewRaw(`SELECT count(*) FROM local_artist`).Scan(ctx, &beforeArtists)
	wrongDigest := hex.EncodeToString(make([]byte, sha256.Size))
	_, err := persistence.NewLibraryRepository(db).CreateStandaloneRelease(ctx, providerCreateRequest(f, &persistence.LibraryReleaseCreateCover{MetadataResultID: coverID, ExpectedSHA256: wrongDigest}))
	if err == nil {
		t.Fatal("create with stale saved-cover digest succeeded")
	}
	var afterRecordings, afterMappings, afterArtists, afterProviderArtists, recordingCredits, releases, tracks, positions int
	for query, target := range map[string]*int{
		`SELECT count(*) FROM local_recording`:          &afterRecordings,
		`SELECT count(*) FROM provider_local_recording`: &afterMappings,
		`SELECT count(*) FROM local_artist`:             &afterArtists,
		`SELECT count(*) FROM provider_local_artist`:    &afterProviderArtists,
		`SELECT count(*) FROM recording_artist`:         &recordingCredits,
		`SELECT count(*) FROM local_release`:            &releases,
		`SELECT count(*) FROM local_track`:              &tracks,
		`SELECT count(*) FROM release_track`:            &positions,
	} {
		if err := db.NewRaw(query).Scan(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if afterRecordings != beforeRecordings || afterMappings != beforeMappings || afterArtists != beforeArtists || afterProviderArtists != 0 || recordingCredits != 0 || releases != 0 || tracks != 0 || positions != 0 {
		t.Fatalf("cover rejection left provider writes: recordings %d/%d mappings %d/%d artists %d/%d artist mappings %d credits %d releases %d tracks %d positions %d", beforeRecordings, afterRecordings, beforeMappings, afterMappings, beforeArtists, afterArtists, afterProviderArtists, recordingCredits, releases, tracks, positions)
	}
}
