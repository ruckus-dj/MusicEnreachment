package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type SetupState struct {
	Completed           bool
	ConfigurationHealth settings.ConfigurationHealth
	Platform            settings.PlatformState
	Runtime             settings.RuntimeSettings
	SHA256Enabled       bool
}

type PathValidation struct {
	ToolsDirectory             string
	OutputDirectory            string
	OutputCaseSensitive        bool
	OutputUnicodeNormalization string
}

type SetupService struct {
	store             settings.Store
	registry          *settings.Registry
	platform          settings.PlatformState
	musicbrainzClient musicbrainz.Checker
	installations     InstallationLookup
}

type InstallationLookup interface {
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
	ListInstallations(context.Context, string, string, string) ([]persistence.ToolInstallation, error)
}

func NewSetup(store settings.Store, registry *settings.Registry, platform settings.PlatformState, installations InstallationLookup, checker musicbrainz.Checker) *SetupService {
	if checker == nil {
		checker = musicbrainz.NewClient()
	}
	return &SetupService{
		store:             store,
		registry:          registry,
		platform:          platform,
		musicbrainzClient: checker,
		installations:     installations,
	}
}
func (s *SetupService) State(ctx context.Context) (SetupState, error) {
	return s.state(ctx, false)
}

func (s *SetupService) state(ctx context.Context, completing bool) (SetupState, error) {
	completed, err := s.registry.SetupCompleted(ctx)
	if err != nil {
		return SetupState{}, err
	}
	runtimeSettings, err := s.registry.ReadRuntimeSettings(ctx)
	if err != nil {
		return SetupState{}, fmt.Errorf("read setup runtime settings: %w", err)
	}
	sha256Enabled, err := s.registry.GetSHA256Enabled(ctx)
	if err != nil {
		return SetupState{}, fmt.Errorf("read setup SHA-256 setting: %w", err)
	}
	// Validate the same values that Complete later compares under database locks.
	health := settings.ConfigurationHealth{Healthy: true}
	if s.platform.Diagnostic {
		health.Healthy = false
		health.Problems = append(health.Problems, s.platform.Reason)
	}
	for _, required := range []struct{ key, value string }{
		{settings.ToolsDirectoryKey, runtimeSettings.ToolsDirectory},
		{settings.OutputDirectoryKey, runtimeSettings.OutputDirectory},
		{settings.PublicationFormatKey, runtimeSettings.PublicationFormat},
		{settings.ActiveFFmpegInstallationKey, runtimeSettings.ActiveFFmpegInstallation},
		{settings.ActiveFPCalcInstallationKey, runtimeSettings.ActiveFPCalcInstallation},
	} {
		if required.value == "" {
			health.Healthy = false
			health.Problems = append(health.Problems, "missing or invalid "+required.key)
		}
	}
	if !s.platform.Platform.Supported() {
		health.Healthy = false
		health.Problems = append(health.Problems, "unsupported instance platform")
	}
	for _, required := range []struct {
		packageKind string
		id          string
	}{
		{packageKind: "ffmpeg", id: runtimeSettings.ActiveFFmpegInstallation},
		{packageKind: "fpcalc", id: runtimeSettings.ActiveFPCalcInstallation},
	} {
		if s.installations == nil {
			health.Healthy = false
			health.Problems = append(health.Problems, required.packageKind+" installation validation unavailable")
			continue
		}
		id, parseErr := uuid.Parse(required.id)
		if parseErr != nil {
			health.Healthy = false
			health.Problems = append(health.Problems, "active "+required.packageKind+" installation is invalid")
			continue
		}
		installation, err := s.installations.GetInstallation(ctx, id)
		if err != nil {
			health.Healthy = false
			health.Problems = append(health.Problems, "active "+required.packageKind+" installation is unavailable")
			continue
		}
		if installation.PackageKind != required.packageKind ||
			installation.PlatformGOOS != s.platform.Platform.GOOS ||
			installation.PlatformGOARCH != s.platform.Platform.GOARCH ||
			installation.State != "ready" {
			health.Healthy = false
			health.Problems = append(health.Problems, "active "+required.packageKind+" installation is not ready for this platform")
			continue
		}
		var versions map[string]string
		if installation.VerifiedAt == nil || json.Unmarshal(installation.ExecutableVersions, &versions) != nil {
			health.Healthy = false
			health.Problems = append(health.Problems, "active "+required.packageKind+" installation has no verified executables")
			continue
		}
		for _, name := range tools.ExpectedExecutables(tools.PackageKind(required.packageKind), s.platform.Platform.GOOS) {
			if strings.TrimSpace(versions[name]) == "" {
				health.Healthy = false
				health.Problems = append(health.Problems, "active "+required.packageKind+" installation has no verified "+name)
			}
		}
	}
	if !completing && runtimeSettings.MusicBrainzVerifiedAt == nil {
		health.Healthy = false
		health.Problems = append(health.Problems, "musicbrainz not verified")
	}
	if runtimeSettings.ToolsDirectory != "" && runtimeSettings.OutputDirectory != "" &&
		settings.PathsOverlap(runtimeSettings.ToolsDirectory, runtimeSettings.OutputDirectory) {
		health.Healthy = false
		health.Problems = append(health.Problems, "tools directory overlaps output directory")
	}
	if runtimeSettings.ToolsDirectory != "" {
		info, err := os.Stat(runtimeSettings.ToolsDirectory)
		if err != nil || !info.IsDir() || settings.ProbeWritable(runtimeSettings.ToolsDirectory) != nil {
			health.Healthy = false
			health.Problems = append(health.Problems, "tools directory is unavailable or not writable")
		}
	}
	if runtimeSettings.OutputDirectory != "" {
		info, err := os.Stat(runtimeSettings.OutputDirectory)
		if err != nil || !info.IsDir() {
			health.Healthy = false
			health.Problems = append(health.Problems, "output directory is unavailable")
		} else {
			current, probeErr := settings.ProbeOutputDirectory(runtimeSettings.OutputDirectory, !completed)
			if probeErr != nil {
				health.Healthy = false
				health.Problems = append(health.Problems, "output directory is not ready")
			} else if runtimeSettings.OutputCaseSensitive == nil || runtimeSettings.OutputUnicodeNormalization == "" {
				health.Healthy = false
				health.Problems = append(health.Problems, "output filesystem semantics are missing")
			} else if current.CaseSensitive != *runtimeSettings.OutputCaseSensitive ||
				current.UnicodeNormalization != runtimeSettings.OutputUnicodeNormalization {
				health.Healthy = false
				health.Problems = append(health.Problems, "output filesystem semantics changed")
			}
		}
	}
	return SetupState{Completed: completed, ConfigurationHealth: health, Platform: s.platform, Runtime: runtimeSettings, SHA256Enabled: sha256Enabled}, nil
}
func (s *SetupService) SaveRuntime(ctx context.Context, toolsDirectory, outputDirectory, publicationFormat string) error {
	update := settings.RuntimeUpdate{}
	currentTools, hasTools, err := s.registry.GetToolsDirectory(ctx)
	if err != nil {
		return fmt.Errorf("get tools directory: %w", err)
	}
	currentOutput, hasOutput, err := s.registry.GetOutputDirectory(ctx)
	if err != nil {
		return fmt.Errorf("get output directory: %w", err)
	}
	if toolsDirectory != "" {
		normalized, err := settings.NormalizePath(toolsDirectory)
		if err != nil {
			return fmt.Errorf("tools directory: %w", err)
		}
		if err := settings.ProbeWritable(normalized); err != nil {
			return fmt.Errorf("tools directory: %w", err)
		}
		if hasTools && normalized != currentTools {
			if s.installations == nil {
				return fmt.Errorf("tools directory changes require the managed-tools move service")
			}
			installed, err := s.installations.ListInstallations(ctx, "", "", "")
			if err != nil {
				return fmt.Errorf("check managed installations: %w", err)
			}
			if len(installed) != 0 {
				return fmt.Errorf("tools directory cannot change while installations exist; use the move operation")
			}
		}
		expectedTools := currentTools
		if !hasTools {
			expectedTools = ""
		}
		currentTools, hasTools = normalized, true
		update.ToolsDirectory = &normalized
		update.ExpectedToolsDirectory = &expectedTools
	}
	if outputDirectory != "" {
		normalized, err := settings.NormalizePath(outputDirectory)
		if err != nil {
			return fmt.Errorf("output directory: %w", err)
		}
		semantics, err := settings.ProbeOutputDirectory(normalized, true)
		if err != nil {
			return fmt.Errorf("output directory semantics: %w", err)
		}
		currentOutput, hasOutput = normalized, true
		update.OutputDirectory = &normalized
		update.OutputCaseSensitive = &semantics.CaseSensitive
		update.OutputUnicodeNormalization = &semantics.UnicodeNormalization
	}
	if hasTools && hasOutput && settings.PathsOverlap(currentTools, currentOutput) {
		return fmt.Errorf("tools directory overlaps with output directory")
	}
	if publicationFormat != "" {
		update.PublicationFormat = &publicationFormat
	}
	return s.registry.UpdateRuntime(ctx, update)
}

