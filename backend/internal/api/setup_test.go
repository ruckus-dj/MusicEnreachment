package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type apiSettingsStore map[string]string

type failingSettingsStore struct {
	apiSettingsStore
}

func (failingSettingsStore) SetMany(context.Context, map[string]string) error {
	return fmt.Errorf("database secret: private-credential")
}

func (store failingSettingsStore) Set(ctx context.Context, key, value string) error {
	if key == settings.MusicBrainzVerifiedAtKey {
		return fmt.Errorf("database secret: private-credential")
	}
	return store.apiSettingsStore.Set(ctx, key, value)
}

type successfulMusicBrainzChecker struct{}

func (successfulMusicBrainzChecker) CheckConnectivity(context.Context, string, string) musicbrainz.CheckResult {
	return musicbrainz.CheckResult{Success: true}
}

func (store apiSettingsStore) Get(_ context.Context, key string) (string, bool, error) {
	value, exists := store[key]
	return value, exists, nil
}

func (store apiSettingsStore) Set(_ context.Context, key, value string) error {
	store[key] = value
	return nil
}

func (store apiSettingsStore) SetMany(_ context.Context, values map[string]string) error {
	for key, value := range values {
		store[key] = value
	}
	return nil
}

func (store apiSettingsStore) SetIfAbsent(_ context.Context, key, value string) (string, error) {
	if current, exists := store[key]; exists {
		return current, nil
	}
	store[key] = value
	return value, nil
}

func (store apiSettingsStore) InitializePlatform(_ context.Context, goos, goarch string) (string, string, bool, error) {
	persistedOS, hasOS := store[settings.PlatformGOOSKey]
	persistedArch, hasArch := store[settings.PlatformGOARCHKey]
	if hasOS != hasArch {
		return persistedOS, persistedArch, false, nil
	}
	if !hasOS {
		store[settings.PlatformGOOSKey] = goos
		store[settings.PlatformGOARCHKey] = goarch
		return goos, goarch, true, nil
	}
	return persistedOS, persistedArch, true, nil
}

func TestSetupMutationRoutesCloseAfterCompletion(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	handler := api.HandlerWithSetup(setup)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/setup/runtime", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("setup mutation status = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body.String())
	}

	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/setup/runtime"},
		{http.MethodPost, "/setup/paths/check"},
	} {
		response = httptest.NewRecorder()
		request = httptest.NewRequest(test.method, test.path, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("post-completion %s %s status=%d, want %d: %s", test.method, test.path, response.Code, http.StatusNotFound, response.Body.String())
		}
	}
}

func TestCheckSetupPathsValidatesWithoutSaving(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	handler := api.HandlerWithSetup(setup)
	root := t.TempDir()
	toolsRoot := filepath.Join(root, "tools")
	outputRoot := filepath.Join(root, "output")
	for _, test := range []struct {
		name   string
		output string
		want   int
	}{
		{"valid", outputRoot, http.StatusOK},
		{"overlap", filepath.Join(toolsRoot, "music"), http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{
				"tools_directory": toolsRoot, "output_directory": test.output,
			})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/setup/paths/check", strings.NewReader(string(body)))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if response.Code == http.StatusOK && (!strings.Contains(response.Body.String(), toolsRoot) || !strings.Contains(response.Body.String(), outputRoot)) {
				t.Fatalf("normalized checked paths absent: %s", response.Body.String())
			}
			if response.Code == http.StatusOK {
				var checked api.CheckPathsBody
				if err := json.Unmarshal(response.Body.Bytes(), &checked); err != nil {
					t.Fatal(err)
				}
				if checked.OutputUnicodeNormalization == "" {
					t.Fatalf("output filesystem semantics absent: %s", response.Body.String())
				}
			}
			if len(store) != 0 {
				t.Fatalf("path check persisted settings: %v", store)
			}
			for _, path := range []string{toolsRoot, test.output} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("path check created directory %s: %v", path, err)
				}
			}
		})
	}
}

func TestSetupMutationDoesNotExposeStorageError(t *testing.T) {
	store := failingSettingsStore{apiSettingsStore{}}
	registry := settings.New(store, nil)
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/setup/musicbrainz", strings.NewReader(`{"mode":"public","base_url":""}`))
	request.Header.Set("Content-Type", "application/json")
	api.HandlerWithSetup(setup).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private-credential") {
		t.Fatalf("storage error leaked into API response: %s", response.Body.String())
	}
}

