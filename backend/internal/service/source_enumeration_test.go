package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
)

type sourceEnumerationTestOpener struct{ root *sourceWalkFakeDirectory }

func (opener sourceEnumerationTestOpener) OpenRoot(context.Context, string) (sourcefs.Directory, error) {
	return opener.root, nil
}

func TestEnumerateSourceTreeCollectsRootScope(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	scopes, err := EnumerateSourceTree(context.Background(), missing, func(SourceWalkEntry) error {
		t.Fatal("visitor called for a root that could not be opened")
		return nil
	})
	if err != nil {
		t.Fatalf("enumerate missing root: %v", err)
	}
	if len(scopes) != 1 || scopes[0] != (SourceEnumerationScope{Kind: "root"}) {
		t.Fatalf("unreadable scopes = %#v, want root scope", scopes)
	}
}

func TestEnumerateSourceTreeAbortsOnVisitorErrorAndCancellation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "track.flac"), []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := errors.New("visitor failed")
	if _, err := EnumerateSourceTree(context.Background(), root, func(SourceWalkEntry) error { return want }); !errors.Is(err, want) {
		t.Fatalf("visitor error = %v, want %v", err, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = EnumerateSourceTree(ctx, root, func(SourceWalkEntry) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled enumeration error = %v, want context cancellation", err)
	}
}

func TestEnumerateSourceTreeCollectsSubtreeAndFileScopes(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("permission denied")
	root := &sourceWalkFakeDirectory{
		entries: []sourcefs.Entry{
			{Name: "locked", Kind: sourcefs.KindDir},
			{Name: "unreadable.flac", Kind: sourcefs.KindRegular},
		},
		dirErr:  map[string]error{"locked": denied},
		fileErr: map[string]error{"unreadable.flac": denied},
		info:    rootInfo,
	}
	var visited []string
	scopes, err := enumerateSourceTree(context.Background(), sourceEnumerationTestOpener{root: root}, rootPath, func(entry SourceWalkEntry) error {
		visited = append(visited, entry.RelativePath)
		return nil
	})
	if err != nil {
		t.Fatalf("enumerate fake source tree: %v", err)
	}
	if len(visited) != 0 {
		t.Fatalf("visited files = %v, want none", visited)
	}
	want := []SourceEnumerationScope{{RelativePath: "locked", Kind: "subtree"}, {RelativePath: "unreadable.flac", Kind: "file"}}
	if len(scopes) != len(want) {
		t.Fatalf("unreadable scopes = %#v, want %#v", scopes, want)
	}
	for i := range want {
		if scopes[i] != want[i] {
			t.Fatalf("unreadable scopes = %#v, want %#v", scopes, want)
		}
	}
}

func TestValidEnumerationScopeRequiresContainedRelativePath(t *testing.T) {
	for _, scope := range []SourceEnumerationScope{
		{Kind: "root"},
		{Kind: "subtree", RelativePath: "album/disc"},
		{Kind: "file", RelativePath: "album/track.flac"},
	} {
		if !validEnumerationScope(scope) {
			t.Errorf("valid scope rejected: %+v", scope)
		}
	}
	for _, scope := range []SourceEnumerationScope{
		{Kind: "other"},
		{Kind: "root", RelativePath: "album"},
		{Kind: "subtree", RelativePath: "../outside"},
		{Kind: "file", RelativePath: "/outside.flac"},
		{Kind: "file", RelativePath: `album\\track.flac`},
	} {
		if validEnumerationScope(scope) {
			t.Errorf("invalid scope accepted: %+v", scope)
		}
	}
}
