package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type obsoleteCleanupSettingsFake struct {
	path string
	err  error
}

func (s obsoleteCleanupSettingsFake) ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error) {
	return settings.RuntimeSettings{OutputDirectory: s.path}, s.err
}

type obsoleteCleanupRepoFake struct {
	operation     *persistence.Operation
	authority     *persistence.ObsoletePublicationOutputCleanup
	getErr        error
	executeErr    error
	callbackErr   error
	callbackCalls int
	state         string
}

func (r *obsoleteCleanupRepoFake) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	op := *r.operation
	if r.state != "" {
		op.State = r.state
	}
	return &op, nil
}
func (r *obsoleteCleanupRepoFake) ReadObsoletePublicationOutputCleanup(context.Context, uuid.UUID) (*persistence.ObsoletePublicationOutputCleanup, error) {
	return r.authority, nil
}
func (r *obsoleteCleanupRepoFake) ExecuteObsoletePublicationOutputCleanup(ctx context.Context, _ uuid.UUID, _ int, _ int64, fn func(context.Context, persistence.ObsoletePublicationOutputCleanup) error) (bool, error) {
	if r.executeErr != nil {
		return false, r.executeErr
	}
	r.callbackCalls++
	err := fn(ctx, *r.authority)
	if r.callbackErr != nil {
		return false, r.callbackErr
	}
	return err == nil, err
}

