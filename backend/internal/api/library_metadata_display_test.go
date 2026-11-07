package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryMetadataSpecSchema struct {
	Ref        string                               `json:"$ref"`
	Type       json.RawMessage                      `json:"type"`
	Format     string                               `json:"format"`
	Properties map[string]libraryMetadataSpecSchema `json:"properties"`
	Required   []string                             `json:"required"`
	Items      *libraryMetadataSpecSchema           `json:"items"`
}

type libraryMetadataSpecResponse struct {
	Content map[string]struct {
		Schema libraryMetadataSpecSchema `json:"schema"`
	} `json:"content"`
}

type libraryMetadataSpecOperation struct {
	OperationID string                                 `json:"operationId"`
	Responses   map[string]libraryMetadataSpecResponse `json:"responses"`
}

type libraryMetadataSpecPath struct {
	Get   *libraryMetadataSpecOperation `json:"get"`
	Patch *libraryMetadataSpecOperation `json:"patch"`
}

type libraryMetadataSpecDocument struct {
	Components struct {
		Schemas map[string]libraryMetadataSpecSchema `json:"schemas"`
	} `json:"components"`
	Paths map[string]libraryMetadataSpecPath `json:"paths"`
}

func (document libraryMetadataSpecDocument) component(t *testing.T, name string) libraryMetadataSpecSchema {
	t.Helper()
	schema, ok := document.Components.Schemas[name]
	if !ok {
		t.Fatalf("OpenAPI component %q is missing", name)
	}
	return schema
}

func (schema libraryMetadataSpecSchema) property(t *testing.T, name string) libraryMetadataSpecSchema {
	t.Helper()
	property, ok := schema.Properties[name]
	if !ok {
		t.Fatalf("OpenAPI schema property %q is missing", name)
	}
	return property
}

func (schema libraryMetadataSpecSchema) requires(name string) bool {
	for _, required := range schema.Required {
		if required == name {
			return true
		}
	}
	return false
}

func (schema libraryMetadataSpecSchema) typeNames(t *testing.T) map[string]bool {
	t.Helper()
	if len(schema.Type) == 0 || string(schema.Type) == "null" {
		return map[string]bool{}
	}
	var single string
	if err := json.Unmarshal(schema.Type, &single); err == nil {
		return map[string]bool{single: true}
	}
	var multiple []string
	if err := json.Unmarshal(schema.Type, &multiple); err != nil {
		t.Fatalf("decode OpenAPI type %s: %v", schema.Type, err)
	}
	names := make(map[string]bool, len(multiple))
	for _, name := range multiple {
		names[name] = true
	}
	return names
}

func (path libraryMetadataSpecPath) operation(method string) *libraryMetadataSpecOperation {
	switch method {
	case http.MethodGet:
		return path.Get
	case http.MethodPatch:
		return path.Patch
	default:
		return nil
	}
}

func (operation *libraryMetadataSpecOperation) responseSchema(t *testing.T, status string) libraryMetadataSpecSchema {
	t.Helper()
	response, ok := operation.Responses[status]
	if !ok {
		t.Fatalf("OpenAPI operation %q has no %s response", operation.OperationID, status)
	}
	media, ok := response.Content["application/json"]
	if !ok {
		t.Fatalf("OpenAPI operation %q has no JSON %s response", operation.OperationID, status)
	}
	return media.Schema
}

