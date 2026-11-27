package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/artwork"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

var (
	ErrReleaseCoverNotFound = errors.New("library release cover not found")
	ErrReleaseCoverConflict = errors.New("library release cover revision conflict")
	ErrReleaseCoverInvalid  = errors.New("library release cover request is invalid")
)

func IsLibraryReleaseCoverNotFound(err error) bool { return errors.Is(err, ErrReleaseCoverNotFound) }
func IsLibraryReleaseCoverConflict(err error) bool { return errors.Is(err, ErrReleaseCoverConflict) }
func IsLibraryReleaseCoverInvalid(err error) bool  { return errors.Is(err, ErrReleaseCoverInvalid) }

type releaseCoverRepository interface {
	ReadReleaseCover(context.Context, uuid.UUID) (persistence.ReleaseCover, error)
	ReadSelectedReleaseCover(context.Context, uuid.UUID, uuid.UUID) (persistence.SelectedReleaseCover, error)
	SetPendingReleaseCover(context.Context, uuid.UUID, int64, uuid.UUID, []byte, string) (persistence.ReleaseCover, error)
	ReadReleaseDetail(context.Context, uuid.UUID) (*persistence.LibraryReleaseDetail, error)
}

// releaseCoverBoundRepository fences a pending write on the release's exact
// MusicBrainz provider binding inside the same transaction as the revision CAS.
type releaseCoverBoundRepository interface {
	SetPendingReleaseCoverFromMusicBrainz(context.Context, uuid.UUID, int64, string, uuid.UUID, []byte, string) (persistence.ReleaseCover, error)
}

const (
	// releaseCoverThumbnailDimension bounds album-tile output while leaving
	// enough pixels for high-density displays.
	releaseCoverThumbnailDimension = 160
	// releaseCoverThumbnailTimeout bounds the whole thumbnail request, including
	// waiting for the single generation slot.
	releaseCoverThumbnailTimeout = 10 * time.Second
)

type ReleaseCoverSummary struct {
	ReleaseID          uuid.UUID  `json:"release_id"`
	Revision           int64      `json:"revision"`
	Mode               string     `json:"mode"`
	SelectedGeneration *uuid.UUID `json:"selected_generation,omitempty"`
	SelectedMIME       *string    `json:"selected_mime,omitempty"`
	SelectedSHA256     *string    `json:"selected_sha256,omitempty"`
	SelectedSize       *int64     `json:"selected_size,omitempty"`
	PendingGeneration  *uuid.UUID `json:"pending_generation,omitempty"`
	PendingMIME        *string    `json:"pending_mime,omitempty"`
	PendingSHA256      *string    `json:"pending_sha256,omitempty"`
	PendingSize        *int64     `json:"pending_size,omitempty"`
	Warning            *string    `json:"warning,omitempty"`
}

type ReleaseCoverImage struct {
	MIME string
	Data []byte
}

type ReleaseCovers struct {
	repository    releaseCoverRepository
	coordinator   *publication.Coordinator
	settings      releaseCoverSettings
	installations releaseCoverInstallationReader
	admission     releaseCoverAdmissionRepository
	river         persistence.RiverInserter
	platform      tools.Platform

	// thumbnailSlots bounds concurrent selected-cover thumbnail generation so
	// list tiles cannot compete with publication artwork work for libvips.
	thumbnailSlots chan struct{}
	// thumbnailTimeout bounds the whole thumbnail request, including slot wait.
	// It is a field so tests can shorten it.
	thumbnailTimeout time.Duration
}

type releaseCoverSettings interface {
	ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error)
}

type releaseCoverInstallationReader interface {
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
}

type releaseCoverAdmissionRepository interface {
	AdmitReleaseCoverApply(context.Context, persistence.ReleaseCoverApplyAdmission, persistence.RiverInserter) (persistence.ReleaseCoverBatchAdmission, error)
	AdmitReleaseCoverRetry(context.Context, persistence.ReleaseCoverRetryAdmission, persistence.RiverInserter) (persistence.ReleaseCoverBatchAdmission, error)
}

