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
	if _, err := db.NewRaw(`UPDATE local_artist SET metadata=metadata || '{"genres":["keep genre"],"provenance":{"source":"keep provenance"}}'::jsonb WHERE id=?`, artistID).Exec(ctx); err != nil {
		t.Fatalf("seed artist metadata to preserve: %v", err)
	}
	detailsPath := "/library/artists/" + artistID.String() + "/details"
	response = libraryMetadataRequest(t, handler, http.MethodPatch, detailsPath, `{"expected_revision":2,"name":"  Local Artist  ","disambiguation":""}`)
	if response.Code != http.StatusOK {
		t.Fatalf("artist details PATCH status = %d: %s", response.Code, response.Body)
	}
	var details struct {
		ID                 uuid.UUID `json:"id"`
		Name               string    `json:"name"`
		Disambiguation     *string   `json:"disambiguation"`
		Revision           int64     `json:"revision"`
		PublicationApplied bool      `json:"publication_applied"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	if details.ID != artistID || details.Name != "  Local Artist  " || details.Disambiguation == nil || *details.Disambiguation != "" || details.Revision != 3 || details.PublicationApplied {
		t.Fatalf("artist details PATCH response = %+v", details)
	}
	var metadataJSON string
	if err := db.NewRaw(`SELECT metadata::text FROM local_artist WHERE id=?`, artistID).Scan(ctx, &metadataJSON); err != nil {
		t.Fatalf("read artist metadata after details PATCH: %v", err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatal(err)
	}
	var preservedGenres []string
	var preservedProvenance struct {
		Source string `json:"source"`
	}
	var preservedDisambiguation string
	if err := json.Unmarshal(metadata["genres"], &preservedGenres); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(metadata["provenance"], &preservedProvenance); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(metadata["disambiguation"], &preservedDisambiguation); err != nil {
		t.Fatal(err)
	}
	if len(preservedGenres) != 1 || preservedGenres[0] != "keep genre" || preservedProvenance.Source != "keep provenance" || len(metadata["local_metadata"]) == 0 || preservedDisambiguation != "" {
		t.Fatalf("artist metadata after details PATCH = %s", metadataJSON)
	}
	response = libraryMetadataRequest(t, handler, http.MethodPatch, path, `{"expected_revision":2,"changes":{"genre":{"state":"value","value":[{"id":"66666666-6666-4666-8666-666666666666","source_name":"dream pop","name":"Dream Pop"}]}}}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale artist metadata PATCH status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body)
	}
	response = libraryMetadataRequest(t, handler, http.MethodPatch, detailsPath, `{"expected_revision":3,"name":" \t "}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("blank artist name PATCH status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body)
	}
	var revision int64
	if err := db.NewRaw(`SELECT metadata_revision FROM local_artist WHERE id=?`, artistID).Scan(ctx, &revision); err != nil {
		t.Fatalf("read artist revision after rejected details PATCH: %v", err)
	}
	if revision != 3 {
		t.Fatalf("artist revision after rejected details PATCH = %d, want 3", revision)
	}
}
