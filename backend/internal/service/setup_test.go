package service

import (
	"context"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"testing"
)

func TestSetupCanOnlyCompleteWithRequiredSettings(t *testing.T) {
	store := memoryStore{}
	registry := settings.New(store, nil)
	setup := NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}})
	if err := setup.Complete(context.Background()); err == nil {
		t.Fatal("incomplete setup completed")
	}
	if err := setup.SaveRuntime(context.Background(), t.TempDir(), t.TempDir()+"/output", "mka"); err != nil {
		t.Fatal(err)
	}
	if err := setup.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type memoryStore map[string]string

func (m memoryStore) Get(_ context.Context, k string) (string, bool, error) {
	v, ok := m[k]
	return v, ok, nil
}
func (m memoryStore) Set(_ context.Context, k, v string) error { m[k] = v; return nil }
func (m memoryStore) SetIfAbsent(_ context.Context, k, v string) (string, error) {
	if x, ok := m[k]; ok {
		return x, nil
	}
	m[k] = v
	return v, nil
}
