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
	read      persistence.LibraryMetadata
	readErr   error
	updateErr error
	updated   librarymetadata.Envelope
}

func (fixture *libraryMetadataRepositoryFixture) ReadMetadata(context.Context, librarymetadata.Scope, uuid.UUID, uuid.UUID) (persistence.LibraryMetadata, error) {
	return fixture.read, fixture.readErr
}

func (fixture *libraryMetadataRepositoryFixture) UpdateMetadata(_ context.Context, _ librarymetadata.Scope, _, _ uuid.UUID, expected int64, envelope librarymetadata.Envelope) (persistence.LibraryMetadata, error) {
	fixture.updated = envelope
	if fixture.updateErr != nil {
		return persistence.LibraryMetadata{}, fixture.updateErr
	}
	return persistence.LibraryMetadata{Envelope: envelope, Revision: expected + 1}, nil
}

func libraryMetadataSeed(scope librarymetadata.Scope) persistence.LibraryMetadata {
	return persistence.LibraryMetadata{Revision: 7, Envelope: librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: scope,
		Desired: map[string]librarymetadata.FieldValue{
			"comment": librarymetadata.ManualFieldValue(librarymetadata.StateValue, "stored", true),
		},
	}}
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
