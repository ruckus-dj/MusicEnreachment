//go:build integration

package persistence

import (
	"context"
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
		})
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
