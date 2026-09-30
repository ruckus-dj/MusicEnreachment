package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type sourceAPIManagedPaths struct {
	tools  string
	output string
}

func (paths sourceAPIManagedPaths) GetToolsDirectory(context.Context) (string, bool, error) {
	return paths.tools, paths.tools != "", nil
}

func (paths sourceAPIManagedPaths) GetOutputDirectory(context.Context) (string, bool, error) {
	return paths.output, paths.output != "", nil
}

// sourceAPIRiverInserter fails on use: a scan start enqueues its River job
// inside the transaction that stores the operation, so the service must never
// call the client itself.
type sourceAPIRiverInserter struct{}

func (sourceAPIRiverInserter) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("a scan start must enqueue through its repository")
}

// sourceAPIRepository is the in-memory store behind the source endpoints. It
// implements the root, location and scan-start contracts one PostgreSQL
// repository implements in production; the page ordering and the confirmation
// state the handlers read are mirrored here. Durability, locks and uniqueness
// are properties of PostgreSQL and are proven by the persistence integration
// tests, not by this fake.
type sourceAPIRepository struct {
	operations *operationRepositoryFixture
	roots      []*persistence.SourceRoot
	locations  map[uuid.UUID][]persistence.SourceLocation
	candidates map[uuid.UUID][]persistence.SourceScanCandidate
	busy       bool
	disabled   bool
	lastLimit  int
}

func (repository *sourceAPIRepository) CreateSourceRoot(_ context.Context, root *persistence.SourceRoot) error {
	if root.ID == uuid.Nil {
		root.ID = uuid.New()
	}
	// The real repository normalizes the status of a new root; the API reports it.
	if root.Status == "" {
		root.Status = "unknown"
	}
	repository.roots = append(repository.roots, root)
	return nil
}

func (repository *sourceAPIRepository) GetSourceRoot(_ context.Context, id uuid.UUID) (*persistence.SourceRoot, error) {
	for _, root := range repository.roots {
		if root.ID == id {
			return root, nil
		}
	}
	return nil, fmt.Errorf("get source root: %w", sql.ErrNoRows)
}

func (repository *sourceAPIRepository) ListSourceRoots(context.Context) ([]persistence.SourceRoot, error) {
	roots := make([]persistence.SourceRoot, 0, len(repository.roots))
	for _, root := range repository.roots {
		roots = append(roots, *root)
	}
	return roots, nil
}

func (repository *sourceAPIRepository) UpdateSourceRoot(_ context.Context, root *persistence.SourceRoot) error {
	if repository.busy {
		return fmt.Errorf("update source root: %w", persistence.ErrSourceRootActiveScan)
	}
	for index, stored := range repository.roots {
		if stored.ID == root.ID {
			repository.roots[index] = root
			return nil
		}
	}
	return fmt.Errorf("update source root: root does not exist")
}

func (repository *sourceAPIRepository) DeleteSourceRoot(_ context.Context, id uuid.UUID) error {
	if repository.busy {
		return fmt.Errorf("delete source root: %w", persistence.ErrSourceRootActiveScan)
	}
	for index, root := range repository.roots {
		if root.ID == id {
			repository.roots = append(repository.roots[:index], repository.roots[index+1:]...)
			delete(repository.locations, id)
			return nil
		}
	}
	return fmt.Errorf("delete source root: root does not exist")
}

func (repository *sourceAPIRepository) CountSourceLocations(_ context.Context, id uuid.UUID) (int64, error) {
	return int64(len(repository.locations[id])), nil
}

func (repository *sourceAPIRepository) ListSourceLocationsPage(_ context.Context, rootID uuid.UUID, cursor *persistence.SourceLocationCursor, limit int) ([]persistence.SourceLocation, *persistence.SourceLocationCursor, error) {
	repository.lastLimit = limit
	ordered := sourceAPILocationOrder(repository.locations[rootID])
	page := make([]persistence.SourceLocation, 0, limit)
	for _, location := range ordered {
		if !sourceAPILocationAfterCursor(location, cursor) {
			continue
		}
		page = append(page, location)
		if len(page) == limit {
			break
		}
	}
	if len(page) < limit {
		return page, nil, nil
	}
	last := page[len(page)-1]
	return page, &persistence.SourceLocationCursor{RelativePath: last.RelativePath, ID: last.ID}, nil
}

