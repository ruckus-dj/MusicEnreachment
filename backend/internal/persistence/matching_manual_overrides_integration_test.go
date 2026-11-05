//go:build integration

package persistence_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
	"testing"
)

func assertManualOverrideValues(t *testing.T, ctx context.Context, db *bun.DB, trackID uuid.UUID, expectedTitle string, expectedArtists []string) {
	t.Helper()
	var title string
	if err := db.NewRaw(`SELECT title FROM local_track WHERE id=?`, trackID).Scan(ctx, &title); err != nil {
		t.Fatal(err)
	}
	if title != expectedTitle {
		t.Fatalf("local title = %q, want %q", title, expectedTitle)
	}
	var artists []string
	if err := db.NewRaw(`SELECT name FROM track_artist WHERE track_id=? ORDER BY position`, trackID).Scan(ctx, &artists); err != nil {
		t.Fatal(err)
	}
	if len(artists) != len(expectedArtists) {
		t.Fatalf("local artists = %#v, want %#v", artists, expectedArtists)
	}
	for i := range artists {
		if artists[i] != expectedArtists[i] {
			t.Fatalf("local artists = %#v, want %#v", artists, expectedArtists)
		}
	}
}

func ptrString(value string) *string { return &value }

func manualReceiptAssignment(preview persistence.MatchingManualPreview, memberID uuid.UUID) *persistence.MatchingManualAssignment {
	for _, member := range preview.Members {
		if member.FileMemberID == memberID {
			return member.Assignment
		}
	}
	return nil
}
