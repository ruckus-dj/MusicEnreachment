package settings_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type memoryStore struct {
	data map[string]string
}

type recordingStore struct {
	*memoryStore
	setManyCalls int
}

func (store *recordingStore) SetMany(ctx context.Context, values map[string]string) error {
	store.setManyCalls++
	return store.memoryStore.SetMany(ctx, values)
}

func (store *recordingStore) UpdateRuntime(ctx context.Context, _ string, values map[string]string) error {
	return store.SetMany(ctx, values)
}

func (m *memoryStore) Get(_ context.Context, key string) (string, bool, error) {
	value, ok := m.data[key]
	return value, ok, nil
}

func (m *memoryStore) Set(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func (m *memoryStore) SetMany(_ context.Context, values map[string]string) error {
	for key, value := range values {
		m.data[key] = value
	}
	return nil
}

func (m *memoryStore) SetIfAbsent(_ context.Context, key, value string) (string, error) {
	if existing, ok := m.data[key]; ok {
		return existing, nil
	}
	m.data[key] = value
	return value, nil
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

func newMemoryStore() *memoryStore {
	return &memoryStore{data: make(map[string]string)}
}

func TestPlatformInitializationIsAtomic(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	ctx := context.Background()

	current := settings.Platform{GOOS: "linux", GOARCH: "amd64"}
	state, err := registry.InitializePlatform(ctx, current)
	if err != nil || state.Diagnostic {
		t.Fatalf("initialize platform: %v, diagnostic=%v", err, state.Diagnostic)
	}

	// Second initialization with different platform should return diagnostic
	different := settings.Platform{GOOS: "darwin", GOARCH: "arm64"}
	state, err = registry.InitializePlatform(ctx, different)
	if err != nil || !state.Diagnostic || state.Platform != current {
		t.Fatalf("platform mismatch: got diagnostic=%v, platform=%v; want diagnostic=true, platform=linux/amd64", state.Diagnostic, state.Platform)
	}
}

func TestUnsupportedPlatformReturnsImmediate(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	ctx := context.Background()

	unsupported := settings.Platform{GOOS: "windows", GOARCH: "arm64"}
	state, err := registry.InitializePlatform(ctx, unsupported)
	if err != nil || !state.Diagnostic || state.Reason != "unsupported platform" {
		t.Fatalf("unsupported platform: got diagnostic=%v, reason=%q; want diagnostic=true", state.Diagnostic, state.Reason)
	}

	// Should not persist unsupported platform
	if _, exists, _ := store.Get(ctx, settings.PlatformGOOSKey); exists {
		t.Fatal("unsupported platform was persisted")
	}
}

// testAbsolutePath builds a host-absolute path so overlap semantics can be
// exercised identically on Windows (drive or UNC volume) and Unix (root).
func testAbsolutePath(parts ...string) string {
	root := string(filepath.Separator)
	if volume := filepath.VolumeName(os.TempDir()); volume != "" {
		root = volume + string(filepath.Separator)
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func TestPathOverlapDetection(t *testing.T) {
	tools := testAbsolutePath("var", "lib", "tools")
	tests := []struct {
		name     string
		first    string
		second   string
		overlaps bool
	}{
		{"identical", tools, testAbsolutePath("var", "lib", "tools"), true},
		{"root contains descendant", testAbsolutePath(), testAbsolutePath("var", "lib"), true},
		{"descendant contained by root", testAbsolutePath("var", "lib"), testAbsolutePath(), true},
		{"first contains second", testAbsolutePath("var", "lib"), tools, true},
		{"second contains first", tools, testAbsolutePath("var", "lib"), true},
		{"siblings", tools, testAbsolutePath("var", "lib", "output"), false},
		{"disjoint trees", testAbsolutePath("opt", "tools"), testAbsolutePath("var", "output"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := settings.PathsOverlap(tt.first, tt.second); got != tt.overlaps {
				t.Errorf("PathsOverlap(%q, %q) = %v; want %v", tt.first, tt.second, got, tt.overlaps)
			}
		})
	}
}

func TestOutputDirectoryProbesFilesystemSemantics(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "output")

	store := newMemoryStore()
	registry := settings.New(store, nil)
	ctx := context.Background()

	if err := registry.SetOutputDirectory(ctx, outputPath, ""); err != nil {
		t.Fatalf("set output directory: %v", err)
	}

	semantics, err := registry.GetOutputFilesystemSemantics(ctx)
	if err != nil {
		t.Fatalf("get filesystem semantics: %v", err)
	}

	// Verify probed values are reasonable
	if semantics.UnicodeNormalization != "none" && semantics.UnicodeNormalization != "nfc" && semantics.UnicodeNormalization != "nfd" {
		t.Errorf("unexpected unicode normalization: %q", semantics.UnicodeNormalization)
	}
}

func TestProbeFilesystemCleansUp(t *testing.T) {
	tempDir := t.TempDir()
	if _, err := settings.ProbeFilesystemSemantics(tempDir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("successful probe left artifacts: %v", entries)
	}

	// A regular file standing in for a directory fails on every platform;
	// POSIX permission bits do not restrict writes on Windows.
	blocker := filepath.Join(tempDir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(blocker, "probe-target")
	_, err = settings.ProbeFilesystemSemantics(targetPath)
	if err == nil {
		t.Fatal("expected error for a non-directory parent, got nil")
	}

	entries, err = os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".melotrove-") {
			t.Errorf("probe file leaked: %s", entry.Name())
		}
	}
}

func TestToolsAndOutputDirectoriesCannotOverlap(t *testing.T) {
	tempDir := t.TempDir()
	toolsPath := filepath.Join(tempDir, "tools")
	outputPath := filepath.Join(tempDir, "tools", "nested")

	store := newMemoryStore()
	registry := settings.New(store, nil)
	ctx := context.Background()

	if err := os.MkdirAll(toolsPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := registry.SetToolsDirectory(ctx, toolsPath, ""); err != nil {
		t.Fatalf("set tools directory: %v", err)
	}

	if err := registry.SetOutputDirectory(ctx, outputPath, toolsPath); err == nil {
		t.Fatal("expected overlap error, got nil")
	}
}

func TestMusicBrainzConfigValidation(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	ctx := context.Background()

	// Public mode ignores base URL
	if err := registry.SetMusicBrainzConfig(ctx, "public", ""); err != nil {
		t.Fatalf("set public mode: %v", err)
	}

	config, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil || config.Mode != "public" || config.BaseURL != "" {
		t.Fatalf("public config: %+v, %v", config, err)
	}

	// Self-hosted requires valid HTTP(S) URL
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", "invalid-url"); err == nil {
		t.Fatal("accepted invalid self-hosted URL")
	}

	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", "https://mb.example.com"); err != nil {
		t.Fatalf("set self-hosted mode: %v", err)
	}

	config, err = registry.GetMusicBrainzConfig(ctx)
	if err != nil || config.Mode != "self-hosted" || config.BaseURL != "https://mb.example.com" {
		t.Fatalf("self-hosted config: %+v, %v", config, err)
	}
}

func TestMusicBrainzVerificationIsInvalidatedOnConfigChange(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	ctx := context.Background()

	if err := registry.SetMusicBrainzConfig(ctx, "public", ""); err != nil {
		t.Fatal(err)
	}

	if err := registry.MarkMusicBrainzVerified(ctx); err != nil {
		t.Fatal(err)
	}

	config, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil || config.VerifiedAt == nil {
		t.Fatalf("verification not recorded: %+v, %v", config, err)
	}

	// Changing config clears verification
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", "https://mb.example.com"); err != nil {
		t.Fatal(err)
	}

	config, err = registry.GetMusicBrainzConfig(ctx)
	if err != nil || config.VerifiedAt != nil {
		t.Fatalf("verification not cleared: %+v, %v", config, err)
	}
}

