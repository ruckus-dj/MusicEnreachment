// Package settings owns typed runtime settings and instance platform policy.
package settings

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"runtime"
	"strings"
	"time"
)

const (
	PlatformGOOSKey               = "instance.goos"
	PlatformGOARCHKey             = "instance.goarch"
	ToolsDirectoryKey             = "tools_directory"
	OutputDirectoryKey            = "output_directory"
	OutputCaseSensitiveKey        = "output_case_sensitive"
	OutputUnicodeNormalizationKey = "output_unicode_normalization"
	PublicationFormatKey          = "publication_format"
	MusicBrainzModeKey            = "musicbrainz_mode"
	MusicBrainzBaseURLKey         = "musicbrainz_base_url"
	MusicBrainzVerifiedAtKey      = "musicbrainz_verified_at"
	LRCLIBEnabledKey              = "lrclib_enabled"
	LogLevelKey                   = "log_level"
	ActiveFFmpegInstallationKey   = "active_ffmpeg_installation_id"
	ActiveFPCalcInstallationKey   = "active_fpcalc_installation_id"
	SetupCompletedAtKey           = "setup_completed_at"
)

type Store interface {
	Get(context.Context, string) (string, bool, error)
	Set(context.Context, string, string) error
	SetMany(context.Context, map[string]string) error
	SetIfAbsent(context.Context, string, string) (string, error)
}

type SetupCompletionStore interface {
	CompleteSetupOnce(context.Context, string) error
}

type Platform struct{ GOOS, GOARCH string }

func (p Platform) Supported() bool {
	return (p.GOOS == "linux" || p.GOOS == "darwin") && (p.GOARCH == "amd64" || p.GOARCH == "arm64") || p.GOOS == "windows" && p.GOARCH == "amd64"
}

type PlatformState struct {
	Platform   Platform
	Diagnostic bool
	Reason     string
}

type MusicBrainzConfig struct {
	Mode       string // "public" or "self-hosted"
	BaseURL    string // empty for public, HTTP(S) URL for self-hosted
	VerifiedAt *time.Time
}

type ConfigurationHealth struct {
	Healthy  bool
	Problems []string
}

type RuntimeSettings struct {
	ToolsDirectory             string
	OutputDirectory            string
	PublicationFormat          string
	MusicBrainzMode            string
	MusicBrainzBaseURL         string
	MusicBrainzVerifiedAt      *time.Time
	LRCLIBEnabled              bool
	LogLevel                   string
	ActiveFFmpegInstallation   string
	ActiveFPCalcInstallation   string
	OutputCaseSensitive        *bool
	OutputUnicodeNormalization string
}

type Registry struct {
	store Store
	level *slog.LevelVar
	now   func() time.Time
}

func New(store Store, level *slog.LevelVar) *Registry {
	if level == nil {
		level = new(slog.LevelVar)
		level.Set(slog.LevelInfo)
	}
	return &Registry{store: store, level: level, now: time.Now}
}

func NewRegistryWithClock(store Store, clock func() time.Time) *Registry {
	return &Registry{store: store, level: new(slog.LevelVar), now: clock}
}

func (r *Registry) InitializePlatform(ctx context.Context, current Platform) (PlatformState, error) {
	if !current.Supported() {
		return PlatformState{Platform: current, Diagnostic: true, Reason: "unsupported platform"}, nil
	}
	goos, err := r.store.SetIfAbsent(ctx, PlatformGOOSKey, current.GOOS)
	if err != nil {
		return PlatformState{}, err
	}
	arch, err := r.store.SetIfAbsent(ctx, PlatformGOARCHKey, current.GOARCH)
	if err != nil {
		return PlatformState{}, err
	}
	persisted := Platform{GOOS: goos, GOARCH: arch}
	if persisted != current {
		return PlatformState{Platform: persisted, Diagnostic: true, Reason: "instance platform differs from current process"}, nil
	}
	return PlatformState{Platform: persisted}, nil
}

func CurrentPlatform() Platform { return Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH} }

