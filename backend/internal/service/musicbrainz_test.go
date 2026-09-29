package service_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type successfulMusicBrainzCheck struct{}

func (successfulMusicBrainzCheck) CheckConnectivity(context.Context, string, string) musicbrainz.CheckResult {
	return musicbrainz.CheckResult{Success: true}
}

type mockSettingsStore struct {
	data map[string]string
}

func newMockStore() *mockSettingsStore {
	return &mockSettingsStore{data: make(map[string]string)}
}

func (m *mockSettingsStore) Get(_ context.Context, key string) (string, bool, error) {
	val, ok := m.data[key]
	return val, ok, nil
}

func (m *mockSettingsStore) Set(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func (m *mockSettingsStore) SetMany(_ context.Context, values map[string]string) error {
	for key, value := range values {
		m.data[key] = value
	}
	return nil
}

func (m *mockSettingsStore) SetIfAbsent(_ context.Context, key, value string) (string, error) {
	if _, exists := m.data[key]; !exists {
		m.data[key] = value
		return value, nil
	}
	return m.data[key], nil
}

func (m *mockSettingsStore) CompleteSetupIfCurrent(ctx context.Context, expected map[string]string, value string) error {
	for key, checked := range expected {
		if m.data[key] != checked {
			return errors.New("setup configuration changed during final check")
		}
	}
	m.data[settings.MusicBrainzVerifiedAtKey] = value
	_, err := m.SetIfAbsent(ctx, settings.SetupCompletedAtKey, value)
	return err
}

func (m *mockSettingsStore) InitializePlatform(_ context.Context, goos, goarch string) (string, string, bool, error) {
	persistedOS, hasOS := m.data[settings.PlatformGOOSKey]
	persistedArch, hasArch := m.data[settings.PlatformGOARCHKey]
	if hasOS != hasArch {
		return persistedOS, persistedArch, false, nil
	}
	if !hasOS {
		m.data[settings.PlatformGOOSKey] = goos
		m.data[settings.PlatformGOARCHKey] = goarch
		return goos, goarch, true, nil
	}
	return persistedOS, persistedArch, true, nil
}

func TestCheckMusicBrainz_PublicMode_Success(t *testing.T) {
	store := newMockStore()
	_ = store.Set(context.Background(), settings.MusicBrainzModeKey, "public")

	registry := settings.NewRegistryWithClock(store, func() time.Time { return time.Now() })
	setupService := service.NewSetup(store, registry, settings.PlatformState{
		Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"},
	}, nil, successfulMusicBrainzCheck{})

	if err := setupService.CheckMusicBrainz(context.Background()); err != nil {
		t.Errorf("expected success, got: %v", err)
	}

	config, _ := registry.GetMusicBrainzConfig(context.Background())
	if config.VerifiedAt == nil {
		t.Error("expected verification timestamp to be set")
	}
}

func TestCheckMusicBrainz_ConfigChange_InvalidatesVerification(t *testing.T) {
	store := newMockStore()
	_ = store.Set(context.Background(), settings.MusicBrainzModeKey, "public")

	registry := settings.NewRegistryWithClock(store, func() time.Time { return time.Now() })
	_ = registry.MarkMusicBrainzVerified(context.Background())

	config, _ := registry.GetMusicBrainzConfig(context.Background())
	if config.VerifiedAt == nil {
		t.Fatal("expected initial verification timestamp")
	}

	_ = registry.SetMusicBrainzConfig(context.Background(), "self-hosted", "https://mb.example.com")

	newConfig, _ := registry.GetMusicBrainzConfig(context.Background())
	if newConfig.VerifiedAt != nil {
		t.Error("expected verification to be cleared after config change")
	}
}

func TestComplete_RequiresMusicBrainzVerification(t *testing.T) {
	store := newMockStore()
	_ = store.Set(context.Background(), settings.PlatformGOOSKey, "linux")
	_ = store.Set(context.Background(), settings.PlatformGOARCHKey, "amd64")
	_ = store.Set(context.Background(), settings.ToolsDirectoryKey, "/tools")
	_ = store.Set(context.Background(), settings.OutputDirectoryKey, "/output")
	_ = store.Set(context.Background(), settings.PublicationFormatKey, "source")
	_ = store.Set(context.Background(), settings.MusicBrainzModeKey, "public")

	registry := settings.NewRegistryWithClock(store, func() time.Time { return time.Now() })
	setupService := service.NewSetup(store, registry, settings.PlatformState{
		Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"},
	}, nil, nil)

	err := setupService.Complete(context.Background())
	if err == nil {
		t.Error("expected Complete to fail without MusicBrainz verification")
	}
}

func TestComplete_Success(t *testing.T) {
	store := newMockStore()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"id":"5b11f4ce-a62d-471e-81fc-a69a8278c7da"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_ = store.Set(context.Background(), settings.PlatformGOOSKey, "linux")
	_ = store.Set(context.Background(), settings.PlatformGOARCHKey, "amd64")
	_ = store.Set(context.Background(), settings.ToolsDirectoryKey, t.TempDir())
	_ = store.Set(context.Background(), settings.PublicationFormatKey, "source")
	_ = store.Set(context.Background(), settings.MusicBrainzModeKey, "public")
	_ = store.Set(context.Background(), settings.MusicBrainzVerifiedAtKey, now)
	ffmpegID := uuid.New()
	fpcalcID := uuid.New()
	_ = store.Set(context.Background(), settings.ActiveFFmpegInstallationKey, ffmpegID.String())
	_ = store.Set(context.Background(), settings.ActiveFPCalcInstallationKey, fpcalcID.String())

	registry := settings.NewRegistryWithClock(store, func() time.Time { return time.Now() })
	if err := registry.SetMusicBrainzConfig(t.Context(), "self-hosted", upstream.URL); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetToolsDirectory(t.Context(), t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetOutputDirectory(context.Background(), t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	installations := testInstallationLookup{items: map[uuid.UUID]*persistence.ToolInstallation{
		ffmpegID: {ID: ffmpegID, PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64", State: "ready", VerifiedAt: new(time.Time), ExecutableVersions: []byte(`{"ffmpeg":"ffmpeg version 8.0","ffprobe":"ffprobe version 8.0"}`)},
		fpcalcID: {ID: fpcalcID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64", State: "ready", VerifiedAt: new(time.Time), ExecutableVersions: []byte(`{"fpcalc":"fpcalc version 1.6.1"}`)},
	}}
	setupService := service.NewSetup(store, registry, settings.PlatformState{
		Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"},
	}, installations, nil)

	if err := setupService.Complete(context.Background()); err != nil {
		t.Errorf("expected Complete to succeed, got: %v", err)
	}

	completed, _ := registry.SetupCompleted(context.Background())
	if !completed {
		t.Error("expected setup_completed_at to be set")
	}
}

type testInstallationLookup struct {
	items map[uuid.UUID]*persistence.ToolInstallation
}

func (lookup testInstallationLookup) GetInstallation(_ context.Context, id uuid.UUID) (*persistence.ToolInstallation, error) {
	installation, ok := lookup.items[id]
	if !ok {
		return nil, errors.New("installation not found")
	}
	return installation, nil
}

func (lookup testInstallationLookup) ListInstallations(_ context.Context, _, _, _ string) ([]persistence.ToolInstallation, error) {
	installations := make([]persistence.ToolInstallation, 0, len(lookup.items))
	for _, installation := range lookup.items {
		installations = append(installations, *installation)
	}
	return installations, nil
}
