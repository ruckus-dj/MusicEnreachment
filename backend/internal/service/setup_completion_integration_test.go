//go:build integration

package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func completionFixture(t *testing.T, database *bun.DB, endpoint string, clock func() time.Time) (*service.SetupService, *settings.Registry, *persistence.SettingsRepository) {
	t.Helper()
	ctx := t.Context()
	store := persistence.NewSettingsRepository(database)
	registry := settings.NewRegistryWithClock(store, clock)
	platform, err := registry.InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	installations := persistence.NewSetupManagerRepository(database)
	setup := service.NewSetup(store, registry, platform, installations, nil)
	if err := setup.SaveRuntime(ctx, t.TempDir(), t.TempDir(), "mka"); err != nil {
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
		{"ffmpeg", settings.ActiveFFmpegInstallationKey, json.RawMessage(`{"ffmpeg":"8.0","ffprobe":"8.0"}`)},
		{"fpcalc", settings.ActiveFPCalcInstallationKey, json.RawMessage(`{"fpcalc":"1.6"}`)},
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

func TestOutputProbeSerializesConcurrentSetupOperations(t *testing.T) {
	// The unfiltered process-global probe hook must not overlap other tests.
	database := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"id":"5b11f4ce-a62d-471e-81fc-a69a8278c7da"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	setup, registry, _ := completionFixture(t, database, server.URL+"/ws/2", time.Now)
	output, _, err := registry.GetOutputDirectory(ctx)
	if err != nil {
		t.Fatal(err)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseProbe := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseProbe()
	var pause sync.Once
	registered := make(chan string, 4)
	restoreRegistration := settings.SetProbeDirectoryRegistrationHook(func(path string) {
		if path == output {
			registered <- path
		}
	})
	defer restoreRegistration()
	restore := settings.SetProbeFilesystemSemanticsHook(func() {
		pause.Do(func() { close(entered); <-release })
	})
	defer restore()
	stateDone := make(chan error, 1)
	go func() { _, err := setup.State(ctx); stateDone <- err }()
	select {
	case <-registered: // State has registered before entering the paused probe.
	case <-ctx.Done():
		t.Fatal("state did not register for the output guard")
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("state did not reach the filesystem probe seam")
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("semantics probe entries are not present while paused: %v, %v", entries, err)
	}

	completeDone := make(chan error, 1)
	go func() { completeDone <- setup.Complete(ctx) }()
	select {
	case <-registered: // Complete has registered and is now waiting on State's guard.
	case <-ctx.Done():
		t.Fatal("completion did not register for the output guard")
	}
	saveDone := make(chan error, 1)
	go func() { saveDone <- setup.SaveRuntime(ctx, "", output, "") }()
	select {
	case <-registered:
	case <-ctx.Done():
		t.Fatal("runtime save did not register for the output guard")
	}
	validateDone := make(chan error, 1)
	go func() {
		_, err := setup.ValidatePaths(ctx, "", "")
		validateDone <- err
	}()
	select {
	case <-registered:
	case <-ctx.Done():
		t.Fatal("path validation did not register for the output guard")
	}
	restoreRegistration()
	releaseProbe()

	for name, result := range map[string]<-chan error{
		"state": stateDone, "complete": completeDone, "save runtime": saveDone, "validate paths": validateDone,
	} {
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case <-ctx.Done():
			t.Errorf("%s remained blocked: %v", name, ctx.Err())
		}
	}
	if _, err := settings.ProbeOutputDirectory(output, false); err != nil {
		t.Fatalf("probe retry after concurrent setup operations: %v", err)
	}
	entries, err = os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("concurrent probes left artifacts: %v, %v", entries, err)
	}
}

func TestCompleteRejectsChangesDuringConnectivityCheck(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	for _, change := range []string{"musicbrainz identity", "runtime setting", "active installation verification", "output no longer empty"} {
		t.Run(change, func(t *testing.T) {
			// Given: all requirements are healthy, but the final HTTP response is held.
			testpostgres.ResetAndMigrate(t, database)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-release:
					_, err := w.Write([]byte(`{"id":"5b11f4ce-a62d-471e-81fc-a69a8278c7da"}`))
					if err != nil {
						t.Error(err)
					}
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer close(release)
			setup, registry, store := completionFixture(t, database, server.URL+"/ws/2", time.Now)
			result := make(chan error, 1)
			go func() { result <- setup.Complete(ctx) }()
			select {
			case <-started:
			case err := <-result:
				t.Fatalf("completion returned without the final connectivity check: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}

			// When: a concurrent writer commits before the final response.
			var err error
			switch change {
			case "musicbrainz identity":
				err = registry.SetMusicBrainzConfig(ctx, "self-hosted", server.URL+"/ws/2")
			case "runtime setting":
				err = registry.SetPublicationFormat(ctx, "source")
			case "active installation verification":
				_, err = database.ExecContext(ctx, "UPDATE tool_installation SET executable_versions = '{}' WHERE package_kind = 'ffmpeg'")
			case "output no longer empty":
				var output string
				output, _, err = registry.GetOutputDirectory(ctx)
				if err == nil {
					err = os.WriteFile(filepath.Join(output, "new-file.mka"), []byte("changed during check"), 0o600)
				}
			}
			if err != nil {
				t.Fatalf("configuration write was blocked by the connectivity check: %v", err)
			}
			select {
			case release <- struct{}{}:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}

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
		})
	}
}

func TestCompleteHTTPRequiresCurrentMusicBrainzResponse(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	for _, response := range []struct {
		name, body string
		wantStatus int
	}{
		{"missing response", "", http.StatusConflict},
		{"missing artist", `{}`, http.StatusConflict},
		{"success", `{"id":"5b11f4ce-a62d-471e-81fc-a69a8278c7da"}`, http.StatusNoContent},
	} {
		t.Run(response.name, func(t *testing.T) {
			// Given: a previously verified configuration and a controlled endpoint.
			testpostgres.ResetAndMigrate(t, database)
			requests := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.URL.RequestURI()
				if _, err := w.Write([]byte(response.body)); err != nil {
					t.Error(err)
				}
			}))
			defer upstream.Close()
			setup, _, store := completionFixture(t, database, upstream.URL+"/ws/2", time.Now)
			handler := api.HandlerWithSetup(setup)

			// When: the actual completion endpoint receives its final command.
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/setup/complete", nil))

			// Then: the new response, not the stored success, gates completion.
			if recorder.Code != response.wantStatus {
				t.Fatalf("completion status = %d, want %d: %s", recorder.Code, response.wantStatus, recorder.Body)
			}
			select {
			case route := <-requests:
				if route != "/ws/2/artist/5b11f4ce-a62d-471e-81fc-a69a8278c7da?fmt=json" {
					t.Fatalf("unexpected connectivity route: %s", route)
				}
			default:
				t.Fatal("completion did not check the current MusicBrainz endpoint")
			}
			_, completed, err := store.Get(t.Context(), settings.SetupCompletedAtKey)
			if err != nil || completed != (response.wantStatus == http.StatusNoContent) {
				t.Fatalf("completion persisted=%v, err=%v", completed, err)
			}
		})
	}
}

func TestCompletePreservesOriginalTimestamp(t *testing.T) {
	t.Parallel()
	// Given: a reachable endpoint and a clock advanced after initial completion.
	database := testpostgres.OpenMigrated(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"id":"5b11f4ce-a62d-471e-81fc-a69a8278c7da"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	first := time.Date(2026, 9, 28, 12, 0, 0, 123, time.UTC)
	now := first
	setup, _, store := completionFixture(t, database, upstream.URL, func() time.Time { return now })
	if err := setup.Complete(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)

	// When: completion is requested again.
	if err := setup.Complete(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Then: the original fact remains immutable.
	value, exists, err := store.Get(t.Context(), settings.SetupCompletedAtKey)
	if err != nil || !exists || value != first.Format(time.RFC3339Nano) {
		t.Fatalf("original timestamp replaced: value=%q, exists=%v, err=%v", value, exists, err)
	}
}
