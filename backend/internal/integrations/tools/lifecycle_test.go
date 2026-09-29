package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

type versionRunner struct{}

func (versionRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return []byte("ffmpeg version 8.0"), nil
}

type fixedVersionRunner string

func (runner fixedVersionRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return []byte(runner), nil
}

func TestMaterializeRejectsSubstringVersionBeforeWritingTargets(t *testing.T) {
	staging, root := t.TempDir(), t.TempDir()
	writeExecutables(t, staging, "linux")
	_, _, err := NewLifecycle(fixedVersionRunner("ffmpeg version 18.0 built with 8.0")).Materialize(
		context.Background(), staging, root, PackageFFmpeg, "8.0", MaterializeOptions{GOOS: "linux", GOARCH: "amd64"},
	)
	if err == nil {
		t.Fatal("different version with matching substring was installed")
	}
	if _, err := os.Lstat(filepath.Join(root, "ffmpeg", "8.0", "ffmpeg")); !os.IsNotExist(err) {
		t.Fatalf("failed version check wrote target: %v", err)
	}
}

func TestVerifyInstallationRequiresExactVersionToken(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "ffmpeg", "8.0")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutables(t, directory, "linux")
	lifecycle := NewLifecycle(fixedVersionRunner("ffmpeg version 18.0 built with 8.0"))
	if _, err := lifecycle.VerifyInstallation(context.Background(), root, "ffmpeg/8.0", PackageFFmpeg, "8.0", "linux"); err == nil {
		t.Fatal("different installed version passed activation verification")
	}
	lifecycle = NewLifecycle(fixedVersionRunner("ffmpeg version n8.0 Copyright FFmpeg"))
	if _, err := lifecycle.VerifyInstallation(context.Background(), root, "ffmpeg/8.0", PackageFFmpeg, "8.0", "linux"); err != nil {
		t.Fatalf("numbered release token was rejected: %v", err)
	}
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
func TestManagedRelativePathRejectsParentDirectory(t *testing.T) {
	if _, err := ManagedRelativePath(PackageFFmpeg, ".."); err == nil {
		t.Fatal("parent directory accepted as a release identity")
	}
}
func TestMaterializeRequiresEveryFFmpegExecutable(t *testing.T) {
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(staging, "ffmpeg"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := NewLifecycle(versionRunner{}).Materialize(context.Background(), staging, t.TempDir(), PackageFFmpeg, "8.0", MaterializeOptions{GOOS: "linux", GOARCH: "amd64"})
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

func TestPreflightRejectsSymlinkedVersionDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "ffmpeg")); err != nil {
		t.Fatal(err)
	}
	if _, err := PreflightTargets(root, PackageFFmpeg, "8.0", "linux", nil); err == nil {
		t.Fatal("preflight traversed a symlinked package directory")
	}
}

func TestExtractZipRejectsSymlinkEntries(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "link.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("../outside"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ExtractZip(archive, t.TempDir()); err == nil {
		t.Fatal("symlink archive entry accepted")
	}
}

