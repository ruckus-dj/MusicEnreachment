package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func analysisPath(rootID, locationID uuid.UUID) string {
	return "/sources/" + rootID.String() + "/locations/" + locationID.String()
}

func decodeOperation(t *testing.T, responseBody []byte) api.OperationResponse {
	t.Helper()
	var operation api.OperationResponse
	if err := json.Unmarshal(responseBody, &operation); err != nil {
		t.Fatalf("decode operation: %v: %s", err, responseBody)
	}
	return operation
}

func TestRetrySourceAnalysisStepUsesExplicitFailedStep(t *testing.T) {
	platform := supportedSourcePlatform()
	fixture := newSourceAnalysisAPIFixture(t, platform, true)
	root := fixture.seedAnalysisRoot(t, nil)
	location := analysisLocation(root.ID, "disc/track.flac", time.Now().UTC())
	fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}
	currentLocationID := location.ID
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, CurrentLocationID: &currentLocationID, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, InventoryPath: *root.InventoryPath,
		RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		SHA256Enabled: true,
	}
	fixture.repository.works[location.ID] = work
	fixture.repository.steps[location.ID] = []persistence.SourceAnalysisStep{{WorkID: work.ID, Step: "sha256", State: "failed"}}

	path := analysisPath(root.ID, location.ID) + "/retry"
	body := `{"step":"sha256","expected_size_bytes":2048,"expected_mtime":"` + location.Mtime.Format(time.RFC3339Nano) + `"}`
	response := sourceRequest(t, fixture.handler, http.MethodPost, path, body)
	if response.Code != http.StatusAccepted {
		t.Fatalf("retry status=%d, want 202: %s", response.Code, response.Body.String())
	}
	operation := decodeOperation(t, response.Body.Bytes())
	if operation.Kind != service.SourceAnalysisOperationKind || operation.State != "queued" {
		t.Fatalf("queued operation = %+v", operation)
	}
	if len(fixture.operations.operations) != 1 {
		t.Fatalf("queued operation count = %d, want 1", len(fixture.operations.operations))
	}
	var snapshot persistence.SourceAnalysisOperationSnapshot
	stored := fixture.operations.operations[operation.ID]
	if err := json.Unmarshal(stored.InputSnapshot, &snapshot); err != nil || snapshot.Mode != persistence.SourceAnalysisModeSingleStep || snapshot.TargetStep == nil || *snapshot.TargetStep != "sha256" || snapshot.TargetWorkID == nil || *snapshot.TargetWorkID != work.ID || snapshot.SHA256Enabled != nil || len(snapshot.Tools) != 0 {
		t.Fatalf("snapshot = %+v, decode error = %v", snapshot, err)
	}
}

func TestSourceAnalysisEndpointsRequireExplicitContract(t *testing.T) {
	fixture := newSourceAnalysisAPIFixture(t, supportedSourcePlatform(), true)
	rootID, locationID := uuid.New(), uuid.New()
	base := analysisPath(rootID, locationID)
	for _, test := range []struct {
		name, path, body string
		wantStatus       int
	}{
		{name: "legacy implicit analyze removed", path: base + "/analyze", body: `{"expected_size_bytes":1,"expected_mtime":"2026-09-02T08:30:00Z"}`, wantStatus: http.StatusNotFound},
		{name: "step is required", path: base + "/retry", body: `{"expected_size_bytes":1,"expected_mtime":"2026-09-02T08:30:00Z"}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "step is enumerated", path: base + "/retry", body: `{"step":"ffprobe","expected_size_bytes":1,"expected_mtime":"2026-09-02T08:30:00Z"}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "stat is required for rerun", path: base + "/fingerprint/rerun", body: `{"expected_size_bytes":1}`, wantStatus: http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := sourceRequest(t, fixture.handler, http.MethodPost, test.path, test.body)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}
