package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

var ErrMatchingEvaluationDeferred = errors.New("matching evaluation deferred")

type matchingGroupingSnapshotReader interface {
	Snapshot(context.Context) (IncomingGroupingSnapshot, error)
}

type matchingCaptureReader interface {
	CurrentCaptures(context.Context) ([]persistence.IncomingGroupingCapture, error)
}

type matchingProviderSnapshotReader interface {
	GetMusicBrainzMatchingSnapshot(context.Context, string, string, []string, []string) (persistence.ProviderMatchingSnapshot, error)
}

type matchingEvaluationStore interface {
	Get(context.Context, uuid.UUID) (*persistence.IncomingMatchEvaluationHeader, []persistence.IncomingMatchCandidate, error)
	ReplaceIfCurrent(context.Context, persistence.IncomingMatchEvaluation, map[uuid.UUID]*string) error
}

type matchingConfigurationReader interface {
	GetMusicBrainzConfig(context.Context) (settings.MusicBrainzConfig, error)
}

// MatchingEvaluationQueryScope contains caller-selected exact cache identities.
// A file has one key per query family; the evaluator never invents, combines,
// or fans out provider searches.
type MatchingEvaluationQueryScope struct {
	GroupIDs          []uuid.UUID
	ReleaseKeysByFile map[uuid.UUID][]string
	SearchKeysByFile  map[uuid.UUID]MatchingSearchKeys
}

// MatchingEvaluationService composes persisted grouping/capture snapshots,
// saved MusicBrainz configuration, exact cached provider snapshots, pure
// evaluation, and CAS-protected result publication. Dependencies are narrow
// repository/service interfaces; this layer has no Bun or SQL access.
type MatchingEvaluationService struct {
	groups   matchingGroupingSnapshotReader
	captures matchingCaptureReader
	provider matchingProviderSnapshotReader
	results  matchingEvaluationStore
	config   matchingConfigurationReader
}

func NewMatchingEvaluationService(groups *IncomingGroups, captures *persistence.IncomingGroupingRepository, provider *persistence.ProviderCacheRepository, results *persistence.IncomingMatchCandidateRepository, config *settings.Registry) *MatchingEvaluationService {
	return newMatchingEvaluationService(groups, captures, provider, results, config)
}

func newMatchingEvaluationService(groups matchingGroupingSnapshotReader, captures matchingCaptureReader, provider matchingProviderSnapshotReader, results matchingEvaluationStore, config matchingConfigurationReader) *MatchingEvaluationService {
	return &MatchingEvaluationService{groups: groups, captures: captures, provider: provider, results: results, config: config}
}

