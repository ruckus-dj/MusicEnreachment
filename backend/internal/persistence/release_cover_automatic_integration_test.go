//go:build integration

package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestAutomaticCoverSelectionRejectsLateSuccessAndFailureAfterManualSavePostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	releaseID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'automatic cover')`, releaseID); err != nil {
		t.Fatal(err)
	}
	repository := NewLibraryRepository(db)
	image := releaseCoverPNG(t)
	selected, err := repository.RecordAutomaticReleaseCoverSelection(ctx, releaseID, 1, image, "image/png", nil)
	if err != nil || selected.SelectedGeneration == nil {
		t.Fatalf("initial automatic cover: %+v %v", selected, err)
	}
	unchanged, err := repository.RecordAutomaticReleaseCoverSelection(ctx, releaseID, selected.Revision, image, "image/png", nil)
	if err != nil || !sameOptionalUUID(unchanged.SelectedGeneration, selected.SelectedGeneration) || unchanged.Revision != selected.Revision {
		t.Fatalf("same image churned current selection: %+v %v", unchanged, err)
	}
	warning := "provider unavailable"
	warned, err := repository.RecordAutomaticReleaseCoverSelection(ctx, releaseID, selected.Revision, nil, "", &warning)
	if err != nil || warned.Warning == nil || !sameOptionalUUID(warned.SelectedGeneration, selected.SelectedGeneration) {
		t.Fatalf("failed acquisition discarded selected cover: %+v %v", warned, err)
	}
	cleared, err := repository.RecordAutomaticReleaseCoverSelection(ctx, releaseID, warned.Revision, image, "image/png", nil)
	if err != nil || cleared.Warning != nil || !sameOptionalUUID(cleared.SelectedGeneration, selected.SelectedGeneration) {
		t.Fatalf("successful acquisition retained obsolete warning: %+v %v", cleared, err)
	}
	pendingGeneration := uuid.New()
	pending, err := repository.SetPendingReleaseCover(ctx, releaseID, cleared.Revision, pendingGeneration, image, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []bool{false, true} {
		data, mime := image, "image/png"
		var diagnostic *string
		if failure {
			data, mime, diagnostic = nil, "", &warning
		}
		if _, err := repository.RecordAutomaticReleaseCoverSelection(ctx, releaseID, cleared.Revision, data, mime, diagnostic); !errors.Is(err, ErrReleaseCoverConflict) {
			t.Fatalf("late automatic result after manual save: %v", err)
		}
	}
	current, err := repository.ReadReleaseCover(ctx, releaseID)
	if err != nil || current.Revision != pending.Revision || current.Mode != "manual" || current.PendingGeneration == nil || *current.PendingGeneration != pendingGeneration || !sameOptionalUUID(current.SelectedGeneration, selected.SelectedGeneration) {
		t.Fatalf("late result changed manual authority: %+v %v", current, err)
	}
}
