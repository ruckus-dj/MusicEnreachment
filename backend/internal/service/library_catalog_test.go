package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestNormalizeLibraryCatalogQuery(t *testing.T) {
	if got, want := normalizeLibraryCatalogQuery("\u00a0Ａ\u2003 B\u2009Ｃ \u00a0"), "a b c"; got != want {
		t.Fatalf("normalizeLibraryCatalogQuery() = %q, want %q", got, want)
	}
}

type libraryCatalogRepositoryFixture struct {
	artists   persistence.LibraryCatalogArtistPage
	releases  persistence.LibraryCatalogReleasePage
	positions persistence.LibraryCatalogPositionPage
	artist    *persistence.LibraryCatalogArtist
	err       error
	request   persistence.LibraryCatalogRequest
}

func (repository *libraryCatalogRepositoryFixture) ReadLibraryArtists(_ context.Context, request persistence.LibraryCatalogRequest) (persistence.LibraryCatalogArtistPage, error) {
	repository.request = request
	return repository.artists, repository.err
}
func (repository *libraryCatalogRepositoryFixture) ReadLibraryReleases(_ context.Context, request persistence.LibraryCatalogRequest) (persistence.LibraryCatalogReleasePage, error) {
	repository.request = request
	return repository.releases, repository.err
}
func (repository *libraryCatalogRepositoryFixture) ReadLibraryPositions(_ context.Context, request persistence.LibraryCatalogRequest) (persistence.LibraryCatalogPositionPage, error) {
	repository.request = request
	return repository.positions, repository.err
}
func (repository *libraryCatalogRepositoryFixture) ReadLibraryArtist(context.Context, uuid.UUID) (*persistence.LibraryCatalogArtist, error) {
	return repository.artist, repository.err
}

func TestValidateLibraryCatalogFilterRequiresLimit(t *testing.T) {
	filter := LibraryCatalogFilter{Publication: "all"}
	if err := validateLibraryCatalogFilter(&filter); err == nil {
		t.Fatal("expected missing limit to be rejected")
	}
}

func TestLibraryCatalogMapsPersistenceRowsToCleanServiceDTOs(t *testing.T) {
	dbOnly := 19
	artistID := uuid.New()
	artist := persistence.LibraryCatalogArtist{ID: artistID, Name: "Artist", Genres: []string{"ambient"}, GenresJSON: `[{"name":"secret"}]`, Total: 97, ProviderLinks: []persistence.LibraryCatalogProviderLink{{ProviderCode: "mb", ProviderKey: "key", Method: "linked"}}}
	repository := &libraryCatalogRepositoryFixture{artists: persistence.LibraryCatalogArtistPage{Items: []persistence.LibraryCatalogArtist{artist}, Total: 2, Summary: persistence.LibraryCatalogSummary{ArtistCount: 1}}}
	page, err := NewLibraryCatalog(repository).Artists(context.Background(), LibraryCatalogFilter{Limit: 10, Query: "Ａ\u2003B"})
	if err != nil {
		t.Fatal(err)
	}
	if repository.request.Query != "a b" {
		t.Fatalf("normalized query = %q", repository.request.Query)
	}
	if len(page.Items) != 1 || page.Items[0].Genres[0] != "ambient" || page.Items[0].ProviderLinks[0].ProviderKey != "key" {
		t.Fatalf("unexpected mapped page: %#v", page)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"genres_json", "total\":97", "secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("service DTO leaked %q: %s", forbidden, encoded)
		}
	}

	release := persistence.LibraryCatalogRelease{
		ID: uuid.New(), Title: "Album", Metadata: []byte(`{"private":"metadata"}`), Total: 91,
		PositionCountDB: 8, PublishedPositionCountDB: 3, AvailablePositionCountDB: 5,
		Counts:  persistence.LibraryCatalogReleaseCounts{PositionCount: 8, PublishedPositionCount: 3, AvailablePositionCount: 5, ProviderPositionCount: &dbOnly},
		Edition: persistence.LibraryCatalogEdition{Labels: nil, CatalogNumbers: nil, SecondaryTypes: nil},
	}
	repository.releases = persistence.LibraryCatalogReleasePage{Items: []persistence.LibraryCatalogRelease{release}}
	releasePage, err := NewLibraryCatalog(repository).Releases(context.Background(), LibraryCatalogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if releasePage.Items[0].Counts.PositionCount != 8 || releasePage.Items[0].Counts.ProviderPositionCount != &dbOnly {
		t.Fatalf("repo counts not retained: %#v", releasePage.Items[0].Counts)
	}
	encoded, err = json.Marshal(releasePage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private\":\"metadata") || strings.Contains(string(encoded), "position_count_db") {
		t.Fatalf("service DTO leaked persistence fields: %s", encoded)
	}
}

func TestLibraryCatalogUnavailableAndArtistNotFound(t *testing.T) {
	if _, err := (*LibraryCatalog)(nil).Artists(context.Background(), LibraryCatalogFilter{Limit: 1}); !IsLibraryCatalogUnavailable(err) {
		t.Fatalf("nil service error = %v", err)
	}
	if _, err := NewLibraryCatalog(nil).Releases(context.Background(), LibraryCatalogFilter{Limit: 1}); !IsLibraryCatalogUnavailable(err) {
		t.Fatalf("nil repository error = %v", err)
	}
	if _, err := NewLibraryCatalog(&libraryCatalogRepositoryFixture{}).Artist(context.Background(), uuid.New()); !IsLibraryArtistNotFound(err) {
		t.Fatalf("missing artist error = %v", err)
	}
	repository := &libraryCatalogRepositoryFixture{err: errors.New("db unavailable")}
	if _, err := NewLibraryCatalog(repository).Artists(context.Background(), LibraryCatalogFilter{Limit: 1}); err == nil || !strings.Contains(err.Error(), "read library artists") {
		t.Fatalf("repository error = %v", err)
	}
}