func NewReleaseCovers(repository releaseCoverRepository) *ReleaseCovers {
	return &ReleaseCovers{
		repository:       repository,
		thumbnailSlots:   make(chan struct{}, 1),
		thumbnailTimeout: releaseCoverThumbnailTimeout,
	}
}

func (service *ReleaseCovers) WithPublicationCoordinator(coordinator *publication.Coordinator) *ReleaseCovers {
	service.coordinator = coordinator
	return service
}

func (service *ReleaseCovers) WithPublicationAdmission(runtime releaseCoverSettings, installations releaseCoverInstallationReader, admission releaseCoverAdmissionRepository, river persistence.RiverInserter, platform tools.Platform) *ReleaseCovers {
	service.settings, service.installations, service.admission, service.river, service.platform = runtime, installations, admission, river, platform
	return service
}

func (service *ReleaseCovers) Read(ctx context.Context, releaseID uuid.UUID) (ReleaseCoverSummary, error) {
	cover, err := service.read(ctx, releaseID)
	if err != nil {
		return ReleaseCoverSummary{}, err
	}
	return releaseCoverSummary(cover), nil
}

func (service *ReleaseCovers) Save(ctx context.Context, releaseID uuid.UUID, expectedRevision int64, data []byte, mime string) (ReleaseCoverSummary, error) {
	return service.save(ctx, releaseID, expectedRevision, data, mime, nil)
}

// SaveFromMusicBrainz records pending artwork from a listed MusicBrainz image.
// The pending write succeeds only while the release is still linked to the same
// exact MusicBrainz release MBID; a relink conflicts with the revision CAS.
func (service *ReleaseCovers) SaveFromMusicBrainz(ctx context.Context, releaseID uuid.UUID, expectedRevision int64, releaseMBID string, data []byte, mime string) (ReleaseCoverSummary, error) {
	if releaseMBID == "" || strings.TrimSpace(releaseMBID) != releaseMBID {
		return ReleaseCoverSummary{}, fmt.Errorf("save library release cover: %w", ErrReleaseCoverInvalid)
	}
	return service.save(ctx, releaseID, expectedRevision, data, mime, &releaseMBID)
}

func (service *ReleaseCovers) save(ctx context.Context, releaseID uuid.UUID, expectedRevision int64, data []byte, mime string, expectedMBID *string) (ReleaseCoverSummary, error) {
	if service == nil || service.repository == nil {
		return ReleaseCoverSummary{}, errors.New("library release cover service is unavailable")
	}
	if releaseID == uuid.Nil || expectedRevision < 1 || len(data) == 0 {
		return ReleaseCoverSummary{}, fmt.Errorf("save library release cover: %w", ErrReleaseCoverInvalid)
	}
	format, _, _, err := artwork.Inspect(data)
	if err != nil || "image/"+format != mime {
		return ReleaseCoverSummary{}, fmt.Errorf("save library release cover: %w", ErrReleaseCoverInvalid)
	}
	if err := artwork.ValidateEncoding(data, format); err != nil {
		return ReleaseCoverSummary{}, fmt.Errorf("save library release cover: %w", ErrReleaseCoverInvalid)
	}
	if _, err := service.repository.ReadReleaseDetail(ctx, releaseID); err != nil {
		return ReleaseCoverSummary{}, mapReleaseCoverReadError("save", err)
	}
	release := func() (ReleaseCoverSummary, error) {
		generation, err := uuid.NewRandom()
		if err != nil {
			return ReleaseCoverSummary{}, fmt.Errorf("create release cover generation: %w", err)
		}
		var cover persistence.ReleaseCover
		if expectedMBID == nil {
			cover, err = service.repository.SetPendingReleaseCover(ctx, releaseID, expectedRevision, generation, data, mime)
		} else if bound, ok := service.repository.(releaseCoverBoundRepository); ok {
			cover, err = bound.SetPendingReleaseCoverFromMusicBrainz(ctx, releaseID, expectedRevision, *expectedMBID, generation, data, mime)
		} else {
			return ReleaseCoverSummary{}, errors.New("library release cover repository does not support bound saves")
		}
		if errors.Is(err, persistence.ErrReleaseCoverConflict) {
			return ReleaseCoverSummary{}, fmt.Errorf("save library release cover: %w", ErrReleaseCoverConflict)
		}
		if err != nil {
			if errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
				return ReleaseCoverSummary{}, fmt.Errorf("save library release cover: %w", ErrReleaseCoverNotFound)
			}
			return ReleaseCoverSummary{}, fmt.Errorf("save library release cover: %w", err)
		}
		return releaseCoverSummary(cover), nil
	}
	if service.coordinator == nil {
		return release()
	}
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return ReleaseCoverSummary{}, fmt.Errorf("acquire publication mutation lock: %w", err)
	}
	defer unlock()
	return release()
}

