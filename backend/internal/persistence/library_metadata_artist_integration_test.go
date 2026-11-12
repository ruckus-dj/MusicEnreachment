//go:build integration

package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestLibraryArtistMetadataRepositoryUsesRevisionAndCatalog(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	artistID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name) VALUES(?,?)`, artistID, "Genre Test Artist").Exec(ctx); err != nil {
		t.Fatalf("insert local artist: %v", err)
	}
	repository := NewLibraryRepository(db)
	if _, err := db.NewRaw(`UPDATE genre_catalog SET items=?::jsonb,updated_at=now() WHERE singleton=true`, `[{"id":"55555555-5555-4555-8555-555555555555","source_name":"art rock","name":"Art Rock"}]`).Exec(ctx); err != nil {
		t.Fatalf("seed genre catalog: %v", err)
	}
	catalog, err := repository.GenreCatalogItems(ctx)
	if err != nil {
		t.Fatalf("read genre catalog for metadata: %v", err)
	}
	if len(catalog) != 1 || catalog[0].ID != "55555555-5555-4555-8555-555555555555" || catalog[0].Name != "Art Rock" {
		t.Fatalf("genre catalog = %#v", catalog)
	}
	read, err := repository.ReadMetadata(ctx, librarymetadata.ScopeArtist, uuid.Nil, artistID)
	if err != nil {
		t.Fatalf("read initial artist metadata: %v", err)
	}
	if read.Revision != 1 || read.Envelope.Scope != librarymetadata.ScopeArtist {
		t.Fatalf("initial artist metadata = %#v", read)
	}
	genreID := "55555555-5555-4555-8555-555555555555"
	envelope := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion,
		Scope:   librarymetadata.ScopeArtist,
		Desired: map[string]librarymetadata.FieldValue{
			"genre": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []librarymetadata.GenreReference{{ID: genreID, SourceName: "art rock", Name: "Art Rock"}}, true),
		},
	}
	updated, err := repository.UpdateMetadata(ctx, librarymetadata.ScopeArtist, uuid.Nil, artistID, read.Revision, envelope)
	if err != nil {
		t.Fatalf("update artist metadata: %v", err)
	}
	if updated.Revision != 2 {
		t.Fatalf("updated artist revision = %d, want 2", updated.Revision)
	}
	if _, err := repository.UpdateMetadata(ctx, librarymetadata.ScopeArtist, uuid.Nil, artistID, read.Revision, envelope); !errors.Is(err, ErrLibraryMetadataConflict) {
		t.Fatalf("stale artist update error = %v, want conflict", err)
	}
	read, err = repository.ReadMetadata(ctx, librarymetadata.ScopeArtist, uuid.Nil, artistID)
	if err != nil {
		t.Fatalf("read updated artist metadata: %v", err)
	}
	if read.Revision != 2 {
		t.Fatalf("read artist revision = %d, want 2", read.Revision)
	}
	genres, ok := read.Envelope.Desired["genre"].Value.([]any)
	if !ok || len(genres) != 1 {
		t.Fatalf("read artist genres = %#v", read.Envelope.Desired["genre"].Value)
	}
}

func TestLibraryMetadataArtistCreditPatchValidatesAndHydratesIdentity(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	artistID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name) VALUES(?,?)`, artistID, "Canonical Artist").Exec(ctx); err != nil {
		t.Fatalf("insert local artist: %v", err)
	}
	recordingID := uuid.New()
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO local_release(id,title) VALUES(?,?)`, releaseID, "Credit Test Release").Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO release_artist(release_id,position,artist_id,name,name_join_phrase) VALUES (?,0,?,'Canonical Artist','')`, releaseID, artistID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO local_recording(id,title) VALUES(?,?)`, recordingID, "Credit Test Recording").Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO local_track(id,title) VALUES(?,?)`, trackID, "Credit Test Track").Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`INSERT INTO release_track(id,release_id,track_id,recording_id,position,medium,displayed_number) VALUES (?,?,?,?,0,0,'1')`, positionID, releaseID, trackID, recordingID).Exec(ctx)
		return err
	}); err != nil {
		t.Fatalf("insert local recording fixture: %v", err)
	}
	repository := NewLibraryRepository(db)
	current, err := repository.ReadMetadata(ctx, librarymetadata.ScopeRecording, uuid.Nil, recordingID)
	if err != nil {
		t.Fatalf("read recording metadata: %v", err)
	}
	clientMBID := uuid.NewString()
	envelope := librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion,
		Scope:   librarymetadata.ScopeRecording,
		Desired: map[string]librarymetadata.FieldValue{
			"artist_credit": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []librarymetadata.ArtistCredit{{
				ArtistID: artistID.String(), ArtistName: "Untrusted Client Name", CreditName: "", JoinPhrase: "", MBID: &clientMBID,
			}}, true),
		},
	}
	updated, err := repository.UpdateMetadata(ctx, librarymetadata.ScopeRecording, uuid.Nil, recordingID, current.Revision, envelope)
	if err != nil {
		t.Fatalf("update recording artist credit: %v", err)
	}
	display, err := repository.ReadMetadataDisplay(ctx, librarymetadata.ScopeRecording, uuid.Nil, recordingID)
	if err != nil {
		t.Fatalf("read hydrated recording metadata: %v", err)
	}
	if display.Metadata.Envelope.Desired["artist_credit"].Value == nil || display.EffectiveArtistCredit == nil || len(display.EffectiveArtistCredit.Credits) != 1 {
		t.Fatalf("artist credit display = %#v", display)
	}
	credit := display.EffectiveArtistCredit.Credits[0]
	if credit.ArtistID != artistID.String() || credit.ArtistName != "Canonical Artist" || credit.CreditName != "" || credit.JoinPhrase != "" || credit.MBID != nil || credit.MetadataRevision == 0 {
		t.Fatalf("hydrated credit = %#v", credit)
	}
	invalid := envelope
	invalid.Desired = map[string]librarymetadata.FieldValue{
		"artist_credit": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []librarymetadata.ArtistCredit{{
			ArtistID: uuid.NewString(), ArtistName: "Missing", CreditName: "Missing", JoinPhrase: "",
		}}, true),
	}
	if _, err := repository.UpdateMetadata(ctx, librarymetadata.ScopeRecording, uuid.Nil, recordingID, updated.Revision, invalid); !errors.Is(err, ErrLibraryMetadataArtistNotFound) {
		t.Fatalf("update with unknown local artist error = %v", err)
	}
	unchanged, err := repository.ReadMetadata(ctx, librarymetadata.ScopeRecording, uuid.Nil, recordingID)
	if err != nil {
		t.Fatalf("read metadata after rejected artist reference: %v", err)
	}
	if unchanged.Revision != updated.Revision {
		t.Fatalf("revision after rejected artist reference = %d, want %d", unchanged.Revision, updated.Revision)
	}
}

