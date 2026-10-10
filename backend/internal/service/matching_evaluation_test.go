package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestMatchingEvaluationDoesNotAdoptRevisionPublishedAfterBaseline(t *testing.T) {
	fileA, fileB, groupAID, groupBID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	workA, workB, analysisA, analysisB := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	groupA := IncomingGroup{ID: groupAID, Revision: "group-a-r1", Members: []uuid.UUID{fileA}}
	groupB := IncomingGroup{ID: groupBID, Revision: "group-b-r1", Members: []uuid.UUID{fileB}}
	locationA := IncomingLocation{RootID: uuid.New(), WorkID: workA, LocationID: uuid.New(), ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "album-a/track.flac", SizeBytes: 10, Mtime: "1970-01-01T00:00:01Z"}
	locationB := IncomingLocation{RootID: uuid.New(), WorkID: workB, LocationID: uuid.New(), ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "album-b/track.flac", SizeBytes: 10, Mtime: "1970-01-01T00:00:01Z"}
	grouping := IncomingGroupingSnapshot{Epoch: 1, Groups: []IncomingGroup{groupA, groupB}, Files: []IncomingFile{
		{VariantID: fileA, CaptureAnalysisID: analysisA, CaptureIdentity: "capture-a-r1", Locations: []IncomingLocation{locationA}, Tags: map[string][]string{"ALBUM": {"Album A"}}},
		{VariantID: fileB, CaptureAnalysisID: analysisB, CaptureIdentity: "capture-b-r1", Locations: []IncomingLocation{locationB}, Tags: map[string][]string{"ALBUM": {"Album B"}}},
	}}
	provider := persistence.ProviderMatchingSnapshot{
		Source:                     persistence.ProviderSource{ID: uuid.New(), ProviderID: uuid.New(), Namespace: "mb", Endpoint: "https://mb.example", ConfigurationIdentity: "config"},
		Responses:                  map[string]*persistence.ProviderCachedResponse{"release-query": nil, "recording-query": nil},
		AcceptedProjectionRevision: "projection-r1",
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	store := &baselineRaceStore{
		headers: map[uuid.UUID]*persistence.IncomingMatchEvaluationHeader{groupAID: {EvaluationRevision: "revision-0-a"}, groupBID: {EvaluationRevision: "revision-0-b"}},
		candidates: map[uuid.UUID][]persistence.IncomingMatchCandidate{
			groupAID: {baselineRaceCandidate(fileA, provider.Source.ProviderID, provider.Source.ID, uuid.New(), .61)},
			groupBID: {baselineRaceCandidate(fileB, provider.Source.ProviderID, provider.Source.ID, uuid.New(), .62)},
		},
	}
	captures := &blockingMatchingCaptures{
		entered: entered, resume: resume,
		captures: []persistence.IncomingGroupingCapture{
			{WorkID: workA, VariantID: fileA, CaptureAnalysisID: &analysisA, RootID: locationA.RootID, LocationID: locationA.LocationID, ConfiguredPath: locationA.ConfiguredPath, InventoryPath: locationA.InventoryPath, RelativePath: locationA.RelativePath, SizeBytes: locationA.SizeBytes, Mtime: time.Unix(1, 0), Tags: json.RawMessage(`{"ALBUM":["Album A"]}`), ProbeResult: json.RawMessage(`{}`), Ready: true},
			{WorkID: workB, VariantID: fileB, CaptureAnalysisID: &analysisB, RootID: locationB.RootID, LocationID: locationB.LocationID, ConfiguredPath: locationB.ConfiguredPath, InventoryPath: locationB.InventoryPath, RelativePath: locationB.RelativePath, SizeBytes: locationB.SizeBytes, Mtime: time.Unix(1, 0), Tags: json.RawMessage(`{"ALBUM":["Album B"]}`), ProbeResult: json.RawMessage(`{}`), Ready: true},
		},
	}
	evaluator := newMatchingEvaluationService(
		staticMatchingGroups{snapshot: grouping}, captures,
		staticMatchingProvider{snapshot: provider}, store,
		staticMatchingConfig{config: settings.MusicBrainzConfig{Mode: "self-hosted", BaseURL: provider.Source.Endpoint, Identity: "config"}},
	)
	scope := MatchingEvaluationQueryScope{
		GroupIDs:          []uuid.UUID{groupAID, groupBID},
		ReleaseKeysByFile: map[uuid.UUID][]string{fileA: {}, fileB: {}},
		SearchKeysByFile:  map[uuid.UUID]MatchingSearchKeys{fileA: {Release: "release-query", Recording: "recording-query"}, fileB: {Release: "release-query", Recording: "recording-query"}},
	}
	aDone := make(chan error, 1)
	go func() { _, err := evaluator.EvaluateAndPersist(context.Background(), scope); aDone <- err }()
	<-entered // A captured both revisions before it began reading capture/provider inputs.
	if got := store.snapshotGetIDs(); len(got) != 2 || got[0] != groupAID || got[1] != groupBID {
		t.Fatalf("baseline reads before input loading = %v, want both requested groups", got)
	}
	newCandidateA := baselineRaceCandidate(fileA, provider.Source.ProviderID, provider.Source.ID, uuid.New(), .94)
	newCandidateB := baselineRaceCandidate(fileB, provider.Source.ProviderID, provider.Source.ID, uuid.New(), .93)
	bEvaluation := baselineRaceEvaluation(groupA, provider, newCandidateA)
	bEvaluation.Groups = append(bEvaluation.Groups, persistence.IncomingMatchGroupEvaluation{GroupID: groupBID, GroupRevision: groupB.Revision, Members: []persistence.IncomingMatchMemberFence{{FileMemberID: fileB, WorkID: workB}}, Candidates: []persistence.IncomingMatchCandidate{newCandidateB}})
	if err := store.ReplaceIfCurrent(context.Background(), bEvaluation, map[uuid.UUID]*string{groupAID: baselineRaceStringPointer("revision-0-a"), groupBID: baselineRaceStringPointer("revision-0-b")}); err != nil {
		t.Fatalf("publish evaluator B result: %v", err)
	}
	newState := make(map[uuid.UUID]struct {
		header     *persistence.IncomingMatchEvaluationHeader
		candidates []persistence.IncomingMatchCandidate
	}, 2)
	for _, groupID := range scope.GroupIDs {
		header, candidates, err := store.Get(context.Background(), groupID)
		if err != nil || header == nil || len(candidates) != 1 {
			t.Fatalf("B result for %s = %#v %#v err=%v", groupID, header, candidates, err)
		}
		newState[groupID] = struct {
			header     *persistence.IncomingMatchEvaluationHeader
			candidates []persistence.IncomingMatchCandidate
		}{header, candidates}
	}
	close(resume)
	if err := <-aDone; !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("A stale baseline result = %v, want conflict", err)
	}
	for _, groupID := range scope.GroupIDs {
		gotHeader, gotCandidates, err := store.Get(context.Background(), groupID)
		published := newState[groupID]
		if err != nil || gotHeader == nil || gotHeader.EvaluationRevision != published.header.EvaluationRevision || !equalRaceCandidates(gotCandidates, published.candidates) {
			t.Fatalf("A overwrote B's accepted group %s header/candidate positions: header=%#v candidates=%#v err=%v", groupID, gotHeader, gotCandidates, err)
		}
	}
}