func (service *ReleaseCovers) Image(ctx context.Context, releaseID, generation uuid.UUID) (ReleaseCoverImage, error) {
	cover, err := service.read(ctx, releaseID)
	if err != nil {
		return ReleaseCoverImage{}, err
	}
	if cover.SelectedGeneration != nil && *cover.SelectedGeneration == generation {
		return releaseCoverPreview(ctx, *cover.SelectedMIME, cover.SelectedBytes)
	}
	if cover.PendingGeneration != nil && *cover.PendingGeneration == generation {
		return releaseCoverPreview(ctx, *cover.PendingMIME, cover.PendingBytes)
	}
	return ReleaseCoverImage{}, ErrReleaseCoverNotFound
}

// Thumbnail serves a bounded PNG of the current selected generation only. One
// request slot is acquired before the lookup so queued tiles do not load cover
// bytes or stage artwork while another thumbnail owns libvips. The selected
// lookup also avoids release detail and pending manual bytes.
func (service *ReleaseCovers) Thumbnail(ctx context.Context, releaseID, generation uuid.UUID) (ReleaseCoverImage, error) {
	if service == nil || service.repository == nil || service.thumbnailSlots == nil {
		return ReleaseCoverImage{}, errors.New("library release cover service is unavailable")
	}
	if releaseID == uuid.Nil || generation == uuid.Nil {
		return ReleaseCoverImage{}, fmt.Errorf("read library release cover thumbnail: %w", ErrReleaseCoverInvalid)
	}
	timeout := service.thumbnailTimeout
	if timeout <= 0 {
		timeout = releaseCoverThumbnailTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case service.thumbnailSlots <- struct{}{}:
		defer func() { <-service.thumbnailSlots }()
	case <-ctx.Done():
		return ReleaseCoverImage{}, fmt.Errorf("read library release cover thumbnail: %w", ctx.Err())
	}
	cover, err := service.repository.ReadSelectedReleaseCover(ctx, releaseID, generation)
	if err != nil {
		return ReleaseCoverImage{}, mapReleaseCoverReadError("read", err)
	}
	if cover.Generation == nil || *cover.Generation != generation {
		return ReleaseCoverImage{}, ErrReleaseCoverNotFound
	}
	thumbnail, err := releaseCoverThumbnail(ctx, cover.Data)
	if err != nil {
		return ReleaseCoverImage{}, fmt.Errorf("read library release cover thumbnail: %w", err)
	}
	return thumbnail, nil
}

