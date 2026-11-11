// Package settings owns typed runtime settings and instance platform policy.
package settings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrSettingsConflict reports that a browser's expected saved value is stale.
var ErrSettingsConflict = errors.New("settings conflict")

const (
	PlatformGOOSKey                  = "instance.goos"
	PlatformGOARCHKey                = "instance.goarch"
	ToolsDirectoryKey                = "tools_directory"
	OutputDirectoryKey               = "output_directory"
	WorkDirectoryKey                 = "work_directory"
	OutputCaseSensitiveKey           = "output_case_sensitive"
	OutputUnicodeNormalizationKey    = "output_unicode_normalization"
	PublicationFormatKey             = "publication_format"
	PublicationPathTemplateKey       = "publication_path_template"
	PublicationRemoveSourcesKey      = "publication_remove_sources"
	MusicBrainzModeKey               = "musicbrainz_mode"
	MusicBrainzBaseURLKey            = "musicbrainz_base_url"
	MusicBrainzConfigIdentityKey     = "musicbrainz_config_identity"
	MusicBrainzVerifiedAtKey         = "musicbrainz_verified_at"
	LRCLIBEnabledKey                 = "lrclib_enabled"
	SHA256EnabledKey                 = "sha256_enabled"
	LogLevelKey                      = "log_level"
	ActiveFFmpegInstallationKey      = "active_ffmpeg_installation_id"
	ActiveFPCalcInstallationKey      = "active_fpcalc_installation_id"
	SetupCompletedAtKey              = "setup_completed_at"
	SourceFileConcurrencyKey         = "source_file_concurrency"
	AcoustIDApplicationKey           = "acoustid_application_key"
	GitHubTokenKey                   = "github_token"
	MusicBrainzSelfHostedThrottleKey = "musicbrainz_self_hosted_throttle"
	MusicBrainzSelfHostedDelayKey    = "musicbrainz_self_hosted_delay_seconds"
)

type Store interface {
	Get(context.Context, string) (string, bool, error)
	Set(context.Context, string, string) error
	SetMany(context.Context, map[string]string) error
	SetIfAbsent(context.Context, string, string) (string, error)
	InitializePlatform(context.Context, string, string) (string, string, bool, error)
}

type SetupCompletionStore interface {
	CompleteSetupOnce(context.Context, string) error
}

type verifiedSetupCompletionStore interface {
	CompleteSetupIfCurrent(context.Context, map[string]string, string) error
}

type musicBrainzVerificationStore interface {
	SetMusicBrainzVerifiedIfCurrent(context.Context, string, string, string, string) (bool, error)
}

type musicBrainzConfigSnapshotStore interface {
	GetMusicBrainzConfigValues(context.Context) (map[string]string, error)
}

type conditionalMusicBrainzSettingsStore interface {
	UpdateMusicBrainzSettingsIfCurrent(context.Context, MusicBrainzSettingsExpected, *string, *string, *bool, *float64) error
}

type publicationPlanningSnapshotStore interface {
	GetPublicationPlanningValues(context.Context) (map[string]string, error)
}

type runtimeUpdateStore interface {
	UpdateRuntime(context.Context, string, string, map[string]string) error
}

type conditionalRuntimeUpdateStore interface {
	UpdateRuntimeIfCurrent(context.Context, string, string, RuntimeSettingsExpected, map[string]string) error
}

type acoustIDCredentialStore interface {
	SetOrDeleteAcoustIDCredential(context.Context, *string) error
	ReadAcoustIDCredential(context.Context) (string, bool, string, error)
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
	Identity   string // changes when the effective provider target changes
	VerifiedAt *time.Time
}

// MusicBrainzSettingsExpected projects the saved provider target and any
// provider preferences being edited by a browser draft.
type MusicBrainzSettingsExpected struct {
	Mode                   *string
	BaseURL                *string
	SelfHostedThrottle     *bool
	SelfHostedDelaySeconds *float64
}

type ConfigurationHealth struct {
	Healthy  bool
	Problems []string
}

type RuntimeSettings struct {
	SourceFileConcurrency             int
	ToolsDirectory                    string
	WorkDirectory                     string
	OutputDirectory                   string
	PublicationFormat                 string
	PublicationPathTemplate           string
	PublicationRemoveSources          bool
	MusicBrainzMode                   string
	MusicBrainzBaseURL                string
	MusicBrainzVerifiedAt             *time.Time
	LRCLIBEnabled                     bool
	LogLevel                          string
	ActiveFFmpegInstallation          string
	ActiveFPCalcInstallation          string
	OutputCaseSensitive               *bool
	OutputUnicodeNormalization        string
	HasAcoustIDApplicationKey         bool
	HasGitHubToken                    bool
	MusicBrainzSelfHostedThrottle     bool
	MusicBrainzSelfHostedDelaySeconds float64
}

func (r *Registry) ReadGitHubToken(ctx context.Context) (string, bool, error) {
	return r.store.Get(ctx, GitHubTokenKey)
}

func (r *Registry) HasGitHubToken(ctx context.Context) (bool, error) {
	_, present, err := r.ReadGitHubToken(ctx)
	return present, err
}

func (r *Registry) SetGitHubToken(ctx context.Context, token string) error {
	return r.store.Set(ctx, GitHubTokenKey, token)
}