// ValidatePaths checks proposed paths without changing runtime settings or
// creating missing directories. Empty arguments use the saved paths.
func (s *SetupService) ValidatePaths(ctx context.Context, toolsDir, outputDir string) (PathValidation, error) {
	savedTools, _, err := s.registry.GetToolsDirectory(ctx)
	if err != nil {
		return PathValidation{}, fmt.Errorf("read tools directory: %w", err)
	}
	savedOutput, _, err := s.registry.GetOutputDirectory(ctx)
	if err != nil {
		return PathValidation{}, fmt.Errorf("read output directory: %w", err)
	}
	completed, err := s.registry.SetupCompleted(ctx)
	if err != nil {
		return PathValidation{}, fmt.Errorf("read setup completion: %w", err)
	}
	if toolsDir == "" {
		toolsDir = savedTools
	}
	if outputDir == "" {
		outputDir = savedOutput
	}
	if toolsDir == "" {
		return PathValidation{}, fmt.Errorf("tools directory is required")
	}
	if outputDir == "" {
		return PathValidation{}, fmt.Errorf("output directory is required")
	}
	toolsPath, err := settings.NormalizePath(toolsDir)
	if err != nil {
		return PathValidation{}, fmt.Errorf("tools directory: %w", err)
	}
	outputPath, err := settings.NormalizePath(outputDir)
	if err != nil {
		return PathValidation{}, fmt.Errorf("output directory: %w", err)
	}
	if settings.PathsOverlap(toolsPath, outputPath) {
		return PathValidation{}, fmt.Errorf("tools directory overlaps output directory")
	}
	toolsProbe, _, err := existingProbeDirectory(toolsPath)
	if err != nil {
		return PathValidation{}, fmt.Errorf("tools directory: %w", err)
	}
	if err := settings.ProbeWritable(toolsProbe); err != nil {
		return PathValidation{}, fmt.Errorf("tools directory is not writable or creatable: %w", err)
	}
	outputProbe, exists, err := existingProbeDirectory(outputPath)
	if err != nil {
		return PathValidation{}, fmt.Errorf("output directory: %w", err)
	}
	semantics, err := settings.ProbeOutputDirectory(outputProbe, exists && (!completed || outputPath != savedOutput))
	if err != nil {
		return PathValidation{}, fmt.Errorf("output directory is not writable or empty or semantics could not be probed: %w", err)
	}
	return PathValidation{
		ToolsDirectory:             toolsPath,
		OutputDirectory:            outputPath,
		OutputCaseSensitive:        semantics.CaseSensitive,
		OutputUnicodeNormalization: semantics.UnicodeNormalization,
	}, nil
}

