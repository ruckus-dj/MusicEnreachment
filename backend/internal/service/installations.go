package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type InstallationRepository interface {
	ActivateInstallation(context.Context, uuid.UUID, string, string, string, string) error
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
	DeleteInstallation(context.Context, uuid.UUID, string, string, string, string, func(*persistence.ToolInstallation) error) error
}

type InstallationVerifier interface {
	VerifyInstallation(context.Context, string, string, tools.PackageKind, string, string) (map[string]string, error)
}

type ToolsDirectoryReader interface {
	GetToolsDirectory(context.Context) (string, bool, error)
}

type Installations struct {
	repository     InstallationRepository
	platform       settings.Platform
	toolsDirectory ToolsDirectoryReader
	verifier       InstallationVerifier
}

func NewInstallations(repository InstallationRepository, platform settings.Platform, toolsDirectory ToolsDirectoryReader, verifier InstallationVerifier) *Installations {
	return &Installations{repository: repository, platform: platform, toolsDirectory: toolsDirectory, verifier: verifier}
}

func (s *Installations) Activate(ctx context.Context, packageKind string, id uuid.UUID) error {
	var setting string
	var kind tools.PackageKind
	switch packageKind {
	case "ffmpeg":
		setting = settings.ActiveFFmpegInstallationKey
		kind = tools.PackageFFmpeg
	case "fpcalc":
		setting = settings.ActiveFPCalcInstallationKey
		kind = tools.PackageFPCalc
	default:
		return fmt.Errorf("unsupported package %q", packageKind)
	}
	if s.verifier == nil || s.toolsDirectory == nil {
		return fmt.Errorf("installation verification is unavailable")
	}
	installation, err := s.repository.GetInstallation(ctx, id)
	if err != nil {
		return fmt.Errorf("get installation for activation: %w", err)
	}
	if installation.PackageKind != packageKind || installation.PlatformGOOS != s.platform.GOOS ||
		installation.PlatformGOARCH != s.platform.GOARCH || installation.State != "ready" {
		return fmt.Errorf("installation is not ready for activation on this platform")
	}
	root, exists, err := s.toolsDirectory.GetToolsDirectory(ctx)
	if err != nil {
		return fmt.Errorf("read tools directory: %w", err)
	}
	if !exists || root == "" {
		return fmt.Errorf("tools directory is not configured")
	}
	if _, err := s.verifier.VerifyInstallation(ctx, root, installation.RelativePath, kind, installation.ReleaseIdentity, s.platform.GOOS); err != nil {
		return fmt.Errorf("verify installation before activation: %w", err)
	}
	return s.repository.ActivateInstallation(ctx, id, packageKind, s.platform.GOOS, s.platform.GOARCH, setting)
}

func (s *Installations) Delete(ctx context.Context, packageKind string, id uuid.UUID) error {
	var kind tools.PackageKind
	var setting string
	switch packageKind {
	case "ffmpeg":
		kind = tools.PackageFFmpeg
		setting = settings.ActiveFFmpegInstallationKey
	case "fpcalc":
		kind = tools.PackageFPCalc
		setting = settings.ActiveFPCalcInstallationKey
	default:
		return fmt.Errorf("unsupported package %q", packageKind)
	}
	if s.toolsDirectory == nil {
		return fmt.Errorf("tools directory is not configured")
	}
	root, exists, err := s.toolsDirectory.GetToolsDirectory(ctx)
	if err != nil {
		return fmt.Errorf("read tools directory: %w", err)
	}
	if !exists || root == "" {
		return fmt.Errorf("tools directory is not configured")
	}
	return s.repository.DeleteInstallation(ctx, id, packageKind, s.platform.GOOS, s.platform.GOARCH, setting, func(installation *persistence.ToolInstallation) error {
		return tools.Delete(root, installation.RelativePath, s.platform.GOOS, map[string]string{}, kind, id.String())
	})
}