func (repository *sourceAPIRepository) CreateSourceScanOperationAndEnqueue(ctx context.Context, operation *persistence.Operation, _ persistence.RiverInserter, _ river.JobArgs, _ *river.InsertOpts) error {
	if repository.disabled {
		return fmt.Errorf("create scan operation: %w", persistence.ErrSourceRootDisabled)
	}
	if repository.busy {
		return fmt.Errorf("create scan operation: %w", persistence.ErrSourceRootActiveScan)
	}
	return repository.operations.CreateOperation(ctx, operation)
}

func sourceAPILocationOrder(locations []persistence.SourceLocation) []persistence.SourceLocation {
	ordered := slices.Clone(locations)
	slices.SortFunc(ordered, func(first, second persistence.SourceLocation) int {
		if first.RelativePath != second.RelativePath {
			return strings.Compare(first.RelativePath, second.RelativePath)
		}
		return strings.Compare(first.ID.String(), second.ID.String())
	})
	return ordered
}

func sourceAPILocationAfterCursor(location persistence.SourceLocation, cursor *persistence.SourceLocationCursor) bool {
	if cursor == nil {
		return true
	}
	if location.RelativePath != cursor.RelativePath {
		return location.RelativePath > cursor.RelativePath
	}
	return strings.Compare(location.ID.String(), cursor.ID.String()) > 0
}

type sourcesAPIFixture struct {
	handler    http.Handler
	repository *sourceAPIRepository
	operations *operationRepositoryFixture
	outputRoot string
}

func supportedSourcePlatform() settings.PlatformState {
	return settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}
}

func newSourcesAPIFixture(t *testing.T, platform settings.PlatformState, completeSetup bool) sourcesAPIFixture {
	t.Helper()
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if completeSetup {
		if err := registry.CompleteSetup(context.Background()); err != nil {
			t.Fatalf("complete the first-run setup: %v", err)
		}
	}
	operations := &operationRepositoryFixture{operations: map[uuid.UUID]*persistence.Operation{}}
	repository := &sourceAPIRepository{
		operations: operations, locations: map[uuid.UUID][]persistence.SourceLocation{},
		candidates: map[uuid.UUID][]persistence.SourceScanCandidate{},
	}
	toolsRoot, outputRoot := t.TempDir(), t.TempDir()
	roots := service.NewSourceRoots(repository, sourceAPIManagedPaths{tools: toolsRoot, output: outputRoot})
	scans := service.NewSourceScanOperations(repository, roots, registry, platform, sourceAPIRiverInserter{})
	handler := api.HandlerWithDependencies(api.Dependencies{
		Setup:           service.NewSetup(store, registry, platform, nil, nil),
		SourceRoots:     roots,
		SourceLocations: service.NewSourceLocations(repository),
		SourceScan:      scans,
		Operations:      service.NewOperations(operations),
	})
	return sourcesAPIFixture{handler: handler, repository: repository, operations: operations, outputRoot: outputRoot}
}

func sourceRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeSourceRoot(t *testing.T, response *httptest.ResponseRecorder) api.SourceRootResponse {
	t.Helper()
	var root api.SourceRootResponse
	if err := json.Unmarshal(response.Body.Bytes(), &root); err != nil {
		t.Fatalf("decode the source root response: %v: %s", err, response.Body.String())
	}
	return root
}

func decodeSourceLocations(t *testing.T, response *httptest.ResponseRecorder) api.SourceLocationsBody {
	t.Helper()
	var page api.SourceLocationsBody
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode the source locations response: %v: %s", err, response.Body.String())
	}
	return page
}

