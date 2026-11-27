package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ReadReleaseMusicBrainzReleaseID returns the exact MusicBrainz release MBID
// linked to a visible library release. A visible release without a MusicBrainz
// link returns an empty identity and linked=false. It never falls back to a
// release group or another provider.
func (repository *LibraryRepository) ReadReleaseMusicBrainzReleaseID(ctx context.Context, releaseID uuid.UUID) (string, bool, error) {
	if releaseID == uuid.Nil {
		return "", false, errors.New("release identity is required")
	}
	return readReleaseMusicBrainzReleaseID(ctx, repository.db, releaseID, false)
}

func readReleaseMusicBrainzReleaseID(ctx context.Context, database bun.IDB, releaseID uuid.UUID, lock bool) (string, bool, error) {
	var exists bool
	if err := database.NewRaw(`SELECT EXISTS (SELECT 1 FROM local_release WHERE id=?)`, releaseID).Scan(ctx, &exists); err != nil {
		return "", false, fmt.Errorf("read library release identity: %w", err)
	}
	if !exists {
		return "", false, fmt.Errorf("read library release identity: %w", ErrLibraryReleaseNotFound)
	}
	var mbid sql.NullString
	query := `SELECT provider_release.provider_key
		FROM provider_local_release
		JOIN provider ON provider.id=provider_local_release.provider_id AND provider.code='musicbrainz'
		JOIN provider_release ON provider_release.id=provider_local_release.provider_release_id
			AND provider_release.provider_id=provider_local_release.provider_id
		WHERE provider_local_release.local_release_id=?`
	if lock {
		// The pending write uses the same transaction; locking the binding row
		// makes a concurrent relink wait for the CAS decision.
		query += ` FOR UPDATE OF provider_local_release`
	}
	err := database.NewRaw(query, releaseID).Scan(ctx, &mbid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read library release identity: %w", err)
	}
	if !mbid.Valid || mbid.String == "" {
		return "", false, nil
	}
	return mbid.String, true, nil
}
