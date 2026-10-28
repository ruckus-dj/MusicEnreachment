//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestReadLibraryReleaseDetailWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	localOnlyID, linkedID := uuid.New(), uuid.New()
	artistA, artistB := uuid.New(), uuid.New()
	trackID, recordingID := uuid.New(), uuid.New()
	variantA, variantB := uuid.New(), uuid.New()
	rootID, locationID := uuid.New(), uuid.New()
	positionA, positionB := uuid.New(), uuid.New()
	providerID, providerReleaseID := uuid.New(), uuid.New()
	musicBrainzProviderID, providerArtistA, providerArtistB := uuid.New(), uuid.New(), uuid.New()
	artistAMbid, artistBMbid := uuid.New(), uuid.New()
	musicBrainzReleaseID, releaseMBID := uuid.New(), uuid.New()

	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title,metadata,metadata_version) VALUES (?,?,'{"year":2024}',3)`, []any{localOnlyID, "Album"}},
		{`INSERT INTO local_release(id,title) VALUES (?,?)`, []any{linkedID, "Provider linked"}},
		{`INSERT INTO local_artist(id,name) VALUES (?,?),(?,?)`, []any{artistA, "Artist A", artistB, "Artist B"}},
		{`INSERT INTO release_artist(release_id,position,artist_id,name,name_join_phrase,role) VALUES (?,?,?,?,?,?),(?,?,?,?,?,?)`, []any{localOnlyID, 0, artistA, "A credit", " & ", "performer", localOnlyID, 1, artistB, "B credit", "", "composer"}},
		{`INSERT INTO local_track(id,title,metadata,metadata_version) VALUES (?,?,'{"genre":"rock"}',7)`, []any{trackID, "Song"}},
		{`INSERT INTO local_recording(id,title) VALUES (?,?)`, []any{recordingID, "Recording"}},
		{`INSERT INTO release_track(id,release_id,track_id,recording_id,medium,position,displayed_number,duration_ms) VALUES (?,?,?,?,0,0,'A1',NULL),(?,?,?,?,1,0,'B1',0)`, []any{positionA, localOnlyID, trackID, recordingID, positionB, localOnlyID, trackID, recordingID}},
		{`INSERT INTO track_artist(track_id,position,artist_id,name,name_join_phrase,role) VALUES (?,?,?,?,?,?)`, []any{trackID, 0, artistA, "Track credit", "", "vocalist"}},
		{`INSERT INTO recording_artist(recording_id,position,artist_id,name,name_join_phrase,role) VALUES (?,?,?,?,?,?)`, []any{recordingID, 0, artistA, "Recording credit", "", "producer"}},
		{`INSERT INTO release_medium(id,release_id,medium,title,format) VALUES (?, ?, 5, 'Bonus disc', 'CD')`, []any{uuid.New(), linkedID}},
		{`INSERT INTO media_variant(id,size_bytes) VALUES (?,1024),(?,2048)`, []any{variantA, variantB}},
		{`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,metadata) VALUES (?,?,?,true,true,'automatic','policy-v1','{"source":"one"}'),(?,?,?,false,false,'manual',NULL,'{}')`, []any{uuid.New(), trackID, variantA, uuid.New(), trackID, variantB}},
		{`INSERT INTO source_root(id,configured_path,display_name,status,enabled,scan_generation,inventory_path) VALUES (?,?,'Library','available',false,1,?)`, []any{rootID, "/srv/library", "/srv/library"}},
		{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,?,1024,?,1,'audio',?)`, []any{locationID, rootID, "disc/song.flac", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), variantA}},
		{`INSERT INTO provider(id,code) VALUES (?, 'fixture')`, []any{providerID}},
		{`INSERT INTO provider(id,code) VALUES (?, 'musicbrainz')`, []any{musicBrainzProviderID}},
		{`INSERT INTO provider_artist(id,provider_id,provider_key,name) VALUES (?,?,?,'Artist A'),(?,?,?,'Artist B')`, []any{providerArtistA, musicBrainzProviderID, artistAMbid.String(), providerArtistB, musicBrainzProviderID, artistBMbid.String()}},
		{`INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method) VALUES (?,?,?,'manual'),(?,?,?,'manual')`, []any{musicBrainzProviderID, providerArtistA, artistA, musicBrainzProviderID, providerArtistB, artistB}},
		{`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'linked')`, []any{providerReleaseID, providerID, "linked-release"}},
		{`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`, []any{providerID, providerReleaseID, linkedID}},
		{`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'MusicBrainz linked')`, []any{musicBrainzReleaseID, musicBrainzProviderID, releaseMBID.String()}},
		{`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`, []any{musicBrainzProviderID, musicBrainzReleaseID, linkedID}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert library detail fixture: %v", err)
		}
	}

	detail, err := persistence.NewLibraryRepository(db).ReadReleaseDetail(ctx, localOnlyID)
	if err != nil {
		t.Fatalf("read local release detail: %v", err)
	}
	if detail.Title != "Album" || detail.MetadataVersion != 3 || len(detail.Artists) != 2 || detail.Artists[0].CreditName != "A credit" || detail.Artists[1].CreditName != "B credit" || detail.Artists[0].Role != "performer" || detail.Artists[0].JoinPhrase != " & " {
		t.Fatalf("release detail metadata/credits = %+v", detail)
	}
	if detail.PrimaryArtistMBIDs[artistA] == nil || *detail.PrimaryArtistMBIDs[artistA] != artistAMbid || detail.PrimaryArtistMBIDs[artistB] == nil || *detail.PrimaryArtistMBIDs[artistB] != artistBMbid {
		t.Fatalf("MusicBrainz identities = %+v", detail.PrimaryArtistMBIDs)
	}
	if len(detail.Tracks) != 2 {
		t.Fatalf("release positions = %+v", detail.Tracks)
	}
	tracksByPosition := make(map[uuid.UUID]persistence.LibraryReleaseTrackDetail, len(detail.Tracks))
	for _, track := range detail.Tracks {
		tracksByPosition[track.ID] = track
	}
	first, firstOK := tracksByPosition[positionA]
	second, secondOK := tracksByPosition[positionB]
	if !firstOK || !secondOK || first.ID == second.ID {
		t.Fatalf("release positions = %+v", detail.Tracks)
	}
	if first.LocalTrackID != trackID || second.LocalTrackID != trackID || first.RecordingID == nil || *first.RecordingID != recordingID || first.RecordingTitle == nil || *first.RecordingTitle != "Recording" || first.MediumID == uuid.Nil || second.MediumID == uuid.Nil || first.MediumID == second.MediumID || first.Medium != 1 || first.Position != 1 || first.DurationMS.Valid || second.Medium != 2 || second.Position != 1 || !second.DurationMS.Valid || second.DurationMS.Int64 != 0 {
		t.Fatalf("one-based track positions/identity = %+v %+v", first, second)
	}
	var blankMediumTitle, blankMediumFormat string
	if err := db.NewRaw(`SELECT title,format FROM release_medium WHERE id=?`, first.MediumID).Scan(ctx, &blankMediumTitle, &blankMediumFormat); err != nil || blankMediumTitle != "" || blankMediumFormat != "" {
		t.Fatalf("legacy release_track writer medium defaults = %q/%q, %v; want blank values", blankMediumTitle, blankMediumFormat, err)
	}
	assertDuplicateOccurrence := func(label string, track persistence.LibraryReleaseTrackDetail) {
		if len(track.Artists) != 1 || track.Artists[0].ArtistID != artistA || track.Artists[0].CreditName != "Track credit" || track.Artists[0].Role != "vocalist" {
			t.Fatalf("%s duplicate position credits = %+v", label, track.Artists)
		}
		sources := make(map[uuid.UUID]persistence.LibraryTrackSourceDetail, len(track.Sources))
		for _, source := range track.Sources {
			sources[source.MediaVariantID] = source
		}
		variantASource, variantAOK := sources[variantA]
		variantBSource, variantBOK := sources[variantB]
		if len(track.Sources) != 2 || len(sources) != 2 || !variantAOK || !variantBOK || len(variantASource.Locations) != 1 || len(variantBSource.Locations) != 0 {
			t.Fatalf("%s duplicate position sources/locations = %+v", label, track.Sources)
		}
		location := variantASource.Locations[0]
		if location.RootID != rootID || location.LocationID != locationID || location.RelativePath != "disc/song.flac" || location.RootStatus != "available" || location.RootEnabled {
			t.Fatalf("%s source location/root state = %+v", label, location)
		}
	}
	assertDuplicateOccurrence("first", first)
	assertDuplicateOccurrence("second", second)
	linked, err := persistence.NewLibraryRepository(db).ReadReleaseDetail(ctx, linkedID)
	if err != nil || linked.ID != linkedID {
		t.Fatalf("provider-linked release detail = %+v, %v", linked, err)
	}
	if !linked.CanRefreshMusicBrainz || detail.CanRefreshMusicBrainz {
		t.Fatalf("MusicBrainz refresh capability local=%t linked=%t, want false/true", detail.CanRefreshMusicBrainz, linked.CanRefreshMusicBrainz)
	}
	if len(linked.Media) != 1 || linked.Media[0].Medium != 5 || linked.Media[0].Title != "Bonus disc" || linked.Media[0].Format != "CD" || len(linked.Tracks) != 0 {
		t.Fatalf("empty medium detail = %+v; expected retained zero-based persistence coordinate and no positions", linked)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM release_medium WHERE release_id=? AND medium=0`, localOnlyID); err == nil {
		t.Fatal("deleting a medium with a release position succeeded; expected restrictive FK")
	}
	if _, err := persistence.NewLibraryRepository(db).ReadReleaseDetail(ctx, uuid.New()); err == nil || !errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
		t.Fatalf("unknown release error = %v, want not found", err)
	}
}