type staticMatchingGroups struct{ snapshot IncomingGroupingSnapshot }

func (groups staticMatchingGroups) Snapshot(context.Context) (IncomingGroupingSnapshot, error) {
	return groups.snapshot, nil
}

type blockingMatchingCaptures struct {
	entered, resume chan struct{}
	captures        []persistence.IncomingGroupingCapture
}

func (captures *blockingMatchingCaptures) CurrentCaptures(context.Context) ([]persistence.IncomingGroupingCapture, error) {
	close(captures.entered)
	<-captures.resume
	return captures.captures, nil
}

type staticMatchingProvider struct {
	snapshot persistence.ProviderMatchingSnapshot
}

func (provider staticMatchingProvider) GetMusicBrainzMatchingSnapshot(context.Context, string, string, []string, []string) (persistence.ProviderMatchingSnapshot, error) {
	return provider.snapshot, nil
}

type staticMatchingConfig struct{ config settings.MusicBrainzConfig }

func (config staticMatchingConfig) GetMusicBrainzConfig(context.Context) (settings.MusicBrainzConfig, error) {
	return config.config, nil
}

type baselineRaceStore struct {
	mu         sync.Mutex
	headers    map[uuid.UUID]*persistence.IncomingMatchEvaluationHeader
	candidates map[uuid.UUID][]persistence.IncomingMatchCandidate
	getIDs     []uuid.UUID
	next       int
}

