package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var (
	ErrLibraryPrimaryArtistInvalidRequest = errors.New("library primary artist request is invalid")
	ErrLibraryPrimaryArtistConflict       = errors.New("library primary artist revision conflict")
	ErrLibraryPrimaryArtistInvalid        = errors.New("library primary artist is not a current member")
	ErrLibraryPositionNotFound            = errors.New("library release position not found")
)

type libraryPrimaryArtistWriter interface {
	SetPrimaryArtist(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, int64, *int64) (int64, error)
}

type LibraryPrimaryArtists struct{ repository libraryPrimaryArtistWriter }

func NewLibraryPrimaryArtists(repository libraryPrimaryArtistWriter) *LibraryPrimaryArtists {
	return &LibraryPrimaryArtists{repository: repository}
}

func (service *LibraryPrimaryArtists) Set(ctx context.Context, releaseID, positionID uuid.UUID, artistID *uuid.UUID, expectedRevision int64, expectedTrackRevision *int64) (int64, error) {
	if service == nil || service.repository == nil {
		return 0, errors.New("library primary artist selection is unavailable")
	}
	if releaseID == uuid.Nil || expectedRevision < 1 || (artistID != nil && *artistID == uuid.Nil) || (expectedTrackRevision != nil && *expectedTrackRevision < 1) || (positionID != uuid.Nil && expectedTrackRevision == nil) {
		return 0, fmt.Errorf("set library primary artist: %w", ErrLibraryPrimaryArtistInvalidRequest)
	}
	revision, err := service.repository.SetPrimaryArtist(ctx, releaseID, positionID, artistID, expectedRevision, expectedTrackRevision)
	if errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
		return 0, fmt.Errorf("set library primary artist: %w", ErrLibraryReleaseNotFound)
	}
	if errors.Is(err, persistence.ErrLibraryPositionNotFound) {
		return 0, fmt.Errorf("set library primary artist: %w", ErrLibraryPositionNotFound)
	}
	if errors.Is(err, persistence.ErrLibraryPrimaryArtistConflict) {
		return 0, fmt.Errorf("set library primary artist: %w", ErrLibraryPrimaryArtistConflict)
	}
	if errors.Is(err, persistence.ErrLibraryPrimaryArtistInvalid) {
		return 0, fmt.Errorf("set library primary artist: %w", ErrLibraryPrimaryArtistInvalid)
	}
	if err != nil {
		return 0, fmt.Errorf("set library primary artist: %w", err)
	}
	return revision, nil
}
