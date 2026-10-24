package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type releaseCoverRepositoryFixture struct {
	cover          persistence.ReleaseCover
	setCalls       int
	selectedBefore []byte
	readErr        error
}

func (fixture *releaseCoverRepositoryFixture) ReadReleaseDetail(context.Context, uuid.UUID) (*persistence.LibraryReleaseDetail, error) {
	if fixture.readErr != nil {
		return nil, fixture.readErr
	}
	return &persistence.LibraryReleaseDetail{}, nil
}

func (fixture *releaseCoverRepositoryFixture) ReadReleaseCover(context.Context, uuid.UUID) (persistence.ReleaseCover, error) {
	return fixture.cover, nil
}

func (fixture *releaseCoverRepositoryFixture) SetPendingReleaseCover(_ context.Context, releaseID uuid.UUID, expected int64, generation uuid.UUID, data []byte, mime string) (persistence.ReleaseCover, error) {
	fixture.setCalls++
	if expected != fixture.cover.Revision {
		return persistence.ReleaseCover{}, persistence.ErrReleaseCoverConflict
	}
	fixture.selectedBefore = append([]byte(nil), fixture.cover.SelectedBytes...)
	fixture.cover.Revision++
	fixture.cover.ReleaseID = releaseID
	fixture.cover.Mode = "manual"
	fixture.cover.PendingGeneration = &generation
	fixture.cover.PendingBytes = append([]byte(nil), data...)
	fixture.cover.PendingMIME = &mime
	size := int64(len(data))
	fixture.cover.PendingSize = &size
	return fixture.cover, nil
}

func releaseCoverPNG(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageData.Set(0, 0, color.White)
	var data bytes.Buffer
	if err := png.Encode(&data, imageData); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestReleaseCoverSaveCreatesPendingWithoutChangingSelected(t *testing.T) {
	releaseID := uuid.New()
	selected := []byte("selected")
	fixture := &releaseCoverRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 4, Mode: "automatic", SelectedBytes: selected}}
	service := NewReleaseCovers(fixture)
	result, err := service.Save(context.Background(), releaseID, 4, releaseCoverPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if fixture.setCalls != 1 || result.Revision != 5 || result.Mode != "manual" || result.PendingGeneration == nil {
		t.Fatalf("unexpected saved cover: %+v", result)
	}
	if !bytes.Equal(fixture.selectedBefore, selected) {
		t.Fatal("save modified selected cover")
	}
	if result.SelectedGeneration != nil {
		t.Fatal("save assigned a selected generation")
	}
}

func TestReleaseCoverSaveRejectsInvalidImageWithoutWrite(t *testing.T) {
	releaseID := uuid.New()
	fixture := &releaseCoverRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 1, Mode: "automatic"}}
	_, err := NewReleaseCovers(fixture).Save(context.Background(), releaseID, 1, []byte("not an image"), "image/png")
	if !IsLibraryReleaseCoverInvalid(err) {
		t.Fatalf("expected invalid image, got %v", err)
	}
	if fixture.setCalls != 0 {
		t.Fatal("invalid image reached persistence")
	}
	encoded := releaseCoverPNG(t)
	_, err = NewReleaseCovers(fixture).Save(context.Background(), releaseID, 1, encoded[:len(encoded)/2], "image/png")
	if !IsLibraryReleaseCoverInvalid(err) || fixture.setCalls != 0 {
		t.Fatalf("truncated PNG was not rejected as invalid before persistence: calls=%d err=%v", fixture.setCalls, err)
	}
}

func TestReleaseCoverImageOnlyServesCurrentGenerations(t *testing.T) {
	releaseID, selectedID, pendingID := uuid.New(), uuid.New(), uuid.New()
	selectedData, pendingData := []byte("selected"), []byte("pending")
	selectedMIME, pendingMIME := "image/jpeg", "image/png"
	fixture := &releaseCoverRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 2, Mode: "manual", SelectedGeneration: &selectedID, SelectedBytes: selectedData, SelectedMIME: &selectedMIME, PendingGeneration: &pendingID, PendingBytes: pendingData, PendingMIME: &pendingMIME}}
	service := NewReleaseCovers(fixture)
	imageResult, err := service.Image(context.Background(), releaseID, pendingID)
	if err != nil || imageResult.MIME != pendingMIME || !bytes.Equal(imageResult.Data, pendingData) {
		t.Fatalf("pending image = %+v, %v", imageResult, err)
	}
	if _, err := service.Image(context.Background(), releaseID, uuid.New()); !IsLibraryReleaseCoverNotFound(err) {
		t.Fatalf("expected unavailable generation, got %v", err)
	}
}

func TestReleaseCoverSaveAcceptsJPEGAndMapsMissingRelease(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatal(err)
	}
	releaseID := uuid.New()
	fixture := &releaseCoverRepositoryFixture{cover: persistence.ReleaseCover{Revision: 1}}
	result, err := NewReleaseCovers(fixture).Save(context.Background(), releaseID, 1, encoded.Bytes(), "image/jpeg")
	if err != nil || result.PendingMIME == nil || *result.PendingMIME != "image/jpeg" {
		t.Fatalf("JPEG save = %+v, %v", result, err)
	}
	fixture.readErr = persistence.ErrLibraryReleaseNotFound
	if _, err := NewReleaseCovers(fixture).Read(context.Background(), releaseID); !IsLibraryReleaseCoverNotFound(err) {
		t.Fatalf("missing release read error = %v", err)
	}
	if _, err := NewReleaseCovers(fixture).Save(context.Background(), releaseID, 2, encoded.Bytes(), "image/jpeg"); !IsLibraryReleaseCoverNotFound(err) {
		t.Fatalf("missing release save error = %v", err)
	}
}

func TestReleaseCoverSaveCanceledWhileWaitingDoesNotWrite(t *testing.T) {
	coordinator := publication.NewCoordinator()
	release, err := coordinator.Acquire(context.Background(), []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &releaseCoverRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: uuid.New(), Revision: 1}}
	covers := NewReleaseCovers(fixture).WithPublicationCoordinator(coordinator)
	ctx := newCancelOnDoneContext()
	done := make(chan error, 1)
	go func() { _, err := covers.Save(ctx, uuid.New(), 1, releaseCoverPNG(t), "image/png"); done <- err }()
	select {
	case <-ctx.waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("save did not wait for coordinator")
	}
	ctx.cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save error = %v", err)
	}
	if fixture.setCalls != 0 {
		t.Fatal("canceled save wrote pending cover")
	}
	release()
	if _, err := covers.Save(context.Background(), fixture.cover.ReleaseID, 1, releaseCoverPNG(t), "image/png"); err != nil {
		t.Fatalf("save after release: %v", err)
	}
	if fixture.setCalls != 1 {
		t.Fatalf("set calls = %d, want 1", fixture.setCalls)
	}
}