func TestLibraryMetadataOpenAPIDisplaySchemaIsGETOnly(t *testing.T) {
	humaAPI := New(chi.NewRouter())
	RegisterAll(humaAPI, Dependencies{})
	encoded, err := json.Marshal(humaAPI.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	var document libraryMetadataSpecDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode OpenAPI document: %v", err)
	}

	const (
		displayRef = "#/components/schemas/LibraryMetadataGetResponse"
		desiredRef = "#/components/schemas/LibraryMetadataResponse"
	)
	for _, test := range []struct {
		name        string
		operationID string
		method      string
		path        string
		wantRef     string
	}{
		{"artist GET includes display baseline", "get-library-artist-metadata", http.MethodGet, "/library/artists/{artist_id}/metadata", displayRef},
		{"artist PATCH stays desired only", "patch-library-artist-metadata", http.MethodPatch, "/library/artists/{artist_id}/metadata", desiredRef},
		{"release GET includes display baseline", "get-library-release-metadata", http.MethodGet, "/library/releases/{release_id}/metadata", displayRef},
		{"release PATCH stays desired only", "patch-library-release-metadata", http.MethodPatch, "/library/releases/{release_id}/metadata", desiredRef},
		{"recording GET includes display baseline", "get-library-recording-metadata", http.MethodGet, "/library/recordings/{recording_id}/metadata", displayRef},
		{"recording PATCH stays desired only", "patch-library-recording-metadata", http.MethodPatch, "/library/recordings/{recording_id}/metadata", desiredRef},
		{"track GET stays desired only", "get-library-track-metadata", http.MethodGet, "/library/tracks/{track_id}/metadata", desiredRef},
		{"track PATCH stays desired only", "patch-library-track-metadata", http.MethodPatch, "/library/tracks/{track_id}/metadata", desiredRef},
		{"position GET stays desired only", "get-library-position-metadata", http.MethodGet, "/library/releases/{release_id}/positions/{position_id}/metadata", desiredRef},
		{"position PATCH stays desired only", "patch-library-position-metadata", http.MethodPatch, "/library/releases/{release_id}/positions/{position_id}/metadata", desiredRef},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, ok := document.Paths[test.path]
			if !ok {
				t.Fatalf("OpenAPI path %q is missing", test.path)
			}
			operation := path.operation(test.method)
			if operation == nil {
				t.Fatalf("OpenAPI path %q has no %s operation", test.path, test.method)
			}
			if operation.OperationID != test.operationID {
				t.Fatalf("operation ID = %q, want %q", operation.OperationID, test.operationID)
			}
			schema := operation.responseSchema(t, "200")
			if schema.Ref != test.wantRef {
				t.Fatalf("200 response schema = %q, want %q", schema.Ref, test.wantRef)
			}
		})
	}

	display := document.component(t, "LibraryMetadataGetResponse")
	for _, name := range []string{"envelope", "revision", "publication_applied"} {
		if !display.requires(name) {
			t.Errorf("display response must require %q: %#v", name, display.Required)
		}
	}
	baseline := display.property(t, "provider_genre_baseline")
	if baseline.Ref != "#/components/schemas/ProviderGenreBaseline" {
		t.Errorf("provider_genre_baseline schema = %q, want ProviderGenreBaseline reference", baseline.Ref)
	}
	if display.requires("provider_genre_baseline") {
		t.Error("provider_genre_baseline must remain optional for owners without a provider binding")
	}

	desired := document.component(t, "LibraryMetadataResponse")
	for _, name := range []string{"envelope", "revision", "publication_applied"} {
		if !desired.requires(name) {
			t.Errorf("desired response must require %q: %#v", name, desired.Required)
		}
	}
	if _, present := desired.Properties["provider_genre_baseline"]; present {
		t.Error("desired-only response must not expose provider_genre_baseline")
	}

	for _, response := range []struct {
		name   string
		schema libraryMetadataSpecSchema
	}{{"display", display}, {"desired", desired}} {
		if envelope := response.schema.property(t, "envelope"); envelope.Ref != "#/components/schemas/Envelope" {
			t.Errorf("%s response envelope = %q, want Envelope reference", response.name, envelope.Ref)
		}
	}
	envelope := document.component(t, "Envelope")
	for _, name := range []string{"version", "scope", "desired"} {
		if !envelope.requires(name) {
			t.Errorf("local envelope must require %q: %#v", name, envelope.Required)
		}
	}

	providerBaseline := document.component(t, "ProviderGenreBaseline")
	for _, name := range []string{"provider_code", "provider_source_id", "provider_key", "evidence", "genres", "digest"} {
		if !providerBaseline.requires(name) {
			t.Errorf("provider baseline must require %q: %#v", name, providerBaseline.Required)
		}
	}
	if evidence := providerBaseline.property(t, "evidence"); evidence.Ref != "#/components/schemas/ProviderGenreEvidence" {
		t.Errorf("evidence schema = %q, want ProviderGenreEvidence reference", evidence.Ref)
	}
	providerGenres := providerBaseline.property(t, "genres")
	if !providerGenres.typeNames(t)["array"] {
		t.Errorf("provider baseline genres must be an array: %s", providerGenres.Type)
	}
	if providerGenres.Items == nil || providerGenres.Items.Ref != "#/components/schemas/GenreReference" {
		t.Errorf("provider baseline genres items = %#v, want GenreReference reference", providerGenres.Items)
	}

	evidence := document.component(t, "ProviderGenreEvidence")
	for _, name := range []string{"authority", "fetch_order"} {
		property := evidence.property(t, name)
		if !evidence.requires(name) {
			t.Errorf("provider evidence must require %q: %#v", name, evidence.Required)
		}
		if !property.typeNames(t)["integer"] || property.Format != "int64" {
			t.Errorf("provider evidence %s must be an int64 integer: type=%s format=%q", name, property.Type, property.Format)
		}
	}

	genreReference := document.component(t, "GenreReference")
	for _, name := range []string{"id", "source_name", "name"} {
		if !genreReference.requires(name) {
			t.Errorf("genre reference must require %q: %#v", name, genreReference.Required)
		}
		if !genreReference.property(t, name).typeNames(t)["string"] {
			t.Errorf("genre reference %s must be a string", name)
		}
	}
}