func normalizedSourcePath(t *testing.T, path string) string {
	t.Helper()
	normalized, err := settings.NormalizePath(path)
	if err != nil {
		t.Fatalf("normalize %q: %v", path, err)
	}
	return normalized
}

func sourceStringPointer(value string) *string { return &value }

func (fixture sourcesAPIFixture) createRoot(t *testing.T, name, path string) api.SourceRootResponse {
	t.Helper()
	body := fmt.Sprintf(`{"display_name":%q,"configured_path":%q}`, name, path)
	response := sourceRequest(t, fixture.handler, http.MethodPost, "/sources", body)
	if response.Code != http.StatusOK {
		t.Fatalf("create a source root: status=%d %s", response.Code, response.Body.String())
	}
	return decodeSourceRoot(t, response)
}

func (fixture sourcesAPIFixture) seedRoot(t *testing.T, root *persistence.SourceRoot, locations []persistence.SourceLocation) {
	t.Helper()
	fixture.repository.roots = append(fixture.repository.roots, root)
	fixture.repository.locations[root.ID] = locations
}

// TestSourceRoutesAnswerWithoutDependencies pins that every source route is
// registered even when the OpenAPI export passes empty Dependencies: a missing
// service is a safe 503, never a 404 or a panic.
func TestSourceRoutesAnswerWithoutDependencies(t *testing.T) {
	handler := api.HandlerWithDependencies(api.Dependencies{})
	id := uuid.NewString()
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/sources", ""},
		{http.MethodPost, "/sources", `{"display_name":"Music","configured_path":"/music"}`},
		{http.MethodGet, "/sources/" + id, ""},
		{http.MethodPatch, "/sources/" + id, `{}`},
		{http.MethodDelete, "/sources/" + id, `{"confirmed_path":"/music","confirmed_location_count":0}`},
		{http.MethodGet, "/sources/" + id + "/locations", ""},
		{http.MethodPost, "/sources/" + id + "/scan", ""},
	} {
		response := sourceRequest(t, handler, request.method, request.path, request.body)
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s status=%d, want %d: %s",
				request.method, request.path, response.Code, http.StatusServiceUnavailable, response.Body.String())
		}
	}
}

