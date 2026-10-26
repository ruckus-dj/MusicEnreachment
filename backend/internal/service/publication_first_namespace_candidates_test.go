package service

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func decodeFirstPublicationScope(t *testing.T, scope string) []string {
	t.Helper()
	var parts []string
	if err := json.Unmarshal([]byte(scope), &parts); err != nil {
		t.Fatalf("decode directory scope %q: %v", scope, err)
	}
	return parts
}

func TestBuildFirstPublicationNamespaceCandidatesSharesArtistAndTypeScopes(t *testing.T) {
	artistID, releaseA, releaseB := uuid.New(), uuid.New(), uuid.New()
	positionA, positionB := uuid.New(), uuid.New()
	componentsA := []string{"Artist [mbid]", "Album", "2020 - Record A [ra]", "01-01 - Track A.flac"}
	componentsB := []string{"Artist [mbid]", "Album", "2021 - Record B [rb]", "01-02 - Track B.flac"}

	a, err := buildFirstPublicationNamespaceCandidates(publication.DefaultTemplate, componentsA, artistID, releaseA, positionA, "Album")
	if err != nil {
		t.Fatalf("build release A candidates: %v", err)
	}
	b, err := buildFirstPublicationNamespaceCandidates(publication.DefaultTemplate, componentsB, artistID, releaseB, positionB, "Album")
	if err != nil {
		t.Fatalf("build release B candidates: %v", err)
	}
	if len(a) != 4 || len(b) != 4 {
		t.Fatalf("candidate counts = %d, %d; want 4", len(a), len(b))
	}
	for i, candidate := range a {
		if candidate.PositionID != positionA || candidate.NodeKind != kindFor(i) {
			t.Fatalf("release A candidate %d = %#v", i, candidate)
		}
	}
	for i, candidate := range b {
		if candidate.PositionID != positionB || candidate.NodeKind != kindFor(i) {
			t.Fatalf("release B candidate %d = %#v", i, candidate)
		}
	}
	// Artist and album-type directories are shared across releases of one artist.
	if a[0].RelativePath != b[0].RelativePath || a[0].DirectoryScope != b[0].DirectoryScope || !a[0].ShareDirectory || !a[1].ShareDirectory || !a[2].ShareDirectory {
		t.Fatalf("artist directory not shared: %#v vs %#v", a[0], b[0])
	}
	if got := decodeFirstPublicationScope(t, a[0].DirectoryScope); len(got) != 2 || got[0] != "artist" || got[1] != artistID.String() {
		t.Fatalf("artist scope = %v", got)
	}
	if a[1].RelativePath != b[1].RelativePath || a[1].DirectoryScope != b[1].DirectoryScope {
		t.Fatalf("album-type directory not shared: %#v vs %#v", a[1], b[1])
	}
	if got := decodeFirstPublicationScope(t, a[1].DirectoryScope); len(got) != 3 || got[0] != "artist-type" || got[1] != artistID.String() || got[2] != "Album" {
		t.Fatalf("album-type scope = %v", got)
	}
	// The release directory belongs to a single release and scopes differ.
	if a[2].RelativePath == b[2].RelativePath || a[2].DirectoryScope == b[2].DirectoryScope {
		t.Fatalf("release directories collided: %#v vs %#v", a[2], b[2])
	}
	if got := decodeFirstPublicationScope(t, a[2].DirectoryScope); len(got) != 2 || got[0] != "release" || got[1] != releaseA.String() {
		t.Fatalf("release A scope = %v", got)
	}
	if got := decodeFirstPublicationScope(t, b[2].DirectoryScope); len(got) != 2 || got[0] != "release" || got[1] != releaseB.String() {
		t.Fatalf("release B scope = %v", got)
	}
	if a[3].DirectoryScope != "" || a[3].RelativePath != a[2].RelativePath+"/"+componentsA[3] {
		t.Fatalf("release A file candidate = %#v", a[3])
	}
	if b[3].RelativePath != b[2].RelativePath+"/"+componentsB[3] {
		t.Fatalf("release B file candidate = %#v", b[3])
	}
}

func kindFor(index int) string {
	if index < 3 {
		return firstPublicationNodeDirectory
	}
	return firstPublicationNodeFile
}

func TestBuildFirstPublicationNamespaceCandidatesSharesReleaseAcrossTracks(t *testing.T) {
	artistID, releaseID := uuid.New(), uuid.New()
	positionOne, positionTwo := uuid.New(), uuid.New()
	componentsOne := []string{"Artist", "Album", "2020 - Record", "01-01 - One.flac"}
	componentsTwo := []string{"Artist", "Album", "2020 - Record", "01-02 - Two.flac"}

	one, err := buildFirstPublicationNamespaceCandidates(publication.DefaultTemplate, componentsOne, artistID, releaseID, positionOne, "Album")
	if err != nil {
		t.Fatalf("build track one candidates: %v", err)
	}
	two, err := buildFirstPublicationNamespaceCandidates(publication.DefaultTemplate, componentsTwo, artistID, releaseID, positionTwo, "Album")
	if err != nil {
		t.Fatalf("build track two candidates: %v", err)
	}
	if len(one) != 4 || len(two) != 4 {
		t.Fatalf("candidate counts = %d, %d; want 4", len(one), len(two))
	}
	for i := 0; i < 3; i++ {
		if one[i].RelativePath != two[i].RelativePath || one[i].DirectoryScope != two[i].DirectoryScope {
			t.Fatalf("release directory %d not shared: %#v vs %#v", i, one[i], two[i])
		}
	}
	if one[3].RelativePath == two[3].RelativePath {
		t.Fatalf("distinct tracks share a file candidate: %#v", one[3])
	}
	if one[3].RelativePath != one[2].RelativePath+"/"+componentsOne[3] || two[3].RelativePath != two[2].RelativePath+"/"+componentsTwo[3] {
		t.Fatalf("track file candidates = %#v, %#v", one[3], two[3])
	}
}

