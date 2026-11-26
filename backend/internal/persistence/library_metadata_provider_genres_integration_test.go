//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

func TestReadMetadataDisplayProviderGenreScopesPostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := preparePendingGraphFixture(t)
	assertAcceptedRefreshWakesPendingGraph(t, ctx, fixture)
	artists, recording := seedProviderTagGenreAuthority(t, ctx, fixture)
	setTestArtistGenreFetchOrder(t, ctx, fixture, artists[0])
	const releaseGenres = `[{"id":"00000000-0000-4000-8000-000000000002","name":"release genre"}]`
	const recordingGenres = `[{"id":"00000000-0000-4000-8000-000000000003","name":"recording genre"}]`
	for _, projection := range []struct{ kind, key, genres string }{
		{kind: "release", key: fixture.releaseKey, genres: releaseGenres},
		{kind: "recording", key: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", genres: recordingGenres},
	} {
		result, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET fields=jsonb_set(fields,'{genres}',?::jsonb,true), field_evidence=field_evidence || '{"genres":{"authority":4,"fetch_order":9}}'::jsonb WHERE provider_source_id=? AND entity_kind=? AND provider_key=?`, projection.genres, fixture.source.ID, projection.kind, projection.key).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			t.Fatalf("update %s genre projection affected %d rows, err=%v", projection.kind, count, err)
		}
	}
	// Configure the exact source identity consumed by the catalog reader.
	if _, err := fixture.f.db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_mode','self-hosted'),('musicbrainz_base_url',?),('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, fixture.source.Endpoint, pendingGraphTestIdentity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	updated, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET
		fields=fields || '{"asin":"B001234567","text-representation":{"script":"Latn"}}'::jsonb,
		field_evidence=field_evidence || '{"asin":{"authority":4,"fetch_order":11},"text-representation":{"authority":4,"fetch_order":12}}'::jsonb
		WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, fixture.source.ID, fixture.releaseKey).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := updated.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("seed accepted release identifiers affected %d rows, err=%v", count, err)
	}
	repo := NewLibraryRepository(fixture.f.db)
	cases := []struct {
		name  string
		scope librarymetadata.Scope
		owner uuid.UUID
		want  string
	}{
		{name: "artist", scope: librarymetadata.ScopeArtist, owner: artists[0], want: "Provider Artist"},
		{name: "release", scope: librarymetadata.ScopeRelease, owner: fixture.release, want: "Release Genre"},
		{name: "recording", scope: librarymetadata.ScopeRecording, owner: recording, want: "Recording Genre"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.ReadMetadataDisplay(ctx, tc.scope, fixture.release, tc.owner)
			if err != nil {
				t.Fatal(err)
			}
			if got.ProviderGenreBaseline == nil || len(got.ProviderGenreBaseline.Genres) != 1 || got.ProviderGenreBaseline.Genres[0].Name != tc.want || got.ProviderGenreBaseline.Digest == "" {
				t.Fatalf("baseline = %+v, want genre %q", got.ProviderGenreBaseline, tc.want)
			}
			if tc.scope != librarymetadata.ScopeRelease && len(got.ProviderReleaseLinks) != 0 {
				t.Fatalf("%s scope provider links = %+v, want none", tc.name, got.ProviderReleaseLinks)
			}
		})
	}
	releaseDisplay, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopeRelease, fixture.release, fixture.release)
	if err != nil {
		t.Fatal(err)
	}
	identifierBaseline := releaseDisplay.ProviderReleaseIdentifierBaseline
	if identifierBaseline == nil || identifierBaseline.ASIN == nil || *identifierBaseline.ASIN != "B001234567" || identifierBaseline.Script == nil || *identifierBaseline.Script != "Latn" || identifierBaseline.Digest == "" {
		t.Fatalf("accepted release identifier baseline = %+v, want ASIN and script from the bound accepted release", identifierBaseline)
	}
	if identifierBaseline.Evidence["asin"].FetchOrder != 11 || identifierBaseline.Evidence["text-representation"].FetchOrder != 12 {
		t.Fatalf("identifier baseline evidence = %+v, want both accepted field evidences", identifierBaseline.Evidence)
	}
	if len(releaseDisplay.ProviderReleaseLinks) != 1 || releaseDisplay.ProviderReleaseLinks[0].ProviderCode != "musicbrainz" || releaseDisplay.ProviderReleaseLinks[0].ProviderKey != fixture.releaseKey || releaseDisplay.ProviderReleaseLinks[0].Method != "manual" {
		t.Fatalf("release provider links = %+v, want the stored manual musicbrainz binding", releaseDisplay.ProviderReleaseLinks)
	}
	recordingKey := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	recordingDisplay, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopeRecording, fixture.release, recording)
	if err != nil {
		t.Fatal(err)
	}
	if len(recordingDisplay.ProviderRecordingLinks) != 1 || recordingDisplay.ProviderRecordingLinks[0].ProviderCode != "musicbrainz" ||
		recordingDisplay.ProviderRecordingLinks[0].ProviderKey != recordingKey || recordingDisplay.ProviderRecordingLinks[0].Method != "manual" {
		t.Fatalf("recording provider links = %+v, want the exact manual musicbrainz recording binding", recordingDisplay.ProviderRecordingLinks)
	}
	if len(recordingDisplay.ProviderReleaseLinks) != 0 || len(recordingDisplay.ProviderPositionLinks) != 0 {
		t.Fatalf("recording scope leaked other provider links: %+v %+v", recordingDisplay.ProviderReleaseLinks, recordingDisplay.ProviderPositionLinks)
	}

	nativeTrackKey := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	var trackID uuid.UUID
	var trackMedium, trackPosition, trackAuthority int
	var trackFetchOrder int64
	if err := fixture.f.db.NewRaw(`SELECT track.track_id,track.medium,track.position,track.authority,track.fetch_order
		FROM provider_source_release_track track
		JOIN provider_local_track binding ON binding.provider_id=track.provider_id AND binding.provider_track_id=track.track_id
		JOIN release_track position ON position.track_id=binding.local_track_id
		WHERE track.provider_source_id=? AND position.id=? AND track.present`, fixture.source.ID, fixture.position).Scan(ctx, &trackID, &trackMedium, &trackPosition, &trackAuthority, &trackFetchOrder); err != nil {
		t.Fatal(err)
	}
	if trackID.String() == nativeTrackKey || uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:track:"+nativeTrackKey)) != trackID {
		t.Fatalf("accepted native track %q does not derive internal track %s", nativeTrackKey, trackID)
	}
	seedPositionMediaEvidence(t, ctx, fixture, nativeTrackKey, recordingKey, trackMedium, trackPosition, trackAuthority, trackFetchOrder)
	positionDisplay, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopePosition, fixture.release, fixture.position)
	if err != nil {
		t.Fatal(err)
	}
	if len(positionDisplay.ProviderPositionLinks) != 1 || positionDisplay.ProviderPositionLinks[0].ProviderCode != "musicbrainz" ||
		positionDisplay.ProviderPositionLinks[0].ProviderKey != nativeTrackKey || positionDisplay.ProviderPositionLinks[0].Method != "manual" {
		t.Fatalf("position provider links = %+v, want the accepted native track key", positionDisplay.ProviderPositionLinks)
	}
	if positionDisplay.ProviderPositionLinks[0].ProviderKey == trackID.String() {
		t.Fatal("position provider link exposed the internal SHA1-derived provider track id")
	}
	if len(positionDisplay.ProviderReleaseLinks) != 0 || len(positionDisplay.ProviderRecordingLinks) != 0 {
		t.Fatalf("position scope leaked other provider links: %+v %+v", positionDisplay.ProviderReleaseLinks, positionDisplay.ProviderRecordingLinks)
	}

	// Missing media evidence and a conflicting native track MBID both yield no
	// position key while the accepted normalized/__tracks evidence stays intact.
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET field_evidence=field_evidence-'media' WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, fixture.source.ID, fixture.releaseKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	missing, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopePosition, fixture.release, fixture.position)
	if err != nil || len(missing.ProviderPositionLinks) != 0 {
		t.Fatalf("position without media evidence = %+v, err=%v", missing.ProviderPositionLinks, err)
	}
	seedPositionMediaEvidence(t, ctx, fixture, "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", recordingKey, trackMedium, trackPosition, trackAuthority, trackFetchOrder)
	conflicting, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopePosition, fixture.release, fixture.position)
	if err != nil || len(conflicting.ProviderPositionLinks) != 0 {
		t.Fatalf("position with a non-deriving native track = %+v, err=%v", conflicting.ProviderPositionLinks, err)
	}
	seedPositionMediaEvidence(t, ctx, fixture, nativeTrackKey, recordingKey, trackMedium, trackPosition, trackAuthority, trackFetchOrder)
	// A different internal track claiming the same normalized slot is ambiguous.
	var fieldsText string
	if err := fixture.f.db.NewRaw(`SELECT fields::text FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, fixture.source.ID, fixture.releaseKey).Scan(ctx, &fieldsText); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fieldsText), &fields); err != nil {
		t.Fatal(err)
	}
	var normalized []json.RawMessage
	if err := json.Unmarshal(fields["normalized_tracks"], &normalized); err != nil {
		t.Fatal(err)
	}
	originalNormalized := append([]json.RawMessage(nil), normalized...)
	var sameSlot map[string]json.RawMessage
	if err := json.Unmarshal(normalized[0], &sameSlot); err != nil {
		t.Fatal(err)
	}
	sameSlot["id"], err = json.Marshal(uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	otherTrack, err := json.Marshal(sameSlot)
	if err != nil {
		t.Fatal(err)
	}
	duplicated, err := json.Marshal(append(normalized, otherTrack))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET fields=jsonb_set(fields,'{normalized_tracks}',?::jsonb,true) WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, string(duplicated), fixture.source.ID, fixture.releaseKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopePosition, fixture.release, fixture.position)
	if err != nil || len(ambiguous.ProviderPositionLinks) != 0 {
		t.Fatalf("position with duplicate normalized tracks = %+v, err=%v", ambiguous.ProviderPositionLinks, err)
	}
	restored, err := json.Marshal(originalNormalized)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET fields=jsonb_set(fields,'{normalized_tracks}',?::jsonb,true) WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, string(restored), fixture.source.ID, fixture.releaseKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, malformedEvidence := range []string{`null`, `{}`, `{"authority":4}`, `{"authority":4,"fetch_order":null}`, `{"authority":-1,"fetch_order":11}`} {
		updated, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET field_evidence=jsonb_set(field_evidence,'{asin}',?::jsonb,true) WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, malformedEvidence, fixture.source.ID, fixture.releaseKey).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if count, err := updated.RowsAffected(); err != nil || count != 1 {
			t.Fatalf("seed malformed ASIN evidence affected %d rows, err=%v", count, err)
		}
		if _, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopeRelease, fixture.release, fixture.release); err == nil {
			t.Fatalf("accepted ASIN with malformed evidence %s", malformedEvidence)
		}
		if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET field_evidence=jsonb_set(field_evidence,'{asin}','{"authority":4,"fetch_order":11}'::jsonb,true) WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, fixture.source.ID, fixture.releaseKey).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopeArtist, fixture.release, artists[0])
	if err != nil {
		t.Fatal(err)
	}
	const nextIdentity = "provider-genre-config-generation-2"
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source SET configuration_identity=? WHERE id=?`, nextIdentity, fixture.source.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`UPDATE app_setting SET setting_value=? WHERE setting_name='musicbrainz_config_identity'`, nextIdentity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopeArtist, fixture.release, artists[0])
	if err != nil {
		t.Fatal(err)
	}
	if first.ProviderGenreBaseline.SourceID != second.ProviderGenreBaseline.SourceID || first.ProviderGenreBaseline.Digest == second.ProviderGenreBaseline.Digest {
		t.Fatalf("configuration generation did not change digest for stable source: before=%+v after=%+v", first.ProviderGenreBaseline, second.ProviderGenreBaseline)
	}
	// A bound recording with accepted empty genres remains empty despite populated
	// release and artist projections; scope values are never inherited.
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET fields=jsonb_set(fields,'{genres}','[]'::jsonb,true), field_evidence=field_evidence || '{"genres":{"authority":4,"fetch_order":9}}'::jsonb WHERE provider_source_id=? AND entity_kind='recording' AND provider_key='cccccccc-cccc-4ccc-8ccc-cccccccccccc'`, fixture.source.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	empty, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopeRecording, fixture.release, recording)
	if err != nil || empty.ProviderGenreBaseline == nil || len(empty.ProviderGenreBaseline.Genres) != 0 {
		t.Fatalf("accepted empty recording baseline = %+v, err=%v", empty.ProviderGenreBaseline, err)
	}
	// Removing only field evidence is different from an accepted empty value.
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET field_evidence=field_evidence-'genres' WHERE provider_source_id=? AND entity_kind='recording' AND provider_key='cccccccc-cccc-4ccc-8ccc-cccccccccccc'`, fixture.source.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	absent, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopeRecording, fixture.release, recording)
	if err != nil || absent.ProviderGenreBaseline != nil {
		t.Fatalf("recording without genre evidence = %+v, err=%v", absent.ProviderGenreBaseline, err)
	}
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET field_evidence=field_evidence || '{"genres":{"authority":4,"fetch_order":9}}'::jsonb WHERE provider_source_id=? AND entity_kind='recording' AND provider_key='cccccccc-cccc-4ccc-8ccc-cccccccccccc'`, fixture.source.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// Local override states are deliberately independent from provider baseline.
	for _, metadata := range []string{
		`{"local_metadata":{"version":1,"scope":"artist","desired":{"genre":{"state":"value","value":[{"id":"00000000-0000-4000-8000-000000000004","source_name":"manual","name":"Manual"}],"authority":"manual","provenance":{"kind":"manual"}}}}}`,
		`{"local_metadata":{"version":1,"scope":"artist","desired":{"genre":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}}}}}`,
		`{}`,
	} {
		if _, err := fixture.f.db.NewRaw(`UPDATE local_artist SET metadata=?::jsonb WHERE id=?`, metadata, artists[0]).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		localDisplay, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopeArtist, fixture.release, artists[0])
		if err != nil || localDisplay.ProviderGenreBaseline == nil || localDisplay.ProviderGenreBaseline.Digest != second.ProviderGenreBaseline.Digest {
			t.Fatalf("local override affected provider baseline: %+v, err=%v", localDisplay.ProviderGenreBaseline, err)
		}
	}
	// A recording has no implicit inheritance from either its release or artists.
	if _, err := fixture.f.db.NewRaw(`DELETE FROM provider_local_recording WHERE local_recording_id=?`, recording).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ReadMetadataDisplay(ctx, librarymetadata.ScopeRecording, fixture.release, recording)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderGenreBaseline != nil {
		t.Fatalf("unbound recording inherited genres: %+v", got.ProviderGenreBaseline)
	}
}