// EvaluateAndPersist reads only saved captures and provider cache state. If any
// group is unready, it returns deferred results without publishing any group.
func (evaluator *MatchingEvaluationService) EvaluateAndPersist(ctx context.Context, scope MatchingEvaluationQueryScope) ([]MatchingGroupEvaluationResult, error) {
	if evaluator == nil || evaluator.groups == nil || evaluator.captures == nil || evaluator.provider == nil || evaluator.results == nil || evaluator.config == nil {
		return nil, fmt.Errorf("matching evaluation dependencies are unavailable")
	}
	if len(scope.GroupIDs) == 0 {
		return nil, fmt.Errorf("matching evaluation requires an explicit group scope")
	}
	expectedRevisions, err := captureMatchingEvaluationRevisions(ctx, scope.GroupIDs, evaluator.results)
	if err != nil {
		return nil, err
	}
	grouping, err := evaluator.groups.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	requestedGroups, err := requestedMatchingGroups(grouping.Groups, scope.GroupIDs)
	if err != nil {
		return nil, err
	}
	for _, group := range requestedGroups {
		if len(group.UnreadyMembers) != 0 {
			deferred := make([]MatchingGroupEvaluationResult, len(requestedGroups))
			for index, current := range requestedGroups {
				deferred[index] = MatchingGroupEvaluationResult{GroupID: current.ID, Decision: MatchingDecisionDeferred, Reason: "requested scope contains an unready group; no partial publication is allowed"}
			}
			return deferred, ErrMatchingEvaluationDeferred
		}
	}
	captures, err := evaluator.captures.CurrentCaptures(ctx)
	if err != nil {
		return nil, err
	}
	config, err := evaluator.config.GetMusicBrainzConfig(ctx)
	if err != nil {
		return nil, err
	}
	if config.Identity == "" {
		return nil, fmt.Errorf("matching evaluation requires saved MusicBrainz configuration identity")
	}
	memberIDs := make(map[uuid.UUID]bool)
	for _, group := range requestedGroups {
		for _, member := range group.Members {
			memberIDs[member] = true
		}
	}
	releaseSet, querySet := make(map[string]bool), make(map[string]bool)
	for member := range memberIDs {
		releases, ok := scope.ReleaseKeysByFile[member]
		if !ok || !canonicalMatchingReleaseKeys(releases) {
			return nil, fmt.Errorf("matching evaluation requires sorted explicit release keys for every group member")
		}
		for _, key := range releases {
			releaseSet[key] = true
		}
		keys, ok := scope.SearchKeysByFile[member]
		if !ok || keys.Release == "" || keys.Recording == "" {
			return nil, fmt.Errorf("matching evaluation requires exactly one release and recording search key per group member")
		}
		querySet[keys.Release], querySet[keys.Recording] = true, true
	}
	providerSnapshot, err := evaluator.provider.GetMusicBrainzMatchingSnapshot(ctx,
		musicBrainzEndpoint(config), config.Identity,
		sortedMatchingKeys(releaseSet), sortedMatchingKeys(querySet))
	if err != nil {
		return nil, err
	}
	results, err := EvaluateMatchingSnapshot(ctx, MatchingEvaluationSnapshot{
		Grouping: grouping, Captures: captures, Provider: providerSnapshot,
		RequestedGroupIDs: scope.GroupIDs, ReleaseKeysByFile: scope.ReleaseKeysByFile, SearchKeysByFile: scope.SearchKeysByFile,
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("matching evaluation produced no groups")
	}
	for _, result := range results {
		if result.Decision == MatchingDecisionDeferred {
			return results, ErrMatchingEvaluationDeferred
		}
	}
	combined, err := combineMatchingGroupResults(results, providerSnapshot, sortedMatchingKeys(releaseSet), sortedMatchingKeys(querySet))
	if err != nil {
		return nil, err
	}
	if err := evaluator.results.ReplaceIfCurrent(ctx, combined, expectedRevisions); err != nil {
		return nil, err
	}
	return results, nil
}

func captureMatchingEvaluationRevisions(ctx context.Context, groupIDs []uuid.UUID, store matchingEvaluationStore) (map[uuid.UUID]*string, error) {
	expected := make(map[uuid.UUID]*string, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID == uuid.Nil {
			return nil, fmt.Errorf("matching evaluation group scope has an invalid ID")
		}
		if _, duplicate := expected[groupID]; duplicate {
			return nil, fmt.Errorf("matching evaluation group scope has a duplicate ID")
		}
		header, _, err := store.Get(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if header == nil {
			expected[groupID] = nil
			continue
		}
		revision := header.EvaluationRevision
		expected[groupID] = &revision
	}
	return expected, nil
}

func combineMatchingGroupResults(results []MatchingGroupEvaluationResult, provider persistence.ProviderMatchingSnapshot, releaseKeys, queryKeys []string) (persistence.IncomingMatchEvaluation, error) {
	if len(results) == 0 {
		return persistence.IncomingMatchEvaluation{}, fmt.Errorf("matching evaluation results are required")
	}
	combined := results[0].Result
	combined.Groups = nil
	combined.Provider.ReleaseKeys = append([]string(nil), releaseKeys...)
	combined.Provider.CacheRevisions = make(map[string]*string, len(queryKeys))
	for _, key := range queryKeys {
		combined.Provider.CacheRevisions[key] = matchingCachedRevision(provider.Responses[key])
	}
	combined.Provider.CacheKey = queryKeys[0]
	combined.Provider.CacheRevision = copyString(combined.Provider.CacheRevisions[combined.Provider.CacheKey])
	for _, result := range results {
		if len(result.Result.Groups) != 1 {
			return persistence.IncomingMatchEvaluation{}, fmt.Errorf("matching evaluation result must contain one group")
		}
		combined.Groups = append(combined.Groups, result.Result.Groups[0])
	}
	return combined, nil
}

// MatchingEvaluationSnapshot is a complete, read-only calculation input. Search
// keys and release keys are explicit per-file cache identities; a full provider
// union snapshot is retained for its projection fence and filtered only here.
type MatchingEvaluationSnapshot struct {
	Grouping          IncomingGroupingSnapshot
	Captures          []persistence.IncomingGroupingCapture
	Provider          persistence.ProviderMatchingSnapshot
	RequestedGroupIDs []uuid.UUID
	ReleaseKeysByFile map[uuid.UUID][]string
	SearchKeysByFile  map[uuid.UUID]MatchingSearchKeys
}

type MatchingGroupDecision string

const (
	MatchingDecisionReview   MatchingGroupDecision = "review"
	MatchingDecisionSelected MatchingGroupDecision = "selected"
	MatchingDecisionDeferred MatchingGroupDecision = "deferred"
)

type MatchingGroupEvaluationResult struct {
	GroupID  uuid.UUID
	Decision MatchingGroupDecision
	Reason   string
	Result   persistence.IncomingMatchEvaluation
}

// EvaluateMatchingSnapshot is pure: it consumes saved captures and a cached
// provider projection only. It never performs I/O, persists confidence as
// confirmation, or schedules follow-up work.
func EvaluateMatchingSnapshot(ctx context.Context, snapshot MatchingEvaluationSnapshot) ([]MatchingGroupEvaluationResult, error) {
	if snapshot.Grouping.Epoch < 0 || len(snapshot.Grouping.Groups) == 0 {
		return nil, fmt.Errorf("matching evaluation requires a current grouping snapshot")
	}
	groupsRevision, err := IncomingGroupsRevision(snapshot.Grouping.Groups)
	if err != nil {
		return nil, err
	}
	groups, err := requestedMatchingGroups(snapshot.Grouping.Groups, snapshot.RequestedGroupIDs)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		if len(group.UnreadyMembers) != 0 {
			results := make([]MatchingGroupEvaluationResult, len(groups))
			for index, current := range groups {
				results[index] = MatchingGroupEvaluationResult{GroupID: current.ID, Decision: MatchingDecisionDeferred, Reason: "snapshot contains an unready group; no partial publication is allowed"}
			}
			return results, nil
		}
	}
	files := make(map[uuid.UUID]IncomingFile, len(snapshot.Grouping.Files))
	for _, file := range snapshot.Grouping.Files {
		files[file.VariantID] = file
	}
	capturesByFile := make(map[uuid.UUID][]persistence.IncomingGroupingCapture)
	for _, capture := range snapshot.Captures {
		capturesByFile[capture.VariantID] = append(capturesByFile[capture.VariantID], capture)
	}
	results := make([]MatchingGroupEvaluationResult, 0, len(groups))
	for _, group := range groups {
		result := MatchingGroupEvaluationResult{GroupID: group.ID}
		if len(group.UnreadyMembers) != 0 {
			result.Decision, result.Reason = MatchingDecisionDeferred, "group contains unready members"
			results = append(results, result)
			continue
		}
		groupResult, decision, reason, err := evaluateMatchingGroup(ctx, snapshot, groupsRevision, group, files, capturesByFile)
		if err != nil {
			return nil, err
		}
		result.Result, result.Decision, result.Reason = groupResult, decision, reason
		results = append(results, result)
	}
	for _, result := range results {
		if result.Decision == MatchingDecisionDeferred {
			for index := range results {
				results[index].Result = persistence.IncomingMatchEvaluation{}
				results[index].Decision = MatchingDecisionDeferred
				results[index].Reason = "snapshot contains an unready group; no partial publication is allowed"
			}
			break
		}
	}
	return results, nil
}

func requestedMatchingGroups(groups []IncomingGroup, ids []uuid.UUID) ([]IncomingGroup, error) {
	if len(ids) == 0 {
		return groups, nil
	}
	wanted := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || wanted[id] {
			return nil, fmt.Errorf("matching evaluation group scope has an invalid or duplicate ID")
		}
		wanted[id] = true
	}
	selected := make([]IncomingGroup, 0, len(ids))
	for _, group := range groups {
		if wanted[group.ID] {
			selected = append(selected, group)
			delete(wanted, group.ID)
		}
	}
	if len(wanted) != 0 {
		return nil, fmt.Errorf("matching evaluation group scope is stale")
	}
	return selected, nil
}

