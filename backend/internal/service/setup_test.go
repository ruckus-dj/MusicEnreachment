package service

import (
	"context"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"testing"
)

func TestSetupCanOnlyCompleteWithRequiredSettings(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	platform := settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}
	service := NewSetup(store, registry, platform)
	ctx := context.Background()

	if err := service.Complete(ctx); err == nil {
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
	if err := store.Set(ctx, settings.ActiveFPCalcInstallationKey, "test-fpcalc-id"); err != nil {
		t.Fatal(err)
	}

	if err := service.Complete(ctx); err != nil {
		t.Fatalf("complete failed with all required settings: %v", err)
	}

	completed, err := registry.SetupCompleted(ctx)
	if err != nil || !completed {
		t.Fatalf("setup not marked completed: %v, %v", completed, err)
	}
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
func (m *memoryStore) SetIfAbsent(_ context.Context, k, v string) (string, error) {
	if x, ok := m.data[k]; ok {
		return x, nil
	}
	m.data[k] = v
	return v, nil
}
