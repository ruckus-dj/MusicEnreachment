package persistence

import (
	"context"
	"crypto/sha256"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// RecordAutomaticReleaseCoverSelection commits a complete acquisition result.
// There is no intermediate owner to strand on worker cancellation: the durable
// reconciliation delivery may simply repeat the lookup after a failure.
func (repository *LibraryRepository) RecordAutomaticReleaseCoverSelection(ctx context.Context, releaseID uuid.UUID, revision int64, data []byte, mime string, warning *string) (ReleaseCover, error) {
	if len(data) > 0 {
		if err := validateNewReleaseCoverImage(uuid.New(), data, mime); err != nil {
			return ReleaseCover{}, err
		}
	}
	return repository.mutateReleaseCover(ctx, releaseID, revision, func(tx bun.Tx, current ReleaseCover) error {
		if current.Mode != "automatic" || current.AcquisitionOwner != nil {
			return ErrReleaseCoverConflict
		}
		if len(data) == 0 {
			_, err := tx.ExecContext(ctx, `UPDATE release_publication_cover SET revision=revision+1,warning=?,updated_at=now() WHERE release_id=?`, warning, releaseID)
			return err
		}
		digest := sha256.Sum256(data)
		if current.SelectedGeneration != nil && string(current.SelectedSHA256) == string(digest[:]) && current.SelectedMIME != nil && *current.SelectedMIME == mime {
			if current.Warning == nil {
				return nil
			}
			_, err := tx.ExecContext(ctx, `UPDATE release_publication_cover SET revision=revision+1,warning=NULL,updated_at=now() WHERE release_id=?`, releaseID)
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE release_publication_cover SET revision=revision+1,selected_generation=?,selected_bytes=?,selected_mime=?,selected_sha256=?,selected_size=?,warning=NULL,updated_at=now() WHERE release_id=?`, uuid.New(), data, mime, digest[:], int64(len(data)), releaseID)
		return err
	})
}
