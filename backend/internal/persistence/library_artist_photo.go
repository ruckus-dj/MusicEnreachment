package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	// MaxLibraryArtistPhotoBytes bounds one stored artist thumbnail. The
	// provider request already asks for a small tile; this is a defensive cap.
	MaxLibraryArtistPhotoBytes = 1 << 20
	// MaxLibraryArtistPhotoDimension bounds both stored thumbnail axes. The
	// provider is asked for a 330-pixel tile; 400 also covers a small original.
	MaxLibraryArtistPhotoDimension = 400
)

var ErrLibraryArtistPhotoNotFound = errors.New("library artist photo not found")

// LibraryArtistPhotoRecord is one durable, verified artist photograph. The
// identity is the exact MusicBrainz artist MBID and the MusicBrainz
// configuration identity that produced the attribution, not a mutable local
// artist row.
type LibraryArtistPhotoRecord struct {
	ConfigIdentity string
	ArtistMBID     uuid.UUID
	ImageURL       string
	SourceURL      string
	Author         string
	License        string
	LicenseURL     *string
	Data           []byte
	MIME           string
	SHA256         []byte
	SizeBytes      int64
	Width          int
	Height         int
	FetchedAt      time.Time
}

// SaveLibraryArtistPhoto validates and stores one bounded thumbnail together
// with its verified attribution. Re-saving the same identity replaces the tile
// and attribution atomically.
func (repository *LibraryRepository) SaveLibraryArtistPhoto(ctx context.Context, record LibraryArtistPhotoRecord) error {
	normalized, err := validateLibraryArtistPhotoRecord(record, false)
	if err != nil {
		return fmt.Errorf("save library artist photo: %w", err)
	}
	if _, err := repository.db.NewRaw(`INSERT INTO library_artist_photo
		(config_identity, artist_mbid, image_url, source_url, author, license, license_url,
		 image_data, image_mime, image_sha256, image_size_bytes, image_width, image_height, fetched_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, now())
		ON CONFLICT (config_identity, artist_mbid) DO UPDATE SET
			image_url=EXCLUDED.image_url, source_url=EXCLUDED.source_url,
			author=EXCLUDED.author, license=EXCLUDED.license, license_url=EXCLUDED.license_url,
			image_data=EXCLUDED.image_data, image_mime=EXCLUDED.image_mime,
			image_sha256=EXCLUDED.image_sha256, image_size_bytes=EXCLUDED.image_size_bytes,
			image_width=EXCLUDED.image_width, image_height=EXCLUDED.image_height,
			fetched_at=EXCLUDED.fetched_at, updated_at=now()`,
		normalized.ConfigIdentity, normalized.ArtistMBID, normalized.ImageURL, normalized.SourceURL,
		normalized.Author, normalized.License, normalized.LicenseURL,
		normalized.Data, normalized.MIME, normalized.SHA256, normalized.SizeBytes,
		normalized.Width, normalized.Height, normalized.FetchedAt).Exec(ctx); err != nil {
		return fmt.Errorf("save library artist photo: %w", err)
	}
	return nil
}

// ReadLibraryArtistPhoto returns one stored photo by exact provider identity.
func (repository *LibraryRepository) ReadLibraryArtistPhoto(ctx context.Context, configIdentity, artistMBID string) (LibraryArtistPhotoRecord, error) {
	identity, err := canonicalArtistPhotoConfigIdentity(configIdentity)
	if err != nil {
		return LibraryArtistPhotoRecord{}, fmt.Errorf("read library artist photo: %w", err)
	}
	mbid, err := canonicalArtistPhotoMBID(artistMBID)
	if err != nil {
		return LibraryArtistPhotoRecord{}, fmt.Errorf("read library artist photo: %w", err)
	}
	record := LibraryArtistPhotoRecord{ConfigIdentity: identity, ArtistMBID: mbid}
	err = repository.db.NewRaw(`SELECT image_url, source_url, author, license, license_url,
		image_data, image_mime, image_sha256, image_size_bytes, image_width, image_height, fetched_at
		FROM library_artist_photo WHERE config_identity=? AND artist_mbid=?`, identity, mbid).
		Scan(ctx, &record.ImageURL, &record.SourceURL, &record.Author, &record.License, &record.LicenseURL,
			&record.Data, &record.MIME, &record.SHA256, &record.SizeBytes, &record.Width, &record.Height, &record.FetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryArtistPhotoRecord{}, ErrLibraryArtistPhotoNotFound
	}
	if err != nil {
		return LibraryArtistPhotoRecord{}, fmt.Errorf("read library artist photo: %w", err)
	}
	if _, err := validateLibraryArtistPhotoRecord(record, true); err != nil {
		return LibraryArtistPhotoRecord{}, fmt.Errorf("read library artist photo: %w", err)
	}
	return record, nil
}

func canonicalArtistPhotoConfigIdentity(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 512 || strings.ContainsRune(value, 0) {
		return "", errors.New("config identity is invalid")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", errors.New("config identity is invalid")
		}
	}
	return value, nil
}

func canonicalArtistPhotoMBID(value string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil || parsed == uuid.Nil || !strings.EqualFold(strings.TrimSpace(value), parsed.String()) {
		return uuid.Nil, errors.New("artist MBID is invalid")
	}
	return parsed, nil
}

