package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func evaluate(ctx context.Context, input fixture, opts options) (report, error) {
	result := report{
		Name: input.Name, Provenance: input.Provenance, Policy: service.MatchingPolicyVersion,
		ExpectedID: input.ExpectedReleaseID, BaselineSource: input.BaselineSource,
		BaselineIDs: input.BaselineIDs, Observed: input.BaselineObserved, ShortlistLimit: opts.limit,
		Timings: make(map[string]float64),
	}
	captured, err := readyMembers(input.Captures)
	if err != nil {
		return report{}, err
	}
	result.ReadyFiles = len(captured)
	if opts.stage == "baseline" || opts.stage == "all" {
		if err := result.replayBaseline(ctx, input); err != nil {
			return report{}, err
		}
		if opts.stage == "baseline" {
			return result, nil
		}
	}
	start := time.Now()
	result.Ranking, err = service.RankMatchingExperimentCatalog(captured, input.Headers, opts.limit)
	result.Timings["rank_headers"] = milliseconds(start)
	if err != nil {
		return report{}, fmt.Errorf("rank candidate headers: %w", err)
	}
	var ids []string
	result.ExpectedStatus = "not_in_captured_catalog"
	if input.ExpectedReleaseID == "" {
		result.ExpectedStatus = "no_known_edition"
	}
	for _, row := range result.Ranking {
		if row.ReleaseID == input.ExpectedReleaseID {
			result.ExpectedStatus = "outside_shortlist"
			if row.Shortlisted {
				result.ExpectedStatus = "shortlisted"
			}
		}
		if row.Shortlisted {
			ids = append(ids, row.ReleaseID)
		}
	}
	if opts.stage == "shortlist" {
		return result, nil
	}
	if slices.Contains(ids, input.ExpectedReleaseID) {
		if _, found := input.Layouts[input.ExpectedReleaseID]; !found {
			result.ExpectedStatus = "shortlisted_tracklist_not_captured"
		}
	}
	if err := result.replay(ctx, input, ids, start); err != nil {
		return report{}, err
	}
	return result, nil
}

func (r *report) replayBaseline(ctx context.Context, input fixture) error {
	if len(input.BaselineIDs) == 0 {
		return nil
	}
	start := time.Now()
	layouts, missing, err := readLayouts(input.Layouts, input.BaselineIDs)
	r.Timings["baseline_tracklist_decode"] = milliseconds(start)
	if err != nil {
		return err
	}
	if len(missing) != 0 {
		return fmt.Errorf("baseline edition tracklists not captured: %v", missing)
	}
	start = time.Now()
	baseline, err := scoreLayouts(input, layouts)
	r.Timings["baseline_pair_scoring"] = milliseconds(start)
	if err != nil {
		return err
	}
	start = time.Now()
	r.Baseline, err = evaluateReleaseFirst(ctx, baseline)
	r.Timings["baseline_release_first"] = milliseconds(start)
	if err != nil || len(input.BaselineObserved) == 0 {
		return err
	}
	var saved savedDecision
	if err := json.Unmarshal(input.BaselineObserved, &saved); err != nil {
		return fmt.Errorf("decode observed baseline: %w", err)
	}
	if saved.Outcome != "" {
		match := saved.Outcome == r.Baseline.Outcome &&
			saved.SelectionReason == r.Baseline.Selection.Reason &&
			saved.SelectedID == r.Baseline.Selection.SelectedReleaseID &&
			(saved.Assignments == nil || maps.Equal(saved.Assignments, r.Baseline.Assignment.Choices))
		r.BaselineMatch = &match
	}
	return nil
}

