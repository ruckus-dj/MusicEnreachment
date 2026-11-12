//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// parsePostgresTimestamp accepts the timestamptz text representation in the
// session time zone, which depends on the server configuration rather than the
// instant stored. Comparing instants avoids pinning an incidental rendering.
func parsePostgresTimestamp(t *testing.T, value string) time.Time {
	t.Helper()
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999-07:00"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed
		}
	}
	t.Fatalf("parse PostgreSQL timestamp %q", value)
	return time.Time{}
}

// assertJSONEqual compares decoded JSON values so that jsonb formatting such as
// whitespace or key ordering does not change the expected meaning.
func assertJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode actual value %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode expected value %s: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("actual value = %s, want %s", got, want)
	}
}

func TestReadReleaseDetailIncludesCurrentPublicationPerPositionPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	releaseID, trackID, artistID := uuid.New(), uuid.New(), uuid.New()
	publishedPositionID, unpublishedPositionID := uuid.New(), uuid.New()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO local_artist(id,name) VALUES (?,'Release Artist')`, artistID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO local_release(id,title) VALUES (?,?)`, releaseID, "Album").Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO release_artist(release_id,position,artist_id,name,name_join_phrase) VALUES (?,0,?,'Release Artist','')`, releaseID, artistID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO release_medium(id,release_id,medium,title,format) VALUES (?,?,1,'Disc 1','MKA')`, uuid.New(), releaseID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, trackID, "Song").Exec(ctx); err != nil {
			return err
		}
		for _, position := range []struct {
			id      uuid.UUID
			number  int
			display string
		}{{publishedPositionID, 1, "1"}, {unpublishedPositionID, 2, "2"}} {
			if _, err := tx.NewRaw(`INSERT INTO release_track(id,release_id,track_id,position,medium,displayed_number) VALUES (?,?,?,?,1,?)`, position.id, releaseID, trackID, position.number, position.display).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	generation := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?, ?, ?, ?, '{}', '{"facts":{"codec":"FLAC","container":"MKA"}}', '{}', '{}', 1, '2026-11-05T12:30:00Z')`, publishedPositionID, uuid.New(), generation, "Artist/Album/01 - Song.mka").Exec(ctx); err != nil {
		t.Fatal(err)
	}

	detail, err := NewLibraryRepository(db).ReadReleaseDetail(ctx, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Tracks) != 2 {
		t.Fatalf("release detail positions = %d, want 2", len(detail.Tracks))
	}
	var published, unpublished *LibraryPositionPublication
	for i := range detail.Tracks {
		track := &detail.Tracks[i]
		switch track.ID {
		case publishedPositionID:
			published = track.Publication
		case unpublishedPositionID:
			unpublished = track.Publication
		}
	}
	if published == nil {
		t.Fatal("published position has no publication")
	}
	if published.Generation != generation || published.RelativePath != "Artist/Album/01 - Song.mka" {
		t.Fatalf("published position summary = %+v", published)
	}
	if !parsePostgresTimestamp(t, published.ValidatedAt).Equal(time.Date(2026, time.November, 5, 12, 30, 0, 0, time.UTC)) {
		t.Fatalf("published position validated at = %q", published.ValidatedAt)
	}
	assertJSONEqual(t, published.ActualAudio, `{"facts":{"codec":"FLAC","container":"MKA"}}`)
	if unpublished != nil {
		t.Fatalf("unpublished position has publication: %+v", unpublished)
	}
}