func (r *Registry) DeleteGitHubToken(ctx context.Context) error {
	store, ok := r.store.(interface {
		Delete(context.Context, string) error
	})
	if !ok {
		return fmt.Errorf("delete GitHub token is unavailable")
	}
	return store.Delete(ctx, GitHubTokenKey)
}

// RuntimeUpdate selects the validated runtime values to change in one write.
// A nil field leaves its setting unchanged.
type RuntimeUpdate struct {
	ToolsDirectory             *string
	WorkDirectory              *string
	ExpectedToolsDirectory     *string
	OutputDirectory            *string
	ExpectedOutputDirectory    *string
	OutputCaseSensitive        *bool
	OutputUnicodeNormalization *string
	PublicationFormat          *string
	PublicationPathTemplate    *string
	PublicationRemoveSources   *bool
	SourceFileConcurrency      *int
	Expected                   *RuntimeSettingsExpected
}

// RuntimeSettingsExpected is the typed projection of browser-saved values
// against which supplied runtime changes are conditionally applied.
type RuntimeSettingsExpected struct {
	ToolsDirectory           *string
	WorkDirectory            *string
	OutputDirectory          *string
	PublicationFormat        *string
	PublicationPathTemplate  *string
	PublicationRemoveSources *bool
	SourceFileConcurrency    *int
}

// DefaultRuntimeSettingsExpected returns the effective values used when a
// setting row has not yet been persisted.
func DefaultRuntimeSettingsExpected() RuntimeSettingsExpected {
	format := ""
	work, output, tools := "", "", ""
	template := defaultPublicationPathTemplate
	removeSources := defaultPublicationRemoveSources
	concurrency := defaultFileConcurrency
	return RuntimeSettingsExpected{
		ToolsDirectory: &tools, WorkDirectory: &work, OutputDirectory: &output,
		PublicationFormat: &format, PublicationPathTemplate: &template,
		PublicationRemoveSources: &removeSources, SourceFileConcurrency: &concurrency,
	}
}

type Registry struct {
	store               Store
	level               *slog.LevelVar
	now                 func() time.Time
	runtimeUpdateMu     sync.Mutex
	concurrencyObserver func()
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
	goos, arch, complete, err := r.store.InitializePlatform(ctx, current.GOOS, current.GOARCH)
	if err != nil {
		return PlatformState{}, err
	}
	persisted := Platform{GOOS: goos, GOARCH: arch}
	if !complete {
		return PlatformState{Platform: persisted, Diagnostic: true, Reason: "instance platform is incomplete"}, nil
	}
	if persisted != current {
		return PlatformState{Platform: persisted, Diagnostic: true, Reason: "instance platform differs from current process"}, nil
	}
	return PlatformState{Platform: persisted}, nil
}

func CurrentPlatform() Platform { return Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH} }

