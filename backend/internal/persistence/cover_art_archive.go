package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Cover Art Archive cache states. "unchecked" also covers a transient failed
// attempt; only an explicit provider answer may become absent/complete.
const (
	CoverArtArchiveUnchecked       = "unchecked"
	CoverArtArchiveFrontPresent    = "present"
	CoverArtArchiveFrontAbsent     = "absent"
	CoverArtArchiveGalleryComplete = "complete"

	maxCoverArtArchiveGalleryImages = 500
)

var ErrCoverArtArchiveImageNotFound = errors.New("cover art archive image not found")

// CoverArtArchiveRelease is the durable state of one exact release MBID. Front
// availability and gallery availability are reported independently. Images
// contains entries from the latest fetched listing while GalleryStatus reports
// whether that listing is fully downloaded and definitive.
type CoverArtArchiveRelease struct {
	ReleaseMBID      string
	FrontStatus      string
	FrontImageID     *string
	FrontCheckedAt   *time.Time
	FrontError       *string
	GalleryStatus    string
	GalleryListedAt  *time.Time
	GalleryCheckedAt *time.Time
	GalleryError     *string
	Images           []CoverArtArchiveImage
}

// CoverArtArchiveImage is one image listing entry and, when read through
// ReadCoverArtArchiveImage, its cached bytes. Data is never loaded by
// ReadCoverArtArchiveRelease.
type CoverArtArchiveImage struct {
	ID            string
	Position      int
	Front         bool
	Back          bool
	Types         []string
	Comment       string
	ThumbnailURL  string
	Listed        bool
	Cached        bool
	Data          []byte
	MIME          *string
	SHA256        []byte
	SizeBytes     *int64
	DownloadedAt  *time.Time
	DownloadError *string
}

// CoverArtArchiveFront is the independently cached front image for a release.
// Checked is false while the provider has not returned a definitive answer;
// Found is true only when usable bytes are present.
type CoverArtArchiveFront struct {
	Status    string
	Checked   bool
	Found     bool
	ImageID   *string
	Data      []byte
	MIME      *string
	SHA256    []byte
	SizeBytes *int64
	CheckedAt *time.Time
	LastError *string
}

// SaveCoverArtArchiveGalleryCandidates records one fetched gallery listing
// without declaring it complete. It is idempotent, preserves previously cached
// image bytes, and marks images absent from the new listing as unlisted instead
// of deleting them. Empty images is a fetched empty listing; gallery completion
// (and therefore definitive absence) is recorded separately.
func (repository *LibraryRepository) SaveCoverArtArchiveGalleryCandidates(ctx context.Context, releaseMBID string, images []CoverArtArchiveImage) error {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return fmt.Errorf("save cover art archive gallery candidates: %w", err)
	}
	if len(images) > maxCoverArtArchiveGalleryImages {
		return errors.New("save cover art archive gallery candidates: too many images")
	}
	type storedImage struct {
		id           string
		position     int
		front        bool
		back         bool
		types        string
		comment      string
		thumbnailURL string
	}
	stored := make([]storedImage, 0, len(images))
	seen := make(map[string]struct{}, len(images))
	for position, image := range images {
		id, ok := canonicalCoverArtArchiveImageID(image.ID)
		if !ok {
			return errors.New("save cover art archive gallery candidates: image ID is invalid")
		}
		if _, duplicate := seen[id]; duplicate {
			return errors.New("save cover art archive gallery candidates: image IDs contain a duplicate")
		}
		seen[id] = struct{}{}
		types := nonNilStrings(image.Types)
		encodedTypes, err := json.Marshal(types)
		if err != nil {
			return fmt.Errorf("save cover art archive gallery candidates: encode image types: %w", err)
		}
		stored = append(stored, storedImage{
			id: id, position: position, front: image.Front, back: image.Back,
			types: string(encodedTypes), comment: image.Comment, thumbnailURL: image.ThumbnailURL,
		})
	}
	err = repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO cover_art_archive_release
			(release_mbid, gallery_status, gallery_listed_at, gallery_checked_at, gallery_error)
			VALUES (?, 'unchecked', now(), NULL, NULL)
			ON CONFLICT (release_mbid) DO UPDATE SET
				gallery_status='unchecked', gallery_listed_at=now(), gallery_checked_at=NULL,
				gallery_error=NULL, updated_at=now()`, mbid).Exec(ctx); err != nil {
			return fmt.Errorf("save cover art archive gallery candidates: record listing: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE cover_art_archive_image SET listed=false, updated_at=now()
			WHERE release_mbid=? AND listed`, mbid).Exec(ctx); err != nil {
			return fmt.Errorf("save cover art archive gallery candidates: retire previous listing: %w", err)
		}
		for _, image := range stored {
			if _, err := tx.NewRaw(`INSERT INTO cover_art_archive_image
				(release_mbid, image_id, position, front, back, types, comment, thumbnail_url, listed)
				VALUES (?, ?, ?, ?, ?, ?::jsonb, ?, ?, true)
				ON CONFLICT (release_mbid, image_id) DO UPDATE SET
					position=EXCLUDED.position, front=EXCLUDED.front, back=EXCLUDED.back,
					types=EXCLUDED.types, comment=EXCLUDED.comment, thumbnail_url=EXCLUDED.thumbnail_url,
					listed=true, updated_at=now()`,
				mbid, image.id, image.position, image.front, image.back, image.types, image.comment, image.thumbnailURL).Exec(ctx); err != nil {
				return fmt.Errorf("save cover art archive gallery candidates: record image %s: %w", image.id, err)
			}
		}
		return nil
	})
	return err
}