func releaseCoverThumbnail(ctx context.Context, data []byte) (ReleaseCoverImage, error) {
	if len(data) == 0 {
		return ReleaseCoverImage{}, ErrReleaseCoverInvalid
	}
	tempDir, err := os.MkdirTemp("", "melotrove-cover-thumbnail-*")
	if err != nil {
		return ReleaseCoverImage{}, fmt.Errorf("create private release cover thumbnail directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	input, err := os.CreateTemp(tempDir, "cover-source-*")
	if err != nil {
		return ReleaseCoverImage{}, fmt.Errorf("create private release cover thumbnail source: %w", err)
	}
	inputPath := input.Name()
	if _, err := input.Write(data); err != nil {
		_ = input.Close()
		return ReleaseCoverImage{}, fmt.Errorf("write private release cover thumbnail source: %w", err)
	}
	if err := input.Close(); err != nil {
		return ReleaseCoverImage{}, fmt.Errorf("close private release cover thumbnail source: %w", err)
	}
	outputPath := filepath.Join(tempDir, "thumbnail.png")
	if err := artwork.ThumbnailSized(ctx, inputPath, outputPath, releaseCoverThumbnailDimension); err != nil {
		return ReleaseCoverImage{}, fmt.Errorf("create bounded release cover thumbnail: %w", err)
	}
	thumbnail, err := os.ReadFile(outputPath)
	if err != nil {
		return ReleaseCoverImage{}, fmt.Errorf("read private release cover thumbnail: %w", err)
	}
	return ReleaseCoverImage{MIME: "image/png", Data: thumbnail}, nil
}

func releaseCoverPreview(ctx context.Context, mime string, data []byte) (ReleaseCoverImage, error) {
	if mime != "image/tiff" && mime != "image/bmp" {
		return ReleaseCoverImage{MIME: mime, Data: append([]byte(nil), data...)}, nil
	}
	tempDir, err := os.MkdirTemp("", "melotrove-cover-preview-*")
	if err != nil {
		return ReleaseCoverImage{}, fmt.Errorf("create private release cover preview directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	receipt := &persistence.PublicationAttemptCoverInput{Bytes: data, MIME: &mime}
	thumbnail, err := publicationCoverThumbnail(ctx, tempDir, receipt)
	if err != nil {
		return ReleaseCoverImage{}, fmt.Errorf("create release cover preview: %w", err)
	}
	return ReleaseCoverImage{MIME: thumbnail.MIME, Data: thumbnail.Data}, nil
}

func (service *ReleaseCovers) read(ctx context.Context, releaseID uuid.UUID) (persistence.ReleaseCover, error) {
	if service == nil || service.repository == nil {
		return persistence.ReleaseCover{}, errors.New("library release cover service is unavailable")
	}
	if releaseID == uuid.Nil {
		return persistence.ReleaseCover{}, fmt.Errorf("read library release cover: %w", ErrReleaseCoverInvalid)
	}
	if _, err := service.repository.ReadReleaseDetail(ctx, releaseID); err != nil {
		return persistence.ReleaseCover{}, mapReleaseCoverReadError("read", err)
	}
	cover, err := service.repository.ReadReleaseCover(ctx, releaseID)
	if err != nil {
		return persistence.ReleaseCover{}, fmt.Errorf("read library release cover: %w", err)
	}
	return cover, nil
}

func mapReleaseCoverReadError(action string, err error) error {
	if errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
		return fmt.Errorf("%s library release cover: %w", action, ErrReleaseCoverNotFound)
	}
	return fmt.Errorf("%s library release cover: %w", action, err)
}

func releaseCoverSummary(cover persistence.ReleaseCover) ReleaseCoverSummary {
	return ReleaseCoverSummary{
		ReleaseID: cover.ReleaseID, Revision: cover.Revision, Mode: cover.Mode,
		SelectedGeneration: cover.SelectedGeneration, SelectedMIME: cover.SelectedMIME, SelectedSHA256: coverDigest(cover.SelectedSHA256), SelectedSize: cover.SelectedSize,
		PendingGeneration: cover.PendingGeneration, PendingMIME: cover.PendingMIME, PendingSHA256: coverDigest(cover.PendingSHA256), PendingSize: cover.PendingSize,
		Warning: cover.Warning,
	}
}

func coverDigest(digest []byte) *string {
	if len(digest) == 0 {
		return nil
	}
	value := fmt.Sprintf("%x", digest)
	return &value
}