func (store *baselineRaceStore) Get(_ context.Context, id uuid.UUID) (*persistence.IncomingMatchEvaluationHeader, []persistence.IncomingMatchCandidate, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.getIDs = append(store.getIDs, id)
	header := store.headers[id]
	if header == nil {
		return nil, nil, nil
	}
	copyHeader := *header
	return &copyHeader, cloneRaceCandidates(store.candidates[id]), nil
}

func (store *baselineRaceStore) snapshotGetIDs() []uuid.UUID {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]uuid.UUID(nil), store.getIDs...)
}
func (store *baselineRaceStore) ReplaceIfCurrent(_ context.Context, evaluation persistence.IncomingMatchEvaluation, expected map[uuid.UUID]*string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, group := range evaluation.Groups {
		current := store.headers[group.GroupID]
		token := expected[group.GroupID]
		if current == nil {
			if token != nil {
				return persistence.ErrIncomingMatchConflict
			}
		} else if token == nil || current.EvaluationRevision != *token {
			return persistence.ErrIncomingMatchConflict
		}
	}
	for _, group := range evaluation.Groups {
		store.next++
		store.headers[group.GroupID] = &persistence.IncomingMatchEvaluationHeader{GroupID: group.GroupID, EvaluationRevision: fmt.Sprintf("revision-%d", store.next)}
		store.candidates[group.GroupID] = cloneRaceCandidates(group.Candidates)
	}
	return nil
}

func baselineRaceEvaluation(group IncomingGroup, provider persistence.ProviderMatchingSnapshot, candidate persistence.IncomingMatchCandidate) persistence.IncomingMatchEvaluation {
	return persistence.IncomingMatchEvaluation{ExpectedGroupsRevision: "groups-r1", ExpectedGroupingEpoch: 1, PolicyVersion: MatchingPolicyVersion, Provider: persistence.IncomingMatchProviderFence{ProviderID: provider.Source.ProviderID, ProviderSourceID: provider.Source.ID, Namespace: provider.Source.Namespace, Endpoint: provider.Source.Endpoint, ConfigurationIdentity: provider.Source.ConfigurationIdentity, CacheKey: "release-query", AcceptedProjectionRevision: provider.AcceptedProjectionRevision, ReleaseKeys: []string{}}, Groups: []persistence.IncomingMatchGroupEvaluation{{GroupID: group.ID, GroupRevision: group.Revision, Members: []persistence.IncomingMatchMemberFence{{FileMemberID: group.Members[0], WorkID: uuid.New()}}, Candidates: []persistence.IncomingMatchCandidate{candidate}}}}
}
func baselineRaceCandidate(file, provider, source, release uuid.UUID, score float64) persistence.IncomingMatchCandidate {
	return persistence.IncomingMatchCandidate{FileMemberID: file, ProviderID: provider, ProviderSourceID: source, ReleaseID: release, Score: score, Evidence: json.RawMessage(`{"kind":"race-test"}`), Positions: []persistence.IncomingMatchPosition{{TrackID: uuid.New(), Medium: 0, Position: 0}}}
}
func baselineRaceStringPointer(value string) *string { return &value }
func cloneRaceCandidates(values []persistence.IncomingMatchCandidate) []persistence.IncomingMatchCandidate {
	result := append([]persistence.IncomingMatchCandidate(nil), values...)
	for i := range result {
		result[i].Positions = append([]persistence.IncomingMatchPosition(nil), values[i].Positions...)
	}
	return result
}
func equalRaceCandidates(a, b []persistence.IncomingMatchCandidate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ReleaseID != b[i].ReleaseID || a[i].FileMemberID != b[i].FileMemberID || a[i].Score != b[i].Score || len(a[i].Positions) != len(b[i].Positions) {
			return false
		}
		for j := range a[i].Positions {
			if a[i].Positions[j] != b[i].Positions[j] {
				return false
			}
		}
	}
	return true
}

