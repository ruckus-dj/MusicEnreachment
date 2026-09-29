package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestSetupCanOnlyCompleteWithRequiredSettings(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	platform, err := registry.InitializePlatform(t.Context(), settings.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	installations := testInstallationLookup{items: make(map[uuid.UUID]*persistence.ToolInstallation)}
	setup := NewSetup(store, registry, platform, installations, nil)
	ctx := context.Background()

	if err := setup.Complete(ctx); err == nil {
		t.Fatal("complete succeeded without required settings")
	}

	toolsPath := t.TempDir()
	outputPath := t.TempDir()
	if err := registry.SetToolsDirectory(ctx, toolsPath, ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetOutputDirectory(ctx, outputPath, ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPublicationFormat(ctx, "mka"); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"id":"5b11f4ce-a62d-471e-81fc-a69a8278c7da"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", upstream.URL); err != nil {
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

	if err := setup.Complete(ctx); err == nil {
		t.Fatal("complete accepted ready installations without executable verification")
	}
	verifiedAt := time.Now().UTC()
	installations.items[ffmpegID].VerifiedAt = &verifiedAt
	installations.items[ffmpegID].ExecutableVersions = []byte(`{"ffmpeg":"ffmpeg version 8.0"}`)
	installations.items[fpcalcID].VerifiedAt = &verifiedAt
	installations.items[fpcalcID].ExecutableVersions = []byte(`{"fpcalc":"fpcalc version 1.6.1"}`)
	if err := setup.Complete(ctx); err == nil {
		t.Fatal("complete accepted FFmpeg without a verified ffprobe")
	}
	installations.items[ffmpegID].ExecutableVersions = []byte(`{"ffmpeg":"ffmpeg version 8.0","ffprobe":"ffprobe version 8.0"}`)
	savedCaseSensitivity := store.data[settings.OutputCaseSensitiveKey]
	if savedCaseSensitivity == "true" {
		store.data[settings.OutputCaseSensitiveKey] = "false"
	} else {
		store.data[settings.OutputCaseSensitiveKey] = "true"
	}
	if err := setup.Complete(ctx); err == nil || !strings.Contains(err.Error(), "semantics") {
		t.Fatalf("complete accepted stale filesystem semantics: %v", err)
	}
	store.data[settings.OutputCaseSensitiveKey] = savedCaseSensitivity
	if err := setup.Complete(ctx); err != nil {
		t.Fatalf("complete failed with all required settings: %v", err)
	}

	completed, err := registry.SetupCompleted(ctx)
	if err != nil || !completed {
		t.Fatalf("setup not marked completed: %v, %v", completed, err)
	}
	firstCompletion := store.data[settings.SetupCompletedAtKey]
	if err := os.WriteFile(outputPath+"/published.mka", []byte("published"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := setup.State(ctx)
	if err != nil || !state.ConfigurationHealth.Healthy {
		t.Fatalf("published output degraded completed setup: %#v, %v", state.ConfigurationHealth, err)
	}
	if err := os.Remove(outputPath + "/published.mka"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(outputPath); err != nil {
		t.Fatal(err)
	}
	state, err = setup.State(ctx)
	if err != nil || !state.Completed || state.ConfigurationHealth.Healthy {
		t.Fatalf("missing output directory was not reported: %#v, %v", state, err)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("health check recreated missing output directory: %v", err)
	}
	if err := os.Mkdir(outputPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(toolsPath); err != nil {
		t.Fatal(err)
	}
	state, err = setup.State(ctx)
	if err != nil || !strings.Contains(strings.Join(state.ConfigurationHealth.Problems, " "), "tools directory is unavailable") {
		t.Fatalf("missing tools directory was not reported: %#v, %v", state.ConfigurationHealth, err)
	}
	if _, err := os.Stat(toolsPath); !os.IsNotExist(err) {
		t.Fatalf("health check recreated missing tools directory: %v", err)
	}
	installations.items[ffmpegID].State = "failed"
	state, err = setup.State(ctx)
	if err != nil || !state.Completed || state.ConfigurationHealth.Healthy {
		t.Fatalf("completed setup did not report degraded health: %#v, %v", state, err)
	}
	if err := setup.Complete(ctx); err == nil {
		t.Fatal("complete accepted degraded health")
	}
	if store.data[settings.SetupCompletedAtKey] != firstCompletion {
		t.Fatal("completion timestamp changed after health degradation")
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

func TestSaveRuntimeRejectsWhitespaceDirectoryLikeRegistry(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	registry := settings.New(store, nil)
	setup := NewSetup(store, registry, settings.PlatformState{}, nil, nil)

	if err := registry.SetToolsDirectory(ctx, "   ", ""); err == nil {
		t.Fatal("registry accepted a whitespace-only directory")
	}
	if err := setup.SaveRuntime(ctx, "   ", "", ""); err == nil {
		t.Fatal("service accepted a directory rejected by registry")
	}
	if err := setup.SaveRuntime(ctx, "", "   ", ""); err == nil {
		t.Fatal("service accepted a whitespace-only output directory")
	}
	if len(store.data) != 0 {
		t.Fatalf("invalid directories changed settings: %#v", store.data)
	}
}

func TestSaveRuntimePreservesOmittedSettingsAndBlocksToolsMove(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	registry := settings.New(store, nil)
	toolsPath, outputPath := t.TempDir(), t.TempDir()
	if err := registry.SetToolsDirectory(ctx, toolsPath, outputPath); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetOutputDirectory(ctx, outputPath, toolsPath); err != nil {
		t.Fatal(err)
	}
	installation := &persistence.ToolInstallation{ID: uuid.New(), PackageKind: "ffmpeg", State: "ready"}
	setup := NewSetup(store, registry, settings.PlatformState{}, testInstallationLookup{
		items: map[uuid.UUID]*persistence.ToolInstallation{installation.ID: installation},
	}, nil)
	originalTools := store.data[settings.ToolsDirectoryKey]
	originalOutput := store.data[settings.OutputDirectoryKey]

	if err := setup.SaveRuntime(ctx, "", "", "mka"); err != nil {
		t.Fatal(err)
	}
	if store.data[settings.ToolsDirectoryKey] != originalTools ||
		store.data[settings.OutputDirectoryKey] != originalOutput ||
		store.data[settings.PublicationFormatKey] != "mka" {
		t.Fatalf("partial update changed omitted settings: %#v", store.data)
	}
	if err := setup.SaveRuntime(ctx, t.TempDir(), "", "source"); err == nil {
		t.Fatal("direct tools directory change accepted with installations")
	}
	if store.data[settings.ToolsDirectoryKey] != originalTools ||
		store.data[settings.PublicationFormatKey] != "mka" {
		t.Fatalf("rejected tools move partially changed settings: %#v", store.data)
	}
}

func TestSaveRuntimeCommitsValidatedFieldsInOneWrite(t *testing.T) {
	ctx := context.Background()
	store := &recordingRuntimeStore{memoryStore: newMemoryStore()}
	setup := NewSetup(store, settings.New(store, nil), settings.PlatformState{}, nil, nil)

	if err := setup.SaveRuntime(ctx, t.TempDir(), t.TempDir(), "mka"); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || len(store.data) != 5 {
		t.Fatalf("runtime settings were not committed together: calls=%d settings=%#v", store.calls, store.data)
	}
}

type recordingRuntimeStore struct {
	*memoryStore
	calls int
}

func (store *recordingRuntimeStore) SetMany(ctx context.Context, values map[string]string) error {
	store.calls++
	return store.memoryStore.SetMany(ctx, values)
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

func (m *memoryStore) CompleteSetupIfCurrent(ctx context.Context, expected map[string]string, value string) error {
	for name, checked := range expected {
		if m.data[name] != checked {
			return fmt.Errorf("setup configuration changed during final check")
		}
	}
	m.data[settings.MusicBrainzVerifiedAtKey] = value
	_, err := m.SetIfAbsent(ctx, settings.SetupCompletedAtKey, value)
	return err
}

func (m *memoryStore) InitializePlatform(_ context.Context, goos, goarch string) (string, string, bool, error) {
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
