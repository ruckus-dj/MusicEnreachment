package service

import (
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestNamespaceDigestIgnoresCanonicalKeysAndTracksResolution(t *testing.T) {
	base := FirstPublicationPreview{ContextDigest: "context"}
	first := base
	if err := applyNamespaceDigest(&first, []persistence.PublicationNamespaceNode{
		{CanonicalKey: "random-a", RelativePath: "Artist/Album/song [12345678].flac", NodeKind: "file", DirectoryScope: ""},
	}); err != nil {
		t.Fatal(err)
	}
	second := base
	if err := applyNamespaceDigest(&second, []persistence.PublicationNamespaceNode{
		{CanonicalKey: "random-b", RelativePath: "Artist/Album/song [12345678].flac", NodeKind: "file", DirectoryScope: ""},
	}); err != nil {
		t.Fatal(err)
	}
	if first.ContextDigest != second.ContextDigest {
		t.Fatalf("random canonical keys changed semantic digest: %q != %q", first.ContextDigest, second.ContextDigest)
	}
	changed := base
	if err := applyNamespaceDigest(&changed, []persistence.PublicationNamespaceNode{
		{CanonicalKey: "random-c", RelativePath: "Artist/Album/song [abcdef12].flac", NodeKind: "file", DirectoryScope: ""},
	}); err != nil {
		t.Fatal(err)
	}
	if first.ContextDigest == changed.ContextDigest {
		t.Fatal("changed resolved path retained the preview digest")
	}
}