func evaluateMatchingGroup(
	ctx context.Context,
	snapshot MatchingEvaluationSnapshot,
	groupsRevision string,
	group IncomingGroup,
	files map[uuid.UUID]IncomingFile,
	capturesByFile map[uuid.UUID][]persistence.IncomingGroupingCapture,
) (persistence.IncomingMatchEvaluation, MatchingGroupDecision, string, error) {
	if group.ID == uuid.Nil || group.Revision == "" || len(group.Members) == 0 {
		return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("matching evaluation group identity or members are invalid")
	}
	memberSet := make(map[uuid.UUID]bool, len(group.Members))
	allCaptures := make([]persistence.IncomingGroupingCapture, 0)
	searchKeys := make(map[uuid.UUID]MatchingSearchKeys, len(group.Members))
	providerReleaseKeys := make(map[string]bool)
	cacheRevisions := make(map[string]*string)
	memberFences := make([]persistence.IncomingMatchMemberFence, 0, len(group.Members))
	for _, memberID := range group.Members {
		if memberSet[memberID] {
			return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("matching evaluation group has duplicate members")
		}
		memberSet[memberID] = true
		file, ok := files[memberID]
		if !ok {
			return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("matching evaluation member is absent from grouping snapshot")
		}
		captures := capturesByFile[memberID]
		if len(captures) == 0 {
			return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("matching evaluation member has no saved capture")
		}
		for _, capture := range captures {
			if !capture.Ready || capture.WorkID == uuid.Nil || capture.CaptureAnalysisID == nil || *capture.CaptureAnalysisID != file.CaptureAnalysisID {
				return persistence.IncomingMatchEvaluation{}, MatchingDecisionDeferred, "member capture is not ready/current", nil
			}
		}
		keys, ok := snapshot.SearchKeysByFile[memberID]
		if !ok || keys.Release == "" || keys.Recording == "" {
			return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("matching evaluation requires exactly one release and recording search key per member")
		}
		searchKeys[memberID] = keys
		if _, ok := snapshot.Provider.Responses[keys.Release]; !ok {
			return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("provider snapshot omitted the exact release search key")
		}
		if _, ok := snapshot.Provider.Responses[keys.Recording]; !ok {
			return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("provider snapshot omitted the exact recording search key")
		}
		if keys.Release != "" {
			cacheRevisions[keys.Release] = matchingCachedRevision(snapshot.Provider.Responses[keys.Release])
		}
		if keys.Recording != "" {
			cacheRevisions[keys.Recording] = matchingCachedRevision(snapshot.Provider.Responses[keys.Recording])
		}
		keysForFile, ok := snapshot.ReleaseKeysByFile[memberID]
		if !ok || !canonicalMatchingReleaseKeys(keysForFile) {
			return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("matching evaluation requires sorted, unique explicit release keys for every member")
		}
		for _, key := range keysForFile {
			providerReleaseKeys[key] = true
		}
		first := captures[0]
		for _, capture := range captures[1:] {
			if capture.WorkID != first.WorkID || !equalOptionalUUID(capture.CaptureAnalysisID, first.CaptureAnalysisID) ||
				!equalOptionalUUID(capture.MetadataWinner, first.MetadataWinner) || !equalOptionalUUID(capture.ProbeWinner, first.ProbeWinner) ||
				!equalOptionalStringPointer(capture.Fingerprint, first.Fingerprint) || !equalOptionalUUID(capture.FingerprintWinner, first.FingerprintWinner) {
				return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("incoming member has conflicting saved matching captures")
			}
		}
		memberFences = append(memberFences, persistence.IncomingMatchMemberFence{FileMemberID: memberID, WorkID: first.WorkID, SourceSHA256: append([]byte(nil), first.SourceSHA256...)})
		allCaptures = append(allCaptures, captures...)
	}

	providerKeys := sortedMatchingKeys(providerReleaseKeys)
	filtered := snapshot.Provider
	filtered.Releases = nil
	allowed := make(map[string]bool, len(providerKeys))
	for _, key := range providerKeys {
		allowed[key] = true
	}
	for _, release := range snapshot.Provider.Releases {
		if allowed[release.ProviderKey] {
			filtered.Releases = append(filtered.Releases, release)
		}
	}
	inputs := make([]MatchingInput, 0)
	for _, memberID := range group.Members {
		memberKeys := make(map[string]bool)
		for _, key := range snapshot.ReleaseKeysByFile[memberID] {
			memberKeys[key] = true
		}
		perFileSnapshot := filtered
		perFileSnapshot.Releases = nil
		for _, release := range filtered.Releases {
			if memberKeys[release.ProviderKey] {
				perFileSnapshot.Releases = append(perFileSnapshot.Releases, release)
			}
		}
		perFileInputs, err := BuildMatchingInputs(allCapturesForMember(allCaptures, memberID), perFileSnapshot, map[uuid.UUID]MatchingSearchKeys{memberID: searchKeys[memberID]}, nil)
		if err != nil {
			return persistence.IncomingMatchEvaluation{}, "", "", err
		}
		inputs = append(inputs, perFileInputs...)
	}
	return makeMatchingGroupResult(ctx, snapshot, groupsRevision, group, memberFences, inputs, providerKeys, cacheRevisions)
}

