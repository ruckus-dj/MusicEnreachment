package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryMetadataRepositoryFixture struct {
	read       persistence.LibraryMetadata
	readErr    error
	updateErr  error
	updated    librarymetadata.Envelope
	genres     []librarymetadata.GenreReference
	display    *persistence.LibraryMetadataDisplay
	displayErr error
}

func (fixture *libraryMetadataRepositoryFixture) ReadMetadata(context.Context, librarymetadata.Scope, uuid.UUID, uuid.UUID) (persistence.LibraryMetadata, error) {
	return fixture.read, fixture.readErr
}

func (fixture *libraryMetadataRepositoryFixture) ReadMetadataDisplay(context.Context, librarymetadata.Scope, uuid.UUID, uuid.UUID) (persistence.LibraryMetadataDisplay, error) {
	if fixture.displayErr != nil {
		return persistence.LibraryMetadataDisplay{}, fixture.displayErr
	}
	if fixture.display != nil {
		return *fixture.display, nil
	}
	return persistence.LibraryMetadataDisplay{Metadata: fixture.read}, fixture.readErr
}

func (fixture *libraryMetadataRepositoryFixture) UpdateMetadata(_ context.Context, _ librarymetadata.Scope, _, _ uuid.UUID, expected int64, envelope librarymetadata.Envelope) (persistence.LibraryMetadata, error) {
	fixture.updated = envelope
	if fixture.updateErr != nil {
		return persistence.LibraryMetadata{}, fixture.updateErr
	}
	return persistence.LibraryMetadata{Envelope: envelope, Revision: expected + 1}, nil
}

func (fixture *libraryMetadataRepositoryFixture) GenreCatalogItems(context.Context) ([]librarymetadata.GenreReference, error) {
	return fixture.genres, nil
}

func libraryMetadataSeed(scope librarymetadata.Scope) persistence.LibraryMetadata {
	return persistence.LibraryMetadata{Revision: 7, Envelope: librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: scope,
		Desired: map[string]librarymetadata.FieldValue{
			"comment": librarymetadata.ManualFieldValue(librarymetadata.StateValue, "stored", true),
		},
	}}
}

func TestLibraryMetadataHTTPCanonicalizesGenreByCatalogID(t *testing.T) {
	id := uuid.New()
	genreID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	repository := &libraryMetadataRepositoryFixture{
		read:   libraryMetadataSeed(librarymetadata.ScopeRelease),
		genres: []librarymetadata.GenreReference{{ID: genreID.String(), SourceName: "post-punk", Name: "Post-Punk"}},
	}
	handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
	path := "/library/releases/" + id.String() + "/metadata"
	body := `{"expected_revision":7,"changes":{"genre":{"state":"value","value":[{"id":"22222222-2222-4222-8222-222222222222","source_name":"forged","name":"Forged"}]}}}`
	response := libraryMetadataRequest(t, handler, http.MethodPatch, path, body)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d: %s", response.Code, response.Body)
	}
	var result struct {
		Envelope librarymetadata.Envelope `json:"envelope"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	genres, ok := result.Envelope.Desired["genre"].Value.([]any)
	if !ok || len(genres) != 1 {
		t.Fatalf("response genres = %#v", result.Envelope.Desired["genre"].Value)
	}
	var genre librarymetadata.GenreReference
	encoded, err := json.Marshal(genres[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &genre); err != nil {
		t.Fatal(err)
	}
	if genre != (librarymetadata.GenreReference{ID: "22222222-2222-4222-8222-222222222222", SourceName: "post-punk", Name: "Post-Punk"}) {
		t.Fatalf("response genre = %#v, want catalog-canonical names", genre)
	}
	persisted, ok := repository.updated.Desired["genre"].Value.([]librarymetadata.GenreReference)
	if !ok || len(persisted) != 1 || persisted[0] != (librarymetadata.GenreReference{ID: "22222222-2222-4222-8222-222222222222", SourceName: "post-punk", Name: "Post-Punk"}) {
		t.Fatalf("persisted genre = %#v, want canonical catalog identity and names", repository.updated.Desired["genre"].Value)
	}
}

func TestLibraryArtistMetadataHTTPUsesOptimisticGenreEnvelope(t *testing.T) {
	artistID := uuid.New()
	genreID := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	repository := &libraryMetadataRepositoryFixture{
		read: persistence.LibraryMetadata{Revision: 3, Envelope: librarymetadata.Envelope{
			Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeArtist, Desired: map[string]librarymetadata.FieldValue{},
		}},
		genres: []librarymetadata.GenreReference{{ID: genreID.String(), SourceName: "art rock", Name: "Art Rock"}},
	}
	handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
	path := "/library/artists/" + artistID.String() + "/metadata"
	response := libraryMetadataRequest(t, handler, http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", response.Code, response.Body)
	}
	var got struct {
		Envelope librarymetadata.Envelope `json:"envelope"`
		Revision int64                    `json:"revision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Envelope.Scope != librarymetadata.ScopeArtist || got.Revision != 3 {
		t.Fatalf("GET response = %+v", got)
	}
	body := `{"expected_revision":3,"changes":{"genre":{"state":"value","value":[{"id":"44444444-4444-4444-8444-444444444444","source_name":"fake","name":"fake"}]}}}`
	response = libraryMetadataRequest(t, handler, http.MethodPatch, path, body)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d: %s", response.Code, response.Body)
	}
	if repository.updated.Scope != librarymetadata.ScopeArtist || repository.updated.Desired["genre"].State != librarymetadata.StateValue {
		t.Fatalf("persisted artist envelope = %#v", repository.updated)
	}
}

