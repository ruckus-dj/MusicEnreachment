//go:build linux || darwin

package sourcefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOutputRenameNoReplaceRejectsNonRegularSources(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "dangling-symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			rootPath := outputRenameRoot(t)
			root := openOutputRenameRoot(t, rootPath)
			defer func() { _ = root.Close() }()

			sourcePath := filepath.Join(rootPath, "source")
			switch kind {
			case "directory":
				if err := os.Mkdir(sourcePath, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink", "dangling-symlink":
				linkTarget := filepath.Join(rootPath, "missing-target")
				if kind == "symlink" {
					linkTarget = filepath.Join(rootPath, "existing-target")
					if err := os.WriteFile(linkTarget, []byte("target"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(linkTarget, sourcePath); err != nil {
					t.Skipf("symlink creation unavailable: %v", err)
				}
			case "fifo":
				if err := unix.Mkfifo(sourcePath, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(sourcePath)
			if err != nil {
				t.Fatal(err)
			}

			err = root.(OutputMutation).RenameNoReplace(ctx, root, "source", root, "target")
			wantErr := ErrNotRegular
			if kind == "symlink" || kind == "dangling-symlink" {
				wantErr = ErrLink
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("rename error = %v, want %v", err, wantErr)
			}
			after, err := os.Lstat(sourcePath)
			if err != nil {
				t.Fatalf("source was removed: %v", err)
			}
			if !os.SameFile(before, after) {
				t.Fatal("source identity changed")
			}
			if _, err := os.Lstat(filepath.Join(rootPath, "target")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("target exists or could not be inspected: %v", err)
			}
		})
	}
}
