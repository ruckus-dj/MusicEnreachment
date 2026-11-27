//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestCoverArtArchiveReleaseStatusesWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	repository := persistence.NewLibraryRepository(db)
	ctx := context.Background()
	present := uuid.NewString()
	absent := uuid.NewString()
	unchecked := uuid.NewString()

	if err := repository.SaveCoverArtArchiveFront(ctx, present, "", []byte("front-bytes"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkCoverArtArchiveFrontAbsent(ctx, absent); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveCoverArtArchiveGalleryCandidates(ctx, present, []persistence.CoverArtArchiveImage{{ID: "1", Front: true}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpsertCoverArtArchiveImage(ctx, present, "1", []byte("image-bytes"), "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteCoverArtArchiveGallery(ctx, present); err != nil {
		t.Fatal(err)
	}

	statuses, err := repository.ReadCoverArtArchiveReleaseStatuses(ctx, []string{present, absent, unchecked, present})
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 3 {
		t.Fatalf("statuses = %#v, want three deduplicated editions", statuses)
	}
	if got := statuses[present]; got.FrontStatus != persistence.CoverArtArchiveFrontPresent ||
		got.GalleryStatus != persistence.CoverArtArchiveGalleryComplete || got.ReleaseMBID != present {
		t.Fatalf("present status = %#v", got)
	}
	if got := statuses[absent]; got.FrontStatus != persistence.CoverArtArchiveFrontAbsent ||
		got.GalleryStatus != persistence.CoverArtArchiveUnchecked || got.ReleaseMBID != absent {
		t.Fatalf("absent status = %#v", got)
	}
	if got := statuses[unchecked]; got.FrontStatus != persistence.CoverArtArchiveUnchecked ||
		got.GalleryStatus != persistence.CoverArtArchiveUnchecked || got.ReleaseMBID != unchecked {
		t.Fatalf("unchecked status = %#v", got)
	}
	if _, err := repository.ReadCoverArtArchiveReleaseStatuses(ctx, []string{"not-an-mbid"}); err == nil {
		t.Fatal("invalid MBID was accepted")
	}
}
