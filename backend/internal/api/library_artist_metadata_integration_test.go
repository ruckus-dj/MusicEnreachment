//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLibraryArtistMetadataHTTPUsesPersistedCatalogAndRevision(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	artistID := uuid.New()
	genreID := "66666666-6666-4666-8666-666666666666"
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name) VALUES(?,?)`, artistID, "API Genre Artist").Exec(ctx); err != nil {
		t.Fatalf("insert local artist: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE genre_catalog SET items=?::jsonb,updated_at=now() WHERE singleton=true`, `[{"id":"66666666-6666-4666-8666-666666666666","source_name":"dream pop","name":"Dream Pop"}]`).Exec(ctx); err != nil {
		t.Fatalf("seed genre catalog: %v", err)
	}
	repository := persistence.NewLibraryRepository(db)
	handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(repository)})
	path := "/library/artists/" + artistID.String() + "/metadata"
	body := `{"expected_revision":1,"changes":{"genre":{"state":"value","value":[{"id":"66666666-6666-4666-8666-666666666666","source_name":"spoofed source","name":"Spoofed display"}]}}}`
	response := libraryMetadataRequest(t, handler, http.MethodPatch, path, body)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d: %s", response.Code, response.Body)
	}
	var result struct {
		Envelope librarymetadata.Envelope `json:"envelope"`
		Revision int64                    `json:"revision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision != 2 || result.Envelope.Scope != librarymetadata.ScopeArtist {
		t.Fatalf("PATCH response = %+v", result)
	}
	genres, ok := result.Envelope.Desired["genre"].Value.([]any)
	if !ok || len(genres) != 1 {
		t.Fatalf("canonical API genre = %#v", result.Envelope.Desired["genre"].Value)
	}
	canonical, ok := genres[0].(map[string]any)
	if !ok || canonical["id"] != genreID || canonical["source_name"] != "dream pop" || canonical["name"] != "Dream Pop" {
		t.Fatalf("canonical API genre = %#v", genres[0])
	}

	response = libraryMetadataRequest(t, handler, http.MethodPatch, path, `{"expected_revision":2,"changes":{"genre":{"state":"value","value":[{"id":"77777777-7777-4777-8777-777777777777","source_name":"unknown","name":"Unknown"}]}}}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown genre PATCH status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body)
	}
	if _, err := db.NewRaw(`UPDATE local_artist SET metadata=metadata || '{"genres":["keep genre"],"provenance":{"source":"keep provenance"},"unrelated":{"keep":"root"}}'::jsonb WHERE id=?`, artistID).Exec(ctx); err != nil {
		t.Fatalf("seed artist metadata to preserve: %v", err)
	}
	type artistState struct {
		Name              string
		HasDisambiguation bool
		Disambiguation    string
		Revision          int64
		Metadata          map[string]json.RawMessage
	}
	readArtist := func() artistState {
		t.Helper()
		var state artistState
		var raw string
		if err := db.NewRaw(`SELECT name,metadata->'disambiguation' IS NOT NULL,COALESCE(metadata->>'disambiguation',''),metadata_revision,metadata::text FROM local_artist WHERE id=?`, artistID).Scan(ctx, &state.Name, &state.HasDisambiguation, &state.Disambiguation, &state.Revision, &raw); err != nil {
			t.Fatalf("read artist state: %v", err)
		}
		if err := json.Unmarshal([]byte(raw), &state.Metadata); err != nil {
			t.Fatal(err)
		}
		return state
	}
	desiredSort := func(state artistState) string {
		t.Helper()
		var envelope librarymetadata.Envelope
		if err := json.Unmarshal(state.Metadata["local_metadata"], &envelope); err != nil {
			t.Fatal(err)
		}
		value, _ := envelope.Desired["sort_name"].Value.(string)
		return value
	}
	mixedBody := `{"expected_revision":2,"name":"  Local Artist  ","disambiguation":"","changes":{"sort_name":{"state":"value","value":"Local Artist, The"},"genre":{"state":"value","value":[{"id":"66666666-6666-4666-8666-666666666666","source_name":"dream pop","name":"Dream Pop"}]}}}`
	response = libraryMetadataRequest(t, handler, http.MethodPatch, path, mixedBody)
	if response.Code != http.StatusOK {
		t.Fatalf("mixed artist metadata PATCH status = %d: %s", response.Code, response.Body)
	}
	var mixed struct {
		Envelope librarymetadata.Envelope `json:"envelope"`
		Revision int64                    `json:"revision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &mixed); err != nil {
		t.Fatal(err)
	}
	mixedSort, _ := mixed.Envelope.Desired["sort_name"].Value.(string)
	if mixed.Revision != 3 || mixedSort != "Local Artist, The" {
		t.Fatalf("mixed artist metadata PATCH response = %+v", mixed)
	}
	mixedGenres, ok := mixed.Envelope.Desired["genre"].Value.([]any)
	if !ok || len(mixedGenres) != 1 {
		t.Fatalf("mixed artist metadata genre = %#v", mixed.Envelope.Desired["genre"].Value)
	}
	if canonical, ok := mixedGenres[0].(map[string]any); !ok || canonical["id"] != genreID || canonical["source_name"] != "dream pop" || canonical["name"] != "Dream Pop" {
		t.Fatalf("mixed artist metadata genre = %#v", mixedGenres[0])
	}
	afterMixed := readArtist()
	if afterMixed.Revision != 3 || afterMixed.Name != "  Local Artist  " || !afterMixed.HasDisambiguation || afterMixed.Disambiguation != "" || desiredSort(afterMixed) != "Local Artist, The" {
		t.Fatalf("artist after mixed PATCH = %+v", afterMixed)
	}
	var preservedGenres []string
	var preservedProvenance struct {
		Source string `json:"source"`
	}
	var preservedUnrelated struct {
		Keep string `json:"keep"`
	}
	if err := json.Unmarshal(afterMixed.Metadata["genres"], &preservedGenres); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(afterMixed.Metadata["provenance"], &preservedProvenance); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(afterMixed.Metadata["unrelated"], &preservedUnrelated); err != nil {
		t.Fatal(err)
	}
	if len(preservedGenres) != 1 || preservedGenres[0] != "keep genre" || preservedProvenance.Source != "keep provenance" || preservedUnrelated.Keep != "root" {
		t.Fatalf("artist metadata after mixed PATCH = %+v", afterMixed.Metadata)
	}
	response = libraryMetadataRequest(t, handler, http.MethodPatch, path, `{"expected_revision":3,"name":"Rejected Name","disambiguation":"Rejected Disambiguation","changes":{"sort_name":{"state":"value","value":"Rejected Sort"},"genre":{"state":"value","value":[{"id":"77777777-7777-4777-8777-777777777777","source_name":"unknown","name":"Unknown"}]}}}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid mixed artist metadata PATCH status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body)
	}
	state := readArtist()
	if state.Revision != afterMixed.Revision || state.Name != afterMixed.Name || state.HasDisambiguation != afterMixed.HasDisambiguation || state.Disambiguation != afterMixed.Disambiguation || desiredSort(state) != desiredSort(afterMixed) || string(state.Metadata["local_metadata"]) != string(afterMixed.Metadata["local_metadata"]) {
		t.Fatalf("artist after invalid mixed PATCH = %+v", state)
	}
	response = libraryMetadataRequest(t, handler, http.MethodPatch, path, `{"expected_revision":2,"name":"Stale Name","disambiguation":"Stale Disambiguation","changes":{"sort_name":{"state":"value","value":"Stale Sort"},"genre":{"state":"value","value":[{"id":"66666666-6666-4666-8666-666666666666","source_name":"dream pop","name":"Dream Pop"}]}}}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale mixed artist metadata PATCH status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body)
	}
	state = readArtist()
	if state.Revision != afterMixed.Revision || state.Name != afterMixed.Name || state.HasDisambiguation != afterMixed.HasDisambiguation || state.Disambiguation != afterMixed.Disambiguation || desiredSort(state) != desiredSort(afterMixed) || string(state.Metadata["local_metadata"]) != string(afterMixed.Metadata["local_metadata"]) {
		t.Fatalf("artist after stale mixed PATCH = %+v", state)
	}
	response = libraryMetadataRequest(t, handler, http.MethodPatch, path, `{"expected_revision":3,"name":" \t "}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("blank artist name PATCH status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body)
	}
	state = readArtist()
	if state.Revision != 3 || state.Name != afterMixed.Name || !state.HasDisambiguation || state.Disambiguation != "" || desiredSort(state) != desiredSort(afterMixed) || string(state.Metadata["local_metadata"]) != string(afterMixed.Metadata["local_metadata"]) {
		t.Fatalf("artist after rejected details PATCH = %+v", state)
	}
}