func obsoleteCleanupFixture(t *testing.T, relative string, contents []byte) (string, persistence.ObsoletePublicationOutputCleanup) {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	opener := sourcefs.NewOutputOpener()
	dir, err := opener.OpenRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := dir.Close(); err != nil {
			t.Error(err)
		}
	})
	rootID, err := dir.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parent := dir
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	parts = parts[:len(parts)-1]
	for _, part := range parts {
		tr := parent.(sourcefs.OutputDirectoryTraversal)
		child, e := tr.OpenExistingDir(context.Background(), part)
		if e != nil {
			t.Fatal(e)
		}
		parent = child
		t.Cleanup(func() {
			if err := child.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	parentID, err := parent.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rootJSON, _ := json.Marshal(persistence.PublicationDirectoryIdentity{Platform: rootID.Platform, Volume: rootID.Volume, FileID: rootID.FileID})
	parentJSON, _ := json.Marshal(persistence.PublicationDirectoryIdentity{Platform: parentID.Platform, Volume: parentID.Volume, FileID: parentID.FileID})
	hash := sha256.Sum256(contents)
	return root, persistence.ObsoletePublicationOutputCleanup{RelativePath: relative, NativePath: path, OutputRootIdentity: rootJSON, ParentIdentity: parentJSON, FileSize: info.Size(), ModifiedAt: info.ModTime(), SHA256: hash[:]}
}

func TestObsoletePublicationOutputCleanupRemovesOnlyExactOwnedOutput(t *testing.T) {
	root, authority := obsoleteCleanupFixture(t, "Artist/Album/track.mp3", []byte("exact output"))
	id := uuid.New()
	repo := &obsoleteCleanupRepoFake{operation: &persistence.Operation{ID: id, Kind: persistence.ObsoletePublicationOutputCleanupOperationKind, State: "queued", Attempt: 1}, authority: &authority}
	runner := NewObsoletePublicationOutputCleanupRunner(repo, obsoleteCleanupSettingsFake{path: root}, publication.NewCoordinator(), sourcefs.NewOutputOpener())
	if err := runner.Execute(context.Background(), id, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(authority.NativePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned output remains: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("root removed: %v", err)
	}
}

func TestObsoletePublicationOutputCleanupMissingLeafSucceedsButChangedHashDoesNot(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		root, a := obsoleteCleanupFixture(t, "album/track.mp3", []byte("before"))
		if err := os.Remove(a.NativePath); err != nil {
			t.Fatal(err)
		}
		r := newCleanupExecuteRunner(t, root, a)
		if err := r.Execute(context.Background(), uuid.MustParse("00000000-0000-4000-8000-000000000001"), 1); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("same-size different-content", func(t *testing.T) {
		root, a := obsoleteCleanupFixture(t, "album/track.mp3", []byte("before"))
		if err := os.WriteFile(a.NativePath, []byte("change"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(a.NativePath, a.ModifiedAt, a.ModifiedAt); err != nil {
			t.Fatal(err)
		}
		r := newCleanupExecuteRunner(t, root, a)
		if err := r.Execute(context.Background(), uuid.MustParse("00000000-0000-4000-8000-000000000001"), 1); err == nil {
			t.Fatal("expected changed hash rejection")
		}
		if _, err := os.Stat(a.NativePath); err != nil {
			t.Fatal("changed file was removed")
		}
	})
}

func TestObsoletePublicationOutputCleanupRejectsFrozenWrongParent(t *testing.T) {
	root, a := obsoleteCleanupFixture(t, "Artist/Album/track.mp3", []byte("same output"))
	var identity persistence.PublicationDirectoryIdentity
	if err := json.Unmarshal(a.ParentIdentity, &identity); err != nil {
		t.Fatal(err)
	}
	identity.FileID = "wrong-parent"
	a.ParentIdentity, _ = json.Marshal(identity)
	runner := newCleanupExecuteRunner(t, root, a)
	err := runner.Execute(context.Background(), uuid.MustParse("00000000-0000-4000-8000-000000000001"), 1)
	if err == nil {
		t.Fatal("expected frozen parent identity rejection")
	}
	if _, err := os.Stat(a.NativePath); err != nil {
		t.Fatalf("output was removed: %v", err)
	}
}

func newCleanupExecuteRunner(t *testing.T, root string, authority persistence.ObsoletePublicationOutputCleanup) *ObsoletePublicationOutputCleanupRunner {
	t.Helper()
	id := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	repo := &obsoleteCleanupRepoFake{operation: &persistence.Operation{ID: id, Kind: persistence.ObsoletePublicationOutputCleanupOperationKind, State: "queued", Attempt: 1}, authority: &authority}
	runner := NewObsoletePublicationOutputCleanupRunner(repo, obsoleteCleanupSettingsFake{path: root}, publication.NewCoordinator(), sourcefs.NewOutputOpener())
	return runner
}

func TestObsoletePublicationOutputCleanupExecuteSkipsCompletedAndPropagatesDatabaseError(t *testing.T) {
	id := uuid.New()
	repo := &obsoleteCleanupRepoFake{operation: &persistence.Operation{ID: id, Kind: persistence.ObsoletePublicationOutputCleanupOperationKind, State: "succeeded"}}
	runner := NewObsoletePublicationOutputCleanupRunner(repo, obsoleteCleanupSettingsFake{}, publication.NewCoordinator(), sourcefs.NewOutputOpener())
	if err := runner.Execute(context.Background(), id, 1); err != nil {
		t.Fatal(err)
	}
	if repo.callbackCalls != 0 {
		t.Fatalf("completed operation invoked cleanup %d times", repo.callbackCalls)
	}
	want := errors.New("database unavailable")
	repo.getErr = want
	if err := runner.Execute(context.Background(), id, 1); !errors.Is(err, want) {
		t.Fatalf("Execute error = %v", err)
	}
}

func TestObsoletePublicationOutputCleanupSettingsFailureDoesNotClaimOrFailOperation(t *testing.T) {
	want := errors.New("settings database unavailable")
	id := uuid.New()
	repo := &obsoleteCleanupRepoFake{operation: &persistence.Operation{ID: id, Kind: persistence.ObsoletePublicationOutputCleanupOperationKind, State: "queued", Attempt: 1}}
	runner := NewObsoletePublicationOutputCleanupRunner(repo, obsoleteCleanupSettingsFake{err: want}, publication.NewCoordinator(), sourcefs.NewOutputOpener())
	if err := runner.Execute(context.Background(), id, 3); !errors.Is(err, want) {
		t.Fatalf("Execute error = %v", err)
	}
	if repo.callbackCalls != 0 {
		t.Fatalf("cleanup callback called %d times", repo.callbackCalls)
	}
	if repo.operation.State != "queued" {
		t.Fatalf("operation state changed to %q", repo.operation.State)
	}
}
