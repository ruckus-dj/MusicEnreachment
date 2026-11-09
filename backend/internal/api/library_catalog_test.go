package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryCatalogAPIRepository struct {
	artist    *persistence.LibraryCatalogArtist
	releases  persistence.LibraryCatalogReleasePage
	positions persistence.LibraryCatalogPositionPage
	artists   persistence.LibraryCatalogArtistPage
	audio     []persistence.LibraryCatalogAudioCandidate
	requests  []persistence.LibraryCatalogRequest
}

func (repository *libraryCatalogAPIRepository) ReadLibraryArtists(_ context.Context, request persistence.LibraryCatalogRequest) (persistence.LibraryCatalogArtistPage, error) {
	repository.requests = append(repository.requests, request)
	return repository.artists, nil
}
func (repository *libraryCatalogAPIRepository) ReadLibraryReleases(_ context.Context, request persistence.LibraryCatalogRequest) (persistence.LibraryCatalogReleasePage, error) {
	repository.requests = append(repository.requests, request)
	return repository.releases, nil
}
func (repository *libraryCatalogAPIRepository) ReadLibraryPositions(_ context.Context, request persistence.LibraryCatalogRequest) (persistence.LibraryCatalogPositionPage, error) {
	repository.requests = append(repository.requests, request)
	return repository.positions, nil
}
func (repository *libraryCatalogAPIRepository) ReadLibraryAudioCandidates(context.Context, persistence.LibraryCatalogRequest) ([]persistence.LibraryCatalogAudioCandidate, error) {
	return repository.audio, nil
}
func (repository *libraryCatalogAPIRepository) ReadLibraryArtist(context.Context, uuid.UUID) (*persistence.LibraryCatalogArtist, error) {
	return repository.artist, nil
}
func (repository *libraryCatalogAPIRepository) ReadLibraryReleaseGroupEditions(context.Context, uuid.UUID, int, int) (persistence.LibraryReleaseGroupEditions, error) {
	return persistence.LibraryReleaseGroupEditions{}, nil
}
func (repository *libraryCatalogAPIRepository) ReadLibraryReleaseGroupEditionTracks(context.Context, uuid.UUID, string) (persistence.LibraryReleaseGroupEditionTracks, error) {
	return persistence.LibraryReleaseGroupEditionTracks{}, nil
}

func libraryCatalogHandler(catalog *service.LibraryCatalog) http.Handler {
	router := chi.NewRouter()
	api := New(router)
	registerLibraryCatalog(api, catalog)
	return router
}