func TestBuildFirstPublicationNamespaceCandidatesDistinctArtistsShareName(t *testing.T) {
	releaseID, positionID := uuid.New(), uuid.New()
	components := []string{"Shared Name", "Album", "2020 - Record", "01-01 - One.flac"}

	first, err := buildFirstPublicationNamespaceCandidates(publication.DefaultTemplate, components, uuid.New(), releaseID, positionID, "Album")
	if err != nil {
		t.Fatalf("build first artist candidates: %v", err)
	}
	second, err := buildFirstPublicationNamespaceCandidates(publication.DefaultTemplate, components, uuid.New(), releaseID, positionID, "Album")
	if err != nil {
		t.Fatalf("build second artist candidates: %v", err)
	}
	if first[0].RelativePath != second[0].RelativePath {
		t.Fatalf("rendered artist directory names differ: %q vs %q", first[0].RelativePath, second[0].RelativePath)
	}
	if first[0].DirectoryScope == second[0].DirectoryScope {
		t.Fatalf("distinct artists share a directory scope: %q", first[0].DirectoryScope)
	}
}

func TestBuildFirstPublicationNamespaceCandidatesFlatUnicodeTemplate(t *testing.T) {
	positionID := uuid.New()
	candidates, err := buildFirstPublicationNamespaceCandidates("{ALBUM_NAME}.{EXT}", []string{"曲 目.flac"}, uuid.Nil, uuid.Nil, positionID, "")
	if err != nil {
		t.Fatalf("build flat candidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("flat candidate count = %d; want 1", len(candidates))
	}
	want := publication.NamespaceCandidate{RelativePath: "曲 目.flac", NodeKind: firstPublicationNodeFile, PositionID: positionID}
	if candidates[0] != want {
		t.Fatalf("flat candidate = %#v; want %#v", candidates[0], want)
	}
}

func TestBuildFirstPublicationNamespaceCandidatesFallsBackToApprovedAlbumType(t *testing.T) {
	artistID, releaseID, positionID := uuid.New(), uuid.New(), uuid.New()
	components := []string{"Artist", "Unclassified", "2020 - Record", "01-01 - One.flac"}

	candidates, err := buildFirstPublicationNamespaceCandidates(publication.DefaultTemplate, components, artistID, releaseID, positionID, "")
	if err != nil {
		t.Fatalf("build candidates: %v", err)
	}
	if got := decodeFirstPublicationScope(t, candidates[1].DirectoryScope); len(got) != 3 || got[2] != "Unclassified" {
		t.Fatalf("fallback album-type scope = %v", got)
	}
}

func TestBuildFirstPublicationNamespaceCandidatesSupportsNestedTemplates(t *testing.T) {
	positionID := uuid.New()
	candidates, err := buildFirstPublicationNamespaceCandidates("{ALBUM_PRIMARY_ARTIST}/{ALBUM_NAME}/{TRACK_TITLE}.{EXT}", []string{"Artist", "Album", "Track.flac"}, uuid.Nil, uuid.Nil, positionID, "")
	if err != nil {
		t.Fatalf("build nested candidates: %v", err)
	}
	if len(candidates) != 3 || candidates[0].RelativePath != "Artist" || candidates[1].RelativePath != "Artist/Album" || candidates[2].RelativePath != "Artist/Album/Track.flac" {
		t.Fatalf("nested candidates = %#v", candidates)
	}
	if !candidates[0].ShareDirectory || !candidates[1].ShareDirectory || candidates[0].DirectoryScope == "" || candidates[1].DirectoryScope == "" {
		t.Fatalf("nested directory sharing/scopes = %#v", candidates[:2])
	}
}

func TestBuildFirstPublicationNamespaceCandidatesRejectsUnsupportedLayout(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		templateSource string
		components     []string
	}{
		{"default short", publication.DefaultTemplate, []string{"Artist", "Album", "Track.flac"}},
		{"empty template", "", []string{"Track.flac"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidates, err := buildFirstPublicationNamespaceCandidates(testCase.templateSource, testCase.components, uuid.New(), uuid.New(), uuid.New(), "Album")
			if !errors.Is(err, ErrFirstPublicationNamespaceLayoutUnavailable) {
				t.Fatalf("error = %v; want ErrFirstPublicationNamespaceLayoutUnavailable", err)
			}
			if candidates != nil {
				t.Fatalf("candidates = %#v; want nil", candidates)
			}
		})
	}
}

func TestBuildFirstPublicationNamespaceCandidatesRejectsUnsafeComponent(t *testing.T) {
	positionID := uuid.New()
	candidates, err := buildFirstPublicationNamespaceCandidates("{ALBUM_NAME}.{EXT}", []string{"Track: Bad.flac"}, uuid.Nil, uuid.Nil, positionID, "")
	if !errors.Is(err, ErrFirstPublicationNamespaceLayoutUnavailable) || candidates != nil {
		t.Fatalf("unsafe component result = %#v, %v; want layout error and nil candidates", candidates, err)
	}
}

func TestBuildFirstPublicationNamespaceCandidatesRequiresPosition(t *testing.T) {
	components := []string{"Artist", "Album", "2020 - Record", "01-01 - One.flac"}
	if _, err := buildFirstPublicationNamespaceCandidates(publication.DefaultTemplate, components, uuid.New(), uuid.New(), uuid.Nil, "Album"); err == nil {
		t.Fatal("missing position ID was accepted")
	}
}
