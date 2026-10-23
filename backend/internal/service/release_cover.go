package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"

	"github.com/google/uuid"
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
	SetPendingReleaseCover(context.Context, uuid.UUID, int64, uuid.UUID, []byte, string) (persistence.ReleaseCover, error)
	ReadReleaseDetail(context.Context, uuid.UUID) (*persistence.LibraryReleaseDetail, error)
}

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
	return &ReleaseCovers{repository: repository}
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
	if service == nil || service.repository == nil {
		return ReleaseCoverSummary{}, errors.New("library release cover service is unavailable")
	}
	if releaseID == uuid.Nil || expectedRevision < 1 || len(data) == 0 || (mime != "image/png" && mime != "image/jpeg") {
		return ReleaseCoverSummary{}, fmt.Errorf("save library release cover: %w", ErrReleaseCoverInvalid)
	}
	_, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || "image/"+format != mime {
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
		cover, err := service.repository.SetPendingReleaseCover(ctx, releaseID, expectedRevision, generation, data, mime)
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
		return ReleaseCoverImage{MIME: *cover.SelectedMIME, Data: append([]byte(nil), cover.SelectedBytes...)}, nil
	}
	if cover.PendingGeneration != nil && *cover.PendingGeneration == generation {
		return ReleaseCoverImage{MIME: *cover.PendingMIME, Data: append([]byte(nil), cover.PendingBytes...)}, nil
	}
	return ReleaseCoverImage{}, ErrReleaseCoverNotFound
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
