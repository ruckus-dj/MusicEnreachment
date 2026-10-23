package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var ErrReleaseCoverConflict = errors.New("release cover authority changed")

type ReleaseCover struct {
	ReleaseID          uuid.UUID
	Revision           int64
	Mode               string
	SelectedGeneration *uuid.UUID
	SelectedBytes      []byte
	SelectedMIME       *string
	SelectedSHA256     []byte
	SelectedSize       *int64
	PendingGeneration  *uuid.UUID
	PendingBytes       []byte
	PendingMIME        *string
	PendingSHA256      []byte
	PendingSize        *int64
	AcquisitionOwner   *uuid.UUID
	Warning            *string
}

// ReadReleaseCover returns an absent-but-uninitialized record without creating
// product state. The automatic/empty baseline is created only on a CAS write.
func (repository *LibraryRepository) ReadReleaseCover(ctx context.Context, releaseID uuid.UUID) (ReleaseCover, error) {
	if releaseID == uuid.Nil {
		return ReleaseCover{}, errors.New("release cover identity is required")
	}
	result, err := readReleaseCover(ctx, repository.db, releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return ReleaseCover{ReleaseID: releaseID, Revision: 1, Mode: "automatic"}, nil
	}
	if err != nil {
		return ReleaseCover{}, fmt.Errorf("read release cover authority: %w", err)
	}
	if err := result.validate(); err != nil {
		return ReleaseCover{}, fmt.Errorf("read release cover authority: %w", err)
	}
	return result, nil
}

func readReleaseCover(ctx context.Context, database bun.IDB, releaseID uuid.UUID) (ReleaseCover, error) {
	var result ReleaseCover
	err := database.NewRaw(`SELECT release_id,revision,mode,selected_generation,selected_bytes,selected_mime,selected_sha256,selected_size,
		pending_generation,pending_bytes,pending_mime,pending_sha256,pending_size,acquisition_owner,warning
		FROM release_publication_cover WHERE release_id=?`, releaseID).
		Scan(ctx, &result.ReleaseID, &result.Revision, &result.Mode, &result.SelectedGeneration, &result.SelectedBytes,
			&result.SelectedMIME, &result.SelectedSHA256, &result.SelectedSize, &result.PendingGeneration,
			&result.PendingBytes, &result.PendingMIME, &result.PendingSHA256, &result.PendingSize,
			&result.AcquisitionOwner, &result.Warning)
	if err == nil {
		err = result.validate()
	}
	return result, err
}

func (cover ReleaseCover) validate() error {
	if cover.Revision < 1 || cover.Mode != "automatic" && cover.Mode != "manual" {
		return errors.New("release cover authority has invalid revision or mode")
	}
	for _, image := range []struct {
		generation *uuid.UUID
		data       []byte
		mime       *string
		digest     []byte
		size       *int64
	}{{cover.SelectedGeneration, cover.SelectedBytes, cover.SelectedMIME, cover.SelectedSHA256, cover.SelectedSize},
		{cover.PendingGeneration, cover.PendingBytes, cover.PendingMIME, cover.PendingSHA256, cover.PendingSize}} {
		if image.generation == nil && len(image.data) == 0 && image.mime == nil && len(image.digest) == 0 && image.size == nil {
			continue
		}
		if image.generation == nil || *image.generation == uuid.Nil || len(image.data) == 0 || image.mime == nil || len(image.digest) != sha256.Size || image.size == nil || *image.size != int64(len(image.data)) {
			return errors.New("release cover authority has an incomplete image tuple")
		}
		if *image.mime != "image/png" && *image.mime != "image/jpeg" {
			return errors.New("release cover authority has an unsupported image MIME type")
		}
		if err := validateReleaseCoverImage(*image.generation, image.data, *image.mime); err != nil {
			return fmt.Errorf("release cover authority image is invalid: %w", err)
		}
		digest := sha256.Sum256(image.data)
		if !bytes.Equal(digest[:], image.digest) {
			return errors.New("release cover authority image digest does not match its bytes")
		}
	}
	if cover.PendingGeneration != nil && cover.Mode != "manual" {
		return errors.New("automatic release cover authority contains a pending manual choice")
	}
	if cover.AcquisitionOwner != nil && (cover.Mode != "automatic" || *cover.AcquisitionOwner == uuid.Nil) {
		return errors.New("release cover acquisition owner is invalid for the current mode")
	}
	return nil
}

