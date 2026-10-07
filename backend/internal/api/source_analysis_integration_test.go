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
	database, _ := openAPTransitionDatabase(t)
	testpostgres.ResetAndMigrate(t, database)
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
	handler := api.HandlerWithDependencies(api.Dependencies{
		Setup:                 service.NewSetup(store, registry, platform, setupManager, nil),
		SourceRoots:           service.NewSourceRoots(inventory, registry),
		SourceLocations:       service.NewSourceLocations(inventory),
		SourceLocationDetails: service.NewSourceLocationDetails(inventory),
		SourceAnalysis:        service.NewSourceAnalysisOperations(persistence.NewSourceAnalysisStartStore(database), registry, registry, platform, nil),
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

	verifiedAt := time.Now().UTC()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "test", ReleaseIdentity: "test-1", RelativePath: "ffmpeg/test-1", State: "ready",
		ExecutableVersions: json.RawMessage(`{}`), ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verifiedAt,
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("store a managed installation: %v", err)
	}
	active := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: "queued", Stage: service.SourceAnalysisStageQueued,
		InputSnapshot: json.RawMessage(`{}`), TargetSourceRootID: &root.ID, TargetSourceLocationID: &location.ID,
		AnalysisInstallationID: &installation.ID,
	}
	if err := setupManager.CreateOperation(ctx, active); err != nil {
		t.Fatalf("store a queued analysis: %v", err)
	}
	discovered := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet, detailPath, ""))
	if discovered.ActiveAnalysisOperationID == nil || *discovered.ActiveAnalysisOperationID != active.ID {
		t.Fatalf("active analysis = %v, want %s", discovered.ActiveAnalysisOperationID, active.ID)
	}
	operation := decodeOperation(t, sourceRequest(t, handler, http.MethodGet, "/operations/"+active.ID.String(), ""))
	if operation.TargetSourceLocationID == nil || *operation.TargetSourceLocationID != location.ID {
		t.Fatalf("operation targets = %+v", operation)
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

	analyzePath := detailPath + "/analyze"
	for _, body := range []string{
		`{"expected_size_bytes":-1,"expected_mtime":"2026-09-02T08:30:00Z"}`,
		`{"expected_size_bytes":2048,"expected_mtime":"soon"}`,
		`{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z","path":"/etc/passwd"}`,
	} {
		response := sourceRequest(t, handler, http.MethodPost, analyzePath, body)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("body %s status=%d, want 422: %s", body, response.Code, response.Body.String())
		}
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
	if response := sourceRequest(t, readOnly, http.MethodPost, analyzePath,
		`{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z"}`); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("diagnostic analysis status=%d, want 503: %s", response.Code, response.Body.String())
	}
}
