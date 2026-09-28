package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type installationRepositoryFixture struct {
	installation *persistence.ToolInstallation
	activated    bool
}

func (fixture *installationRepositoryFixture) ActivateInstallation(context.Context, uuid.UUID, string, string, string, string) error {
	fixture.activated = true
	return nil
}

func (fixture *installationRepositoryFixture) GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error) {
	return fixture.installation, nil
}

func (fixture *installationRepositoryFixture) ListInstallations(context.Context, string, string, string) ([]persistence.ToolInstallation, error) {
	if fixture.installation == nil {
		return nil, nil
	}
	return []persistence.ToolInstallation{*fixture.installation}, nil
}

func (*installationRepositoryFixture) DeleteInstallation(context.Context, uuid.UUID, string, string, string, string, func(*persistence.ToolInstallation) error) error {
	return nil
}

type toolsDirectoryFixture string

func (directory toolsDirectoryFixture) GetToolsDirectory(context.Context) (string, bool, error) {
	return string(directory), true, nil
}

type installationVerifierFixture struct {
	err   error
	calls int
}

func (verifier *installationVerifierFixture) VerifyInstallation(context.Context, string, string, tools.PackageKind, string, string) (map[string]string, error) {
	verifier.calls++
	return nil, verifier.err
}

func TestActivationDoesNotChangeActiveSelectionWhenBinaryVerificationFails(t *testing.T) {
	id := uuid.New()
	repository := &installationRepositoryFixture{installation: &persistence.ToolInstallation{
		ID: id, PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		State: "ready", RelativePath: "ffmpeg/8.0", ReleaseIdentity: "8.0",
	}}
	verifier := &installationVerifierFixture{err: errors.New("binary mismatch")}
	installations := service.NewInstallations(
		repository,
		settings.Platform{GOOS: "linux", GOARCH: "amd64"},
		toolsDirectoryFixture("/tools"),
		verifier,
	)

	if err := installations.Activate(context.Background(), "ffmpeg", id); err == nil {
		t.Fatal("activation accepted a binary that failed re-verification")
	}
	if repository.activated {
		t.Fatal("active setting changed after failed executable verification")
	}
	if verifier.calls != 1 {
		t.Fatalf("verification calls = %d, want 1", verifier.calls)
	}
}
