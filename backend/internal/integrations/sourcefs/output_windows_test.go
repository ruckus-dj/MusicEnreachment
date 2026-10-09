//go:build windows

package sourcefs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsOutputAdapterCreateExclusiveReportsExistingFile(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	const name = "foreign-output.bin"
	want := []byte("foreign file must remain unchanged")
	path := filepath.Join(rootPath, name)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := NewOutputOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := root.CreateExclusive(ctx, name); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("CreateExclusive error = %v, want fs.ErrExist", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("existing file changed: got %q, want %q", got, want)
	}
}