func TestReadMetadataDisplayRejectsMalformedProviderGenreEvidencePostgreSQL(t *testing.T) {
	ctx := context.Background()
	fixture := preparePendingGraphFixture(t)
	assertAcceptedRefreshWakesPendingGraph(t, ctx, fixture)
	artists, _ := seedProviderTagGenreAuthority(t, ctx, fixture)
	setTestArtistGenreFetchOrder(t, ctx, fixture, artists[0])
	if _, err := fixture.f.db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_mode','self-hosted'),('musicbrainz_base_url',?),('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, fixture.source.Endpoint, pendingGraphTestIdentity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	baseline, err := NewLibraryRepository(fixture.f.db).ReadMetadataDisplay(ctx, librarymetadata.ScopeArtist, fixture.release, artists[0])
	if err != nil || baseline.ProviderGenreBaseline == nil {
		t.Fatalf("prove valid baseline before rejection tests: %+v, err=%v", baseline.ProviderGenreBaseline, err)
	}
	for _, evidence := range []string{`null`, `{}`, `{"fetch_order":1}`, `{"authority":-1,"fetch_order":1}`, `{"authority":1.5,"fetch_order":1}`, `{"authority":1,"fetch_order":1.5}`} {
		t.Run(evidence, func(t *testing.T) {
			if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET field_evidence=jsonb_set(field_evidence,'{genres}',?::jsonb,true) WHERE provider_source_id=? AND entity_kind='artist' AND provider_key='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'`, evidence, fixture.source.ID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			_, err := NewLibraryRepository(fixture.f.db).ReadMetadataDisplay(ctx, librarymetadata.ScopeArtist, fixture.release, artists[0])
			if err == nil {
				t.Fatal("malformed accepted genre evidence was silently omitted")
			}
		})
	}
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET fields=jsonb_set(fields,'{genres}','[{"id":"bad-id","name":"broken"}]'::jsonb,true), field_evidence=field_evidence || '{"genres":{"authority":1,"fetch_order":1}}'::jsonb WHERE provider_source_id=? AND entity_kind='artist' AND provider_key='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'`, fixture.source.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLibraryRepository(fixture.f.db).ReadMetadataDisplay(ctx, librarymetadata.ScopeArtist, fixture.release, artists[0]); err == nil {
		t.Fatal("malformed genre reference was silently omitted")
	}
	// Present JSON null is malformed evidence even when the corresponding genres
	// are explicitly null; only SQL NULL evidence means absent evidence.
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET fields=jsonb_set(fields,'{genres}','null'::jsonb,true), field_evidence=jsonb_set(field_evidence,'{genres}','null'::jsonb,true) WHERE provider_source_id=? AND entity_kind='artist' AND provider_key='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'`, fixture.source.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLibraryRepository(fixture.f.db).ReadMetadataDisplay(ctx, librarymetadata.ScopeArtist, fixture.release, artists[0]); err == nil {
		t.Fatal("JSON null evidence with JSON null genres was silently omitted")
	}
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET field_evidence=jsonb_set(field_evidence,'{genres}','{"authority":1,"fetch_order":1}'::jsonb,true) WHERE provider_source_id=? AND entity_kind='artist' AND provider_key='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'`, fixture.source.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := NewLibraryRepository(fixture.f.db).ReadMetadataDisplay(ctx, librarymetadata.ScopeArtist, fixture.release, artists[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderGenreBaseline != nil {
		t.Fatalf("explicit JSON null genres should be absent: %+v", got.ProviderGenreBaseline)
	}
}

func setTestArtistGenreFetchOrder(t *testing.T, ctx context.Context, fixture pendingGraphFixture, artistID uuid.UUID) {
	t.Helper()
	result, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET field_evidence=jsonb_set(field_evidence,'{genres,fetch_order}','9'::jsonb,true) WHERE provider_source_id=? AND entity_kind='artist' AND provider_key='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'`, fixture.source.ID).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		t.Fatalf("update artist genre evidence for local artist %s affected %d rows, err=%v", artistID, count, err)
	}
}

func seedPositionMediaEvidence(t *testing.T, ctx context.Context, fixture pendingGraphFixture, nativeTrackKey, recordingKey string, medium, position, authority int, fetchOrder int64) {
	t.Helper()
	media := make([]map[string]any, medium+1)
	for index := range media {
		media[index] = map[string]any{"position": index + 1, "tracks": []any{}}
	}
	tracks := make([]map[string]any, position+1)
	for index := range tracks {
		tracks[index] = map[string]any{}
	}
	tracks[position] = map[string]any{
		"id": nativeTrackKey, "number": "1",
		"recording": map[string]any{"id": recordingKey, "title": "Track"},
	}
	media[medium]["tracks"] = tracks
	encoded, err := json.Marshal(media)
	if err != nil {
		t.Fatal(err)
	}
	evidence := fmt.Sprintf(`{"media":{"authority":%d,"fetch_order":%d}}`, authority, fetchOrder)
	result, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET fields=jsonb_set(fields,'{media}',?::jsonb,true), field_evidence=field_evidence || ?::jsonb WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, string(encoded), evidence, fixture.source.ID, fixture.releaseKey).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("seed position media evidence affected %d rows, err=%v", count, err)
	}
}
