package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestSetupCanOnlyCompleteWithRequiredSettings(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	platform := settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}
	installations := testInstallationLookup{items: make(map[uuid.UUID]*persistence.ToolInstallation)}
	setup := NewSetup(store, registry, platform, installations, nil)
	ctx := context.Background()

	if err := setup.Complete(ctx); err == nil {
		t.Fatal("complete succeeded without required settings")
	}

	if err := registry.SetToolsDirectory(ctx, t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetOutputDirectory(ctx, t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPublicationFormat(ctx, "mka"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetMusicBrainzConfig(ctx, "public", ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkMusicBrainzVerified(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, settings.ActiveFFmpegInstallationKey, "test-ffmpeg-id"); err != nil {
		t.Fatal(err)
	}
	ffmpegID := uuid.New()
	fpcalcID := uuid.New()
	if err := store.Set(ctx, settings.ActiveFFmpegInstallationKey, ffmpegID.String()); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, settings.ActiveFPCalcInstallationKey, fpcalcID.String()); err != nil {
		t.Fatal(err)
	}
	installations.items[ffmpegID] = &persistence.ToolInstallation{ID: ffmpegID, PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64", State: "ready"}
	installations.items[fpcalcID] = &persistence.ToolInstallation{ID: fpcalcID, PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64", State: "ready"}

	if err := setup.Complete(ctx); err != nil {
		t.Fatalf("complete failed with all required settings: %v", err)
	}

	completed, err := registry.SetupCompleted(ctx)
	if err != nil || !completed {
		t.Fatalf("setup not marked completed: %v, %v", completed, err)
	}
}

func TestSaveRuntimeDoesNotPartiallyPersistInvalidSettings(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	registry := settings.New(store, nil)
	setup := NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	toolsPath := t.TempDir()
	outputPath := t.TempDir()

	if err := setup.SaveRuntime(ctx, toolsPath, outputPath, "invalid"); err == nil {
		t.Fatal("invalid publication format was accepted")
	}
	if len(store.data) != 0 {
		t.Fatalf("invalid settings were partially persisted: %#v", store.data)
	}
}

type testInstallationLookup struct {
	items map[uuid.UUID]*persistence.ToolInstallation
}

func (lookup testInstallationLookup) GetInstallation(_ context.Context, id uuid.UUID) (*persistence.ToolInstallation, error) {
	installation, ok := lookup.items[id]
	if !ok {
		return nil, fmt.Errorf("installation not found")
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

func newMemoryStore() *memoryStore {
	return &memoryStore{data: make(map[string]string)}
}

type memoryStore struct {
	data map[string]string
}

func (m *memoryStore) Get(_ context.Context, k string) (string, bool, error) {
	v, ok := m.data[k]
	return v, ok, nil
}
func (m *memoryStore) Set(_ context.Context, k, v string) error { m.data[k] = v; return nil }
func (m *memoryStore) SetMany(_ context.Context, values map[string]string) error {
	for k, v := range values {
		m.data[k] = v
	}
	return nil
}
func (m *memoryStore) SetIfAbsent(_ context.Context, k, v string) (string, error) {
	if x, ok := m.data[k]; ok {
		return x, nil
	}
	m.data[k] = v
	return v, nil
}
