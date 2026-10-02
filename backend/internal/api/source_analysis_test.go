package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func analysisPath(rootID, locationID uuid.UUID) string {
	return "/sources/" + rootID.String() + "/locations/" + locationID.String() + "/analyze"
}

func decodeOperation(t *testing.T, response *httptest.ResponseRecorder) api.OperationResponse {
	t.Helper()
	var operation api.OperationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &operation); err != nil {
		t.Fatalf("decode the operation: %v: %s", err, response.Body.String())
	}
	return operation
}

func TestAnalyzeSourceLocationReturnsQueuedSnapshotWithTargets(t *testing.T) {
	platform := supportedSourcePlatform()
	fixture := newSourceAnalysisAPIFixture(t, platform, true)
	fixture.activateFFmpeg(t, platform)
	root := fixture.seedAnalysisRoot(t, nil)
	location := analysisLocation(root.ID, "disc/track.flac", time.Now().UTC())
	fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}

	response := sourceRequest(t, fixture.handler, http.MethodPost, analysisPath(root.ID, location.ID),
		analyzeBody(location.SizeBytes, location.Mtime.Format(time.RFC3339Nano)))
	if response.Code != http.StatusAccepted {
		t.Fatalf("start status=%d, want 202: %s", response.Code, response.Body.String())
	}
	operation := decodeOperation(t, response)
	if operation.Kind != service.SourceAnalysisOperationKind || operation.State != "queued" ||
		operation.Stage != service.SourceAnalysisStageQueued {
		t.Fatalf("started operation = %+v", operation)
	}
	if operation.TargetSourceRootID == nil || *operation.TargetSourceRootID != root.ID ||
		operation.TargetSourceLocationID == nil || *operation.TargetSourceLocationID != location.ID {
		t.Fatalf("operation targets = %+v", operation)
	}
	stored, err := fixture.operations.GetOperation(context.Background(), operation.ID)
	if err != nil || stored.TargetSourceLocationID == nil || *stored.TargetSourceLocationID != location.ID {
		t.Fatalf("stored operation = %+v, %v", stored, err)
	}
	if len(fixture.repository.locations[root.ID]) != 1 {
		t.Fatal("a start changed the inventory")
	}
}

