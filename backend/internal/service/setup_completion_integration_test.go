//go:build integration

package service_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func completionFixture(t *testing.T, database *bun.DB, endpoint string, clock func() time.Time) (*service.SetupService, *settings.Registry, *service.GenreCatalogService) {
	t.Helper()
	river := openSourceScanRiver(t, database)
	ctx := t.Context()
	store := persistence.NewSettingsRepository(database)
	registry := settings.NewRegistryWithClock(store, clock)
	platform, err := registry.InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	installations := persistence.NewSetupManagerRepository(database)
	catalogRepo := persistence.NewGenreCatalogRepository(database, installations)
	provider := service.NewProviderFactory(registry, nil, nil, http.DefaultClient, nil)
	catalog := service.NewGenreCatalogService(catalogRepo, registry, provider).
		WithRefreshQueue(catalogRepo, river).WithOperations(service.NewOperations(installations)).WithSetupCompletion(registry)
	setup := service.NewSetup(store, registry, platform, installations, nil).WithOutputReset(
		service.NewOutputReset(installations, settings.NewResetFilesystem(), []string{"analysis", "publication", "checks", "media"}),
	).WithGenreCatalog(catalog)
	toolsDirectory, outputDirectory := t.TempDir(), filepath.Join(t.TempDir(), "output")
	if err := setup.SaveRuntimeUpdate(ctx, service.RuntimeUpdateRequest{ToolsDirectory: &toolsDirectory, OutputDirectory: &outputDirectory, PublicationFormat: setupCompletionStringPointer("mka")}); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.SaveMusicBrainz(ctx, "self-hosted", endpoint); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkMusicBrainzVerified(ctx); err != nil {
		t.Fatal(err)
	}
	for _, required := range []struct {
		kind, key string
		versions  json.RawMessage
	}{
		{"ffmpeg", settings.ActiveFFmpegInstallationKey, json.RawMessage(`{"ffmpeg":"ffmpeg version 8.0 build","ffprobe":"ffprobe version 8.0 build"}`)},
		{"fpcalc", settings.ActiveFPCalcInstallationKey, json.RawMessage(`{"fpcalc":"fpcalc version 1.6.0 build"}`)},
	} {
		installation := &persistence.ToolInstallation{
			ID: uuid.New(), PackageKind: required.kind, PlatformGOOS: "linux", PlatformGOARCH: "amd64",
			SourceName: "test", ReleaseIdentity: "1", RelativePath: required.kind + "/1", State: "preparing",
		}
		if err := installations.CreateInstallation(ctx, installation); err != nil {
			t.Fatal(err)
		}
		if err := installations.MarkInstallationReady(ctx, installation.ID, required.versions, clock()); err != nil {
			t.Fatal(err)
		}
		if activated, err := installations.ActivateInstallationDuringSetup(ctx, installation.ID, required.kind, "linux", "amd64", required.key); err != nil || !activated {
			t.Fatalf("activate setup fixture installation: activated=%t err=%v", activated, err)
		}
	}
	return setup, registry, catalog
}

func TestInitialOutputResetDirectoriesDoNotBlockSetupCompletion(t *testing.T) {
	database := testpostgres.OpenMigratedWithRiver(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case "/ws/2/genre/all":
			body = `{"genre-count":1,"genre-offset":0,"genres":[{"id":"11111111-1111-4111-8111-111111111111","name":"ambient"}]}`
		case "/ws/2/artist/" + setupCompletionArtistID:
			body = `{"id":"` + setupCompletionArtistID + `"}`
		default:
			t.Errorf("unexpected MusicBrainz request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(body)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	ctx := t.Context()
	setup, registry, catalog := completionFixture(t, database, server.URL+"/ws/2", time.Now)
	state, err := setup.State(ctx)
	if err != nil || !state.ConfigurationHealth.Healthy {
		t.Fatalf("initial output reset made configuration unhealthy: %#v, %v", state.ConfigurationHealth, err)
	}
	if _, err := setup.ValidatePaths(t.Context(), "", ""); err != nil {
		t.Fatalf("saved output root failed validation: %v", err)
	}
	for _, area := range []string{"analysis", "publication", "checks", "media"} {
		output, _, err := registry.GetOutputDirectory(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(filepath.Join(output, area)); err != nil || !info.IsDir() {
			t.Fatalf("initial output area %q missing: info=%v err=%v", area, info, err)
		}
	}
	config, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The last successfully published catalog may still be from a different provider.
	if _, err := database.NewRaw(`UPDATE genre_catalog SET published_configuration_identity=?, updated_at=now() WHERE singleton`, uuid.NewString()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := registry.CompleteSetupIfCurrent(ctx, state.Platform.Platform, state.Runtime, config); !errors.Is(err, settings.ErrSetupGenreCatalogNotReady) {
		t.Fatalf("completion committed without the current catalog: %v", err)
	}
	if completed, err := registry.SetupCompleted(ctx); completed || err != nil {
		t.Fatalf("invalid catalog changed setup completion: %v, %v", completed, err)
	}
	if _, err := catalog.StartSetupRefresh(ctx, uuid.NewString()); !errors.Is(err, persistence.ErrGenreCatalogSetupIdentityStale) {
		t.Fatalf("stale identity was admitted: %v", err)
	}
	snapshot, err := catalog.StartSetupRefresh(ctx, config.Identity)
	if err != nil {
		t.Fatal(err)
	}
	repo := persistence.NewSetupManagerRepository(database)
	operation, err := repo.GetOperation(ctx, snapshot.ID)
	if err != nil || operation.RiverJobID == nil {
		t.Fatalf("durable setup catalog operation is missing a River job: %v, %v", operation, err)
	}
	if _, err := database.NewRaw(`UPDATE river_job SET state='discarded', finalized_at=now() WHERE id=?`, *operation.RiverJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := catalog.DispatchDueRefresh(ctx); err != nil {
		t.Fatalf("retire a lost Setup delivery without scheduling another: %v", err)
	}
	lost, err := repo.GetOperation(ctx, snapshot.ID)
	if err != nil || lost.State != "failed" {
		t.Fatalf("lost Setup delivery was not settled: %v, %v", lost, err)
	}
	catalogState, err := persistence.NewGenreCatalogRepository(database).State(ctx)
	if err != nil || catalogState.RefreshRequest.OperationID != snapshot.ID || catalogState.RefreshDueAt != nil {
		t.Fatalf("recovery unexpectedly scheduled a Setup refresh: %+v, %v", catalogState.RefreshRequest, err)
	}
	snapshot, err = catalog.StartSetupRefresh(ctx, config.Identity)
	if err != nil || snapshot.ID == lost.ID {
		t.Fatalf("could not explicitly retry the lost Setup load: %+v, %v", snapshot, err)
	}
	operation, err = repo.GetOperation(ctx, snapshot.ID)
	if err != nil || operation.RiverJobID == nil {
		t.Fatalf("retry Setup catalog operation is missing a River job: %v, %v", operation, err)
	}
	if err := catalog.RefreshDelivery(ctx, snapshot.ID, *operation.RiverJobID); err != nil {
		t.Fatalf("publish the complete setup catalog: %v", err)
	}
	state, err = setup.State(ctx)
	if err != nil || !state.GenreCatalog.Ready || state.GenreCatalog.Count != 1 {
		t.Fatalf("published catalog did not unlock Setup: %+v, %v", state.GenreCatalog, err)
	}
	if err := setup.Complete(ctx, config.Identity); err != nil {
		t.Fatalf("setup completion rejected the initial reset output: %v", err)
	}
}

func setupCompletionStringPointer(value string) *string { return &value }
