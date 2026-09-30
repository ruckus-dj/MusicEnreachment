package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// TestOperationEndpointsExposeTheScanTargetSourceRootID pins the wire contract a
// root page depends on: GET /operations and GET /operations/{id} carry the
// source root of a scan, so a page opened for one root can tell its scan apart
// from a scan of another root, while install and move responses keep the field
// omitted.
func TestOperationEndpointsExposeTheScanTargetSourceRootID(t *testing.T) {
	rootID, installationID := uuid.New(), uuid.New()
	scanID, installID, moveID := uuid.New(), uuid.New(), uuid.New()
	repository := &transitionOperationsRepository{rows: map[uuid.UUID]*persistence.Operation{
		scanID: {
			ID: scanID, Kind: service.SourceScanOperationKind, State: "running", Stage: service.SourceScanStageTraversing,
			TargetSourceRootID: &rootID,
		},
		installID: {
			ID: installID, Kind: "install", State: "running", Stage: "download",
			TargetInstallationID: &installationID,
		},
		moveID: {ID: moveID, Kind: "move_tools_root", State: "running", Stage: "moving"},
	}}
	router := chi.NewRouter()
	huma := api.New(router)
	api.RegisterAll(huma, api.Dependencies{Operations: service.NewOperations(repository)})

	scan := decodeOperationBody(t, transitionRequest(t, router, http.MethodGet, "/operations/"+scanID.String(), ""))
	if scan["target_source_root_id"] != rootID.String() {
		t.Fatalf("scan target_source_root_id = %v, want %s", scan["target_source_root_id"], rootID)
	}

	install := decodeOperationBody(t, transitionRequest(t, router, http.MethodGet, "/operations/"+installID.String(), ""))
	if _, present := install["target_source_root_id"]; present {
		t.Fatalf("install response carries target_source_root_id = %v, want the field omitted", install["target_source_root_id"])
	}
	if install["target_installation_id"] != installationID.String() {
		t.Fatalf("install target_installation_id = %v, want %s", install["target_installation_id"], installationID)
	}

	move := decodeOperationBody(t, transitionRequest(t, router, http.MethodGet, "/operations/"+moveID.String(), ""))
	if _, present := move["target_source_root_id"]; present {
		t.Fatalf("move response carries target_source_root_id = %v, want the field omitted", move["target_source_root_id"])
	}

	list := transitionRequest(t, router, http.MethodGet, "/operations", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var page struct {
		Operations []map[string]any `json:"operations"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode the operations page %q: %v", list.Body.String(), err)
	}
	seen := map[string]bool{}
	for _, operation := range page.Operations {
		id, _ := operation["id"].(string)
		seen[id] = true
		switch id {
		case scanID.String():
			if operation["target_source_root_id"] != rootID.String() {
				t.Fatalf("listed scan target_source_root_id = %v, want %s", operation["target_source_root_id"], rootID)
			}
		case installID.String(), moveID.String():
			if value, present := operation["target_source_root_id"]; present {
				t.Fatalf("listed %s carries target_source_root_id = %v, want the field omitted", operation["kind"], value)
			}
		}
	}
	if !seen[scanID.String()] || !seen[installID.String()] || !seen[moveID.String()] {
		t.Fatalf("listed operations omit the fixtures: %v", seen)
	}
}

func decodeOperationBody(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode operation body %q: %v", response.Body.String(), err)
	}
	return decoded
}
