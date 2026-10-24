package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestAutomaticAudioUpgradeNamespacePreservesChangedExtensionTarget(t *testing.T) {
	root := prepareFirstPublicationRoot(t)
	positionID := uuid.New()
	if err := os.WriteFile(filepath.Join(root, "foo.mp3"), []byte("old audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	claims := []persistence.PublicationNamespaceClaim{{
		CanonicalKey: "old-file", RelativePath: "foo.mp3", NodeKind: "file", OwnerPositionID: &positionID,
	}}

	nodes, err := automaticAudioUpgradeNamespace(context.Background(), root, positionID, "foo.flac", claims, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].RelativePath != "foo.flac" || nodes[0].NodeKind != "file" {
		t.Fatalf("upgrade namespace = %#v", nodes)
	}
}

func TestAutomaticAudioUpgradeNamespaceAllowsSamePathOwnClaim(t *testing.T) {
	root := prepareFirstPublicationRoot(t)
	positionID := uuid.New()
	if err := os.WriteFile(filepath.Join(root, "foo.flac"), []byte("published audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	claims := []persistence.PublicationNamespaceClaim{{
		CanonicalKey: "own-file", RelativePath: "foo.flac", NodeKind: "file", OwnerPositionID: &positionID,
	}}

	nodes, err := automaticAudioUpgradeNamespace(context.Background(), root, positionID, "foo.flac", claims, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].RelativePath != "foo.flac" {
		t.Fatalf("upgrade namespace = %#v", nodes)
	}
}

func TestAutomaticAudioUpgradeNamespaceRejectsOccupiedTargetWithoutSuffix(t *testing.T) {
	for _, test := range []struct {
		name  string
		claim bool
	}{
		{name: "unmanaged file"},
		{name: "foreign claim", claim: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := prepareFirstPublicationRoot(t)
			positionID, foreignID := uuid.New(), uuid.New()
			var claims []persistence.PublicationNamespaceClaim
			if test.claim {
				claims = append(claims, persistence.PublicationNamespaceClaim{
					CanonicalKey: "foreign-file", RelativePath: "foo.flac", NodeKind: "file", OwnerPositionID: &foreignID,
				})
			} else if err := os.WriteFile(filepath.Join(root, "foo.flac"), []byte("occupied"), 0o600); err != nil {
				t.Fatal(err)
			}

			nodes, err := automaticAudioUpgradeNamespace(context.Background(), root, positionID, "foo.flac", claims, nil)
			if err == nil {
				t.Fatalf("occupied target unexpectedly planned as %#v", nodes)
			}
			if len(nodes) != 0 {
				t.Fatalf("occupied target produced suffix: %#v", nodes)
			}
		})
	}
}

func TestAutomaticAudioUpgradeNamespaceRejectsNativeAlias(t *testing.T) {
	root := prepareFirstPublicationRoot(t)
	parent, err := sourcefs.NewOutputOpener().OpenRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	traversal, ok := parent.(sourcefs.OutputDirectoryTraversal)
	if !ok {
		_ = parent.Close()
		t.Fatal("output root does not support traversal")
	}
	technicalParent, err := traversal.OpenExistingDir(context.Background(), "analysis")
	if err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	equivalent, probeErr := sourcefs.NativeCompareComponentsIn(context.Background(), parent, technicalParent, "FOO.FLAC", "foo.flac")
	closeErr := errors.Join(technicalParent.Close(), parent.Close())
	if probeErr != nil || closeErr != nil {
		t.Fatalf("probe native equivalence: %v", errors.Join(probeErr, closeErr))
	}
	if !equivalent {
		t.Skip("filesystem treats case variants as distinct")
	}
	positionID, foreignID := uuid.New(), uuid.New()
	claims := []persistence.PublicationNamespaceClaim{{
		CanonicalKey: "foreign-file", RelativePath: "FOO.FLAC", NodeKind: "file", OwnerPositionID: &foreignID,
	}}

	nodes, err := automaticAudioUpgradeNamespace(context.Background(), root, positionID, "foo.flac", claims, nil)
	if err == nil {
		t.Fatalf("native alias target unexpectedly planned as %#v", nodes)
	}
}
