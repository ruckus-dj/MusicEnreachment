//go:build integration

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/uptrace/bun"

	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// sourceApplicationFixture is the production source composition: the same
// repositories, services and worker constructors app.Run installs, down to the
// shared persistence.SetupManagerRepository.
type sourceApplicationFixture struct {
	database   *bun.DB
	store      *persistence.SettingsRepository
	registry   *settings.Registry
	platform   settings.PlatformState
	operations *service.Operations
	roots      *service.SourceRoots
	inventory  *persistence.SourceInventoryRepository
	installer  *persistence.SetupManagerRepository
	worker     *jobs.SourceScanWorker
	router     http.Handler
}

func newSourceApplicationFixture(t *testing.T, tc testContainer) sourceApplicationFixture {
	t.Helper()
	database := tc.database
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	store := persistence.NewSettingsRepository(database)
	registry := settings.New(store, nil)
	platform, err := registry.InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatalf("initialize the instance platform: %v", err)
	}
	toolsRoot, outputRoot := t.TempDir(), t.TempDir()
	if err := registry.SetToolsDirectory(ctx, toolsRoot, outputRoot); err != nil {
		t.Fatalf("record the tools directory: %v", err)
	}
	if err := registry.SetOutputDirectory(ctx, outputRoot, toolsRoot); err != nil {
		t.Fatalf("record the output directory: %v", err)
	}

	setupManager := persistence.NewSetupManagerRepository(database)
	operations := service.NewOperations(setupManager)
	inventory := persistence.NewSourceInventoryRepository(database)
	roots := service.NewSourceRoots(inventory, registry)
	scan := service.NewSourceScanOperations(inventory, roots, registry, platform, tc.riverClient)
	worker := jobs.NewSourceScanWorker(
		scanWorkerRepository{SetupManagerRepository: setupManager, SourceInventoryRepository: inventory},
		operations, roots, registry, platform, tools.NewLifecycle(nil),
	)

	setupDependency := api.HandlerWithDependencies(api.Dependencies{
		Setup:           service.NewSetup(store, registry, platform, setupManager, nil),
		SourceRoots:     roots,
		SourceLocations: service.NewSourceLocations(inventory),
		SourceScan:      scan,
		Operations:      operations,
	})

	return sourceApplicationFixture{
		database: database, store: store, registry: registry, platform: platform,
		operations: operations,
		roots:      roots,
		inventory:  inventory,
		installer:  setupManager,
		worker:     worker,
		router:     setupDependency,
	}
}

// switchPlatform rebuilds the router's setup dependency for a switched instance
// platform, which is how the route guard observes a platform an earlier process
// stored.
func (fixture *sourceApplicationFixture) switchPlatform(t *testing.T, platform settings.PlatformState) {
	t.Helper()
	fixture.platform = platform
	fixture.router = api.HandlerWithDependencies(api.Dependencies{
		Setup:           service.NewSetup(fixture.store, fixture.registry, platform, fixture.installer, nil),
		SourceRoots:     fixture.roots,
		SourceLocations: service.NewSourceLocations(fixture.inventory),
		SourceScan:      service.NewSourceScanOperations(fixture.inventory, fixture.roots, fixture.registry, platform, nil),
		Operations:      fixture.operations,
	})
}

// mutateStatus answers the status of one HTTP request against the composed
// router.
func (fixture sourceApplicationFixture) mutateStatus(t *testing.T, method, path, body string) int {
	t.Helper()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	fixture.router.ServeHTTP(response, request)
	return response.Code
}

type sourceCreateBody struct {
	DisplayName    string `json:"display_name"`
	ConfiguredPath string `json:"configured_path"`
}

type sourceDeleteBody struct {
	ConfirmedPath          string `json:"confirmed_path"`
	ConfirmedLocationCount int64  `json:"confirmed_location_count"`
}

func decodeInto(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode the response: %v: %s", err, response.Body.String())
	}
}

func normalizedSourcePath(t *testing.T, path string) string {
	t.Helper()
	normalized, err := settings.NormalizePath(path)
	if err != nil {
		t.Fatalf("normalize %q: %v", path, err)
	}
	return normalized
}

func (fixture sourceApplicationFixture) createRoot(t *testing.T, name, path string) api.SourceRootResponse {
	t.Helper()
	input, err := json.Marshal(sourceCreateBody{DisplayName: name, ConfiguredPath: path})
	if err != nil {
		t.Fatal(err)
	}
	var root api.SourceRootResponse
	decodeInto(t, fixture.request(t, http.MethodPost, "/sources", string(input)), &root)
	return root
}

