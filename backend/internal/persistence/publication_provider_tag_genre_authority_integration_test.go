//go:build integration

package persistence

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

func TestPublicationProviderTagRecordingArtistGenreAuthorityPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := preparePendingGraphFixture(t)
	assertAcceptedRefreshWakesPendingGraph(t, ctx, fixture)
	artists, recordingID := seedProviderTagGenreAuthority(t, ctx, fixture)
	artistMetadata := `{"disambiguation":"local identity","local_metadata":{"version":1,"scope":"artist","desired":{"sort_name":{"state":"value","value":"Artist, Local","authority":"manual","provenance":{"kind":"manual"}}}}}`
	if _, err := fixture.f.db.NewRaw(`UPDATE local_artist SET metadata=?::jsonb,metadata_revision=metadata_revision+1 WHERE id=?`, artistMetadata, artists[0]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_artist SET metadata='{"disambiguation":"provider identity"}'::jsonb WHERE id=(SELECT provider_artist_id FROM provider_local_artist WHERE provider_id=? AND local_artist_id=?)`, fixture.source.ProviderID, artists[0]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	providerContext := readProviderGenreAuthorityContext(t, ctx, fixture)
	const artistKey = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if len(providerContext.ArtistFacts) != 1 || localArtistSortName(providerContext.ArtistFacts, artistKey) != "Artist, Local" {
		t.Fatalf("bound provider artist facts = %+v, want local sort metadata", providerContext.ArtistFacts)
	}
	disambiguation, present, err := providerArtistMetadataString(providerContext.ArtistFacts, artistKey, "disambiguation")
	if err != nil || !present || disambiguation != "provider identity" {
		t.Fatalf("bound provider artist disambiguation = %q, present %v, error %v", disambiguation, present, err)
	}
	var trackID uuid.UUID
	if err := fixture.f.db.NewRaw(`SELECT track_id FROM release_track WHERE id=?`, fixture.position).Scan(ctx, &trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`INSERT INTO track_artist(track_id,position,artist_id,name,name_join_phrase) VALUES(?,0,?,'credit','')`, trackID, artists[0]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixture.f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		credits, err := firstPublicationCredits(ctx, tx, "track_artist", "track_id", trackID)
		if err != nil {
			return err
		}
		if len(credits) != 1 || credits[0].SortName != "Artist, Local" || credits[0].MetadataRevision <= 0 || credits[0].Disambiguation != "provider identity" {
			return errors.New("first publication credit omitted artist sort, revision, or bound provider disambiguation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name       string
		metadata   string
		wantGenres []string
	}{
		{
			name:       "manual value overrides provider genres",
			metadata:   genreArtistMetadata(`{"state":"value","value":[{"id":"00000000-0000-4000-8000-000000000002","source_name":"manual","name":"Manual"}],"authority":"manual","provenance":{"kind":"manual"}}`),
			wantGenres: []string{"Manual"},
		},
		{
			name:       "explicit empty clears current genre",
			metadata:   genreArtistMetadata(`{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}}`),
			wantGenres: []string{},
		},
		{
			name:       "absent falls back to accepted provider genre",
			metadata:   genreArtistMetadata(`{"state":"absent","authority":"manual","provenance":{"kind":"manual"}}`),
			wantGenres: []string{"Provider Artist"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := fixture.f.db.NewRaw(`UPDATE local_artist SET metadata=?::jsonb, metadata_revision=metadata_revision+1 WHERE id=?`, test.metadata, artists[0]).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			snapshot := readProviderGenreAuthorityContext(t, ctx, fixture)
			if !snapshot.RecordingBound || len(snapshot.GenreArtists) != 2 {
				t.Fatalf("recording artist snapshot: recording bound %v, artists %d", snapshot.RecordingBound, len(snapshot.GenreArtists))
			}
			input := providerGenreAuthorityInput(t, snapshot)
			if err := validateProviderGenreAuthorityInput(ctx, fixture, input); err != nil {
				t.Fatalf("validate accepted provider authority baseline: %v", err)
			}
			got := input.DesiredTags["GENRE"]
			if !reflect.DeepEqual(got, test.wantGenres) {
				t.Fatalf("projected genre = %#v, want %#v", got, test.wantGenres)
			}
		})
	}

	// An explicit empty on one artist must not suppress a nonempty genre from
	// another recording artist.
	if _, err := fixture.f.db.NewRaw(`UPDATE local_artist SET metadata=?::jsonb, metadata_revision=metadata_revision+1 WHERE id=?`, genreArtistMetadata(`{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}}`), artists[0]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`UPDATE local_artist SET metadata=?::jsonb, metadata_revision=metadata_revision+1 WHERE id=?`, genreArtistMetadata(`{"state":"value","value":[{"id":"00000000-0000-4000-8000-000000000003","source_name":"other","name":"Other"}],"authority":"manual","provenance":{"kind":"manual"}}`), artists[1]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	mixed := providerGenreAuthorityInput(t, readProviderGenreAuthorityContext(t, ctx, fixture))
	if !reflect.DeepEqual(mixed.DesiredTags["GENRE"], []string{"Other"}) {
		t.Fatalf("mixed artist genre result = %#v, want [Other]", mixed.DesiredTags["GENRE"])
	}

	// Capture a valid contract, then prove both a local metadata revision/content
	// change and a credit-order change invalidate its provider authority.
	if _, err := fixture.f.db.NewRaw(`UPDATE local_artist SET metadata='{}'::jsonb, metadata_revision=metadata_revision+1 WHERE id=?`, artists[1]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	captured := providerGenreAuthorityInput(t, readProviderGenreAuthorityContext(t, ctx, fixture))
	if err := validateProviderGenreAuthorityInput(ctx, fixture, captured); err != nil {
		t.Fatalf("valid artist authority baseline rejected: %v", err)
	}
	if _, err := fixture.f.db.NewRaw(`UPDATE local_artist SET metadata=?::jsonb, metadata_revision=metadata_revision+1 WHERE id=?`, genreArtistMetadata(`{"state":"value","value":[{"id":"00000000-0000-4000-8000-000000000004","source_name":"changed","name":"Changed"}],"authority":"manual","provenance":{"kind":"manual"}}`), artists[0]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := validateProviderGenreAuthorityInput(ctx, fixture, captured); !errors.Is(err, ErrPublicationAdmissionConflict) {
		t.Fatalf("changed artist metadata authority error = %v, want admission conflict", err)
	}

	captured = providerGenreAuthorityInput(t, readProviderGenreAuthorityContext(t, ctx, fixture))
	if err := validateProviderGenreAuthorityInput(ctx, fixture, captured); err != nil {
		t.Fatalf("refreshed artist authority baseline rejected: %v", err)
	}
	if _, err := fixture.f.db.NewRaw(`DELETE FROM recording_artist WHERE recording_id=?`, recordingID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`INSERT INTO recording_artist(recording_id,position,artist_id,name) VALUES (?,0,?,'credit'), (?,1,?,'credit')`, recordingID, artists[1], recordingID, artists[0]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := validateProviderGenreAuthorityInput(ctx, fixture, captured); !errors.Is(err, ErrPublicationAdmissionConflict) {
		t.Fatalf("changed artist credit order authority error = %v, want admission conflict", err)
	}
	if _, err := fixture.f.db.NewRaw(`DELETE FROM provider_local_artist WHERE provider_id=? AND local_artist_id=?`, fixture.source.ProviderID, artists[0]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	withoutArtistBinding := readProviderGenreAuthorityContext(t, ctx, fixture)
	if len(withoutArtistBinding.ArtistFacts) != 0 {
		t.Fatalf("provider artist facts without a local binding = %+v, want none", withoutArtistBinding.ArtistFacts)
	}
}

func TestReadFirstPublicationProviderGenresUsesCurrentSourceAndBindingsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := preparePendingGraphFixture(t)
	assertAcceptedRefreshWakesPendingGraph(t, ctx, fixture)
	for name, value := range map[string]string{
		"musicbrainz_mode":            "custom",
		"musicbrainz_base_url":        fixture.source.Endpoint,
		"musicbrainz_config_identity": pendingGraphTestIdentity,
	} {
		if _, err := fixture.f.db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES(?,?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, name, value).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	var snapshot *PublicationProviderTagContext
	if err := fixture.f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		snapshot, err = readFirstPublicationProviderGenres(ctx, tx, fixture.release, fixture.position)
		return err
	}); err != nil {
		t.Fatalf("read first-publication genres with real provider bindings: %v", err)
	}
	if snapshot == nil || snapshot.Source.ID != fixture.source.ID || snapshot.Position.ID == uuid.Nil {
		t.Fatalf("bound first-publication provider context = %+v", snapshot)
	}

	if _, err := fixture.f.db.NewRaw(`DELETE FROM provider_local_track WHERE provider_id=? AND local_track_id=(SELECT track_id FROM release_track WHERE id=?)`, fixture.source.ProviderID, fixture.position).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot = nil
	if err := fixture.f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		snapshot, err = readFirstPublicationProviderGenres(ctx, tx, fixture.release, fixture.position)
		return err
	}); err != nil {
		t.Fatalf("read first-publication genres without a current provider track binding: %v", err)
	}
	if snapshot != nil {
		t.Fatalf("provider context without a current binding = %+v, want nil", snapshot)
	}
}

func seedProviderTagGenreAuthority(t *testing.T, ctx context.Context, fixture pendingGraphFixture) ([2]uuid.UUID, uuid.UUID) {
	t.Helper()
	const artistKey = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const recordingKey = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	artists := [2]uuid.UUID{uuid.New(), uuid.New()}
	recordingID := uuid.New()
	var providerRecordingID uuid.UUID
	if err := fixture.f.db.NewRaw(`SELECT id FROM provider_recording WHERE provider_id=? AND provider_key=?`, fixture.source.ProviderID, recordingKey).Scan(ctx, &providerRecordingID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`INSERT INTO local_recording(id,title) VALUES (?,'recording')`, recordingID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method) VALUES(?,?,?,'manual')`, fixture.source.ProviderID, providerRecordingID, recordingID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`UPDATE release_track SET recording_id=? WHERE id=?`, recordingID, fixture.position).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for index, artistID := range artists {
		if _, err := fixture.f.db.NewRaw(`INSERT INTO local_artist(id,name) VALUES (?,?)`, artistID, "local artist").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.f.db.NewRaw(`INSERT INTO recording_artist(recording_id,position,artist_id,name) VALUES (?,?,?,?)`, recordingID, index, artistID, "credit").Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var providerArtistID uuid.UUID
	if err := fixture.f.db.NewRaw(`SELECT id FROM provider_artist WHERE provider_id=? AND provider_key=?`, fixture.source.ProviderID, artistKey).Scan(ctx, &providerArtistID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.f.db.NewRaw(`INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method) VALUES(?,?,?,'manual')`, fixture.source.ProviderID, providerArtistID, artists[0]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	artistFields := `[{"id":"00000000-0000-4000-8000-000000000001","name":"Provider Artist"}]`
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET fields=jsonb_set(fields,'{genres}',?::jsonb,true), field_evidence=field_evidence || '{"genres":{"authority":3}}'::jsonb WHERE provider_source_id=? AND entity_kind='artist' AND provider_key=?`, artistFields, fixture.source.ID, artistKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	credits := `[{"name":"Artist","joinphrase":"","artist":{"id":"` + artistKey + `"}}]`
	if _, err := fixture.f.db.NewRaw(`UPDATE provider_source_entity SET fields=jsonb_set(fields,'{artist-credit}',?::jsonb,true), field_evidence=field_evidence || '{"artist-credit":{"authority":3}}'::jsonb WHERE provider_source_id=? AND entity_kind='recording' AND provider_key=?`, credits, fixture.source.ID, recordingKey).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return artists, recordingID
}

func readProviderGenreAuthorityContext(t *testing.T, ctx context.Context, fixture pendingGraphFixture) *PublicationProviderTagContext {
	t.Helper()
	var snapshot *PublicationProviderTagContext
	if err := fixture.f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return readPublicationProviderTagContextForPositionTx(ctx, tx, fixture.release, fixture.position, fixture.source.Endpoint, pendingGraphTestIdentity, false, &snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	if snapshot == nil {
		t.Fatal("provider genre context is missing")
	}
	return snapshot
}

func providerGenreAuthorityInput(t *testing.T, snapshot *PublicationProviderTagContext) PublicationInputs {
	t.Helper()
	input := publicationProviderTagFixture(t)
	input.ProviderTags.Current.Tags["GENRE"] = []string{"Current Genre"}
	patch, err := BuildPublicationProviderCanonicalTagPatch(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	protection, err := BuildPublicationProviderTagProtection(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	input.ProviderTags.ProviderSourceID = snapshot.Source.ID
	input.ProviderTags.Endpoint = snapshot.Source.Endpoint
	input.ProviderTags.ConfigurationIdentity = snapshot.Source.ConfigurationIdentity
	input.ProviderTags.AuthorityRevision = snapshot.Digest
	input.ProviderTags.Patch = patch
	input.ProviderTags.ProtectedKeys = protection.Keys
	input.DesiredTags, err = publication.ApplyProviderTagPatch(input.ProviderTags.Current.Tags, patch, protection.Keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate provider genre contract: %v", err)
	}
	return input
}

func validateProviderGenreAuthorityInput(ctx context.Context, fixture pendingGraphFixture, input PublicationInputs) error {
	return fixture.f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return validatePublicationProviderTagAuthorityTx(ctx, tx, fixture.release, fixture.position, input)
	})
}

func genreArtistMetadata(field string) string {
	return `{"local_metadata":{"version":1,"scope":"artist","desired":{"genre":` + field + `}}}`
}
