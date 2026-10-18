//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestReadManualLyricsReceiptProjectionPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	lyrics := "[ar:日本語]\r\n[00:01.00]Привет — café\r\n"
	digestBytes := sha256.Sum256([]byte(lyrics))
	digest := hex.EncodeToString(digestBytes[:])
	positionID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New()
	generation, epoch := uuid.New(), uuid.New()
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?, 'lyrics receipt')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'lyrics receipt')`, trackID); err != nil {
		t.Fatal(err)
	}
	envelope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{
		"synced_lyrics": librarymetadata.ManualFieldValue(librarymetadata.StateValue, lyrics, true),
	}}
	metadata, err := json.Marshal(map[string]any{"local_metadata": envelope})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position,metadata,metadata_revision) VALUES (?,?,?,0,?::jsonb,7)`, positionID, releaseID, trackID, string(metadata)); err != nil {
		t.Fatal(err)
	}
	assets := `{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`
	embeddedAssets := `{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"embedded","sha256":"` + digest + `","format":"lrc"}}`
	actualBytes, err := json.Marshal(map[string]any{"version": 1, "tags": map[string][]string{"TITLE": {"Track"}, "LYRICS": {lyrics}}})
	if err != nil {
		t.Fatal(err)
	}
	actual := string(actualBytes)
	if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'Artist/Album/track.mka','{}'::jsonb,'{}'::jsonb,?::jsonb,?::jsonb,1,now())`, positionID, epoch, generation, actual, embeddedAssets); err != nil {
		t.Fatal(err)
	}
	insertReceipt := func(state ManualLyricsDesiredState, hash any, warning any, confirmed, published uuid.UUID) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO publication_manual_lyrics_receipt(position_id,epoch,desired_revision,confirmed_generation,published_generation,desired_state,desired_sha256,warning_code,recorded_at) VALUES (?,?,7,?,?,?, ?,?,?)`, positionID, epoch, confirmed, published, string(state), hash, warning, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	read := func() ManualLyricsReceiptProjection {
		t.Helper()
		projection, err := NewSetupManagerRepository(db).ReadManualLyricsReceiptProjection(ctx, positionID)
		if err != nil {
			t.Fatal(err)
		}
		return projection
	}
	assertPending := func(want bool) {
		t.Helper()
		if got := read().Pending; got != want {
			t.Fatalf("receipt pending = %t, want %t", got, want)
		}
	}
	for _, invalid := range []struct {
		name, state   string
		hash, warning any
	}{
		{name: "unsupported desired state", state: "unknown", hash: digest},
		{name: "malformed desired hash", state: string(ManualLyricsValue), hash: "not-a-digest"},
		{name: "unsupported warning", state: string(ManualLyricsValue), hash: digest, warning: "other_warning"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, `INSERT INTO publication_manual_lyrics_receipt
				(position_id,epoch,desired_revision,confirmed_generation,published_generation,desired_state,desired_sha256,warning_code)
				VALUES (?,?,7,?,?, ?,?,?)`, positionID, epoch, uuid.New(), generation, invalid.state, invalid.hash, invalid.warning); err == nil {
				t.Fatalf("accepted invalid receipt %#v", invalid)
			}
		})
	}

	// A receipt can confirm an earlier publication generation without claiming
	// it was the generation that was actually published.
	insertReceipt(ManualLyricsValue, digest, nil, uuid.New(), generation)
	projection := read()
	if projection.Pending || projection.Receipt == nil || projection.Receipt.ConfirmedGeneration == generation || projection.Receipt.PublishedGeneration != generation {
		t.Fatalf("matching receipt projection = %#v, want fulfilled G0 confirmation for current generation", projection)
	}
	if _, err := db.ExecContext(ctx, `UPDATE release_track SET metadata_revision=8 WHERE id=?`, positionID); err != nil {
		t.Fatal(err)
	}
	assertPending(true)
	if _, err := db.ExecContext(ctx, `UPDATE release_track SET metadata_revision=7 WHERE id=?`, positionID); err != nil {
		t.Fatal(err)
	}
	newGeneration := uuid.New()
	if _, err := db.ExecContext(ctx, `UPDATE track_publication SET generation=? WHERE position_id=?`, newGeneration, positionID); err != nil {
		t.Fatal(err)
	}
	assertPending(true)
	if _, err := db.ExecContext(ctx, `DELETE FROM publication_manual_lyrics_receipt WHERE position_id=?`, positionID); err != nil {
		t.Fatal(err)
	}
	assertPending(true)

	// A warning is informational: the desired value is fulfilled when the actual
	// published LYRICS bytes match its digest exactly.
	if _, err := db.ExecContext(ctx, `UPDATE track_publication SET generation=? WHERE position_id=?`, generation, positionID); err != nil {
		t.Fatal(err)
	}
	insertReceipt(ManualLyricsValue, digest, "readback_mismatch", generation, generation)
	assertPending(false)
	if _, err := db.ExecContext(ctx, `UPDATE track_publication SET actual_metadata='{"version":1,"tags":{"TITLE":["Track"]}}'::jsonb WHERE position_id=?`, positionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE track_publication SET actual_assets=?::jsonb WHERE position_id=?`, assets, positionID); err != nil {
		t.Fatal(err)
	}
	assertPending(true)
	if _, err := db.ExecContext(ctx, `DELETE FROM publication_manual_lyrics_receipt WHERE position_id=?`, positionID); err != nil {
		t.Fatal(err)
	}

	// An explicit-empty receipt is fulfilled only by observed metadata and asset
	// absence; an embedded LYRICS asset keeps it pending.
	if _, err := db.ExecContext(ctx, `UPDATE track_publication SET actual_metadata='{"version":1,"tags":{"TITLE":["Track"]}}'::jsonb WHERE position_id=?`, positionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE track_publication SET actual_assets=?::jsonb WHERE position_id=?`, assets, positionID); err != nil {
		t.Fatal(err)
	}
	insertReceipt(ManualLyricsExplicitEmpty, nil, "readback_mismatch", generation, generation)
	assertPending(false)
	for _, invalidMetadata := range []string{
		`{}`,
		`{"version":1,"tags":null}`,
		`{"version":99,"tags":{}}`,
	} {
		if _, err := db.ExecContext(ctx, `UPDATE track_publication SET actual_metadata=?::jsonb WHERE position_id=?`, invalidMetadata, positionID); err != nil {
			t.Fatal(err)
		}
		assertPending(true)
	}
	if _, err := db.ExecContext(ctx, `UPDATE track_publication SET actual_metadata='{"version":1,"tags":{"TITLE":["Track"]}}'::jsonb WHERE position_id=?`, positionID); err != nil {
		t.Fatal(err)
	}
	assertPending(false)
	embedded := `{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"embedded","sha256":"` + digest + `","format":"lrc"}}`
	if _, err := db.ExecContext(ctx, `UPDATE track_publication SET actual_assets=?::jsonb WHERE position_id=?`, embedded, positionID); err != nil {
		t.Fatal(err)
	}
	assertPending(true)
	if _, err := db.ExecContext(ctx, `DELETE FROM release_track WHERE id=?`, positionID); err == nil {
		t.Fatal("release position deletion succeeded with a receipt; want ON DELETE RESTRICT")
	}
}
