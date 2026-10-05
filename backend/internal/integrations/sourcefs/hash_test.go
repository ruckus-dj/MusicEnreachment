package sourcefs

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestSHA256EmptyAndContents(t *testing.T) {
	for _, content := range []string{"", "a known source payload\n"} {
		path := filepath.Join(t.TempDir(), "source")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		handle, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		file := newRegularFile(handle)
		got, err := SHA256(context.Background(), file)
		if err != nil {
			t.Fatal(err)
		}
		want := sha256.Sum256([]byte(content))
		if got != want {
			t.Fatalf("SHA256(%q)=%x want %x", content, got, want)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSHA256HonorsContextBeforeBorrow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	handle, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	file := newRegularFile(handle)
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SHA256(ctx, file); err == nil {
		t.Fatal("expected canceled context")
	}
}

func TestSHA256ReturnsReadError(t *testing.T) {
	directory, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := directBorrowFile{file: directory}
	t.Cleanup(func() {
		if err := directory.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := SHA256(context.Background(), file); err == nil {
		t.Fatal("expected directory read error")
	}
}

func TestSHA256HonorsCancellationWhileBorrowed(t *testing.T) {
	handle, err := os.Create(filepath.Join(t.TempDir(), "source"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := handle.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	file := directBorrowFile{file: handle, cancel: cancel}
	if _, err := SHA256(ctx, file); err == nil {
		t.Fatal("expected cancellation during the borrow")
	}
}

type directBorrowFile struct {
	file   *os.File
	cancel context.CancelFunc
}

func (file directBorrowFile) Stat(context.Context) (fs.FileInfo, error) { return file.file.Stat() }
func (file directBorrowFile) Borrow(_ context.Context, callback func(*os.File) error) error {
	if file.cancel != nil {
		file.cancel()
	}
	return callback(file.file)
}
func (file directBorrowFile) Close() error { return file.file.Close() }