// SetPendingReleaseCover records a manual choice without changing the effective
// selected image. Applying the pending image is a separate explicit CAS action.
func (repository *LibraryRepository) SetPendingReleaseCover(ctx context.Context, releaseID uuid.UUID, expectedRevision int64, generation uuid.UUID, data []byte, mime string) (ReleaseCover, error) {
	if err := validateReleaseCoverImage(generation, data, mime); err != nil {
		return ReleaseCover{}, err
	}
	return repository.mutateReleaseCover(ctx, releaseID, expectedRevision, func(tx bun.Tx, current ReleaseCover) error {
		digest := sha256.Sum256(data)
		size := int64(len(data))
		_, err := tx.ExecContext(ctx, `UPDATE release_publication_cover SET revision=revision+1,mode='manual',pending_generation=?,pending_bytes=?,pending_mime=?,pending_sha256=?,pending_size=?,acquisition_owner=NULL,updated_at=now() WHERE release_id=?`, generation, data, mime, digest[:], size, releaseID)
		return err
	})
}

func (repository *LibraryRepository) ApplyPendingReleaseCover(ctx context.Context, releaseID uuid.UUID, expectedRevision int64, pendingGeneration uuid.UUID) (ReleaseCover, error) {
	return repository.mutateReleaseCover(ctx, releaseID, expectedRevision, func(tx bun.Tx, current ReleaseCover) error {
		return applyPendingReleaseCoverTx(ctx, tx, current, pendingGeneration)
	})
}

