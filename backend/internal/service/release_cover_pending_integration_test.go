//go:build integration

package service

import (
	"bytes"
	"context"
	"image/color"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestReleaseCoverSavePersistsPendingWithoutPublicationAuthorityPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	releaseID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,?)`, releaseID, "Cover fixture"); err != nil {
		t.Fatalf("insert local release: %v", err)
	}
	library := persistence.NewLibraryRepository(db)
	service := NewReleaseCovers(library)

	baseline, err := library.ReadReleaseCover(ctx, releaseID)
	if err != nil || baseline.Revision != 1 || baseline.Mode != "automatic" || baseline.SelectedGeneration != nil || baseline.PendingGeneration != nil {
		t.Fatalf("initial cover authority = %+v, %v", baseline, err)
	}
	var coverRows int
	if err := db.NewRaw(`SELECT count(*) FROM release_publication_cover WHERE release_id=?`, releaseID).Scan(ctx, &coverRows); err != nil || coverRows != 0 {
		t.Fatalf("initial read created authority row: count=%d err=%v", coverRows, err)
	}
	var operationsBefore, attemptsBefore int
	if err := db.NewRaw(`SELECT count(*) FROM operation`).Scan(ctx, &operationsBefore); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_publication_attempt`).Scan(ctx, &attemptsBefore); err != nil {
		t.Fatalf("count publication attempts: %v", err)
	}

	imageA := simpleCoverPNG(t, color.RGBA{R: 255, A: 255})
	imageB := simpleCoverPNG(t, color.RGBA{B: 255, A: 255})
	savedA, err := service.Save(ctx, releaseID, baseline.Revision, imageA, "image/png")
	if err != nil || savedA.Revision != 2 || savedA.Mode != "manual" || savedA.SelectedGeneration != nil || savedA.PendingGeneration == nil {
		t.Fatalf("save pending A = %+v, %v", savedA, err)
	}
	selectedA, err := library.ApplyPendingReleaseCover(ctx, releaseID, savedA.Revision, *savedA.PendingGeneration)
	if err != nil || selectedA.SelectedGeneration == nil || *selectedA.SelectedGeneration != *savedA.PendingGeneration {
		t.Fatalf("select fixture cover A: %+v, %v", selectedA, err)
	}
	selectedGeneration := *selectedA.SelectedGeneration
	selectedBytes := append([]byte(nil), selectedA.SelectedBytes...)

	savedB, err := service.Save(ctx, releaseID, selectedA.Revision, imageB, "image/png")
	if err != nil || savedB.Revision != selectedA.Revision+1 || savedB.SelectedGeneration == nil || *savedB.SelectedGeneration != selectedGeneration || savedB.PendingGeneration == nil || *savedB.PendingGeneration == selectedGeneration {
		t.Fatalf("save pending B changed selected authority: %+v, %v", savedB, err)
	}
	current, err := library.ReadReleaseCover(ctx, releaseID)
	if err != nil || current.SelectedGeneration == nil || *current.SelectedGeneration != selectedGeneration || !bytes.Equal(current.SelectedBytes, selectedBytes) || current.PendingGeneration == nil || *current.PendingGeneration != *savedB.PendingGeneration || !bytes.Equal(current.PendingBytes, imageB) {
		t.Fatalf("persisted selected/pending bytes = %+v, %v", current, err)
	}
	imageOut, err := service.Image(ctx, releaseID, selectedGeneration)
	if err != nil || !bytes.Equal(imageOut.Data, selectedBytes) || imageOut.MIME != "image/png" {
		t.Fatalf("selected image endpoint data mismatch: mime=%q err=%v", imageOut.MIME, err)
	}

	if _, err := service.Save(ctx, releaseID, selectedA.Revision, imageA, "image/png"); !IsLibraryReleaseCoverConflict(err) {
		t.Fatalf("stale save error = %v, want conflict", err)
	}
	afterStale, err := library.ReadReleaseCover(ctx, releaseID)
	if err != nil || afterStale.Revision != current.Revision || afterStale.PendingGeneration == nil || *afterStale.PendingGeneration != *current.PendingGeneration || !bytes.Equal(afterStale.PendingBytes, current.PendingBytes) || afterStale.SelectedGeneration == nil || *afterStale.SelectedGeneration != selectedGeneration {
		t.Fatalf("stale save changed cover authority: %+v, %v", afterStale, err)
	}
	var operationsAfter, attemptsAfter int
	if err := db.NewRaw(`SELECT count(*) FROM operation`).Scan(ctx, &operationsAfter); err != nil {
		t.Fatalf("count operations after saves: %v", err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_publication_attempt`).Scan(ctx, &attemptsAfter); err != nil {
		t.Fatalf("count publication attempts after saves: %v", err)
	}
	if operationsAfter != operationsBefore || attemptsAfter != attemptsBefore {
		t.Fatalf("cover saves created publication work: operations %d->%d attempts %d->%d", operationsBefore, operationsAfter, attemptsBefore, attemptsAfter)
	}
}
