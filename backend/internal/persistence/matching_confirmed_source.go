package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingConfirmedSource is the exact library position and source association
// established by one manual or Local confirmation. PositionID is the
// release_track row at the confirmed medium/position; TrackSourceID is the
// exact track_source link for the attached media variant.
type MatchingConfirmedSource struct {
	FileMemberID   uuid.UUID `json:"file_member_id"`
	PositionID     uuid.UUID `json:"position_id"`
	TrackSourceID  uuid.UUID `json:"track_source_id"`
	MediaVariantID uuid.UUID `json:"media_variant_id"`
}

// readMatchingConfirmedSource follows the library track established by
// materialization. Reusing a track does not move its library position when the
// provider reorders the release. A missing or ambiguous library position is a
// conflict, not permission to choose a position by its provider ordinal.
func readMatchingConfirmedSource(ctx context.Context, tx bun.Tx, fileMemberID, releaseID, localTrackID, variantID uuid.UUID) (MatchingConfirmedSource, error) {
	if fileMemberID == uuid.Nil || releaseID == uuid.Nil || localTrackID == uuid.Nil || variantID == uuid.Nil {
		return MatchingConfirmedSource{}, errors.New("confirmed matching source identity is invalid")
	}
	var links []MatchingConfirmedSource
	err := tx.NewRaw(`SELECT layout.id AS position_id, association.id AS track_source_id
		FROM release_track layout
		JOIN track_source association ON association.track_id=layout.track_id
		WHERE layout.release_id=? AND layout.track_id=? AND association.media_variant_id=?
		LIMIT 2`, releaseID, localTrackID, variantID).Scan(ctx, &links)
	if err != nil {
		return MatchingConfirmedSource{}, fmt.Errorf("read confirmed matching source: %w", err)
	}
	if len(links) != 1 {
		return MatchingConfirmedSource{}, ErrIncomingMatchConflict
	}
	source := links[0]
	source.FileMemberID = fileMemberID
	source.MediaVariantID = variantID
	return source, nil
}
