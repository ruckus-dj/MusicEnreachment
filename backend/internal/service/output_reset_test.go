package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func outputResetTestTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

type outputResetRepositoryStub struct {
	prepareErr error
	calls      int
	journal    persistence.OutputResetJournal
}

func (r *outputResetRepositoryStub) RunOutputReset(
	ctx context.Context,
	oldRoot, newRoot string,
	prepare func(context.Context, func(string) error) error,
	finish func(context.Context, persistence.OutputResetJournal) error,
) (uuid.UUID, error) {
	r.calls++
	if r.prepareErr != nil {
		return uuid.Nil, r.prepareErr
	}
	var paths []string
	err := prepare(ctx, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	r.journal = persistence.OutputResetJournal{State: "committed", OldRoot: oldRoot, NewRoot: newRoot, CreatedDirectories: paths}
	return uuid.New(), finish(ctx, r.journal)
}

func (r *outputResetRepositoryStub) RecoverOutputReset(ctx context.Context, callback func(context.Context, persistence.OutputResetJournal) error) error {
	return callback(ctx, r.journal)
}

func TestOutputResetDefersFilesystemMutationUntilRepositoryPreparation(t *testing.T) {
	parent := outputResetTestTempDir(t)
	oldRoot := filepath.Join(parent, "old")
	newRoot := filepath.Join(parent, "new")
	if err := os.Mkdir(oldRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(newRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	repository := &outputResetRepositoryStub{prepareErr: errors.New("journal unavailable")}
	service := NewOutputReset(repository, settings.NewResetFilesystem(), []string{"analysis"})
	if _, err := service.Reset(context.Background(), oldRoot, newRoot); err == nil {
		t.Fatal("expected reset error")
	}
	if _, err := os.Lstat(filepath.Join(newRoot, "analysis")); !os.IsNotExist(err) {
		t.Fatalf("filesystem mutated before preparation callback: %v", err)
	}
	if repository.calls != 1 {
		t.Fatalf("repository called %d times", repository.calls)
	}
}

func TestOutputResetRunsPreparedDirectoriesThroughJournalCallback(t *testing.T) {
	parent := outputResetTestTempDir(t)
	oldRoot := filepath.Join(parent, "old")
	newRoot := filepath.Join(parent, "new")
	if err := os.Mkdir(oldRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(newRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	repository := new(outputResetRepositoryStub)
	service := NewOutputReset(repository, settings.NewResetFilesystem(), []string{"analysis", "media"})
	if _, err := service.Reset(context.Background(), oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	if len(repository.journal.CreatedDirectories) != 2 {
		t.Fatalf("journaled paths = %v", repository.journal.CreatedDirectories)
	}
	for _, area := range []string{"analysis", "media"} {
		if info, err := os.Stat(filepath.Join(newRoot, area)); err != nil || !info.IsDir() {
			t.Fatalf("logical directory %q not prepared: %v", area, err)
		}
	}
}

func TestOutputResetSameCanonicalRootIsNoop(t *testing.T) {
	root := outputResetTestTempDir(t)
	if err := os.WriteFile(filepath.Join(root, "keep"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := new(outputResetRepositoryStub)
	service := NewOutputReset(repository, settings.NewResetFilesystem(), []string{"analysis"})
	if token, err := service.Reset(context.Background(), root, root); err != nil || token != uuid.Nil {
		t.Fatalf("same-root reset = (%s, %v), want no-op", token, err)
	}
	if repository.calls != 0 {
		t.Fatalf("same-root reset called repository %d times", repository.calls)
	}
	if _, err := os.Stat(filepath.Join(root, "keep")); err != nil {
		t.Fatalf("populated output root changed: %v", err)
	}
}