func TestCreateSourceRootValidatesOperatorInput(t *testing.T) {
	fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), true)
	source := t.TempDir()

	created := fixture.createRoot(t, "  Music  ", source)
	if created.DisplayName != "Music" || created.ConfiguredPath != normalizedSourcePath(t, source) ||
		!created.Enabled || created.InventoryPath != nil || created.Stale || created.LocationCount != 0 {
		t.Fatalf("created root = %+v", created)
	}
	if created.Status != "unknown" || created.SafeError != nil || created.LastSuccessfulScanAt != nil || created.ScanGeneration != 0 {
		t.Fatalf("created root state = %+v", created)
	}
	if len(fixture.repository.roots) != 1 {
		t.Fatalf("roots stored = %d, want 1", len(fixture.repository.roots))
	}

	regularFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(regularFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	managedOverlap := filepath.Join(fixture.outputRoot, "music")
	if err := os.MkdirAll(managedOverlap, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{"missing display name", fmt.Sprintf(`{"configured_path":%q}`, t.TempDir()), http.StatusUnprocessableEntity},
		{"missing configured path", `{"display_name":"Music"}`, http.StatusUnprocessableEntity},
		{"relative path", `{"display_name":"Music","configured_path":"music/relative"}`, http.StatusBadRequest},
		{"absent directory", fmt.Sprintf(`{"display_name":"Music","configured_path":%q}`, filepath.Join(t.TempDir(), "absent")), http.StatusBadRequest},
		{"file instead of directory", fmt.Sprintf(`{"display_name":"Music","configured_path":%q}`, regularFile), http.StatusBadRequest},
		{"overlap with the managed output root", fmt.Sprintf(`{"display_name":"Music","configured_path":%q}`, managedOverlap), http.StatusBadRequest},
		{"duplicate configured path", fmt.Sprintf(`{"display_name":"Other","configured_path":%q}`, source), http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := sourceRequest(t, fixture.handler, http.MethodPost, "/sources", test.body)
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
	if len(fixture.repository.roots) != 1 {
		t.Fatalf("a rejected create stored a root: %d roots", len(fixture.repository.roots))
	}
}

func TestSourceRootReadReportsAvailabilityAndStaleInventory(t *testing.T) {
	fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), true)
	previousPath := normalizedSourcePath(t, t.TempDir())
	currentPath := normalizedSourcePath(t, t.TempDir())
	lastSuccess := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	root := &persistence.SourceRoot{
		ID: uuid.New(), DisplayName: "Music", ConfiguredPath: currentPath, Enabled: true,
		Status: persistence.SourceRootStatusUnavailable, SafeError: sourceStringPointer("the source directory is not readable"),
		InventoryPath: &previousPath, ScanGeneration: 7, LastSuccessfulScanAt: &lastSuccess,
	}
	fixture.seedRoot(t, root, []persistence.SourceLocation{
		{ID: uuid.New(), SourceRootID: root.ID, RelativePath: "a.flac", ProbeStatus: persistence.SourceProbeStatusAudio},
	})

	response := sourceRequest(t, fixture.handler, http.MethodGet, "/sources", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list sources status=%d: %s", response.Code, response.Body.String())
	}
	var listed api.SourcesBody
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Sources) != 1 {
		t.Fatalf("listed sources = %d, want 1", len(listed.Sources))
	}
	fetched := decodeSourceRoot(t, sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+root.ID.String(), ""))
	for _, presented := range []api.SourceRootResponse{listed.Sources[0], fetched} {
		if presented.ID != root.ID || presented.InventoryPath == nil || *presented.InventoryPath != previousPath {
			t.Fatalf("presented root = %+v", presented)
		}
		if !presented.Stale || presented.ScanGeneration != 7 || presented.LocationCount != 1 {
			t.Fatalf("presented inventory state = %+v", presented)
		}
		if presented.Status != persistence.SourceRootStatusUnavailable || presented.SafeError == nil {
			t.Fatalf("presented availability = %+v", presented)
		}
		if presented.LastSuccessfulScanAt == nil || !presented.LastSuccessfulScanAt.Equal(lastSuccess) {
			t.Fatalf("presented last success = %+v", presented)
		}
	}

	missing := sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+uuid.NewString(), "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown root status=%d, want 404: %s", missing.Code, missing.Body.String())
	}
}

