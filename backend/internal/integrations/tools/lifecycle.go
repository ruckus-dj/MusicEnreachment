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
	"strings"

	"github.com/ulikunitz/xz"
)

const (
	maxArchiveEntries       = 50000
	maxExpandedArchiveBytes = 8 << 30
	maxXZDictionaryBytes    = 256 << 20
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
		if info, err := os.Lstat(target); err == nil {
			if info.IsDir() || (!info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0) {
				return Preflight{}, fmt.Errorf("managed executable target has unsupported file type: %s", target)
			}
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
	if len(reader.File) > maxArchiveEntries {
		return fmt.Errorf("archive contains too many entries")
	}
	var expandedBytes uint64
	for _, entry := range reader.File {
		mode := entry.FileInfo().Mode()
		if mode&os.ModeSymlink != 0 || (!entry.FileInfo().IsDir() && !mode.IsRegular()) {
			return fmt.Errorf("archive contains unsupported entry %q", entry.Name)
		}
		if entry.UncompressedSize64 > maxExpandedArchiveBytes-expandedBytes {
			return fmt.Errorf("archive exceeds expanded size limit")
		}
		expandedBytes += entry.UncompressedSize64
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
	return extractTar(gzipReader, staging)
}

func ExtractTarXz(archive, staging string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	xzReader, err := (xz.ReaderConfig{DictCap: maxXZDictionaryBytes}).NewReader(file)
	if err != nil {
		return err
	}
	return extractTar(xzReader, staging)
}

func extractTar(compressed io.Reader, staging string) error {
	reader := tar.NewReader(compressed)
	entryCount := 0
	var expandedBytes int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		entryCount++
		if entryCount > maxArchiveEntries {
			return fmt.Errorf("archive contains too many entries")
		}
		if header.Size < 0 || header.Size > maxExpandedArchiveBytes-expandedBytes {
			return fmt.Errorf("archive exceeds expanded size limit")
		}
		expandedBytes += header.Size
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
	if unsafeArchiveName(name) {
		return fmt.Errorf("archive entry escapes staging: %q", name)
	}
	cleanName := filepath.Clean(name)
	if cleanName == "." && isDir {
		return nil
	}
	target := filepath.Join(root, cleanName)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(name) || cleanName == ".." {
		return fmt.Errorf("archive entry escapes staging: %q", name)
	}
	if err := rejectSymlinkAncestors(root, filepath.Dir(target)); err != nil {
		return err
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

func unsafeArchiveName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || strings.Contains(name, ":") {
		return true
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func rejectSymlinkAncestors(root, directory string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("archive path escapes staging")
	}
	current := root
	for _, part := range append([]string{""}, strings.Split(relative, string(filepath.Separator))...) {
		if part != "" && part != "." {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect staging path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive path traverses symlink")
		}
	}
	return nil
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

type MaterializeOptions struct {
	GOOS               string
	GOARCH             string
	ManagedPaths       []string
	ConfirmedConflicts []string
}

func NewLifecycle(runner CommandRunner) *Lifecycle {
	if runner == nil {
		runner = SystemRunner{}
	}
	return &Lifecycle{runner: runner}
}

func (l *Lifecycle) Materialize(ctx context.Context, staging, root string, kind PackageKind, version string, options MaterializeOptions) (string, map[string]string, error) {
	relative, err := ManagedRelativePath(kind, version)
	if err != nil {
		return "", nil, err
	}
	if !filepath.IsAbs(root) || !filepath.IsAbs(staging) || !(Platform{GOOS: options.GOOS, GOARCH: options.GOARCH}).Supported() {
		return "", nil, fmt.Errorf("materialization paths and target platform are required")
	}
	directory := filepath.Join(root, relative)
	managed := make(map[string]struct{}, len(options.ManagedPaths))
	for _, path := range options.ManagedPaths {
		managed[filepath.Clean(path)] = struct{}{}
	}
	preflight, err := PreflightTargets(root, kind, version, options.GOOS, managed)
	if err != nil {
		return "", nil, err
	}
	confirmed := make(map[string]struct{}, len(options.ConfirmedConflicts))
	for _, path := range options.ConfirmedConflicts {
		confirmed[filepath.Clean(path)] = struct{}{}
	}
	if len(confirmed) != len(preflight.Conflicts) {
		return "", nil, fmt.Errorf("overwrite confirmation does not match current conflicts")
	}
	for _, path := range preflight.Conflicts {
		if _, ok := confirmed[path]; !ok {
			return "", nil, fmt.Errorf("overwrite confirmation does not match current conflicts")
		}
	}

	stagedFiles := make(map[string]string, len(preflight.Targets))
	defer func() {
		for _, path := range stagedFiles {
			_ = os.Remove(path)
		}
	}()
	versions := map[string]string{}
	for _, name := range ExpectedExecutables(kind, options.GOOS) {
		source, err := findExecutable(staging, name)
		if err != nil {
			return "", nil, err
		}
		target := filepath.Join(directory, name)
		if err := rejectSymlinkAncestors(root, filepath.Dir(target)); err != nil {
			return "", nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", nil, err
		}
		if err := rejectSymlinkAncestors(staging, staging); err != nil {
			return "", nil, err
		}
		file, err := os.CreateTemp(filepath.Dir(target), ".melotrove-install-"+name+"-")
		if err != nil {
			return "", nil, err
		}
		tempPath := file.Name()
		if err := file.Close(); err != nil {
			_ = os.Remove(tempPath)
			return "", nil, err
		}
		if err := os.Remove(tempPath); err != nil {
			return "", nil, err
		}
		if err := copyExecutable(source, tempPath); err != nil {
			return "", nil, err
		}
		output, err := l.runner.Run(ctx, tempPath, "--version")
		if err != nil {
			return "", nil, fmt.Errorf("verify %s: %w", name, err)
		}
		versions[name] = strings.TrimSpace(string(output))
		if versions[name] == "" || !strings.Contains(strings.ToLower(versions[name]), strings.ToLower(strings.TrimPrefix(version, "v"))) {
			return "", nil, fmt.Errorf("%s version does not match release %q", name, version)
		}
		stagedFiles[target] = tempPath
	}

	type replacement struct {
		target string
		backup string
	}
	committed := make([]replacement, 0, len(stagedFiles))
	rollback := func() {
		for index := len(committed) - 1; index >= 0; index-- {
			item := committed[index]
			_ = os.Remove(item.target)
			if item.backup != "" {
				_ = os.Rename(item.backup, item.target)
			}
		}
	}
	targetNames := ExpectedExecutables(kind, options.GOOS)
	for _, name := range targetNames {
		target := filepath.Join(directory, name)
		tempPath := stagedFiles[target]
		item := replacement{target: target}
		if _, err := os.Lstat(target); err == nil {
			_, isManaged := managed[target]
			_, isConfirmed := confirmed[target]
			if !isManaged && !isConfirmed {
				rollback()
				return "", nil, fmt.Errorf("materialization target changed after preflight")
			}
			backupFile, err := os.CreateTemp(filepath.Dir(target), ".melotrove-backup-")
			if err != nil {
				rollback()
				return "", nil, err
			}
			item.backup = backupFile.Name()
			if err := backupFile.Close(); err != nil {
				_ = os.Remove(item.backup)
				rollback()
				return "", nil, err
			}
			if err := os.Remove(item.backup); err != nil {
				rollback()
				return "", nil, err
			}
			if err := os.Rename(target, item.backup); err != nil {
				rollback()
				return "", nil, err
			}
		} else if os.IsNotExist(err) {
			if _, isConfirmed := confirmed[target]; isConfirmed {
				rollback()
				return "", nil, fmt.Errorf("overwrite confirmation is stale")
			}
		} else {
			rollback()
			return "", nil, err
		}
		if err := os.Rename(tempPath, target); err != nil {
			if item.backup != "" {
				_ = os.Rename(item.backup, target)
			}
			rollback()
			return "", nil, err
		}
		committed = append(committed, item)
		delete(stagedFiles, target)
	}
	for _, item := range committed {
		if item.backup != "" {
			if err := os.Remove(item.backup); err != nil {
				return "", nil, fmt.Errorf("remove replaced managed executable: %w", err)
			}
		}
	}
	return relative, versions, nil
}

func (l *Lifecycle) VerifyInstallation(ctx context.Context, root, relative string, kind PackageKind, version, goos string) (map[string]string, error) {
	expectedRelative, err := ManagedRelativePath(kind, version)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(root) || filepath.Clean(relative) != expectedRelative {
		return nil, fmt.Errorf("installation path does not match its identity")
	}
	directory := filepath.Join(root, expectedRelative)
	if err := rejectSymlinkAncestors(root, directory); err != nil {
		return nil, err
	}
	versions := make(map[string]string)
	for _, name := range ExpectedExecutables(kind, goos) {
		executable := filepath.Join(directory, name)
		info, err := os.Lstat(executable)
		if err != nil {
			return nil, fmt.Errorf("inspect managed executable %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("managed executable %s is not a regular file", name)
		}
		output, err := l.runner.Run(ctx, executable, "--version")
		if err != nil {
			return nil, fmt.Errorf("verify managed executable %s: %w", name, err)
		}
		verified := strings.TrimSpace(string(output))
		if verified == "" || !strings.Contains(strings.ToLower(verified), strings.ToLower(strings.TrimPrefix(version, "v"))) {
			return nil, fmt.Errorf("managed executable %s does not match release %q", name, version)
		}
		versions[name] = verified
	}
	return versions, nil
}

func findExecutable(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == name && entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive executable %s is a symlink", name)
		}
		if !entry.IsDir() && entry.Name() == name {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("archive executable %s is not a regular file", name)
			}
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
func Delete(root, relative, goos string, active map[string]string, packageKind PackageKind, id string) error {
	if active[string(packageKind)] == id {
		return fmt.Errorf("cannot delete active installation")
	}
	if packageKind != PackageFFmpeg && packageKind != PackageFPCalc {
		return fmt.Errorf("unsupported package %q", packageKind)
	}
	cleanRelative := filepath.Clean(relative)
	if relative == "" || filepath.IsAbs(relative) || cleanRelative == "." || cleanRelative == ".." || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe managed path")
	}
	expectedRelative, err := ManagedRelativePath(packageKind, filepath.Base(cleanRelative))
	if err != nil || cleanRelative != expectedRelative {
		return fmt.Errorf("unsafe managed path")
	}
	directory := filepath.Join(root, cleanRelative)
	rel, err := filepath.Rel(root, directory)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe managed path")
	}
	if err := rejectSymlinkAncestors(root, directory); err != nil {
		return err
	}
	type backup struct {
		target string
		path   string
	}
	backups := make([]backup, 0, len(ExpectedExecutables(packageKind, goos)))
	rollback := func() {
		for index := len(backups) - 1; index >= 0; index-- {
			_ = os.Rename(backups[index].path, backups[index].target)
		}
	}
	for _, name := range ExpectedExecutables(packageKind, goos) {
		target := filepath.Join(directory, name)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			rollback()
			return fmt.Errorf("inspect managed executable %s: %w", name, err)
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			rollback()
			return fmt.Errorf("managed executable %s has unsupported file type", name)
		}
		file, err := os.CreateTemp(directory, ".melotrove-delete-")
		if err != nil {
			rollback()
			return err
		}
		backupPath := file.Name()
		if err := file.Close(); err != nil {
			_ = os.Remove(backupPath)
			rollback()
			return err
		}
		if err := os.Remove(backupPath); err != nil {
			rollback()
			return err
		}
		if err := os.Rename(target, backupPath); err != nil {
			rollback()
			return fmt.Errorf("stage managed executable removal %s: %w", name, err)
		}
		backups = append(backups, backup{target: target, path: backupPath})
	}
	for _, item := range backups {
		if err := os.Remove(item.path); err != nil {
			return fmt.Errorf("remove managed executable: %w", err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect installation directory: %w", err)
	}
	if err == nil && len(entries) == 0 {
		if err := os.Remove(directory); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove empty installation directory: %w", err)
		}
	}
	return nil
}
