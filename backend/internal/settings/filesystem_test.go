package settings_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