type libraryMetadataDisplayBody struct {
	Envelope              librarymetadata.Envelope        `json:"envelope"`
	Revision              int64                           `json:"revision"`
	PublicationApplied    bool                            `json:"publication_applied"`
	ProviderGenreBaseline *libraryMetadataDisplayBaseline `json:"provider_genre_baseline"`
}

type libraryMetadataDisplayBaseline struct {
	ProviderCode     string                           `json:"provider_code"`
	ProviderSourceID string                           `json:"provider_source_id"`
	ProviderKey      string                           `json:"provider_key"`
	Evidence         libraryMetadataDisplayEvidence   `json:"evidence"`
	Genres           []librarymetadata.GenreReference `json:"genres"`
	Digest           string                           `json:"digest"`
}

type libraryMetadataDisplayEvidence struct {
	Authority  int   `json:"authority"`
	FetchOrder int64 `json:"fetch_order"`
}

func libraryMetadataDisplayPath(scope librarymetadata.Scope, id uuid.UUID) string {
	switch scope {
	case librarymetadata.ScopeArtist:
		return "/library/artists/" + id.String() + "/metadata"
	case librarymetadata.ScopeRelease:
		return "/library/releases/" + id.String() + "/metadata"
	case librarymetadata.ScopeRecording:
		return "/library/recordings/" + id.String() + "/metadata"
	case librarymetadata.ScopeTrack:
		return "/library/tracks/" + id.String() + "/metadata"
	case librarymetadata.ScopePosition:
		return "/library/releases/" + id.String() + "/positions/" + uuid.New().String() + "/metadata"
	default:
		panic("unsupported library metadata scope")
	}
}