func (r *report) replay(ctx context.Context, input fixture, ids []string, pathStart time.Time) error {
	start := time.Now()
	layouts, missing, err := readLayouts(input.Layouts, ids)
	r.Timings["tracklist_decode"] = milliseconds(start)
	if err != nil {
		return err
	}
	r.NotHydrated = missing
	r.LayoutScope = "complete_shortlist"
	if len(missing) > 0 {
		r.LayoutScope = "partial_shortlist_diagnostic_only"
	}
	if len(layouts) == 0 {
		return r.maybeMemoryControl(ctx, input)
	}
	start = time.Now()
	scored, err := scoreLayouts(input, layouts)
	r.Timings["pair_scoring"] = milliseconds(start)
	if err != nil {
		return err
	}
	r.ScoredPairs = len(scored.Inputs)
	start = time.Now()
	comparison, err := service.CompareMatchingExperimentLayouts(ctx, scored)
	r.Timings["whole_layout_solve"] = milliseconds(start)
	r.Timings["whole_layout_path"] = milliseconds(pathStart)
	if err != nil {
		return err
	}
	clearUnprovedAcrossTies(&comparison, missing)
	r.Compared = &comparison
	start = time.Now()
	r.ReleaseFirst, err = evaluateReleaseFirst(ctx, scored)
	r.Timings["release_first_on_shortlist"] = milliseconds(start)
	if err != nil {
		return err
	}
	// Reverse unordered collections to check repeatability on the exact
	// prepared scores without changing tag or credit order.
	slices.Reverse(scored.Members)
	slices.Reverse(scored.Files)
	slices.Reverse(scored.Inputs)
	slices.Reverse(scored.ReleaseLayouts)
	start = time.Now()
	repeated, err := service.CompareMatchingExperimentLayouts(ctx, scored)
	r.Timings["whole_layout_with_reused_scores"] = milliseconds(start)
	if err != nil {
		return err
	}
	clearUnprovedAcrossTies(&repeated, missing)
	r.CachedVerified = reflect.DeepEqual(comparison, repeated)
	if !r.CachedVerified {
		return errors.New("whole-layout comparison changed when replaying the same scores")
	}
	return r.maybeMemoryControl(ctx, input)
}

func clearUnprovedAcrossTies(comparison *service.MatchingExperimentLayoutComparison, missing []string) {
	if len(missing) == 0 {
		return
	}
	for i := range comparison.Layouts {
		comparison.Layouts[i].AcrossLayoutTie = false
	}
}

func (r *report) maybeMemoryControl(ctx context.Context, input fixture) error {
	if len(input.MemoryTags) == 0 {
		return nil
	}
	start := time.Now()
	if input.ExpectedReleaseID == "" {
		return errors.New("corrected tag control requires a known edition")
	}
	layouts, missing, err := readLayouts(input.Layouts, []string{input.ExpectedReleaseID})
	if err != nil {
		return err
	}
	if len(missing) != 0 {
		return fmt.Errorf("corrected tag control edition is missing: %v", missing)
	}
	input.Captures = slices.Clone(input.Captures)
	unused := maps.Clone(input.MemoryTags)
	for i := range input.Captures {
		capture := &input.Captures[i]
		overlay, ok := input.MemoryTags[capture.VariantID]
		if !ok {
			continue
		}
		var tags map[string][]string
		if err := json.Unmarshal(capture.Tags, &tags); err != nil {
			return fmt.Errorf("decode original control tags: %w", err)
		}
		if tags == nil {
			tags = make(map[string][]string)
		}
		maps.Copy(tags, overlay)
		capture.Tags, err = json.Marshal(tags)
		if err != nil {
			return err
		}
		delete(unused, capture.VariantID)
	}
	if len(unused) != 0 {
		return errors.New("corrected tag control references an unknown member")
	}
	scored, err := scoreLayouts(input, layouts)
	if err != nil {
		return err
	}
	comparison, err := service.CompareMatchingExperimentLayouts(ctx, scored)
	if err != nil {
		return err
	}
	r.MemoryControl = &comparison
	r.Timings["corrected_tags_control"] = milliseconds(start)
	return nil
}