func TestLibraryMetadataDisplayKeepsEnvelopeWhenTrackCreditsAreFallbackOrUnclassified(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	releaseID, recordingID, artistID := uuid.New(), uuid.New(), uuid.New()
	fallbackTrackID, unclassifiedTrackID := uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name) VALUES(?,?)`, artistID, "Recording Artist").Exec(ctx); err != nil {
		t.Fatalf("insert artist: %v", err)
	}
	fallbackPositionID, unclassifiedPositionID := uuid.New(), uuid.New()
	positions := []struct {
		positionID uuid.UUID
		trackID    uuid.UUID
		position   int
	}{{fallbackPositionID, fallbackTrackID, 0}, {unclassifiedPositionID, unclassifiedTrackID, 1}}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO local_release(id,title) VALUES(?,?)`, releaseID, "Display Test Release").Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO release_artist(release_id,position,artist_id,name,name_join_phrase) VALUES (?,0,?,'Recording Artist','')`, releaseID, artistID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO local_recording(id,title) VALUES(?,?)`, recordingID, "Display Test Recording").Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO recording_artist(recording_id,position,artist_id,name) VALUES(?,?,?,?)`, recordingID, 0, artistID, "Recording Artist").Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO local_track(id,title,metadata) VALUES(?,?,?::jsonb),(?,?,?::jsonb)`,
			fallbackTrackID, "Fallback Track", `{"artist_credit_origin":{"version":1,"kind":"recording_fallback"}}`,
			unclassifiedTrackID, "Unclassified Track", `{}`).Exec(ctx); err != nil {
			return err
		}
		for _, row := range positions {
			if _, err := tx.NewRaw(`INSERT INTO release_track(id,release_id,position,track_id,recording_id) VALUES(?,?,?,?,?)`, row.positionID, releaseID, row.position, row.trackID, recordingID).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("insert release display fixture: %v", err)
	}
	repository := NewLibraryRepository(db)
	recordingMetadata, err := repository.ReadMetadata(ctx, librarymetadata.ScopeRecording, uuid.Nil, recordingID)
	if err != nil {
		t.Fatalf("read recording metadata before key setup: %v", err)
	}
	recordingMetadata.Envelope.Desired["key"] = librarymetadata.ManualFieldValue(librarymetadata.StateValue, "D♭ minor", true)
	if _, err := repository.UpdateMetadata(ctx, librarymetadata.ScopeRecording, uuid.Nil, recordingID, recordingMetadata.Revision, recordingMetadata.Envelope); err != nil {
		t.Fatalf("save recording key: %v", err)
	}
	metadata, err := repository.ReadMetadata(ctx, librarymetadata.ScopePosition, releaseID, fallbackPositionID)
	if err != nil {
		t.Fatalf("read fallback position metadata: %v", err)
	}
	metadata.Envelope.Desired["comment"] = librarymetadata.ManualFieldValue(librarymetadata.StateValue, "preserve unrelated metadata", true)
	if _, err := repository.UpdateMetadata(ctx, librarymetadata.ScopePosition, releaseID, fallbackPositionID, metadata.Revision, metadata.Envelope); err != nil {
		t.Fatalf("save unrelated position metadata: %v", err)
	}
	for _, test := range []struct {
		name       string
		positionID uuid.UUID
		status     librarymetadata.CreditResolutionStatus
		origin     librarymetadata.CreditOrigin
		count      int
	}{
		{name: "valid recording fallback", positionID: fallbackPositionID, status: librarymetadata.CreditResolutionResolved, origin: librarymetadata.CreditOriginRecording, count: 1},
		{name: "unclassified legacy credits", positionID: unclassifiedPositionID, status: librarymetadata.CreditResolutionUnclassified, count: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			display, err := repository.ReadMetadataDisplay(ctx, librarymetadata.ScopePosition, releaseID, test.positionID)
			if err != nil {
				t.Fatalf("ReadMetadataDisplay() error = %v", err)
			}
			if display.Metadata.Envelope.Scope != librarymetadata.ScopePosition || display.EffectiveArtistCredit == nil {
				t.Fatalf("metadata envelope/effective credits = %#v / %#v", display.Metadata.Envelope, display.EffectiveArtistCredit)
			}
			if test.positionID == fallbackPositionID && display.Metadata.Envelope.Desired["comment"].Value != "preserve unrelated metadata" {
				t.Fatalf("unrelated desired metadata was not retained: %#v", display.Metadata.Envelope.Desired)
			}
			if display.EffectiveKey == nil || display.EffectiveKey.Scope != librarymetadata.ScopeRecording || display.EffectiveKey.Field.State != librarymetadata.StateValue || display.EffectiveKey.Field.Value != "D♭ minor" {
				t.Fatalf("effective key = %#v, want joined recording key", display.EffectiveKey)
			}
			if _, exists := display.Metadata.Envelope.Desired["key"]; exists {
				t.Fatalf("recording key was copied into position desired metadata: %#v", display.Metadata.Envelope.Desired["key"])
			}
			if got := display.EffectiveArtistCredit; got.Status != test.status || got.Origin != test.origin || len(got.Credits) != test.count {
				t.Fatalf("effective artist credits = %#v", got)
			}
		})
	}
}