func TestLibraryMetadataDisplayReturnsProviderBaselineFields(t *testing.T) {
	providerSourceID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	genres := []librarymetadata.GenreReference{
		{ID: "11111111-1111-4111-8111-111111111111", SourceName: "post-punk", Name: "Post-Punk"},
		{ID: "22222222-2222-4222-8222-222222222222", SourceName: "art rock", Name: "Art Rock"},
	}
	baseline := persistence.ProviderGenreBaseline{
		ProviderCode: "musicbrainz",
		SourceID:     providerSourceID,
		ProviderKey:  "provider-entity-key",
		Authority:    2,
		FetchOrder:   4,
		Genres:       genres,
		Digest:       "baseline-digest",
	}
	for _, scope := range []librarymetadata.Scope{librarymetadata.ScopeArtist, librarymetadata.ScopeRelease, librarymetadata.ScopeRecording} {
		t.Run(string(scope), func(t *testing.T) {
			id := uuid.New()
			metadata := libraryMetadataSeed(scope)
			repository := &libraryMetadataRepositoryFixture{
				read:    metadata,
				display: &persistence.LibraryMetadataDisplay{Metadata: metadata, ProviderGenreBaseline: &baseline},
			}
			handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
			response := libraryMetadataRequest(t, handler, http.MethodGet, libraryMetadataDisplayPath(scope, id), "")
			if response.Code != http.StatusOK {
				t.Fatalf("GET status = %d: %s", response.Code, response.Body)
			}
			var body libraryMetadataDisplayBody
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Envelope.Scope != scope || body.Revision != metadata.Revision || body.PublicationApplied {
				t.Fatalf("GET metadata = %#v", body)
			}
			got := body.ProviderGenreBaseline
			if got == nil {
				t.Fatal("GET omitted provider_genre_baseline")
			}
			// Numeric fields decode into int/int64: a quoted JSON string would fail here.
			if got.ProviderCode != "musicbrainz" || got.ProviderSourceID != providerSourceID.String() || got.ProviderKey != "provider-entity-key" || got.Digest != "baseline-digest" {
				t.Fatalf("provider identity = %#v", got)
			}
			if got.Evidence.Authority != 2 || got.Evidence.FetchOrder != 4 {
				t.Fatalf("provider evidence = %#v, want authority 2 and fetch_order 4", got.Evidence)
			}
			if len(got.Genres) != len(genres) {
				t.Fatalf("provider genres = %#v, want %#v", got.Genres, genres)
			}
			for index := range genres {
				if got.Genres[index] != genres[index] {
					t.Fatalf("provider genre %d = %#v, want %#v", index, got.Genres[index], genres[index])
				}
			}
		})
	}
}

