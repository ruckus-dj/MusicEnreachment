package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func resetFilesystemTestTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestResetFilesystemPrepareAndRollbackOnlyOwnedEmptyDirectories(t *testing.T) {
	root := resetFilesystemTestTempDir(t)
	fs := NewResetFilesystem()
	path := filepath.Join(root, "analysis")
	var recorded []string
	if err := fs.Prepare(context.Background(), root, []string{path}, func(path string) error {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("journal intent must precede mkdir: %v", err)
		}
		recorded = append(recorded, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0] != path {
		t.Fatalf("unexpected journal paths: %v", recorded)
	}
	if err := fs.Rollback(context.Background(), recorded); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("owned empty directory remains: %v", err)
	}
}

func TestResetFilesystemLeavesPreexistingAndPopulatedDirectories(t *testing.T) {
	root := resetFilesystemTestTempDir(t)
	preexisting := filepath.Join(root, "publication")
	if err := os.Mkdir(preexisting, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(preexisting, "keep")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := NewResetFilesystem()
	called := false
	if err := fs.Prepare(context.Background(), root, []string{preexisting}, func(string) error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("pre-existing directory was journaled")
	}
	if err := fs.Rollback(context.Background(), []string{preexisting}); err == nil {
		t.Fatal("expected rollback to fail without proven ownership")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("pre-existing data was changed: %v", err)
	}

	owned := filepath.Join(root, "checks")
	if err := fs.Prepare(context.Background(), root, []string{owned}, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owned, "foreign"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fs.Rollback(context.Background(), []string{owned}); err == nil {
		t.Fatal("expected populated owned directory to be retained")
	}
	if _, err := os.Stat(filepath.Join(owned, "foreign")); err != nil {
		t.Fatalf("populated directory was changed: %v", err)
	}
}

func TestResetFilesystemDurableManifestSurvivesNewHelper(t *testing.T) {
	root := resetFilesystemTestTempDir(t)
	created := filepath.Join(root, "output", "analysis")
	var manifest []ResetDirectoryRecord
	filesystem := NewResetFilesystem()
	if err := filesystem.PrepareDurable(context.Background(), filepath.Join(root, "output"), []string{filepath.Join(root, "output"), created}, func(record ResetDirectoryRecord) error {
		manifest = append(manifest, record)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []ResetDirectoryRecord{
		{Path: filepath.Join(root, "output"), Phase: "intent"},
		{Path: filepath.Join(root, "output"), Phase: "created"},
		{Path: created, Phase: "intent"},
		{Path: created, Phase: "created"},
	}
	if len(manifest) != len(want) {
		t.Fatalf("expected durable intent/result pairs for both created paths, got %#v", manifest)
	}
	for i := range want {
		if manifest[i].Path != want[i].Path || manifest[i].Phase != want[i].Phase {
			t.Fatalf("manifest[%d] = %#v, want %#v", i, manifest[i], want[i])
		}
	}
	// Creating analysis changes output's metadata on Unix (including its ctime
	// and link count), but must not invalidate the output directory identity.
	// A fresh helper represents process restart: rollback must rely only on the
	// serialized OS identities, not on process-local state.
	if err := NewResetFilesystem().RollbackManifest(context.Background(), manifest); err != nil {
		t.Fatalf("fresh-helper rollback could not verify the output/analysis directories: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "output")); !os.IsNotExist(err) {
		t.Fatalf("recovered output root remains: %v", err)
	}
}

func TestResetFilesystemDoesNotRemoveDirectoryAfterIdentityReplacement(t *testing.T) {
	root := resetFilesystemTestTempDir(t)
	path := filepath.Join(root, "output")
	var manifest []ResetDirectoryRecord
	if err := NewResetFilesystem().PrepareDurable(context.Background(), path, []string{path}, func(record ResetDirectoryRecord) error {
		manifest = append(manifest, record)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Rename the original aside instead of removing it so its inode stays
	// alive. Removing and immediately recreating the directory can reuse the
	// same inode (ABA), which would let the replacement inherit the recorded
	// identity and mask the swap.
	backup := filepath.Join(root, "original-output")
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	original, err := os.Lstat(backup)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(original, replacement) {
		t.Fatal("replacement directory reused the original inode; identity check cannot fail closed")
	}
	if err := NewResetFilesystem().RollbackManifest(context.Background(), manifest); err == nil {
		t.Fatal("expected replacement identity to fail closed")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("replacement directory was removed: %v", err)
	}
	if info, err := os.Stat(backup); err != nil || !info.IsDir() {
		t.Fatalf("original directory backup was not retained: %v", err)
	}
}

func TestResetFilesystemPlansMissingAncestorChainAndRejectsOtherParents(t *testing.T) {
	base := resetFilesystemTestTempDir(t)
	existing := filepath.Join(base, "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(existing, "missing", "output")
	filesystem := NewResetFilesystem()
	planned, err := filesystem.PlannedDirectories(root, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 2 || planned[0] != filepath.Join(existing, "missing") || planned[1] != root {
		t.Fatalf("planned directories = %v", planned)
	}
	var manifest []ResetDirectoryRecord
	if err := filesystem.PrepareDurable(context.Background(), root, []string{root}, func(record ResetDirectoryRecord) error {
		manifest = append(manifest, record)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.RollbackManifestForRoot(context.Background(), root, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(existing, "missing")); !os.IsNotExist(err) {
		t.Fatalf("planned ancestor remains: %v", err)
	}
	if err := filesystem.RollbackManifestForRoot(context.Background(), root, []ResetDirectoryRecord{{Path: filepath.Join(base, "unrelated"), Phase: "created", Identity: "foreign"}}); err == nil {
		t.Fatal("manifest with an unrelated parent was accepted")
	}
}
