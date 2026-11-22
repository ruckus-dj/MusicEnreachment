package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestListOperationsCanRequestOnlyActiveSourceScans(t *testing.T) {
	rootID := uuid.New()
	scan := persistence.Operation{
		ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "queued",
		TargetSourceRootID: &rootID,
	}
	repository := &activeSourceScanAPIRepository{
		transitionOperationsRepository: &transitionOperationsRepository{},
		scan:                           scan,
	}
	router := chi.NewRouter()
	api.RegisterAll(api.New(router), api.Dependencies{Operations: service.NewOperations(repository)})

	response := decodeOperationBody(t, transitionRequest(t, router, http.MethodGet, "/operations?active_source_scans=true", ""))
	operations, ok := response["operations"].([]any)
	if !ok || len(operations) != 1 {
		t.Fatalf("operations=%#v, want one active scan", response["operations"])
	}
	operation, ok := operations[0].(map[string]any)
	if !ok || operation["id"] != scan.ID.String() || operation["target_source_root_id"] != rootID.String() {
		t.Fatalf("operation=%#v, want scan %s for root %s", operations[0], scan.ID, rootID)
	}
}

type activeSourceScanAPIRepository struct {
	*transitionOperationsRepository
	scan persistence.Operation
}

func (repository *activeSourceScanAPIRepository) ListActiveSourceScanOperations(context.Context) ([]persistence.Operation, error) {
	return []persistence.Operation{repository.scan}, nil
}

func TestListOperationsCanRequestScopedToolDiscovery(t *testing.T) {
	failed := persistence.Operation{ID: uuid.New(), Kind: "install", State: "failed"}
	repository := &toolDiscoveryAPIRepository{
		transitionOperationsRepository: &transitionOperationsRepository{},
		operations:                     []persistence.Operation{failed},
		scan:                           persistence.Operation{ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "queued"},
	}
	router := chi.NewRouter()
	api.RegisterAll(api.New(router), api.Dependencies{Operations: service.NewOperations(repository)})

	response := decodeOperationBody(t, transitionRequest(t, router, http.MethodGet, "/operations?tool_discovery=install_and_move&state=failed", ""))
	operations, ok := response["operations"].([]any)
	if !ok || len(operations) != 1 {
		t.Fatalf("operations=%#v, want one tool operation", response["operations"])
	}
	operation, ok := operations[0].(map[string]any)
	if !ok || operation["id"] != failed.ID.String() {
		t.Fatalf("operation=%#v, want %s", operations[0], failed.ID)
	}
	if !repository.installAndMove || repository.state != "failed" {
		t.Fatalf("tool discovery scope=%v state=%q, want install_and_move and failed", repository.installAndMove, repository.state)
	}

	if response := transitionRequest(t, router, http.MethodGet, "/operations?tool_discovery=install", ""); response.Code != http.StatusOK {
		t.Fatalf("install discovery status=%d body=%s", response.Code, response.Body.String())
	}
	if repository.installAndMove || repository.state != "" {
		t.Fatalf("tool discovery scope=%v state=%q, want install and the fixed state set", repository.installAndMove, repository.state)
	}

	// active_source_scans owns the request; tool discovery is not consulted.
	if response := transitionRequest(t, router, http.MethodGet, "/operations?active_source_scans=true&tool_discovery=install_and_move&state=failed", ""); response.Code != http.StatusOK {
		t.Fatalf("active scan discovery status=%d body=%s", response.Code, response.Body.String())
	}
	if repository.discoveryCalls != 2 || repository.scanCalls != 1 {
		t.Fatalf("discovery calls=%d scan calls=%d, want 2 and 1", repository.discoveryCalls, repository.scanCalls)
	}
}

type toolDiscoveryAPIRepository struct {
	*transitionOperationsRepository
	operations     []persistence.Operation
	scan           persistence.Operation
	installAndMove bool
	state          string
	discoveryCalls int
	scanCalls      int
}

func (repository *toolDiscoveryAPIRepository) ListToolDiscoveryOperations(_ context.Context, installAndMove bool, state string) ([]persistence.Operation, error) {
	repository.discoveryCalls++
	repository.installAndMove = installAndMove
	repository.state = state
	return repository.operations, nil
}

func (repository *toolDiscoveryAPIRepository) ListActiveSourceScanOperations(context.Context) ([]persistence.Operation, error) {
	repository.scanCalls++
	return []persistence.Operation{repository.scan}, nil
}

func TestCleanupOperationDetailUsesOneConsistentRead(t *testing.T) {
	operationID, artifactID := uuid.New(), uuid.New()
	repository := &operationDetailRepository{
		transitionOperationsRepository: &transitionOperationsRepository{rows: map[uuid.UUID]*persistence.Operation{
			operationID: {
				ID: operationID, Kind: service.SourceAnalysisArtifactCleanupOperationKind,
				State: "running", Stage: "cleaning_artifacts",
			},
		}},
	}
	// Simulate a worker settling both rows between two otherwise-independent
	// reads. The detail reader represents one transaction's terminal snapshot.
	finished := time.Now().UTC()
	repository.detail = &persistence.OperationWithCleanupItems{
		Operation: &persistence.Operation{
			ID: operationID, Kind: service.SourceAnalysisArtifactCleanupOperationKind,
			State: "succeeded", Stage: "finished", FinishedAt: &finished,
		},
		CleanupItems: []persistence.SourceAnalysisArtifactCleanupItem{{
			ArtifactID: artifactID, OperationID: operationID, State: "succeeded",
		}},
	}

	router := chi.NewRouter()
	huma := api.New(router)
	api.RegisterAll(huma, api.Dependencies{
		Operations:            service.NewOperations(repository),
		SourceArtifactCleanup: service.NewSourceAnalysisArtifactCleanup(nil, nil),
	})

	response := decodeOperationBody(t, transitionRequest(t, router, http.MethodGet, "/operations/"+operationID.String(), ""))
	if response["state"] != "succeeded" {
		t.Fatalf("operation state = %v, want succeeded", response["state"])
	}
	results, ok := response["cleanup_results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("cleanup_results = %#v, want one terminal result", response["cleanup_results"])
	}
	result, ok := results[0].(map[string]any)
	if !ok || result["state"] != "succeeded" || result["artifact_id"] != artifactID.String() {
		t.Fatalf("cleanup result = %#v, want succeeded result for %s", results[0], artifactID)
	}
	if repository.readCount != 1 {
		t.Fatalf("combined detail reads = %d, want exactly one", repository.readCount)
	}
}

type operationDetailRepository struct {
	*transitionOperationsRepository
	detail    *persistence.OperationWithCleanupItems
	readCount int
}

func (repository *operationDetailRepository) ReadOperationWithCleanupItems(_ context.Context, _ uuid.UUID) (*persistence.OperationWithCleanupItems, error) {
	repository.readCount++
	return repository.detail, nil
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
