package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	audio     []persistence.LibraryCatalogAudioCandidate
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
func (repository *libraryCatalogRepositoryFixture) ReadLibraryAudioCandidates(_ context.Context, request persistence.LibraryCatalogRequest) ([]persistence.LibraryCatalogAudioCandidate, error) {
	repository.request = request
	return repository.audio, repository.err
}
func (repository *libraryCatalogRepositoryFixture) ReadLibraryReleaseGroupEditions(context.Context, uuid.UUID, int, int) (persistence.LibraryReleaseGroupEditions, error) {
	return persistence.LibraryReleaseGroupEditions{}, repository.err
}
func (repository *libraryCatalogRepositoryFixture) ReadLibraryReleaseGroupEditionTracks(context.Context, uuid.UUID, string) (persistence.LibraryReleaseGroupEditionTracks, error) {
	return persistence.LibraryReleaseGroupEditionTracks{}, repository.err
}

func TestValidateLibraryCatalogFilterRequiresLimit(t *testing.T) {
	filter := LibraryCatalogFilter{Publication: "all"}
	if err := validateLibraryCatalogFilter(&filter); err == nil {
		t.Fatal("expected missing limit to be rejected")
	}
}

func TestValidateLibraryCatalogAvailability(t *testing.T) {
	filter := LibraryCatalogFilter{Limit: 1}
	if err := validateLibraryCatalogFilter(&filter); err != nil || filter.Availability != "all" {
		t.Fatalf("default availability = %q, err %v", filter.Availability, err)
	}
	for _, value := range []string{"all", "available", "missing"} {
		filter := LibraryCatalogFilter{Limit: 1, Availability: value}
		if err := validateLibraryCatalogFilter(&filter); err != nil {
			t.Errorf("availability %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"bogus", "AVAILABLE"} {
		filter := LibraryCatalogFilter{Limit: 1, Availability: value}
		if err := validateLibraryCatalogFilter(&filter); err == nil {
			t.Errorf("availability %q accepted", value)
		}
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
	repository.releases = persistence.LibraryCatalogReleasePage{Items: []persistence.LibraryCatalogReleaseItem{{Kind: "library_release", LibraryRelease: &release}}}
	releasePage, err := NewLibraryCatalog(repository).Releases(context.Background(), LibraryCatalogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if releasePage.Items[0].Kind != "library_release" || releasePage.Items[0].LibraryRelease == nil || releasePage.Items[0].LibraryRelease.Counts.PositionCount != 8 || releasePage.Items[0].LibraryRelease.Counts.ProviderPositionCount != &dbOnly {
		t.Fatalf("repo release item not retained: %#v", releasePage.Items[0])
	}
	encoded, err = json.Marshal(releasePage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private\":\"metadata") || strings.Contains(string(encoded), "position_count_db") {
		t.Fatalf("service DTO leaked persistence fields: %s", encoded)
	}
}

func TestLibraryCatalogAvailabilityFiltersForwardObservedIDs(t *testing.T) {
	id := uuid.New()
	artistID := uuid.New()
	artist := &persistence.LibraryCatalogArtist{ID: artistID}
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "audio"), []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := &libraryCatalogRepositoryFixture{artist: artist, audio: []persistence.LibraryCatalogAudioCandidate{{PositionID: id, RootPath: root, RelativePath: "audio", Kind: "publication"}}}
	_, err = NewLibraryCatalog(repository).Positions(context.Background(), LibraryCatalogFilter{Limit: 10, Availability: "available"})
	if err != nil {
		t.Fatal(err)
	}
	if repository.request.Availability != "available" || len(repository.request.AvailablePositionIDs) != 1 || repository.request.AvailablePositionIDs[0] != id {
		t.Fatalf("forwarded request = %#v", repository.request)
	}
	// Artist detail derives its availability count from the complete observed ID set.
	repository.audio = []persistence.LibraryCatalogAudioCandidate{{PositionID: id, RootPath: root, RelativePath: "audio", Kind: "publication"}}
	result, err := NewLibraryCatalog(repository).Artist(context.Background(), artistID)
	if err != nil {
		t.Fatal(err)
	}
	if result.AvailablePositionCount != 1 {
		t.Fatalf("available count = %d", result.AvailablePositionCount)
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
	if _, err := NewLibraryCatalog(repository).Artists(context.Background(), LibraryCatalogFilter{Limit: 1}); !errors.Is(err, repository.err) {
		t.Fatalf("repository error = %v", err)
	}
}
