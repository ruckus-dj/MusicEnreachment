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
	prepareErr       error
	calls            int
	journal          persistence.OutputResetJournal
	request          persistence.OutputResetRequest
	manifest         []persistence.OutputResetDirectory
	semanticsRequest persistence.OutputSemanticsRefreshRequest
}

func (r *outputResetRepositoryStub) RunOutputResetRequest(ctx context.Context, request persistence.OutputResetRequest) (uuid.UUID, error) {
	r.calls++
	r.request = request
	if r.prepareErr != nil {
		return uuid.Nil, r.prepareErr
	}
	var paths []string
	err := request.Prepare(ctx, func(directory persistence.OutputResetDirectory) error {
		r.manifest = append(r.manifest, directory)
		if directory.Phase == "intent" {
			paths = append(paths, directory.Path)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	r.journal = persistence.OutputResetJournal{State: "committed", OldRoot: request.ExpectedOldRoot, NewRoot: request.NewRoot, CreatedDirectories: paths}
	return uuid.New(), request.Finish(ctx, r.journal)
}

func (r *outputResetRepositoryStub) RunOutputSemanticsRefresh(ctx context.Context, request persistence.OutputSemanticsRefreshRequest) error {
	r.semanticsRequest = request
	_, err := request.Refresh(ctx)
	return err
}

type outputResetSettingsStore struct {
	settings.Store
	values map[string]string
	err    error
}

func (s outputResetSettingsStore) Get(_ context.Context, name string) (string, bool, error) {
	value, found := s.values[name]
	return value, found, s.err
}

func (r *outputResetRepositoryStub) RecoverOutputReset(ctx context.Context, callback func(context.Context, persistence.OutputResetJournal) error) error {
	return callback(ctx, r.journal)
}

func newOutputResetTestService(repository outputResetRepository, logicalAreas []string) *OutputReset {
	service := NewOutputReset(repository, settings.NewResetFilesystem(), logicalAreas)
	service.registry = settings.New(outputResetSettingsStore{values: map[string]string{}}, nil)
	return service
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
	service := newOutputResetTestService(repository, []string{"analysis"})
	if _, err := service.ResetRuntime(context.Background(), "", oldRoot, newRoot, settings.RuntimeUpdate{}); err == nil {
		t.Fatal("expected reset error")
	}
	if _, err := os.Lstat(filepath.Join(newRoot, "analysis")); !os.IsNotExist(err) {
		t.Fatalf("filesystem mutated before preparation callback: %v", err)
	}
	if repository.calls != 1 {
		t.Fatalf("repository called %d times", repository.calls)
	}
	if repository.request.ExpectedToolsRoot != "" {
		t.Fatalf("request expected tools root without an explicit expectation = %q, want empty", repository.request.ExpectedToolsRoot)
	}
}

func TestOutputResetRunsPreparedDirectoriesThroughJournalCallback(t *testing.T) {
	parent := outputResetTestTempDir(t)
	oldRoot := filepath.Join(parent, "old")
	newRoot := filepath.Join(parent, "missing", "new")
	if err := os.Mkdir(oldRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	repository := new(outputResetRepositoryStub)
	service := newOutputResetTestService(repository, []string{"analysis", "media"})
	if _, err := service.ResetRuntime(context.Background(), "", oldRoot, newRoot, settings.RuntimeUpdate{}); err != nil {
		t.Fatal(err)
	}
	ancestor := filepath.Join(parent, "missing")
	wantPaths := []string{ancestor, newRoot, filepath.Join(newRoot, "analysis"), filepath.Join(newRoot, "media")}
	if len(repository.request.AllowedDirectories) != len(wantPaths) {
		t.Fatalf("planned allowed directories = %v, want %v", repository.request.AllowedDirectories, wantPaths)
	}
	allowed := make(map[string]bool, len(repository.request.AllowedDirectories))
	for _, path := range repository.request.AllowedDirectories {
		allowed[path] = true
	}
	for _, path := range wantPaths {
		if !allowed[path] {
			t.Errorf("planned allowed directories %v omit %q", repository.request.AllowedDirectories, path)
		}
	}
	if len(repository.journal.CreatedDirectories) != len(wantPaths) {
		t.Fatalf("journaled paths = %v, want all planned creations %v", repository.journal.CreatedDirectories, wantPaths)
	}
	if len(repository.manifest) != 2*len(wantPaths) {
		t.Fatalf("durable manifest = %#v, want intent/created pair for each path", repository.manifest)
	}
	for index, path := range wantPaths {
		intent, created := repository.manifest[index*2], repository.manifest[index*2+1]
		if intent.Path != path || intent.Phase != "intent" || created.Path != path || created.Phase != "created" || created.Identity == "" {
			t.Errorf("manifest records for %q = %#v, %#v; want durable intent and created identity", path, intent, created)
		}
	}
	for _, area := range []string{"analysis", "media"} {
		if info, err := os.Stat(filepath.Join(newRoot, area)); err != nil || !info.IsDir() {
			t.Fatalf("logical directory %q not prepared: %v", area, err)
		}
	}
}

func TestOutputResetPassesExpectedToolsRootToDurableRequest(t *testing.T) {
	parent := outputResetTestTempDir(t)
	oldRoot := filepath.Join(parent, "old")
	newRoot := filepath.Join(parent, "new")
	toolsRoot := filepath.Join(parent, "tools")
	if err := os.Mkdir(oldRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	repository := new(outputResetRepositoryStub)
	service := newOutputResetTestService(repository, nil)
	if _, err := service.ResetRuntime(context.Background(), toolsRoot, oldRoot, newRoot, settings.RuntimeUpdate{}); err != nil {
		t.Fatal(err)
	}
	if repository.request.ExpectedToolsRoot != toolsRoot {
		t.Fatalf("request expected tools root = %q, want captured expectation %q", repository.request.ExpectedToolsRoot, toolsRoot)
	}
}

func TestSetupRuntimeUpdateStopsBeforeOutputResetWhenToolsRootLookupFails(t *testing.T) {
	parent := outputResetTestTempDir(t)
	lookupErr := errors.New("tools root lookup failed")
	store := outputResetSettingsStore{err: lookupErr}
	registry := settings.New(store, nil)
	repository := new(outputResetRepositoryStub)
	service := NewSetup(store, registry, settings.PlatformState{}, nil, nil).WithOutputReset(newOutputResetTestService(repository, []string{"analysis"}))
	candidate := filepath.Join(parent, "new")
	if err := service.SaveRuntimeUpdate(context.Background(), RuntimeUpdateRequest{OutputDirectory: &candidate}); !errors.Is(err, lookupErr) {
		t.Fatalf("runtime update error = %v, want tools-root lookup failure %v", err, lookupErr)
	}
	if repository.calls != 0 {
		t.Fatalf("output reset repository called %d times after tools-root lookup failed", repository.calls)
	}
}

func TestOutputResetSameCanonicalRootIsNoop(t *testing.T) {
	root := outputResetTestTempDir(t)
	if err := os.WriteFile(filepath.Join(root, "keep"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository := new(outputResetRepositoryStub)
	service := newOutputResetTestService(repository, []string{"analysis"})
	if token, err := service.ResetRuntime(context.Background(), "", root, root, settings.RuntimeUpdate{}); err != nil || token != uuid.Nil {
		t.Fatalf("same-root reset = (%s, %v), want no-op", token, err)
	}
	if repository.calls != 0 {
		t.Fatalf("same-root reset called repository %d times", repository.calls)
	}
	if repository.semanticsRequest.ExpectedOutputRoot != root {
		t.Fatalf("semantics refresh root = %q, want %q", repository.semanticsRequest.ExpectedOutputRoot, root)
	}
	if _, err := os.Stat(filepath.Join(root, "keep")); err != nil {
		t.Fatalf("populated output root changed: %v", err)
	}
}
