package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anyascii/go"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
)

type IncomingMatchingReconcilePlan struct {
	PlannedGroups    []IncomingMatchingReconcileTarget `json:"planned_groups"`
	DeferredGroupIDs []uuid.UUID                       `json:"deferred_group_ids"`
}

const TypedFallbackQueryPlanVersion = 1

type TypedFallbackQueryPlan struct {
	SchemaVersion int                       `json:"schema_version"`
	Stages        []TypedFallbackQueryStage `json:"stages"`
}

type TypedFallbackQueryStage struct {
	Kind   string `json:"kind"`
	Query  string `json:"query"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

type IncomingMatchingFallbackTarget struct {
	GroupID       uuid.UUID              `json:"group_id"`
	GroupRevision string                 `json:"group_revision"`
	Plan          TypedFallbackQueryPlan `json:"plan"`
}

type IncomingMatchingReconcileTarget struct {
	GroupID       uuid.UUID `json:"group_id"`
	GroupRevision string    `json:"group_revision"`
	Query         string    `json:"query"`
}

// PlanIncomingMatchingReconcile deterministically plans every automatic, ready
// group with artist metadata. Groups without usable artists are reported as
// deferred so consumers cannot mistake them for completed work.
func PlanIncomingMatchingReconcile(snapshot persistence.IncomingMatchingReconcileSnapshot) (IncomingMatchingReconcilePlan, error) {
	groups := append([]persistence.IncomingMatchingReconcileGroup(nil), snapshot.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Group.ID.String() < groups[j].Group.ID.String() })
	plan := IncomingMatchingReconcilePlan{
		PlannedGroups:    make([]IncomingMatchingReconcileTarget, 0),
		DeferredGroupIDs: make([]uuid.UUID, 0),
	}
	for _, group := range groups {
		if group.Authority.Mode != "automatic" || group.Group.ID == uuid.Nil || group.Group.Revision == "" || len(group.Group.UnreadyMembers) > 0 {
			continue
		}
		artists, err := reconcileGroupArtists(group)
		if err != nil {
			return IncomingMatchingReconcilePlan{}, err
		}
		if len(artists) == 0 {
			plan.DeferredGroupIDs = append(plan.DeferredGroupIDs, group.Group.ID)
			continue
		}
		terms := make([]string, len(artists))
		for i, artist := range artists {
			terms[i] = `artist:"` + escapeLucenePhrase(artist) + `"`
		}
		query := terms[0]
		if len(terms) > 1 {
			query = "(" + strings.Join(terms, " OR ") + ")"
		}
		plan.PlannedGroups = append(plan.PlannedGroups, IncomingMatchingReconcileTarget{GroupID: group.Group.ID, GroupRevision: group.Group.Revision, Query: query})
	}
	return plan, nil
}

// PlanIncomingMatchingFallbacks builds deterministic finite typed search plans
// for ready automatic groups without artist metadata. Each metadata dimension is
// planned independently: album and title together yield album_track, album then
// track; album alone yields album; title alone yields track; a group with
// neither still yields a typed empty plan so no eligible group is silently
// dropped. Artist-bearing groups are excluded using the exact reconcile
// artist semantics (album artist when present per file, otherwise artist), so a
// present-but-empty album artist is not treated as artist-bearing. This is a
// planner contract only; reconciliation deliberately does not admit or execute
// these plans until the fallback stage engine is available.
func PlanIncomingMatchingFallbacks(snapshot persistence.IncomingMatchingReconcileSnapshot) ([]IncomingMatchingFallbackTarget, error) {
	groups := append([]persistence.IncomingMatchingReconcileGroup(nil), snapshot.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Group.ID.String() < groups[j].Group.ID.String() })
	plans := make([]IncomingMatchingFallbackTarget, 0)
	for _, group := range groups {
		if group.Authority.Mode != "automatic" || group.Group.ID == uuid.Nil || group.Group.Revision == "" || len(group.Group.UnreadyMembers) > 0 {
			continue
		}
		view, err := incomingFilesFromCaptures(flattenReconcileCaptures(group))
		if err != nil {
			return nil, fmt.Errorf("select incoming fallback metadata captures: %w", err)
		}
		files := make(map[uuid.UUID]IncomingFile, len(view.Files))
		for _, file := range view.Files {
			files[file.VariantID] = file
		}
		members := append([]uuid.UUID(nil), group.Group.Members...)
		sort.Slice(members, func(i, j int) bool { return members[i].String() < members[j].String() })
		tagSets := make([]map[string][]string, 0, len(members))
		for _, member := range members {
			file, ok := files[member]
			if !ok {
				continue
			}
			tagSets = append(tagSets, file.Tags)
		}
		fallbackPlan, artistBearing := policy.PlanFallbackQueries(tagSets)
		if artistBearing {
			continue
		}
		plans = append(plans, IncomingMatchingFallbackTarget{GroupID: group.Group.ID, GroupRevision: group.Group.Revision, Plan: TypedFallbackQueryPlan{SchemaVersion: fallbackPlan.SchemaVersion, Stages: convertFallbackStages(fallbackPlan.Stages)}})
	}
	return plans, nil
}

func flattenReconcileCaptures(group persistence.IncomingMatchingReconcileGroup) []persistence.IncomingGroupingCapture {
	members := append([]uuid.UUID(nil), group.Group.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i].String() < members[j].String() })
	captures := make([]persistence.IncomingGroupingCapture, 0)
	for _, member := range members {
		captures = append(captures, group.MemberCaptures[member]...)
	}
	return captures
}

func convertFallbackStages(stages []policy.TypedFallbackQueryStage) []TypedFallbackQueryStage {
	converted := make([]TypedFallbackQueryStage, len(stages))
	for index, stage := range stages {
		converted[index] = TypedFallbackQueryStage{Kind: stage.Kind, Query: stage.Query, Offset: stage.Offset, Limit: stage.Limit}
	}
	return converted
}

func reconcileGroupArtists(group persistence.IncomingMatchingReconcileGroup) ([]string, error) {
	memberIDs := append([]uuid.UUID(nil), group.Group.Members...)
	sort.Slice(memberIDs, func(i, j int) bool { return memberIDs[i].String() < memberIDs[j].String() })
	captures := make([]persistence.IncomingGroupingCapture, 0)
	for _, memberID := range memberIDs {
		captures = append(captures, group.MemberCaptures[memberID]...)
	}
	view, err := incomingFilesFromCaptures(captures)
	if err != nil {
		return nil, fmt.Errorf("select incoming matching metadata captures: %w", err)
	}
	filesByID := make(map[uuid.UUID]IncomingFile, len(view.Files))
	for _, file := range view.Files {
		filesByID[file.VariantID] = file
	}
	artists := make([]string, 0)
	for _, memberID := range memberIDs {
		file, selected := filesByID[memberID]
		if !selected {
			continue
		}
		if len(file.Tags) == 0 {
			continue
		}
		tags := file.Tags
		albumArtist, present := sourceTagValues(tags, "ALBUMARTIST", "ALBUM ARTIST", "ALBUM_ARTIST")
		values := albumArtist
		if !present {
			values, _ = sourceTagValues(tags, "ARTIST")
		}
		for _, value := range values {
			if !hasNonWhitespaceRune(value) {
				continue
			}
			artists = append(artists, value, canonicalMatchingText(value), canonicalMatchingText(anyascii.Transliterate(value)))
		}
	}
	return stableUniqueMatchingValues(artists), nil
}

func sourceTagValues(tags map[string][]string, aliases ...string) ([]string, bool) {
	lookup := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		lookup[strings.ToUpper(strings.TrimSpace(alias))] = struct{}{}
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0)
	present := false
	for _, key := range keys {
		if _, ok := lookup[strings.ToUpper(strings.TrimSpace(key))]; !ok {
			continue
		}
		present = true
		values = append(values, tags[key]...)
	}
	return values, present
}

func escapeLucenePhrase(value string) string {
	var escaped strings.Builder
	for _, character := range value {
		if strings.ContainsRune(`+-&|!(){}[]^"~*?:\\/`, character) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}