// CompleteCoverArtArchiveGallery atomically marks the current listed candidates
// complete. Every listed image must already have durable bytes. An empty listed
// set is a definitive absence. On failure the previous status and all cached
// images remain unchanged.
func (repository *LibraryRepository) CompleteCoverArtArchiveGallery(ctx context.Context, releaseMBID string) error {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return fmt.Errorf("complete cover art archive gallery: %w", err)
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var listedAt *time.Time
		err := tx.NewRaw(`SELECT gallery_listed_at FROM cover_art_archive_release WHERE release_mbid=? FOR UPDATE`, mbid).Scan(ctx, &listedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("complete cover art archive gallery: listing candidates are not recorded")
		}
		if err != nil {
			return fmt.Errorf("complete cover art archive gallery: lock listing: %w", err)
		}
		if listedAt == nil {
			return errors.New("complete cover art archive gallery: listing candidates are not recorded")
		}
		var missing int
		if err := tx.NewRaw(`SELECT count(*) FROM cover_art_archive_image
			WHERE release_mbid=? AND listed AND data IS NULL`, mbid).Scan(ctx, &missing); err != nil {
			return fmt.Errorf("complete cover art archive gallery: inspect downloads: %w", err)
		}
		if missing > 0 {
			return fmt.Errorf("complete cover art archive gallery: %d listed images are not downloaded", missing)
		}
		if _, err := tx.NewRaw(`UPDATE cover_art_archive_release
			SET gallery_status='complete', gallery_checked_at=now(), gallery_error=NULL, updated_at=now()
			WHERE release_mbid=?`, mbid).Exec(ctx); err != nil {
			return fmt.Errorf("complete cover art archive gallery: record completion: %w", err)
		}
		return nil
	})
}

// SaveCoverArtArchiveGallery is the convenience path for a listing whose images
// are already cached: it records candidates and atomically marks them complete.
func (repository *LibraryRepository) SaveCoverArtArchiveGallery(ctx context.Context, releaseMBID string, images []CoverArtArchiveImage) error {
	if err := repository.SaveCoverArtArchiveGalleryCandidates(ctx, releaseMBID, images); err != nil {
		return err
	}
	return repository.CompleteCoverArtArchiveGallery(ctx, releaseMBID)
}