func makeMatchingGroupResult(
	ctx context.Context,
	snapshot MatchingEvaluationSnapshot,
	groupsRevision string,
	group IncomingGroup,
	members []persistence.IncomingMatchMemberFence,
	inputs []MatchingInput,
	releaseKeys []string,
	cacheRevisions map[string]*string,
) (persistence.IncomingMatchEvaluation, MatchingGroupDecision, string, error) {
	type edgeKey struct {
		file    uuid.UUID
		release string
	}
	type edge struct{ input MatchingInput }
	byFileRelease := make(map[edgeKey][]edge)
	releaseBest := make(map[edgeKey]float64)
	for _, input := range inputs {
		key := edgeKey{input.FileID, input.ReleaseID}
		byFileRelease[key] = append(byFileRelease[key], edge{input})
		if score, ok := releaseBest[key]; !ok || input.ReleaseScore.Confidence > score {
			releaseBest[key] = input.ReleaseScore.Confidence
		}
	}
	releaseFiles := make([]ReleaseSelectionFile, 0, len(members))
	for _, member := range members {
		file := ReleaseSelectionFile{ID: member.FileMemberID.String()}
		for key, score := range releaseBest {
			if key.file == member.FileMemberID {
				file.Candidates = append(file.Candidates, ReleaseSelectionCandidate{ReleaseID: key.release, Score: MatchingScore{Profile: ReleaseMatchingProfile, Confidence: score}})
			}
		}
		releaseFiles = append(releaseFiles, file)
	}
	releaseSelection, err := SelectReleaseGroup(releaseFiles)
	if err != nil {
		return persistence.IncomingMatchEvaluation{}, "", "", err
	}
	selectedRelease := releaseSelection.SelectedReleaseID
	allMemberReleaseEligible := releaseSelection.Reason == ReleaseSelectionSelected
	decision, reason := MatchingDecisionReview, "release selection is ambiguous or below threshold"
	if allMemberReleaseEligible {
		decision, reason = MatchingDecisionSelected, "unique release selection and assignment"
	}
	positionsByEdge := make(map[edgeKey][]persistence.IncomingMatchPosition)
	assignmentCandidates := make([]MatchingAssignmentCandidate, 0)
	for key, edges := range byFileRelease {
		for _, item := range edges {
			positionsByEdge[key] = append(positionsByEdge[key], persistence.IncomingMatchPosition{TrackID: matchingTrackID(snapshot.Provider, item.input.ReleaseID, item.input.Medium, item.input.Track), Medium: item.input.Medium, Position: item.input.Track})
			if !allMemberReleaseEligible || key.release != selectedRelease || item.input.AssignmentScore.Confidence < minimumReleaseCandidateConfidence {
				continue
			}
			candidateID := fmt.Sprintf("%s/%d/%d", key.release, item.input.Medium, item.input.Track)
			file := findIncomingFile(snapshot.Grouping.Files, key.file)
			locations := make([]MatchingAssignmentLocation, 0, len(file.Locations))
			for _, location := range file.Locations {
				locations = append(locations, MatchingAssignmentLocation{RootID: location.RootID.String(), RelativeParent: path.Dir(location.RelativePath)})
			}
			assignmentCandidates = append(assignmentCandidates, MatchingAssignmentCandidate{FileVariantID: key.file.String(), CandidateID: candidateID, PositionID: candidateID, RecordingMBID: item.input.RecordingID, Score: item.input.AssignmentScore, Locations: locations})
		}
	}
	if allMemberReleaseEligible {
		eligible := make(map[uuid.UUID]bool)
		for _, candidate := range assignmentCandidates {
			eligible[uuid.MustParse(candidate.FileVariantID)] = true
		}
		for _, member := range members {
			if !eligible[member.FileMemberID] {
				allMemberReleaseEligible = false
				break
			}
		}
		if !allMemberReleaseEligible {
			decision, reason = MatchingDecisionReview, "selected release has a member without an eligible assignment edge"
		}
	}
	if allMemberReleaseEligible {
		assignment, err := SelectMatchingAssignments(ctx, assignmentCandidates)
		if err != nil {
			return persistence.IncomingMatchEvaluation{}, "", "", err
		}
		if !assignment.Unique {
			decision, reason = MatchingDecisionReview, "selected release assignment is not uniquely optimal"
		}
	}

	groupEval := persistence.IncomingMatchGroupEvaluation{GroupID: group.ID, GroupRevision: group.Revision, Members: members}
	fileEvidence := make([]map[string]any, 0, len(members))
	for _, member := range members {
		file := findIncomingFile(snapshot.Grouping.Files, member.FileMemberID)
		fields := matchingSourceFields(file.Tags)
		keys := snapshot.SearchKeysByFile[member.FileMemberID]
		fileEvidence = append(fileEvidence, map[string]any{
			"file_member_id":      member.FileMemberID,
			"release_query_key":   keys.Release,
			"recording_query_key": keys.Recording,
			"raw_tags":            matchingRawTags(file),
			"mbid_diagnostics":    matchingMBIDDiagnostics(member.FileMemberID, fields),
		})
	}
	groupEvidence, err := json.Marshal(map[string]any{
		"schema_version": 1, "group_decision": decision, "decision_reason": reason,
		"selected_release_key": selectedRelease, "release_selection": releaseSelection, "files": fileEvidence,
	})
	if err != nil {
		return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("encode group matching evidence: %w", err)
	}
	groupEval.Evidence = groupEvidence
	for key, edges := range byFileRelease {
		best := releaseBest[key]
		evidenceEdges := make([]map[string]any, 0, len(edges))
		positions := positionsByEdge[key]
		for _, item := range edges {
			evidenceEdges = append(evidenceEdges, map[string]any{
				"schema_version": 1,
				"medium":         item.input.Medium, "position": item.input.Track,
				"release_profile": item.input.ReleaseScore, "release_profile_input": item.input.ReleaseScoreInput,
				"assignment_profile": item.input.AssignmentScore, "assignment_profile_input": item.input.AssignmentInput,
				"release_query_key":   snapshot.SearchKeysByFile[key.file].Release,
				"recording_query_key": snapshot.SearchKeysByFile[key.file].Recording,
				"raw_tags":            matchingRawTags(findIncomingFile(snapshot.Grouping.Files, key.file)),
				"mbid_diagnostics":    item.input.Diagnostics,
			})
		}
		decisionEvidence := map[string]any{
			"schema_version": 1,
			"group_decision": decision, "selected_release_key": selectedRelease,
			"release_selection": releaseSelection, "edge_evidence": evidenceEdges,
		}
		evidence, err := json.Marshal(decisionEvidence)
		if err != nil {
			return persistence.IncomingMatchEvaluation{}, "", "", fmt.Errorf("encode matching evidence: %w", err)
		}
		var releaseID uuid.UUID
		for _, release := range snapshot.Provider.Releases {
			if release.ProviderKey == key.release {
				releaseID = release.ID
				break
			}
		}
		if releaseID == uuid.Nil {
			continue
		}
		groupEval.Candidates = append(groupEval.Candidates, persistence.IncomingMatchCandidate{FileMemberID: key.file, ProviderID: snapshot.Provider.Source.ProviderID, ProviderSourceID: snapshot.Provider.Source.ID, ReleaseID: releaseID, Score: best, Evidence: evidence, Positions: positions})
	}
	sort.Slice(groupEval.Candidates, func(i, j int) bool {
		if groupEval.Candidates[i].FileMemberID != groupEval.Candidates[j].FileMemberID {
			return groupEval.Candidates[i].FileMemberID.String() < groupEval.Candidates[j].FileMemberID.String()
		}
		return groupEval.Candidates[i].ReleaseID.String() < groupEval.Candidates[j].ReleaseID.String()
	})
	keys := make([]string, 0, len(cacheRevisions))
	for key := range cacheRevisions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	cacheKey := ""
	var cacheRevision *string
	if len(keys) > 0 {
		cacheKey = keys[0]
		cacheRevision = copyString(cacheRevisions[cacheKey])
	}
	providerFence := persistence.IncomingMatchProviderFence{ProviderID: snapshot.Provider.Source.ProviderID, ProviderSourceID: snapshot.Provider.Source.ID, Namespace: snapshot.Provider.Source.Namespace, Endpoint: snapshot.Provider.Source.Endpoint, ConfigurationIdentity: snapshot.Provider.Source.ConfigurationIdentity, CacheKey: cacheKey, CacheRevision: cacheRevision, CacheRevisions: cacheRevisions, AcceptedProjectionRevision: snapshot.Provider.AcceptedProjectionRevision, ReleaseKeys: releaseKeys}
	evaluation := persistence.IncomingMatchEvaluation{ExpectedGroupsRevision: groupsRevision, ExpectedGroupingEpoch: snapshot.Grouping.Epoch, PolicyVersion: MatchingPolicyVersion, Provider: providerFence, Groups: []persistence.IncomingMatchGroupEvaluation{groupEval}}
	return evaluation, decision, reason, nil
}