func TestMatchingEvaluationUsesSeparateReleaseAndAssignmentProfiles(t *testing.T) {
	fileID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New()
	runnerUpID, runnerUpTrackID := uuid.New(), uuid.New()
	group := IncomingGroup{ID: uuid.New(), Revision: "group-r1", Members: []uuid.UUID{fileID}}
	inputs := []MatchingInput{
		syntheticMatchingInput(fileID, "release-a", 0, 0, .95, .2),
		syntheticMatchingInput(fileID, "release-b", 0, 1, .8, .99),
	}
	provider := persistence.ProviderMatchingSnapshot{Source: persistence.ProviderSource{ID: uuid.New(), ProviderID: uuid.New(), Namespace: "mb", Endpoint: "https://mb.example", ConfigurationIdentity: "config"}, AcceptedProjectionRevision: "projection-r1", Releases: []persistence.MatchingRelease{
		{ID: releaseID, ProviderKey: "release-a", Tracks: []persistence.MatchingReleaseTrack{{ID: trackID, Medium: 0, Position: 0, RecordingProviderKey: "recording"}}},
		{ID: runnerUpID, ProviderKey: "release-b", Tracks: []persistence.MatchingReleaseTrack{{ID: runnerUpTrackID, Medium: 0, Position: 1, RecordingProviderKey: "recording"}}},
	}}
	result, decision, reason, err := makeMatchingGroupResult(context.Background(), MatchingEvaluationSnapshot{Provider: provider, Grouping: IncomingGroupingSnapshot{Epoch: 1, Groups: []IncomingGroup{group}}}, "groups-r1", group, []persistence.IncomingMatchMemberFence{{FileMemberID: fileID, WorkID: uuid.New()}}, inputs, []string{"release-a", "release-b"}, map[string]*string{"release-search": nil, "recording-search": nil})
	if err != nil {
		t.Fatal(err)
	}
	if decision != MatchingDecisionReview || reason == "" {
		t.Fatalf("decision = %q (%s), want review", decision, reason)
	}
	if len(result.Groups) != 1 || len(result.Groups[0].Candidates) != 2 {
		t.Fatalf("candidates = %#v", result.Groups)
	}
	var candidate persistence.IncomingMatchCandidate
	for _, current := range result.Groups[0].Candidates {
		if current.ReleaseID == releaseID {
			candidate = current
		}
	}
	if candidate.ReleaseID == uuid.Nil || candidate.Score != .95 || len(candidate.Positions) != 1 || candidate.Positions[0].TrackID != trackID {
		t.Fatalf("candidate did not retain selected release score/all positions: %#v", candidate)
	}
	var evidence struct {
		Version  int                          `json:"schema_version"`
		Decision MatchingGroupDecision        `json:"group_decision"`
		Edges    []map[string]json.RawMessage `json:"edge_evidence"`
	}
	if err := json.Unmarshal(candidate.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Version != 1 || evidence.Decision != MatchingDecisionReview || len(evidence.Edges) != 1 || len(evidence.Edges[0]["release_profile"]) == 0 || len(evidence.Edges[0]["assignment_profile"]) == 0 {
		t.Fatalf("profile evidence missing: %s", candidate.Evidence)
	}
	var groupEvidence map[string]any
	if err := json.Unmarshal(result.Groups[0].Evidence, &groupEvidence); err != nil || groupEvidence["schema_version"] != float64(1) || groupEvidence["group_decision"] != string(MatchingDecisionReview) {
		t.Fatalf("versioned group decision evidence = %s, err=%v", result.Groups[0].Evidence, err)
	}
}

func TestMatchingEvaluationPersistsExactUniqueSolverAssignmentDeterministically(t *testing.T) {
	fileA, fileB := uuid.New(), uuid.New()
	releaseID := uuid.New()
	trackOne, trackTwo := uuid.New(), uuid.New()
	group := IncomingGroup{ID: uuid.New(), Revision: "group-r1", Members: []uuid.UUID{fileA, fileB}}
	rootID := uuid.New()
	provider := persistence.ProviderMatchingSnapshot{
		Source:                     persistence.ProviderSource{ID: uuid.New(), ProviderID: uuid.New(), Namespace: "mb", Endpoint: "https://mb.example", ConfigurationIdentity: "config"},
		AcceptedProjectionRevision: "projection-r1",
		Releases: []persistence.MatchingRelease{{ID: releaseID, ProviderKey: "release-key", Tracks: []persistence.MatchingReleaseTrack{
			{ID: trackOne, Medium: 1, Position: 1}, {ID: trackTwo, Medium: 1, Position: 2},
		}}},
	}
	inputs := []MatchingInput{
		syntheticMatchingInput(fileA, "release-key", 1, 1, .95, .80),
		syntheticMatchingInput(fileA, "release-key", 1, 2, .95, .95),
		syntheticMatchingInput(fileB, "release-key", 1, 1, .95, .94),
		syntheticMatchingInput(fileB, "release-key", 1, 2, .95, .80),
	}
	snapshot := MatchingEvaluationSnapshot{
		Provider: provider,
		Grouping: IncomingGroupingSnapshot{Epoch: 1, Groups: []IncomingGroup{group}, Files: []IncomingFile{
			{VariantID: fileA, Locations: []IncomingLocation{{RootID: rootID, RelativePath: "album/a.flac"}}},
			{VariantID: fileB, Locations: []IncomingLocation{{RootID: rootID, RelativePath: "album/b.flac"}}},
		}},
	}
	members := []persistence.IncomingMatchMemberFence{{FileMemberID: fileA, WorkID: uuid.New()}, {FileMemberID: fileB, WorkID: uuid.New()}}
	evaluate := func(edges []MatchingInput) MatchingEvaluationDecision {
		result, selected, _, err := makeMatchingGroupResult(t.Context(), snapshot, "groups-r1", group, members, edges, []string{"release-key"}, map[string]*string{})
		if err != nil {
			t.Fatal(err)
		}
		if selected != MatchingDecisionSelected {
			t.Fatalf("group decision = %q, want selected", selected)
		}
		decision, err := DecodeMatchingDecision(result.Groups[0].Evidence)
		if err != nil {
			t.Fatalf("decode persisted decision: %v", err)
		}
		return decision
	}
	first := evaluate(inputs)
	reversed := append([]MatchingInput(nil), inputs...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	second := evaluate(reversed)
	if first.Outcome != MatchingDecisionOutcomeUnique || first.SelectedRelease == nil || first.SelectedRelease.ProviderReleaseID != releaseID || first.SelectedRelease.ProviderReleaseKey != "release-key" {
		t.Fatalf("decision release/outcome = %#v", first)
	}
	want := map[uuid.UUID]uuid.UUID{fileA: trackTwo, fileB: trackOne}
	if len(first.Assignments) != len(want) {
		t.Fatalf("assignments = %#v", first.Assignments)
	}
	for _, assignment := range first.Assignments {
		if want[assignment.FileMemberID] != assignment.ProviderTrackID {
			t.Errorf("solver assignment for %s = %s, want %s (all assignments: %#v)", assignment.FileMemberID, assignment.ProviderTrackID, want[assignment.FileMemberID], first.Assignments)
		}
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("decision changed with edge order:\n%s\n%s", firstJSON, secondJSON)
	}
}

func TestMatchingEvaluationTieRetainsSelectedReleaseWithoutAssignments(t *testing.T) {
	fileID, releaseID, trackOne, trackTwo := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	group := IncomingGroup{ID: uuid.New(), Revision: "group-r1", Members: []uuid.UUID{fileID}}
	provider := persistence.ProviderMatchingSnapshot{
		Source:   persistence.ProviderSource{ID: uuid.New(), ProviderID: uuid.New(), Namespace: "mb", Endpoint: "https://mb.example", ConfigurationIdentity: "config"},
		Releases: []persistence.MatchingRelease{{ID: releaseID, ProviderKey: "release-key", Tracks: []persistence.MatchingReleaseTrack{{ID: trackOne, Medium: 1, Position: 1}, {ID: trackTwo, Medium: 1, Position: 2}}}},
	}
	snapshot := MatchingEvaluationSnapshot{Provider: provider, Grouping: IncomingGroupingSnapshot{Files: []IncomingFile{{VariantID: fileID, Locations: []IncomingLocation{{RootID: uuid.New(), RelativePath: "album/a.flac"}}}}}}
	inputs := []MatchingInput{
		syntheticMatchingInput(fileID, "release-key", 1, 1, .95, .9),
		syntheticMatchingInput(fileID, "release-key", 1, 2, .95, .9),
	}
	result, selected, _, err := makeMatchingGroupResult(t.Context(), snapshot, "groups-r1", group, []persistence.IncomingMatchMemberFence{{FileMemberID: fileID, WorkID: uuid.New()}}, inputs, []string{"release-key"}, map[string]*string{})
	if err != nil {
		t.Fatal(err)
	}
	if selected != MatchingDecisionReview {
		t.Fatalf("group decision = %q, want review", selected)
	}
	decision, err := DecodeMatchingDecision(result.Groups[0].Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != MatchingDecisionOutcomeTie || decision.SelectedRelease == nil || decision.SelectedRelease.ProviderReleaseID != releaseID || len(decision.Assignments) != 0 {
		t.Fatalf("tie decision = %#v", decision)
	}
}

func TestMatchingEvaluationInfeasibleSelectedReleaseDoesNotAbortOtherGroup(t *testing.T) {
	badA, badB, good := uuid.New(), uuid.New(), uuid.New()
	releaseAID, releaseBID := uuid.New(), uuid.New()
	trackA, trackB1, trackB2 := uuid.New(), uuid.New(), uuid.New()
	rootID := uuid.New()
	badGroup := IncomingGroup{ID: uuid.New(), Revision: "bad-r1", Members: []uuid.UUID{badA, badB}}
	goodGroup := IncomingGroup{ID: uuid.New(), Revision: "good-r1", Members: []uuid.UUID{good}}
	provider := persistence.ProviderMatchingSnapshot{
		Source: persistence.ProviderSource{ID: uuid.New(), ProviderID: uuid.New(), Namespace: "mb", Endpoint: "https://mb.example", ConfigurationIdentity: "config"},
		Releases: []persistence.MatchingRelease{
			{ID: releaseAID, ProviderKey: "release-a", Tracks: []persistence.MatchingReleaseTrack{{ID: trackA, Medium: 1, Position: 1}}},
			{ID: releaseBID, ProviderKey: "release-b", Tracks: []persistence.MatchingReleaseTrack{{ID: trackB1, Medium: 1, Position: 1}, {ID: trackB2, Medium: 1, Position: 2}}},
		},
	}
	snapshot := MatchingEvaluationSnapshot{Provider: provider, Grouping: IncomingGroupingSnapshot{Files: []IncomingFile{
		{VariantID: badA, Locations: []IncomingLocation{{RootID: rootID, RelativePath: "bad/a.flac"}}},
		{VariantID: badB, Locations: []IncomingLocation{{RootID: rootID, RelativePath: "bad/b.flac"}}},
		{VariantID: good, Locations: []IncomingLocation{{RootID: uuid.New(), RelativePath: "good/a.flac"}}},
	}}}
	inputs := []MatchingInput{
		syntheticMatchingInput(badA, "release-a", 1, 1, .95, .9),
		syntheticMatchingInput(badB, "release-a", 1, 1, .95, .9),
		// A lower-ranked release is feasible, but selected release assignment must not fall back to it.
		syntheticMatchingInput(badA, "release-b", 1, 1, .8, .9),
		syntheticMatchingInput(badB, "release-b", 1, 2, .8, .9),
		syntheticMatchingInput(good, "release-a", 1, 1, .95, .9),
		syntheticMatchingInput(good, "release-b", 1, 2, .8, .9),
	}
	evaluate := func(group IncomingGroup) MatchingGroupEvaluationResult {
		members := make([]persistence.IncomingMatchMemberFence, 0, len(group.Members))
		groupInputs := make([]MatchingInput, 0)
		for _, member := range group.Members {
			members = append(members, persistence.IncomingMatchMemberFence{FileMemberID: member, WorkID: uuid.New()})
			for _, input := range inputs {
				if input.FileID == member {
					groupInputs = append(groupInputs, input)
				}
			}
		}
		result, decision, reason, err := makeMatchingGroupResult(t.Context(), snapshot, "groups-r1", group, members, groupInputs, []string{"release-a", "release-b"}, map[string]*string{"query": nil})
		if err != nil {
			t.Fatalf("evaluate group %s: %v", group.ID, err)
		}
		return MatchingGroupEvaluationResult{GroupID: group.ID, Decision: decision, Reason: reason, Result: result}
	}
	badResult := evaluate(badGroup)
	goodResult := evaluate(goodGroup)
	if badResult.Decision != MatchingDecisionReview {
		t.Fatalf("infeasible group decision = %q, want review", badResult.Decision)
	}
	badDecision, err := DecodeMatchingDecision(badResult.Result.Groups[0].Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if badDecision.Outcome != MatchingDecisionOutcomeInfeasible || badDecision.SelectedRelease == nil || badDecision.SelectedRelease.ProviderReleaseID != releaseAID || len(badDecision.Assignments) != 0 {
		t.Fatalf("infeasible group decision evidence = %#v", badDecision)
	}
	goodDecision, err := DecodeMatchingDecision(goodResult.Result.Groups[0].Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if goodDecision.Outcome != MatchingDecisionOutcomeUnique || len(goodDecision.Assignments) != 1 || goodDecision.Assignments[0].ProviderTrackID != trackA {
		t.Fatalf("other group decision evidence = %#v", goodDecision)
	}
	combined, err := combineMatchingGroupResults([]MatchingGroupEvaluationResult{badResult, goodResult}, provider, []string{"release-a", "release-b"}, []string{"query"})
	if err != nil {
		t.Fatalf("combine evaluated groups: %v", err)
	}
	if len(combined.Groups) != 2 {
		t.Fatalf("combined persisted evaluation group count = %d, want 2", len(combined.Groups))
	}
	for _, groupEval := range combined.Groups {
		decision, err := DecodeMatchingDecision(groupEval.Evidence)
		if err != nil {
			t.Fatalf("decode combined group %s: %v", groupEval.GroupID, err)
		}
		if groupEval.GroupID == goodGroup.ID && decision.Outcome != MatchingDecisionOutcomeUnique {
			t.Fatalf("other group's decision was lost: %#v", decision)
		}
	}
}

func TestMatchingEvaluationDoesNotDropMembersWithoutEdgesOrFallback(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	releaseA, releaseB := uuid.New(), uuid.New()
	group := IncomingGroup{ID: uuid.New(), Revision: "group-r1", Members: []uuid.UUID{first, second}}
	inputs := []MatchingInput{
		syntheticMatchingInput(first, "release-a", 0, 0, .9, .9),
		syntheticMatchingInput(first, "release-b", 0, 1, .8, .99),
	}
	provider := persistence.ProviderMatchingSnapshot{Source: persistence.ProviderSource{ID: uuid.New(), ProviderID: uuid.New(), Namespace: "mb", Endpoint: "https://mb.example", ConfigurationIdentity: "config"}, AcceptedProjectionRevision: "projection-r1", Releases: []persistence.MatchingRelease{
		{ID: releaseA, ProviderKey: "release-a", Tracks: []persistence.MatchingReleaseTrack{{ID: uuid.New(), Medium: 0, Position: 0}}},
		{ID: releaseB, ProviderKey: "release-b", Tracks: []persistence.MatchingReleaseTrack{{ID: uuid.New(), Medium: 0, Position: 1}}},
	}}
	members := []persistence.IncomingMatchMemberFence{{FileMemberID: first, WorkID: uuid.New()}, {FileMemberID: second, WorkID: uuid.New()}}
	result, decision, _, err := makeMatchingGroupResult(context.Background(), MatchingEvaluationSnapshot{Provider: provider}, "groups-r1", group, members, inputs, []string{"release-a", "release-b"}, map[string]*string{"release-search": nil, "recording-search": nil})
	if err != nil {
		t.Fatal(err)
	}
	if decision != MatchingDecisionReview {
		t.Fatalf("decision = %q, want review because second member has no edge", decision)
	}
	if len(result.Groups[0].Members) != 2 {
		t.Fatalf("missing-edge member was dropped: %#v", result.Groups[0].Members)
	}
	if len(result.Groups[0].Candidates) != 2 {
		t.Fatalf("candidate edges were lost: %#v", result.Groups[0].Candidates)
	}
	evaluationDecision, err := DecodeMatchingDecision(result.Groups[0].Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if evaluationDecision.Outcome != MatchingDecisionOutcomeInfeasible || len(evaluationDecision.Assignments) != 0 || evaluationDecision.SelectedRelease != nil {
		t.Fatalf("infeasible decision = %#v", evaluationDecision)
	}
}

func TestMatchingEvaluationEqualReleaseSumsAreReview(t *testing.T) {
	one, two := uuid.New(), uuid.New()
	group := IncomingGroup{ID: uuid.New(), Revision: "group-r1", Members: []uuid.UUID{one, two}}
	inputs := []MatchingInput{
		syntheticMatchingInput(one, "release-a", 0, 0, .8, .9), syntheticMatchingInput(two, "release-a", 0, 1, .7, .9),
		syntheticMatchingInput(one, "release-b", 0, 2, .7, .9), syntheticMatchingInput(two, "release-b", 0, 3, .8, .9),
	}
	provider := persistence.ProviderMatchingSnapshot{Source: persistence.ProviderSource{ID: uuid.New(), ProviderID: uuid.New(), Namespace: "mb", Endpoint: "https://mb.example", ConfigurationIdentity: "config"}, AcceptedProjectionRevision: "projection-r1", Releases: []persistence.MatchingRelease{
		{ID: uuid.New(), ProviderKey: "release-a", Tracks: []persistence.MatchingReleaseTrack{{ID: uuid.New(), Position: 0}, {ID: uuid.New(), Position: 1}}},
		{ID: uuid.New(), ProviderKey: "release-b", Tracks: []persistence.MatchingReleaseTrack{{ID: uuid.New(), Position: 2}, {ID: uuid.New(), Position: 3}}},
	}}
	_, decision, _, err := makeMatchingGroupResult(context.Background(), MatchingEvaluationSnapshot{Provider: provider}, "groups-r1", group, []persistence.IncomingMatchMemberFence{{FileMemberID: one, WorkID: uuid.New()}, {FileMemberID: two, WorkID: uuid.New()}}, inputs, []string{"release-a", "release-b"}, map[string]*string{"release-search": nil, "recording-search": nil})
	if err != nil {
		t.Fatal(err)
	}
	if decision != MatchingDecisionReview {
		t.Fatalf("equal release sums decision = %q, want review", decision)
	}
}

func TestMatchingEvaluationDefersWholeSnapshotWhenAnyGroupUnready(t *testing.T) {
	readyID, pendingID := uuid.New(), uuid.New()
	snapshot := MatchingEvaluationSnapshot{Grouping: IncomingGroupingSnapshot{Epoch: 1, Groups: []IncomingGroup{
		{ID: uuid.New(), Revision: "ready", Members: []uuid.UUID{readyID}},
		{ID: uuid.New(), Revision: "pending", Members: []uuid.UUID{pendingID}, UnreadyMembers: []uuid.UUID{pendingID}},
	}, Files: []IncomingFile{
		{VariantID: readyID, CaptureAnalysisID: uuid.New(), CaptureIdentity: "ready-capture", Locations: []IncomingLocation{{RootID: uuid.New(), RelativePath: "ready/track.flac"}}},
		{VariantID: pendingID, CaptureAnalysisID: uuid.New(), CaptureIdentity: "pending-capture", Locations: []IncomingLocation{{RootID: uuid.New(), RelativePath: "pending/track.flac"}}},
	}}}
	results, err := EvaluateMatchingSnapshot(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v", results)
	}
	for _, result := range results {
		if result.Decision != MatchingDecisionDeferred || len(result.Result.Groups) != 0 {
			t.Fatalf("partial publication possible: %#v", result)
		}
	}
}

func TestMatchingEvaluationKeepsMissingQueryScoreDistinctFromZero(t *testing.T) {
	zero := persistence.ProviderMatchingSnapshot{SearchScores: map[string]map[string]persistence.MatchingSearchScore{
		"exact-query": {"release-id": {Present: true, Value: 0}},
	}}
	if value := matchingSnapshotSearchScore(zero, "exact-query", "release-id"); value == nil || *value != 0 {
		t.Fatalf("explicit zero score = %v, want present zero", value)
	}
	if value := matchingSnapshotSearchScore(zero, "exact-query", "other-release"); value != nil {
		t.Fatalf("missing result score = %v, want missing", value)
	}
	if value := matchingSnapshotSearchScore(zero, "other-query", "release-id"); value != nil {
		t.Fatalf("missing query score = %v, want missing", value)
	}
}

func syntheticMatchingInput(file uuid.UUID, release string, medium, position int, releaseScore, assignmentScore float64) MatchingInput {
	return MatchingInput{FileID: file, ReleaseID: release, RecordingID: "recording", Medium: medium, Track: position,
		ReleaseScore:    MatchingScore{Profile: ReleaseMatchingProfile, PolicyVersion: MatchingPolicyVersion, Confidence: releaseScore},
		AssignmentScore: MatchingScore{Profile: RecordingReleaseMatchingProfile, PolicyVersion: MatchingPolicyVersion, Confidence: assignmentScore}}
}