// RecordCoverArtArchiveGalleryError records a failed listing attempt without
// changing the authoritative gallery status or retiring cached images.
func (repository *LibraryRepository) RecordCoverArtArchiveGalleryError(ctx context.Context, releaseMBID, message string) error {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return fmt.Errorf("record cover art archive gallery error: %w", err)
	}
	if strings.TrimSpace(message) == "" {
		return errors.New("record cover art archive gallery error: message is required")
	}
	if _, err := repository.db.NewRaw(`INSERT INTO cover_art_archive_release (release_mbid, gallery_error)
		VALUES (?, ?)
		ON CONFLICT (release_mbid) DO UPDATE SET gallery_error=EXCLUDED.gallery_error, updated_at=now()`, mbid, message).Exec(ctx); err != nil {
		return fmt.Errorf("record cover art archive gallery error: %w", err)
	}
	return nil
}

// SaveCoverArtArchiveFront records a successfully downloaded exact front image
// for a release. imageID may be empty when the provider front endpoint did not
// expose a listing ID. The bytes are validated and digested here.
func (repository *LibraryRepository) SaveCoverArtArchiveFront(ctx context.Context, releaseMBID, imageID string, data []byte, mime string) error {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return fmt.Errorf("save cover art archive front: %w", err)
	}
	var frontImageID any
	if imageID != "" {
		id, ok := canonicalCoverArtArchiveImageID(imageID)
		if !ok {
			return errors.New("save cover art archive front: image ID is invalid")
		}
		frontImageID = id
	}
	mime, digest, size, err := validateCoverArtArchiveBytes(data, mime)
	if err != nil {
		return fmt.Errorf("save cover art archive front: %w", err)
	}
	if _, err := repository.db.NewRaw(`INSERT INTO cover_art_archive_release
		(release_mbid, front_status, front_image_id, front_data, front_mime, front_sha256, front_size_bytes, front_checked_at, front_error)
		VALUES (?, 'present', ?, ?, ?, ?, ?, now(), NULL)
		ON CONFLICT (release_mbid) DO UPDATE SET
			front_status='present', front_image_id=EXCLUDED.front_image_id,
			front_data=EXCLUDED.front_data, front_mime=EXCLUDED.front_mime,
			front_sha256=EXCLUDED.front_sha256, front_size_bytes=EXCLUDED.front_size_bytes,
			front_checked_at=now(), front_error=NULL, updated_at=now()`,
		mbid, frontImageID, data, mime, digest, size).Exec(ctx); err != nil {
		return fmt.Errorf("save cover art archive front: %w", err)
	}
	return nil
}

// MarkCoverArtArchiveFrontAbsent records a definitive provider absence. It
// clears cached front bytes because the exact provider path answered 404.
func (repository *LibraryRepository) MarkCoverArtArchiveFrontAbsent(ctx context.Context, releaseMBID string) error {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return fmt.Errorf("mark cover art archive front absent: %w", err)
	}
	if _, err := repository.db.NewRaw(`INSERT INTO cover_art_archive_release
		(release_mbid, front_status, front_checked_at, front_error)
		VALUES (?, 'absent', now(), NULL)
		ON CONFLICT (release_mbid) DO UPDATE SET
			front_status='absent', front_image_id=NULL, front_data=NULL, front_mime=NULL,
			front_sha256=NULL, front_size_bytes=NULL, front_checked_at=now(), front_error=NULL, updated_at=now()`, mbid).Exec(ctx); err != nil {
		return fmt.Errorf("mark cover art archive front absent: %w", err)
	}
	return nil
}

// RecordCoverArtArchiveFrontError records a failed front attempt without
// changing a known front status or erasing usable front bytes.
func (repository *LibraryRepository) RecordCoverArtArchiveFrontError(ctx context.Context, releaseMBID, message string) error {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return fmt.Errorf("record cover art archive front error: %w", err)
	}
	if strings.TrimSpace(message) == "" {
		return errors.New("record cover art archive front error: message is required")
	}
	if _, err := repository.db.NewRaw(`INSERT INTO cover_art_archive_release (release_mbid, front_error)
		VALUES (?, ?)
		ON CONFLICT (release_mbid) DO UPDATE SET front_error=EXCLUDED.front_error, updated_at=now()`, mbid, message).Exec(ctx); err != nil {
		return fmt.Errorf("record cover art archive front error: %w", err)
	}
	return nil
}