func TestUpdateSourceRootAppliesEditsAndMapsConflicts(t *testing.T) {
	fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), true)
	root := fixture.createRoot(t, "Music", t.TempDir())

	response := sourceRequest(t, fixture.handler, http.MethodPatch, "/sources/"+root.ID.String(),
		`{"display_name":"Renamed","enabled":false}`)
	if response.Code != http.StatusOK {
		t.Fatalf("update status=%d: %s", response.Code, response.Body.String())
	}
	updated := decodeSourceRoot(t, response)
	if updated.DisplayName != "Renamed" || updated.Enabled || updated.ConfiguredPath != root.ConfiguredPath {
		t.Fatalf("updated root = %+v", updated)
	}

	invalid := sourceRequest(t, fixture.handler, http.MethodPatch, "/sources/"+root.ID.String(), `{"display_name":""}`)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty display name status=%d, want 422: %s", invalid.Code, invalid.Body.String())
	}
	missing := sourceRequest(t, fixture.handler, http.MethodPatch, "/sources/"+uuid.NewString(), `{"enabled":true}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown root update status=%d, want 404: %s", missing.Code, missing.Body.String())
	}

	fixture.repository.busy = true
	busy := sourceRequest(t, fixture.handler, http.MethodPatch, "/sources/"+root.ID.String(), `{"enabled":true}`)
	if busy.Code != http.StatusConflict {
		t.Fatalf("active scan update status=%d, want 409: %s", busy.Code, busy.Body.String())
	}
}

func TestDeleteSourceRootRequiresConfirmationAndKeepsSourceFiles(t *testing.T) {
	fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), true)
	source := t.TempDir()
	track := filepath.Join(source, "track.flac")
	if err := os.WriteFile(track, []byte("audio bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := fixture.createRoot(t, "Music", source)
	fixture.repository.locations[root.ID] = []persistence.SourceLocation{
		{ID: uuid.New(), SourceRootID: root.ID, RelativePath: "track.flac", ProbeStatus: persistence.SourceProbeStatusAudio},
	}
	deleteBody := func(confirmedPath string, count int) string {
		return fmt.Sprintf(`{"confirmed_path":%q,"confirmed_location_count":%d}`, confirmedPath, count)
	}

	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{"wrong configured path", deleteBody(filepath.Join(source, "other"), 1), http.StatusBadRequest},
		{"wrong location count", deleteBody(normalizedSourcePath(t, source), 2), http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := sourceRequest(t, fixture.handler, http.MethodDelete, "/sources/"+root.ID.String(), test.body)
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if len(fixture.repository.roots) != 1 {
				t.Fatalf("a refused deletion removed the root: %d roots", len(fixture.repository.roots))
			}
		})
	}

	missing := sourceRequest(t, fixture.handler, http.MethodDelete, "/sources/"+uuid.NewString(), deleteBody("/music", 0))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown root deletion status=%d, want 404: %s", missing.Code, missing.Body.String())
	}

	fixture.repository.busy = true
	active := sourceRequest(t, fixture.handler, http.MethodDelete, "/sources/"+root.ID.String(), deleteBody(root.ConfiguredPath, 1))
	if active.Code != http.StatusConflict {
		t.Fatalf("active scan deletion status=%d, want 409: %s", active.Code, active.Body.String())
	}
	fixture.repository.busy = false

	response := sourceRequest(t, fixture.handler, http.MethodDelete, "/sources/"+root.ID.String(), deleteBody(root.ConfiguredPath, 1))
	if response.Code != http.StatusNoContent {
		t.Fatalf("confirmed deletion status=%d, want 204: %s", response.Code, response.Body.String())
	}
	if len(fixture.repository.roots) != 0 || len(fixture.repository.locations[root.ID]) != 0 {
		t.Fatalf("deleted root still stored: %d roots", len(fixture.repository.roots))
	}
	preserved, err := os.ReadFile(track)
	if err != nil || string(preserved) != "audio bytes" {
		t.Fatalf("deleting a root touched its source file: %q, %v", preserved, err)
	}
}

func TestListSourceLocationsPaginatesThePublishedInventory(t *testing.T) {
	fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), true)
	root := &persistence.SourceRoot{
		ID: uuid.New(), DisplayName: "Music", ConfiguredPath: normalizedSourcePath(t, t.TempDir()),
		Enabled: true, Status: persistence.SourceRootStatusAvailable,
	}
	mtime := time.Date(2026, time.September, 2, 8, 30, 0, 0, time.UTC)
	expected := make([]string, 0, 5)
	locations := make([]persistence.SourceLocation, 0, 5)
	for index := range 5 {
		path := fmt.Sprintf("disc-%d.flac", index)
		expected = append(expected, path)
		locations = append(locations, persistence.SourceLocation{
			ID: uuid.New(), SourceRootID: root.ID, RelativePath: path,
			SizeBytes: int64(100 + index), Mtime: mtime, ProbeStatus: persistence.SourceProbeStatusAudio,
		})
	}
	fixture.seedRoot(t, root, locations)
	// The candidates of a scan that has not been applied must never be served.
	fixture.repository.candidates[root.ID] = []persistence.SourceScanCandidate{
		{ID: uuid.New(), OperationID: uuid.New(), RelativePath: "unfinished.flac", ProbeStatus: persistence.SourceProbeStatusAudio},
	}

	collected := make([]string, 0, len(expected))
	cursor := ""
	for page := 0; ; page++ {
		if page > 5 {
			t.Fatal("location pagination did not terminate")
		}
		requestPath := "/sources/" + root.ID.String() + "/locations?limit=2"
		if cursor != "" {
			requestPath += "&cursor=" + cursor
		}
		response := sourceRequest(t, fixture.handler, http.MethodGet, requestPath, "")
		if response.Code != http.StatusOK {
			t.Fatalf("page %d status=%d: %s", page, response.Code, response.Body.String())
		}
		body := decodeSourceLocations(t, response)
		if len(body.Locations) > 2 {
			t.Fatalf("page %d returned %d locations, want at most 2", page, len(body.Locations))
		}
		for _, location := range body.Locations {
			if location.ProbeStatus != persistence.SourceProbeStatusAudio || location.SizeBytes <= 0 || !location.Mtime.Equal(mtime) {
				t.Fatalf("location = %+v", location)
			}
			collected = append(collected, location.RelativePath)
		}
		if body.NextCursor == nil {
			break
		}
		cursor = *body.NextCursor
	}
	if !slices.Equal(collected, expected) {
		t.Fatalf("paged locations = %v, want %v", collected, expected)
	}

	defaulted := sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+root.ID.String()+"/locations", "")
	if defaulted.Code != http.StatusOK || len(decodeSourceLocations(t, defaulted).Locations) != len(expected) {
		t.Fatalf("default page status=%d: %s", defaulted.Code, defaulted.Body.String())
	}
}

func TestListSourceLocationsValidatesItsInputs(t *testing.T) {
	fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), true)
	root := &persistence.SourceRoot{
		ID: uuid.New(), DisplayName: "Music", ConfiguredPath: normalizedSourcePath(t, t.TempDir()),
		Enabled: true, Status: persistence.SourceRootStatusAvailable,
	}
	fixture.seedRoot(t, root, []persistence.SourceLocation{
		{ID: uuid.New(), SourceRootID: root.ID, RelativePath: "a.flac", ProbeStatus: persistence.SourceProbeStatusNoAudio},
	})

	for _, test := range []struct {
		name string
		path string
		want int
	}{
		{"zero limit", "/locations?limit=0", http.StatusUnprocessableEntity},
		{"limit above the maximum", "/locations?limit=201", http.StatusUnprocessableEntity},
		{"unparsable limit", "/locations?limit=abc", http.StatusUnprocessableEntity},
		{"foreign cursor", "/locations?cursor=not-base64!", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+root.ID.String()+test.path, "")
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}

	unknown := sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+uuid.NewString()+"/locations", "")
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown root locations status=%d, want 404: %s", unknown.Code, unknown.Body.String())
	}
}

func TestStartSourceScanRefusalsAndQueuedSnapshot(t *testing.T) {
	t.Run("unfinished setup", func(t *testing.T) {
		fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), false)
		response := sourceRequest(t, fixture.handler, http.MethodPost, "/sources/"+uuid.NewString()+"/scan", "")
		if response.Code != http.StatusConflict {
			t.Fatalf("status=%d, want 409: %s", response.Code, response.Body.String())
		}
		if len(fixture.operations.operations) != 0 {
			t.Fatal("a refused scan start created an operation")
		}
	})

	t.Run("diagnostic platform", func(t *testing.T) {
		platform := settings.PlatformState{
			Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}, Diagnostic: true, Reason: "instance platform mismatch",
		}
		fixture := newSourcesAPIFixture(t, platform, true)
		response := sourceRequest(t, fixture.handler, http.MethodPost, "/sources/"+uuid.NewString()+"/scan", "")
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want 503: %s", response.Code, response.Body.String())
		}
	})

	t.Run("unknown root", func(t *testing.T) {
		fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), true)
		response := sourceRequest(t, fixture.handler, http.MethodPost, "/sources/"+uuid.NewString()+"/scan", "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("status=%d, want 404: %s", response.Code, response.Body.String())
		}
	})

	for _, test := range []struct {
		name   string
		reason error
		want   int
	}{
		{"disabled root", persistence.ErrSourceRootDisabled, http.StatusConflict},
		{"root with an active scan", persistence.ErrSourceRootActiveScan, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), true)
			root := fixture.createRoot(t, "Music", t.TempDir())
			if errors.Is(test.reason, persistence.ErrSourceRootDisabled) {
				fixture.repository.disabled = true
			} else {
				fixture.repository.busy = true
			}
			response := sourceRequest(t, fixture.handler, http.MethodPost, "/sources/"+root.ID.String()+"/scan", "")
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if len(fixture.operations.operations) != 0 {
				t.Fatal("a refused scan start created an operation")
			}
		})
	}

	t.Run("queued scan", func(t *testing.T) {
		fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), true)
		root := fixture.createRoot(t, "Music", t.TempDir())
		response := sourceRequest(t, fixture.handler, http.MethodPost, "/sources/"+root.ID.String()+"/scan", "")
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d: %s", response.Code, response.Body.String())
		}
		var operation api.OperationResponse
		if err := json.Unmarshal(response.Body.Bytes(), &operation); err != nil {
			t.Fatal(err)
		}
		if operation.Kind != service.SourceScanOperationKind || operation.State != "queued" || operation.Stage != service.SourceScanStageQueued {
			t.Fatalf("started operation = %+v", operation)
		}
		stored, err := fixture.operations.GetOperation(context.Background(), operation.ID)
		if err != nil || stored.TargetSourceRootID == nil || *stored.TargetSourceRootID != root.ID {
			t.Fatalf("stored operation = %+v, %v", stored, err)
		}
	})
}

// TestFailedScanKeepsPublishedInventoryVisible pins the read contract of a root
// whose last scan failed: the endpoints serve the locations of the last applied
// generation and never the candidates of the failed attempt, whose removal is
// the job of the scan transaction itself.
func TestFailedScanKeepsPublishedInventoryVisible(t *testing.T) {
	fixture := newSourcesAPIFixture(t, supportedSourcePlatform(), true)
	previousPath := normalizedSourcePath(t, t.TempDir())
	currentPath := normalizedSourcePath(t, t.TempDir())
	lastSuccess := time.Date(2026, time.September, 3, 9, 0, 0, 0, time.UTC)
	root := &persistence.SourceRoot{
		ID: uuid.New(), DisplayName: "Music", ConfiguredPath: currentPath, Enabled: true,
		Status: persistence.SourceRootStatusAvailable, InventoryPath: &previousPath,
		ScanGeneration: 3, LastSuccessfulScanAt: &lastSuccess,
	}
	fixture.seedRoot(t, root, []persistence.SourceLocation{
		{ID: uuid.New(), SourceRootID: root.ID, RelativePath: "kept.flac", ProbeStatus: persistence.SourceProbeStatusAudio},
		{ID: uuid.New(), SourceRootID: root.ID, RelativePath: "broken.flac", ProbeStatus: persistence.SourceProbeStatusProbeError, SafeError: sourceStringPointer("ffprobe could not confirm an audio stream")},
	})
	failed := uuid.New()
	fixture.operations.operations[failed] = &persistence.Operation{
		ID: failed, Kind: service.SourceScanOperationKind, State: "failed", Stage: service.SourceScanStageTraversing,
		SafeError: sourceStringPointer("the source tree could not be read"), TargetSourceRootID: &root.ID,
	}

	page := decodeSourceLocations(t, sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+root.ID.String()+"/locations", ""))
	presented := make([]string, 0, len(page.Locations))
	for _, location := range page.Locations {
		presented = append(presented, location.RelativePath)
		if location.ProbeStatus == persistence.SourceProbeStatusProbeError && location.SafeError == nil {
			t.Fatalf("probe error presented without its safe reason: %+v", location)
		}
	}
	if !slices.Equal(presented, []string{"broken.flac", "kept.flac"}) {
		t.Fatalf("locations after a failed scan = %v", presented)
	}

	state := decodeSourceRoot(t, sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+root.ID.String(), ""))
	if state.ScanGeneration != 3 || state.LocationCount != 2 || !state.Stale || state.InventoryPath == nil || *state.InventoryPath != previousPath {
		t.Fatalf("root state after a failed scan = %+v", state)
	}
}
