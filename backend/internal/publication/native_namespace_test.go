package publication

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func nativeComparator(_ context.Context, _ string, a, b string) (bool, error) {
	return strings.EqualFold(a, b), nil
}

func TestPlanNativeNamespaceUsesExistingRepresentativeAndKey(t *testing.T) {
	owner := uuid.New()
	claims := []NamespaceClaim{{CanonicalKey: "legacy", RelativePath: "Old", NodeKind: "directory", DirectoryScope: "scope"}}
	got, err := PlanNativeNamespace(context.Background(), []NamespaceCandidate{{RelativePath: "old", NodeKind: "directory", DirectoryScope: "scope", PositionID: owner}, {RelativePath: "old/Track.ogg", NodeKind: "file", PositionID: owner}}, claims, nativeComparator)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].RelativePath != "Old/Track.ogg" || !strings.HasPrefix(got[1].CanonicalKey, "legacy/") || got[0].CanonicalKey != "legacy" {
		t.Fatalf("planned node = %#v", got)
	}
}

func TestPlanNativeNamespaceRejectsFileDirectoryAndOwnerCollisions(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name       string
		want       string
		claims     []NamespaceClaim
		candidates []NamespaceCandidate
	}{
		{"file/directory", "file/directory collision", []NamespaceClaim{{CanonicalKey: "key", RelativePath: "Music", NodeKind: "file", OwnerPositionID: &owner}}, []NamespaceCandidate{{RelativePath: "music", NodeKind: "directory", DirectoryScope: "scope", PositionID: owner}}},
		{"different owner", "file is owned by another position", []NamespaceClaim{{CanonicalKey: "key", RelativePath: "Song", NodeKind: "file", OwnerPositionID: &owner}}, []NamespaceCandidate{{RelativePath: "song", NodeKind: "file", PositionID: other}}},
		{"scope", "directory scope conflict", []NamespaceClaim{{CanonicalKey: "key", RelativePath: "Folder", NodeKind: "directory", DirectoryScope: "old"}}, []NamespaceCandidate{{RelativePath: "folder", NodeKind: "directory", DirectoryScope: "new", PositionID: owner}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PlanNativeNamespace(context.Background(), tc.candidates, tc.claims, nativeComparator)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPlanNativeNamespacePreservesNativeDistinctUnicode(t *testing.T) {
	calls := 0
	cmp := func(context.Context, string, string, string) (bool, error) { calls++; return false, nil }
	owner := uuid.New()
	got, err := PlanNativeNamespace(context.Background(), []NamespaceCandidate{{RelativePath: "é", NodeKind: "file", PositionID: owner}, {RelativePath: "é", NodeKind: "file", PositionID: owner}}, nil, cmp)
	if err != nil || len(got) != 2 || got[0].CanonicalKey == got[1].CanonicalKey || calls == 0 {
		t.Fatalf("got %#v, calls %d, err %v", got, calls, err)
	}
}

func TestPlanNativeNamespacePropagatesComparisonErrors(t *testing.T) {
	want := errors.New("unknown native comparison")
	cmp := func(context.Context, string, string, string) (bool, error) { return false, want }
	_, err := PlanNativeNamespace(context.Background(), []NamespaceCandidate{{RelativePath: "x", NodeKind: "file", PositionID: uuid.New()}, {RelativePath: "y", NodeKind: "file", PositionID: uuid.New()}}, nil, cmp)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

func TestPlanNativeNamespaceRequiresAndUsesExplicitScopedParents(t *testing.T) {
	owner := uuid.New()
	candidates := []NamespaceCandidate{{RelativePath: "Folder/Track.ogg", NodeKind: "file", PositionID: owner}, {RelativePath: "Folder", NodeKind: "directory", DirectoryScope: "approved", PositionID: owner}}
	got, err := PlanNativeNamespace(context.Background(), candidates, nil, nativeComparator)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RelativePath != "Folder/Track.ogg" || got[1].DirectoryScope != "approved" {
		t.Fatalf("planned = %#v", got)
	}
	_, err = PlanNativeNamespace(context.Background(), candidates[:1], nil, nativeComparator)
	if err == nil {
		t.Fatal("expected missing parent scope to fail")
	}
}

func TestPlanNativeNamespaceCancellationDuringComparison(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmp := func(context.Context, string, string, string) (bool, error) { cancel(); return false, nil }
	_, err := PlanNativeNamespace(ctx, []NamespaceCandidate{{RelativePath: "a", NodeKind: "file", PositionID: uuid.New()}, {RelativePath: "b", NodeKind: "file", PositionID: uuid.New()}}, nil, cmp)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestPlanNativeNamespacePinsDirectoryPathsByScope(t *testing.T) {
	owner := uuid.New()
	claims := []NamespaceClaim{
		{CanonicalKey: "artist-key", RelativePath: "Artist Old", NodeKind: "directory", DirectoryScope: "artist-id"},
		{CanonicalKey: "artist-key/release-key", RelativePath: "Artist Old/Album Old", NodeKind: "directory", DirectoryScope: "release-id"},
	}
	candidates := []NamespaceCandidate{
		{RelativePath: "Artist New/Album New/track.ogg", NodeKind: "file", PositionID: owner},
		{RelativePath: "Artist New/Album New", NodeKind: "directory", DirectoryScope: "release-id", PositionID: owner},
		{RelativePath: "Artist New", NodeKind: "directory", DirectoryScope: "artist-id", PositionID: owner},
	}
	cmp := func(context.Context, string, string, string) (bool, error) { return false, nil }
	got, err := PlanNativeNamespace(context.Background(), candidates, claims, cmp)
	if err != nil {
		t.Fatal(err)
	}
	if got[1].CanonicalKey != "artist-key/release-key" || got[1].RelativePath != "Artist Old/Album Old" {
		t.Fatalf("release node = %#v", got[1])
	}
	if got[2].CanonicalKey != "artist-key" || got[2].RelativePath != "Artist Old" {
		t.Fatalf("artist node = %#v", got[2])
	}
	if got[0].RelativePath != "Artist Old/Album Old/track.ogg" {
		t.Fatalf("file path = %q", got[0].RelativePath)
	}
}

func TestPlanNativeNamespaceScopeReuseStillRejectsNativeAlias(t *testing.T) {
	owner := uuid.New()
	claims := []NamespaceClaim{
		{CanonicalKey: "artist", RelativePath: "Alpha", NodeKind: "directory", DirectoryScope: "artist-id"},
		{CanonicalKey: "other", RelativePath: "Beta", NodeKind: "directory", DirectoryScope: "other-id"},
	}
	candidates := []NamespaceCandidate{{RelativePath: "Beta/track", NodeKind: "file", PositionID: owner}, {RelativePath: "Beta", NodeKind: "directory", DirectoryScope: "artist-id", PositionID: owner}}
	if _, err := PlanNativeNamespace(context.Background(), candidates, claims, nativeComparator); err == nil {
		t.Fatal("expected native alias collision with other scope")
	}
}

func TestPlanNativeNamespaceScopeReuseChecksRepresentativeAgainstSiblings(t *testing.T) {
	owner := uuid.New()
	claims := []NamespaceClaim{
		{CanonicalKey: "selected", RelativePath: "Alpha", NodeKind: "directory", DirectoryScope: "selected-scope"},
		{CanonicalKey: "other", RelativePath: "ALPHA", NodeKind: "directory", DirectoryScope: "other-scope"},
	}
	candidate := NamespaceCandidate{RelativePath: "New Name", NodeKind: "directory", DirectoryScope: "selected-scope", PositionID: owner}
	if _, err := PlanNativeNamespace(context.Background(), []NamespaceCandidate{candidate}, claims, nativeComparator); err == nil {
		t.Fatal("expected selected representative alias to reject scope reuse")
	}
}

func TestPlanNativeNamespaceRejectsExistingEquivalentClaims(t *testing.T) {
	claims := []NamespaceClaim{{CanonicalKey: "one", RelativePath: "A", NodeKind: "directory"}, {CanonicalKey: "two", RelativePath: "a", NodeKind: "directory"}}
	if _, err := PlanNativeNamespace(context.Background(), []NamespaceCandidate{{RelativePath: "a", NodeKind: "file", PositionID: uuid.New()}}, claims, nativeComparator); err == nil {
		t.Fatal("expected conflicting existing aliases to block a relevant candidate")
	}
}

func TestPlanNativeNamespaceComparesOnlyCandidateSiblings(t *testing.T) {
	const count = 1000
	claims := make([]NamespaceClaim, 0, count+2)
	for i := 0; i < count; i++ {
		owner := uuid.New()
		claims = append(claims, NamespaceClaim{CanonicalKey: uuid.NewString(), RelativePath: fmt.Sprintf("claim-%d", i), NodeKind: "file", OwnerPositionID: &owner})
	}
	unrelatedOwner := uuid.New()
	claims = append(claims,
		NamespaceClaim{CanonicalKey: "unrelated", RelativePath: "Unrelated", NodeKind: "directory", DirectoryScope: "unrelated"},
		NamespaceClaim{CanonicalKey: "unrelated/child", RelativePath: "Unrelated/Child", NodeKind: "file", OwnerPositionID: &unrelatedOwner},
	)
	calls := 0
	cmp := func(_ context.Context, parent, _, _ string) (bool, error) {
		calls++
		if parent != "" {
			t.Errorf("compared unrelated subtree at parent %q", parent)
		}
		return false, nil
	}
	_, err := PlanNativeNamespace(context.Background(), []NamespaceCandidate{{RelativePath: "target", NodeKind: "file", PositionID: uuid.New()}}, claims, cmp)
	if err != nil {
		t.Fatal(err)
	}
	if calls > 2*len(claims) {
		t.Fatalf("native comparisons = %d for %d claims; want linear candidate-scoped work", calls, len(claims))
	}
}

func TestPlanNativeNamespaceDoesNotCompareWithoutCandidates(t *testing.T) {
	claims := []NamespaceClaim{
		{CanonicalKey: "one", RelativePath: "A", NodeKind: "directory", DirectoryScope: "one"},
		{CanonicalKey: "two", RelativePath: "a", NodeKind: "directory", DirectoryScope: "two"},
	}
	calls := 0
	cmp := func(context.Context, string, string, string) (bool, error) { calls++; return true, nil }
	if _, err := PlanNativeNamespace(context.Background(), nil, claims, cmp); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("native comparisons = %d, want zero", calls)
	}
}

func TestPlanNativeNamespaceChecksRequestedParentSiblingsWhenScopeExistsElsewhere(t *testing.T) {
	owner := uuid.New()
	for _, tc := range []struct {
		name    string
		want    string
		sibling NamespaceClaim
	}{
		{
			name:    "file",
			want:    "file/directory collision",
			sibling: NamespaceClaim{CanonicalKey: "requested/conflict", RelativePath: "Requested/Conflict", NodeKind: "file", OwnerPositionID: &owner},
		},
		{
			name:    "directory",
			want:    "directory scope conflict",
			sibling: NamespaceClaim{CanonicalKey: "requested/conflict", RelativePath: "Requested/Conflict", NodeKind: "directory", DirectoryScope: "conflicting-scope"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := []NamespaceClaim{
				{CanonicalKey: "elsewhere", RelativePath: "Elsewhere", NodeKind: "directory", DirectoryScope: "elsewhere-scope"},
				{CanonicalKey: "elsewhere/folder", RelativePath: "Elsewhere/Folder", NodeKind: "directory", DirectoryScope: "shared-scope"},
				{CanonicalKey: "requested", RelativePath: "Requested", NodeKind: "directory", DirectoryScope: "requested-scope"},
				tc.sibling,
			}
			candidates := []NamespaceCandidate{
				{RelativePath: "Requested", NodeKind: "directory", DirectoryScope: "requested-scope", PositionID: owner},
				{RelativePath: "Requested/conflict", NodeKind: "directory", DirectoryScope: "shared-scope", PositionID: owner},
			}
			_, err := PlanNativeNamespace(context.Background(), candidates, claims, nativeComparator)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