// UpsertCoverArtArchiveImage durably caches bytes for one exact image. It is
// idempotent, creates a minimal listed entry when listing metadata has not
// arrived yet, and never erases existing bytes on a later error.
func (repository *LibraryRepository) UpsertCoverArtArchiveImage(ctx context.Context, releaseMBID, imageID string, data []byte, mime string) error {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return fmt.Errorf("upsert cover art archive image: %w", err)
	}
	id, ok := canonicalCoverArtArchiveImageID(imageID)
	if !ok {
		return errors.New("upsert cover art archive image: image ID is invalid")
	}
	mime, digest, size, err := validateCoverArtArchiveBytes(data, mime)
	if err != nil {
		return fmt.Errorf("upsert cover art archive image: %w", err)
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO cover_art_archive_release (release_mbid) VALUES (?)
			ON CONFLICT (release_mbid) DO NOTHING`, mbid).Exec(ctx); err != nil {
			return fmt.Errorf("upsert cover art archive image: ensure release cache: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO cover_art_archive_image
			(release_mbid, image_id, data, mime, sha256, size_bytes, downloaded_at)
			VALUES (?, ?, ?, ?, ?, ?, now())
			ON CONFLICT (release_mbid, image_id) DO UPDATE SET
				data=EXCLUDED.data, mime=EXCLUDED.mime, sha256=EXCLUDED.sha256,
				size_bytes=EXCLUDED.size_bytes, downloaded_at=now(), download_error=NULL, updated_at=now()`,
			mbid, id, data, mime, digest, size).Exec(ctx); err != nil {
			return fmt.Errorf("upsert cover art archive image: %w", err)
		}
		return nil
	})
}

// SaveCoverArtArchiveImageData is kept as the strict update spelling used by
// callers that already recorded listing candidates.
func (repository *LibraryRepository) SaveCoverArtArchiveImageData(ctx context.Context, releaseMBID, imageID string, data []byte, mime string) error {
	return repository.UpsertCoverArtArchiveImage(ctx, releaseMBID, imageID, data, mime)
}

// RecordCoverArtArchiveImageError records a failed image download without
// changing cached bytes or listing metadata.
func (repository *LibraryRepository) RecordCoverArtArchiveImageError(ctx context.Context, releaseMBID, imageID, message string) error {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return fmt.Errorf("record cover art archive image error: %w", err)
	}
	id, ok := canonicalCoverArtArchiveImageID(imageID)
	if !ok {
		return errors.New("record cover art archive image error: image ID is invalid")
	}
	if strings.TrimSpace(message) == "" {
		return errors.New("record cover art archive image error: message is required")
	}
	result, err := repository.db.NewRaw(`UPDATE cover_art_archive_image
		SET download_error=?, updated_at=now() WHERE release_mbid=? AND image_id=?`, message, mbid, id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("record cover art archive image error: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("record cover art archive image error: inspect update: %w", err)
	}
	if affected == 0 {
		return ErrCoverArtArchiveImageNotFound
	}
	return nil
}