func TestMusicBrainzCheckDoesNotExposeStorageError(t *testing.T) {
	store := failingSettingsStore{apiSettingsStore{}}
	registry := settings.New(store, nil)
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, successfulMusicBrainzChecker{})
	handler := api.HandlerWithSetup(setup)
	for _, path := range []string{"/setup/check-musicbrainz", "/settings/musicbrainz/check"} {
		response := httptest.NewRecorder()
		if path == "/settings/musicbrainz/check" {
			store.apiSettingsStore[settings.SetupCompletedAtKey] = time.Now().UTC().Format(time.RFC3339Nano)
		}
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "private-credential") {
			t.Errorf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestSettingsAndMoveRoutesUseSetupCompletionGates(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	handler := api.HandlerWithSetup(setup)

	requests := []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{http.MethodPut, "/settings/runtime", `{}`, http.StatusConflict},
		{http.MethodPost, "/tools/move/preflight", `{"new_tools_directory":"/new-tools","remove_old_files":false}`, http.StatusConflict},
		{http.MethodDelete, "/tools/installations/" + uuid.NewString(), `{"package_kind":"ffmpeg"}`, http.StatusConflict},
	}
	for _, test := range requests {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("%s %s status=%d, want %d: %s", test.method, test.path, response.Code, test.want, response.Body.String())
		}
	}

	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	requests = []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{http.MethodPut, "/setup/runtime", `{}`, http.StatusNotFound},
		{http.MethodPut, "/settings/runtime", `{}`, http.StatusNoContent},
		{http.MethodPost, "/tools/move/preflight", `{"new_tools_directory":"/new-tools","remove_old_files":false}`, http.StatusServiceUnavailable},
	}
	for _, test := range requests {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("%s %s status=%d, want %d: %s", test.method, test.path, response.Code, test.want, response.Body.String())
		}
	}
}

func TestPlatformDiagnosticBlocksSetupMutations(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	setup := service.NewSetup(store, registry, settings.PlatformState{
		Platform:   settings.Platform{GOOS: "linux", GOARCH: "amd64"},
		Diagnostic: true, Reason: "instance platform mismatch",
	}, nil, nil)
	for _, path := range []string{"/setup/complete", "/setup/paths/check"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		api.HandlerWithSetup(setup).ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("diagnostic %s status=%d, want %d: %s", path, response.Code, http.StatusServiceUnavailable, response.Body.String())
		}
	}
}

func TestSettingsMutationAppliesDynamicLogLevel(t *testing.T) {
	store := apiSettingsStore{}
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	registry := settings.New(store, level)
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := api.HandlerWithSetup(setup)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/settings/log-level", strings.NewReader(`{"level":"debug"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || level.Level() != slog.LevelDebug {
		t.Fatalf("settings mutation status=%d level=%v", response.Code, level.Level())
	}
}

type apiCatalogFixture struct{}

func (apiCatalogFixture) List(_ context.Context, kind tools.PackageKind, _ tools.Platform) ([]tools.Release, error) {
	if kind == tools.PackageFFmpeg {
		return []tools.Release{{
			Identity: "8.0",
			Artifacts: []tools.Artifact{
				{Name: "ffmpeg.zip", URL: "https://ffmpeg.martin-riedl.de/ffmpeg.zip"},
				{Name: "ffprobe.zip", URL: "https://ffmpeg.martin-riedl.de/ffprobe.zip"},
			},
		}}, nil
	}
	return []tools.Release{{
		Identity: "1.6.1",
		Artifacts: []tools.Artifact{{
			Name: "fpcalc.zip", URL: "https://github.com/acoustid/fpcalc.zip",
		}},
	}}, nil
}

func (apiCatalogFixture) Resolve(context.Context, tools.PackageKind, tools.Platform, string) (tools.Release, error) {
	return tools.Release{}, nil
}

func TestCatalogResponseDoesNotExposeArtifactURLs(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	catalog := service.NewCatalogService(apiCatalogFixture{}, tools.Platform{GOOS: "linux", GOARCH: "amd64"})
	handler := api.HandlerWithDependencies(api.Dependencies{Setup: setup, Catalog: catalog})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tools/catalog?package_kind=fpcalc", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "github.com/acoustid/fpcalc.zip") || !strings.Contains(response.Body.String(), `"name":"fpcalc.zip"`) {
		t.Fatalf("catalog response leaked or omitted artifact metadata: %s", response.Body.String())
	}
}