func TestAnalyzeSourceLocationValidatesObservedIdentityOnly(t *testing.T) {
	platform := supportedSourcePlatform()
	fixture := newSourceAnalysisAPIFixture(t, platform, true)
	fixture.activateFFmpeg(t, platform)
	root := fixture.seedAnalysisRoot(t, nil)
	location := analysisLocation(root.ID, "disc/track.flac", time.Now().UTC())
	fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}
	path := analysisPath(root.ID, location.ID)

	for _, test := range []struct {
		name string
		body string
	}{
		{"negative size", `{"expected_size_bytes":-1,"expected_mtime":"2026-09-02T08:30:00Z"}`},
		{"missing size", `{"expected_mtime":"2026-09-02T08:30:00Z"}`},
		{"missing mtime", `{"expected_size_bytes":2048}`},
		{"unparsable mtime", `{"expected_size_bytes":2048,"expected_mtime":"soon"}`},
		{"arbitrary filesystem path", `{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z","path":"/etc/passwd"}`},
		{"arbitrary tool input", `{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z","tool_path":"/bin/sh"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := sourceRequest(t, fixture.handler, http.MethodPost, path, test.body)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d, want 422: %s", response.Code, response.Body.String())
			}
		})
	}
	if len(fixture.operations.operations) != 0 {
		t.Fatalf("an invalid body created %d operations", len(fixture.operations.operations))
	}
}

func TestAnalyzeSourceLocationRefusals(t *testing.T) {
	platform := supportedSourcePlatform()

	t.Run("unknown root", func(t *testing.T) {
		fixture := newSourceAnalysisAPIFixture(t, platform, true)
		response := sourceRequest(t, fixture.handler, http.MethodPost, analysisPath(uuid.New(), uuid.New()),
			analyzeBody(2048, "2026-09-02T08:30:00Z"))
		if response.Code != http.StatusNotFound {
			t.Fatalf("status=%d, want 404: %s", response.Code, response.Body.String())
		}
	})

	t.Run("foreign location", func(t *testing.T) {
		fixture := newSourceAnalysisAPIFixture(t, platform, true)
		root := fixture.seedAnalysisRoot(t, nil)
		foreignRoot := fixture.seedAnalysisRoot(t, nil)
		foreign := analysisLocation(foreignRoot.ID, "other.flac", time.Now().UTC())
		fixture.repository.locations[foreignRoot.ID] = []persistence.SourceLocation{foreign}
		response := sourceRequest(t, fixture.handler, http.MethodPost, analysisPath(root.ID, foreign.ID),
			analyzeBody(foreign.SizeBytes, foreign.Mtime.Format(time.RFC3339Nano)))
		if response.Code != http.StatusNotFound {
			t.Fatalf("status=%d, want 404: %s", response.Code, response.Body.String())
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*persistence.SourceLocation, *persistence.SourceRoot)
	}{
		{"changed size", func(location *persistence.SourceLocation, _ *persistence.SourceRoot) { location.SizeBytes += 1 }},
		{"not audio", func(location *persistence.SourceLocation, _ *persistence.SourceRoot) {
			location.ProbeStatus = persistence.SourceProbeStatusNoAudio
		}},
		{"disabled root", func(_ *persistence.SourceLocation, root *persistence.SourceRoot) { root.Enabled = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSourceAnalysisAPIFixture(t, platform, true)
			fixture.activateFFmpeg(t, platform)
			root := fixture.seedAnalysisRoot(t, nil)
			location := analysisLocation(root.ID, "disc/track.flac", time.Now().UTC())
			expected := location
			test.mutate(&location, root)
			fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}
			response := sourceRequest(t, fixture.handler, http.MethodPost, analysisPath(root.ID, location.ID),
				analyzeBody(2048, expected.Mtime.Format(time.RFC3339Nano)))
			if response.Code != http.StatusConflict {
				t.Fatalf("status=%d, want 409: %s", response.Code, response.Body.String())
			}
			if len(fixture.operations.operations) != 0 {
				t.Fatal("a refused start created an operation")
			}
		})
	}

	t.Run("root has an active operation", func(t *testing.T) {
		fixture := newSourceAnalysisAPIFixture(t, platform, true)
		fixture.activateFFmpeg(t, platform)
		root := fixture.seedAnalysisRoot(t, nil)
		location := analysisLocation(root.ID, "disc/track.flac", time.Now().UTC())
		fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}
		active := &persistence.Operation{
			ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: "queued", Stage: service.SourceAnalysisStageQueued,
			TargetSourceRootID: &root.ID, TargetSourceLocationID: &location.ID,
		}
		if err := fixture.operations.CreateOperation(context.Background(), active); err != nil {
			t.Fatal(err)
		}
		response := sourceRequest(t, fixture.handler, http.MethodPost, analysisPath(root.ID, location.ID),
			analyzeBody(location.SizeBytes, location.Mtime.Format(time.RFC3339Nano)))
		if response.Code != http.StatusConflict {
			t.Fatalf("status=%d, want 409: %s", response.Code, response.Body.String())
		}
	})

	t.Run("no managed tool", func(t *testing.T) {
		fixture := newSourceAnalysisAPIFixture(t, platform, true)
		root := fixture.seedAnalysisRoot(t, nil)
		location := analysisLocation(root.ID, "disc/track.flac", time.Now().UTC())
		fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}
		response := sourceRequest(t, fixture.handler, http.MethodPost, analysisPath(root.ID, location.ID),
			analyzeBody(location.SizeBytes, location.Mtime.Format(time.RFC3339Nano)))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want 503: %s", response.Code, response.Body.String())
		}
	})
}

func TestAnalyzeSourceLocationAllowsRepeatAfterTerminalAndKeepsResult(t *testing.T) {
	platform := supportedSourcePlatform()
	fixture := newSourceAnalysisAPIFixture(t, platform, true)
	fixture.activateFFmpeg(t, platform)
	root := fixture.seedAnalysisRoot(t, nil)
	location := analysisLocation(root.ID, "disc/track.flac", time.Now().UTC())
	previous := uuid.New()
	fixture.repository.variants[previous] = &persistence.MediaVariant{
		ID: previous, SizeBytes: location.SizeBytes, AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion,
		FFProbeVersion: "6.1.1", FFProbeJSON: json.RawMessage(technicalResultFixture),
		ObservedTags: json.RawMessage(`{}`), InspectedAt: time.Now().UTC(), AppliedOperationID: uuid.New(),
	}
	location.MediaVariantID = &previous
	fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}
	body := analyzeBody(location.SizeBytes, location.Mtime.Format(time.RFC3339Nano))

	first := sourceRequest(t, fixture.handler, http.MethodPost, analysisPath(root.ID, location.ID), body)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first start status=%d: %s", first.Code, first.Body.String())
	}
	firstOperation := decodeOperation(t, first)

	detail := decodeSourceLocationDetail(t, sourceRequest(t, fixture.handler, http.MethodGet,
		"/sources/"+root.ID.String()+"/locations/"+location.ID.String(), ""))
	if detail.MediaVariantID == nil || *detail.MediaVariantID != previous || detail.ActiveAnalysisOperationID == nil ||
		*detail.ActiveAnalysisOperationID != firstOperation.ID {
		t.Fatalf("detail during the first analysis = %+v", detail)
	}

	fixture.operations.operations[firstOperation.ID].State = "succeeded"

	second := sourceRequest(t, fixture.handler, http.MethodPost, analysisPath(root.ID, location.ID), body)
	if second.Code != http.StatusAccepted {
		t.Fatalf("repeat start status=%d: %s", second.Code, second.Body.String())
	}
	secondOperation := decodeOperation(t, second)
	if secondOperation.ID == firstOperation.ID {
		t.Fatal("the repeat reused the terminal operation")
	}

	reloaded := decodeSourceLocationDetail(t, sourceRequest(t, fixture.handler, http.MethodGet,
		"/sources/"+root.ID.String()+"/locations/"+location.ID.String(), ""))
	if reloaded.MediaVariantID == nil || *reloaded.MediaVariantID != previous {
		t.Fatalf("the queued repeat changed the stored result: %+v", reloaded)
	}
	if reloaded.ActiveAnalysisOperationID == nil || *reloaded.ActiveAnalysisOperationID != secondOperation.ID {
		t.Fatalf("active analysis after the repeat = %v, want %s", reloaded.ActiveAnalysisOperationID, secondOperation.ID)
	}

	snapshot := sourceRequest(t, fixture.handler, http.MethodGet, "/operations/"+secondOperation.ID.String(), "")
	if snapshot.Code != http.StatusOK {
		t.Fatalf("operation reload status=%d: %s", snapshot.Code, snapshot.Body.String())
	}
	reconnected := decodeOperation(t, snapshot)
	if reconnected.TargetSourceRootID == nil || *reconnected.TargetSourceRootID != root.ID ||
		reconnected.TargetSourceLocationID == nil || *reconnected.TargetSourceLocationID != location.ID {
		t.Fatalf("reconnected operation targets = %+v", reconnected)
	}

	failed := fixture.operations.operations[secondOperation.ID]
	failed.State = "failed"
	failed.SafeError = sourceStringPointer("the analysis failed safely")
	retry := sourceRequest(t, fixture.handler, http.MethodPost, "/operations/"+secondOperation.ID.String()+"/retry", "")
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status=%d: %s", retry.Code, retry.Body.String())
	}
	retried := decodeOperation(t, retry)
	if retried.State != "queued" || retried.TargetSourceLocationID == nil || *retried.TargetSourceLocationID != location.ID {
		t.Fatalf("retried operation = %+v", retried)
	}
	afterRetry := decodeSourceLocationDetail(t, sourceRequest(t, fixture.handler, http.MethodGet,
		"/sources/"+root.ID.String()+"/locations/"+location.ID.String(), ""))
	if afterRetry.ActiveAnalysisOperationID == nil || *afterRetry.ActiveAnalysisOperationID != secondOperation.ID {
		t.Fatalf("active analysis after retry = %v, want %s", afterRetry.ActiveAnalysisOperationID, secondOperation.ID)
	}
}