func TestPublicationFormatValidation(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	ctx := context.Background()

	if err := registry.SetPublicationFormat(ctx, "invalid"); err == nil {
		t.Fatal("accepted invalid format")
	}

	for _, format := range []string{"source", "mka"} {
		if err := registry.SetPublicationFormat(ctx, format); err != nil {
			t.Fatalf("set format %q: %v", format, err)
		}

		got, exists, err := registry.GetPublicationFormat(ctx)
		if err != nil || !exists || got != format {
			t.Fatalf("get format: %q, %v, %v; want %q", got, exists, err, format)
		}
	}
}

func TestUpdateRuntimeValidatesEveryFieldBeforeWriting(t *testing.T) {
	for _, test := range []struct {
		name   string
		update settings.RuntimeUpdate
	}{
		{"relative tools path", settings.RuntimeUpdate{ToolsDirectory: pointer("relative"), PublicationFormat: pointer("mka")}},
		{"whitespace tools path", settings.RuntimeUpdate{ToolsDirectory: pointer("   ")}},
		{"relative output path", settings.RuntimeUpdate{OutputDirectory: pointer("relative"), PublicationFormat: pointer("mka")}},
		{"invalid Unicode semantics", settings.RuntimeUpdate{ToolsDirectory: pointer("/tools"), OutputUnicodeNormalization: pointer("invalid")}},
		{"invalid format", settings.RuntimeUpdate{ToolsDirectory: pointer("/tools"), PublicationFormat: pointer("invalid")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &recordingStore{memoryStore: newMemoryStore()}
			store.data[settings.PublicationFormatKey] = "source"
			err := settings.New(store, nil).UpdateRuntime(context.Background(), test.update)
			if err == nil || store.setManyCalls != 0 || store.data[settings.PublicationFormatKey] != "source" {
				t.Fatalf("invalid update: err=%v, writes=%d, settings=%v", err, store.setManyCalls, store.data)
			}
		})
	}
}

