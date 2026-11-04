package service_test

import (
	"context"
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
)

const setupCompletionArtistID = "5b11f4ce-a62d-471e-81fc-a69a8278c7da"

func mockCompletionFixture(t *testing.T, endpoint string, clock func() time.Time) (*service.SetupService, *settings.Registry, *mockSettingsStore) {
	t.Helper()
	ctx := t.Context()
	store := newMockStore()
	registry := settings.NewRegistryWithClock(store, clock)
	platform, err := registry.InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	toolsDirectory, outputDirectory := t.TempDir(), t.TempDir()
	if err := registry.SetToolsDirectory(ctx, toolsDirectory, ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetOutputDirectory(ctx, outputDirectory, toolsDirectory); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, settings.PublicationFormatKey, "mka"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", endpoint); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkMusicBrainzVerified(ctx); err != nil {
		t.Fatal(err)
	}
	ffmpegID, fpcalcID := uuid.New(), uuid.New()
	_ = store.Set(ctx, settings.ActiveFFmpegInstallationKey, ffmpegID.String())
	_ = store.Set(ctx, settings.ActiveFPCalcInstallationKey, fpcalcID.String())
	installations := testInstallationLookup{items: map[uuid.UUID]*persistence.ToolInstallation{
		ffmpegID: {ID: ffmpegID, PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64", State: "ready", VerifiedAt: new(time.Time), ExecutableVerificationOutput: []byte(`{"ffmpeg":"ffmpeg version 8.0","ffprobe":"ffprobe version 8.0"}`)},
		fpcalcID: {ID: fpcalcID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64", State: "ready", VerifiedAt: new(time.Time), ExecutableVerificationOutput: []byte(`{"fpcalc":"fpcalc version 1.6.1"}`)},
	}}
	return service.NewSetup(store, registry, platform, installations, nil), registry, store
}

func TestCompleteHTTPRequiresCurrentMusicBrainzResponse(t *testing.T) {
	for _, response := range []struct {
		name, body string
		wantStatus int
	}{
		{"missing response", "", http.StatusConflict},
		{"missing artist", `{}`, http.StatusConflict},
		{"success", `{"id":"` + setupCompletionArtistID + `"}`, http.StatusNoContent},
	} {
		t.Run(response.name, func(t *testing.T) {
			requests := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.URL.RequestURI()
				if _, err := w.Write([]byte(response.body)); err != nil {
					t.Error(err)
				}
			}))
			defer upstream.Close()
			setup, _, store := mockCompletionFixture(t, upstream.URL+"/ws/2", time.Now)
			handler := api.HandlerWithSetup(setup)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/setup/complete", nil))
			if recorder.Code != response.wantStatus {
				t.Fatalf("completion status = %d, want %d: %s", recorder.Code, response.wantStatus, recorder.Body)
			}
			select {
			case route := <-requests:
				if route != "/ws/2/artist/"+setupCompletionArtistID+"?fmt=json" {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"id":"` + setupCompletionArtistID + `"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	first := time.Date(2026, 9, 28, 12, 0, 0, 123, time.UTC)
	now := first
	setup, _, store := mockCompletionFixture(t, upstream.URL, func() time.Time { return now })
	if err := setup.Complete(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := setup.Complete(t.Context()); err != nil {
		t.Fatal(err)
	}
	value, exists, err := store.Get(t.Context(), settings.SetupCompletedAtKey)
	if err != nil || !exists || value != first.Format(time.RFC3339Nano) {
		t.Fatalf("original timestamp replaced: value=%q, exists=%v, err=%v", value, exists, err)
	}
}

func TestOutputProbeSerializesConcurrentSetupOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"id":"` + setupCompletionArtistID + `"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	setup, registry, _ := mockCompletionFixture(t, server.URL+"/ws/2", time.Now)
	output, _, err := registry.GetOutputDirectory(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, area := range []string{"analysis", "publication", "checks", "media"} {
		if err := os.MkdirAll(filepath.Join(output, area), 0o700); err != nil {
			t.Fatal(err)
		}
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
	case <-registered:
	case <-ctx.Done():
		t.Fatal("state did not register for the output guard")
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("state did not reach the filesystem probe seam")
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 5 {
		t.Fatalf("semantics probe entries are not present while paused: %v, %v", entries, err)
	}
	completeDone := make(chan error, 1)
	go func() { completeDone <- setup.Complete(ctx) }()
	select {
	case <-registered:
	case <-ctx.Done():
		t.Fatal("completion did not register for the output guard")
	}
	saveDone := make(chan error, 1)
	go func() {
		saveDone <- setup.SaveRuntimeUpdate(ctx, service.RuntimeUpdateRequest{OutputDirectory: &output})
	}()
	select {
	case <-registered:
	case <-ctx.Done():
		t.Fatal("runtime save did not register for the output guard")
	}
	validateDone := make(chan error, 1)
	go func() { _, err := setup.ValidatePaths(ctx, "", ""); validateDone <- err }()
	select {
	case <-registered:
	case <-ctx.Done():
		t.Fatal("path validation did not register for the output guard")
	}
	restoreRegistration()
	releaseProbe()
	for name, result := range map[string]<-chan error{"state": stateDone, "complete": completeDone, "save runtime": saveDone, "validate paths": validateDone} {
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
	if err != nil || len(entries) != 4 {
		t.Fatalf("concurrent probes left artifacts: %v, %v", entries, err)
	}
}
