package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var (
	ErrLibraryProviderRefreshNotFound = errors.New("library release not found")
	ErrLibraryProviderRefreshConflict = errors.New("MusicBrainz release binding is unavailable or changed")
)

type libraryProviderRefreshRepository interface {
	ReadMusicBrainzReleaseBinding(context.Context, uuid.UUID) (string, error)
}

type libraryProviderRefreshStarter interface {
	StartMusicBrainzReleaseRefresh(context.Context, uuid.UUID, string) (*persistence.Operation, error)
}

type LibraryProviderRefresh struct {
	repository libraryProviderRefreshRepository
	provider   libraryProviderRefreshStarter
}

func NewLibraryProviderRefresh(repository libraryProviderRefreshRepository, provider libraryProviderRefreshStarter) *LibraryProviderRefresh {
	return &LibraryProviderRefresh{repository: repository, provider: provider}
}

func (service *LibraryProviderRefresh) Start(ctx context.Context, releaseID uuid.UUID) (uuid.UUID, error) {
	if service == nil || service.repository == nil || service.provider == nil {
		return uuid.Nil, errors.New("library MusicBrainz refresh is unavailable")
	}
	mbid, err := service.repository.ReadMusicBrainzReleaseBinding(ctx, releaseID)
	if err != nil {
		if errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
			return uuid.Nil, fmt.Errorf("start library MusicBrainz refresh: %w", ErrLibraryProviderRefreshNotFound)
		}
		return uuid.Nil, fmt.Errorf("start library MusicBrainz refresh: read binding: %w", err)
	}
	if mbid == "" {
		return uuid.Nil, ErrLibraryProviderRefreshConflict
	}
	operation, err := service.provider.StartMusicBrainzReleaseRefresh(ctx, releaseID, mbid)
	if err != nil {
		if errors.Is(err, persistence.ErrMusicBrainzReleaseBindingChanged) {
			return uuid.Nil, fmt.Errorf("start library MusicBrainz refresh: %w", ErrLibraryProviderRefreshConflict)
		}
		return uuid.Nil, fmt.Errorf("start library MusicBrainz refresh: %w", err)
	}
	return operation.ID, nil
}