func (r *Registry) SetLogLevel(ctx context.Context, value string) error {
	level, ok := parseLogLevel(value)
	if !ok {
		return fmt.Errorf("invalid log level %q", value)
	}
	if err := r.store.Set(ctx, LogLevelKey, value); err != nil {
		return err
	}
	r.level.Set(level)
	return nil
}

func (r *Registry) LoadLogLevel(ctx context.Context) error {
	value, exists, err := r.store.Get(ctx, LogLevelKey)
	if err != nil || !exists {
		return err
	}
	level, ok := parseLogLevel(value)
	if !ok {
		return fmt.Errorf("stored invalid log level %q", value)
	}
	r.level.Set(level)
	return nil
}

func (r *Registry) CompleteSetup(ctx context.Context) error {
	value := r.now().UTC().Format(time.RFC3339Nano)
	if store, ok := r.store.(SetupCompletionStore); ok {
		return store.CompleteSetupOnce(ctx, value)
	}
	_, err := r.store.SetIfAbsent(ctx, SetupCompletedAtKey, value)
	return err
}
func (r *Registry) SetupCompleted(ctx context.Context) (bool, error) {
	_, ok, err := r.store.Get(ctx, SetupCompletedAtKey)
	return ok, err
}

// GetToolsDirectory returns the normalized tools directory path.
func (r *Registry) GetToolsDirectory(ctx context.Context) (string, bool, error) {
	return r.store.Get(ctx, ToolsDirectoryKey)
}

// SetToolsDirectory validates and stores the normalized tools directory path.
func (r *Registry) SetToolsDirectory(ctx context.Context, path string, outputDirectory string) error {
	normalized, err := NormalizePath(path)
	if err != nil {
		return fmt.Errorf("tools directory: %w", err)
	}
	if outputDirectory != "" && PathsOverlap(normalized, outputDirectory) {
		return fmt.Errorf("tools directory overlaps with output directory")
	}
	if err := ProbeWritable(normalized); err != nil {
		return fmt.Errorf("tools directory: %w", err)
	}
	return r.store.Set(ctx, ToolsDirectoryKey, normalized)
}

// GetOutputDirectory returns the normalized output directory path.
func (r *Registry) GetOutputDirectory(ctx context.Context) (string, bool, error) {
	return r.store.Get(ctx, OutputDirectoryKey)
}

// SetOutputDirectory validates, probes filesystem semantics, and stores the output directory.
func (r *Registry) SetOutputDirectory(ctx context.Context, path string, toolsDirectory string) error {
	normalized, err := NormalizePath(path)
	if err != nil {
		return fmt.Errorf("output directory: %w", err)
	}
	if toolsDirectory != "" && PathsOverlap(normalized, toolsDirectory) {
		return fmt.Errorf("output directory overlaps with tools directory")
	}
	if err := ProbeWritableEmpty(normalized); err != nil {
		return fmt.Errorf("output directory: %w", err)
	}

	// Probe filesystem semantics
	semantics, err := ProbeFilesystemSemantics(normalized)
	if err != nil {
		return fmt.Errorf("probe filesystem semantics: %w", err)
	}

	// Store all three values atomically by validating first, then setting
	return r.store.SetMany(ctx, map[string]string{
		OutputDirectoryKey:            normalized,
		OutputCaseSensitiveKey:        fmt.Sprintf("%t", semantics.CaseSensitive),
		OutputUnicodeNormalizationKey: semantics.UnicodeNormalization,
	})
}

// GetOutputFilesystemSemantics returns the probed filesystem characteristics.
func (r *Registry) GetOutputFilesystemSemantics(ctx context.Context) (FilesystemSemantics, error) {
	caseSensitive, csExists, err := r.store.Get(ctx, OutputCaseSensitiveKey)
	if err != nil {
		return FilesystemSemantics{}, err
	}
	unicodeNorm, unExists, err := r.store.Get(ctx, OutputUnicodeNormalizationKey)
	if err != nil {
		return FilesystemSemantics{}, err
	}
	if !csExists || !unExists {
		return FilesystemSemantics{}, fmt.Errorf("filesystem semantics not probed")
	}
	return FilesystemSemantics{
		CaseSensitive:        caseSensitive == "true",
		UnicodeNormalization: unicodeNorm,
	}, nil
}