func validateLibraryArtistPhotoRecord(record LibraryArtistPhotoRecord, verifyDigest bool) (LibraryArtistPhotoRecord, error) {
	identity, err := canonicalArtistPhotoConfigIdentity(record.ConfigIdentity)
	if err != nil {
		return LibraryArtistPhotoRecord{}, err
	}
	if record.ArtistMBID == uuid.Nil {
		return LibraryArtistPhotoRecord{}, errors.New("artist MBID is invalid")
	}
	imageURL, ok := canonicalArtistPhotoImageURL(record.ImageURL)
	if !ok {
		return LibraryArtistPhotoRecord{}, errors.New("image URL is invalid")
	}
	sourceURL, ok := canonicalArtistPhotoSourceURL(record.SourceURL)
	if !ok {
		return LibraryArtistPhotoRecord{}, errors.New("source URL is invalid")
	}
	author, err := boundedArtistPhotoText(record.Author, 512, false)
	if err != nil {
		return LibraryArtistPhotoRecord{}, fmt.Errorf("author is invalid: %w", err)
	}
	license, err := boundedArtistPhotoText(record.License, 512, true)
	if err != nil {
		return LibraryArtistPhotoRecord{}, fmt.Errorf("license is invalid: %w", err)
	}
	licenseURL, ok := canonicalArtistPhotoLicenseURL(record.LicenseURL)
	if record.LicenseURL != nil && !ok {
		return LibraryArtistPhotoRecord{}, errors.New("license URL is invalid")
	}
	mime := strings.ToLower(strings.TrimSpace(record.MIME))
	switch mime {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
	default:
		return LibraryArtistPhotoRecord{}, errors.New("image MIME type is unsupported")
	}
	if len(record.Data) == 0 || len(record.Data) > MaxLibraryArtistPhotoBytes {
		return LibraryArtistPhotoRecord{}, errors.New("image size is unsupported")
	}
	if record.Width <= 0 || record.Height <= 0 ||
		record.Width > MaxLibraryArtistPhotoDimension || record.Height > MaxLibraryArtistPhotoDimension {
		return LibraryArtistPhotoRecord{}, errors.New("image dimensions are unsupported")
	}
	digest := sha256.Sum256(record.Data)
	if verifyDigest {
		if len(record.SHA256) != sha256.Size || !bytes.Equal(record.SHA256, digest[:]) {
			return LibraryArtistPhotoRecord{}, errors.New("image digest does not match its bytes")
		}
		if record.SizeBytes != int64(len(record.Data)) {
			return LibraryArtistPhotoRecord{}, errors.New("image size does not match its bytes")
		}
	}
	normalized := record
	normalized.ConfigIdentity = identity
	normalized.ImageURL = imageURL
	normalized.SourceURL = sourceURL
	normalized.Author = author
	normalized.License = license
	normalized.LicenseURL = licenseURL
	normalized.MIME = mime
	normalized.SHA256 = digest[:]
	normalized.SizeBytes = int64(len(record.Data))
	if normalized.FetchedAt.IsZero() {
		normalized.FetchedAt = time.Now().UTC()
	}
	return normalized, nil
}

func boundedArtistPhotoText(value string, limit int, required bool) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return "", errors.New("value is required")
		}
		return "", nil
	}
	if len([]rune(trimmed)) > limit {
		return "", errors.New("value is too long")
	}
	for _, character := range trimmed {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return "", errors.New("value contains control characters")
		}
	}
	return trimmed, nil
}

func canonicalArtistPhotoImageURL(raw string) (string, bool) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value.Scheme != "https" || (value.Port() != "" && value.Port() != "443") ||
		value.User != nil || value.RawQuery != "" || value.Fragment != "" || path.Clean(value.Path) != value.Path {
		return "", false
	}
	if !strings.HasPrefix(value.Path, "/wikipedia/commons/") {
		return "", false
	}
	switch strings.ToLower(value.Hostname()) {
	case "upload.wikimedia.org":
		return value.String(), true
	case "thumb.wikimedia.org":
		if strings.HasPrefix(value.Path, "/wikipedia/commons/thumb/") {
			return value.String(), true
		}
	}
	return "", false
}

func canonicalArtistPhotoSourceURL(raw string) (string, bool) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value.Scheme != "https" || (value.Port() != "" && value.Port() != "443") ||
		!strings.EqualFold(value.Hostname(), "commons.wikimedia.org") ||
		value.User != nil || value.RawQuery != "" || value.Fragment != "" || path.Clean(value.Path) != value.Path {
		return "", false
	}
	const prefix = "/wiki/File:"
	if !strings.HasPrefix(value.Path, prefix) || len(value.Path) <= len(prefix) {
		return "", false
	}
	return value.String(), true
}

func canonicalArtistPhotoLicenseURL(raw *string) (*string, bool) {
	if raw == nil {
		return nil, true
	}
	value, err := url.Parse(strings.TrimSpace(*raw))
	if err != nil || value.Scheme != "https" || (value.Port() != "" && value.Port() != "443") ||
		!strings.EqualFold(value.Hostname(), "creativecommons.org") ||
		value.User != nil || value.RawQuery != "" || value.Fragment != "" {
		return nil, false
	}
	if !strings.HasPrefix(value.Path, "/licenses/") && !strings.HasPrefix(value.Path, "/publicdomain/") {
		return nil, false
	}
	normalized := value.String()
	return &normalized, true
}