func (fixture sourceApplicationFixture) request(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	fixture.router.ServeHTTP(response, request)
	return response
}

// TestApplicationSourcesEndpointsRequireSetup pins the composed router refusal
// of the source surface before the first-run Setup is complete: every route
// answers 409 and the platform guard never gets a chance to mask it.
func TestApplicationSourcesEndpointsRequireSetup(t *testing.T) {
	t.Parallel()
	fixture := newSourceApplicationFixture(t, startSourceApplicationDatabase(t))
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
		response := fixture.request(t, request.method, request.path, request.body)
		if response.Code != http.StatusConflict {
			t.Errorf("%s %s status=%d, want %d: %s",
				request.method, request.path, response.Code, http.StatusConflict, response.Body.String())
		}
	}
}

// TestApplicationSourcesEndpointsRefuseScanOnAMismatchedPlatform pins the scan
// mutation guard on a setup-complete instance whose platform is diagnostic: the
// refusal is the platform 503, not the disabled root and not the setup 409.
func TestApplicationSourcesEndpointsRefuseScanOnAMismatchedPlatform(t *testing.T) {
	t.Parallel()
	fixture := newSourceApplicationFixture(t, startSourceApplicationDatabase(t))
	if err := fixture.registry.CompleteSetup(context.Background()); err != nil {
		t.Fatalf("complete the first-run setup: %v", err)
	}

	diagnostic, err := fixture.registry.InitializePlatform(context.Background(), settings.Platform{GOOS: "windows", GOARCH: "arm64"})
	if err != nil {
		t.Fatalf("switch the instance to a mismatched platform: %v", err)
	}
	if !diagnostic.Diagnostic {
		t.Fatalf("platform state = %+v, want a diagnostic instance", diagnostic)
	}
	fixture.switchPlatform(t, diagnostic)

	if status := fixture.mutateStatus(t, http.MethodGet, "/sources", ""); status != http.StatusOK {
		t.Fatalf("list sources on a diagnostic platform status=%d, want 200", status)
	}
	if status := fixture.mutateStatus(t, http.MethodPost, "/sources/"+uuid.NewString()+"/scan", ""); status != http.StatusServiceUnavailable {
		t.Fatalf("scan on a diagnostic platform status=%d, want 503", status)
	}
	if count, err := fixture.countOperations(context.Background()); err != nil || count != 0 {
		t.Fatalf("a refused scan start recorded %d operations, %v", count, err)
	}
}

