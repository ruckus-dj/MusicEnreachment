package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingCoordinatorRepository interface {
	AdmitMatchingRun(context.Context, persistence.RiverInserter, persistence.MatchingAdmission) (*persistence.Operation, error)
}

type matchingGroupSnapshotReader interface {
	State(context.Context) (persistence.IncomingGroupingState, error)
	List(context.Context) ([]persistence.IncomingGroupingGroup, error)
}

type MatchingProviderRequest struct {
	Kind            string `json:"kind"`
	QueryOrMBID     string `json:"query_or_mbid"`
	Offset          int    `json:"offset,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	ExplicitRefresh bool   `json:"explicit_refresh,omitempty"`
}

type MatchingCoordinator struct {
	repository matchingCoordinatorRepository
	groups     matchingGroupSnapshotReader
	providers  *ProviderFetchService
	river      persistence.RiverInserter
}

type MatchingEvaluationOperationArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (MatchingEvaluationOperationArgs) Kind() string { return "matching_evaluation_v1" }

func NewMatchingCoordinator(repository matchingCoordinatorRepository, groups matchingGroupSnapshotReader, providers *ProviderFetchService, river persistence.RiverInserter) *MatchingCoordinator {
	return &MatchingCoordinator{repository: repository, groups: groups, providers: providers, river: river}
}

// Start admits exactly the requested current groups and the provider requests
// explicitly supplied by the caller. It does not expand group scope or fetch.
func (coordinator *MatchingCoordinator) Start(ctx context.Context, admissionKey string, scope MatchingEvaluationQueryScope, requests []MatchingProviderRequest) (*persistence.Operation, error) {
	if coordinator == nil || coordinator.repository == nil || coordinator.groups == nil || coordinator.providers == nil || coordinator.river == nil {
		return nil, fmt.Errorf("matching coordinator is unavailable")
	}
	if admissionKey == "" || len(scope.GroupIDs) == 0 || len(requests) == 0 {
		return nil, fmt.Errorf("matching admission key, exact group scope, and provider requests are required")
	}
	state, err := coordinator.groups.State(ctx)
	if err != nil {
		return nil, fmt.Errorf("read incoming grouping state: %w", err)
	}
	if state.NeedsRefresh {
		return nil, fmt.Errorf("incoming grouping requires refresh")
	}
	allGroups, err := coordinator.groups.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("read incoming groups: %w", err)
	}
	byID := make(map[uuid.UUID]persistence.IncomingGroupingGroup, len(allGroups))
	for _, group := range allGroups {
		byID[group.ID] = group
	}
	ids := append([]uuid.UUID(nil), scope.GroupIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	targets := make([]persistence.MatchingTarget, 0, len(ids))
	for i, id := range ids {
		group, ok := byID[id]
		if id == uuid.Nil || !ok || len(group.Members) == 0 || i > 0 && ids[i-1] == id {
			return nil, fmt.Errorf("matching group scope is invalid or stale")
		}
		targets = append(targets, persistence.MatchingTarget{GroupID: id, Revision: group.Revision})
	}
	queryScope, err := canonicalMatchingQueryScope(scope, targets, byID)
	if err != nil {
		return nil, err
	}
	prepared := make([]persistence.MatchingProviderRequest, 0, len(requests))
	planRequests := make([]matchingPlanProviderRequest, 0, len(requests))
	for _, request := range requests {
		operation, err := coordinator.providers.PrepareOperation(ctx, request.Kind, request.QueryOrMBID, request.Offset, request.Limit, request.ExplicitRefresh)
		if err != nil {
			return nil, fmt.Errorf("prepare matching provider request: %w", err)
		}
		prepared = append(prepared, persistence.MatchingProviderRequest{Operation: operation, Args: ProviderFetchOperationArgs{OperationID: operation.ID}})
		if operation.ProviderSourceID == nil || operation.ProviderConfigurationIdentity == nil || operation.ProviderID == nil || operation.ProviderEndpoint == "" || operation.ProviderCacheKey == nil {
			return nil, fmt.Errorf("prepared provider request is missing its captured source fence")
		}
		planRequests = append(planRequests, matchingPlanProviderRequest{
			Kind: operation.Kind, Snapshot: operation.InputSnapshot, CacheKey: *operation.ProviderCacheKey,
			ExplicitRefresh: operation.ProviderExplicitRefresh, ProviderSourceID: *operation.ProviderSourceID,
			EndpointConfigurationIdentity: *operation.ProviderConfigurationIdentity, ProviderID: *operation.ProviderID,
		})
	}
	prepared, planRequests = canonicalMatchingProviderRequests(prepared, planRequests)
	planVersion := 2
	if len(queryScope.AcoustIDKeysByFile) != 0 {
		planVersion = 3
	}
	plan := matchingAdmissionPlan{SchemaVersion: planVersion, GroupingEpoch: state.Revision, Targets: targets, PolicyVersion: MatchingPolicyVersion, EvaluationQueryScope: queryScope, ProviderRequests: planRequests}
	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encode matching plan: %w", err)
	}
	parentSnapshot, err := json.Marshal(matchingParentIntent{SchemaVersion: 1, Plan: json.RawMessage(encodedPlan)})
	if err != nil {
		return nil, fmt.Errorf("encode matching parent intent: %w", err)
	}
	parent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: parentSnapshot}
	return coordinator.repository.AdmitMatchingRun(ctx, coordinator.river, persistence.MatchingAdmission{
		AdmissionKey: admissionKey, GroupingEpoch: state.Revision, Targets: targets, Plan: encodedPlan, PolicyVersion: MatchingPolicyVersion,
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parent.ID}, Providers: prepared,
	})
}

type matchingAdmissionPlan struct {
	SchemaVersion        int                           `json:"schema_version"`
	GroupingEpoch        int64                         `json:"grouping_epoch"`
	Targets              []persistence.MatchingTarget  `json:"targets"`
	PolicyVersion        string                        `json:"policy_version"`
	EvaluationQueryScope MatchingEvaluationQueryScope  `json:"evaluation_query_scope"`
	ProviderRequests     []matchingPlanProviderRequest `json:"provider_requests"`
}

func canonicalMatchingQueryScope(scope MatchingEvaluationQueryScope, targets []persistence.MatchingTarget, groups map[uuid.UUID]persistence.IncomingGroupingGroup) (MatchingEvaluationQueryScope, error) {
	result := MatchingEvaluationQueryScope{
		GroupIDs:           append([]uuid.UUID(nil), scope.GroupIDs...),
		ReleaseKeysByFile:  make(map[uuid.UUID][]string, len(scope.ReleaseKeysByFile)),
		SearchKeysByFile:   make(map[uuid.UUID]MatchingSearchKeys, len(scope.SearchKeysByFile)),
		AcoustIDKeysByFile: make(map[uuid.UUID]string, len(scope.AcoustIDKeysByFile)),
	}
	sort.Slice(result.GroupIDs, func(i, j int) bool { return result.GroupIDs[i].String() < result.GroupIDs[j].String() })
	selectedMembers := make(map[uuid.UUID]struct{})
	for _, target := range targets {
		group := groups[target.GroupID]
		for _, member := range group.Members {
			selectedMembers[member] = struct{}{}
		}
		for _, member := range group.UnreadyMembers {
			selectedMembers[member] = struct{}{}
		}
	}
	if len(scope.ReleaseKeysByFile) != len(selectedMembers) || len(scope.SearchKeysByFile) != len(selectedMembers) {
		return MatchingEvaluationQueryScope{}, fmt.Errorf("matching query scope must cover exactly the selected group members")
	}
	for member := range selectedMembers {
		releases, releaseOK := scope.ReleaseKeysByFile[member]
		keys, searchOK := scope.SearchKeysByFile[member]
		if !releaseOK || !searchOK || keys.Release == "" || keys.Recording == "" {
			return MatchingEvaluationQueryScope{}, fmt.Errorf("matching query scope requires one release and recording search key per selected file")
		}
		for index, key := range releases {
			if key == "" || index > 0 && releases[index-1] >= key {
				return MatchingEvaluationQueryScope{}, fmt.Errorf("matching query scope release keys must be sorted and unique")
			}
		}
		result.ReleaseKeysByFile[member] = append([]string{}, releases...)
		result.SearchKeysByFile[member] = keys
		if key, ok := scope.AcoustIDKeysByFile[member]; ok {
			if key == "" {
				return MatchingEvaluationQueryScope{}, fmt.Errorf("matching AcoustID cache key must be non-empty")
			}
			result.AcoustIDKeysByFile[member] = key
		}
	}
	for member := range scope.AcoustIDKeysByFile {
		if _, ok := selectedMembers[member]; !ok {
			return MatchingEvaluationQueryScope{}, fmt.Errorf("matching AcoustID scope contains an unselected file")
		}
	}
	return result, nil
}

type matchingPlanProviderRequest struct {
	Kind                          string          `json:"kind"`
	Snapshot                      json.RawMessage `json:"snapshot"`
	CacheKey                      string          `json:"cache_key"`
	ExplicitRefresh               bool            `json:"explicit_refresh"`
	ProviderSourceID              uuid.UUID       `json:"provider_source_id"`
	EndpointConfigurationIdentity string          `json:"endpoint_configuration_identity"`
	ProviderID                    uuid.UUID       `json:"provider_id"`
}

func canonicalMatchingProviderRequests(prepared []persistence.MatchingProviderRequest, plans []matchingPlanProviderRequest) ([]persistence.MatchingProviderRequest, []matchingPlanProviderRequest) {
	type canonicalRequest struct {
		prepared persistence.MatchingProviderRequest
		plan     matchingPlanProviderRequest
		encoded  []byte
	}
	requests := make([]canonicalRequest, len(prepared))
	for index := range prepared {
		encoded, _ := json.Marshal(plans[index])
		requests[index] = canonicalRequest{prepared: prepared[index], plan: plans[index], encoded: encoded}
	}
	sort.Slice(requests, func(i, j int) bool {
		if comparison := bytes.Compare(requests[i].encoded, requests[j].encoded); comparison != 0 {
			return comparison < 0
		}
		return requests[i].prepared.Operation.ID.String() < requests[j].prepared.Operation.ID.String()
	})
	orderedPrepared := make([]persistence.MatchingProviderRequest, len(requests))
	orderedPlans := make([]matchingPlanProviderRequest, len(requests))
	for index, request := range requests {
		orderedPrepared[index], orderedPlans[index] = request.prepared, request.plan
	}
	return orderedPrepared, orderedPlans
}

type matchingParentIntent struct {
	SchemaVersion int             `json:"schema_version"`
	Plan          json.RawMessage `json:"plan"`
}

var _ river.JobArgs = MatchingEvaluationOperationArgs{}
