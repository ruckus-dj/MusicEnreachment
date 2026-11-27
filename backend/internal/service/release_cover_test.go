package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/artwork"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type releaseCoverRepositoryFixture struct {
	cover             persistence.ReleaseCover
	setCalls          int
	selectedReadCalls int
	detailReadCalls   int
	selectedBefore    []byte
	readErr           error
	musicBrainzMBID   string
}

func (fixture *releaseCoverRepositoryFixture) ReadReleaseDetail(context.Context, uuid.UUID) (*persistence.LibraryReleaseDetail, error) {
	fixture.detailReadCalls++
	if fixture.readErr != nil {
		return nil, fixture.readErr
	}
	return &persistence.LibraryReleaseDetail{}, nil
}

func (fixture *releaseCoverRepositoryFixture) ReadReleaseCover(context.Context, uuid.UUID) (persistence.ReleaseCover, error) {
	return fixture.cover, nil
}

func (fixture *releaseCoverRepositoryFixture) ReadSelectedReleaseCover(_ context.Context, _ uuid.UUID, generation uuid.UUID) (persistence.SelectedReleaseCover, error) {
	fixture.selectedReadCalls++
	if fixture.readErr != nil {
		return persistence.SelectedReleaseCover{}, fixture.readErr
	}
	if fixture.cover.SelectedGeneration == nil || *fixture.cover.SelectedGeneration != generation {
		return persistence.SelectedReleaseCover{ReleaseID: fixture.cover.ReleaseID}, nil
	}
	return persistence.SelectedReleaseCover{
		ReleaseID:  fixture.cover.ReleaseID,
		Generation: fixture.cover.SelectedGeneration,
		Data:       fixture.cover.SelectedBytes,
		MIME:       fixture.cover.SelectedMIME,
		SHA256:     fixture.cover.SelectedSHA256,
		Size:       fixture.cover.SelectedSize,
	}, nil
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

func (fixture *releaseCoverRepositoryFixture) SetPendingReleaseCoverFromMusicBrainz(ctx context.Context, releaseID uuid.UUID, expected int64, releaseMBID string, generation uuid.UUID, data []byte, mime string) (persistence.ReleaseCover, error) {
	if fixture.musicBrainzMBID == "" || fixture.musicBrainzMBID != releaseMBID {
		return persistence.ReleaseCover{}, persistence.ErrReleaseCoverConflict
	}
	return fixture.SetPendingReleaseCover(ctx, releaseID, expected, generation, data, mime)
}

func releaseCoverPNG(t *testing.T) []byte {
	t.Helper()
	return releaseCoverPNGFilled(t, color.White)
}

func releaseCoverPNGFilled(t *testing.T, fill color.Color) []byte {
	t.Helper()
	return releaseCoverPNGAtSize(t, 1, 1, fill)
}

func releaseCoverPNGAtSize(t *testing.T, width, height int, fill color.Color) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			imageData.Set(x, y, fill)
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, imageData); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestReleaseCoverSaveCreatesPendingWithoutChangingSelected(t *testing.T) {
	releaseID := uuid.New()
	selectedGeneration := uuid.New()
	selected := []byte("selected")
	fixture := &releaseCoverRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 4, Mode: "automatic", SelectedGeneration: &selectedGeneration, SelectedBytes: selected}}
	service := NewReleaseCovers(fixture)
	saved := releaseCoverPNG(t)
	result, err := service.Save(context.Background(), releaseID, 4, saved, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if fixture.setCalls != 1 || result.Revision != 5 || result.Mode != "manual" || result.PendingGeneration == nil {
		t.Fatalf("unexpected saved cover: %+v", result)
	}
	if !bytes.Equal(fixture.selectedBefore, selected) {
		t.Fatal("save modified selected cover")
	}
	if result.SelectedGeneration == nil || *result.SelectedGeneration != selectedGeneration {
		t.Fatal("save changed the selected generation")
	}
	revisionBefore := fixture.cover.Revision
	selectedGenerationBefore := *fixture.cover.SelectedGeneration
	selectedBytesBefore := append([]byte(nil), fixture.cover.SelectedBytes...)
	pendingGenerationBefore := *fixture.cover.PendingGeneration
	pendingBytesBefore := append([]byte(nil), fixture.cover.PendingBytes...)
	stale := releaseCoverPNGFilled(t, color.Black)
	if bytes.Equal(stale, saved) {
		t.Fatal("stale save must use different image bytes")
	}
	if _, err := service.Save(context.Background(), releaseID, 4, stale, "image/png"); !IsLibraryReleaseCoverConflict(err) {
		t.Fatalf("stale save error = %v, want conflict", err)
	}
	if fixture.setCalls != 2 || fixture.cover.Revision != revisionBefore ||
		fixture.cover.SelectedGeneration == nil || *fixture.cover.SelectedGeneration != selectedGenerationBefore ||
		!bytes.Equal(fixture.cover.SelectedBytes, selectedBytesBefore) ||
		fixture.cover.PendingGeneration == nil || *fixture.cover.PendingGeneration != pendingGenerationBefore ||
		!bytes.Equal(fixture.cover.PendingBytes, pendingBytesBefore) {
		t.Fatalf("stale save changed cover authority: calls=%d cover=%+v", fixture.setCalls, fixture.cover)
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

func TestReleaseCoverThumbnailUsesSelectedOnlyLookupAndBoundsOutput(t *testing.T) {
	releaseID, generation := uuid.New(), uuid.New()
	source := releaseCoverPNGAtSize(t, 640, 320, color.White)
	mime := "image/png"
	digest := sha256.Sum256(source)
	size := int64(len(source))
	fixture := &releaseCoverRepositoryFixture{cover: persistence.ReleaseCover{
		ReleaseID:          releaseID,
		Revision:           2,
		Mode:               "manual",
		SelectedGeneration: &generation,
		SelectedBytes:      source,
		SelectedMIME:       &mime,
		SelectedSHA256:     digest[:],
		SelectedSize:       &size,
	}}
	result, err := NewReleaseCovers(fixture).Thumbnail(context.Background(), releaseID, generation)
	if err != nil {
		t.Fatal(err)
	}
	if result.MIME != "image/png" || bytes.Equal(result.Data, source) {
		t.Fatalf("thumbnail MIME = %q, unchanged = %t", result.MIME, bytes.Equal(result.Data, source))
	}
	format, width, height, err := artwork.Inspect(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || width > releaseCoverThumbnailDimension || height > releaseCoverThumbnailDimension {
		t.Fatalf("thumbnail = %s %dx%d, want png within %d px", format, width, height, releaseCoverThumbnailDimension)
	}
	if width != 160 || height != 80 {
		t.Fatalf("thumbnail dimensions = %dx%d, want 160x80", width, height)
	}
	if fixture.selectedReadCalls != 1 || fixture.detailReadCalls != 0 {
		t.Fatalf("lookups = selected:%d detail:%d, want selected only", fixture.selectedReadCalls, fixture.detailReadCalls)
	}
}

func TestReleaseCoverThumbnailRejectsPendingMissingAndAbsentReleases(t *testing.T) {
	releaseID, selectedID, pendingID := uuid.New(), uuid.New(), uuid.New()
	selectedMIME, pendingMIME := "image/png", "image/png"
	fixture := &releaseCoverRepositoryFixture{cover: persistence.ReleaseCover{
		ReleaseID:          releaseID,
		Revision:           2,
		Mode:               "manual",
		SelectedGeneration: &selectedID,
		SelectedBytes:      releaseCoverPNG(t),
		SelectedMIME:       &selectedMIME,
		PendingGeneration:  &pendingID,
		PendingBytes:       releaseCoverPNG(t),
		PendingMIME:        &pendingMIME,
	}}
	service := NewReleaseCovers(fixture)
	if _, err := service.Thumbnail(context.Background(), releaseID, pendingID); !IsLibraryReleaseCoverNotFound(err) {
		t.Fatalf("pending thumbnail error = %v, want not found", err)
	}
	if _, err := service.Thumbnail(context.Background(), releaseID, uuid.New()); !IsLibraryReleaseCoverNotFound(err) {
		t.Fatalf("stale thumbnail error = %v, want not found", err)
	}
	fixture.readErr = persistence.ErrLibraryReleaseNotFound
	if _, err := service.Thumbnail(context.Background(), releaseID, selectedID); !IsLibraryReleaseCoverNotFound(err) {
		t.Fatalf("missing release thumbnail error = %v, want not found", err)
	}
}

func TestReleaseCoverThumbnailWaitsForSlotBeforeRepositoryReadAndTimesOut(t *testing.T) {
	releaseID, generation := uuid.New(), uuid.New()
	source := releaseCoverPNGAtSize(t, 640, 320, color.White)
	mime := "image/png"
	digest := sha256.Sum256(source)
	size := int64(len(source))
	fixture := &releaseCoverRepositoryFixture{cover: persistence.ReleaseCover{
		ReleaseID:          releaseID,
		Revision:           2,
		Mode:               "manual",
		SelectedGeneration: &generation,
		SelectedBytes:      source,
		SelectedMIME:       &mime,
		SelectedSHA256:     digest[:],
		SelectedSize:       &size,
	}}
	covers := NewReleaseCovers(fixture)
	covers.thumbnailTimeout = 20 * time.Millisecond
	covers.thumbnailSlots <- struct{}{}
	_, err := covers.Thumbnail(context.Background(), releaseID, generation)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued thumbnail error = %v, want deadline exceeded", err)
	}
	if fixture.selectedReadCalls != 0 {
		t.Fatalf("queued thumbnail read repository %d times before acquiring a slot", fixture.selectedReadCalls)
	}
	<-covers.thumbnailSlots
	// Only the blocked request needs the short deadline.
	covers.thumbnailTimeout = releaseCoverThumbnailTimeout
	if _, err := covers.Thumbnail(context.Background(), releaseID, generation); err != nil {
		t.Fatalf("thumbnail after waiting for a slot: %v", err)
	}
	if fixture.selectedReadCalls != 1 {
		t.Fatalf("selected reads = %d, want 1", fixture.selectedReadCalls)
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