// applyPendingReleaseCoverTx promotes the selected pending image in a caller's
// transaction so publication admission can make the authority change atomic
// with its publication attempts and River jobs.
func applyPendingReleaseCoverTx(ctx context.Context, tx bun.Tx, current ReleaseCover, pendingGeneration uuid.UUID) error {
	if current.Mode != "manual" || current.PendingGeneration == nil || *current.PendingGeneration != pendingGeneration || current.AcquisitionOwner != nil {
		return ErrReleaseCoverConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE release_publication_cover SET revision=revision+1,
		selected_generation=pending_generation,selected_bytes=pending_bytes,selected_mime=pending_mime,selected_sha256=pending_sha256,selected_size=pending_size,
		pending_generation=NULL,pending_bytes=NULL,pending_mime=NULL,pending_sha256=NULL,pending_size=NULL,acquisition_owner=NULL,warning=NULL,updated_at=now()
		WHERE release_id=? AND revision=? AND mode='manual' AND pending_generation=?`, current.ReleaseID, current.Revision, pendingGeneration)
	return requireOneReleaseCoverRow(result, err)
}

func (repository *LibraryRepository) ReturnReleaseCoverToAutomatic(ctx context.Context, releaseID uuid.UUID, expectedRevision int64) (ReleaseCover, error) {
	return repository.mutateReleaseCover(ctx, releaseID, expectedRevision, func(tx bun.Tx, current ReleaseCover) error {
		_, err := tx.ExecContext(ctx, `UPDATE release_publication_cover SET revision=revision+1,mode='automatic',pending_generation=NULL,pending_bytes=NULL,pending_mime=NULL,pending_sha256=NULL,pending_size=NULL,acquisition_owner=NULL,updated_at=now() WHERE release_id=?`, releaseID)
		return err
	})
}

// ClaimReleaseCoverAcquisition installs one durable owner; completion and
// failure must present this same token. This slice does not start acquisitions.
func (repository *LibraryRepository) ClaimReleaseCoverAcquisition(ctx context.Context, releaseID uuid.UUID, expectedRevision int64, owner uuid.UUID) (ReleaseCover, error) {
	if owner == uuid.Nil {
		return ReleaseCover{}, errors.New("release cover acquisition owner is required")
	}
	return repository.mutateReleaseCover(ctx, releaseID, expectedRevision, func(tx bun.Tx, current ReleaseCover) error {
		if current.Mode != "automatic" {
			return ErrReleaseCoverConflict
		}
		result, err := tx.ExecContext(ctx, `UPDATE release_publication_cover SET revision=revision+1,acquisition_owner=?,updated_at=now() WHERE release_id=? AND acquisition_owner IS NULL`, owner, releaseID)
		return requireOneReleaseCoverRow(result, err)
	})
}

func (repository *LibraryRepository) FinishReleaseCoverAcquisition(ctx context.Context, releaseID uuid.UUID, expectedRevision int64, owner uuid.UUID, data []byte, mime string) (ReleaseCover, error) {
	if err := validateReleaseCoverImage(uuid.New(), data, mime); err != nil {
		return ReleaseCover{}, err
	}
	return repository.mutateReleaseCover(ctx, releaseID, expectedRevision, func(tx bun.Tx, current ReleaseCover) error {
		if current.AcquisitionOwner == nil || *current.AcquisitionOwner != owner || current.Mode != "automatic" {
			return ErrReleaseCoverConflict
		}
		digest := sha256.Sum256(data)
		size := int64(len(data))
		_, err := tx.ExecContext(ctx, `UPDATE release_publication_cover SET revision=revision+1,selected_generation=?,selected_bytes=?,selected_mime=?,selected_sha256=?,selected_size=?,acquisition_owner=NULL,warning=NULL,updated_at=now() WHERE release_id=?`, uuid.New(), data, mime, digest[:], size, releaseID)
		return err
	})
}

func (repository *LibraryRepository) FailReleaseCoverAcquisition(ctx context.Context, releaseID uuid.UUID, expectedRevision int64, owner uuid.UUID, warning string) (ReleaseCover, error) {
	if warning == "" {
		return ReleaseCover{}, errors.New("release cover warning is required")
	}
	return repository.mutateReleaseCover(ctx, releaseID, expectedRevision, func(tx bun.Tx, current ReleaseCover) error {
		if current.AcquisitionOwner == nil || *current.AcquisitionOwner != owner || current.Mode != "automatic" {
			return ErrReleaseCoverConflict
		}
		_, err := tx.ExecContext(ctx, `UPDATE release_publication_cover SET revision=revision+1,acquisition_owner=NULL,warning=?,updated_at=now() WHERE release_id=?`, warning, releaseID)
		return err
	})
}

func (repository *LibraryRepository) mutateReleaseCover(ctx context.Context, releaseID uuid.UUID, expectedRevision int64, mutate func(bun.Tx, ReleaseCover) error) (ReleaseCover, error) {
	if releaseID == uuid.Nil || expectedRevision < 1 {
		return ReleaseCover{}, errors.New("invalid release cover identity or revision")
	}
	var result ReleaseCover
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_publication_cover(release_id) VALUES (?) ON CONFLICT (release_id) DO NOTHING`, releaseID); err != nil {
			return err
		}
		var locked uuid.UUID
		if err := tx.NewRaw(`SELECT release_id FROM release_publication_cover WHERE release_id=? FOR UPDATE`, releaseID).Scan(ctx, &locked); err != nil {
			return err
		}
		current, err := readReleaseCover(ctx, tx, releaseID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return ErrReleaseCoverConflict
		}
		if err := mutate(tx, current); err != nil {
			return err
		}
		result, err = readReleaseCover(ctx, tx, releaseID)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrReleaseCoverConflict) {
			return ReleaseCover{}, err
		}
		return ReleaseCover{}, fmt.Errorf("update release cover authority: %w", err)
	}
	return result, nil
}

func requireOneReleaseCoverRow(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrReleaseCoverConflict
	}
	return nil
}

func validateReleaseCoverImage(generation uuid.UUID, data []byte, mime string) error {
	if generation == uuid.Nil || len(data) == 0 || mime != "image/png" && mime != "image/jpeg" {
		return errors.New("release cover image is invalid")
	}
	_, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || format != strings.TrimPrefix(mime, "image/") {
		return errors.New("release cover image bytes do not match their declared MIME type")
	}
	return nil
}