func (r *Registry) SetLogLevel(ctx context.Context, value string) error {
	if _, err := serializeSetting(logSetting, value); err != nil {
		return err
	}
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

// HasAcoustIDApplicationKey exposes key presence only; the secret itself is
// intentionally never returned from the settings registry.
func (r *Registry) HasAcoustIDApplicationKey(ctx context.Context) (bool, error) {
	_, exists, err := r.store.Get(ctx, AcoustIDApplicationKey)
	return exists, err
}

// SetAcoustIDApplicationKey saves the exact supplied application key.
func (r *Registry) SetAcoustIDApplicationKey(ctx context.Context, value string) error {
	if value == "" || len(value) > 4096 {
		return fmt.Errorf("AcoustID application key is invalid")
	}
	store, ok := r.store.(acoustIDCredentialStore)
	if !ok {
		return fmt.Errorf("atomic AcoustID credential storage is unavailable")
	}
	return store.SetOrDeleteAcoustIDCredential(ctx, &value)
}

// DeleteAcoustIDApplicationKey removes the application key entirely. Its
// absence is the sole signal that AcoustID lookup is unavailable.
func (r *Registry) DeleteAcoustIDApplicationKey(ctx context.Context) error {
	store, ok := r.store.(acoustIDCredentialStore)
	if !ok {
		return fmt.Errorf("atomic AcoustID credential storage is unavailable")
	}
	return store.SetOrDeleteAcoustIDCredential(ctx, nil)
}

// ReadAcoustIDCredential returns the secret together with its coherently read
// opaque configuration revision for internal provider execution only.
func (r *Registry) ReadAcoustIDCredential(ctx context.Context) (key string, present bool, revision string, err error) {
	store, ok := r.store.(acoustIDCredentialStore)
	if !ok {
		return "", false, "", fmt.Errorf("atomic AcoustID credential storage is unavailable")
	}
	return store.ReadAcoustIDCredential(ctx)
}

func (r *Registry) LoadLogLevel(ctx context.Context) error {
	value, exists, err := readSetting(ctx, r.store, logSetting)
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
	value, err := serializeSetting(setupCompletedSetting, r.now().UTC())
	if err != nil {
		return err
	}
	if store, ok := r.store.(SetupCompletionStore); ok {
		return store.CompleteSetupOnce(ctx, value)
	}
	_, err = r.store.SetIfAbsent(ctx, SetupCompletedAtKey, value)
	return err
}

// CompleteSetupIfCurrent binds the final successful connectivity check to the
// validated runtime settings. The store must compare and complete atomically.
func (r *Registry) CompleteSetupIfCurrent(ctx context.Context, platform Platform, runtime RuntimeSettings, checked MusicBrainzConfig) error {
	store, ok := r.store.(verifiedSetupCompletionStore)
	if !ok {
		return fmt.Errorf("atomic setup completion is unavailable")
	}
	value, err := serializeSetting(setupCompletedSetting, r.now().UTC())
	if err != nil {
		return err
	}
	if runtime.OutputCaseSensitive == nil {
		return fmt.Errorf("output filesystem semantics are missing")
	}
	return store.CompleteSetupIfCurrent(ctx, map[string]string{
		PlatformGOOSKey:               platform.GOOS,
		PlatformGOARCHKey:             platform.GOARCH,
		ToolsDirectoryKey:             runtime.ToolsDirectory,
		OutputDirectoryKey:            runtime.OutputDirectory,
		OutputCaseSensitiveKey:        strconv.FormatBool(*runtime.OutputCaseSensitive),
		OutputUnicodeNormalizationKey: runtime.OutputUnicodeNormalization,
		PublicationFormatKey:          runtime.PublicationFormat,
		ActiveFFmpegInstallationKey:   runtime.ActiveFFmpegInstallation,
		ActiveFPCalcInstallationKey:   runtime.ActiveFPCalcInstallation,
		MusicBrainzModeKey:            checked.Mode,
		MusicBrainzBaseURLKey:         checked.BaseURL,
		MusicBrainzConfigIdentityKey:  checked.Identity,
	}, value)
}

func (r *Registry) SetupCompleted(ctx context.Context) (bool, error) {
	_, ok, err := r.store.Get(ctx, SetupCompletedAtKey)
	return ok, err
}

// GetToolsDirectory returns the normalized tools directory path.
func (r *Registry) GetToolsDirectory(ctx context.Context) (string, bool, error) {
	return readSetting(ctx, r.store, toolsRootSetting)
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
	value, err := serializeSetting(toolsRootSetting, normalized)
	if err != nil {
		return err
	}
	return r.store.Set(ctx, ToolsDirectoryKey, value)
}

// GetOutputDirectory returns the normalized output directory path.
func (r *Registry) GetOutputDirectory(ctx context.Context) (string, bool, error) {
	return readSetting(ctx, r.store, outputRootSetting)
}

func (r *Registry) GetWorkDirectory(ctx context.Context) (string, bool, error) {
	return readSetting(ctx, r.store, workRootSetting)
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
	semantics, err := ProbeOutputDirectory(normalized, true)
	if err != nil {
		return fmt.Errorf("output directory: %w", err)
	}
	normalized, err = serializeSetting(outputRootSetting, normalized)
	if err != nil {
		return err
	}
	caseSensitive, err := serializeSetting(outputCaseSetting, semantics.CaseSensitive)
	if err != nil {
		return err
	}
	unicodeNormalization, err := serializeSetting(outputUnicodeSetting, semantics.UnicodeNormalization)
	if err != nil {
		return err
	}

	// Store all three values atomically by validating first, then setting
	return r.store.SetMany(ctx, map[string]string{
		OutputDirectoryKey:            normalized,
		OutputCaseSensitiveKey:        caseSensitive,
		OutputUnicodeNormalizationKey: unicodeNormalization,
	})
}

func (r *Registry) UpdateRuntime(ctx context.Context, update RuntimeUpdate) error {
	r.runtimeUpdateMu.Lock()
	defer r.runtimeUpdateMu.Unlock()
	values, err := runtimeUpdateValues(update)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	if update.Expected != nil {
		expected, err := normalizeRuntimeExpected(*update.Expected)
		if err != nil {
			return err
		}
		if err := validateRuntimeExpected(expected, values); err != nil {
			return err
		}
		update.Expected = &expected
	}
	if store, ok := r.store.(runtimeUpdateStore); ok {
		if (update.ToolsDirectory != nil || update.WorkDirectory != nil || update.OutputDirectory != nil || update.OutputCaseSensitive != nil || update.OutputUnicodeNormalization != nil) &&
			(update.ExpectedToolsDirectory == nil || update.ExpectedOutputDirectory == nil) {
			return fmt.Errorf("runtime path updates require expected current roots")
		}
		expectedTools, expectedOutput := "", ""
		if update.ExpectedToolsDirectory != nil {
			expectedTools = *update.ExpectedToolsDirectory
		}
		if update.ExpectedOutputDirectory != nil {
			expectedOutput = *update.ExpectedOutputDirectory
		}
		var err error
		if update.Expected != nil {
			conditionalStore, ok := r.store.(conditionalRuntimeUpdateStore)
			if !ok {
				return fmt.Errorf("conditional runtime settings storage is unavailable")
			}
			err = conditionalStore.UpdateRuntimeIfCurrent(ctx, expectedTools, expectedOutput, *update.Expected, values)
		} else {
			err = store.UpdateRuntime(ctx, expectedTools, expectedOutput, values)
		}
		if err == nil {
			r.notifyConcurrencyUpdate(values)
		}
		return err
	}
	if update.ToolsDirectory != nil || update.WorkDirectory != nil || update.OutputDirectory != nil || update.OutputCaseSensitive != nil || update.OutputUnicodeNormalization != nil {
		return fmt.Errorf("runtime path updates require transactional settings storage")
	}
	if update.Expected != nil {
		return fmt.Errorf("conditional runtime settings storage is unavailable")
	}
	err = r.store.SetMany(ctx, values)
	if err == nil {
		r.notifyConcurrencyUpdate(values)
	}
	return err
}

func validateRuntimeExpected(expected RuntimeSettingsExpected, values map[string]string) error {
	checks := []struct {
		name string
		set  bool
	}{
		{ToolsDirectoryKey, expected.ToolsDirectory != nil},
		{WorkDirectoryKey, expected.WorkDirectory != nil},
		{OutputDirectoryKey, expected.OutputDirectory != nil},
		{PublicationFormatKey, expected.PublicationFormat != nil},
		{PublicationPathTemplateKey, expected.PublicationPathTemplate != nil},
		{PublicationRemoveSourcesKey, expected.PublicationRemoveSources != nil},
		{SourceFileConcurrencyKey, expected.SourceFileConcurrency != nil},
	}
	for _, check := range checks {
		if _, changed := values[check.name]; changed && !check.set {
			return fmt.Errorf("expected saved value is required for %s", check.name)
		}
	}
	return nil
}

func normalizeRuntimeExpected(expected RuntimeSettingsExpected) (RuntimeSettingsExpected, error) {
	if expected.ToolsDirectory != nil {
		value, err := normalizeExpectedPath(*expected.ToolsDirectory, toolsRootSetting, "tools directory")
		if err != nil {
			return RuntimeSettingsExpected{}, err
		}
		expected.ToolsDirectory = &value
	}
	if expected.WorkDirectory != nil {
		value, err := normalizeExpectedPath(*expected.WorkDirectory, workRootSetting, "work directory")
		if err != nil {
			return RuntimeSettingsExpected{}, err
		}
		expected.WorkDirectory = &value
	}
	if expected.OutputDirectory != nil {
		value, err := normalizeExpectedPath(*expected.OutputDirectory, outputRootSetting, "output directory")
		if err != nil {
			return RuntimeSettingsExpected{}, err
		}
		expected.OutputDirectory = &value
	}
	if expected.PublicationFormat != nil {
		if *expected.PublicationFormat != "" {
			value, err := serializeSetting(publicationSetting, *expected.PublicationFormat)
			if err != nil {
				return RuntimeSettingsExpected{}, err
			}
			expected.PublicationFormat = &value
		}
	}
	if expected.PublicationPathTemplate != nil {
		value, err := serializeSetting(publicationPathTemplateSetting, *expected.PublicationPathTemplate)
		if err != nil {
			return RuntimeSettingsExpected{}, err
		}
		expected.PublicationPathTemplate = &value
	}
	if expected.PublicationRemoveSources != nil {
		_, err := serializeSetting(publicationRemoveSourcesSetting, *expected.PublicationRemoveSources)
		if err != nil {
			return RuntimeSettingsExpected{}, err
		}
	}
	if expected.SourceFileConcurrency != nil {
		_, err := serializeSetting(sourceFileConcurrencySetting, *expected.SourceFileConcurrency)
		if err != nil {
			return RuntimeSettingsExpected{}, err
		}
	}
	return expected, nil
}

// NormalizeRuntimeSettingsExpected validates and canonicalizes the values in a
// browser's expected runtime projection before persistence compares them.
func NormalizeRuntimeSettingsExpected(expected RuntimeSettingsExpected) (RuntimeSettingsExpected, error) {
	return normalizeRuntimeExpected(expected)
}

func normalizeExpectedPath(value string, definition settingDefinition[string], label string) (string, error) {
	if value == "" {
		return "", nil
	}
	normalized, err := NormalizePath(value)
	if err != nil {
		return "", fmt.Errorf("expected %s: %w", label, err)
	}
	return serializeSetting(definition, normalized)
}

// WithConcurrencyObserver registers a non-blocking wake-up called after source
// file concurrency is durably updated and before runtime updates are unlocked.
// Observers should re-read the setting rather than treating the notification as
// carrying a value.
func (r *Registry) WithConcurrencyObserver(observer func()) *Registry {
	r.runtimeUpdateMu.Lock()
	defer r.runtimeUpdateMu.Unlock()
	r.concurrencyObserver = observer
	return r
}

func (r *Registry) notifyConcurrencyUpdate(values map[string]string) {
	if _, changed := values[SourceFileConcurrencyKey]; changed && r.concurrencyObserver != nil {
		r.concurrencyObserver()
	}
}

// CoordinateRuntimeReset serializes a durable output reset with all runtime updates.
// The callback receives roots read once under the lock and already serialized values
// for the atomic reset transaction.
func (r *Registry) CoordinateRuntimeReset(ctx context.Context, expectedTools, expectedOutput string, update RuntimeUpdate, run func(string, string, map[string]string) error) error {
	r.runtimeUpdateMu.Lock()
	defer r.runtimeUpdateMu.Unlock()
	values, err := runtimeUpdateValues(update)
	if err != nil {
		return err
	}
	if update.Expected != nil {
		expected, err := normalizeRuntimeExpected(*update.Expected)
		if err != nil {
			return err
		}
		if err := validateRuntimeExpected(expected, values); err != nil {
			return err
		}
		update.Expected = &expected
	}
	if run == nil {
		return fmt.Errorf("runtime reset callback is required")
	}
	err = run(expectedTools, expectedOutput, values)
	if err == nil {
		r.notifyConcurrencyUpdate(values)
	}
	return err
}

func runtimeUpdateValues(update RuntimeUpdate) (map[string]string, error) {
	values := make(map[string]string)
	if update.ToolsDirectory != nil {
		normalized, err := NormalizePath(*update.ToolsDirectory)
		if err != nil {
			return nil, fmt.Errorf("tools directory: %w", err)
		}
		value, err := serializeSetting(toolsRootSetting, normalized)
		if err != nil {
			return nil, fmt.Errorf("tools directory: %w", err)
		}
		values[ToolsDirectoryKey] = value
	}
	if update.WorkDirectory != nil {
		normalized, err := NormalizePath(*update.WorkDirectory)
		if err != nil {
			return nil, fmt.Errorf("work directory: %w", err)
		}
		value, err := serializeSetting(workRootSetting, normalized)
		if err != nil {
			return nil, fmt.Errorf("work directory: %w", err)
		}
		values[WorkDirectoryKey] = value
	}
	if update.OutputDirectory != nil {
		normalized, err := NormalizePath(*update.OutputDirectory)
		if err != nil {
			return nil, fmt.Errorf("output directory: %w", err)
		}
		value, err := serializeSetting(outputRootSetting, normalized)
		if err != nil {
			return nil, fmt.Errorf("output directory: %w", err)
		}
		values[OutputDirectoryKey] = value
	}
	if update.OutputCaseSensitive != nil {
		value, err := serializeSetting(outputCaseSetting, *update.OutputCaseSensitive)
		if err != nil {
			return nil, fmt.Errorf("output case sensitivity: %w", err)
		}
		values[OutputCaseSensitiveKey] = value
	}
	if update.OutputUnicodeNormalization != nil {
		value, err := serializeSetting(outputUnicodeSetting, *update.OutputUnicodeNormalization)
		if err != nil {
			return nil, fmt.Errorf("output Unicode normalization: %w", err)
		}
		values[OutputUnicodeNormalizationKey] = value
	}
	if update.PublicationFormat != nil {
		value, err := serializeSetting(publicationSetting, *update.PublicationFormat)
		if err != nil {
			return nil, fmt.Errorf("publication format: %w", err)
		}
		values[PublicationFormatKey] = value
	}
	if update.PublicationPathTemplate != nil {
		value, err := serializeSetting(publicationPathTemplateSetting, *update.PublicationPathTemplate)
		if err != nil {
			return nil, fmt.Errorf("publication path template: %w", err)
		}
		values[PublicationPathTemplateKey] = value
	}
	if update.PublicationRemoveSources != nil {
		value, err := serializeSetting(publicationRemoveSourcesSetting, *update.PublicationRemoveSources)
		if err != nil {
			return nil, fmt.Errorf("publication source removal: %w", err)
		}
		values[PublicationRemoveSourcesKey] = value
	}
	if update.SourceFileConcurrency != nil {
		value, err := serializeSetting(sourceFileConcurrencySetting, *update.SourceFileConcurrency)
		if err != nil {
			return nil, fmt.Errorf("source file concurrency: %w", err)
		}
		values[SourceFileConcurrencyKey] = value
	}
	return values, nil
}

// GetPublicationFormat returns "source" or "mka".
func (r *Registry) GetPublicationFormat(ctx context.Context) (string, bool, error) {
	return readSetting(ctx, r.store, publicationSetting)
}

// GetMusicBrainzConfig returns the current MusicBrainz configuration.
func (r *Registry) GetMusicBrainzConfig(ctx context.Context) (MusicBrainzConfig, error) {
	if store, ok := r.store.(musicBrainzConfigSnapshotStore); ok {
		values, err := store.GetMusicBrainzConfigValues(ctx)
		if err != nil {
			return MusicBrainzConfig{}, err
		}
		mode, err := parseSnapshotSetting(values, musicBrainzModeSetting)
		if err != nil {
			return MusicBrainzConfig{}, err
		}
		baseURL, err := parseSnapshotSetting(values, musicBrainzURLSetting)
		if err != nil {
			return MusicBrainzConfig{}, err
		}
		identity, err := parseSnapshotSetting(values, musicBrainzIdentitySetting)
		if err != nil {
			return MusicBrainzConfig{}, err
		}
		var verifiedAt *time.Time
		if value := values[MusicBrainzVerifiedAtKey]; value != "" {
			parsed, parseErr := musicBrainzVerifiedSetting.parse(value)
			if parseErr != nil {
				return MusicBrainzConfig{}, fmt.Errorf("invalid stored MusicBrainz verification: %w", parseErr)
			}
			verifiedAt = &parsed
		}
		return MusicBrainzConfig{Mode: mode, BaseURL: baseURL, Identity: identity, VerifiedAt: verifiedAt}, nil
	}
	mode, _, err := readSetting(ctx, r.store, musicBrainzModeSetting)
	if err != nil {
		return MusicBrainzConfig{}, err
	}
	baseURL, _, err := readSetting(ctx, r.store, musicBrainzURLSetting)
	if err != nil {
		return MusicBrainzConfig{}, err
	}
	identity, _, err := readSetting(ctx, r.store, musicBrainzIdentitySetting)
	if err != nil {
		return MusicBrainzConfig{}, err
	}

	var verifiedAt *time.Time
	if verifiedStr, exists, err := r.store.Get(ctx, MusicBrainzVerifiedAtKey); err != nil {
		return MusicBrainzConfig{}, err
	} else if exists && verifiedStr != "" {
		parsed, parseErr := musicBrainzVerifiedSetting.parse(verifiedStr)
		if parseErr != nil {
			return MusicBrainzConfig{}, fmt.Errorf("invalid stored MusicBrainz verification: %w", parseErr)
		}
		verifiedAt = &parsed
	}

	return MusicBrainzConfig{Mode: mode, BaseURL: baseURL, Identity: identity, VerifiedAt: verifiedAt}, nil
}

func parseSnapshotSetting[T any](values map[string]string, definition settingDefinition[T]) (T, error) {
	raw, exists := values[definition.name]
	if !exists {
		if definition.defaultVal != nil {
			return *definition.defaultVal, nil
		}
		var zero T
		return zero, nil
	}
	value, err := definition.parse(raw)
	if err == nil && definition.validate != nil {
		err = definition.validate(value)
	}
	if err != nil {
		var zero T
		return zero, fmt.Errorf("invalid stored %s: %w", definition.name, err)
	}
	return value, nil
}

// SetMusicBrainzConfig validates and stores MusicBrainz configuration.
// Changing the effective provider target invalidates the previous verification.
func (r *Registry) SetMusicBrainzConfig(ctx context.Context, mode, baseURL string) error {
	mode, err := serializeSetting(musicBrainzModeSetting, mode)
	if err != nil {
		return err
	}
	if mode == "self-hosted" {
		if baseURL == "" {
			return fmt.Errorf("self-hosted mode requires valid HTTP(S) base URL")
		}
	}
	if mode == "public" {
		baseURL = "" // Public mode ignores base URL
	}
	baseURL, err = serializeSetting(musicBrainzURLSetting, baseURL)
	if err != nil {
		return err
	}

	return r.store.SetMany(ctx, map[string]string{
		MusicBrainzModeKey:           mode,
		MusicBrainzBaseURLKey:        baseURL,
		MusicBrainzConfigIdentityKey: uuid.NewString(),
		MusicBrainzVerifiedAtKey:     "",
	})
}

// UpdateMusicBrainzSettings atomically applies the provider target group and
// independently supplied throttle and delay fields against their saved values.
func (r *Registry) UpdateMusicBrainzSettings(ctx context.Context, expected MusicBrainzSettingsExpected, mode, baseURL *string, throttle *bool, delaySeconds *float64) error {
	if mode == nil && throttle == nil && delaySeconds == nil {
		return nil
	}
	if (mode == nil) != (baseURL == nil) || (expected.Mode == nil) != (expected.BaseURL == nil) {
		return fmt.Errorf("MusicBrainz mode and base URL must be supplied together")
	}
	if (mode == nil) != (expected.Mode == nil) {
		return fmt.Errorf("expected MusicBrainz target is required when changing the target")
	}
	if mode != nil {
		serializedMode, err := serializeSetting(musicBrainzModeSetting, *mode)
		if err != nil {
			return err
		}
		if serializedMode == "public" {
			*baseURL = ""
		} else if *baseURL == "" {
			return fmt.Errorf("self-hosted mode requires valid HTTP(S) base URL")
		}
		serializedURL, err := serializeSetting(musicBrainzURLSetting, *baseURL)
		if err != nil {
			return err
		}
		*mode, *baseURL = serializedMode, serializedURL
		expectedMode, err := serializeSetting(musicBrainzModeSetting, *expected.Mode)
		if err != nil {
			return fmt.Errorf("expected MusicBrainz mode: %w", err)
		}
		if expectedMode == "self-hosted" && *expected.BaseURL == "" {
			return fmt.Errorf("expected self-hosted mode requires valid HTTP(S) base URL")
		}
		expectedURL, err := serializeSetting(musicBrainzURLSetting, *expected.BaseURL)
		if err != nil {
			return fmt.Errorf("expected MusicBrainz base URL: %w", err)
		}
		expected.Mode, expected.BaseURL = &expectedMode, &expectedURL
	}
	if throttle != nil && expected.SelfHostedThrottle == nil {
		return fmt.Errorf("expected saved value is required for MusicBrainz self-hosted throttle")
	}
	if delaySeconds != nil && expected.SelfHostedDelaySeconds == nil {
		return fmt.Errorf("expected saved value is required for MusicBrainz self-hosted delay")
	}
	if expected.SelfHostedThrottle != nil {
		if _, err := serializeSetting(musicBrainzSelfHostedThrottleSetting, *expected.SelfHostedThrottle); err != nil {
			return fmt.Errorf("expected MusicBrainz self-hosted throttle: %w", err)
		}
	}
	if expected.SelfHostedDelaySeconds != nil {
		if _, err := serializeSetting(musicBrainzSelfHostedDelaySetting, *expected.SelfHostedDelaySeconds); err != nil {
			return fmt.Errorf("expected MusicBrainz self-hosted delay: %w", err)
		}
	}
	if throttle != nil {
		if _, err := serializeSetting(musicBrainzSelfHostedThrottleSetting, *throttle); err != nil {
			return err
		}
	}
	if delaySeconds != nil {
		if _, err := serializeSetting(musicBrainzSelfHostedDelaySetting, *delaySeconds); err != nil {
			return err
		}
	}
	store, ok := r.store.(conditionalMusicBrainzSettingsStore)
	if !ok {
		return fmt.Errorf("conditional MusicBrainz settings storage is unavailable")
	}
	return store.UpdateMusicBrainzSettingsIfCurrent(ctx, expected, mode, baseURL, throttle, delaySeconds)
}

// MarkMusicBrainzVerified records successful connectivity check.
func (r *Registry) MarkMusicBrainzVerified(ctx context.Context) error {
	return r.store.Set(ctx, MusicBrainzVerifiedAtKey, r.now().UTC().Format(time.RFC3339Nano))
}

// SetMusicBrainzVerified records successful connectivity check for the given configuration.
// This method should be called after a successful CheckMusicBrainz call.
func (r *Registry) SetMusicBrainzVerified(ctx context.Context, config MusicBrainzConfig) error {
	verifiedAt, err := serializeSetting(musicBrainzVerifiedSetting, r.now().UTC())
	if err != nil {
		return err
	}
	if store, ok := r.store.(musicBrainzVerificationStore); ok {
		matched, err := store.SetMusicBrainzVerifiedIfCurrent(ctx, config.Mode, config.BaseURL, config.Identity, verifiedAt)
		if err != nil {
			return err
		}
		if !matched {
			return fmt.Errorf("configuration changed since check was initiated")
		}
		return nil
	}
	// In-memory stores used by isolated service tests have no DB transaction.
	current, err := r.GetMusicBrainzConfig(ctx)
	if err != nil {
		return err
	}
	if current.Identity != config.Identity || (config.Identity == "" && (current.Mode != config.Mode || current.BaseURL != config.BaseURL)) {
		return fmt.Errorf("configuration changed since check was initiated")
	}
	return r.store.Set(ctx, MusicBrainzVerifiedAtKey, verifiedAt)
}

// GetLRCLIBEnabled returns whether LRCLIB integration is enabled.
func (r *Registry) GetLRCLIBEnabled(ctx context.Context) (bool, error) {
	value, _, err := readSetting(ctx, r.store, lrclibSetting)
	return value, err
}

// GetSHA256Enabled returns whether SHA-256 sampling is enabled.
func (r *Registry) GetSHA256Enabled(ctx context.Context) (bool, error) {
	value, _, err := readSetting(ctx, r.store, sha256Setting)
	return value, err
}

// GetSourceFileConcurrency returns the maximum number of files processed at once.
func (r *Registry) GetSourceFileConcurrency(ctx context.Context) (int, error) {
	value, _, err := readSetting(ctx, r.store, sourceFileConcurrencySetting)
	return value, err
}

// SetSourceFileConcurrency validates and stores the per-file processing limit.
func (r *Registry) SetSourceFileConcurrency(ctx context.Context, concurrency int) error {
	value, err := serializeSetting(sourceFileConcurrencySetting, concurrency)
	if err != nil {
		return err
	}
	r.runtimeUpdateMu.Lock()
	defer r.runtimeUpdateMu.Unlock()
	if err := r.store.Set(ctx, SourceFileConcurrencyKey, value); err != nil {
		return err
	}
	r.notifyConcurrencyUpdate(map[string]string{SourceFileConcurrencyKey: value})
	return nil
}

func (r *Registry) ReadRuntimeSettings(ctx context.Context) (RuntimeSettings, error) {
	tools, _, err := readSetting(ctx, r.store, toolsRootSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	work, _, err := readSetting(ctx, r.store, workRootSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	output, _, err := readSetting(ctx, r.store, outputRootSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	format, _, err := readSetting(ctx, r.store, publicationSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	template, _, err := readSetting(ctx, r.store, publicationPathTemplateSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	removeSources, _, err := readSetting(ctx, r.store, publicationRemoveSourcesSetting)
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
	logLevel, _, err := readSetting(ctx, r.store, logSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	ffmpegID, ffmpegExists, err := readSetting(ctx, r.store, activeFFmpegSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	fpcalcID, fpcalcExists, err := readSetting(ctx, r.store, activeFPCalcSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	ffmpeg, fpcalc := "", ""
	if ffmpegExists {
		ffmpeg = ffmpegID.String()
	}
	if fpcalcExists {
		fpcalc = fpcalcID.String()
	}
	caseSensitiveValue, caseExists, err := readSetting(ctx, r.store, outputCaseSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	var caseSensitive *bool
	if caseExists {
		caseSensitive = &caseSensitiveValue
	}
	unicodeNormalization, _, err := readSetting(ctx, r.store, outputUnicodeSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	fileConcurrency, _, err := readSetting(ctx, r.store, sourceFileConcurrencySetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	hasAcoustIDKey, err := r.HasAcoustIDApplicationKey(ctx)
	if err != nil {
		return RuntimeSettings{}, err
	}
	hasGitHubToken, err := r.HasGitHubToken(ctx)
	if err != nil {
		return RuntimeSettings{}, err
	}
	selfHostedThrottle, _, err := readSetting(ctx, r.store, musicBrainzSelfHostedThrottleSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	selfHostedDelay, _, err := readSetting(ctx, r.store, musicBrainzSelfHostedDelaySetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	return RuntimeSettings{
		SourceFileConcurrency:             fileConcurrency,
		ToolsDirectory:                    tools,
		WorkDirectory:                     work,
		OutputDirectory:                   output,
		PublicationFormat:                 format,
		PublicationPathTemplate:           template,
		PublicationRemoveSources:          removeSources,
		MusicBrainzMode:                   musicBrainz.Mode,
		MusicBrainzBaseURL:                musicBrainz.BaseURL,
		MusicBrainzVerifiedAt:             musicBrainz.VerifiedAt,
		LRCLIBEnabled:                     lrclib,
		LogLevel:                          logLevel,
		ActiveFFmpegInstallation:          ffmpeg,
		ActiveFPCalcInstallation:          fpcalc,
		OutputCaseSensitive:               caseSensitive,
		OutputUnicodeNormalization:        unicodeNormalization,
		HasAcoustIDApplicationKey:         hasAcoustIDKey,
		HasGitHubToken:                    hasGitHubToken,
		MusicBrainzSelfHostedThrottle:     selfHostedThrottle,
		MusicBrainzSelfHostedDelaySeconds: selfHostedDelay,
	}, nil
}

// ReadPublicationPlanningSettings reads the exact runtime settings consumed by
// a publication planner from one store snapshot. It deliberately excludes
// unrelated settings and secrets.
func (r *Registry) ReadPublicationPlanningSettings(ctx context.Context) (RuntimeSettings, error) {
	store, ok := r.store.(publicationPlanningSnapshotStore)
	if !ok {
		return RuntimeSettings{}, fmt.Errorf("coherent publication settings snapshot is unavailable")
	}
	values, err := store.GetPublicationPlanningValues(ctx)
	if err != nil {
		return RuntimeSettings{}, fmt.Errorf("read publication planning settings: %w", err)
	}
	return ParsePublicationPlanningValues(values)
}

// ParsePublicationPlanningValues decodes the exact settings needed by a
// publication planner from a previously captured settings snapshot.
func ParsePublicationPlanningValues(values map[string]string) (RuntimeSettings, error) {
	output, err := parseSnapshotSetting(values, outputRootSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	format, err := parseSnapshotSetting(values, publicationSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	template, err := parseSnapshotSetting(values, publicationPathTemplateSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	var caseSensitive *bool
	if _, exists := values[OutputCaseSensitiveKey]; exists {
		parsed, err := parseSnapshotSetting(values, outputCaseSetting)
		if err != nil {
			return RuntimeSettings{}, err
		}
		caseSensitive = &parsed
	}
	unicodeNormalization, err := parseSnapshotSetting(values, outputUnicodeSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	ffmpeg, err := parseSnapshotSetting(values, activeFFmpegSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	fpcalc, err := parseSnapshotSetting(values, activeFPCalcSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	lrclib, err := parseSnapshotSetting(values, lrclibSetting)
	if err != nil {
		return RuntimeSettings{}, err
	}
	return RuntimeSettings{
		OutputDirectory: output, PublicationFormat: format, PublicationPathTemplate: template,
		OutputCaseSensitive: caseSensitive, OutputUnicodeNormalization: unicodeNormalization,
		ActiveFFmpegInstallation: ffmpeg.String(), ActiveFPCalcInstallation: fpcalc.String(), LRCLIBEnabled: lrclib,
	}, nil
}

// UpdateMusicBrainzProviderSettings atomically stores only the supplied fields,
// preserving concurrent updates to the other optional field.
func (r *Registry) UpdateMusicBrainzProviderSettings(ctx context.Context, throttle *bool, delaySeconds *float64) error {
	values := make(map[string]string, 2)
	if throttle != nil {
		value, err := serializeSetting(musicBrainzSelfHostedThrottleSetting, *throttle)
		if err != nil {
			return err
		}
		values[MusicBrainzSelfHostedThrottleKey] = value
	}
	if delaySeconds != nil {
		value, err := serializeSetting(musicBrainzSelfHostedDelaySetting, *delaySeconds)
		if err != nil {
			return err
		}
		values[MusicBrainzSelfHostedDelayKey] = value
	}
	if len(values) == 0 {
		return nil
	}
	return r.store.SetMany(ctx, values)
}

func (r *Registry) GetMusicBrainzProviderSettings(ctx context.Context) (bool, float64, error) {
	throttle, _, err := readSetting(ctx, r.store, musicBrainzSelfHostedThrottleSetting)
	if err != nil {
		return false, 0, err
	}
	delay, _, err := readSetting(ctx, r.store, musicBrainzSelfHostedDelaySetting)
	if err != nil {
		return false, 0, err
	}
	return throttle, delay, nil
}

// SetLRCLIBEnabled stores the LRCLIB integration flag.
func (r *Registry) SetLRCLIBEnabled(ctx context.Context, enabled bool) error {
	value, err := serializeSetting(lrclibSetting, enabled)
	if err != nil {
		return err
	}
	return r.store.Set(ctx, LRCLIBEnabledKey, value)
}

// SetSHA256Enabled stores the SHA-256 sampling flag.
func (r *Registry) SetSHA256Enabled(ctx context.Context, enabled bool) error {
	value, err := serializeSetting(sha256Setting, enabled)
	if err != nil {
		return err
	}
	return r.store.Set(ctx, SHA256EnabledKey, value)
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
