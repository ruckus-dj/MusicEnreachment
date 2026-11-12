package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// LibraryCatalogAudioCandidate is a database snapshot candidate for service-side
// filesystem observation. It does not authorize filesystem mutation.
type LibraryCatalogAudioCandidate struct {
	PositionID   uuid.UUID
	RootPath     string
	RelativePath string
	SizeBytes    int64
	MTimeNS      int64 `bun:"mtime_ns"`
	Kind         string
}

func normalizeCatalogAvailability(request LibraryCatalogRequest) LibraryCatalogRequest {
	if request.Availability == "" {
		request.Availability = "all"
	}
	return request
}

func availabilityPositionIDs(request LibraryCatalogRequest) []uuid.UUID {
	if len(request.AvailablePositionIDs) == 0 {
		return []uuid.UUID{uuid.Nil}
	}
	return request.AvailablePositionIDs
}

// ReadLibraryAudioCandidates reads candidate paths only; filesystem inspection
// and the resulting availability observation belong to the service layer.
func (repository *LibraryRepository) ReadLibraryAudioCandidates(ctx context.Context, request LibraryCatalogRequest) ([]LibraryCatalogAudioCandidate, error) {
	rows := make([]LibraryCatalogAudioCandidate, 0)
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		query := `SELECT rt.id AS position_id,sr.configured_path AS root_path,sl.relative_path,sl.size_bytes,
			(EXTRACT(EPOCH FROM sl.mtime)::numeric*1000000000)::bigint AS mtime_ns,'source' AS kind
			FROM release_track rt
			JOIN track_source ts ON ts.track_id=rt.track_id
			JOIN source_location sl ON sl.media_variant_id=ts.media_variant_id AND sl.probe_status='audio'
			JOIN source_root sr ON sr.id=sl.source_root_id AND sr.enabled AND sr.status='available'
				AND sr.inventory_path=sr.configured_path
			JOIN local_release r ON r.id=rt.release_id
			WHERE (?::uuid IS NULL OR r.id=?)
			AND (?::uuid IS NULL OR (EXISTS(SELECT 1 FROM release_artist ra WHERE ra.release_id=r.id AND ra.artist_id=?) AND EXISTS(SELECT 1 FROM local_artist requested_artist WHERE requested_artist.id=? AND requested_artist.catalog_visible IS TRUE)))
			UNION ALL
			SELECT rt.id AS position_id,setting.setting_value AS root_path,tp.relative_path,0::bigint AS size_bytes,0::bigint AS mtime_ns,'publication' AS kind
			FROM track_publication tp
			JOIN publication_namespace_state ns ON ns.singleton=true AND tp.epoch=ns.output_epoch
			JOIN app_setting setting ON setting.setting_name='output_directory' AND setting.setting_value<>''
			JOIN release_track rt ON rt.id=tp.position_id
			JOIN local_release r ON r.id=rt.release_id
			WHERE (?::uuid IS NULL OR r.id=?)
			AND (?::uuid IS NULL OR (EXISTS(SELECT 1 FROM release_artist ra WHERE ra.release_id=r.id AND ra.artist_id=?) AND EXISTS(SELECT 1 FROM local_artist requested_artist WHERE requested_artist.id=? AND requested_artist.catalog_visible IS TRUE)))
			ORDER BY position_id,kind,root_path,relative_path`
		return tx.NewRaw(query, request.ReleaseID, request.ReleaseID, request.ArtistID, request.ArtistID, request.ArtistID,
			request.ReleaseID, request.ReleaseID, request.ArtistID, request.ArtistID, request.ArtistID).Scan(ctx, &rows)
	})
	if err != nil {
		return nil, fmt.Errorf("read library audio candidates: %w", err)
	}
	return rows, nil
}