// ReadCoverArtArchiveRelease returns independent front/gallery status and the
// current listed image metadata. Cached image bytes are loaded only through
// ReadCoverArtArchiveImage.
func (repository *LibraryRepository) ReadCoverArtArchiveRelease(ctx context.Context, releaseMBID string) (CoverArtArchiveRelease, error) {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return CoverArtArchiveRelease{}, fmt.Errorf("read cover art archive release: %w", err)
	}
	result := CoverArtArchiveRelease{
		ReleaseMBID:   mbid,
		FrontStatus:   CoverArtArchiveUnchecked,
		GalleryStatus: CoverArtArchiveUnchecked,
		Images:        []CoverArtArchiveImage{},
	}
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`SELECT front_status, front_image_id, front_checked_at, front_error,
			gallery_status, gallery_listed_at, gallery_checked_at, gallery_error
			FROM cover_art_archive_release WHERE release_mbid=?`, mbid).
			Scan(ctx, &result.FrontStatus, &result.FrontImageID, &result.FrontCheckedAt, &result.FrontError,
				&result.GalleryStatus, &result.GalleryListedAt, &result.GalleryCheckedAt, &result.GalleryError)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var rows []struct {
			ID            string          `bun:"image_id"`
			Position      int             `bun:"position"`
			Front         bool            `bun:"front"`
			Back          bool            `bun:"back"`
			Types         json.RawMessage `bun:"types"`
			Comment       string          `bun:"comment"`
			ThumbnailURL  string          `bun:"thumbnail_url"`
			Listed        bool            `bun:"listed"`
			Cached        bool            `bun:"cached"`
			DownloadError *string         `bun:"download_error"`
		}
		if err := tx.NewRaw(`SELECT image_id, position, front, back, types, comment, thumbnail_url, listed,
			(data IS NOT NULL) AS cached, download_error
			FROM cover_art_archive_image WHERE release_mbid=? AND listed
			ORDER BY position, image_id`, mbid).Scan(ctx, &rows); err != nil {
			return err
		}
		for _, row := range rows {
			var types []string
			if len(row.Types) > 0 {
				if err := json.Unmarshal(row.Types, &types); err != nil {
					return err
				}
			}
			result.Images = append(result.Images, CoverArtArchiveImage{
				ID: row.ID, Position: row.Position, Front: row.Front, Back: row.Back,
				Types: nonNilStrings(types), Comment: row.Comment, ThumbnailURL: row.ThumbnailURL,
				Listed: row.Listed, Cached: row.Cached, DownloadError: row.DownloadError,
			})
		}
		return nil
	})
	if err != nil {
		return CoverArtArchiveRelease{}, fmt.Errorf("read cover art archive release: %w", err)
	}
	return result, nil
}

// ReadCoverArtArchiveFront returns the cached front bytes, if any.
func (repository *LibraryRepository) ReadCoverArtArchiveFront(ctx context.Context, releaseMBID string) (CoverArtArchiveFront, error) {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return CoverArtArchiveFront{}, fmt.Errorf("read cover art archive front: %w", err)
	}
	result := CoverArtArchiveFront{Status: CoverArtArchiveUnchecked}
	err = repository.db.NewRaw(`SELECT front_status, front_image_id, front_data, front_mime, front_sha256,
		front_size_bytes, front_checked_at, front_error
		FROM cover_art_archive_release WHERE release_mbid=?`, mbid).
		Scan(ctx, &result.Status, &result.ImageID, &result.Data, &result.MIME, &result.SHA256,
			&result.SizeBytes, &result.CheckedAt, &result.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return CoverArtArchiveFront{}, fmt.Errorf("read cover art archive front: %w", err)
	}
	result.Checked = result.Status != CoverArtArchiveUnchecked
	result.Found = result.Status == CoverArtArchiveFrontPresent
	return result, validateCoverArtArchiveFront(result)
}

// ReadCoverArtArchiveImage returns one image and its cached bytes, if any.
func (repository *LibraryRepository) ReadCoverArtArchiveImage(ctx context.Context, releaseMBID, imageID string) (CoverArtArchiveImage, error) {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return CoverArtArchiveImage{}, fmt.Errorf("read cover art archive image: %w", err)
	}
	id, ok := canonicalCoverArtArchiveImageID(imageID)
	if !ok {
		return CoverArtArchiveImage{}, errors.New("read cover art archive image: image ID is invalid")
	}
	var result CoverArtArchiveImage
	var types json.RawMessage
	err = repository.db.NewRaw(`SELECT image_id, position, front, back, types, comment, thumbnail_url, listed,
		(data IS NOT NULL) AS cached, data, mime, sha256, size_bytes, downloaded_at, download_error
		FROM cover_art_archive_image WHERE release_mbid=? AND image_id=?`, mbid, id).
		Scan(ctx, &result.ID, &result.Position, &result.Front, &result.Back, &types, &result.Comment,
			&result.ThumbnailURL, &result.Listed, &result.Cached, &result.Data, &result.MIME,
			&result.SHA256, &result.SizeBytes, &result.DownloadedAt, &result.DownloadError)
	if errors.Is(err, sql.ErrNoRows) {
		return CoverArtArchiveImage{}, ErrCoverArtArchiveImageNotFound
	}
	if err != nil {
		return CoverArtArchiveImage{}, fmt.Errorf("read cover art archive image: %w", err)
	}
	if len(types) > 0 {
		if err := json.Unmarshal(types, &result.Types); err != nil {
			return CoverArtArchiveImage{}, fmt.Errorf("read cover art archive image: decode types: %w", err)
		}
	}
	result.Types = nonNilStrings(result.Types)
	if err := validateCoverArtArchiveCachedImage(result.Data, result.MIME, result.SHA256, result.SizeBytes); err != nil {
		return CoverArtArchiveImage{}, fmt.Errorf("read cover art archive image: %w", err)
	}
	return result, nil
}

