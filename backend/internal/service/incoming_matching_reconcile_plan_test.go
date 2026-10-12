package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestPlanIncomingMatchingReconcileUsesCompleteArtistValuesDeterministically(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	capture := selectedReconcileCapture(`{"ARTIST":["ignored"],"ALBUMARTIST":["Björk / Guðmundsdóttir; Co.","Second Artist"]}`)
	capture.VariantID = memberID
	snapshot := persistence.IncomingMatchingReconcileSnapshot{Groups: []persistence.IncomingMatchingReconcileGroup{{
		Group:          persistence.IncomingGroupingGroup{ID: groupID, Revision: "revision-1", Members: []uuid.UUID{memberID}},
		Authority:      persistence.IncomingGroupMatchingAuthority{GroupID: groupID, Mode: "automatic"},
		MemberCaptures: map[uuid.UUID][]persistence.IncomingGroupingCapture{memberID: {capture}},
	}}}
	first, err := PlanIncomingMatchingReconcile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanIncomingMatchingReconcile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.PlannedGroups) != 1 || first.PlannedGroups[0] != second.PlannedGroups[0] {
		t.Fatalf("plans are not deterministic: %+v / %+v", first, second)
	}
	want := `(artist:"Björk \/ Guðmundsdóttir; Co." OR artist:"björk guðmundsdóttir co" OR artist:"bjork gudmundsdottir co" OR artist:"Second Artist" OR artist:"second artist")`
	if first.PlannedGroups[0].Query != want {
		t.Fatalf("query = %q, want %q", first.PlannedGroups[0].Query, want)
	}
}

