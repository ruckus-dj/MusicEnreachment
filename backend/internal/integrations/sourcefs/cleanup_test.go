package sourcefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const cleanupArtifactPath = "analysis/staging/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002/00000000-0000-0000-0000-000000000003"

// cleanupTempDir canonicalizes t.TempDir so the no-follow root walk does not
// reject platform symlinks such as macOS /var -> /private/var.
func cleanupTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCleanerRemovesOnlyRegisteredArtifact(t *testing.T) {
	root := cleanupTempDir(t)
	artifact := filepath.Join(root, filepath.FromSlash(cleanupArtifactPath))
	if err := os.MkdirAll(filepath.Dir(artifact), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewCleaner().RemoveRegistered(context.Background(), root, cleanupArtifactPath)
	if err != nil || outcome != CleanupDeleted {
		t.Fatalf("RemoveRegistered() = (%v, %v), want (%v, nil)", outcome, err, CleanupDeleted)
	}
	if _, err := os.Stat(artifact); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact stat error = %v, want not-exist", err)
	}
	if info, err := os.Stat(filepath.Dir(artifact)); err != nil || !info.IsDir() {
		t.Fatalf("artifact parent was pruned: stat = (%v, %v)", info, err)
	}
	outcome, err = NewCleaner().RemoveRegistered(context.Background(), root, cleanupArtifactPath)
	if err != nil || outcome != CleanupMissing {
		t.Fatalf("second RemoveRegistered() = (%v, %v), want (%v, nil)", outcome, err, CleanupMissing)
	}
}

func TestCleanerDistinguishesMissingStagingFromMissingOutputRoot(t *testing.T) {
	root := cleanupTempDir(t)
	outcome, err := NewCleaner().RemoveRegistered(context.Background(), root, cleanupArtifactPath)
	if err != nil || outcome != CleanupMissing {
		t.Fatalf("missing artifact = (%v, %v), want (%v, nil)", outcome, err, CleanupMissing)
	}
	_, err = NewCleaner().RemoveRegistered(context.Background(), filepath.Join(root, "missing-root"), cleanupArtifactPath)
	if err == nil {
		t.Fatal("missing output root was reported as a missing artifact")
	}
}

func TestCleanerValidatesCanonicalPathBeforeFilesystemAccess(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, path := range []string{
		"analysis/staging/work/artifact",
		"analysis/staging/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002/../artifact",
		"analysis/staging/00000000-0000-0000-0000-00000000000A/00000000-0000-0000-0000-000000000002/00000000-0000-0000-0000-000000000003",
		"/analysis/staging/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002/00000000-0000-0000-0000-000000000003",
		`analysis\staging\00000000-0000-0000-0000-000000000001\00000000-0000-0000-0000-000000000002\00000000-0000-0000-0000-000000000003`,
		"analysis/staging/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002/00000000-0000-0000-0000-000000000003/00000000-0000-0000-0000-000000000004",
		"analysis/staging/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002/00000000-0000-0000-0000-000000000003/",
	} {
		if _, err := NewCleaner().RemoveRegistered(cancelled, cleanupTempDir(t), path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("RemoveRegistered(%q) error = %v, want ErrInvalidPath", path, err)
		}
	}
	if _, err := NewCleaner().RemoveRegistered(cancelled, cleanupTempDir(t), cleanupArtifactPath); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cleanup error = %v, want context.Canceled", err)
	}
}

func TestCleanerMissingStagingCreatesNothing(t *testing.T) {
	root := cleanupTempDir(t)
	outcome, err := NewCleaner().RemoveRegistered(context.Background(), root, cleanupArtifactPath)
	if err != nil || outcome != CleanupMissing {
		t.Fatalf("RemoveRegistered() = (%v, %v), want (%v, nil)", outcome, err, CleanupMissing)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cleanup of missing staging created %d entries: %v", len(entries), entries)
	}
}

func TestCleanerRemovesArtifactAndPreservesForeignEntries(t *testing.T) {
	root := cleanupTempDir(t)
	artifact := filepath.Join(root, filepath.FromSlash(cleanupArtifactPath))
	if err := os.MkdirAll(filepath.Dir(artifact), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(filepath.Dir(artifact), "sibling")
	if err := os.WriteFile(sibling, []byte("sibling"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreignDir := filepath.Join(root, "analysis", "staging", "foreign")
	if err := os.Mkdir(foreignDir, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(foreignDir, "keep")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	outcome, err := NewCleaner().RemoveRegistered(context.Background(), root, cleanupArtifactPath)
	if err != nil || outcome != CleanupDeleted {
		t.Fatalf("RemoveRegistered() = (%v, %v), want (%v, nil)", outcome, err, CleanupDeleted)
	}
	if _, err := os.Stat(artifact); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact after cleanup = %v, want not-exist", err)
	}
	if got, err := os.ReadFile(sibling); err != nil || string(got) != "sibling" {
		t.Fatalf("sibling after cleanup = %q, %v; want untouched", got, err)
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "keep" {
		t.Fatalf("foreign file after cleanup = %q, %v; want untouched", got, err)
	}
}
