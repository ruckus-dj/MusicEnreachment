//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLibraryMetadataPersistsPerOwnerAndPreservesOtherMetadata(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repo := persistence.NewLibraryRepository(db)
	release, recording, track, positionA, positionB := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{{`INSERT INTO local_release(id,title,metadata) VALUES(?,?, '{"other":{"keep":true}}')`, []any{release, "Album"}}, {`INSERT INTO local_recording(id,title) VALUES(?,?)`, []any{recording, "Song"}}, {`INSERT INTO local_track(id,title) VALUES(?,?)`, []any{track, "Song"}}, {`INSERT INTO release_track(id,release_id,track_id,recording_id,medium,position) VALUES(?,?,?, ?,1,1),(?,?,?, ?,1,2)`, []any{positionA, release, track, recording, positionB, release, track, recording}}} {
		if _, err := db.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	update := func(scope librarymetadata.Scope, owner uuid.UUID, name string, value any) persistence.LibraryMetadata {
		t.Helper()
		state := librarymetadata.ManualFieldValue(librarymetadata.StateValue, value, true)
		envelope := librarymetadata.Envelope{Version: 1, Scope: scope, Desired: map[string]librarymetadata.FieldValue{name: state}}
		got, err := repo.UpdateMetadata(ctx, scope, release, owner, 1, envelope)
		if err != nil {
			t.Fatalf("update %s: %v", scope, err)
		}
		return got
	}
	update(librarymetadata.ScopeRelease, release, "compilation", true)
	update(librarymetadata.ScopeRecording, recording, "isrc", []any{"USABC1234567"})
	update(librarymetadata.ScopeTrack, track, "genre", []any{"jazz"})
	update(librarymetadata.ScopePosition, positionA, "track_number", 1)
	update(librarymetadata.ScopePosition, positionB, "track_number", 2)
	for _, item := range []struct {
		scope librarymetadata.Scope
		owner uuid.UUID
		field string
		want  any
	}{{librarymetadata.ScopeRelease, release, "compilation", true}, {librarymetadata.ScopeRecording, recording, "isrc", []any{"USABC1234567"}}, {librarymetadata.ScopeTrack, track, "genre", []any{"jazz"}}, {librarymetadata.ScopePosition, positionA, "track_number", json.Number("1")}, {librarymetadata.ScopePosition, positionB, "track_number", json.Number("2")}} {
		got, err := repo.ReadMetadata(ctx, item.scope, release, item.owner)
		if err != nil {
			t.Fatal(err)
		}
		field := got.Envelope.Desired[item.field]
		if field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual || field.State != librarymetadata.StateValue {
			t.Fatalf("unexpected field: %+v", field)
		}
		if field.Value == nil {
			t.Fatalf("missing field value: %+v", field)
		}
	}
	var other string
	if err := db.NewRaw(`SELECT metadata->'other'->>'keep' FROM local_release WHERE id=?`, release).Scan(ctx, &other); err != nil || other != "true" {
		t.Fatalf("unrelated metadata = %q, %v", other, err)
	}
	_, err := repo.UpdateMetadata(ctx, librarymetadata.ScopePosition, release, positionA, 1, librarymetadata.Envelope{Version: 1, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{"track_number": librarymetadata.ManualFieldValue(librarymetadata.StateValue, 3, true)}})
	if !errors.Is(err, persistence.ErrLibraryMetadataConflict) {
		t.Fatalf("stale revision error = %v", err)
	}
	if _, err := repo.ReadMetadata(ctx, librarymetadata.ScopePosition, uuid.New(), positionA); !errors.Is(err, persistence.ErrLibraryMetadataNotFound) {
		t.Fatalf("mismatched release error = %v", err)
	}
}
