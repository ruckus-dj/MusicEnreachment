package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// SourceLocationsDefaultPageSize is the page a location list serves when its
// caller names no limit. The HTTP boundary validates a requested limit against
// its own schema; this is the fallback of a caller that passes none.
const SourceLocationsDefaultPageSize = 50

var (
	// ErrSourceLocationCursor reports a pagination cursor that is not one this
	// API issued. A cursor is opaque to the caller, so a value the service
	// cannot decode is a malformed request, not a store failure.
	ErrSourceLocationCursor = errors.New("source location cursor is invalid")
	// ErrSourceRootNotFound reports a source root the caller named and the store
	// does not hold.
	ErrSourceRootNotFound = errors.New("source root not found")
)

// IsSourceRootNotFound reports whether reading a source root failed because no
// such root exists. A repository reports a missing row with sql.ErrNoRows and
// wraps it with the read it was serving; the sentinel covers a root this package
// already classified itself.
func IsSourceRootNotFound(err error) bool {
	return errors.Is(err, ErrSourceRootNotFound) || errors.Is(err, sql.ErrNoRows)
}

// SourceLocationRepository is the persistence contract of a location list. The
// page is ordered by (relative_path, id) and the cursor is the row the previous
// page ended on.
type SourceLocationRepository interface {
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
	ListSourceLocationsPage(context.Context, uuid.UUID, *persistence.SourceLocationCursor, int) ([]persistence.SourceLocation, *persistence.SourceLocationCursor, error)
}

// SourceLocation is one file of the last successful inventory of a root. It
// describes the published inventory alone: the candidates of a scan that has not
// been applied are never part of it.
type SourceLocation struct {
	ID           uuid.UUID
	RelativePath string
	SizeBytes    int64
	Mtime        time.Time
	ProbeStatus  string
	SafeError    *string
	// MediaVariantID is the stored result of the last successful analysis of
	// this file, or nil when it was never analyzed or its inventory changed
	// since. The list carries the identifier alone; the technical result itself
	// is only served by the location detail endpoint.
	MediaVariantID *uuid.UUID
}

// SourceLocationPage is one page of the last successful inventory of a root. An
// empty NextCursor means the page is the last one.
type SourceLocationPage struct {
	Locations  []SourceLocation
	NextCursor string
}

// SourceLocations reads the inventory a root published. It never walks a
// directory, never counts candidates and never writes: it serves the locations
// of the last successful scan.
type SourceLocations struct {
	repository SourceLocationRepository
}

func NewSourceLocations(repository SourceLocationRepository) *SourceLocations {
	return &SourceLocations{repository: repository}
}

// List returns one page of the last successful inventory of a root, ordered by
// exact relative path. The cursor is the opaque value the previous page
// returned: a caller passes it back unchanged and never constructs one. A limit
// of zero or less falls back to the default page size.
//
// A root that does not exist is reported with ErrSourceRootNotFound, and a
// cursor that is not one this API issued with ErrSourceLocationCursor.
func (s *SourceLocations) List(ctx context.Context, rootID uuid.UUID, cursor string, limit int) (SourceLocationPage, error) {
	if limit <= 0 {
		limit = SourceLocationsDefaultPageSize
	}
	if _, err := s.repository.GetSourceRoot(ctx, rootID); err != nil {
		if IsSourceRootNotFound(err) {
			return SourceLocationPage{}, fmt.Errorf("list source locations: %w", ErrSourceRootNotFound)
		}
		return SourceLocationPage{}, fmt.Errorf("list source locations: %w", err)
	}
	decoded, err := decodeSourceLocationCursor(cursor)
	if err != nil {
		return SourceLocationPage{}, err
	}
	locations, next, err := s.repository.ListSourceLocationsPage(ctx, rootID, decoded, limit)
	if err != nil {
		return SourceLocationPage{}, fmt.Errorf("list source locations: %w", err)
	}
	page := SourceLocationPage{Locations: make([]SourceLocation, 0, len(locations))}
	for index := range locations {
		page.Locations = append(page.Locations, SourceLocation{
			ID: locations[index].ID, RelativePath: locations[index].RelativePath,
			SizeBytes: locations[index].SizeBytes, Mtime: locations[index].Mtime,
			ProbeStatus: locations[index].ProbeStatus, SafeError: locations[index].SafeError,
			MediaVariantID: locations[index].MediaVariantID,
		})
	}
	if next != nil {
		page.NextCursor = encodeSourceLocationCursor(*next)
	}
	return page, nil
}

// decodeSourceLocationCursor turns the opaque cursor of a previous page back
// into the ordering key it carries, and an empty cursor into the first page. The
// path and the ID are separated by a NUL byte, which no file name contains, and
// the whole key travels base64url encoded so a caller cannot read or forge the
// ordering columns.
func decodeSourceLocationCursor(value string) (*persistence.SourceLocationCursor, error) {
	if value == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode source location cursor: %w", ErrSourceLocationCursor)
	}
	path, id, found := strings.Cut(string(raw), "\x00")
	if !found || path == "" {
		return nil, fmt.Errorf("open source location cursor: %w", ErrSourceLocationCursor)
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("open source location cursor: %w", ErrSourceLocationCursor)
	}
	return &persistence.SourceLocationCursor{RelativePath: path, ID: parsed}, nil
}

func encodeSourceLocationCursor(cursor persistence.SourceLocationCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursor.RelativePath + "\x00" + cursor.ID.String()))
}
