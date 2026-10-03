package settings_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestPathsOverlapWhenExistingDirectoryHasDifferentCase(t *testing.T) {
	root := t.TempDir()
	semantics, err := settings.ProbeFilesystemSemantics(root)
	if err != nil {
		t.Fatal(err)
	}
	if semantics.CaseSensitive {
		t.Skip("requires a case-insensitive filesystem")
	}
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		filepath.Join(root, "TOOLS"),
		filepath.Join(root, "TOOLS", "output"),
	} {
		if !settings.PathsOverlap(tools, output) {
			t.Errorf("paths %q and %q alias on this filesystem", tools, output)
		}
	}
	store := newMemoryStore()
	if err := settings.New(store, nil).SetOutputDirectory(context.Background(), filepath.Join(root, "TOOLS"), tools); err == nil {
		t.Fatal("output directory accepted a case-aliased tools directory")
	}
}

func TestSourcePathsOverlapUsesReadOnlyContainmentAndIdentity(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	nestedManaged := filepath.Join(source, "managed", "absent")
	nestedSource := filepath.Join(source, "existing")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(nestedSource, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		first  string
		second string
		want   bool
	}{
		{name: "managed absent beneath source", first: source, second: nestedManaged, want: true},
		{name: "source beneath managed", first: nestedSource, second: source, want: true},
		{name: "disjoint", first: source, second: filepath.Join(root, "other"), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := settings.SourcePathsOverlap(test.first, test.second)
			if err != nil || got != test.want {
				t.Fatalf("SourcePathsOverlap(%q, %q) = %t, %v; want %t", test.first, test.second, got, err, test.want)
			}
		})
	}
}

func TestSourcePathsOverlapAllowsDistinctCaseDirectories(t *testing.T) {
	root := t.TempDir()
	upper, lower := filepath.Join(root, "Music"), filepath.Join(root, "music")
	if err := os.Mkdir(upper, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lower, 0o755); err != nil {
		if os.IsExist(err) {
			t.Skip("requires a case-sensitive filesystem")
		}
		t.Fatal(err)
	}
	upperInfo, err := os.Stat(upper)
	if err != nil {
		t.Fatal(err)
	}
	lowerInfo, err := os.Stat(lower)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(upperInfo, lowerInfo) {
		t.Skip("requires a case-sensitive filesystem")
	}
	got, err := settings.SourcePathsOverlap(upper, lower)
	if err != nil || got {
		t.Fatalf("distinct case paths overlap = %t, %v; want false", got, err)
	}
}

func TestSourcePathsOverlapAllowsAbsentCaseDistinctManagedSuffixWithoutProbes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "Music")
	managed := filepath.Join(root, "music", "absent", "output")
	if err := os.Mkdir(source, 0o555); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "music")); err == nil {
		t.Skip("requires a case-sensitive filesystem")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	// Do not rely on permission bits: they do not prevent writes when this suite
	// runs with elevated privileges. Observe all semantics-probe create attempts
	// through the filesystem seam instead.
	var attempts []string
	restore := settings.SetFilesystemCreateAttemptHook(func(path string) { attempts = append(attempts, path) })
	defer restore()

	got, err := settings.SourcePathsOverlap(source, managed)
	if err != nil || got {
		t.Fatalf("SourcePathsOverlap(%q, %q) = %t, %v; want distinct paths", source, managed, got, err)
	}
	if len(attempts) != 0 {
		t.Fatalf("overlap validation attempted filesystem creates at %v", attempts)
	}
}

func TestProbeFilesystemSemanticsReportsNoneWhenUnicodeNamesDiffer(t *testing.T) {
	root := t.TempDir()
	nfc := filepath.Join(root, "caf\u00e9")
	nfd := filepath.Join(root, "cafe\u0301")
	if err := os.WriteFile(nfc, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nfd); err == nil {
		t.Skip("filesystem aliases NFC and NFD names")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	semantics, err := settings.ProbeFilesystemSemantics(root)
	if err != nil {
		t.Fatal(err)
	}
	if semantics.UnicodeNormalization != "none" {
		t.Fatalf("distinct NFC/NFD names: normalization=%q, want none", semantics.UnicodeNormalization)
	}
}

func TestProbeWritableEmptyAcceptsEmptyAndRejectsExistingEntries(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if err := settings.ProbeWritableEmpty(empty); err != nil {
		t.Fatalf("empty writable directory rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(empty, "existing.mka"), []byte("publication"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := settings.ProbeWritableEmpty(empty); err == nil {
		t.Fatal("non-empty output directory accepted")
	}
}

func TestProbeDirectoryGuardSerializesOutputProbeAndCleanup(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "output")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- settings.WithProbeDirectory(output, func(string) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	started := make(chan struct{})
	probeDone := make(chan error, 1)
	go func() {
		close(started)
		_, err := settings.ProbeOutputDirectory(filepath.Join(root, ".", "output"), true)
		probeDone <- err
	}()
	<-started
	if err := os.WriteFile(filepath.Join(output, ".melotrove-semantics-user-data"), []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-guardDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-probeDone:
		if err == nil {
			t.Fatal("output probe accepted a real user entry")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("output probe remained blocked after releasing directory guard")
	}
	if err := os.Remove(filepath.Join(output, ".melotrove-semantics-user-data")); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.ProbeOutputDirectory(output, true); err != nil {
		t.Fatalf("retry after removing user entry: %v", err)
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("probe cleanup left entries: %v, %v", entries, err)
	}
}

func TestProbeOutputDirectoryKeepsSemanticsEntriesUntilProbeReturns(t *testing.T) {
	output := t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	var pause sync.Once
	restore := settings.SetProbeFilesystemSemanticsHook(func() {
		pause.Do(func() {
			close(entered)
			<-release
		})
	})
	defer restore()

	done := make(chan error, 1)
	go func() {
		_, err := settings.ProbeOutputDirectory(output, true)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("semantics probe did not reach the synchronization seam")
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("semantics probe entries are not present while paused: %v, %v", entries, err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("semantics probe did not finish after release")
	}
	entries, err = os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("semantics probe cleanup left entries: %v, %v", entries, err)
	}
	if _, err := settings.ProbeOutputDirectory(output, true); err != nil {
		t.Fatalf("retry after cleanup: %v", err)
	}
}

func TestSetOutputDirectoryRejectsNonEmptyDirectoryWithoutSaving(t *testing.T) {
	output := filepath.Join(t.TempDir(), "output")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "existing.mka"), []byte("publication"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore()
	err := settings.New(store, nil).SetOutputDirectory(context.Background(), output, "")
	if err == nil {
		t.Fatal("non-empty output directory accepted")
	}
	if _, exists := store.data[settings.OutputDirectoryKey]; exists {
		t.Fatal("rejected output directory was saved")
	}
}
