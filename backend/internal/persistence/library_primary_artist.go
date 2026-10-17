package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var (
	ErrLibraryPrimaryArtistConflict = errors.New("library primary artist revision conflict")
	ErrLibraryPrimaryArtistInvalid  = errors.New("library primary artist is not a current member")
	ErrLibraryPositionNotFound      = errors.New("library release position not found")
)

// SetPrimaryArtist changes only the primary-artist selection. Scope is release
// or position; expectedTrackRevision adds a membership-change fence for a
// position whose effective credits are shared by local tracks.
func (repository *LibraryRepository) SetPrimaryArtist(ctx context.Context, releaseID, positionID uuid.UUID, artistID *uuid.UUID, expectedRevision int64, expectedTrackRevision *int64) (int64, error) {
	var revision int64
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var currentRevision int64
		var selected *uuid.UUID
		if positionID == uuid.Nil {
			if err := tx.NewRaw(`SELECT metadata_revision,primary_artist_id FROM local_release WHERE id=? FOR UPDATE`, releaseID).Scan(ctx, &currentRevision, &selected); errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryReleaseNotFound
			} else if err != nil {
				return err
			}
			// Keep lock acquisition consistent with position updates and matching writes.
			positions := make([]struct {
				ID      uuid.UUID `bun:"id"`
				TrackID uuid.UUID `bun:"track_id"`
			}, 0)
			if err := tx.NewRaw(`SELECT id,track_id FROM release_track WHERE release_id=? ORDER BY medium,position,id FOR UPDATE`, releaseID).Scan(ctx, &positions); err != nil {
				return err
			}
			trackIDs := make([]uuid.UUID, 0, len(positions))
			for _, position := range positions {
				trackIDs = append(trackIDs, position.TrackID)
			}
			if len(trackIDs) > 0 {
				var locked []uuid.UUID
				if err := tx.NewRaw(`SELECT id FROM local_track WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(trackIDs)).Scan(ctx, &locked); err != nil {
					return err
				}
			}
			if expectedRevision != currentRevision {
				return ErrLibraryPrimaryArtistConflict
			}
			if artistID != nil {
				var exists bool
				if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM release_artist WHERE release_id=? AND artist_id=?)`, releaseID, *artistID).Scan(ctx, &exists); err != nil {
					return err
				}
				if !exists {
					return ErrLibraryPrimaryArtistInvalid
				}
			}
			if sameNullableUUID(selected, artistID) {
				revision = currentRevision
				return nil
			}
			if err := tx.NewRaw(`UPDATE local_release SET primary_artist_id=?,metadata_revision=metadata_revision+1,updated_at=now() WHERE id=? RETURNING metadata_revision`, artistID, releaseID).Scan(ctx, &revision); err != nil {
				return err
			}
			return nil
		}

		var releaseRevision int64
		if err := tx.NewRaw(`SELECT metadata_revision FROM local_release WHERE id=? FOR UPDATE`, releaseID).Scan(ctx, &releaseRevision); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryReleaseNotFound
		} else if err != nil {
			return err
		}
		// Lock all positions first, deterministically, then local tracks.
		positions := make([]struct {
			ID       uuid.UUID  `bun:"id"`
			TrackID  uuid.UUID  `bun:"track_id"`
			Revision int64      `bun:"metadata_revision"`
			Selected *uuid.UUID `bun:"primary_artist_id"`
		}, 0)
		if err := tx.NewRaw(`SELECT id,track_id,metadata_revision,primary_artist_id FROM release_track WHERE release_id=? ORDER BY medium,position,id FOR UPDATE`, releaseID).Scan(ctx, &positions); err != nil {
			return err
		}
		var position struct {
			TrackID  uuid.UUID
			Revision int64
			Selected *uuid.UUID
		}
		foundPosition := false
		for _, item := range positions {
			if item.ID == positionID {
				position.TrackID, position.Revision, position.Selected, foundPosition = item.TrackID, item.Revision, item.Selected, true
				break
			}
		}
		if !foundPosition {
			return ErrLibraryPositionNotFound
		}
		trackIDs := make([]uuid.UUID, 0, len(positions))
		for _, item := range positions {
			trackIDs = append(trackIDs, item.TrackID)
		}
		if len(trackIDs) > 0 {
			var tracks []struct {
				ID       uuid.UUID `bun:"id"`
				Revision int64     `bun:"metadata_revision"`
			}
			if err := tx.NewRaw(`SELECT id,metadata_revision FROM local_track WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(trackIDs)).Scan(ctx, &tracks); err != nil {
				return err
			}
			if expectedTrackRevision != nil {
				found := false
				for _, track := range tracks {
					if track.ID == position.TrackID {
						found = true
						if track.Revision != *expectedTrackRevision {
							return ErrLibraryPrimaryArtistConflict
						}
					}
				}
				if !found {
					return fmt.Errorf("position track disappeared")
				}
			}
		}
		if expectedRevision != position.Revision {
			return ErrLibraryPrimaryArtistConflict
		}
		if artistID != nil {
			var exists bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM track_artist WHERE track_id=? AND artist_id=?)`, position.TrackID, *artistID).Scan(ctx, &exists); err != nil {
				return err
			}
			if !exists {
				return ErrLibraryPrimaryArtistInvalid
			}
		}
		if sameNullableUUID(position.Selected, artistID) {
			revision = position.Revision
			return nil
		}
		if err := tx.NewRaw(`UPDATE release_track SET primary_artist_id=?,metadata_revision=metadata_revision+1 WHERE id=? RETURNING metadata_revision`, artistID, positionID).Scan(ctx, &revision); err != nil {
			return err
		}
		_ = releaseRevision // release row is locked to preserve global lock order.
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("set library primary artist: %w", err)
	}
	return revision, nil
}

func sameNullableUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