func TestCatalogResponseIncludesMacOSIntelNotice(t *testing.T) {
	store := apiSettingsStore{}
	platform := tools.Platform{GOOS: "darwin", GOARCH: "amd64"}
	setup := service.NewSetup(store, settings.New(store, nil),
		settings.PlatformState{Platform: settings.Platform{GOOS: "darwin", GOARCH: "amd64"}}, nil, nil)
	catalog := service.NewCatalogService(apiCatalogFixture{}, platform)
	response := httptest.NewRecorder()
	api.HandlerWithDependencies(api.Dependencies{Setup: setup, Catalog: catalog}).
		ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tools/catalog?package_kind=ffmpeg", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("macOS Intel catalog status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body api.CatalogBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Platform.GOOS != platform.GOOS || body.Platform.GOARCH != platform.GOARCH || body.Notice == "" {
		t.Fatalf("macOS Intel catalog platform = %s/%s, notice present = %t", body.Platform.GOOS, body.Platform.GOARCH, body.Notice != "")
	}
}

func TestOpenAPIGeneratorRegistrationContainsProductionOperations(t *testing.T) {
	humaAPI := api.New(chi.NewRouter())
	api.RegisterAll(humaAPI, api.Dependencies{})
	encoded, err := json.Marshal(humaAPI.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	spec := string(encoded)
	for _, operationID := range []string{
		"get-setup-state", "save-setup-runtime", "check-setup-paths", "complete-setup",
		"list-tool-catalog", "preflight-tool-install", "start-tool-install",
		"list-installations", "activate-tool-installation", "delete-tool-installation",
		"preflight-tools-root-move", "start-tools-root-move",
		"list-operations", "get-operation", "retry-operation", "dismiss-operation",
		"update-log-level", "update-lrclib-setting", "update-musicbrainz-settings",
	} {
		if !strings.Contains(spec, `"operationId":"`+operationID+`"`) {
			t.Errorf("OpenAPI contract omits operation ID %q", operationID)
		}
	}
}

type operationRepositoryFixture struct {
	mu         sync.Mutex
	operations map[uuid.UUID]*persistence.Operation
}

func (repository *operationRepositoryFixture) CreateOperation(_ context.Context, operation *persistence.Operation) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.operations[operation.ID] = operation
	return nil
}

func (repository *operationRepositoryFixture) GetOperation(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	operation := repository.operations[id]
	if operation == nil {
		return nil, fmt.Errorf("operation not found")
	}
	return operation, nil
}

func (repository *operationRepositoryFixture) ListOperations(_ context.Context, states ...string) ([]persistence.Operation, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := make([]persistence.Operation, 0, len(repository.operations))
	for _, operation := range repository.operations {
		if len(states) == 0 || containsString(states, operation.State) {
			result = append(result, *operation)
		}
	}
	return result, nil
}

func (repository *operationRepositoryFixture) UpdateOperation(_ context.Context, operation *persistence.Operation) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.operations[operation.ID] = operation
	return nil
}

func (repository *operationRepositoryFixture) TransitionOperation(_ context.Context, id uuid.UUID, transition func(*persistence.Operation) error) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	operation := repository.operations[id]
	if operation == nil {
		return fmt.Errorf("operation not found")
	}
	return transition(operation)
}

func (*operationRepositoryFixture) DismissOperation(context.Context, uuid.UUID) error {
	return nil
}

func (*operationRepositoryFixture) DeleteSucceededBefore(context.Context, time.Time) error {
	return nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func TestOperationSSEEmitsOnlyWakeupAndSnapshotCanBeRefetched(t *testing.T) {
	id := uuid.New()
	repository := &operationRepositoryFixture{operations: map[uuid.UUID]*persistence.Operation{
		id: {ID: id, Kind: "install", State: "queued", Stage: "queued", InputSnapshot: json.RawMessage(`{"target_identity":"ffmpeg:test:8.0:linux:amd64"}`)},
	}}
	operations := service.NewOperations(repository)
	server := httptest.NewServer(api.HandlerWithDependencies(api.Dependencies{Operations: operations}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/operations/"+id.String()+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") == "" {
		t.Fatalf("SSE response status=%d headers=%v", response.StatusCode, response.Header)
	}

	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	if err := operations.Running(ctx, id, "download"); err != nil {
		t.Fatal(err)
	}
	var event, data string
	for event == "" || data == "" {
		select {
		case line := <-lines:
			if strings.HasPrefix(line, "event: ") {
				event = line
			}
			if strings.HasPrefix(line, "data: ") {
				data = line
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for SSE notification: %v", ctx.Err())
		}
	}
	if event != "event: operation-changed" || data != "data: "+id.String() {
		t.Fatalf("SSE payload = %q / %q", event, data)
	}

	snapshotResponse, err := http.Get(server.URL + "/operations/" + id.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshotResponse.Body.Close() }()
	if snapshotResponse.StatusCode != http.StatusOK {
		t.Fatalf("REST snapshot status = %d", snapshotResponse.StatusCode)
	}
}