func allCapturesForMember(captures []persistence.IncomingGroupingCapture, id uuid.UUID) []persistence.IncomingGroupingCapture {
	result := make([]persistence.IncomingGroupingCapture, 0)
	for _, capture := range captures {
		if capture.VariantID == id {
			result = append(result, capture)
		}
	}
	return result
}
func findIncomingFile(files []IncomingFile, id uuid.UUID) IncomingFile {
	for _, file := range files {
		if file.VariantID == id {
			return file
		}
	}
	return IncomingFile{}
}
func matchingRawTags(file IncomingFile) map[string][]string {
	result := make(map[string][]string, len(file.Tags))
	for k, v := range file.Tags {
		result[k] = append([]string(nil), v...)
	}
	return result
}
func matchingTrackID(snapshot persistence.ProviderMatchingSnapshot, releaseKey string, medium, position int) uuid.UUID {
	for _, release := range snapshot.Releases {
		if release.ProviderKey == releaseKey {
			for _, track := range release.Tracks {
				if track.Medium == medium && track.Position == position {
					return track.ID
				}
			}
		}
	}
	return uuid.Nil
}
func canonicalMatchingReleaseKeys(keys []string) bool {
	for i, key := range keys {
		if key == "" || i > 0 && keys[i-1] >= key {
			return false
		}
	}
	return true
}
func sortedMatchingKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func matchingCachedRevision(value *persistence.ProviderCachedResponse) *string {
	if value == nil {
		return nil
	}
	return copyString(value.Revision)
}
func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
func equalOptionalUUID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func equalOptionalStringPointer(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
