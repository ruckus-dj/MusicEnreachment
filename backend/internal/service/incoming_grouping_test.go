package service

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestGroupIncomingFilesMBIDCrossRootAndStableRevision(t *testing.T) {
	first := incomingTestFile("00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000011", "00000000-0000-4000-8000-000000000021", "one/track.flac", map[string][]string{"MUSICBRAINZ_ALBUMID": {" 123E4567-E89B-12D3-A456-426614174000 "}, "ALBUM": {"Record"}, "ARTIST": {"Artist"}})
	second := incomingTestFile("00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000012", "00000000-0000-4000-8000-000000000022", "other/track.flac", map[string][]string{"MUSICBRAINZ_ALBUMID": {"123e4567-e89b-12d3-a456-426614174000"}, "ALBUM": {"Record"}, "ARTIST": {"Artist"}})
	groups, err := GroupIncomingFiles([]IncomingFile{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Members) != 2 {
		t.Fatalf("expected cross-root MBID group, got %#v", groups)
	}
	reversed, err := GroupIncomingFiles([]IncomingFile{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if groups[0].ID != reversed[0].ID || groups[0].Revision != reversed[0].Revision {
		t.Fatalf("input permutation changed identity/revision: %#v %#v", groups, reversed)
	}
	changed := second
	changed.Locations = []IncomingLocation{second.Locations[0], {RootID: second.Locations[0].RootID, RelativePath: "alternate/track.flac", SizeBytes: 10, Mtime: "mtime"}}
	updated, err := GroupIncomingFiles([]IncomingFile{first, changed})
	if err != nil {
		t.Fatal(err)
	}
	if updated[0].ID != groups[0].ID || updated[0].Revision == groups[0].Revision {
		t.Fatalf("membership identity or revision semantics wrong: %#v %#v", groups[0], updated[0])
	}
}

func TestReplayIncomingGroupingActionsUsesCurrentPartition(t *testing.T) {
	firstID, secondID := uuid.New(), uuid.New()
	firstMember, secondMember := uuid.New(), uuid.New()
	groups := []IncomingGroup{
		{ID: firstID, Revision: "one", Members: []uuid.UUID{firstMember}},
		{ID: secondID, Revision: "two", Members: []uuid.UUID{secondMember}},
	}
	base, err := IncomingGroupsRevision(groups)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := ReplayIncomingGroupingActions(groups, base, []IncomingGroupingAction{{Kind: "merge", GroupIDs: []uuid.UUID{firstID, secondID}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Groups) != 1 || len(draft.Groups[0].Members) != 2 {
		t.Fatalf("merge actions were not replayed against current groups: %#v", draft.Groups)
	}
	if _, err := ReplayIncomingGroupingActions(groups, "stale", nil); err == nil {
		t.Fatal("expected stale base revision to be rejected")
	}
	if _, err := ReplayIncomingGroupingActions(groups, base, []IncomingGroupingAction{{Kind: "move", MemberIDs: []uuid.UUID{uuid.New()}, TargetGroupID: firstID}}); err == nil {
		t.Fatal("expected nonexistent member reference to be rejected")
	}
}

func TestGroupIncomingFilesEditionFieldsAndDiscNumber(t *testing.T) {
	baseTags := map[string][]string{"ALBUM": {"Album"}, "ALBUMARTIST": {"Artist"}, "ALBUMDATE": {"2001"}, "CATALOGNUMBER": {"cat"}, "RELEASECOUNTRY": {"US"}, "DISCNUMBER": {"1"}}
	a := incomingTestFile("00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000011", "00000000-0000-4000-8000-000000000021", "a.flac", baseTags)
	b := incomingTestFile("00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000012", "00000000-0000-4000-8000-000000000022", "b.flac", map[string][]string{"ALBUM": {"Album"}, "ALBUMARTIST": {"Artist"}, "DATE": {"2001"}, "CATALOG": {"cat"}, "RELEASECOUNTRY": {"US"}, "DISCNUMBER": {"2"}})
	groups, err := GroupIncomingFiles([]IncomingFile{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("aliases and disc number should not split group: %#v", groups)
	}
	for index, change := range []map[string][]string{{"DATE": {"2002"}}, {"CATALOGNUMBER": {"other"}}, {"RELEASECOUNTRY": {"GB"}}} {
		changedTags := map[string][]string{"ALBUM": {"Album"}, "ALBUMARTIST": {"Artist"}, "DATE": {"2001"}, "CATALOGNUMBER": {"cat"}, "RELEASECOUNTRY": {"US"}}
		for key, value := range change {
			changedTags[key] = value
		}
		changed := b
		changed.Tags = changedTags
		split, err := GroupIncomingFiles([]IncomingFile{a, changed})
		if err != nil {
			t.Fatal(err)
		}
		if len(split) != 2 {
			t.Errorf("edition field case %d should split, got %#v", index, split)
		}
	}
}

func TestGroupIncomingFilesFallbackAndMultiValueNormalization(t *testing.T) {
	root := uuid.MustParse("00000000-0000-4000-8000-000000000021")
	a := incomingTestFile("00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000011", root.String(), "a/song.flac", map[string][]string{"ALBUM": {"Café;Live / Set"}, "ARTIST": {"First", "Second", "First"}, "DATE": {"2000"}})
	b := incomingTestFile("00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000012", "00000000-0000-4000-8000-000000000022", "b/song.flac", map[string][]string{"ALBUM": {"Cafe\u0301;Live / Set"}, "ARTIST": {"First", "Second"}, "DATE": {"2000"}})
	groups, err := GroupIncomingFiles([]IncomingFile{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("normalized full arrays should group across roots, got %#v", groups)
	}
	if !reflect.DeepEqual(incomingFields(a.Tags).Artist, []string{"first", "second"}) {
		t.Fatalf("artist order/dedup not retained: %#v", incomingFields(a.Tags).Artist)
	}
	if normalizeIncomingTag("  X/Y;Z  ") != "x/y;z" {
		t.Fatal("separator was split or trimmed incorrectly")
	}
	if got := incomingFields(map[string][]string{"ALBUM": {"", "  "}, "ALBUMARTIST": {"  "}, "ARTIST": {" Artist "}}); got.Album != nil || got.Artist[0] != "artist" {
		t.Fatalf("empty exclusion or full artist fallback failed: %#v", got)
	}
	blankAlbumArtist := incomingFields(map[string][]string{"ALBUMARTIST": {"", "  "}, "ARTIST": {"Fallback Artist"}})
	if !reflect.DeepEqual(blankAlbumArtist.Artist, []string{"fallback artist"}) {
		t.Fatalf("blank album artist did not fall back to artist: %#v", blankAlbumArtist.Artist)
	}
	insufficientA := incomingTestFile("00000000-0000-4000-8000-000000000003", "00000000-0000-4000-8000-000000000013", root.String(), "one/song.flac", map[string][]string{"ALBUM": {"Album"}, "ARTIST": {"Artist"}})
	insufficientB := incomingTestFile("00000000-0000-4000-8000-000000000004", "00000000-0000-4000-8000-000000000014", root.String(), "two/song.flac", map[string][]string{"ALBUM": {"Album"}, "ARTIST": {"Artist"}})
	fallback, err := GroupIncomingFiles([]IncomingFile{insufficientA, insufficientB})
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback) != 2 {
		t.Fatalf("insufficient keys in different folders merged: %#v", fallback)
	}
	blankDateCatalogA := incomingTestFile("00000000-0000-4000-8000-000000000005", "00000000-0000-4000-8000-000000000015", root.String(), "three/song.flac", map[string][]string{"ALBUM": {"Album"}, "ARTIST": {"Artist"}, "DATE": {"", "  "}, "CATALOG": {" "}})
	blankDateCatalogB := incomingTestFile("00000000-0000-4000-8000-000000000006", "00000000-0000-4000-8000-000000000016", root.String(), "four/song.flac", map[string][]string{"ALBUM": {"Album"}, "ARTIST": {"Artist"}, "DATE": {" "}, "CATALOG": {"", "  "}})
	fallback, err = GroupIncomingFiles([]IncomingFile{blankDateCatalogA, blankDateCatalogB})
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback) != 2 {
		t.Fatalf("blank date/catalog values merged files from different folders: %#v", fallback)
	}
}

func TestGroupIncomingFilesRejectsInvalidRelativePaths(t *testing.T) {
	for _, relativePath := range []string{"", "/absolute/song.flac", "../outside/song.flac", "folder/../../outside/song.flac"} {
		t.Run(relativePath, func(t *testing.T) {
			file := incomingTestFile("00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000011", "00000000-0000-4000-8000-000000000021", relativePath, nil)
			if _, err := GroupIncomingFiles([]IncomingFile{file}); err == nil {
				t.Fatalf("invalid relative path %q accepted", relativePath)
			}
		})
	}
}

func TestGroupIncomingFilesLocationsDeduplicateAndMBIDDiagnostics(t *testing.T) {
	file := incomingTestFile("00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000011", "00000000-0000-4000-8000-000000000021", "dir/song.flac", map[string][]string{"MUSICBRAINZ_ALBUMID": {"bad", "123e4567-e89b-12d3-a456-426614174000", "123E4567-E89B-12D3-A456-426614174000", "also-bad"}})
	file.Locations = append(file.Locations, file.Locations[0])
	groups, err := GroupIncomingFiles([]IncomingFile{file})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Members) != 1 || len(groups[0].Diagnostics) != 1 || groups[0].Diagnostics[0].Code != "invalid_release_mbid" {
		t.Fatalf("duplicate path or invalid ID handling failed: %#v", groups)
	}
	file.Tags["MUSICBRAINZ_ALBUMID"] = []string{"123e4567-e89b-12d3-a456-426614174000", "123e4567-e89b-12d3-a456-426614174001"}
	groups, err = GroupIncomingFiles([]IncomingFile{file})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Diagnostics[0].Code != "multiple_release_mbids" {
		t.Fatalf("multiple IDs were not isolated unresolved: %#v", groups)
	}
}

func TestGroupIncomingFilesRequiresStableVariantIdentity(t *testing.T) {
	file := incomingTestFile("00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000011", "00000000-0000-4000-8000-000000000021", "song.flac", nil)
	file.VariantID = uuid.Nil
	if _, err := GroupIncomingFiles([]IncomingFile{file}); err == nil {
		t.Fatal("missing variant ID accepted")
	}
}

func TestIncomingGroupDraftOperationsArePureAndValidateReferences(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	groups := []IncomingGroup{{ID: uuid.New(), Revision: "r1", Members: []uuid.UUID{a, b}, Diagnostics: []IncomingGroupingDiagnostic{{VariantID: a, Code: "diag-a", Values: []string{"a"}}, {VariantID: b, Code: "diag-b", Values: []string{"b"}}}}, {ID: uuid.New(), Revision: "r2", Members: []uuid.UUID{c}, Diagnostics: []IncomingGroupingDiagnostic{{VariantID: c, Code: "diag-c", Values: []string{"c"}}}}}
	original := cloneIncomingGroups(groups)
	base, err := IncomingGroupsRevision(groups)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := DraftIncomingMerge(groups, base, groups[0].ID, groups[1].ID)
	if err != nil || len(merged.Groups) != 1 || len(merged.Groups[0].Members) != 3 {
		t.Fatalf("merge failed: %#v %v", merged, err)
	}
	split, err := DraftIncomingSplit(groups, base, groups[0].ID, []uuid.UUID{a})
	if err != nil || len(split.Groups) != 3 {
		t.Fatalf("split failed: %#v %v", split, err)
	}
	moved, err := DraftIncomingMove(groups, base, []uuid.UUID{c}, groups[0].ID)
	if err != nil || len(moved.Groups) != 1 || len(moved.Groups[0].Members) != 3 {
		t.Fatalf("move failed: %#v %v", moved, err)
	}
	if !reflect.DeepEqual(groups, original) {
		t.Fatal("draft helper mutated originals")
	}
	assertIncomingDraftDiagnosticMembers(t, merged.Groups, map[uuid.UUID]bool{a: true, b: true, c: true})
	assertIncomingDraftDiagnosticMembers(t, split.Groups, map[uuid.UUID]bool{a: true, b: true, c: true})
	assertIncomingDraftDiagnosticMembers(t, moved.Groups, map[uuid.UUID]bool{a: true, b: true, c: true})
	if _, err := DraftIncomingMerge(groups, base, uuid.New(), groups[1].ID); err == nil {
		t.Fatal("unknown group reference accepted")
	}
	if _, err := DraftIncomingSplit(groups, base, groups[0].ID, []uuid.UUID{uuid.New()}); err == nil {
		t.Fatal("unknown split member accepted")
	}
	if _, err := DraftIncomingMove(groups, base, []uuid.UUID{uuid.New()}, groups[0].ID); err == nil {
		t.Fatal("unknown move member accepted")
	}
	if _, err := DraftIncomingMerge(groups, "stale", groups[0].ID, groups[1].ID); err == nil {
		t.Fatal("stale base revision accepted")
	}
}

func TestDraftIncomingMoveReassignsDiagnosticsAndChangesAggregateRevision(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	groups := []IncomingGroup{
		{ID: uuid.New(), Revision: "source-revision", Members: []uuid.UUID{a, b}, Diagnostics: []IncomingGroupingDiagnostic{{VariantID: a, Code: "source-a"}, {VariantID: b, Code: "source-b"}}},
		{ID: uuid.New(), Revision: "target-revision", Members: []uuid.UUID{c, d}, Diagnostics: []IncomingGroupingDiagnostic{{VariantID: c, Code: "target-c"}, {VariantID: d, Code: "target-d"}}},
	}
	original := cloneIncomingGroups(groups)
	baseRevision, err := IncomingGroupsRevision(groups)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := DraftIncomingMove(groups, baseRevision, []uuid.UUID{a}, groups[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.BaseRevision != baseRevision {
		t.Fatalf("move changed base revision: got %q want %q", draft.BaseRevision, baseRevision)
	}
	if len(draft.Groups) != 2 {
		t.Fatalf("move should keep both non-empty groups: %#v", draft.Groups)
	}
	assertIncomingDraftDiagnosticMembers(t, draft.Groups, map[uuid.UUID]bool{a: true, b: true, c: true, d: true})
	for _, group := range draft.Groups {
		for _, diagnostic := range group.Diagnostics {
			contains := false
			for _, member := range group.Members {
				contains = contains || member == diagnostic.VariantID
			}
			if !contains {
				t.Errorf("group %s retained stale diagnostic for %s", group.ID, diagnostic.VariantID)
			}
		}
	}
	newRevision, err := IncomingGroupsRevision(draft.Groups)
	if err != nil {
		t.Fatal(err)
	}
	if newRevision == baseRevision {
		t.Fatal("moving membership did not change aggregate revision")
	}
	if !reflect.DeepEqual(groups, original) {
		t.Fatal("move mutated original groups")
	}
}

func assertIncomingDraftDiagnosticMembers(t *testing.T, groups []IncomingGroup, expected map[uuid.UUID]bool) {
	t.Helper()
	seen := make(map[uuid.UUID]bool)
	for _, group := range groups {
		members := make(map[uuid.UUID]bool, len(group.Members))
		for _, member := range group.Members {
			members[member] = true
		}
		for _, diagnostic := range group.Diagnostics {
			if !members[diagnostic.VariantID] {
				t.Errorf("group %s has diagnostic for non-member %s", group.ID, diagnostic.VariantID)
			}
			seen[diagnostic.VariantID] = true
		}
	}
	for variant := range expected {
		if !seen[variant] {
			t.Errorf("diagnostic for variant %s was lost", variant)
		}
	}
}

func incomingTestFile(variant, analysis, root, relative string, tags map[string][]string) IncomingFile {
	return IncomingFile{VariantID: uuid.MustParse(variant), CaptureAnalysisID: uuid.MustParse(analysis), Locations: []IncomingLocation{{RootID: uuid.MustParse(root), RelativePath: relative, SizeBytes: 10, Mtime: "mtime"}}, Tags: tags}
}
