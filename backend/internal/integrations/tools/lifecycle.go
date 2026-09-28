package tools

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Preflight describes only exact managed target files; a tools root can contain
// arbitrary unrelated data.
type Preflight struct{ Targets, Conflicts []string }

func ManagedRelativePath(kind PackageKind, version string) (string, error) {
	if kind != PackageFFmpeg && kind != PackageFPCalc {
		return "", fmt.Errorf("unsupported package %q", kind)
	}
	if version == "" || version != filepath.Base(version) || strings.ContainsAny(version, `\\/:`) || version == "." {
		return "", fmt.Errorf("unsafe release identity %q", version)
	}
	return filepath.Join(string(kind), version), nil
}
func ExpectedExecutables(kind PackageKind, goos string) []string {
	names := []string{"fpcalc"}
	if kind == PackageFFmpeg {
		names = []string{"ffmpeg", "ffprobe"}
	}
	for i := range names {
		names[i] = executableName(names[i], goos)
	}
	return names
}
func PreflightTargets(root string, kind PackageKind, version, goos string, known map[string]struct{}) (Preflight, error) {
	relative, err := ManagedRelativePath(kind, version)
	if err != nil {
		return Preflight{}, err
	}
	result := Preflight{}
	for _, name := range ExpectedExecutables(kind, goos) {
		target := filepath.Join(root, relative, name)
		result.Targets = append(result.Targets, target)
		if _, err := os.Lstat(target); err == nil {
			if _, managed := known[target]; !managed {
				result.Conflicts = append(result.Conflicts, target)
			}
		} else if !os.IsNotExist(err) {
			return Preflight{}, fmt.Errorf("inspect target %s: %w", target, err)
		}
	}
	return result, nil
}

// ExtractZip and ExtractTarGzip refuse traversal and all links before writing.
func ExtractZip(archive, staging string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	for _, entry := range reader.File {
		if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive contains symlink %q", entry.Name)
		}
		if err := extractFile(staging, entry.Name, entry.FileInfo().IsDir(), entry.Mode(), func() (io.ReadCloser, error) { return entry.Open() }); err != nil {
			return err
		}
	}
	return nil
}
func ExtractTarGzip(archive, staging string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() { _ = gzipReader.Close() }()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			return fmt.Errorf("archive contains link %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return fmt.Errorf("archive contains unsupported entry %q", header.Name)
		}
		if err := extractFile(staging, header.Name, header.Typeflag == tar.TypeDir, os.FileMode(header.Mode), func() (io.ReadCloser, error) { return io.NopCloser(reader), nil }); err != nil {
			return err
		}
	}
}
func extractFile(root, name string, isDir bool, mode os.FileMode, open func() (io.ReadCloser, error)) error {
	target := filepath.Join(root, name)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(name) {
		return fmt.Errorf("archive entry escapes staging: %q", name)
	}
	if isDir {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	input, err := open()
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func VerifySHA256(file, expected string) error {
	if expected == "" {
		return nil
	}
	input, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, input); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), strings.TrimSpace(expected)) {
		return fmt.Errorf("checksum mismatch")
	}
	return nil
}

type Lifecycle struct{ runner CommandRunner }

func NewLifecycle(runner CommandRunner) *Lifecycle {
	if runner == nil {
		runner = SystemRunner{}
	}
	return &Lifecycle{runner: runner}
}

// Materialize atomically exposes a verified package from operation staging.
// overwrite is permitted only after the caller has confirmed Preflight.Conflicts.
func (l *Lifecycle) Materialize(ctx context.Context, staging, root string, kind PackageKind, version string, overwrite bool) (string, map[string]string, error) {
	relative, err := ManagedRelativePath(kind, version)
	if err != nil {
		return "", nil, err
	}
	target := filepath.Join(root, relative)
	if _, err := os.Stat(target); err == nil && !overwrite {
		return "", nil, fmt.Errorf("managed target already exists")
	}
	temp := target + ".staging"
	if err := os.RemoveAll(temp); err != nil {
		return "", nil, err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	if err := os.MkdirAll(temp, 0o755); err != nil {
		return "", nil, err
	}
	versions := map[string]string{}
	for _, name := range ExpectedExecutables(kind, runtime.GOOS) {
		source, err := findExecutable(staging, name)
		if err != nil {
			return "", nil, err
		}
		destination := filepath.Join(temp, name)
		if err := copyExecutable(source, destination); err != nil {
			return "", nil, err
		}
		output, err := l.runner.Run(ctx, destination, "--version")
		if err != nil {
			return "", nil, fmt.Errorf("verify %s: %w", name, err)
		}
		versions[name] = strings.TrimSpace(string(output))
		if versions[name] == "" || !strings.Contains(strings.ToLower(versions[name]), strings.ToLower(version)) {
			return "", nil, fmt.Errorf("%s version does not match release %q", name, version)
		}
	}
	if overwrite {
		if err := os.RemoveAll(target); err != nil {
			return "", nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", nil, err
	}
	if err := os.Rename(temp, target); err != nil {
		return "", nil, err
	}
	return relative, versions, nil
}
func findExecutable(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == name {
			if found != "" {
				return fmt.Errorf("multiple %s executables", name)
			}
			found = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("archive does not contain %s", name)
	}
	return found, nil
}
func copyExecutable(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func Activate(installations map[string]string, packageKind PackageKind, id string) error {
	if _, ok := installations[id]; !ok {
		return fmt.Errorf("installation %q is not ready", id)
	}
	installations[string(packageKind)] = id
	return nil
}
func Delete(root, relative string, active map[string]string, packageKind PackageKind, id string) error {
	if active[string(packageKind)] == id {
		return fmt.Errorf("cannot delete active installation")
	}
	if relative == "" || filepath.IsAbs(relative) || strings.HasPrefix(relative, "..") {
		return fmt.Errorf("unsafe managed path")
	}
	return os.RemoveAll(filepath.Join(root, relative))
}