// GetPublicationFormat returns "source" or "mka".
func (r *Registry) GetPublicationFormat(ctx context.Context) (string, bool, error) {
	return r.store.Get(ctx, PublicationFormatKey)
}

// SetPublicationFormat validates and stores the publication format.
func (r *Registry) SetPublicationFormat(ctx context.Context, format string) error {
	if format != "source" && format != "mka" {
		return fmt.Errorf("publication format must be 'source' or 'mka'")
	}
	return r.store.Set(ctx, PublicationFormatKey, format)
}

// GetMusicBrainzConfig returns the current MusicBrainz configuration.
func (r *Registry) GetMusicBrainzConfig(ctx context.Context) (MusicBrainzConfig, error) {
	mode, _, err := r.store.Get(ctx, MusicBrainzModeKey)
	if err != nil {
		return MusicBrainzConfig{}, err
	}
	if mode == "" {
		mode = "public"
	}

	baseURL, _, err := r.store.Get(ctx, MusicBrainzBaseURLKey)
	if err != nil {
		return MusicBrainzConfig{}, err
	}

	var verifiedAt *time.Time
	if verifiedStr, exists, err := r.store.Get(ctx, MusicBrainzVerifiedAtKey); err != nil {
		return MusicBrainzConfig{}, err
	} else if exists && verifiedStr != "" {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, verifiedStr); parseErr == nil {
			verifiedAt = &parsed
		}
	}

	return MusicBrainzConfig{Mode: mode, BaseURL: baseURL, VerifiedAt: verifiedAt}, nil
}

// SetMusicBrainzConfig validates and stores MusicBrainz configuration.
// Changing the configuration invalidates the previous verification.
func (r *Registry) SetMusicBrainzConfig(ctx context.Context, mode, baseURL string) error {
	if mode != "public" && mode != "self-hosted" {
		return fmt.Errorf("musicbrainz mode must be 'public' or 'self-hosted'")
	}
	if mode == "self-hosted" {
		parsed, err := url.Parse(baseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("self-hosted mode requires valid HTTP(S) base URL")
		}
	}
	if mode == "public" {
		baseURL = "" // Public mode ignores base URL
	}

	return r.store.SetMany(ctx, map[string]string{
		MusicBrainzModeKey:       mode,
		MusicBrainzBaseURLKey:    baseURL,
		MusicBrainzVerifiedAtKey: "",
	})
}

// MarkMusicBrainzVerified records successful connectivity check.
func (r *Registry) MarkMusicBrainzVerified(ctx context.Context) error {
	return r.store.Set(ctx, MusicBrainzVerifiedAtKey, r.now().UTC().Format(time.RFC3339Nano))
}

// SetMusicBrainzVerified records successful connectivity check for the given configuration.
// This method should be called after a successful CheckMusicBrainz call.
func (r *Registry) SetMusicBrainzVerified(ctx context.Context, config MusicBrainzConfig) error {
	current, err := r.GetMusicBrainzConfig(ctx)
	if err != nil {
		return err
	}
	if current.Mode != config.Mode || current.BaseURL != config.BaseURL {
		return fmt.Errorf("configuration changed since check was initiated")
	}
	return r.store.Set(ctx, MusicBrainzVerifiedAtKey, r.now().UTC().Format(time.RFC3339Nano))
}

// GetLRCLIBEnabled returns whether LRCLIB integration is enabled.
func (r *Registry) GetLRCLIBEnabled(ctx context.Context) (bool, error) {
	value, exists, err := r.store.Get(ctx, LRCLIBEnabledKey)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil // Enabled by default
	}
	return value == "true", nil
}