func TestLibraryMetadataDisplayAcceptedEmptyGenresAreAnArray(t *testing.T) {
	for _, scope := range []librarymetadata.Scope{librarymetadata.ScopeArtist, librarymetadata.ScopeRelease, librarymetadata.ScopeRecording} {
		t.Run(string(scope), func(t *testing.T) {
			id := uuid.New()
			metadata := libraryMetadataSeed(scope)
			repository := &libraryMetadataRepositoryFixture{
				read: metadata,
				display: &persistence.LibraryMetadataDisplay{Metadata: metadata, ProviderGenreBaseline: &persistence.ProviderGenreBaseline{
					ProviderCode: "musicbrainz",
					SourceID:     uuid.New(),
					ProviderKey:  "provider-entity-key",
					Authority:    1,
					FetchOrder:   3,
					Genres:       []librarymetadata.GenreReference{},
					Digest:       "empty-baseline-digest",
				}},
			}
			handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
			response := libraryMetadataRequest(t, handler, http.MethodGet, libraryMetadataDisplayPath(scope, id), "")
			if response.Code != http.StatusOK {
				t.Fatalf("GET status = %d: %s", response.Code, response.Body)
			}
			var body libraryMetadataDisplayBody
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.ProviderGenreBaseline == nil {
				t.Fatal("GET omitted provider_genre_baseline with accepted empty genres")
			}
			if body.ProviderGenreBaseline.Genres == nil || len(body.ProviderGenreBaseline.Genres) != 0 {
				t.Fatalf("accepted empty genres = %#v, want a non-nil empty array", body.ProviderGenreBaseline.Genres)
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			var baseline map[string]json.RawMessage
			if err := json.Unmarshal(raw["provider_genre_baseline"], &baseline); err != nil {
				t.Fatal(err)
			}
			if genres := bytes.TrimSpace(baseline["genres"]); !bytes.Equal(genres, []byte("[]")) {
				t.Fatalf("accepted empty genres encoded as %s, want []", genres)
			}
		})
	}
}

func TestLibraryMetadataDisplayOmitsMissingProviderBaseline(t *testing.T) {
	// A missing binding/evidence row is modelled as a nil baseline and must omit
	// the property. A malformed baseline is an error, covered separately.
	for _, scope := range []librarymetadata.Scope{
		librarymetadata.ScopeArtist,
		librarymetadata.ScopeRelease,
		librarymetadata.ScopeRecording,
		librarymetadata.ScopeTrack,
		librarymetadata.ScopePosition,
	} {
		t.Run(string(scope), func(t *testing.T) {
			id := uuid.New()
			metadata := libraryMetadataSeed(scope)
			repository := &libraryMetadataRepositoryFixture{
				read:    metadata,
				display: &persistence.LibraryMetadataDisplay{Metadata: metadata},
			}
			handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
			response := libraryMetadataRequest(t, handler, http.MethodGet, libraryMetadataDisplayPath(scope, id), "")
			if response.Code != http.StatusOK {
				t.Fatalf("GET status = %d: %s", response.Code, response.Body)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			if _, present := raw["provider_genre_baseline"]; present {
				t.Fatalf("GET exposed an absent provider_genre_baseline: %s", response.Body)
			}
		})
	}

	for _, scope := range []librarymetadata.Scope{librarymetadata.ScopeArtist, librarymetadata.ScopeRelease, librarymetadata.ScopeRecording} {
		t.Run("PATCH "+string(scope), func(t *testing.T) {
			id := uuid.New()
			metadata := libraryMetadataSeed(scope)
			repository := &libraryMetadataRepositoryFixture{
				read: metadata,
				display: &persistence.LibraryMetadataDisplay{Metadata: metadata, ProviderGenreBaseline: &persistence.ProviderGenreBaseline{
					ProviderCode: "musicbrainz",
					SourceID:     uuid.New(),
					ProviderKey:  "provider-entity-key",
					Authority:    1,
					FetchOrder:   3,
					Genres:       []librarymetadata.GenreReference{},
					Digest:       "baseline-digest",
				}},
			}
			handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
			response := libraryMetadataRequest(t, handler, http.MethodPatch, libraryMetadataDisplayPath(scope, id), `{"expected_revision":7,"changes":{"genre":{"state":"explicit_empty"}}}`)
			if response.Code != http.StatusOK {
				t.Fatalf("PATCH status = %d: %s", response.Code, response.Body)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			if _, present := raw["provider_genre_baseline"]; present {
				t.Fatalf("PATCH exposed provider_genre_baseline: %s", response.Body)
			}
		})
	}
}

func TestLibraryMetadataDisplayReadFailureIsUnavailable(t *testing.T) {
	for _, scope := range []librarymetadata.Scope{librarymetadata.ScopeArtist, librarymetadata.ScopeRelease, librarymetadata.ScopeRecording} {
		t.Run(string(scope), func(t *testing.T) {
			id := uuid.New()
			repository := &libraryMetadataRepositoryFixture{
				read:       libraryMetadataSeed(scope),
				displayErr: errors.New("invalid accepted provider genre evidence"),
			}
			handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
			response := libraryMetadataRequest(t, handler, http.MethodGet, libraryMetadataDisplayPath(scope, id), "")
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("GET status = %d, want %d: %s", response.Code, http.StatusServiceUnavailable, response.Body)
			}
			if bytes.Contains(response.Body.Bytes(), []byte("invalid accepted provider genre evidence")) {
				t.Fatalf("GET leaked the display failure: %s", response.Body)
			}
		})
	}
}
