//go:build integration

package persistence

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestReadPositionTagComparisonUsesBoundProviderAndExplicitOverrides(t *testing.T) {
	ctx := context.Background()
	fixture := preparePendingGraphFixture(t)
	db := fixture.f.db
	exec := func(query string, args ...any) {
		t.Helper()
		result, err := db.NewRaw(query, args...).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed == 0 {
			t.Fatalf("tag comparison fixture mutation changed %d rows: %v", changed, err)
		}
	}
	assertAcceptedRefreshWakesPendingGraph(t, ctx, fixture)
	exec(`INSERT INTO app_setting(setting_name,setting_value) VALUES
		('musicbrainz_mode','self-hosted'),('musicbrainz_base_url',?),('musicbrainz_config_identity',?)
		ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, fixture.source.Endpoint, pendingGraphTestIdentity)
	repo := NewLibraryRepository(db)
	read := func() LibraryTagComparison {
		t.Helper()
		got, err := repo.ReadPositionTagComparison(ctx, fixture.release, fixture.position)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if baseline := read(); baseline.Provider.Name != "MusicBrainz" || baseline.Provider.Status != "saved" ||
		!slices.Equal(baseline.Provider.Tags["ALBUM"], []string{"Refreshed provider release"}) {
		t.Fatalf("read accepted exact MusicBrainz baseline = %+v", baseline)
	}

	native := NewLocalProviderRepository(db)
	recording, err := native.Save(ctx, LocalProviderItem{Kind: "recording", Title: "Local comparison track"}, true)
	if err != nil {
		t.Fatal(err)
	}
	localPosition := uuid.New()
	release, err := native.Save(ctx, LocalProviderItem{
		Kind: "release", Title: "Local comparison release",
		Tracks: []LocalProviderTrack{{ID: localPosition, RecordingID: recording.ID}},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method)
		VALUES(?,?,?,'manual')`, localProviderID, release.ID, fixture.release)
	exec(`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method)
		SELECT ?,?,track_id,'manual' FROM release_track WHERE id=?`, localProviderID, localPosition, fixture.position)
	exec(`UPDATE release_track SET metadata=jsonb_set(metadata,'{local_metadata}',?::jsonb,true) WHERE id=?`,
		`{"version":1,"scope":"position","desired":{
			"comment":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}},
			"key":{"state":"value","value":"D minor","authority":"automatic","provenance":{"kind":"source_metadata","source_field":"KEY"}}
		}}`, fixture.position)
	both := read()
	if both.Provider.Name != "MusicBrainz" || both.Provider.Status != "saved" {
		t.Fatalf("provider with exact MusicBrainz and Local bindings = %+v", both.Provider)
	}
	if cleared := both.Overrides["COMMENT"]; cleared == nil || len(cleared) != 0 {
		t.Fatalf("manual clear = %#v, want explicit empty list", cleared)
	}
	if _, exists := both.Overrides["KEY"]; exists {
		t.Fatalf("automatic metadata exposed as override: %#v", both.Overrides)
	}
	exec(`UPDATE provider_source_entity SET field_evidence=field_evidence-'__tracks'
		WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, fixture.source.ID, fixture.releaseKey)
	if unavailable := read(); unavailable.Provider.Name != "MusicBrainz" || unavailable.Provider.Status != "unavailable" ||
		len(unavailable.Provider.Tags) != 0 {
		t.Fatalf("provider after withdrawing accepted MusicBrainz edge = %+v, want unavailable without Local fallback", unavailable.Provider)
	}
	exec(`DELETE FROM provider_local_track WHERE provider_id=? AND local_track_id=(SELECT track_id FROM release_track WHERE id=?)`,
		fixture.source.ProviderID, fixture.position)
	if local := read(); local.Provider.Name != "Local" || local.Provider.Status != "saved" ||
		!slices.Equal(local.Provider.Tags["ALBUM"], []string{"Local comparison release"}) {
		t.Fatalf("provider with only an exact Local binding = %+v", local.Provider)
	}
	if _, err := repo.ReadPositionTagComparison(ctx, uuid.New(), fixture.position); !errors.Is(err, ErrLibraryPositionNotFound) {
		t.Fatalf("wrong release for exact position returned %v", err)
	}
}
