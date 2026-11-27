package persistence

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// ReadCoverArtArchiveReleaseStatuses reads front/gallery availability for a
// bounded set of exact release MBIDs in one query. Missing rows are reported as
// unchecked so callers do not mistake an uncached edition for a completed one.
func (repository *LibraryRepository) ReadCoverArtArchiveReleaseStatuses(ctx context.Context, releaseMBIDs []string) (map[string]CoverArtArchiveRelease, error) {
	result := make(map[string]CoverArtArchiveRelease, len(releaseMBIDs))
	if len(releaseMBIDs) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(releaseMBIDs))
	for _, value := range releaseMBIDs {
		mbid, err := canonicalCoverArtArchiveMBID(value)
		if err != nil {
			return nil, fmt.Errorf("read cover art archive release statuses: %w", err)
		}
		if _, duplicate := result[mbid]; duplicate {
			continue
		}
		result[mbid] = CoverArtArchiveRelease{
			ReleaseMBID:   mbid,
			FrontStatus:   CoverArtArchiveUnchecked,
			GalleryStatus: CoverArtArchiveUnchecked,
		}
		ids = append(ids, mbid)
	}
	type row struct {
		ReleaseMBID   string `bun:"release_mbid"`
		FrontStatus   string `bun:"front_status"`
		GalleryStatus string `bun:"gallery_status"`
	}
	rows := make([]row, 0, len(ids))
	if err := repository.db.NewRaw(`SELECT release_mbid::text AS release_mbid, front_status, gallery_status
		FROM cover_art_archive_release WHERE release_mbid IN (?)`, bun.List(ids)).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("read cover art archive release statuses: %w", err)
	}
	for _, row := range rows {
		result[row.ReleaseMBID] = CoverArtArchiveRelease{
			ReleaseMBID:   row.ReleaseMBID,
			FrontStatus:   row.FrontStatus,
			GalleryStatus: row.GalleryStatus,
		}
	}
	return result, nil
}