func TestLibraryMetadataProviderGenreBaselineIsGETOnly(t *testing.T) {
	artistID := uuid.New()
	repository := &libraryMetadataRepositoryFixture{read: libraryMetadataSeed(librarymetadata.ScopeArtist)}
	repository.display = &persistence.LibraryMetadataDisplay{Metadata: repository.read, ProviderGenreBaseline: &persistence.ProviderGenreBaseline{
		ProviderCode: "musicbrainz", SourceID: uuid.New(), ProviderKey: "artist-key", Authority: 2, FetchOrder: 4,
		Genres: []librarymetadata.GenreReference{{ID: "11111111-1111-4111-8111-111111111111", SourceName: "rock", Name: "Rock"}}, Digest: "baseline-digest",
	}}
	handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
	path := "/library/artists/" + artistID.String() + "/metadata"
	response := libraryMetadataRequest(t, handler, http.MethodGet, path, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"provider_genre_baseline"`) {
		t.Fatalf("GET response = %d %s; expected provider baseline", response.Code, response.Body)
	}
	response = libraryMetadataRequest(t, handler, http.MethodPatch, path, `{"expected_revision":7,"changes":{"genre":{"state":"explicit_empty"}}}`)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"provider_genre_baseline"`) {
		t.Fatalf("PATCH response = %d %s; provider baseline must be GET-only", response.Code, response.Body)
	}
}