// CoverArtArchiveGalleryEligible reports whether the exact release MBID is
// bound to a local release with at least one live track_source association.
// Merely having a local release is not enough; provider-only discography
// entries are projected separately for front thumbnails.
func (repository *LibraryRepository) CoverArtArchiveGalleryEligible(ctx context.Context, releaseMBID string) (bool, error) {
	mbid, err := canonicalCoverArtArchiveMBID(releaseMBID)
	if err != nil {
		return false, fmt.Errorf("read cover art archive gallery eligibility: %w", err)
	}
	var eligible bool
	err = repository.db.NewRaw(`SELECT EXISTS (
		SELECT 1
		FROM provider_release release
		JOIN provider ON provider.id=release.provider_id AND provider.code='musicbrainz'
		JOIN provider_local_release binding
			ON binding.provider_id=release.provider_id AND binding.provider_release_id=release.id
		WHERE lower(release.provider_key)=?
			AND EXISTS (
				SELECT 1 FROM release_track position
				JOIN track_source association ON association.track_id=position.track_id
				WHERE position.release_id=binding.local_release_id
			)
	)`, mbid).Scan(ctx, &eligible)
	if err != nil {
		return false, fmt.Errorf("read cover art archive gallery eligibility: %w", err)
	}
	return eligible, nil
}

// ReadLibraryArtistCoverArtReleaseMBIDs projects exact release MBIDs whose
// Cover Art Archive front may be shown for one visible library artist. The
// union is:
//   - every release entity in a complete, current-configuration cached
//     MusicBrainz discography linked to the artist;
//   - every exact MusicBrainz release binding of the artist's local releases
//     that has at least one live track_source association.
//
// The result is deduplicated, canonical and sorted. A complete cached
// discography is required only for the provider branch; a source-linked local
// release remains eligible without it.
func (repository *LibraryRepository) ReadLibraryArtistCoverArtReleaseMBIDs(ctx context.Context, artistID uuid.UUID) ([]string, error) {
	if artistID == uuid.Nil {
		return nil, errors.New("read library artist cover art release MBIDs: artist is required")
	}
	var rows []string
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`WITH provider_releases AS (
			SELECT DISTINCT lower(entity.provider_key) AS release_mbid
			FROM local_artist requested
			JOIN provider_local_artist link ON link.local_artist_id=requested.id
			JOIN provider ON provider.id=link.provider_id AND provider.code='musicbrainz'
			JOIN provider_artist artist ON artist.id=link.provider_artist_id AND artist.provider_id=link.provider_id
			JOIN provider_artist_discography completion
				ON completion.provider_id=artist.provider_id
				AND completion.artist_mbid::text=lower(artist.provider_key)
			JOIN provider_source source
				ON source.id=completion.provider_source_id
				AND source.provider_id=completion.provider_id
				AND source.configuration_identity=completion.configuration_identity
			JOIN app_setting config
				ON config.setting_name='musicbrainz_config_identity'
				AND config.setting_value=completion.configuration_identity
			JOIN provider_source_entity entity
				ON entity.provider_source_id=completion.provider_source_id
				AND entity.provider_id=completion.provider_id
				AND entity.entity_kind='release'
			WHERE requested.id=? AND requested.catalog_visible IS TRUE
				AND jsonb_exists(entity.field_evidence,'artist-credit')
				AND EXISTS (
					SELECT 1 FROM jsonb_array_elements(COALESCE(entity.fields->'artist-credit','[]'::jsonb)) credit
					WHERE lower(COALESCE(credit#>>'{artist,id}', credit->>'artist'))=lower(artist.provider_key)
				)
		), local_releases AS (
			SELECT DISTINCT lower(provider_release.provider_key) AS release_mbid
			FROM local_artist requested
			JOIN release_artist credit ON credit.artist_id=requested.id
			JOIN local_release release ON release.id=credit.release_id
			JOIN provider_local_release binding ON binding.local_release_id=release.id
			JOIN provider ON provider.id=binding.provider_id AND provider.code='musicbrainz'
			JOIN provider_release ON provider_release.id=binding.provider_release_id
				AND provider_release.provider_id=binding.provider_id
			WHERE requested.id=? AND requested.catalog_visible IS TRUE
				AND EXISTS (
					SELECT 1 FROM release_track position
					JOIN track_source association ON association.track_id=position.track_id
					WHERE position.release_id=release.id
				)
		)
		SELECT release_mbid FROM (
			SELECT release_mbid FROM provider_releases
			UNION
			SELECT release_mbid FROM local_releases
		) candidates
		WHERE release_mbid ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
		ORDER BY release_mbid`, artistID, artistID).Scan(ctx, &rows)
	})
	if err != nil {
		return nil, fmt.Errorf("read library artist cover art release MBIDs: %w", err)
	}
	result := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		mbid, err := canonicalCoverArtArchiveMBID(row)
		if err != nil {
			continue
		}
		if _, duplicate := seen[mbid]; duplicate {
			continue
		}
		seen[mbid] = struct{}{}
		result = append(result, mbid)
	}
	return result, nil
}

