package sourcefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeCompareComponentsUnicodeAndCleanup(t *testing.T) {
	root, path := openNamespaceProbeRoot(t)
	for _, pair := range [][2]string{
		{"музыка.flac", "музыка.flac"},
		{"café.flac", "café.flac"},
		{"музыка.flac", "音楽.flac"},
		{"café.flac", "cafe\u0301.flac"},
	} {
		got, err := NativeCompareComponents(context.Background(), root, pair[0], pair[1])
		if err != nil {
			t.Fatalf("NativeCompareComponents(%q, %q): %v", pair[0], pair[1], err)
		}
		want := nativeLookupEquivalent(t, pair[0], pair[1])
		if got != want {
			t.Errorf("NativeCompareComponents(%q, %q) = %t, native lookup = %t", pair[0], pair[1], got, want)
		}
		if pair[0] == pair[1] && !got {
			t.Errorf("same component %q was not equivalent", pair[0])
		}
		if pair[0] == "музыка.flac" && pair[1] == "音楽.flac" && got {
			t.Errorf("unrelated Unicode components unexpectedly equivalent")
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("probe left scratch entries behind: %v", entries)
	}
}

func TestNativeCompareComponentsInKeepsScratchInTechnicalParent(t *testing.T) {
	root, rootPath := openNamespaceProbeRoot(t)
	technical, err := root.CreateDirExclusive(context.Background(), "analysis")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = technical.Close() }()

	if _, err := NativeCompareComponentsIn(context.Background(), root, technical, "release.flac", "release.flac"); err != nil {
		t.Fatalf("NativeCompareComponentsIn: %v", err)
	}
	rootEntries, err := os.ReadDir(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(rootEntries) != 1 || rootEntries[0].Name() != "analysis" || !rootEntries[0].IsDir() {
		t.Fatalf("probe created entries outside technical area: %v", rootEntries)
	}
	technicalPath := filepath.Join(rootPath, "analysis")
	technicalEntries, err := os.ReadDir(technicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(technicalEntries) != 0 {
		t.Fatalf("probe left scratch entries in technical area: %v", technicalEntries)
	}
}

func nativeLookupEquivalent(t *testing.T, a, b string) bool {
	t.Helper()
	root := t.TempDir()
	first, err := os.OpenFile(filepath.Join(root, a), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("create native lookup candidate %q: %v", a, err)
	}
	defer func() { _ = first.Close() }()
	second, err := os.Open(filepath.Join(root, b))
	if err == nil {
		defer func() { _ = second.Close() }()
		firstInfo, firstErr := first.Stat()
		secondInfo, secondErr := second.Stat()
		if firstErr != nil || secondErr != nil {
			t.Fatalf("stat native lookup candidates: %v / %v", firstErr, secondErr)
		}
		return os.SameFile(firstInfo, secondInfo)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native lookup %q: %v", b, err)
	}
	second, err = os.OpenFile(filepath.Join(root, b), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("create native lookup candidate %q: %v", b, err)
	}
	defer func() { _ = second.Close() }()
	firstInfo, firstErr := first.Stat()
	secondInfo, secondErr := second.Stat()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("stat native lookup candidates: %v / %v", firstErr, secondErr)
	}
	return os.SameFile(firstInfo, secondInfo)
}

func TestNativeCompareComponentsInvalidNames(t *testing.T) {
	root, _ := openNamespaceProbeRoot(t)
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a:b", "a\x00b", string([]byte{0xff})} {
		if _, err := NativeCompareComponents(context.Background(), root, name, "ok"); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("invalid component %q error = %v", name, err)
		}
	}
}

