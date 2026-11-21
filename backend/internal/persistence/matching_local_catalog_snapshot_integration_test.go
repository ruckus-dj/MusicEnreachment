//go:build integration

package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestValidateMatchingLocalLayoutEvidenceTxFence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	repository := NewMatchingLocalCatalogRepository(db)
	validate := func(headerDigest string, releaseIDs []uuid.UUID, layoutDigest string) error {
		t.Helper()
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return ValidateMatchingLocalLayoutEvidenceTx(ctx, tx, headerDigest, releaseIDs, layoutDigest)
		})
	}

	emptyCatalog, err := repository.ListMatchingLocalSelectionHeaders(ctx)
	if err != nil {
		t.Fatalf("read empty local catalog: %v", err)
	}
	if len(emptyCatalog.Headers) != 0 {
		t.Fatalf("expected empty local catalog, got %d headers", len(emptyCatalog.Headers))
	}
	if err := validate(emptyCatalog.Digest, nil, ""); err != nil {
		t.Fatalf("validate empty local catalog proof: %v", err)
	}

	artistID, recordingID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_artist(id,provider_id,provider_key,name) VALUES(?,?,?,?)`, artistID, localProviderID, uuid.NewString(), "Local Artist"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_recording(id,provider_id,provider_key,title) VALUES(?,?,?,?)`, recordingID, localProviderID, uuid.NewString(), "Recording"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES(?,?,?,?)`, releaseID, localProviderID, uuid.NewString(), "Release"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_release_artist_credit(provider_id,release_id,position,artist_id,name_join_phrase,present) VALUES(?,?,0,?,'',true)`, localProviderID, releaseID, artistID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_release_track(id,provider_id,release_id,recording_id,medium,position,displayed_number,present) VALUES(?,?,?,?,0,0,'1',true)`, trackID, localProviderID, releaseID, recordingID); err != nil {
		t.Fatal(err)
	}

	catalog, err := repository.ListMatchingLocalSelectionHeaders(ctx)
	if err != nil {
		t.Fatalf("read local catalog headers: %v", err)
	}
	snapshot, err := repository.HydrateMatchingLocalSelection(ctx, []uuid.UUID{releaseID})
	if err != nil {
		t.Fatalf("hydrate local selection: %v", err)
	}
	if len(snapshot.Releases) != 1 {
		t.Fatalf("hydrated releases = %d, want 1", len(snapshot.Releases))
	}
	if err := validate(catalog.Digest, []uuid.UUID{releaseID}, snapshot.Digest); err != nil {
		t.Fatalf("validate current local evidence: %v", err)
	}

	// A recording mutation changes only the shortlisted layout digest; the
	// whole-catalog header digest remains valid.
	if _, err := db.ExecContext(ctx, `UPDATE provider_recording SET title=? WHERE id=?`, "Changed Recording", recordingID); err != nil {
		t.Fatal(err)
	}
	if err := validate(catalog.Digest, []uuid.UUID{releaseID}, snapshot.Digest); !errors.Is(err, ErrIncomingMatchConflict) {
		t.Fatalf("layout mutation error = %v, want %v", err, ErrIncomingMatchConflict)
	}
	if _, err := db.ExecContext(ctx, `UPDATE provider_recording SET title=? WHERE id=?`, "Recording", recordingID); err != nil {
		t.Fatal(err)
	}
	if err := validate(catalog.Digest, []uuid.UUID{releaseID}, snapshot.Digest); err != nil {
		t.Fatalf("validate restored local evidence: %v", err)
	}

	// A competing Local header (phantom release) must invalidate both the
	// original shortlist decision and the proved-empty header evidence.
	phantomID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES(?,?,?,?)`, phantomID, localProviderID, uuid.NewString(), "Phantom"); err != nil {
		t.Fatal(err)
	}
	if err := validate(catalog.Digest, []uuid.UUID{releaseID}, snapshot.Digest); !errors.Is(err, ErrIncomingMatchConflict) {
		t.Fatalf("phantom header error = %v, want %v", err, ErrIncomingMatchConflict)
	}
	if err := validate(emptyCatalog.Digest, nil, ""); !errors.Is(err, ErrIncomingMatchConflict) {
		t.Fatalf("empty proof after phantom error = %v, want %v", err, ErrIncomingMatchConflict)
	}
}
