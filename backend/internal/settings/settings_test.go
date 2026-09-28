package settings

import (
	"context"
	"log/slog"
	"testing"
)

type memoryStore map[string]string

func (m memoryStore) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}
func (m memoryStore) Set(_ context.Context, key, value string) error { m[key] = value; return nil }
func (m memoryStore) SetIfAbsent(_ context.Context, key, value string) (string, error) {
	if got, ok := m[key]; ok {
		return got, nil
	}
	m[key] = value
	return value, nil
}

func TestPlatformIsImmutableAndMismatchIsDiagnostic(t *testing.T) {
	r := New(memoryStore{}, nil)
	ctx := context.Background()
	if state, err := r.InitializePlatform(ctx, Platform{"linux", "amd64"}); err != nil || state.Diagnostic {
		t.Fatalf("first platform = %#v, %v", state, err)
	}
	if state, err := r.InitializePlatform(ctx, Platform{"darwin", "arm64"}); err != nil || !state.Diagnostic {
		t.Fatalf("mismatch = %#v, %v", state, err)
	}
}
func TestLogLevelChangesWithoutRestart(t *testing.T) {
	level := new(slog.LevelVar)
	r := New(memoryStore{}, level)
	if err := r.SetLogLevel(context.Background(), "warn"); err != nil || level.Level() != slog.LevelWarn {
		t.Fatalf("level = %v, %v", level.Level(), err)
	}
}
func TestFilesystemProbes(t *testing.T) {
	root := t.TempDir()
	if err := ProbeWritableEmpty(root + "/new"); err != nil {
		t.Fatal(err)
	}
	if !PathsOverlap(root, root+"/child") {
		t.Fatal("nested paths must overlap")
	}
}