func canonicalCoverArtArchiveMBID(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	parsed, err := uuid.Parse(trimmed)
	if err != nil || parsed == uuid.Nil {
		return "", errors.New("release MBID is invalid")
	}
	return parsed.String(), nil
}

func canonicalCoverArtArchiveImageID(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != value {
		return "", false
	}
	return value, true
}

func validateCoverArtArchiveBytes(data []byte, mime string) (string, []byte, int64, error) {
	if len(data) == 0 {
		return "", nil, 0, errors.New("image bytes are required")
	}
	mime = strings.ToLower(strings.TrimSpace(mime))
	if mime != "image/png" && mime != "image/jpeg" {
		return "", nil, 0, errors.New("image MIME type is unsupported")
	}
	digest := sha256.Sum256(data)
	return mime, digest[:], int64(len(data)), nil
}

func validateCoverArtArchiveFront(front CoverArtArchiveFront) error {
	switch front.Status {
	case CoverArtArchiveUnchecked:
		if front.Data != nil || front.MIME != nil || front.SHA256 != nil || front.SizeBytes != nil || front.CheckedAt != nil {
			return errors.New("unchecked front contains image data")
		}
	case CoverArtArchiveFrontAbsent:
		if front.Data != nil || front.MIME != nil || front.SHA256 != nil || front.SizeBytes != nil || front.CheckedAt == nil {
			return errors.New("absent front contains image data or no check time")
		}
	case CoverArtArchiveFrontPresent:
		if err := validateCoverArtArchiveCachedImage(front.Data, front.MIME, front.SHA256, front.SizeBytes); err != nil {
			return err
		}
		if front.CheckedAt == nil {
			return errors.New("present front has no check time")
		}
	default:
		return errors.New("front status is invalid")
	}
	return nil
}

func validateCoverArtArchiveCachedImage(data []byte, mime *string, digest []byte, size *int64) error {
	if data == nil {
		if mime != nil || digest != nil || size != nil {
			return errors.New("cached image tuple is incomplete")
		}
		return nil
	}
	if len(data) == 0 || mime == nil || (*mime != "image/png" && *mime != "image/jpeg") ||
		len(digest) != sha256.Size || size == nil || *size != int64(len(data)) {
		return errors.New("cached image tuple is invalid")
	}
	computed := sha256.Sum256(data)
	if !bytes.Equal(computed[:], digest) {
		return errors.New("cached image digest does not match its bytes")
	}
	return nil
}
