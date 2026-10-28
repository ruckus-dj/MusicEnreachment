package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryPublicationsListAPIRepository struct {
	page persistence.LibraryPublicationListPage
	err  error
}

func (repository *libraryPublicationsListAPIRepository) ReadLibraryPublicationsList(context.Context, persistence.LibraryPublicationListRequest) (persistence.LibraryPublicationListPage, error) {
	return repository.page, repository.err
}

func libraryPublicationsListHandler(list *service.LibraryPublicationsList) http.Handler {
	router := chi.NewRouter()
	registerLibraryPublicationsList(New(router), list)
	return router
}

func TestLibraryPublicationsListAPIEmitsArraysAndMapsAudio(t *testing.T) {
	artistID := uuid.New()
	lossless := true
	repository := &libraryPublicationsListAPIRepository{page: persistence.LibraryPublicationListPage{
		Items: []persistence.LibraryPublicationListItem{{
			ReleaseID: uuid.New(), PositionID: uuid.New(), Artists: []persistence.LibraryPublicationListCredit{{ArtistID: &artistID, ArtistName: "Artist", CreditName: "Credit", JoinPhrase: " & "}},
			Audio: persistence.LibraryPublicationListAudio{StreamIndex: 3, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true, ALACCapabilitiesVerified: true},
		}}, Total: 1,
	}}
	handler := libraryPublicationsListHandler(service.NewLibraryPublicationsList(repository))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/library/publications?limit=10&offset=4", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Items  []map[string]any `json:"items"`
		Total  int              `json:"total"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Items == nil || len(body.Items) != 1 || body.Total != 1 || body.Limit != 10 || body.Offset != 4 {
		t.Fatalf("body = %+v", body)
	}
	item := body.Items[0]
	credits, ok := item["artists"].([]any)
	if !ok || len(credits) != 1 || credits[0].(map[string]any)["artist_id"] != artistID.String() {
		t.Errorf("artists = %#v", item["artists"])
	}
	audio, ok := item["audio"].(map[string]any)
	if !ok || audio["stream_index"] != float64(3) || audio["codec"] != "flac" || audio["lossless"] != true || audio["supported_source_container"] != true || audio["alac_capabilities_verified"] != true {
		t.Errorf("audio = %#v", item["audio"])
	}
	if _, leaked := item["total"]; leaked {
		t.Error("repository-only total leaked into item")
	}

	repository.page = persistence.LibraryPublicationListPage{Items: []persistence.LibraryPublicationListItem{}, Total: 0}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/library/publications?limit=10", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("empty status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Items == nil || len(body.Items) != 0 {
		t.Fatalf("empty items = %#v, err = %v", body.Items, err)
	}
}

func TestLibraryPublicationsListAPIValidationAndUnavailable(t *testing.T) {
	for _, test := range []struct {
		path string
		want int
		list *service.LibraryPublicationsList
	}{
		{path: "/library/publications", want: http.StatusUnprocessableEntity, list: service.NewLibraryPublicationsList(&libraryPublicationsListAPIRepository{})},
		{path: "/library/publications?limit=0", want: http.StatusUnprocessableEntity, list: service.NewLibraryPublicationsList(&libraryPublicationsListAPIRepository{})},
		{path: "/library/publications?limit=101", want: http.StatusUnprocessableEntity, list: service.NewLibraryPublicationsList(&libraryPublicationsListAPIRepository{})},
		{path: "/library/publications?limit=1&offset=-1", want: http.StatusUnprocessableEntity, list: service.NewLibraryPublicationsList(&libraryPublicationsListAPIRepository{})},
		{path: "/library/publications?limit=1", want: http.StatusServiceUnavailable, list: nil},
		{path: "/library/publications?limit=1", want: http.StatusServiceUnavailable, list: service.NewLibraryPublicationsList(nil)},
	} {
		handler := libraryPublicationsListHandler(test.list)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.want {
			t.Errorf("GET %s status = %d, want %d: %s", test.path, response.Code, test.want, response.Body.String())
		}
	}
}
