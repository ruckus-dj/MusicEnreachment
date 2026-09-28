package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// InstallationActivator is the transactional persistence boundary for active
// tool selections.
type InstallationActivator interface {
	ActivateInstallation(context.Context, uuid.UUID, string, string, string, string) error
}

type Installations struct {
	repository InstallationActivator
	platform   settings.Platform
}

func NewInstallations(repository InstallationActivator, platform settings.Platform) *Installations {
	return &Installations{repository: repository, platform: platform}
}

// Activate rejects unknown packages before entering persistence. The repository
// locks and validates the installation and changes the active setting atomically.
func (s *Installations) Activate(ctx context.Context, packageKind string, id uuid.UUID) error {
	var setting string
	switch packageKind {
	case "ffmpeg":
		setting = settings.ActiveFFmpegInstallationKey
	case "fpcalc":
		setting = settings.ActiveFPCalcInstallationKey
	default:
		return fmt.Errorf("unsupported package %q", packageKind)
	}
	return s.repository.ActivateInstallation(ctx, id, packageKind, s.platform.GOOS, s.platform.GOARCH, setting)
}
