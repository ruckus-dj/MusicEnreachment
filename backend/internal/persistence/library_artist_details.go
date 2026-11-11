package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
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

func (repository *LibraryRepository) UpdateLibraryArtistDetails(ctx context.Context, artistID uuid.UUID, expected int64, name, disambiguation *string) (LibraryArtistDetails, error) {
	var result LibraryArtistDetails
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var currentRevision int64
		var currentName string
		if err := tx.NewRaw(`SELECT name,metadata_revision
			FROM local_artist WHERE id=? FOR UPDATE`, artistID).Scan(ctx, &currentName, &currentRevision); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryMetadataNotFound
		} else if err != nil {
			return err
		}
		if currentRevision != expected {
			return ErrLibraryMetadataConflict
		}

		updatedName := currentName
		if name != nil {
			updatedName = *name
		}
		var updatedNullDisambiguation sql.NullString
		var updatedHasDisambiguation bool
		if disambiguation != nil {
			encoded, err := json.Marshal(*disambiguation)
			if err != nil {
				return fmt.Errorf("encode library artist disambiguation: %w", err)
			}
			if err := tx.NewRaw(`UPDATE local_artist SET name=?,metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{disambiguation}',?::jsonb,true),metadata_revision=metadata_revision+1,updated_at=now()
				WHERE id=? RETURNING id,name,metadata->'disambiguation' IS NOT NULL,metadata->>'disambiguation',metadata_revision`, updatedName, string(encoded), artistID).Scan(ctx, &result.ID, &result.Name, &updatedHasDisambiguation, &updatedNullDisambiguation, &result.Revision); err != nil {
				return err
			}
		} else if err := tx.NewRaw(`UPDATE local_artist SET name=?,metadata_revision=metadata_revision+1,updated_at=now()
			WHERE id=? RETURNING id,name,metadata->'disambiguation' IS NOT NULL,metadata->>'disambiguation',metadata_revision`, updatedName, artistID).Scan(ctx, &result.ID, &result.Name, &updatedHasDisambiguation, &updatedNullDisambiguation, &result.Revision); err != nil {
			return err
		}

		if updatedHasDisambiguation {
			value := updatedNullDisambiguation.String
			result.Disambiguation = &value
		}
		return nil
	})
	if errors.Is(err, ErrLibraryMetadataNotFound) || errors.Is(err, ErrLibraryMetadataConflict) {
		return LibraryArtistDetails{}, err
	}
	if err != nil {
		return LibraryArtistDetails{}, fmt.Errorf("update library artist details: %w", err)
	}
	return result, nil
}