func TestNativeCompareComponentsCanceledContextCleansScratch(t *testing.T) {
	root, path := openNamespaceProbeRoot(t)
	foreign := filepath.Join(path, "foreign")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NativeCompareComponents(ctx, root, "один", "два"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled probe error = %v", err)
	}
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "keep" {
		t.Fatalf("foreign entry changed: data=%q err=%v", data, err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "foreign" {
		t.Fatalf("unexpected root entries after canceled probe: %v", entries)
	}
}

func TestNativeCompareComponentsCancellationAndCreateFailureCleanup(t *testing.T) {
	tests := []struct {
		name              string
		cancelAfterDir    bool
		cancelAfterCreate int
		failCreateAt      int
		wantCanceled      bool
		wantCreateFailure bool
	}{
		{name: "after scratch creation", cancelAfterDir: true, wantCanceled: true},
		{name: "after first file creation", cancelAfterCreate: 1, wantCanceled: true},
		{name: "after second file creation", cancelAfterCreate: 2, wantCanceled: true},
		{name: "second file creation failure", failCreateAt: 2, wantCreateFailure: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, path := openNamespaceProbeRoot(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var injected = errors.New("injected second create failure")
			hooks := &namespaceProbeHooks{
				cancel:            cancel,
				cancelAfterDir:    test.cancelAfterDir,
				cancelAfterCreate: test.cancelAfterCreate,
				failCreateAt:      test.failCreateAt,
				injectedCreateErr: injected,
			}
			wrapped := &namespaceProbeOutput{OutputDirectory: root, hooks: hooks}
			_, err := NativeCompareComponents(ctx, wrapped, "A.flac", "Б.flac")
			switch {
			case test.wantCanceled && !errors.Is(err, context.Canceled):
				t.Fatalf("expected cancellation, got %v", err)
			case test.wantCreateFailure && !errors.Is(err, injected):
				t.Fatalf("expected injected create error, got %v", err)
			case !test.wantCanceled && !test.wantCreateFailure && err != nil:
				t.Fatalf("unexpected error: %v", err)
			}
			entries, readErr := os.ReadDir(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("owned scratch entries remain: %v (probe error %v)", entries, err)
			}
		})
	}
}

func TestNativeCompareComponentsCleanupPreservesForeignScratchEntry(t *testing.T) {
	root, path := openNamespaceProbeRoot(t)
	hooks := &namespaceProbeHooks{rootPath: path, addForeignAfterFirst: true}
	wrapped := &namespaceProbeOutput{OutputDirectory: root, hooks: hooks}
	if _, err := NativeCompareComponents(context.Background(), wrapped, "A.flac", "Б.flac"); err == nil {
		t.Fatal("expected cleanup error for nonempty owned scratch directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("expected retained scratch directory, got %v", entries)
	}
	foreign := filepath.Join(path, entries[0].Name(), "foreign")
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "keep" {
		t.Fatalf("foreign scratch entry changed: data=%q err=%v", data, err)
	}
}

type namespaceProbeHooks struct {
	cancel               context.CancelFunc
	cancelAfterDir       bool
	cancelAfterCreate    int
	failCreateAt         int
	injectedCreateErr    error
	createCount          int
	rootPath             string
	scratchPath          string
	addForeignAfterFirst bool
}

type namespaceProbeOutput struct {
	OutputDirectory
	hooks *namespaceProbeHooks
}

func (d *namespaceProbeOutput) namespaceScopeDirectory() OutputDirectory {
	return unwrapNamespaceProbeOutput(d.OutputDirectory)
}

func (d *namespaceProbeOutput) CreateDirExclusive(ctx context.Context, name string) (OutputDirectory, error) {
	child, err := d.OutputDirectory.CreateDirExclusive(ctx, name)
	if err == nil && d.hooks.rootPath != "" && d.hooks.scratchPath == "" {
		d.hooks.scratchPath = filepath.Join(d.hooks.rootPath, name)
	}
	if err == nil && d.hooks.cancelAfterDir {
		d.hooks.cancel()
	}
	if err != nil {
		return nil, err
	}
	return &namespaceProbeOutput{OutputDirectory: child, hooks: d.hooks}, nil
}

func (d *namespaceProbeOutput) CreateExclusive(ctx context.Context, name string) (OutputFile, error) {
	d.hooks.createCount++
	if d.hooks.failCreateAt == d.hooks.createCount {
		return nil, d.hooks.injectedCreateErr
	}
	file, err := d.OutputDirectory.CreateExclusive(ctx, name)
	if err == nil && d.hooks.addForeignAfterFirst && d.hooks.createCount == 1 {
		if writeErr := os.WriteFile(filepath.Join(d.hooks.scratchPath, "foreign"), []byte("keep"), 0o600); writeErr != nil {
			_ = file.Close()
			return nil, writeErr
		}
	}
	if err == nil && d.hooks.cancelAfterCreate == d.hooks.createCount {
		d.hooks.cancel()
	}
	return file, err
}

func (d *namespaceProbeOutput) Identity(ctx context.Context) (DirectoryIdentity, error) {
	return d.OutputDirectory.(DirectoryIdentifier).Identity(ctx)
}

func (d *namespaceProbeOutput) RemoveOwnedRegular(ctx context.Context, name string, parent DirectoryIdentity, expected os.FileInfo) error {
	return d.OutputDirectory.(OutputCleanup).RemoveOwnedRegular(ctx, name, parent, expected)
}

func (d *namespaceProbeOutput) RemoveEmptyChildDir(ctx context.Context, name string, parent, child DirectoryIdentity) error {
	return d.OutputDirectory.(OutputCleanup).RemoveEmptyChildDir(ctx, name, parent, child)
}

func (d *namespaceProbeOutput) OpenExistingRegular(ctx context.Context, name string) (RegularFile, error) {
	mutation, ok := d.OutputDirectory.(OutputMutation)
	if !ok {
		return nil, ErrUnsupported
	}
	return mutation.OpenExistingRegular(ctx, name)
}

func (d *namespaceProbeOutput) RenameNoReplace(ctx context.Context, sourceParent OutputDirectory, sourceName string, targetParent OutputDirectory, targetName string) error {
	mutation, ok := d.OutputDirectory.(OutputMutation)
	if !ok {
		return ErrUnsupported
	}
	return mutation.RenameNoReplace(ctx, unwrapNamespaceProbeOutput(sourceParent), sourceName, unwrapNamespaceProbeOutput(targetParent), targetName)
}

// unwrapNamespaceProbeOutput returns the native capability behind a probe
// wrapper so native mutation primitives, which type-assert concrete output
// directories, receive the underlying implementation rather than this test
// double.
func unwrapNamespaceProbeOutput(directory OutputDirectory) OutputDirectory {
	if wrapped, ok := directory.(*namespaceProbeOutput); ok {
		return wrapped.OutputDirectory
	}
	return directory
}

func openNamespaceProbeRoot(t *testing.T) (OutputDirectory, string) {
	t.Helper()
	path := t.TempDir()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	path = canonical
	root, err := NewOutputOpener().OpenRoot(context.Background(), path)
	if errors.Is(err, ErrUnsupported) {
		t.Skipf("native output filesystem capabilities unavailable: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close output root: %v", err)
		}
	})
	return root, path
}
