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
	positions persistence.LibraryCatalogPositionPage
	artists   persistence.LibraryCatalogArtistPage
}

func (repository *libraryCatalogAPIRepository) ReadLibraryArtists(context.Context, persistence.LibraryCatalogRequest) (persistence.LibraryCatalogArtistPage, error) {
	return repository.artists, nil
}
func (repository *libraryCatalogAPIRepository) ReadLibraryReleases(context.Context, persistence.LibraryCatalogRequest) (persistence.LibraryCatalogReleasePage, error) {
	return persistence.LibraryCatalogReleasePage{Items: []persistence.LibraryCatalogRelease{}}, nil
}
func (repository *libraryCatalogAPIRepository) ReadLibraryPositions(context.Context, persistence.LibraryCatalogRequest) (persistence.LibraryCatalogPositionPage, error) {
	return repository.positions, nil
}
func (repository *libraryCatalogAPIRepository) ReadLibraryArtist(context.Context, uuid.UUID) (*persistence.LibraryCatalogArtist, error) {
	return repository.artist, nil
}

func libraryCatalogHandler(catalog *service.LibraryCatalog) http.Handler {
	router := chi.NewRouter()
	api := New(router)
	registerLibraryCatalog(api, catalog)
	return router
}

func TestLibraryCatalogAPIEmitsConcreteModelsAndNullableFields(t *testing.T) {
	id, trackID := uuid.New(), uuid.New()
	repository := &libraryCatalogAPIRepository{positions: persistence.LibraryCatalogPositionPage{
		Items: []persistence.LibraryCatalogPosition{{ID: uuid.New(), LocalTrackID: trackID, ReleaseID: id, ReleaseTitle: "Album", Medium: 1, Position: 2, Title: "Track", Total: 44}},
		Total: 1,
	}}
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
	if !strings.Contains(recorder.Body.String(), `"summary"`) {
		t.Fatalf("missing summary: %s", recorder.Body.String())
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
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.want {
			t.Errorf("GET %s status = %d, want %d: %s", test.path, recorder.Code, test.want, recorder.Body.String())
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