// TestApplicationSourcesEndpointsDriveTheProductionWorker drives the whole
// source lifecycle through the composed router: create, read, edit, scan through
// the composed worker, page the published inventory, then delete with the
// server-verified confirmation and prove the deletion touched no source file.
func TestApplicationSourcesEndpointsDriveTheProductionWorker(t *testing.T) {
	t.Parallel()
	fixture := newSourceApplicationFixture(t, startSourceApplicationDatabase(t))
	fixture.provisionManagedFFprobe(t)
	if err := fixture.registry.CompleteSetup(context.Background()); err != nil {
		t.Fatalf("complete the first-run setup: %v", err)
	}
	source := t.TempDir()
	track := filepath.Join(source, "disc", "track.flac")
	if err := os.MkdirAll(filepath.Dir(track), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(track, []byte("audio bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := fixture.createRoot(t, "Music", source)
	if root.DisplayName != "Music" || root.ConfiguredPath != normalizedSourcePath(t, source) || !root.Enabled {
		t.Fatalf("created root = %+v", root)
	}

	var listed api.SourcesBody
	decodeInto(t, fixture.request(t, http.MethodGet, "/sources", ""), &listed)
	if len(listed.Sources) != 1 || listed.Sources[0].ID != root.ID {
		t.Fatalf("listed sources = %+v", listed.Sources)
	}

	var edited api.SourceRootResponse
	decodeInto(t, fixture.request(t, http.MethodPatch, "/sources/"+root.ID.String(), `{"display_name":"Archive"}`), &edited)
	if edited.DisplayName != "Archive" || edited.ConfiguredPath != root.ConfiguredPath {
		t.Fatalf("edited root = %+v", edited)
	}

	// The scan mutation goes through the composed router; the traversal is the
	// production worker invoked with the operation the composition queued.
	fixture.runScan(t, root.ID)

	var page api.SourceLocationsBody
	decodeInto(t, fixture.request(t, http.MethodGet, "/sources/"+root.ID.String()+"/locations", ""), &page)
	if len(page.Locations) != 1 || page.Locations[0].RelativePath != "disc/track.flac" || page.Locations[0].SizeBytes != int64(len("audio bytes")) {
		t.Fatalf("published locations = %+v", page.Locations)
	}
	if page.Locations[0].ProbeStatus != persistence.SourceProbeStatusNoAudio {
		var probeFailure string
		if err := fixture.database.NewRaw(`SELECT COALESCE(s.safe_error, '') FROM source_analysis_step s JOIN source_analysis_work w ON w.id=s.work_id WHERE w.location_id=? AND s.step='probe'`, page.Locations[0].ID).Scan(t.Context(), &probeFailure); err != nil {
			t.Fatalf("read probe failure: %v", err)
		}
		t.Fatalf("probe status = %q, want %q for a file ffprobe reports without an audio stream: %s",
			page.Locations[0].ProbeStatus, persistence.SourceProbeStatusNoAudio,
			probeFailure)
	}
	var scanned api.SourceRootResponse
	decodeInto(t, fixture.request(t, http.MethodGet, "/sources/"+root.ID.String(), ""), &scanned)
	if scanned.ScanGeneration != 1 || scanned.LocationCount != 1 || scanned.Stale || scanned.InventoryPath == nil {
		t.Fatalf("root state after a scan = %+v", scanned)
	}

	// A refused deletion keeps both the root and its inventory.
	confirmation := func(path string, count int64) string {
		body, err := json.Marshal(sourceDeleteBody{ConfirmedPath: path, ConfirmedLocationCount: count})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	if status := fixture.mutateStatus(t, http.MethodDelete, "/sources/"+root.ID.String(), confirmation(scanned.ConfiguredPath, 2)); status != http.StatusBadRequest {
		t.Fatalf("deletion with a stale location count status=%d, want 400", status)
	}
	if status := fixture.mutateStatus(t, http.MethodDelete, "/sources/"+root.ID.String(), confirmation(filepath.Join(source, "elsewhere"), 1)); status != http.StatusBadRequest {
		t.Fatalf("deletion with a foreign configured path status=%d, want 400", status)
	}
	if _, err := fixture.inventory.GetSourceRoot(context.Background(), root.ID); err != nil {
		t.Fatalf("a refused deletion removed the root: %v", err)
	}

	if status := fixture.mutateStatus(t, http.MethodDelete, "/sources/"+root.ID.String(), confirmation(scanned.ConfiguredPath, int64(scanned.LocationCount))); status != http.StatusNoContent {
		t.Fatalf("confirmed deletion status=%d, want 204", status)
	}
	if count, err := fixture.inventory.CountSourceLocations(context.Background(), root.ID); err != nil || count != 0 {
		t.Fatalf("locations left after the deletion: %d, %v", count, err)
	}
	if _, err := fixture.inventory.GetSourceRoot(context.Background(), root.ID); err == nil {
		t.Fatal("the deleted root is still stored")
	}
	preserved, err := os.ReadFile(track)
	if err != nil || string(preserved) != "audio bytes" {
		t.Fatalf("deleting a root altered its source file: %q, %v", preserved, err)
	}
	if status := fixture.mutateStatus(t, http.MethodGet, "/sources/"+root.ID.String(), ""); status != http.StatusNotFound {
		t.Fatalf("reading a deleted root status=%d, want 404", status)
	}
}

// provisionManagedFFprobe materializes a fake managed ffmpeg of the active
// installation, so the composed worker's ffprobe is a real executable this test
// controls. The binaries report the release version on the version query and an
// audio-free ffprobe response on a probe, which is the ffprobe contract of an
// audio-less file. The release identity is one the approved catalog offers for
// this platform, so a later install preflight can bind to the same installation.
func (fixture sourceApplicationFixture) provisionManagedFFprobe(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	const release = "7.1"
	root, exists, err := fixture.registry.GetToolsDirectory(ctx)
	if err != nil || !exists {
		t.Fatalf("read the managed tools directory: %v, exists=%v", err, exists)
	}
	directory := filepath.Join(root, "ffmpeg", release)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	names := tools.ExpectedExecutables(tools.PackageFFmpeg, fixture.platform.Platform.GOOS)
	target := filepath.Join(directory, names[0])
	source, err := cachedAppScanProbe()
	if err != nil {
		t.Fatalf("prepare the managed probe fixture: %v", err)
	}
	configuration := struct {
		Version string `json:"version"`
		NoAudio bool   `json:"no_audio"`
	}{Version: release, NoAudio: true}
	if err := copyAppScanProbe(source, target, configuration); err != nil {
		t.Fatalf("write the managed %s: %v", names[0], err)
	}
	for _, name := range names[1:] {
		if err := copyAppScanProbe(source, filepath.Join(directory, name), configuration); err != nil {
			t.Fatalf("write the managed %s: %v", name, err)
		}
	}
	versions, err := json.Marshal(map[string]string{"ffmpeg": "ffmpeg version " + release, "ffprobe": "ffprobe version " + release})
	if err != nil {
		t.Fatal(err)
	}
	verifiedAt := time.Now().UTC()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFFmpeg),
		PlatformGOOS: fixture.platform.Platform.GOOS, PlatformGOARCH: fixture.platform.Platform.GOARCH,
		SourceName: "btbn", ReleaseIdentity: release, RelativePath: filepath.Join("ffmpeg", release),
		State: "ready", ExecutableVersions: versions, VerifiedAt: &verifiedAt,
	}
	if err := fixture.installer.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create the managed ffmpeg installation: %v", err)
	}
	if err := fixture.store.Set(ctx, settings.ActiveFFmpegInstallationKey, installation.ID.String()); err != nil {
		t.Fatalf("activate the managed ffmpeg installation: %v", err)
	}
}

// storedOperation reads the durable operation row the composition wrote, so the
// test asserts the queued scan the worker will receive rather than the snapshot
// the HTTP response already proves.
func (fixture sourceApplicationFixture) storedOperation(t *testing.T, id uuid.UUID) (*persistence.Operation, error) {
	t.Helper()
	operation := new(persistence.Operation)
	err := fixture.database.NewSelect().Model(operation).Where("id = ?", id).Scan(t.Context())
	return operation, err
}

// countOperations reports how many operation rows the database holds.
func (fixture sourceApplicationFixture) countOperations(ctx context.Context) (int, error) {
	return fixture.database.NewSelect().Model((*persistence.Operation)(nil)).Count(ctx)
}

// runScan queues one scan through the composed router and executes the
// production worker with the operation the composition recorded.
func (fixture sourceApplicationFixture) runScan(t *testing.T, rootID uuid.UUID) {
	t.Helper()
	response := fixture.request(t, http.MethodPost, "/sources/"+rootID.String()+"/scan", "")
	if response.Code != http.StatusOK {
		t.Fatalf("start a scan status=%d: %s", response.Code, response.Body.String())
	}
	var operation api.OperationResponse
	decodeInto(t, response, &operation)
	if operation.Kind != service.SourceScanOperationKind || operation.State != "queued" || operation.Stage != service.SourceScanStageQueued {
		t.Fatalf("queued operation = %+v", operation)
	}
	stored, err := fixture.storedOperation(t, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Kind != service.SourceScanOperationKind || stored.TargetSourceRootID == nil || *stored.TargetSourceRootID != rootID {
		t.Fatalf("queued operation = %+v", stored)
	}
	if stored.State != "queued" || stored.Stage != service.SourceScanStageQueued {
		t.Fatalf("queued operation state = %q stage = %q", stored.State, stored.Stage)
	}
	if stored.RiverJobID == nil {
		t.Fatal("queued scan has no River job ID")
	}
	job := &river.Job[service.ScanSourceJobArgs]{
		JobRow: &rivertype.JobRow{ID: *stored.RiverJobID},
		Args:   service.ScanSourceJobArgs{OperationID: stored.ID},
	}
	if err := fixture.worker.Work(context.Background(), job); err != nil {
		t.Fatalf("the production scan worker failed: %v", err)
	}
}

// testContainer carries the PostgreSQL database and the River client the
// composition's scan-start path enqueues through.
type testContainer struct {
	database    *bun.DB
	riverClient *river.Client[*sql.Tx]
}

// startSourceApplicationDatabase provisions the PostgreSQL a source application
// test needs: the project schema, the River schema and a live client whose
// InsertTx path the repositories use exactly as production does.
func startSourceApplicationDatabase(t *testing.T) testContainer {
	t.Helper()
	database := testpostgres.OpenMigrated(t)
	databaseURL := testpostgres.URL(t, database)
	databaseSQL := database.DB

	driver := riverdatabasesql.NewWithPgxListener(databaseSQL, mustRiverListener(t, databaseURL))
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatalf("create the River migrator: %v", err)
	}
	if _, err := migrator.Migrate(t.Context(), rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply the River schema: %v", err)
	}
	client, err := river.NewClient(driver, &river.Config{})
	if err != nil {
		t.Fatalf("create the River client: %v", err)
	}
	return testContainer{database: database, riverClient: client}
}

// mustRiverListener opens the pgx listener pool the River driver constructor
// requires. The client is never started here.
func mustRiverListener(t *testing.T, databaseURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("create the River listener pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
