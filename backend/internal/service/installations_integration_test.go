//go:build integration

package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestDeleteFailedInstallationPreservesUnownedTargetsWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "test", ReleaseIdentity: "8.0", RelativePath: "ffmpeg/8.0", State: "preparing",
	}
	if err := repository.CreateInstallation(ctx, installation); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkInstallationFailed(ctx, installation.ID); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := database.ExecContext(ctx, "INSERT INTO app_setting (setting_name, setting_value, updated_at) VALUES (?, ?, now())", settings.ToolsDirectoryKey, root); err != nil {
		t.Fatal(err)
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

	installations := service.NewInstallations(repository, settings.Platform{GOOS: "linux", GOARCH: "amd64"}, toolsDirectoryFixture(root), nil)
	if err := installations.Delete(ctx, "ffmpeg", installation.ID); err != nil {
		t.Fatalf("delete failed installation: %v", err)
	}

	if _, err := repository.GetInstallation(ctx, installation.ID); err == nil {
		t.Fatal("failed installation row remains after deletion")
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

func TestDeleteReadyInstallationRemovesOnlyManagedTargetsWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "test", ReleaseIdentity: "8.0", RelativePath: "ffmpeg/8.0", State: "preparing",
	}
	if err := repository.CreateInstallation(ctx, installation); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkInstallationReady(ctx, installation.ID, []byte(`{"ffmpeg":"8.0","ffprobe":"8.0"}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := database.ExecContext(ctx, "INSERT INTO app_setting (setting_name, setting_value, updated_at) VALUES (?, ?, now())", settings.ToolsDirectoryKey, root); err != nil {
		t.Fatal(err)
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

	installations := service.NewInstallations(repository, settings.Platform{GOOS: "linux", GOARCH: "amd64"}, toolsDirectoryFixture(root), nil)
	if err := installations.Delete(ctx, "ffmpeg", installation.ID); err != nil {
		t.Fatalf("delete ready installation: %v", err)
	}

	if _, err := repository.GetInstallation(ctx, installation.ID); err == nil {
		t.Fatal("ready installation row remains after deletion")
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
