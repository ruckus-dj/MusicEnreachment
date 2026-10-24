package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ReleaseCoverCandidate is a saved metadata observation with a non-empty,
// observed cover, linked to at least one track on a local release.
type ReleaseCoverCandidate struct {
	ID              uuid.UUID
	WinningResultID uuid.UUID
	SourceSHA256    []byte
	MIME            string
	Data            []byte
	SizeBytes       int64
}

// ReleaseCoverCandidates contains provider identity and all distinct saved
// cover observations associated with tracks on a release. Candidate order is
// stable by metadata-result ID and does not imply a preference.
type ReleaseCoverCandidates struct {
	MusicBrainzMBID *string
	Candidates      []ReleaseCoverCandidate
	CoverErrors     []string
}

// ReadReleaseCoverCandidates reads candidates in a single repeatable-read
// snapshot. Covers are read from saved analysis results; source files are never
// reopened here.
func (repository *LibraryRepository) ReadReleaseCoverCandidates(ctx context.Context, releaseID uuid.UUID) (*ReleaseCoverCandidates, error) {
	result := &ReleaseCoverCandidates{Candidates: make([]ReleaseCoverCandidate, 0)}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var exists bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM local_release WHERE id=?)`, releaseID).Scan(ctx, &exists); err != nil {
			return fmt.Errorf("check library release for cover candidates: %w", err)
		}
		if !exists {
			return fmt.Errorf("read library release cover candidates: %w", ErrLibraryReleaseNotFound)
		}
		var mbid sql.NullString
		if err := tx.NewRaw(`SELECT provider_release.provider_key
			FROM provider_local_release
			JOIN provider ON provider.id=provider_local_release.provider_id AND provider.code='musicbrainz'
			JOIN provider_release ON provider_release.id=provider_local_release.provider_release_id
				AND provider_release.provider_id=provider_local_release.provider_id
			WHERE provider_local_release.local_release_id=?`, releaseID).Scan(ctx, &mbid); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read MusicBrainz release identity for cover candidates: %w", err)
		}
		if mbid.Valid {
			result.MusicBrainzMBID = &mbid.String
		}
		if err := tx.NewRaw(`SELECT DISTINCT result.cover_error
			FROM release_track position
			JOIN track_source association ON association.track_id=position.track_id
			JOIN media_metadata_result result ON result.id=association.source_metadata_result_id
			WHERE position.release_id=? AND result.cover_error IS NOT NULL
			ORDER BY result.cover_error`, releaseID).Scan(ctx, &result.CoverErrors); err != nil {
			return fmt.Errorf("read saved cover diagnostics: %w", err)
		}
		if err := tx.NewRaw(`SELECT DISTINCT result.id, result.winning_result_id, result.source_sha256,
			result.cover_mime AS mime, result.cover_data AS data, result.cover_size_bytes AS size_bytes
			FROM release_track position
			JOIN track_source association ON association.track_id=position.track_id
			JOIN media_metadata_result result ON result.id=association.source_metadata_result_id
			WHERE position.release_id=? AND result.cover_observed=true
				AND result.cover_data IS NOT NULL AND octet_length(result.cover_data)>0
			ORDER BY result.id`, releaseID).Scan(ctx, &result.Candidates); err != nil {
			return fmt.Errorf("read library release cover candidates: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
