package service_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type genreCatalogPageReaderTest struct {
	pages []musicbrainz.GenrePage
	err   error
	calls []int
}

func (r *genreCatalogPageReaderTest) GenrePage(_ context.Context, offset, limit int) (musicbrainz.GenrePage, error) {
	r.calls = append(r.calls, offset)
	if limit != 100 {
		return musicbrainz.GenrePage{}, fmt.Errorf("limit = %d", limit)
	}
	if r.err != nil {
		return musicbrainz.GenrePage{}, r.err
	}
	index := len(r.calls) - 1
	if index >= len(r.pages) {
		return musicbrainz.GenrePage{}, errors.New("unexpected page request")
	}
	return r.pages[index], nil
}

func genreCatalogTestItems(start, count int) []musicbrainz.Genre {
	items := make([]musicbrainz.Genre, count)
	for i := range items {
		items[i] = musicbrainz.Genre{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", start+i+1), Name: "ambient"}
	}
	return items
}

func TestLoadGenreCatalogTraversesEveryFullPage(t *testing.T) {
	reader := &genreCatalogPageReaderTest{pages: []musicbrainz.GenrePage{
		{Count: 101, Offset: 0, Genres: genreCatalogTestItems(0, 100)},
		{Count: 101, Offset: 100, Genres: genreCatalogTestItems(100, 1)},
	}}
	items, err := service.LoadGenreCatalog(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 101 || len(reader.calls) != 2 || reader.calls[0] != 0 || reader.calls[1] != 100 {
		t.Fatalf("items=%d calls=%v", len(items), reader.calls)
	}
	if items[0].SourceName != "ambient" || items[0].Name != "Ambient" {
		t.Fatalf("first item = %+v", items[0])
	}
}

func TestLoadGenreCatalogPreservesExactSourceName(t *testing.T) {
	reader := &genreCatalogPageReaderTest{pages: []musicbrainz.GenrePage{{
		Count: 1, Offset: 0,
		Genres: []musicbrainz.Genre{{ID: "00000000-0000-4000-8000-000000000001", Name: "  ambient  "}},
	}}}
	items, err := service.LoadGenreCatalog(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SourceName != "  ambient  " || items[0].Name != "  Ambient  " {
		t.Fatalf("items = %+v, want exact source spelling and separator-preserving display name", items)
	}
}

func TestLoadGenreCatalogEmptyAndPartialFinalPages(t *testing.T) {
	for _, tc := range []struct {
		name string
		page musicbrainz.GenrePage
	}{
		{name: "empty", page: musicbrainz.GenrePage{Count: 0, Offset: 0, Genres: []musicbrainz.Genre{}}},
		{name: "partial final", page: musicbrainz.GenrePage{Count: 3, Offset: 0, Genres: genreCatalogTestItems(0, 3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &genreCatalogPageReaderTest{pages: []musicbrainz.GenrePage{tc.page}}
			items, err := service.LoadGenreCatalog(context.Background(), reader)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != len(tc.page.Genres) || len(reader.calls) != 1 {
				t.Fatalf("items=%d calls=%v", len(items), reader.calls)
			}
		})
	}
}

func TestLoadGenreCatalogRejectsInvalidPagesWithoutReturningPartialData(t *testing.T) {
	valid := genreCatalogTestItems(0, 1)
	for _, tc := range []struct {
		name  string
		pages []musicbrainz.GenrePage
		err   error
	}{
		{name: "missing page", pages: []musicbrainz.GenrePage{{Count: 101, Offset: 0, Genres: genreCatalogTestItems(0, 100)}}},
		{name: "count changes", pages: []musicbrainz.GenrePage{{Count: 101, Offset: 0, Genres: genreCatalogTestItems(0, 100)}, {Count: 102, Offset: 100, Genres: genreCatalogTestItems(100, 1)}}},
		{name: "wrong offset", pages: []musicbrainz.GenrePage{{Count: 1, Offset: 1, Genres: valid}}},
		{name: "negative count", pages: []musicbrainz.GenrePage{{Count: -1, Offset: 0, Genres: []musicbrainz.Genre{}}}},
		{name: "missing genres", pages: []musicbrainz.GenrePage{{Count: 0, Offset: 0}}},
		{name: "null id", pages: []musicbrainz.GenrePage{{Count: 1, Offset: 0, Genres: []musicbrainz.Genre{{ID: "00000000-0000-0000-0000-000000000000", Name: "ambient"}}}}},
		{name: "non UUID", pages: []musicbrainz.GenrePage{{Count: 1, Offset: 0, Genres: []musicbrainz.Genre{{ID: "not-a-uuid", Name: "ambient"}}}}},
		{name: "blank name", pages: []musicbrainz.GenrePage{{Count: 1, Offset: 0, Genres: []musicbrainz.Genre{{ID: "00000000-0000-4000-8000-000000000001", Name: " \t "}}}}},
		{name: "count overrun", pages: []musicbrainz.GenrePage{{Count: 1, Offset: 0, Genres: genreCatalogTestItems(0, 2)}}},
		{name: "oversized page", pages: []musicbrainz.GenrePage{{Count: 101, Offset: 0, Genres: genreCatalogTestItems(0, 101)}}},
		{name: "duplicate", pages: []musicbrainz.GenrePage{{Count: 2, Offset: 0, Genres: []musicbrainz.Genre{valid[0], valid[0]}}}},
		{name: "provider error", pages: []musicbrainz.GenrePage{{Count: 1, Offset: 0, Genres: valid}}, err: errors.New("upstream down")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &genreCatalogPageReaderTest{pages: tc.pages, err: tc.err}
			items, err := service.LoadGenreCatalog(context.Background(), reader)
			if err == nil {
				t.Fatal("expected error")
			}
			if items != nil {
				t.Fatalf("partial items returned: %+v", items)
			}
		})
	}
}

func TestLoadGenreCatalogDoesNotPreallocateReportedCount(t *testing.T) {
	reader := &genreCatalogPageReaderTest{pages: []musicbrainz.GenrePage{{Count: math.MaxInt, Offset: 0, Genres: genreCatalogTestItems(0, 100)}}}
	items, err := service.LoadGenreCatalog(context.Background(), reader)
	if err == nil || items != nil {
		t.Fatalf("items=%v error=%v, want a failed subsequent page without partial results", items, err)
	}
	if len(reader.calls) != 2 || reader.calls[0] != 0 || reader.calls[1] != 100 {
		t.Fatalf("calls = %v, want [0 100]", reader.calls)
	}
}

func TestLoadGenreCatalogRejectsNilReader(t *testing.T) {
	items, err := service.LoadGenreCatalog(context.Background(), nil)
	if err == nil || items != nil {
		t.Fatalf("items=%v error=%v", items, err)
	}
}

func TestNormalizeGenreNamePreservesEnglishAcronyms(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"edm idm ebm uk us aor nwobhm nrg", "EDM IDM EBM UK US AOR NWOBHM NRG"},
		{"UK garage & drum'n'bass", "UK Garage & Drum'n'bass"},
	} {
		if got := librarymetadata.NormalizeGenreName(tc.raw); got != tc.want {
			t.Errorf("NormalizeGenreName(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestNormalizeGenreNameUsesFullEnglishUnicodeCasing(t *testing.T) {
	if got, want := librarymetadata.NormalizeGenreName("ßOUND Aİ"), "SSound Ai\u0307"; got != want {
		t.Errorf("NormalizeGenreName Unicode = %q, want %q", got, want)
	}
}
