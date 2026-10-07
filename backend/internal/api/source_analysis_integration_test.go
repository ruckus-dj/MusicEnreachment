//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// TestSourceAnalysisHTTPAgainstPostgreSQL drives the inspector and the analysis
// start through the real repository. Root ownership of a location, the stored
// variant read and the active-analysis discovery are PostgreSQL behaviour, not
// properties of a fake.
func TestSourceAnalysisHTTPAgainstPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	store := persistence.NewSettingsRepository(database)
	registry := settings.New(store, nil)
	platform, err := registry.InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	toolsRoot, outputRoot := t.TempDir(), t.TempDir()
	if err := registry.SetToolsDirectory(ctx, toolsRoot, outputRoot); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetOutputDirectory(ctx, outputRoot, toolsRoot); err != nil {
		t.Fatal(err)
	}
	if err := registry.CompleteSetup(ctx); err != nil {
		t.Fatal(err)
	}

	setupManager := persistence.NewSetupManagerRepository(database)
	inventory := persistence.NewSourceInventoryRepository(database)
	driver := riverdatabasesql.New(database.DB)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatalf("create River migrator: %v", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}
	riverClient, err := river.NewClient(driver, &river.Config{})
	if err != nil {
		t.Fatalf("create insert-only River client: %v", err)
	}
	handler := api.HandlerWithDependencies(api.Dependencies{
		Setup:                 service.NewSetup(store, registry, platform, setupManager, nil),
		SourceRoots:           service.NewSourceRoots(inventory, registry),
		SourceLocations:       service.NewSourceLocations(inventory),
		SourceLocationDetails: service.NewSourceLocationDetails(inventory),
		SourceAnalysis:        service.NewSourceAnalysisOperations(persistence.NewSourceAnalysisStartStore(database), registry, registry, platform, riverClient),
		Operations:            service.NewOperations(setupManager),
	})

	root := decodeSourceRoot(t, sourceRequest(t, handler, http.MethodPost, "/sources",
		fmt.Sprintf(`{"display_name":"Music","configured_path":%q}`, t.TempDir())))
	location := persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: root.ID, RelativePath: "disc/track.flac", SizeBytes: 2048,
		Mtime: time.Now().UTC().Truncate(time.Microsecond), LastSeenScanGeneration: 1,
		ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatalf("store a source location: %v", err)
	}
	variant := &persistence.MediaVariant{
		ID: uuid.New(), SizeBytes: location.SizeBytes, AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion,
		FFProbeVersion: "6.1.1", FFProbeJSON: json.RawMessage(technicalResultFixture),
		ObservedTags: json.RawMessage(`{"TITLE":["Song"]}`), InspectedAt: time.Now().UTC(), AppliedOperationID: uuid.New(),
	}
	if _, err := database.NewInsert().Model(variant).Exec(ctx); err != nil {
		t.Fatalf("store a media variant: %v", err)
	}
	if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
		Set("media_variant_id = ?", variant.ID).Where("id = ?", location.ID).Exec(ctx); err != nil {
		t.Fatalf("link the stored variant: %v", err)
	}

	detailPath := "/sources/" + root.ID.String() + "/locations/" + location.ID.String()
	detail := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet, detailPath, ""))
	if detail.AnalysisState != "analyzed" || detail.Result == nil || len(detail.Result.Streams) != 2 ||
		detail.ActiveAnalysisOperationID != nil {
		t.Fatalf("stored detail = %+v", detail)
	}

	if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
		Set("scan_generation = 1").Set("inventory_path = configured_path").
		Set("last_successful_scan_at = now()").Set("status = ?", persistence.SourceRootStatusAvailable).
		Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("establish the source root inventory: %v", err)
	}
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, InventoryPath: root.ConfiguredPath,
		RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		SHA256Enabled: true, OriginScanOperationID: uuid.New(),
	}
	failed := "previous digest failure"
	if err := inventory.StoreSourceAnalysisWork(ctx, work, []persistence.SourceAnalysisStepInput{{
		Step: persistence.SourceStepSHA256, State: "failed", SafeError: &failed,
	}}); err != nil {
		t.Fatalf("store current failed SHA-256 work: %v", err)
	}
	active, err := service.NewSourceAnalysisOperations(
		persistence.NewSourceAnalysisStartStore(database), registry, registry, platform, riverClient,
	).RetryStep(ctx, service.SourceAnalysisStepRequest{
		RootID: root.ID, LocationID: location.ID, Step: persistence.SourceStepSHA256,
		ExpectedSizeBytes: location.SizeBytes, ExpectedMtime: location.Mtime,
	})
	if err != nil {
		t.Fatalf("admit a retry of the failed SHA-256 step: %v", err)
	}
	discovered := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet, detailPath, ""))
	if discovered.ActiveAnalysisOperationID == nil || *discovered.ActiveAnalysisOperationID != active.ID {
		t.Fatalf("active analysis = %v, want %s", discovered.ActiveAnalysisOperationID, active.ID)
	}
	operation := decodeOperation(t, sourceRequest(t, handler, http.MethodGet, "/operations/"+active.ID.String(), "").Body.Bytes())
	if operation.State != "queued" || operation.TargetSourceLocationID == nil || *operation.TargetSourceLocationID != location.ID {
		t.Fatalf("HTTP operation = %+v", operation)
	}
	persisted, err := setupManager.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the persisted operation: %v", err)
	}
	if persisted.TargetWorkID == nil || *persisted.TargetWorkID != work.ID ||
		persisted.TargetStep == nil || *persisted.TargetStep != string(persistence.SourceStepSHA256) || persisted.RiverJobID == nil {
		t.Fatalf("persisted operation targets = %+v", persisted)
	}
	var queuedStepCount int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step
		WHERE work_id = ? AND step = 'sha256' AND state = 'queued' AND execution_operation_id = ?`,
		work.ID, active.ID).Scan(ctx, &queuedStepCount); err != nil {
		t.Fatalf("read the admitted SHA-256 step: %v", err)
	}
	if queuedStepCount != 1 {
		t.Fatalf("queued SHA-256 steps = %d, want 1", queuedStepCount)
	}
	var riverJobCount int
	if err := database.NewRaw("SELECT count(*) FROM river_job WHERE id = ?", *persisted.RiverJobID).Scan(ctx, &riverJobCount); err != nil {
		t.Fatalf("read the admitted River job: %v", err)
	}
	if riverJobCount != 1 {
		t.Fatalf("admitted River jobs = %d, want 1", riverJobCount)
	}

	foreignRoot := decodeSourceRoot(t, sourceRequest(t, handler, http.MethodPost, "/sources",
		fmt.Sprintf(`{"display_name":"Other","configured_path":%q}`, t.TempDir())))
	foreign := persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: foreignRoot.ID, RelativePath: "other.flac", SizeBytes: 64,
		Mtime: time.Now().UTC(), LastSeenScanGeneration: 1, ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(&foreign).Exec(ctx); err != nil {
		t.Fatalf("store a foreign location: %v", err)
	}
	foreignResponse := sourceRequest(t, handler, http.MethodGet,
		"/sources/"+root.ID.String()+"/locations/"+foreign.ID.String(), "")
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign location status=%d, want 404: %s", foreignResponse.Code, foreignResponse.Body.String())
	}

	// The former implicit /analyze API is intentionally gone; callers must now
	// name a failed normalized step and provide the current stat identity.
	if response := sourceRequest(t, handler, http.MethodPost, detailPath+"/analyze",
		`{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z"}`); response.Code != http.StatusNotFound {
		t.Fatalf("legacy analyze status=%d, want 404: %s", response.Code, response.Body.String())
	}
	retryPath := detailPath + "/retry"
	for _, body := range []string{
		`{"expected_size_bytes":-1,"expected_mtime":"2026-09-02T08:30:00Z","step":"sha256"}`,
		`{"expected_size_bytes":2048,"expected_mtime":"soon","step":"sha256"}`,
		`{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z"}`,
		`{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z","step":"sha256","ffprobe_version":"arbitrary"}`,
	} {
		response := sourceRequest(t, handler, http.MethodPost, retryPath, body)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("body %s status=%d, want 422: %s", body, response.Code, response.Body.String())
		}
	}
	rerunPath := detailPath + "/fingerprint/rerun"
	if response := sourceRequest(t, handler, http.MethodPost, rerunPath,
		`{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z","fpcalc_version":"arbitrary"}`); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rerun with caller-selected tool version status=%d, want 422: %s", response.Code, response.Body.String())
	}

	diagnostic := settings.PlatformState{
		Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}, Diagnostic: true, Reason: "instance platform mismatch",
	}
	readOnly := api.HandlerWithDependencies(api.Dependencies{
		Setup:                 service.NewSetup(store, registry, diagnostic, setupManager, nil),
		SourceLocationDetails: service.NewSourceLocationDetails(inventory),
		SourceAnalysis:        service.NewSourceAnalysisOperations(persistence.NewSourceAnalysisStartStore(database), registry, registry, diagnostic, nil),
		Operations:            service.NewOperations(setupManager),
	})
	if response := sourceRequest(t, readOnly, http.MethodGet, detailPath, ""); response.Code != http.StatusOK {
		t.Fatalf("diagnostic detail status=%d, want 200: %s", response.Code, response.Body.String())
	}
	if response := sourceRequest(t, readOnly, http.MethodPost, retryPath,
		`{"step":"sha256","expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z"}`); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("diagnostic analysis retry status=%d, want 503: %s", response.Code, response.Body.String())
	}
}