func TestLibraryCatalogAPIEmitsConcreteModelsAndNullableFields(t *testing.T) {
	id, trackID := uuid.New(), uuid.New()
	release := persistence.LibraryCatalogRelease{ID: id, Title: "Album"}
	groupDate := "1999"
	repository := &libraryCatalogAPIRepository{
		releases: persistence.LibraryCatalogReleasePage{Items: []persistence.LibraryCatalogReleaseItem{
			{Kind: "library_release", LibraryRelease: &release},
			{Kind: "missing_release_group", MissingReleaseGroup: &persistence.LibraryCatalogMissingReleaseGroup{ProviderCode: "musicbrainz", ProviderKey: uuid.NewString(), Title: "Absent", Artists: []persistence.LibraryCatalogCredit{}, OriginalReleaseDate: &groupDate}},
		}, Total: 2},
		positions: persistence.LibraryCatalogPositionPage{
			Items: []persistence.LibraryCatalogPosition{{ID: uuid.New(), LocalTrackID: trackID, ReleaseID: id, ReleaseTitle: "Album", Medium: 1, Position: 2, Title: "Track", Total: 44}},
			Total: 1,
		},
	}
	handler := libraryCatalogHandler(service.NewLibraryCatalog(repository))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/library/positions?limit=10", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", body["items"])
	}
	item := items[0].(map[string]any)
	for _, key := range []string{"recording_id", "provider_duration_ms", "selected_media_variant_id", "publication"} {
		if value, exists := item[key]; !exists || value != nil {
			t.Errorf("%s = %#v (exists %t), expected JSON null", key, value, exists)
		}
	}
	if _, exists := item["total"]; exists {
		t.Fatal("query-only total leaked into item")
	}
	if value, exists := item["audio_available"]; !exists || value != false {
		t.Errorf("audio_available = %#v (exists %t), expected false", value, exists)
	}
	if !strings.Contains(recorder.Body.String(), `"summary"`) {
		t.Fatalf("missing summary: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/library/releases?limit=10", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("release status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	releaseItems := body["items"].([]any)
	if len(releaseItems) != 2 {
		t.Fatalf("release items = %#v", releaseItems)
	}
	concrete := releaseItems[0].(map[string]any)
	if concrete["kind"] != "library_release" || concrete["library_release"].(map[string]any)["id"] != id.String() || concrete["missing_release_group"] != nil {
		t.Fatalf("concrete release envelope = %#v", concrete)
	}
	missing := releaseItems[1].(map[string]any)
	group := missing["missing_release_group"].(map[string]any)
	if missing["kind"] != "missing_release_group" || group["provider_key"] == "" || group["original_release_date"] != groupDate || missing["library_release"] != nil {
		t.Fatalf("missing release group envelope = %#v", missing)
	}

	repository.artists = persistence.LibraryCatalogArtistPage{Items: []persistence.LibraryCatalogArtist{{ID: uuid.New(), Name: "Artist", Genres: []string{}, GenresJSON: `[]`, Total: 55}}}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/library/artists?limit=10", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("artist status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	artist := body["items"].([]any)[0].(map[string]any)
	if value, exists := artist["disambiguation"]; !exists || value != nil {
		t.Errorf("disambiguation = %#v (exists %t), expected JSON null", value, exists)
	}
	if _, exists := artist["genres_json"]; exists {
		t.Fatal("query-only genres JSON leaked into artist")
	}
	if value, exists := artist["available_position_count"]; !exists || value != float64(0) {
		t.Errorf("available_position_count = %#v (exists %t), expected 0", value, exists)
	}
}

func TestLibraryCatalogAPIHumaValidationAndIDErrors(t *testing.T) {
	handler := libraryCatalogHandler(service.NewLibraryCatalog(&libraryCatalogAPIRepository{}))
	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/library/artists", want: http.StatusUnprocessableEntity},
		{path: "/library/releases?limit=1&artist_id=invalid", want: http.StatusBadRequest},
		{path: "/library/positions?limit=1&release_id=invalid", want: http.StatusBadRequest},
		{path: "/library/positions?limit=1&publication=unknown", want: http.StatusUnprocessableEntity},
		{path: "/library/artists?limit=1&availability=unknown", want: http.StatusUnprocessableEntity},
		{path: "/library/releases?limit=1&availability=unknown", want: http.StatusUnprocessableEntity},
		{path: "/library/positions?limit=1&availability=unknown", want: http.StatusUnprocessableEntity},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.want {
			t.Errorf("GET %s status = %d, want %d: %s", test.path, recorder.Code, test.want, recorder.Body.String())
		}
	}
}

func TestLibraryCatalogAPIAvailabilityFilterDefaultsAndForwards(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/library/artists?limit=1", want: "all"},
		{path: "/library/releases?limit=1&availability=available", want: "available"},
		{path: "/library/positions?limit=1&availability=missing", want: "missing"},
	} {
		repository := &libraryCatalogAPIRepository{}
		handler := libraryCatalogHandler(service.NewLibraryCatalog(repository))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", test.path, recorder.Code, recorder.Body.String())
		}
		if len(repository.requests) != 1 {
			t.Fatalf("GET %s library reads = %d, want 1", test.path, len(repository.requests))
		}
		if got := repository.requests[0].Availability; got != test.want {
			t.Errorf("GET %s availability = %q, want %q", test.path, got, test.want)
		}
	}
}

func TestLibraryCatalogAPINotFoundAndUnavailable(t *testing.T) {
	id := uuid.New()
	missing := libraryCatalogHandler(service.NewLibraryCatalog(&libraryCatalogAPIRepository{}))
	recorder := httptest.NewRecorder()
	missing.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/library/artists/"+id.String(), nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing artist status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	unavailable := libraryCatalogHandler(nil)
	recorder = httptest.NewRecorder()
	unavailable.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/library/artists?limit=1", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
