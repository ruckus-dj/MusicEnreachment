package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

var _ service.InstallationRepository = (*persistence.ToolInstallationRepository)(nil)

type installationRepositoryFixture struct {
	installation *persistence.ToolInstallation
	activated    bool
	toolsRoot    string
	deleteCalls  int
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

func (fixture *installationRepositoryFixture) DeleteInstallation(_ context.Context, _ uuid.UUID, _, _, _, _ string, removeFiles func(*persistence.ToolInstallation, string) error) error {
	fixture.deleteCalls++
	return removeFiles(fixture.installation, fixture.toolsRoot)
}

type toolsDirectoryFixture string

func (directory toolsDirectoryFixture) GetToolsDirectory(context.Context) (string, bool, error) {
	return string(directory), true, nil
}

type installationVerifierFixture struct {
	err   error
	calls int
}

func (verifier *installationVerifierFixture) VerifyInstallationVerified(context.Context, string, string, tools.PackageKind, string, string) (tools.VerificationResult, error) {
	verifier.calls++
	return tools.VerificationResult{}, verifier.err
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

func TestDeleteFailedInstallationPreservesUnownedTargets(t *testing.T) {
	root := t.TempDir()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		RelativePath: "ffmpeg/8.0", State: "failed",
	}
	directory := filepath.Join(root, installation.RelativePath)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"ffmpeg": "unknown target", "ffprobe": "confirmed original restored after failed install",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	repository := &installationRepositoryFixture{installation: installation, toolsRoot: root}
	installations := service.NewInstallations(repository, settings.Platform{GOOS: "linux", GOARCH: "amd64"}, toolsDirectoryFixture(root), nil)
	if err := installations.Delete(context.Background(), "ffmpeg", installation.ID); err != nil {
		t.Fatalf("delete failed installation: %v", err)
	}
	if repository.deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", repository.deleteCalls)
	}
	for name, want := range map[string]string{
		"ffmpeg": "unknown target", "ffprobe": "confirmed original restored after failed install",
	} {
		got, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s after failed installation deletion = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestDeleteReadyInstallationRemovesOnlyManagedTargets(t *testing.T) {
	root := t.TempDir()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		RelativePath: "ffmpeg/8.0", State: "ready",
	}
	directory := filepath.Join(root, installation.RelativePath)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe", "operator-note.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	repository := &installationRepositoryFixture{installation: installation, toolsRoot: root}
	installations := service.NewInstallations(repository, settings.Platform{GOOS: "linux", GOARCH: "amd64"}, toolsDirectoryFixture(root), nil)
	if err := installations.Delete(context.Background(), "ffmpeg", installation.ID); err != nil {
		t.Fatalf("delete ready installation: %v", err)
	}
	if repository.deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", repository.deleteCalls)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("managed target %s remains: %v", name, err)
		}
	}
	if contents, err := os.ReadFile(filepath.Join(directory, "operator-note.txt")); err != nil || string(contents) != "operator-note.txt" {
		t.Fatalf("unknown sibling after ready installation deletion = %q, %v", contents, err)
	}
}
