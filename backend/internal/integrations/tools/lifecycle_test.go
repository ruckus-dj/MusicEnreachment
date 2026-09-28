package tools

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

type versionRunner struct{}

func (versionRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return []byte("ffmpeg version 8.0"), nil
}
func TestPreflightDoesNotClaimUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	preflight, err := PreflightTargets(root, PackageFFmpeg, "8.0", "linux", map[string]struct{}{})
	if err != nil || len(preflight.Conflicts) != 0 {
		t.Fatalf("%#v, %v", preflight, err)
	}
}
func TestMaterializeRequiresEveryFFmpegExecutable(t *testing.T) {
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(staging, "ffmpeg"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := NewLifecycle(versionRunner{}).Materialize(context.Background(), staging, t.TempDir(), PackageFFmpeg, "8.0", false)
	if err == nil {
		t.Fatal("partial FFmpeg package was materialized")
	}
}
func TestExtractZipRejectsTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "bad.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("../escape")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("x"))
	_ = writer.Close()
	_ = file.Close()
	if err := ExtractZip(archive, t.TempDir()); err == nil {
		t.Fatal("traversal archive accepted")
	}
}
func TestActivationAndDeleteKeepActiveInstallation(t *testing.T) {
	active := map[string]string{"id": "ffmpeg/8.0"}
	if err := Activate(active, PackageFFmpeg, "id"); err != nil {
		t.Fatal(err)
	}
	if err := Delete(t.TempDir(), "ffmpeg/8.0", active, PackageFFmpeg, "id"); err == nil {
		t.Fatal("active installation deleted")
	}
}
