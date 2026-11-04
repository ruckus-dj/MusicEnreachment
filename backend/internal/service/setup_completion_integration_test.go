//go:build integration

package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

func completionFixture(t *testing.T, database *bun.DB, endpoint string, clock func() time.Time) (*service.SetupService, *settings.Registry, *persistence.SettingsRepository) {
	t.Helper()
	_ = openSourceScanRiver(t, database)
	ctx := t.Context()
	store := persistence.NewSettingsRepository(database)
	registry := settings.NewRegistryWithClock(store, clock)
	platform, err := registry.InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	installations := persistence.NewSetupManagerRepository(database)
	setup := service.NewSetup(store, registry, platform, installations, nil).WithOutputReset(
		service.NewOutputReset(installations, settings.NewResetFilesystem(), []string{"analysis", "publication", "checks", "media"}),
	)
	toolsDirectory, outputDirectory := t.TempDir(), filepath.Join(t.TempDir(), "output")
	if err := setup.SaveRuntimeUpdate(ctx, service.RuntimeUpdateRequest{ToolsDirectory: &toolsDirectory, OutputDirectory: &outputDirectory, PublicationFormat: setupCompletionStringPointer("mka")}); err != nil {
		t.Fatal(err)
	}
	if err := setup.SaveMusicBrainz(ctx, "self-hosted", endpoint); err != nil {
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
	return setup, registry, store
}

func TestInitialOutputResetDirectoriesDoNotBlockSetupCompletion(t *testing.T) {
	database := testpostgres.OpenMigratedWithRiver(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"id":"5b11f4ce-a62d-471e-81fc-a69a8278c7da"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	setup, registry, _ := completionFixture(t, database, server.URL+"/ws/2", time.Now)
	state, err := setup.State(t.Context())
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
	if err := setup.Complete(t.Context()); err != nil {
		t.Fatalf("setup completion rejected the initial reset output: %v", err)
	}
}

func setupCompletionStringPointer(value string) *string { return &value }

func TestCompleteRejectsChangesDuringConnectivityCheck(t *testing.T) {
	database := testpostgres.OpenMigratedWithRiver(t)
	type responseGate struct {
		started chan struct{}
		release chan struct{}
	}
	gates := make(chan responseGate, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gate := <-gates
		close(gate.started)
		select {
		case <-gate.release:
			_, err := w.Write([]byte(`{"id":"5b11f4ce-a62d-471e-81fc-a69a8278c7da"}`))
			if err != nil {
				t.Error(err)
			}
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	setup, registry, store := completionFixture(t, database, server.URL+"/ws/2", time.Now)
	output, _, err := registry.GetOutputDirectory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"musicbrainz identity", "runtime setting", "active installation verification", "output directory changed"} {
		t.Run(change, func(t *testing.T) {
			// Given: all requirements are healthy, but the final HTTP response is held.
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			gate := responseGate{started: make(chan struct{}), release: make(chan struct{})}
			gates <- gate
			result := make(chan error, 1)
			go func() { result <- setup.Complete(ctx) }()
			select {
			case <-gate.started:
			case err := <-result:
				t.Fatalf("completion returned without the final connectivity check: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}

			// When: a concurrent writer commits before the final response.
			var err error
			switch change {
			case "musicbrainz identity":
				err = registry.SetMusicBrainzConfig(ctx, "self-hosted", server.URL+"/different/ws/2")
			case "runtime setting":
				format := "source"
				err = setup.SaveRuntimeUpdate(ctx, service.RuntimeUpdateRequest{PublicationFormat: &format})
			case "active installation verification":
				var mutationResult sql.Result
				mutationResult, err = database.ExecContext(ctx, "UPDATE tool_installation SET executable_verification_output = '{}' WHERE package_kind = 'ffmpeg'")
				if err == nil {
					var changed int64
					changed, err = mutationResult.RowsAffected()
					if err == nil && changed != 1 {
						err = fmt.Errorf("verification mutation changed %d rows, want 1", changed)
					}
				}
			case "output directory changed":
				newOutput := filepath.Join(t.TempDir(), "new-output")
				err = setup.SaveRuntimeUpdate(ctx, service.RuntimeUpdateRequest{OutputDirectory: &newOutput})
			}
			if err != nil {
				t.Fatalf("configuration write was blocked by the connectivity check: %v", err)
			}
			close(gate.release)

			// Then: the checked generation cannot complete the changed requirements.
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("completion accepted requirements changed during the final check")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if value, exists, err := store.Get(ctx, settings.SetupCompletedAtKey); err != nil || exists {
				t.Fatalf("failed completion persisted timestamp %q: exists=%v, err=%v", value, exists, err)
			}
			if change == "musicbrainz identity" {
				current, err := registry.GetMusicBrainzConfig(ctx)
				if err != nil || current.VerifiedAt != nil {
					t.Fatalf("stale check verified current configuration: %+v, %v", current, err)
				}
			}
			switch change {
			case "musicbrainz identity":
				if err := setup.SaveMusicBrainz(ctx, "self-hosted", server.URL+"/ws/2"); err != nil {
					t.Fatal(err)
				}
				if err := registry.MarkMusicBrainzVerified(ctx); err != nil {
					t.Fatal(err)
				}
			case "runtime setting":
				format := "mka"
				if err := setup.SaveRuntimeUpdate(ctx, service.RuntimeUpdateRequest{PublicationFormat: &format}); err != nil {
					t.Fatal(err)
				}
			case "active installation verification":
				if _, err := database.ExecContext(ctx, "UPDATE tool_installation SET executable_verification_output = '{\"ffmpeg\":\"ffmpeg version 8.0 build\",\"ffprobe\":\"ffprobe version 8.0 build\"}' WHERE package_kind = 'ffmpeg'"); err != nil {
					t.Fatal(err)
				}
			case "output directory changed":
				if err := setup.SaveRuntimeUpdate(ctx, service.RuntimeUpdateRequest{OutputDirectory: &output}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
