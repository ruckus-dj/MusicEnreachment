package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

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

func (m *mockSettingsStore) SetIfAbsent(_ context.Context, key, value string) (string, error) {
	if _, exists := m.data[key]; !exists {
		m.data[key] = value
		return value, nil
	}
	return m.data[key], nil
}

func TestCheckMusicBrainz_PublicMode_Success(t *testing.T) {
	store := newMockStore()
	_ = store.Set(context.Background(), settings.MusicBrainzModeKey, "public")

	registry := settings.NewRegistryWithClock(store, func() time.Time { return time.Now() })
	setupService := service.NewSetup(store, registry, settings.PlatformState{
		Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"},
	})

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
	})

	err := setupService.Complete(context.Background())
	if err == nil {
		t.Error("expected Complete to fail without MusicBrainz verification")
	}
}

func TestComplete_Success(t *testing.T) {
	store := newMockStore()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_ = store.Set(context.Background(), settings.PlatformGOOSKey, "linux")
	_ = store.Set(context.Background(), settings.PlatformGOARCHKey, "amd64")
	_ = store.Set(context.Background(), settings.ToolsDirectoryKey, "/tools")
	_ = store.Set(context.Background(), settings.OutputDirectoryKey, "/output")
	_ = store.Set(context.Background(), settings.OutputCaseSensitiveKey, "true")
	_ = store.Set(context.Background(), settings.OutputUnicodeNormalizationKey, "nfc")
	_ = store.Set(context.Background(), settings.PublicationFormatKey, "source")
	_ = store.Set(context.Background(), settings.MusicBrainzModeKey, "public")
	_ = store.Set(context.Background(), settings.MusicBrainzVerifiedAtKey, now)
	_ = store.Set(context.Background(), settings.ActiveFFmpegInstallationKey, "some-uuid")
	_ = store.Set(context.Background(), settings.ActiveFPCalcInstallationKey, "another-uuid")

	registry := settings.NewRegistryWithClock(store, func() time.Time { return time.Now() })
	setupService := service.NewSetup(store, registry, settings.PlatformState{
		Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"},
	})

	if err := setupService.Complete(context.Background()); err != nil {
		t.Errorf("expected Complete to succeed, got: %v", err)
	}

	completed, _ := registry.SetupCompleted(context.Background())
	if !completed {
		t.Error("expected setup_completed_at to be set")
	}
}