func TestExtractTarXzExtractsBtbNFormatAndRejectsTraversal(t *testing.T) {
	for _, test := range []struct {
		name   string
		reject bool
	}{
		{name: "ffmpeg/ffmpeg"},
		{name: "../escape", reject: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var tarBuffer bytes.Buffer
			tarWriter := tar.NewWriter(&tarBuffer)
			payload := []byte("binary")
			if err := tarWriter.WriteHeader(&tar.Header{Name: test.name, Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := tarWriter.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := tarWriter.Close(); err != nil {
				t.Fatal(err)
			}
			var compressed bytes.Buffer
			xzWriter, err := xz.NewWriter(&compressed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := xzWriter.Write(tarBuffer.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err := xzWriter.Close(); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(t.TempDir(), "package.tar.xz")
			if err := os.WriteFile(archive, compressed.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			staging := t.TempDir()
			err = ExtractTarXz(archive, staging)
			if test.reject {
				if err == nil {
					t.Fatal("traversal archive accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(staging, test.name))
			if err != nil || !bytes.Equal(content, payload) {
				t.Fatalf("extracted payload = %q, %v", content, err)
			}
		})
	}
}
func TestActivationAndDeleteKeepActiveInstallation(t *testing.T) {
	active := map[string]string{"id": "ffmpeg/8.0"}
	if err := Activate(active, PackageFFmpeg, "id"); err != nil {
		t.Fatal(err)
	}
	if err := Delete(t.TempDir(), "ffmpeg/8.0", "linux", active, PackageFFmpeg, "id"); err == nil {
		t.Fatal("active installation deleted")
	}
}

func TestMaterializeRequiresExactConflictConfirmationAndPreservesUnknownFiles(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	relative, err := ManagedRelativePath(PackageFFmpeg, "8.0")
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, relative)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegTarget := filepath.Join(directory, "ffmpeg")
	if err := os.WriteFile(ffmpegTarget, []byte("unknown target"), 0o755); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(directory, "operator-note.txt")
	if err := os.WriteFile(note, []byte("preserve"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeExecutables(t, staging, "linux")

	lifecycle := NewLifecycle(versionRunner{})
	if _, _, err := lifecycle.Materialize(context.Background(), staging, root, PackageFFmpeg, "8.0", MaterializeOptions{GOOS: "linux", GOARCH: "amd64"}); err == nil {
		t.Fatal("materialization replaced an unknown target without confirmation")
	}
	if _, _, err := lifecycle.Materialize(context.Background(), staging, root, PackageFFmpeg, "8.0", MaterializeOptions{
		GOOS:               "linux",
		GOARCH:             "amd64",
		ConfirmedConflicts: []string{filepath.Join(directory, "ffprobe")},
	}); err == nil {
		t.Fatal("materialization accepted confirmation for a different target")
	}
	if _, _, err := lifecycle.Materialize(context.Background(), staging, root, PackageFFmpeg, "8.0", MaterializeOptions{
		GOOS:               "linux",
		GOARCH:             "amd64",
		ConfirmedConflicts: []string{ffmpegTarget},
	}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(note); err != nil || string(content) != "preserve" {
		t.Fatalf("unknown sibling changed: %q, %v", content, err)
	}
	if content, err := os.ReadFile(ffmpegTarget); err != nil || string(content) != "binary ffmpeg" {
		t.Fatalf("confirmed target = %q, %v", content, err)
	}
}

func TestMaterializeAcceptsOnlyDBManagedOverwritePaths(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	writeExecutables(t, staging, "linux")
	relative, err := ManagedRelativePath(PackageFFmpeg, "8.0")
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, relative)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegTarget := filepath.Join(directory, "ffmpeg")
	ffprobeTarget := filepath.Join(directory, "ffprobe")
	if err := os.WriteFile(ffmpegTarget, []byte("managed old ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ffprobeTarget, []byte("unknown ffprobe"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err = NewLifecycle(versionRunner{}).Materialize(context.Background(), staging, root, PackageFFmpeg, "8.0", MaterializeOptions{
		GOOS:         "linux",
		GOARCH:       "amd64",
		ManagedPaths: []string{ffmpegTarget},
	})
	if err == nil {
		t.Fatal("materialization replaced an unknown package executable")
	}
	if content, err := os.ReadFile(ffmpegTarget); err != nil || string(content) != "managed old ffmpeg" {
		t.Fatalf("managed file changed after preflight failure: %q, %v", content, err)
	}
	if content, err := os.ReadFile(ffprobeTarget); err != nil || string(content) != "unknown ffprobe" {
		t.Fatalf("unknown file changed after preflight failure: %q, %v", content, err)
	}
}

func TestMaterializeUsesTargetPlatformAndVerifiesWholePackageBeforeCommit(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	writeExecutables(t, staging, "windows")
	relative, err := ManagedRelativePath(PackageFFmpeg, "8.0")
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, relative)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegTarget := filepath.Join(directory, "ffmpeg.exe")
	ffprobeTarget := filepath.Join(directory, "ffprobe.exe")
	if err := os.WriteFile(ffmpegTarget, []byte("old ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ffprobeTarget, []byte("old ffprobe"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := failingVersionRunner{failedName: "ffprobe.exe"}

	_, _, err = NewLifecycle(runner).Materialize(context.Background(), staging, root, PackageFFmpeg, "8.0", MaterializeOptions{
		GOOS:         "windows",
		GOARCH:       "amd64",
		ManagedPaths: []string{ffmpegTarget, ffprobeTarget},
	})
	if err == nil {
		t.Fatal("package verification failure was ignored")
	}
	for path, want := range map[string]string{ffmpegTarget: "old ffmpeg", ffprobeTarget: "old ffprobe"} {
		content, readErr := os.ReadFile(path)
		if readErr != nil || string(content) != want {
			t.Errorf("target %s changed after package failure: %q, %v", path, content, readErr)
		}
	}
}

func TestMaterializeRejectsTargetAddedAfterPreflight(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	writeExecutables(t, staging, "linux")
	target := filepath.Join(root, "ffmpeg", "8.0", "ffmpeg")
	runner := targetCreatingRunner{target: target}
	_, _, err := NewLifecycle(runner).Materialize(context.Background(), staging, root, PackageFFmpeg, "8.0", MaterializeOptions{
		GOOS:   "linux",
		GOARCH: "amd64",
	})
	if err == nil {
		t.Fatal("materialization replaced a target created after preflight")
	}
	if content, readErr := os.ReadFile(target); readErr != nil || string(content) != "raced file" {
		t.Fatalf("concurrent target changed: %q, %v", content, readErr)
	}
}

func TestVerifyInstallationRechecksPackageExecutables(t *testing.T) {
	root := t.TempDir()
	relative, err := ManagedRelativePath(PackageFFmpeg, "8.0")
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, relative)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutables(t, directory, "linux")

	versions, err := NewLifecycle(versionRunner{}).VerifyInstallation(context.Background(), root, relative, PackageFFmpeg, "8.0", "linux")
	if err != nil || len(versions) != 2 {
		t.Fatalf("verified versions = %#v, %v", versions, err)
	}
	if _, err := NewLifecycle(failingVersionRunner{failedName: "ffprobe"}).VerifyInstallation(context.Background(), root, relative, PackageFFmpeg, "8.0", "linux"); err == nil {
		t.Fatal("activation verification accepted a failing executable")
	}
}

func TestDeleteRemovesOnlyManagedExecutables(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "ffmpeg", "8.0")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe", "operator-note.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	active := map[string]string{string(PackageFFmpeg): "other"}
	if err := Delete(root, "ffmpeg/8.0", "linux", active, PackageFFmpeg, "installation-id"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "operator-note.txt")); err != nil {
		t.Fatalf("unknown file was removed: %v", err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Errorf("managed executable %q remains: %v", name, err)
		}
	}
}

func TestExtractRejectsSymlinksAndTraversalComponents(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := extractFile(root, "escape/file", false, 0o644, func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("outside")), nil
	}); err == nil {
		t.Fatal("extraction traversed a symlink directory")
	}
	if err := extractFile(root, "inside/../escape", false, 0o644, func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("traversal")), nil
	}); err == nil {
		t.Fatal("extraction accepted a traversal component")
	}
	if _, err := os.Stat(filepath.Join(outside, "file")); !os.IsNotExist(err) {
		t.Fatalf("symlink target was written: %v", err)
	}
}

func TestVerifySHA256ChecksPublishedDigestWhenPresent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "archive")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(file, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"); err != nil {
		t.Fatalf("valid checksum rejected: %v", err)
	}
	if err := VerifySHA256(file, "bad-checksum"); err == nil {
		t.Fatal("invalid checksum accepted")
	}
	if err := VerifySHA256(file, ""); err != nil {
		t.Fatalf("missing optional checksum rejected: %v", err)
	}
}

type failingVersionRunner struct {
	failedName string
}

type targetCreatingRunner struct {
	target string
}

func (runner targetCreatingRunner) Run(_ context.Context, executable string, _ ...string) ([]byte, error) {
	if strings.Contains(executable, "ffmpeg") {
		if err := os.WriteFile(runner.target, []byte("raced file"), 0o644); err != nil {
			return nil, err
		}
	}
	return []byte("ffmpeg version 8.0"), nil
}

func (runner failingVersionRunner) Run(_ context.Context, executable string, _ ...string) ([]byte, error) {
	if strings.Contains(executable, runner.failedName) {
		return nil, errors.New("verification failed")
	}
	return []byte("ffmpeg version 8.0"), nil
}

func writeExecutables(t *testing.T, staging, goos string) {
	t.Helper()
	for _, name := range ExpectedExecutables(PackageFFmpeg, goos) {
		if err := os.WriteFile(filepath.Join(staging, name), []byte("binary "+strings.TrimSuffix(name, ".exe")), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
