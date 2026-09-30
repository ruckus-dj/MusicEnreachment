package service_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type sourceLocationRepositoryFixture struct {
	root      *persistence.SourceRoot
	locations []persistence.SourceLocation
	limit     int
	cursor    *persistence.SourceLocationCursor
	reads     int
}

func (fixture *sourceLocationRepositoryFixture) GetSourceRoot(_ context.Context, id uuid.UUID) (*persistence.SourceRoot, error) {
	if fixture.root == nil || fixture.root.ID != id {
		return nil, fmt.Errorf("get source root: %w", sql.ErrNoRows)
	}
	return fixture.root, nil
}

func (fixture *sourceLocationRepositoryFixture) ListSourceLocationsPage(_ context.Context, _ uuid.UUID, cursor *persistence.SourceLocationCursor, limit int) ([]persistence.SourceLocation, *persistence.SourceLocationCursor, error) {
	fixture.reads++
	fixture.cursor, fixture.limit = cursor, limit
	ordered := slices.Clone(fixture.locations)
	slices.SortFunc(ordered, func(first, second persistence.SourceLocation) int {
		if first.RelativePath != second.RelativePath {
			return strings.Compare(first.RelativePath, second.RelativePath)
		}
		return strings.Compare(first.ID.String(), second.ID.String())
	})
	page := make([]persistence.SourceLocation, 0, limit)
	for _, location := range ordered {
		if cursor != nil {
			if location.RelativePath < cursor.RelativePath ||
				(location.RelativePath == cursor.RelativePath && strings.Compare(location.ID.String(), cursor.ID.String()) <= 0) {
				continue
			}
		}
		page = append(page, location)
		if len(page) == limit {
			break
		}
	}
	if len(page) < limit {
		return page, nil, nil
	}
	last := page[len(page)-1]
	return page, &persistence.SourceLocationCursor{RelativePath: last.RelativePath, ID: last.ID}, nil
}

func newSourceLocationsFixture(t *testing.T, paths ...string) (*service.SourceLocations, *sourceLocationRepositoryFixture, *persistence.SourceRoot) {
	t.Helper()
	root := &persistence.SourceRoot{ID: uuid.New(), DisplayName: "Music", ConfiguredPath: "/music", Enabled: true, Status: persistence.SourceRootStatusAvailable}
	mtime := time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC)
	locations := make([]persistence.SourceLocation, 0, len(paths))
	for _, path := range paths {
		locations = append(locations, persistence.SourceLocation{
			ID: uuid.New(), SourceRootID: root.ID, RelativePath: path, SizeBytes: 128,
			Mtime: mtime, ProbeStatus: persistence.SourceProbeStatusAudio,
		})
	}
	fixture := &sourceLocationRepositoryFixture{root: root, locations: locations}
	return service.NewSourceLocations(fixture), fixture, root
}

func TestSourceLocationListPaginatesByTheLastRowItReturned(t *testing.T) {
	locations, repository, root := newSourceLocationsFixture(t, "a.flac", "b.flac", "c.flac")

	first, err := locations.List(context.Background(), root.ID, "", 2)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if repository.cursor != nil {
		t.Fatalf("the first page passed a cursor to the store: %+v", repository.cursor)
	}
	if len(first.Locations) != 2 || first.Locations[0].RelativePath != "a.flac" || first.Locations[1].RelativePath != "b.flac" {
		t.Fatalf("first page = %+v", first)
	}
	if first.NextCursor == "" {
		t.Fatal("a full page did not return a cursor")
	}
	if first.Locations[0].ID == uuid.Nil || first.Locations[0].ProbeStatus != persistence.SourceProbeStatusAudio {
		t.Fatalf("first page lost a location field: %+v", first.Locations[0])
	}

	second, err := locations.List(context.Background(), root.ID, first.NextCursor, 2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if repository.cursor == nil || repository.cursor.RelativePath != "b.flac" || repository.cursor.ID != first.Locations[1].ID {
		t.Fatalf("cursor passed to the store = %+v, want the last row of the first page", repository.cursor)
	}
	if len(second.Locations) != 1 || second.Locations[0].RelativePath != "c.flac" || second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestSourceLocationListRejectsAForeignCursor(t *testing.T) {
	locations, repository, root := newSourceLocationsFixture(t, "a.flac")
	for _, test := range []struct {
		name   string
		cursor string
	}{
		{"not base64", "not-base64!"},
		{"no separator", base64.RawURLEncoding.EncodeToString([]byte("a.flac"))},
		{"invalid identifier", base64.RawURLEncoding.EncodeToString([]byte("a.flac\x00not-a-uuid"))},
		{"empty path", base64.RawURLEncoding.EncodeToString([]byte("\x00" + uuid.NewString()))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := locations.List(context.Background(), root.ID, test.cursor, 10); !errors.Is(err, service.ErrSourceLocationCursor) {
				t.Fatalf("cursor %q accepted or reported wrongly: %v", test.cursor, err)
			}
		})
	}
	if repository.reads != 0 {
		t.Fatalf("a foreign cursor reached the store %d times", repository.reads)
	}
}

func TestSourceLocationListReportsAMissingRoot(t *testing.T) {
	locations, _, root := newSourceLocationsFixture(t, "a.flac")
	if _, err := locations.List(context.Background(), uuid.New(), "", 10); !errors.Is(err, service.ErrSourceRootNotFound) {
		t.Fatalf("unknown root reported as %v", err)
	}
	if !service.IsSourceRootNotFound(fmt.Errorf("list source locations: %w", service.ErrSourceRootNotFound)) {
		t.Fatal("the not-found sentinel was not recognized through its wrapping")
	}
	if service.IsSourceRootNotFound(fmt.Errorf("list source locations: the database is unavailable")) {
		t.Fatal("a store failure was classified as a missing root")
	}
	if _, err := locations.List(context.Background(), root.ID, "", 10); err != nil {
		t.Fatalf("existing root: %v", err)
	}
}

func TestSourceLocationListUsesTheDefaultPageSize(t *testing.T) {
	locations, repository, root := newSourceLocationsFixture(t, "a.flac")
	if _, err := locations.List(context.Background(), root.ID, "", 0); err != nil {
		t.Fatalf("default page size: %v", err)
	}
	if repository.limit != service.SourceLocationsDefaultPageSize {
		t.Fatalf("page size = %d, want %d", repository.limit, service.SourceLocationsDefaultPageSize)
	}
}