func TestLibraryMetadataHTTPRejectsUnknownGenreID(t *testing.T) {
	id := uuid.New()
	repository := &libraryMetadataRepositoryFixture{read: libraryMetadataSeed(librarymetadata.ScopeRelease)}
	handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
	body := `{"expected_revision":7,"changes":{"genre":{"state":"value","value":[{"id":"33333333-3333-4333-8333-333333333333","source_name":"rock","name":"Rock"}]}}}`
	response := libraryMetadataRequest(t, handler, http.MethodPatch, "/library/releases/"+id.String()+"/metadata", body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("PATCH status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body)
	}
	if repository.updated.Version != 0 {
		t.Fatalf("unknown genre was persisted: %#v", repository.updated)
	}
}

func libraryMetadataRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if method == http.MethodPatch {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestLibraryMetadataHTTPScopes(t *testing.T) {
	id, releaseID := uuid.New(), uuid.New()
	for _, test := range []struct {
		name  string
		scope librarymetadata.Scope
		path  string
	}{
		{"release", librarymetadata.ScopeRelease, "/library/releases/" + id.String() + "/metadata"},
		{"recording", librarymetadata.ScopeRecording, "/library/recordings/" + id.String() + "/metadata"},
		{"track", librarymetadata.ScopeTrack, "/library/tracks/" + id.String() + "/metadata"},
		{"position", librarymetadata.ScopePosition, "/library/releases/" + releaseID.String() + "/positions/" + id.String() + "/metadata"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &libraryMetadataRepositoryFixture{read: libraryMetadataSeed(test.scope)}
			handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
			var body struct {
				Envelope           librarymetadata.Envelope `json:"envelope"`
				Revision           int64                    `json:"revision"`
				PublicationApplied bool                     `json:"publication_applied"`
			}
			response := libraryMetadataRequest(t, handler, http.MethodGet, test.path, "")
			if response.Code != http.StatusOK {
				t.Fatalf("GET status = %d: %s", response.Code, response.Body)
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Envelope.Scope != test.scope || body.Revision != 7 || body.PublicationApplied || body.Envelope.Desired["comment"].Value != "stored" {
				t.Fatalf("GET body = %+v", body)
			}

			response = libraryMetadataRequest(t, handler, http.MethodPatch, test.path, `{"expected_revision":7,"changes":{"comment":{"state":"value","value":"edited"}}}`)
			if response.Code != http.StatusOK {
				t.Fatalf("PATCH status = %d: %s", response.Code, response.Body)
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			field := body.Envelope.Desired["comment"]
			if field.Value != "edited" || field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual || body.Revision != 8 || body.PublicationApplied {
				t.Fatalf("PATCH body = %+v", body)
			}
			if repository.updated.Desired["comment"].Value != "edited" {
				t.Fatalf("persisted envelope = %+v", repository.updated)
			}
		})
	}
}

func TestLibraryMetadataHTTPErrors(t *testing.T) {
	id := uuid.New()
	path := "/library/releases/" + id.String() + "/metadata"
	available := func(repository *libraryMetadataRepositoryFixture) *service.LibraryMetadataService {
		return service.NewLibraryMetadataService(repository)
	}
	for _, test := range []struct {
		name       string
		method     string
		path       string
		body       string
		dependency *service.LibraryMetadataService
		want       int
	}{
		{"malformed release id before dependency", http.MethodGet, "/library/releases/not-a-uuid/metadata", "", nil, http.StatusBadRequest},
		{"nil release id before dependency", http.MethodGet, "/library/releases/" + uuid.Nil.String() + "/metadata", "", nil, http.StatusBadRequest},
		{"malformed position id before dependency", http.MethodPatch, "/library/releases/" + id.String() + "/positions/not-a-uuid/metadata", `{"expected_revision":1,"changes":{"comment":{"state":"value","value":"x"}}}`, nil, http.StatusBadRequest},
		{"nil position id before dependency", http.MethodGet, "/library/releases/" + id.String() + "/positions/" + uuid.Nil.String() + "/metadata", "", nil, http.StatusBadRequest},
		{"missing service", http.MethodGet, path, "", nil, http.StatusServiceUnavailable},
		{"missing owner", http.MethodGet, path, "", available(&libraryMetadataRepositoryFixture{readErr: persistence.ErrLibraryMetadataNotFound}), http.StatusNotFound},
		{"missing owner on update", http.MethodPatch, path, `{"expected_revision":1,"changes":{"comment":{"state":"value","value":"x"}}}`, available(&libraryMetadataRepositoryFixture{readErr: persistence.ErrLibraryMetadataNotFound}), http.StatusNotFound},
		{"stale revision", http.MethodPatch, path, `{"expected_revision":1,"changes":{"comment":{"state":"value","value":"x"}}}`, available(&libraryMetadataRepositoryFixture{read: libraryMetadataSeed(librarymetadata.ScopeRelease), updateErr: persistence.ErrLibraryMetadataConflict}), http.StatusConflict},
		{"canonical field", http.MethodPatch, path, `{"expected_revision":1,"changes":{"title":{"state":"value","value":"x"}}}`, available(&libraryMetadataRepositoryFixture{read: libraryMetadataSeed(librarymetadata.ScopeRelease)}), http.StatusBadRequest},
		{"forged authority and provenance", http.MethodPatch, path, `{"expected_revision":1,"changes":{"comment":{"state":"value","value":"x","authority":"automatic","provenance":{"kind":"manual"}}}}`, available(&libraryMetadataRepositoryFixture{read: libraryMetadataSeed(librarymetadata.ScopeRelease)}), http.StatusBadRequest},
		{"null state", http.MethodPatch, path, `{"expected_revision":1,"changes":{"comment":{"state":null,"value":"x"}}}`, available(&libraryMetadataRepositoryFixture{read: libraryMetadataSeed(librarymetadata.ScopeRelease)}), http.StatusBadRequest},
		{"huma required schema fields", http.MethodPatch, path, `{}`, available(&libraryMetadataRepositoryFixture{read: libraryMetadataSeed(librarymetadata.ScopeRelease)}), http.StatusUnprocessableEntity},
		{"huma required changes", http.MethodPatch, path, `{"expected_revision":1}`, available(&libraryMetadataRepositoryFixture{read: libraryMetadataSeed(librarymetadata.ScopeRelease)}), http.StatusUnprocessableEntity},
		{"storage failure", http.MethodGet, path, "", available(&libraryMetadataRepositoryFixture{readErr: errors.New("storage unavailable")}), http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := HandlerWithDependencies(Dependencies{LibraryMetadata: test.dependency})
			response := libraryMetadataRequest(t, handler, test.method, test.path, test.body)
			if response.Code != test.want {
				t.Fatalf("%s status = %d, want %d: %s", test.method, response.Code, test.want, response.Body)
			}
		})
	}
}
