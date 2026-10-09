//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func sourceTimePointer(value time.Time) *time.Time { return &value }

// TestSourceRootsHTTPAgainstPostgreSQL drives the source endpoints through the
// real repository. The page boundary, the deletion confirmation that compares
// the stored location count, the active-scan conflict a queued operation row
// decides, and the file preservation of a deletion are PostgreSQL behaviour, not
// properties of a fake.
func TestSourceRootsHTTPAgainstPostgreSQL(t *testing.T) {
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
	handler := api.HandlerWithDependencies(api.Dependencies{
		Setup:           service.NewSetup(store, registry, platform, setupManager, nil),
		SourceRoots:     service.NewSourceRoots(inventory, registry),
		SourceLocations: service.NewSourceLocations(inventory),
		Operations:      service.NewOperations(setupManager),
	})

	source := t.TempDir()
	track := filepath.Join(source, "disc", "track.flac")
	if err := os.MkdirAll(filepath.Dir(track), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(track, []byte("audio bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"display_name":"Music","configured_path":%q,"processing_mode":"in_place"}`, source)
	response := sourceRequest(t, handler, http.MethodPost, "/sources", body)
	if response.Code != http.StatusOK {
		t.Fatalf("create source root status=%d: %s", response.Code, response.Body.String())
	}
	root := decodeSourceRoot(t, response)
	expectedPath, err := settings.NormalizePath(source)
	if err != nil {
		t.Fatalf("normalize the source directory: %v", err)
	}
	if root.ConfiguredPath != expectedPath {
		t.Fatalf("configured path = %q, want the normalized %q", root.ConfiguredPath, expectedPath)
	}

	// A failed scan of the root: its candidates and the operation row are real,
	// and the inventory of the last successful generation is untouched by them.
	failed := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "failed", Stage: service.SourceScanStageTraversing,
		InputSnapshot: []byte(`{"source_root_id":"` + root.ID.String() + `"}`), TargetSourceRootID: &root.ID,
		SafeError: sourceStringPointer("the source tree could not be read"), FinishedAt: sourceTimePointer(time.Now().UTC()),
	}
	if err := setupManager.CreateOperation(ctx, failed); err != nil {
		t.Fatalf("store the failed scan operation: %v", err)
	}
	if _, err := database.NewInsert().Model(&persistence.SourceScanCandidate{
		ID: uuid.New(), OperationID: failed.ID, RelativePath: "unfinished.flac",
		SizeBytes: 12, Mtime: time.Now().UTC(), ProbeStatus: persistence.SourceProbeStatusAudio,
	}).Exec(ctx); err != nil {
		t.Fatalf("store a scan candidate: %v", err)
	}
	expected := []string{"disc/a.flac", "disc/b.flac", "disc/c.flac"}
	for index, path := range expected {
		if _, err := database.NewInsert().Model(&persistence.SourceLocation{
			ID: uuid.New(), SourceRootID: root.ID, RelativePath: path, SizeBytes: int64(128 + index),
			Mtime: time.Now().UTC(), LastSeenScanGeneration: 1,
			ProbeStatus: persistence.SourceProbeStatusAudio,
		}).Exec(ctx); err != nil {
			t.Fatalf("store the location %q: %v", path, err)
		}
	}

	state := decodeSourceRoot(t, sourceRequest(t, handler, http.MethodGet, "/sources/"+root.ID.String(), ""))
	if state.LocationCount != 3 || state.ScanGeneration != 0 || state.Stale || state.InventoryPath != nil {
		t.Fatalf("root state after a failed scan = %+v", state)
	}

	collected := make([]string, 0, len(expected))
	cursor := ""
	for {
		requestPath := "/sources/" + root.ID.String() + "/locations?limit=2"
		if cursor != "" {
			requestPath += "&cursor=" + cursor
		}
		page := decodeSourceLocations(t, sourceRequest(t, handler, http.MethodGet, requestPath, ""))
		for _, location := range page.Locations {
			collected = append(collected, location.RelativePath)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if !slices.Equal(collected, expected) {
		t.Fatalf("paged locations = %v, want %v", collected, expected)
	}
	foreign := sourceRequest(t, handler, http.MethodGet, "/sources/"+root.ID.String()+"/locations?cursor=not-base64!", "")
	if foreign.Code != http.StatusBadRequest {
		t.Fatalf("foreign cursor status=%d: %s", foreign.Code, foreign.Body.String())
	}

	confirmation := func(confirmedPath string, count int) string {
		return fmt.Sprintf(`{"confirmed_path":%q,"confirmed_location_count":%d}`, confirmedPath, count)
	}
	refused := sourceRequest(t, handler, http.MethodDelete, "/sources/"+root.ID.String(), confirmation(root.ConfiguredPath, 2))
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("deletion with a stale count status=%d: %s", refused.Code, refused.Body.String())
	}
	if _, err := inventory.GetSourceRoot(ctx, root.ID); err != nil {
		t.Fatalf("a refused deletion removed the root: %v", err)
	}

	// A queued scan of the root blocks both a path/enabled edit and a deletion.
	active := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "queued", Stage: service.SourceScanStageQueued,
		InputSnapshot: []byte(`{"source_root_id":"` + root.ID.String() + `"}`), TargetSourceRootID: &root.ID,
	}
	if err := setupManager.CreateOperation(ctx, active); err != nil {
		t.Fatalf("store the queued scan operation: %v", err)
	}
	blocked := sourceRequest(t, handler, http.MethodDelete, "/sources/"+root.ID.String(), confirmation(root.ConfiguredPath, 3))
	if blocked.Code != http.StatusConflict {
		t.Fatalf("deletion during an active scan status=%d: %s", blocked.Code, blocked.Body.String())
	}
	disabled := sourceRequest(t, handler, http.MethodPatch, "/sources/"+root.ID.String(), `{"enabled":false}`)
	if disabled.Code != http.StatusConflict {
		t.Fatalf("disable during an active scan status=%d: %s", disabled.Code, disabled.Body.String())
	}
	if _, err := database.NewDelete().Model((*persistence.Operation)(nil)).Where("id = ?", active.ID).Exec(ctx); err != nil {
		t.Fatalf("remove the queued scan operation: %v", err)
	}

	response = sourceRequest(t, handler, http.MethodDelete, "/sources/"+root.ID.String(), confirmation(root.ConfiguredPath, 3))
	if response.Code != http.StatusNoContent {
		t.Fatalf("confirmed deletion status=%d: %s", response.Code, response.Body.String())
	}
	if count, err := inventory.CountSourceLocations(ctx, root.ID); err != nil || count != 0 {
		t.Fatalf("locations left after the deletion: %d, %v", count, err)
	}
	if _, err := inventory.GetSourceRoot(ctx, root.ID); err == nil {
		t.Fatal("the deleted root is still stored")
	}
	preserved, err := os.ReadFile(track)
	if err != nil || string(preserved) != "audio bytes" {
		t.Fatalf("deleting a root touched its source file: %q, %v", preserved, err)
	}
	entries, err := os.ReadDir(outputRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("deleting a root wrote into the managed output directory: %v, %v", entries, err)
	}

	var listed api.SourcesBody
	if err := json.Unmarshal(sourceRequest(t, handler, http.MethodGet, "/sources", "").Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Sources) != 0 {
		t.Fatalf("deleted root still listed: %+v", listed.Sources)
	}
}

// TestDiagnosticPlatformRootMutationsAgainstPostgreSQL proves the platform gate
// the fake cannot: a persisted root is created on the supported platform, the
// process restarts with a mismatched instance platform, and the read endpoints
// keep serving the stored root while every mutation and a scan are refused with
// the existing 503 before they reach the repository, so the rows stay identical.
func TestDiagnosticPlatformRootMutationsAgainstPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	store := persistence.NewSettingsRepository(database)
	registry := settings.New(store, nil)
	if _, err := registry.InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"}); err != nil {
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
	supported := api.HandlerWithDependencies(api.Dependencies{
		Setup:           service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, setupManager, nil),
		SourceRoots:     service.NewSourceRoots(inventory, registry),
		SourceLocations: service.NewSourceLocations(inventory),
		Operations:      service.NewOperations(setupManager),
	})

	source := t.TempDir()
	body := fmt.Sprintf(`{"display_name":"Music","configured_path":%q,"processing_mode":"in_place"}`, source)
	response := sourceRequest(t, supported, http.MethodPost, "/sources", body)
	if response.Code != http.StatusOK {
		t.Fatalf("create source root status=%d: %s", response.Code, response.Body.String())
	}
	root := decodeSourceRoot(t, response)
	location := &persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: root.ID, RelativePath: "disc/track.flac", SizeBytes: 64,
		Mtime: time.Now().UTC(), LastSeenScanGeneration: 1, ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(location).Exec(ctx); err != nil {
		t.Fatalf("store a source location: %v", err)
	}

	diagnostic := api.HandlerWithDependencies(api.Dependencies{
		Setup:           service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}, Diagnostic: true, Reason: "instance platform mismatch"}, setupManager, nil),
		SourceRoots:     service.NewSourceRoots(inventory, registry),
		SourceLocations: service.NewSourceLocations(inventory),
		SourceScan:      service.NewSourceScanOperations(inventory, service.NewSourceRoots(inventory, registry), registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}, Diagnostic: true, Reason: "instance platform mismatch"}, nil),
		Operations:      service.NewOperations(setupManager),
	})

	// The read surface keeps serving the persisted root and its inventory.
	listed := decodeSourceRoot(t, sourceRequest(t, diagnostic, http.MethodGet, "/sources/"+root.ID.String(), ""))
	if listed.ID != root.ID || listed.ConfiguredPath != root.ConfiguredPath || listed.LocationCount != 1 {
		t.Fatalf("diagnostic read of the stored root = %+v", listed)
	}
	page := decodeSourceLocations(t, sourceRequest(t, diagnostic, http.MethodGet, "/sources/"+root.ID.String()+"/locations", ""))
	if len(page.Locations) != 1 || page.Locations[0].RelativePath != location.RelativePath {
		t.Fatalf("diagnostic read of the inventory = %+v", page.Locations)
	}

	confirmation := fmt.Sprintf(`{"confirmed_path":%q,"confirmed_location_count":1}`, root.ConfiguredPath)
	for _, mutation := range []struct{ method, path, body string }{
		{http.MethodPost, "/sources", fmt.Sprintf(`{"display_name":"Other","configured_path":%q,"processing_mode":"in_place"}`, t.TempDir())},
		{http.MethodPatch, "/sources/" + root.ID.String(), `{"display_name":"Renamed"}`},
		{http.MethodDelete, "/sources/" + root.ID.String(), confirmation},
		{http.MethodPost, "/sources/" + root.ID.String() + "/scan", ""},
	} {
		response := sourceRequest(t, diagnostic, mutation.method, mutation.path, mutation.body)
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s status=%d, want 503: %s", mutation.method, mutation.path, response.Code, response.Body.String())
		}
	}

	stored, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("a refused mutation removed the root: %v", err)
	}
	if stored.DisplayName != root.DisplayName || stored.ConfiguredPath != root.ConfiguredPath || !stored.Enabled {
		t.Fatalf("a refused mutation changed the stored root: %+v", stored)
	}
	if count, err := inventory.CountSourceLocations(ctx, root.ID); err != nil || count != 1 {
		t.Fatalf("a refused mutation changed the stored inventory: %d, %v", count, err)
	}
	if roots, err := inventory.ListSourceRoots(ctx); err != nil || len(roots) != 1 {
		t.Fatalf("a refused mutation changed the stored roots: %d, %v", len(roots), err)
	}
}