func readyMembers(captures []persistence.IncomingGroupingCapture) ([]persistence.MatchingAcquisitionCapturedMember, error) {
	byID := make(map[uuid.UUID]persistence.MatchingAcquisitionCapturedMember)
	for _, capture := range captures {
		if !capture.Ready || capture.CaptureAnalysisID == nil || *capture.CaptureAnalysisID == uuid.Nil ||
			capture.VariantID == uuid.Nil || capture.WorkID == uuid.Nil {
			return nil, fmt.Errorf("expected a ready saved capture for %s", capture.VariantID)
		}
		if prior, ok := byID[capture.VariantID]; ok && !reflect.DeepEqual(prior.Tags, capture.Tags) {
			return nil, fmt.Errorf("conflicting saved tags for %s", capture.VariantID)
		}
		byID[capture.VariantID] = persistence.MatchingAcquisitionCapturedMember{
			MemberID: capture.VariantID, Tags: capture.Tags,
		}
	}
	ids := slices.SortedFunc(maps.Keys(byID), func(a, b uuid.UUID) int {
		return slices.Compare(a[:], b[:])
	})
	members := make([]persistence.MatchingAcquisitionCapturedMember, 0, len(ids))
	for _, id := range ids {
		members = append(members, byID[id])
	}
	return members, nil
}

func readLayouts(frozen map[string]json.RawMessage, ids []string) ([]persistence.MatchingRelease, []string, error) {
	layouts := make([]persistence.MatchingRelease, 0, len(ids))
	missing := make([]string, 0)
	for _, id := range ids {
		raw, found := frozen[id]
		if !found {
			missing = append(missing, id)
			continue
		}
		var layout persistence.MatchingRelease
		if err := json.Unmarshal(raw, &layout); err != nil {
			return nil, nil, fmt.Errorf("decode edition %s: %w", id, err)
		}
		if layout.ProviderKey != id || !layout.Complete {
			return nil, nil, fmt.Errorf("edition %s has no complete frozen tracklist", id)
		}
		layouts = append(layouts, layout)
	}
	return layouts, missing, nil
}

func scoreLayouts(input fixture, layouts []persistence.MatchingRelease) (service.MatchingGroupEvaluationInput, error) {
	snapshot := persistence.ProviderMatchingSnapshot{Releases: layouts, SearchScores: input.SearchScores}
	edges, err := service.BuildMatchingInputs(input.Captures, snapshot, input.SearchKeys, nil)
	if err != nil {
		return service.MatchingGroupEvaluationInput{}, fmt.Errorf("score frozen file/position pairs: %w", err)
	}
	result := service.MatchingGroupEvaluationInput{ReleaseLayouts: layouts, Inputs: edges}
	rows := make(map[uuid.UUID]int)
	for _, capture := range input.Captures {
		key := capture.VariantID
		index, found := rows[key]
		if !found {
			index = len(result.Files)
			rows[key] = index
			result.Files = append(result.Files, service.IncomingFile{VariantID: key})
			result.Members = append(result.Members, persistence.IncomingMatchMemberFence{FileMemberID: key})
		}
		file := &result.Files[index]
		if capture.RootID == uuid.Nil || capture.RelativePath == "" {
			return service.MatchingGroupEvaluationInput{}, fmt.Errorf("member %s has no containing folder identity", key)
		}
		file.Locations = append(file.Locations, service.IncomingLocation{
			RootID: capture.RootID, RelativePath: capture.RelativePath,
		})
	}
	return result, nil
}

func evaluateReleaseFirst(ctx context.Context, input service.MatchingGroupEvaluationInput) (*baselineDecision, error) {
	// The unchanged solver can take a long time on dense editions.
	// A timeout is a diagnostic, not an infeasible result.
	step, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	computed, err := service.EvaluateMatchingGroupFromInputs(step, input)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return &baselineDecision{Reason: "unchanged solver exceeded the replay time limit", Outcome: "indeterminate"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("evaluate unchanged release-first kernel: %w", err)
	}
	return &baselineDecision{
		Selection: computed.ReleaseSelection, Decision: computed.Decision,
		Reason: computed.Reason, Outcome: computed.Outcome, Assignment: computed.Assignment,
	}, nil
}