func (r *Registry) ReadRuntimeSettings(ctx context.Context) (RuntimeSettings, error) {
	read := func(key string) (string, error) {
		value, _, err := r.store.Get(ctx, key)
		return value, err
	}
	tools, err := read(ToolsDirectoryKey)
	if err != nil {
		return RuntimeSettings{}, err
	}
	output, err := read(OutputDirectoryKey)
	if err != nil {
		return RuntimeSettings{}, err
	}
	format, err := read(PublicationFormatKey)
	if err != nil {
		return RuntimeSettings{}, err
	}
	musicBrainz, err := r.GetMusicBrainzConfig(ctx)
	if err != nil {
		return RuntimeSettings{}, err
	}
	lrclib, err := r.GetLRCLIBEnabled(ctx)
	if err != nil {
		return RuntimeSettings{}, err
	}
	logLevel, exists, err := r.store.Get(ctx, LogLevelKey)
	if err != nil {
		return RuntimeSettings{}, err
	}
	if !exists {
		logLevel = "info"
	}
	ffmpeg, err := read(ActiveFFmpegInstallationKey)
	if err != nil {
		return RuntimeSettings{}, err
	}
	fpcalc, err := read(ActiveFPCalcInstallationKey)
	if err != nil {
		return RuntimeSettings{}, err
	}
	caseSensitiveValue, caseExists, err := r.store.Get(ctx, OutputCaseSensitiveKey)
	if err != nil {
		return RuntimeSettings{}, err
	}
	var caseSensitive *bool
	if caseExists {
		value := caseSensitiveValue == "true"
		caseSensitive = &value
	}
	unicodeNormalization, err := read(OutputUnicodeNormalizationKey)
	if err != nil {
		return RuntimeSettings{}, err
	}
	return RuntimeSettings{
		ToolsDirectory:             tools,
		OutputDirectory:            output,
		PublicationFormat:          format,
		MusicBrainzMode:            musicBrainz.Mode,
		MusicBrainzBaseURL:         musicBrainz.BaseURL,
		MusicBrainzVerifiedAt:      musicBrainz.VerifiedAt,
		LRCLIBEnabled:              lrclib,
		LogLevel:                   logLevel,
		ActiveFFmpegInstallation:   ffmpeg,
		ActiveFPCalcInstallation:   fpcalc,
		OutputCaseSensitive:        caseSensitive,
		OutputUnicodeNormalization: unicodeNormalization,
	}, nil
}

// SetLRCLIBEnabled stores the LRCLIB integration flag.
func (r *Registry) SetLRCLIBEnabled(ctx context.Context, enabled bool) error {
	return r.store.Set(ctx, LRCLIBEnabledKey, fmt.Sprintf("%t", enabled))
}

// ComputeConfigurationHealth checks all required settings and returns health status.
func (r *Registry) ComputeConfigurationHealth(ctx context.Context, platform PlatformState) (ConfigurationHealth, error) {
	health := ConfigurationHealth{Healthy: true}

	if platform.Diagnostic {
		health.Healthy = false
		health.Problems = append(health.Problems, platform.Reason)
	}

	requiredKeys := []string{
		ToolsDirectoryKey,
		OutputDirectoryKey,
		PublicationFormatKey,
		ActiveFFmpegInstallationKey,
		ActiveFPCalcInstallationKey,
	}

	for _, key := range requiredKeys {
		value, exists, err := r.store.Get(ctx, key)
		if err != nil {
			return ConfigurationHealth{}, fmt.Errorf("read setting %q: %w", key, err)
		}
		if !exists || value == "" {
			health.Healthy = false
			health.Problems = append(health.Problems, fmt.Sprintf("missing or invalid %s", key))
		}
	}

	// MusicBrainz must have recent successful verification
	mbConfig, err := r.GetMusicBrainzConfig(ctx)
	if err != nil {
		return ConfigurationHealth{}, fmt.Errorf("read MusicBrainz configuration: %w", err)
	}
	if mbConfig.VerifiedAt == nil {
		health.Healthy = false
		health.Problems = append(health.Problems, "musicbrainz not verified")
	}

	return health, nil
}

func parseLogLevel(value string) (slog.Level, bool) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return 0, false
}
