//go:build integration

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLibraryMetadataLyricsHTTPPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	releaseID := uuid.New()
	trackID, positionID := uuid.New(), uuid.New()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO local_release(id,title,metadata) VALUES(?,?,'{"other":{"keep":true}}')`, releaseID, "Album"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO local_track(id,title) VALUES (?,'Track')`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
		t.Fatal(err)
	}
	path := "/library/releases/" + releaseID.String() + "/positions/" + positionID.String() + "/metadata"
	handler := HandlerWithDependencies(Dependencies{LibraryMetadata: service.NewLibraryMetadataService(persistence.NewLibraryRepository(db))})
	request := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if method == http.MethodPatch {
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	type result struct {
		Envelope librarymetadata.Envelope `json:"envelope"`
		Revision int64                    `json:"revision"`
	}
	read := func() result {
		t.Helper()
		response := request(http.MethodGet, "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET status %d: %s", response.Code, response.Body)
		}
		var got result
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	baseline := read()
	if baseline.Revision != 1 || len(baseline.Envelope.Desired) != 0 {
		t.Fatalf("baseline = %+v", baseline)
	}
	lyrics := "[00:01.00] Привет\r\n[00:02.00] World"
	body, err := json.Marshal(map[string]any{"expected_revision": 1, "changes": map[string]any{"synced_lyrics": map[string]any{"state": "value", "value": lyrics}}})
	if err != nil {
		t.Fatal(err)
	}
	response := request(http.MethodPatch, string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status %d: %s", response.Code, response.Body)
	}
	after := read()
	field := after.Envelope.Desired["synced_lyrics"]
	if after.Revision != 2 || field.State != librarymetadata.StateValue || field.Value != lyrics || field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual {
		t.Fatalf("persisted lyrics/revision = %+v, revision %d", field, after.Revision)
	}

	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"legacy key", `{"expected_revision":2,"changes":{"lyrics":{"state":"value","value":"x"}}}`, http.StatusBadRequest},
		{"forged authority", `{"expected_revision":2,"changes":{"synced_lyrics":{"state":"value","value":"[00:03.00] x","authority":"automatic"}}}`, http.StatusBadRequest},
		{"forged provenance", `{"expected_revision":2,"changes":{"synced_lyrics":{"state":"value","value":"[00:03.00] x","provenance":{"kind":"manual"}}}}`, http.StatusBadRequest},
		{"invalid timestamps", `{"expected_revision":2,"changes":{"synced_lyrics":{"state":"value","value":"not timestamped"}}}`, http.StatusBadRequest},
		{"stale revision", `{"expected_revision":1,"changes":{"synced_lyrics":{"state":"value","value":"[00:03.00] x"}}}`, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := read()
			response := request(http.MethodPatch, test.body)
			if response.Code != test.status {
				t.Fatalf("PATCH status %d want %d: %s", response.Code, test.status, response.Body)
			}
			unchanged := read()
			if unchanged.Revision != before.Revision || unchanged.Envelope.Desired["synced_lyrics"] != before.Envelope.Desired["synced_lyrics"] {
				t.Fatalf("rejected PATCH mutated metadata: before=%+v after=%+v", before, unchanged)
			}
		})
	}
	response = request(http.MethodPatch, `{"expected_revision":2,"changes":{"synced_lyrics":{"state":"explicit_empty"}}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("explicit-empty PATCH status %d: %s", response.Code, response.Body)
	}
	var emptied result
	if err := json.Unmarshal(response.Body.Bytes(), &emptied); err != nil {
		t.Fatal(err)
	}
	for _, observed := range []result{emptied, read()} {
		field := observed.Envelope.Desired["synced_lyrics"]
		if observed.Revision != 3 || field.State != librarymetadata.StateExplicitEmpty || field.Value != nil || field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual {
			t.Fatalf("explicit-empty round trip=%+v", observed)
		}
	}
}