func TestUpdateRuntimeWritesSuppliedFieldsTogetherAndLeavesNilFields(t *testing.T) {
	store := &recordingStore{memoryStore: newMemoryStore()}
	registry := settings.New(store, nil)
	tools := filepath.Join(t.TempDir(), "tools")
	output := filepath.Join(t.TempDir(), "output")
	canonicalTools, err := settings.NormalizePath(tools)
	if err != nil {
		t.Fatal(err)
	}
	canonicalOutput, err := settings.NormalizePath(output)
	if err != nil {
		t.Fatal(err)
	}
	caseSensitive := false
	unicodeNormalization := "none"
	format := "mka"
	uncleanTools := tools + "/../tools"
	uncleanOutput := output + "/../output"
	err = registry.UpdateRuntime(context.Background(), settings.RuntimeUpdate{
		ToolsDirectory: &uncleanTools, OutputDirectory: &uncleanOutput,
		ExpectedToolsDirectory: pointer(""),
		OutputCaseSensitive:    &caseSensitive, OutputUnicodeNormalization: &unicodeNormalization,
		PublicationFormat: &format,
	})
	if err != nil || store.setManyCalls != 1 {
		t.Fatalf("update runtime: err=%v, SetMany calls=%d", err, store.setManyCalls)
	}
	for key, want := range map[string]string{
		settings.ToolsDirectoryKey: canonicalTools, settings.OutputDirectoryKey: canonicalOutput,
		settings.OutputCaseSensitiveKey: "false", settings.OutputUnicodeNormalizationKey: "none",
		settings.PublicationFormatKey: "mka",
	} {
		if got := store.data[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	format = "source"
	if err := registry.UpdateRuntime(context.Background(), settings.RuntimeUpdate{PublicationFormat: &format}); err != nil {
		t.Fatal(err)
	}
	if store.setManyCalls != 2 || store.data[settings.OutputDirectoryKey] != canonicalOutput || store.data[settings.PublicationFormatKey] != "source" {
		t.Fatalf("nil fields were changed: calls=%d, settings=%v", store.setManyCalls, store.data)
	}
	if err := registry.UpdateRuntime(context.Background(), settings.RuntimeUpdate{}); err != nil || store.setManyCalls != 2 {
		t.Fatalf("empty update wrote settings: err=%v, calls=%d", err, store.setManyCalls)
	}
}

func pointer[T any](value T) *T { return &value }

func TestConfigurationHealthChecksAllRequirements(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	ctx := context.Background()

	platform := settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}
	health, err := registry.ComputeConfigurationHealth(ctx, platform)
	if err != nil {
		t.Fatal(err)
	}

	if health.Healthy {
		t.Fatal("empty configuration reported as healthy")
	}

	if len(health.Problems) == 0 {
		t.Fatal("no problems reported for empty configuration")
	}
}

func TestLRCLIBEnabledDefaultsToTrue(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	ctx := context.Background()

	enabled, err := registry.GetLRCLIBEnabled(ctx)
	if err != nil || !enabled {
		t.Fatalf("default LRCLIB enabled: %v, %v; want true, nil", enabled, err)
	}

	if err := registry.SetLRCLIBEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}

	enabled, err = registry.GetLRCLIBEnabled(ctx)
	if err != nil || enabled {
		t.Fatalf("disabled LRCLIB: %v, %v; want false, nil", enabled, err)
	}
}

func TestCompleteSetupPreservesOriginalTimestamp(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	registry := settings.NewRegistryWithClock(store, func() time.Time { return now })
	ctx := context.Background()

	if err := registry.CompleteSetup(ctx); err != nil {
		t.Fatal(err)
	}
	first, _, err := store.Get(ctx, settings.SetupCompletedAtKey)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := registry.CompleteSetup(ctx); err != nil {
		t.Fatal(err)
	}
	second, _, err := store.Get(ctx, settings.SetupCompletedAtKey)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("completion timestamp changed from %q to %q", first, second)
	}
}

func TestSetLogLevelAppliesToTheInjectedLevelVarWithoutRestart(t *testing.T) {
	ctx := context.Background()
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	store := newMemoryStore()
	registry := settings.New(store, level)
	// The logger is built before the level changes: it must follow the LevelVar
	// without being rebuilt or the process restarted.
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: level}))

	if err := registry.SetLogLevel(ctx, "error"); err != nil {
		t.Fatalf("set error level: %v", err)
	}
	if level.Level() != slog.LevelError {
		t.Fatalf("runtime level = %v, want error without a restart", level.Level())
	}
	if logger.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("informational logging stayed enabled after switching the runtime level to error")
	}
	if value, exists, err := store.Get(ctx, settings.LogLevelKey); err != nil || !exists || value != "error" {
		t.Fatalf("stored level = %q, exists=%t, err=%v", value, exists, err)
	}

	if err := registry.SetLogLevel(ctx, "debug"); err != nil {
		t.Fatalf("set debug level: %v", err)
	}
	if !logger.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("debug logging stayed disabled after switching the runtime level to debug")
	}

	if err := registry.SetLogLevel(ctx, "verbose"); err == nil {
		t.Fatal("invalid log level was accepted")
	}
	if level.Level() != slog.LevelDebug {
		t.Fatalf("rejected level changed the runtime level to %v", level.Level())
	}
}