func existingProbeDirectory(path string) (string, bool, error) {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", false, fmt.Errorf("path is not a directory")
			}
			return current, current == path, nil
		}
		if !os.IsNotExist(err) || current == filepath.Dir(current) {
			return "", false, fmt.Errorf("inspect directory: %w", err)
		}
	}
}

// CheckMusicBrainz verifies connectivity to the configured MusicBrainz endpoint
// and saves the verification timestamp on success.
func (s *SetupService) CheckMusicBrainz(ctx context.Context) error {
	config, err := s.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load MusicBrainz config: %w", err)
	}

	result := s.musicbrainzClient.CheckConnectivity(ctx, config.Mode, config.BaseURL)
	if !result.Success {
		return fmt.Errorf("MusicBrainz connectivity check failed: %s", result.Error)
	}

	if err := s.registry.SetMusicBrainzVerified(ctx, config); err != nil {
		return fmt.Errorf("failed to save verification result: %w", err)
	}

	return nil
}

func (s *SetupService) SaveMusicBrainz(ctx context.Context, mode, baseURL string) error {
	if err := s.registry.SetMusicBrainzConfig(ctx, mode, baseURL); err != nil {
		return fmt.Errorf("save MusicBrainz settings: %w", err)
	}
	return nil
}

func (s *SetupService) SetLRCLIBEnabled(ctx context.Context, enabled bool) error {
	if err := s.registry.SetLRCLIBEnabled(ctx, enabled); err != nil {
		return fmt.Errorf("save LRCLIB setting: %w", err)
	}
	return nil
}

func (s *SetupService) SetLogLevel(ctx context.Context, level string) error {
	if err := s.registry.SetLogLevel(ctx, level); err != nil {
		return fmt.Errorf("save log level: %w", err)
	}
	return nil
}

func (s *SetupService) Complete(ctx context.Context) error {
	if s.platform.Diagnostic || !s.platform.Platform.Supported() {
		return fmt.Errorf("setup is unavailable for the current platform")
	}
	state, err := s.state(ctx, true)
	if err != nil {
		return err
	}
	if !state.ConfigurationHealth.Healthy {
		return fmt.Errorf("setup requirements are not met: %s", strings.Join(state.ConfigurationHealth.Problems, ", "))
	}
	config, err := s.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return fmt.Errorf("read MusicBrainz configuration: %w", err)
	}
	result := s.musicbrainzClient.CheckConnectivity(ctx, config.Mode, config.BaseURL)
	if !result.Success {
		return fmt.Errorf("MusicBrainz connectivity check failed: %s", result.Error)
	}
	// Re-probe after the potentially slow request, against the exact paths
	// whose saved values the commit will lock and compare.
	if err := settings.ProbeWritable(state.Runtime.ToolsDirectory); err != nil {
		return fmt.Errorf("tools directory is not writable: %w", err)
	}
	semantics, err := settings.ProbeOutputDirectory(state.Runtime.OutputDirectory, true)
	if err != nil {
		return fmt.Errorf("output directory is not ready: %w", err)
	}
	if state.Runtime.OutputCaseSensitive == nil || semantics.CaseSensitive != *state.Runtime.OutputCaseSensitive ||
		semantics.UnicodeNormalization != state.Runtime.OutputUnicodeNormalization {
		return fmt.Errorf("output filesystem semantics changed")
	}
	return s.registry.CompleteSetupIfCurrent(ctx, s.platform.Platform, state.Runtime, config)
}
