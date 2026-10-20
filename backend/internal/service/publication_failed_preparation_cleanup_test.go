package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
)

// Exercise the ownership boundary with native directories: only the exact
// empty directory created by this scope is removable after acknowledged cleanup.
func TestPublicationPreInputCleanupRequiresOriginalEmptyDirectory(t *testing.T) {
	t.Run("original empty directory", func(t *testing.T) {
		parentPath := t.TempDir()
		parentPath, err := filepath.EvalSymlinks(parentPath)
		if err != nil {
			t.Fatal(err)
		}
		parent, err := sourcefs.NewOutputOpener().OpenRoot(context.Background(), parentPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := parent.Close(); err != nil {
				t.Error(err)
			}
		}()
		name := "private"
		if err := os.Mkdir(filepath.Join(parentPath, name), 0o700); err != nil {
			t.Fatal(err)
		}
		identifier := parent.(sourcefs.DirectoryIdentifier)
		identity, err := identifier.Identity(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		directory, err := parent.(sourcefs.OutputDirectoryTraversal).OpenExistingDir(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		dirIdentity, err := directory.(sourcefs.DirectoryIdentifier).Identity(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := directory.Close(); err != nil {
			t.Fatal(err)
		}
		if err := parent.(sourcefs.OutputCleanup).RemoveEmptyChildDir(context.Background(), name, identity, dirIdentity); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(parentPath, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("directory remains: %v", err)
		}
	})

	for _, mode := range []string{"missing", "nonempty", "substituted"} {
		t.Run(mode, func(t *testing.T) {
			parentPath := t.TempDir()
			parentPath, err := filepath.EvalSymlinks(parentPath)
			if err != nil {
				t.Fatal(err)
			}
			parent, err := sourcefs.NewOutputOpener().OpenRoot(context.Background(), parentPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := parent.Close(); err != nil {
					t.Error(err)
				}
			}()
			name := "private"
			path := filepath.Join(parentPath, name)
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			identity, err := parent.(sourcefs.DirectoryIdentifier).Identity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			directory, err := parent.(sourcefs.OutputDirectoryTraversal).OpenExistingDir(context.Background(), name)
			if err != nil {
				t.Fatal(err)
			}
			dirIdentity, err := directory.(sourcefs.DirectoryIdentifier).Identity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := directory.Close(); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "nonempty":
				if err := os.WriteFile(filepath.Join(path, "intruder"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "substituted":
				if err := os.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			err = parent.(sourcefs.OutputCleanup).RemoveEmptyChildDir(context.Background(), name, identity, dirIdentity)
			if err == nil {
				t.Fatal("unsafe directory cleanup succeeded")
			}
			info, statErr := os.Stat(path)
			if mode == "missing" {
				if !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("missing directory restored: %v", statErr)
				}
			} else if statErr != nil || !info.IsDir() {
				t.Fatalf("directory not retained: %v", statErr)
			}
		})
	}
}
