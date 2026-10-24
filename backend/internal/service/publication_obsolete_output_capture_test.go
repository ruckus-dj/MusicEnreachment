package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type obsoleteCaptureWrongParent struct{ sourcefs.OutputDirectory }

func (directory obsoleteCaptureWrongParent) VerifyChild(context.Context, string, sourcefs.DirectoryIdentity) error {
	return errors.New("parent identity changed")
}

func (directory obsoleteCaptureWrongParent) OpenExistingDir(ctx context.Context, name string) (sourcefs.OutputDirectory, error) {
	traversal := directory.OutputDirectory.(sourcefs.OutputDirectoryTraversal)
	return traversal.OpenExistingDir(ctx, name)
}

func TestCaptureObsoletePublicationOutputRequiresExactCurrentWitness(t *testing.T) {
	const relative = "Artist/Album/track.mp3"
	contents := []byte("current publication output")
	rootPath, oldAuthority := obsoleteCleanupFixture(t, relative, contents)
	root, err := sourcefs.NewOutputOpener().OpenRoot(context.Background(), rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	size, modified := oldAuthority.FileSize, oldAuthority.ModifiedAt.UnixMicro()
	var ownership persistence.PublicationCurrentOwnership
	ownership.Version = persistence.PublicationCurrentOwnershipVersion
	ownership.Identity = persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &modified, SHA256: hex.EncodeToString(oldAuthority.SHA256)}
	rawOwnership, err := json.Marshal(ownership)
	if err != nil {
		t.Fatal(err)
	}
	previous := persistence.TrackPublication{PositionID: uuid.New(), Epoch: uuid.New(), Generation: uuid.New(), RelativePath: relative, Ownership: rawOwnership}
	claim := uuid.New()
	got, err := captureObsoletePublicationOutput(context.Background(), root, rootPath, previous, claim)
	if err != nil {
		t.Fatal(err)
	}
	if got.PositionID != previous.PositionID || got.Epoch != previous.Epoch || got.Generation != previous.Generation || got.RelativePath != relative || got.NamespaceClaimID != claim || got.FileSize != size || got.ModifiedAt.UnixMicro() != modified || hex.EncodeToString(got.SHA256) != ownership.Identity.SHA256 {
		t.Fatalf("captured authority differs from current witness: %+v", got)
	}
}

func TestCaptureObsoletePublicationOutputRejectsChangedHashAndMissingFile(t *testing.T) {
	for _, test := range []struct {
		name   string
		remove bool
	}{{name: "changed hash"}, {name: "missing", remove: true}} {
		t.Run(test.name, func(t *testing.T) {
			const relative = "Album/track.mp3"
			rootPath, authority := obsoleteCleanupFixture(t, relative, []byte("original"))
			if test.remove {
				if err := os.Remove(filepath.Join(rootPath, filepath.FromSlash(relative))); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(rootPath, filepath.FromSlash(relative)), []byte("modified"), 0600); err != nil {
				t.Fatal(err)
			}
			if !test.remove {
				if err := os.Chtimes(authority.NativePath, authority.ModifiedAt, authority.ModifiedAt); err != nil {
					t.Fatal(err)
				}
			}
			root, err := sourcefs.NewOutputOpener().OpenRoot(context.Background(), rootPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			}()
			size, modified := authority.FileSize, authority.ModifiedAt.UnixMicro()
			ownership, _ := json.Marshal(persistence.PublicationCurrentOwnership{Version: persistence.PublicationCurrentOwnershipVersion, Identity: persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &modified, SHA256: hex.EncodeToString(authority.SHA256)}})
			previous := persistence.TrackPublication{PositionID: uuid.New(), Epoch: uuid.New(), Generation: uuid.New(), RelativePath: relative, Ownership: ownership}
			if got, err := captureObsoletePublicationOutput(context.Background(), root, rootPath, previous, uuid.New()); err == nil || got != nil {
				t.Fatalf("capture = (%v, %v), want failure", got, err)
			}
			if _, err := os.Stat(filepath.Join(rootPath, "Album")); err != nil {
				t.Fatalf("capture changed directory namespace: %v", err)
			}
		})
	}
}

func TestCaptureObsoletePublicationOutputRejectsWrongParent(t *testing.T) {
	rootPath, authority := obsoleteCleanupFixture(t, "Album/track.mp3", []byte("original"))
	root, err := sourcefs.NewOutputOpener().OpenRoot(context.Background(), rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	size, modified := authority.FileSize, authority.ModifiedAt.UnixMicro()
	ownership, err := json.Marshal(persistence.PublicationCurrentOwnership{Version: persistence.PublicationCurrentOwnershipVersion, Identity: persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &modified, SHA256: hex.EncodeToString(authority.SHA256)}})
	if err != nil {
		t.Fatal(err)
	}
	previous := persistence.TrackPublication{PositionID: uuid.New(), Epoch: uuid.New(), Generation: uuid.New(), RelativePath: "Album/track.mp3", Ownership: ownership}
	if got, err := captureObsoletePublicationOutput(context.Background(), obsoleteCaptureWrongParent{OutputDirectory: root}, rootPath, previous, uuid.New()); err == nil || got != nil {
		t.Fatalf("capture = (%v, %v), want parent-identity failure", got, err)
	}
}