func TestPlanIncomingMatchingReconcileFallsBackOnlyWhenAlbumArtistAbsent(t *testing.T) {
	for _, test := range []struct {
		name string
		tags string
		want string
	}{
		{name: "absent", tags: `{"ARTIST":["Fallback"]}`, want: `artist:"Fallback"`},
		{name: "present but empty", tags: `{"ALBUMARTIST":[" "],"ARTIST":["Must not fallback"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			groupID, memberID := uuid.New(), uuid.New()
			capture := selectedReconcileCapture(test.tags)
			capture.VariantID = memberID
			snapshot := persistence.IncomingMatchingReconcileSnapshot{Groups: []persistence.IncomingMatchingReconcileGroup{{
				Group:          persistence.IncomingGroupingGroup{ID: groupID, Revision: "r", Members: []uuid.UUID{memberID}},
				Authority:      persistence.IncomingGroupMatchingAuthority{GroupID: groupID, Mode: "automatic"},
				MemberCaptures: map[uuid.UUID][]persistence.IncomingGroupingCapture{memberID: {capture}},
			}}}
			plan, err := PlanIncomingMatchingReconcile(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if len(plan.PlannedGroups) != 0 || len(plan.DeferredGroupIDs) != 1 || plan.DeferredGroupIDs[0] != groupID {
					t.Fatalf("empty-album-artist result = %+v", plan)
				}
				return
			}
			if len(plan.PlannedGroups) != 1 || !strings.Contains(plan.PlannedGroups[0].Query, test.want) {
				t.Fatalf("plan = %+v, want query %q", plan, test.want)
			}
		})
	}
}

func TestPlanIncomingMatchingReconcileSkipsManualAuthorityAndUnreadyGroups(t *testing.T) {
	manualID, unreadyID, chosenID, memberID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	group := func(id uuid.UUID, authority string, ready bool) persistence.IncomingMatchingReconcileGroup {
		capture := selectedReconcileCapture(`{"ARTIST":["Artist"]}`)
		capture.VariantID = memberID
		result := persistence.IncomingMatchingReconcileGroup{
			Group:          persistence.IncomingGroupingGroup{ID: id, Revision: "r", Members: []uuid.UUID{memberID}},
			Authority:      persistence.IncomingGroupMatchingAuthority{GroupID: id, Mode: authority},
			MemberCaptures: map[uuid.UUID][]persistence.IncomingGroupingCapture{memberID: {capture}},
		}
		if !ready {
			result.Group.UnreadyMembers = []uuid.UUID{memberID}
			result.Group.Members = nil
		}
		return result
	}
	plan, err := PlanIncomingMatchingReconcile(persistence.IncomingMatchingReconcileSnapshot{Groups: []persistence.IncomingMatchingReconcileGroup{
		group(manualID, "manual", true), group(unreadyID, "automatic", false), group(chosenID, "automatic", true),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.PlannedGroups) != 1 || plan.PlannedGroups[0].GroupID != chosenID {
		t.Fatalf("planned groups = %+v, want %s", plan.PlannedGroups, chosenID)
	}
}

func TestPlanIncomingMatchingReconcilePlansEveryEligibleGroup(t *testing.T) {
	firstID, secondID := uuid.MustParse("00000000-0000-4000-8000-000000000001"), uuid.MustParse("00000000-0000-4000-8000-000000000002")
	firstMember, secondMember := uuid.New(), uuid.New()
	group := func(id, member uuid.UUID, artist string) persistence.IncomingMatchingReconcileGroup {
		capture := selectedReconcileCapture(`{"ARTIST":["` + artist + `"]}`)
		capture.VariantID = member
		return persistence.IncomingMatchingReconcileGroup{
			Group:          persistence.IncomingGroupingGroup{ID: id, Revision: "revision", Members: []uuid.UUID{member}},
			Authority:      persistence.IncomingGroupMatchingAuthority{GroupID: id, Mode: "automatic"},
			MemberCaptures: map[uuid.UUID][]persistence.IncomingGroupingCapture{member: {capture}},
		}
	}
	plan, err := PlanIncomingMatchingReconcile(persistence.IncomingMatchingReconcileSnapshot{Groups: []persistence.IncomingMatchingReconcileGroup{
		group(secondID, secondMember, "Second"), group(firstID, firstMember, "First"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.PlannedGroups) != 2 || plan.PlannedGroups[0].GroupID != firstID || plan.PlannedGroups[1].GroupID != secondID {
		t.Fatalf("planned groups = %+v, want ordered groups %s, %s", plan.PlannedGroups, firstID, secondID)
	}
}

func TestPlanIncomingMatchingReconcileUsesSelectedCaptureAcrossLocations(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	unready := persistence.IncomingGroupingCapture{WorkID: uuid.New(), VariantID: memberID}
	selected := selectedReconcileCapture(`{"ARTIST":["Selected artist"]}`)
	selected.WorkID = uuid.New()
	selected.VariantID = memberID
	selected.RootID = uuid.New()
	selected.RelativePath = "z/selected.flac"
	group := persistence.IncomingMatchingReconcileGroup{
		Group:          persistence.IncomingGroupingGroup{ID: groupID, Revision: "r", Members: []uuid.UUID{memberID}},
		Authority:      persistence.IncomingGroupMatchingAuthority{GroupID: groupID, Mode: "automatic"},
		MemberCaptures: map[uuid.UUID][]persistence.IncomingGroupingCapture{memberID: {unready, selected}},
	}
	plan, err := PlanIncomingMatchingReconcile(persistence.IncomingMatchingReconcileSnapshot{Groups: []persistence.IncomingMatchingReconcileGroup{group}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.PlannedGroups) != 1 || !strings.Contains(plan.PlannedGroups[0].Query, `artist:"Selected artist"`) {
		t.Fatalf("plan = %+v, want selected metadata", plan)
	}
}

func TestPlanIncomingMatchingReconcileRejectsConflictingSelectedMetadata(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	first := selectedReconcileCapture(`{"ARTIST":["First"]}`)
	first.VariantID = memberID
	second := selectedReconcileCapture(`{"ARTIST":["Second"]}`)
	second.VariantID = memberID
	second.CaptureAnalysisID = first.CaptureAnalysisID
	group := persistence.IncomingMatchingReconcileGroup{
		Group:          persistence.IncomingGroupingGroup{ID: groupID, Revision: "r", Members: []uuid.UUID{memberID}},
		Authority:      persistence.IncomingGroupMatchingAuthority{GroupID: groupID, Mode: "automatic"},
		MemberCaptures: map[uuid.UUID][]persistence.IncomingGroupingCapture{memberID: {first, second}},
	}
	if _, err := PlanIncomingMatchingReconcile(persistence.IncomingMatchingReconcileSnapshot{Groups: []persistence.IncomingMatchingReconcileGroup{group}}); err == nil || !strings.Contains(err.Error(), "conflicting selected metadata captures") {
		t.Fatalf("error = %v, want conflicting selected metadata error", err)
	}
}

func TestPlanIncomingMatchingFallbackUsesFiniteTypedStagesAndWholeTagValues(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	capture := selectedReconcileCapture(`{"ALBUM":["Björk / Live; Vol. 2"],"TITLE":["Песня / версия; live"]}`)
	capture.VariantID = memberID
	snapshot := persistence.IncomingMatchingReconcileSnapshot{Groups: []persistence.IncomingMatchingReconcileGroup{{
		Group:          persistence.IncomingGroupingGroup{ID: groupID, Revision: "rev", Members: []uuid.UUID{memberID}},
		Authority:      persistence.IncomingGroupMatchingAuthority{GroupID: groupID, Mode: "automatic"},
		MemberCaptures: map[uuid.UUID][]persistence.IncomingGroupingCapture{memberID: {capture}},
	}}}
	plans, err := PlanIncomingMatchingFallbacks(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want one plan", plans)
	}
	got := plans[0].Plan
	if got.SchemaVersion != TypedFallbackQueryPlanVersion || len(got.Stages) != 3 {
		t.Fatalf("plan = %+v, want current version and three ordered stages", got)
	}
	if got.Stages[0].Kind != "album_track" || got.Stages[1].Kind != "album" || got.Stages[2].Kind != "track" {
		t.Fatalf("stage order = %+v", got.Stages)
	}
	for _, stage := range got.Stages {
		if stage.Offset != 0 || stage.Limit != 10 || !strings.Contains(stage.Query, "/") || !strings.Contains(stage.Query, ";") {
			t.Fatalf("stage = %+v, expected bounded query preserving '/' and ';'", stage)
		}
	}
	if !strings.Contains(got.Stages[0].Query, `) AND (`) || !strings.Contains(got.Stages[0].Query, `release:`) || !strings.Contains(got.Stages[0].Query, `recording:`) {
		t.Fatalf("combined query = %q, want grouped release and recording AND", got.Stages[0].Query)
	}
}

func TestPlanIncomingMatchingFallbackPlansAlbumOnlyAsAlbumStage(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	plans, err := PlanIncomingMatchingFallbacks(fallbackSnapshot(groupID, memberID, `{"ALBUM":["Only album"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want one plan", plans)
	}
	stages := plans[0].Plan.Stages
	if plans[0].Plan.SchemaVersion != TypedFallbackQueryPlanVersion || len(stages) != 1 {
		t.Fatalf("plan = %+v, want current version and a single album stage", plans[0].Plan)
	}
	if stages[0].Kind != "album" || stages[0].Offset != 0 || stages[0].Limit != 10 || !strings.Contains(stages[0].Query, `release:"Only album"`) {
		t.Fatalf("stage = %+v, want bounded album query", stages[0])
	}
}

func TestPlanIncomingMatchingFallbackPlansTitleOnlyAsTrackStage(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	plans, err := PlanIncomingMatchingFallbacks(fallbackSnapshot(groupID, memberID, `{"TITLE":["Only title"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want one plan", plans)
	}
	stages := plans[0].Plan.Stages
	if plans[0].Plan.SchemaVersion != TypedFallbackQueryPlanVersion || len(stages) != 1 {
		t.Fatalf("plan = %+v, want current version and a single track stage", plans[0].Plan)
	}
	if stages[0].Kind != "track" || stages[0].Offset != 0 || stages[0].Limit != 10 || !strings.Contains(stages[0].Query, `recording:"Only title"`) {
		t.Fatalf("stage = %+v, want bounded track query", stages[0])
	}
}

func TestPlanIncomingMatchingFallbackRetainsTypedEmptyPlanWhenTagsAbsent(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	plans, err := PlanIncomingMatchingFallbacks(fallbackSnapshot(groupID, memberID, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].GroupID != groupID {
		t.Fatalf("plans = %+v, want one retained target for %s", plans, groupID)
	}
	if plans[0].Plan.SchemaVersion != TypedFallbackQueryPlanVersion || plans[0].Plan.Stages == nil || len(plans[0].Plan.Stages) != 0 {
		t.Fatalf("plan = %+v, want current version and a typed empty stage list", plans[0].Plan)
	}
}

func TestPlanIncomingMatchingFallbackExcludesArtistBearingGroups(t *testing.T) {
	for _, test := range []struct {
		name string
		tags string
	}{
		{name: "album artist", tags: `{"ALBUMARTIST":["Artist"],"ALBUM":["Album"],"TITLE":["Title"]}`},
		{name: "artist fallback", tags: `{"ARTIST":["Artist"],"ALBUM":["Album"],"TITLE":["Title"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			groupID, memberID := uuid.New(), uuid.New()
			plans, err := PlanIncomingMatchingFallbacks(fallbackSnapshot(groupID, memberID, test.tags))
			if err != nil {
				t.Fatal(err)
			}
			if len(plans) != 0 {
				t.Fatalf("plans = %+v, want artist-bearing group excluded from fallbacks", plans)
			}
		})
	}
}

func TestPlanIncomingMatchingFallbackTreatsEmptyAlbumArtistAsNonArtist(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	plans, err := PlanIncomingMatchingFallbacks(fallbackSnapshot(groupID, memberID, `{"ALBUMARTIST":[" "],"ARTIST":["Must not fallback"],"ALBUM":["Album"],"TITLE":["Title"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want one plan for present-but-empty album artist", plans)
	}
	if len(plans[0].Plan.Stages) != 3 {
		t.Fatalf("stages = %+v, want album_track, album, track", plans[0].Plan.Stages)
	}
}

func fallbackSnapshot(groupID, memberID uuid.UUID, tags string) persistence.IncomingMatchingReconcileSnapshot {
	capture := selectedReconcileCapture(tags)
	capture.VariantID = memberID
	return persistence.IncomingMatchingReconcileSnapshot{Groups: []persistence.IncomingMatchingReconcileGroup{{
		Group:          persistence.IncomingGroupingGroup{ID: groupID, Revision: "rev", Members: []uuid.UUID{memberID}},
		Authority:      persistence.IncomingGroupMatchingAuthority{GroupID: groupID, Mode: "automatic"},
		MemberCaptures: map[uuid.UUID][]persistence.IncomingGroupingCapture{memberID: {capture}},
	}}}
}

func selectedReconcileCapture(tags string) persistence.IncomingGroupingCapture {
	analysisID := uuid.New()
	return persistence.IncomingGroupingCapture{
		VariantID: uuid.New(), CaptureAnalysisID: &analysisID, Ready: true,
		Tags: json.RawMessage(tags),
	}
}
