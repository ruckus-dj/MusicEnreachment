package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type SetupState struct {
	Completed           bool
	ConfigurationHealth settings.ConfigurationHealth
	Platform            settings.PlatformState
	Runtime             settings.RuntimeSettings
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
	completed, err := s.registry.SetupCompleted(ctx)
	if err != nil {
		return SetupState{}, err
	}
	health, err := s.registry.ComputeConfigurationHealth(ctx, s.platform)
	if err != nil {
		return SetupState{}, err
	}
	if !s.platform.Platform.Supported() {
		health.Healthy = false
		health.Problems = append(health.Problems, "unsupported instance platform")
	}
	for _, required := range []struct {
		packageKind string
		setting     string
	}{
		{packageKind: "ffmpeg", setting: settings.ActiveFFmpegInstallationKey},
		{packageKind: "fpcalc", setting: settings.ActiveFPCalcInstallationKey},
	} {
		if s.installations == nil {
			health.Healthy = false
			health.Problems = append(health.Problems, required.packageKind+" installation validation unavailable")
			continue
		}
		idValue, exists, err := s.store.Get(ctx, required.setting)
		if err != nil {
			return SetupState{}, fmt.Errorf("read active %s installation: %w", required.packageKind, err)
		}
		id, parseErr := uuid.Parse(idValue)
		if !exists || parseErr != nil {
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
		}
	}
	runtimeSettings, err := s.registry.ReadRuntimeSettings(ctx)
	if err != nil {
		return SetupState{}, fmt.Errorf("read setup runtime settings: %w", err)
	}
	return SetupState{Completed: completed, ConfigurationHealth: health, Platform: s.platform, Runtime: runtimeSettings}, nil
}
func (s *SetupService) SaveRuntime(ctx context.Context, toolsDirectory, outputDirectory, publicationFormat string) error {
	values := make(map[string]string)
	currentTools, hasTools, err := s.registry.GetToolsDirectory(ctx)
	if err != nil {
		return fmt.Errorf("get tools directory: %w", err)
	}
	currentOutput, hasOutput, err := s.registry.GetOutputDirectory(ctx)
	if err != nil {
		return fmt.Errorf("get output directory: %w", err)
	}
	if strings.TrimSpace(toolsDirectory) != "" {
		normalized, err := settings.NormalizePath(toolsDirectory)
		if err != nil {
			return fmt.Errorf("tools directory: %w", err)
		}
		if err := settings.ProbeWritable(normalized); err != nil {
			return fmt.Errorf("tools directory: %w", err)
		}
		currentTools, hasTools = normalized, true
		values[settings.ToolsDirectoryKey] = normalized
	}
	if strings.TrimSpace(outputDirectory) != "" {
		normalized, err := settings.NormalizePath(outputDirectory)
		if err != nil {
			return fmt.Errorf("output directory: %w", err)
		}
		if err := settings.ProbeWritableEmpty(normalized); err != nil {
			return fmt.Errorf("output directory: %w", err)
		}
		semantics, err := settings.ProbeFilesystemSemantics(normalized)
		if err != nil {
			return fmt.Errorf("output directory semantics: %w", err)
		}
		currentOutput, hasOutput = normalized, true
		values[settings.OutputDirectoryKey] = normalized
		values[settings.OutputCaseSensitiveKey] = fmt.Sprintf("%t", semantics.CaseSensitive)
		values[settings.OutputUnicodeNormalizationKey] = semantics.UnicodeNormalization
	}
	if hasTools && hasOutput && settings.PathsOverlap(currentTools, currentOutput) {
		return fmt.Errorf("tools directory overlaps with output directory")
	}
	if publicationFormat != "" {
		if publicationFormat != "source" && publicationFormat != "mka" {
			return fmt.Errorf("invalid publication format")
		}
		values[settings.PublicationFormatKey] = publicationFormat
	}
	return s.store.SetMany(ctx, values)
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

func (s *SetupService) Complete(ctx context.Context) error {
	if s.platform.Diagnostic || !s.platform.Platform.Supported() {
		return fmt.Errorf("setup is unavailable for the current platform")
	}
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	if !state.ConfigurationHealth.Healthy {
		return fmt.Errorf("setup requirements are not met: %s", strings.Join(state.ConfigurationHealth.Problems, ", "))
	}
	toolsDirectory, hasTools, err := s.registry.GetToolsDirectory(ctx)
	if err != nil {
		return fmt.Errorf("read tools directory: %w", err)
	}
	outputDirectory, hasOutput, err := s.registry.GetOutputDirectory(ctx)
	if err != nil {
		return fmt.Errorf("read output directory: %w", err)
	}
	if !hasTools || !hasOutput || settings.PathsOverlap(toolsDirectory, outputDirectory) {
		return fmt.Errorf("setup directories are missing or overlap")
	}
	if err := settings.ProbeWritable(toolsDirectory); err != nil {
		return fmt.Errorf("tools directory is not writable: %w", err)
	}
	if err := settings.ProbeWritableEmpty(outputDirectory); err != nil {
		return fmt.Errorf("output directory is not ready: %w", err)
	}
	if _, err := s.registry.GetOutputFilesystemSemantics(ctx); err != nil {
		return fmt.Errorf("output filesystem semantics are not available: %w", err)
	}
	return s.registry.CompleteSetup(ctx)
}
