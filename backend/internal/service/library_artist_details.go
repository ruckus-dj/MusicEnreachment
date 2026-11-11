package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type LibraryArtistDetails struct {
	ID                 uuid.UUID `json:"id"`
	Name               string    `json:"name"`
	Disambiguation     *string   `json:"disambiguation"`
	Revision           int64     `json:"revision"`
	PublicationApplied bool      `json:"publication_applied"`
}

type libraryArtistDetailsRepository interface {
	ReadLibraryArtistDetails(context.Context, uuid.UUID) (persistence.LibraryArtistDetails, error)
	UpdateLibraryArtistDetails(context.Context, uuid.UUID, int64, *string, *string) (persistence.LibraryArtistDetails, error)
}

func (service *LibraryMetadataService) ReadArtistDetails(ctx context.Context, artistID uuid.UUID) (LibraryArtistDetails, error) {
	if service == nil || service.repository == nil {
		return LibraryArtistDetails{}, errors.New("library artist details are unavailable")
	}
	repository, ok := service.repository.(libraryArtistDetailsRepository)
	if !ok {
		return LibraryArtistDetails{}, errors.New("library artist details are unavailable")
	}
	details, err := repository.ReadLibraryArtistDetails(ctx, artistID)
	if errors.Is(err, persistence.ErrLibraryMetadataNotFound) {
		return LibraryArtistDetails{}, fmt.Errorf("read library artist details: %w", ErrLibraryMetadataNotFound)
	}
	if err != nil {
		return LibraryArtistDetails{}, fmt.Errorf("read library artist details: %w", err)
	}
	return libraryArtistDetailsResult(details), nil
}

func (service *LibraryMetadataService) UpdateArtistDetails(ctx context.Context, artistID uuid.UUID, expected int64, name, disambiguation *string) (LibraryArtistDetails, error) {
	if service == nil || service.repository == nil {
		return LibraryArtistDetails{}, errors.New("library artist details are unavailable")
	}
	if service.coordinator != nil {
		release, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
		if err != nil {
			return LibraryArtistDetails{}, fmt.Errorf("acquire library artist details mutation resource: %w", err)
		}
		defer release()
	}
	if expected < 1 || (name == nil && disambiguation == nil) || (name != nil && strings.TrimSpace(*name) == "") {
		return LibraryArtistDetails{}, ErrLibraryMetadataInvalidRequest
	}
	repository, ok := service.repository.(libraryArtistDetailsRepository)
	if !ok {
		return LibraryArtistDetails{}, errors.New("library artist details are unavailable")
	}
	details, err := repository.UpdateLibraryArtistDetails(ctx, artistID, expected, name, disambiguation)
	if errors.Is(err, persistence.ErrLibraryMetadataNotFound) {
		return LibraryArtistDetails{}, fmt.Errorf("update library artist details: %w", ErrLibraryMetadataNotFound)
	}
	if errors.Is(err, persistence.ErrLibraryMetadataConflict) {
		return LibraryArtistDetails{}, fmt.Errorf("update library artist details: %w", ErrLibraryMetadataConflict)
	}
	if err != nil {
		return LibraryArtistDetails{}, fmt.Errorf("update library artist details: %w", err)
	}
	return libraryArtistDetailsResult(details), nil
}

func libraryArtistDetailsResult(details persistence.LibraryArtistDetails) LibraryArtistDetails {
	return LibraryArtistDetails{
		ID:                 details.ID,
		Name:               details.Name,
		Disambiguation:     details.Disambiguation,
		Revision:           details.Revision,
		PublicationApplied: false,
	}
}
