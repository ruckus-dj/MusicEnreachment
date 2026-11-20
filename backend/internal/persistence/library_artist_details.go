package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type LibraryArtistDetails struct {
	ID             uuid.UUID
	Name           string
	Disambiguation *string
	Revision       int64
}

func (repository *LibraryRepository) ReadLibraryArtistDetails(ctx context.Context, artistID uuid.UUID) (LibraryArtistDetails, error) {
	var result LibraryArtistDetails
	var disambiguation sql.NullString
	var hasDisambiguation bool
	err := repository.db.NewRaw(`SELECT id,name,metadata->'disambiguation' IS NOT NULL,metadata->>'disambiguation',metadata_revision
		FROM local_artist WHERE id=?`, artistID).Scan(ctx, &result.ID, &result.Name, &hasDisambiguation, &disambiguation, &result.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryArtistDetails{}, ErrLibraryMetadataNotFound
	}
	if err != nil {
		return LibraryArtistDetails{}, fmt.Errorf("read library artist details: %w", err)
	}
	if hasDisambiguation {
		value := disambiguation.String
		result.Disambiguation = &value
	}
	return result, nil
}
