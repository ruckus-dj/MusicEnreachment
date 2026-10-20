package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func prepareFirstPublicationRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "analysis"), 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPlanFirstPublicationNamespaceDoesNotCreateTargets(t *testing.T) {
	root := prepareFirstPublicationRoot(t)
	positionID := uuid.New()
	candidates := []publication.NamespaceCandidate{{
		RelativePath: "Музыка.flac",
		NodeKind:     "file",
		PositionID:   positionID,
	}}

	nodes, err := planFirstPublicationNamespace(context.Background(), root, positionID, candidates, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].RelativePath != candidates[0].RelativePath || nodes[0].NodeKind != "file" {
		t.Fatalf("unexpected namespace plan: %#v", nodes)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "analysis" || !entries[0].IsDir() {
		t.Fatalf("namespace planning changed output root: %v", entries)
	}
	analysisEntries, err := os.ReadDir(filepath.Join(root, "analysis"))
	if err != nil {
		t.Fatal(err)
	}
	if len(analysisEntries) != 0 {
		t.Fatalf("namespace planning left technical-area artifacts: %v", analysisEntries)
	}
	if _, err := os.Stat(filepath.Join(root, candidates[0].RelativePath)); !os.IsNotExist(err) {
		t.Fatalf("candidate unexpectedly exists: %v", err)
	}
}

func TestPlanFirstPublicationNamespaceAddsStableSuffixForFilesystemCollision(t *testing.T) {
	root := prepareFirstPublicationRoot(t)
	positionID := uuid.MustParse("12345678-1234-4234-8234-123456789abc")
	if err := os.WriteFile(filepath.Join(root, "Track.ogg"), []byte("unmanaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates := []publication.NamespaceCandidate{{RelativePath: "Track.ogg", NodeKind: "file", PositionID: positionID}}
	for i := 0; i < 2; i++ {
		nodes, err := planFirstPublicationNamespace(context.Background(), root, positionID, candidates, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) != 1 || nodes[0].RelativePath != "Track [12345678].ogg" {
			t.Fatalf("planned node = %#v", nodes)
		}
	}
	contents, err := os.ReadFile(filepath.Join(root, "Track.ogg"))
	if err != nil || string(contents) != "unmanaged" {
		t.Fatalf("unmanaged collision changed: contents %q, err %v", contents, err)
	}
}

func TestPlanFirstPublicationNamespaceDetectsNativeEquivalentFilesystemCollision(t *testing.T) {
	root := prepareFirstPublicationRoot(t)
	positionID := uuid.New()
	if err := os.WriteFile(filepath.Join(root, "TRACK.OGG"), []byte("unmanaged"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	equivalent, probeErr := sourcefs.NativeCompareComponentsIn(context.Background(), parent, technicalParent, "TRACK.OGG", "track.ogg")
	closeErr := errors.Join(technicalParent.Close(), parent.Close())
	if probeErr != nil || closeErr != nil {
		t.Fatalf("probe native equivalence: %v", errors.Join(probeErr, closeErr))
	}
	if !equivalent {
		t.Skip("filesystem treats case variants as distinct")
	}
	nodes, err := planFirstPublicationNamespace(context.Background(), root, positionID, []publication.NamespaceCandidate{{RelativePath: "track.ogg", NodeKind: "file", PositionID: positionID}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].RelativePath == "track.ogg" {
		t.Fatalf("native-equivalent collision was not suffixed: %#v", nodes)
	}
}

func TestPlanFirstPublicationNamespaceExtendsSuffixAroundDurableClaims(t *testing.T) {
	root := prepareFirstPublicationRoot(t)
	positionID, other := uuid.MustParse("12345678-1234-4234-8234-123456789abc"), uuid.New()
	claims := []persistence.PublicationNamespaceClaim{
		{CanonicalKey: "occupied-base", RelativePath: "Track.ogg", NodeKind: "file", OwnerPositionID: &other},
		{CanonicalKey: "occupied-short-suffix", RelativePath: "Track [12345678].ogg", NodeKind: "file", OwnerPositionID: &other},
	}
	nodes, err := planFirstPublicationNamespace(context.Background(), root, positionID, []publication.NamespaceCandidate{{RelativePath: "Track.ogg", NodeKind: "file", PositionID: positionID}}, claims)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].RelativePath != "Track [123456781234].ogg" {
		t.Fatalf("planned node = %#v", nodes)
	}
}

func TestPlanFirstPublicationNamespaceMapsRenderedParentToPinnedScopePath(t *testing.T) {
	root := prepareFirstPublicationRoot(t)
	if err := os.Mkdir(filepath.Join(root, "Pinned"), 0o700); err != nil {
		t.Fatal(err)
	}
	positionID := uuid.New()
	scope := "release-scope"
	claims := []persistence.PublicationNamespaceClaim{{CanonicalKey: "existing-directory", RelativePath: "Pinned", NodeKind: "directory", DirectoryScopeKey: &scope}}
	candidates := []publication.NamespaceCandidate{
		{RelativePath: "Rendered/track.ogg", NodeKind: "file", PositionID: positionID},
		{RelativePath: "Rendered", NodeKind: "directory", DirectoryScope: scope, PositionID: positionID},
	}
	nodes, err := planFirstPublicationNamespace(context.Background(), root, positionID, candidates, claims)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].RelativePath != "Pinned/track.ogg" || nodes[1].RelativePath != "Pinned" {
		t.Fatalf("scope-pinned paths = %#v", nodes)
	}
}

func TestPlanFirstPublicationNamespacePreservesOwnDurableSuffix(t *testing.T) {
	root := prepareFirstPublicationRoot(t)
	positionID := uuid.MustParse("12345678-1234-4234-8234-123456789abc")
	claims := []persistence.PublicationNamespaceClaim{{CanonicalKey: "own-file", RelativePath: "Track [12345678].ogg", NodeKind: "file", OwnerPositionID: &positionID}}
	nodes, err := planFirstPublicationNamespace(context.Background(), root, positionID, []publication.NamespaceCandidate{{RelativePath: "Track.ogg", NodeKind: "file", PositionID: positionID}}, claims)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].RelativePath != "Track [12345678].ogg" {
		t.Fatalf("durable chosen path changed: %#v", nodes)
	}
}

func TestPlanFirstPublicationNamespaceIgnoresUnprobeableUnrelatedSiblings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit these sibling names")
	}
	root := prepareFirstPublicationRoot(t)
	names := []string{"unrelated:name", `unrelated\\name`}
	if runtime.GOOS == "linux" {
		// APFS rejects invalid UTF-8 names; Linux filesystems can retain them.
		names = append(names, string([]byte{'b', 0xff}))
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name), []byte("unmanaged"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	positionID := uuid.New()
	nodes, err := planFirstPublicationNamespace(context.Background(), root, positionID, []publication.NamespaceCandidate{{RelativePath: "Track.ogg", NodeKind: "file", PositionID: positionID}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].RelativePath != "Track.ogg" {
		t.Fatalf("unrelated nonportable siblings changed target: %#v", nodes)
	}
}
