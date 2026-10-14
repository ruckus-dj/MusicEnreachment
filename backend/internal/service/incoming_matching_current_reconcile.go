package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type incomingMatchingCurrentRequestRepository interface {
	ReadPendingCurrentRequests(context.Context) ([]persistence.IncomingMatchingCurrentRequest, error)
	AcknowledgeCurrentRequest(context.Context, uuid.UUID, int64) error
	AcknowledgeRemovedCurrentGroup(context.Context, uuid.UUID, int64) (bool, error)
}

// ReconcileCurrent consumes coalesced generations from the persisted current
// projection. It never reads a source-event snapshot.
func (service *IncomingMatchingReconciliation) ReconcileCurrent(ctx context.Context, delivery IncomingMatchingReconcileDelivery) error {
	if service == nil || service.groups == nil || service.snapshots == nil || service.providers == nil || service.admissions == nil || service.river == nil || service.currentRequests == nil ||
		delivery.EventID == uuid.Nil || delivery.RiverJobID < 1 || delivery.RiverAttempt < 1 || delivery.Epoch < 1 {
		return fmt.Errorf("current incoming matching reconciliation service or delivery is incomplete")
	}
	if err := service.groups.RefreshIfNeeded(ctx); err != nil {
		return fmt.Errorf("refresh persisted incoming grouping: %w", err)
	}
	requests, err := service.currentRequests.ReadPendingCurrentRequests(ctx)
	if err != nil {
		return fmt.Errorf("read current incoming matching requests: %w", err)
	}
	snapshot, err := service.snapshots.MatchingReconcileSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("read persisted incoming matching projection: %w", err)
	}
	requested := make(map[uuid.UUID]persistence.IncomingMatchingCurrentRequest, len(requests))
	requestOrder := make([]uuid.UUID, 0, len(requests))
	for _, request := range requests {
		requested[request.GroupID] = request
		requestOrder = append(requestOrder, request.GroupID)
	}
	sort.Slice(requestOrder, func(i, j int) bool { return requestOrder[i].String() < requestOrder[j].String() })
	groups := make(map[uuid.UUID]persistence.IncomingMatchingReconcileGroup, len(snapshot.Groups))
	current := persistence.IncomingMatchingReconcileSnapshot{Groups: make([]persistence.IncomingMatchingReconcileGroup, 0, len(requests))}
	for _, group := range snapshot.Groups {
		groups[group.Group.ID] = group
		if _, ok := requested[group.Group.ID]; ok {
			current.Groups = append(current.Groups, group)
		}
	}
	plan, err := PlanIncomingMatchingReconcile(current)
	if err != nil {
		return fmt.Errorf("plan current incoming matching reconciliation: %w", err)
	}
	planned := make(map[uuid.UUID]IncomingMatchingReconcileTarget, len(plan.PlannedGroups))
	for _, target := range plan.PlannedGroups {
		planned[target.GroupID] = target
	}
	deferredGroups := make(map[uuid.UUID]struct{}, len(plan.DeferredGroupIDs))
	for _, groupID := range plan.DeferredGroupIDs {
		deferredGroups[groupID] = struct{}{}
	}
	deferred := false
	for _, groupID := range requestOrder {
		request := requested[groupID]
		group, exists := groups[groupID]
		if !exists {
			acknowledged, err := service.currentRequests.AcknowledgeRemovedCurrentGroup(ctx, groupID, request.RequestedGeneration)
			if err != nil {
				return fmt.Errorf("acknowledge removed current incoming group %s: %w", groupID, err)
			}
			if !acknowledged {
				deferred = true
			}
			continue
		}
		if group.Authority.Mode != "automatic" {
			// Manual ownership is not an automatic admission and remains pending
			// until an explicit return-to-automatic action issues a new request.
			continue
		}
		if len(group.Group.UnreadyMembers) > 0 {
			deferred = true
			continue
		}
		target, ok := planned[groupID]
		if !ok {
			// Artistless groups remain deferred until the separately approved
			// fallback activation stage is delivered.
			continue
		}
		providerOperation, err := service.providers.PrepareOperation(ctx, "artist_search", target.Query, 0, 1, false)
		if errors.Is(err, ErrMusicBrainzConfigurationMissing) {
			return ErrIncomingMatchingReconciliationDeferred
		}
		if err != nil {
			return fmt.Errorf("prepare current automatic artist search for group %s: %w", groupID, err)
		}
		parent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider"}
		admission := persistence.AutomaticArtistMatchingAdmission{
			Parent: parent, ParentArgs: MatchingEvaluationOperationArgs{OperationID: parent.ID},
			Provider: persistence.MatchingProviderRequest{Operation: providerOperation, Args: ProviderFetchOperationArgs{OperationID: providerOperation.ID}},
			GroupID:  groupID, ExpectedGroupRevision: target.GroupRevision,
			PolicyVersion: MatchingPolicyVersion, ArtistQueryPlanVersion: 1,
			QueryDescriptor: append([]byte(nil), providerOperation.InputSnapshot...),
			EventID:         delivery.EventID, EventRiverJobID: delivery.RiverJobID,
			EventRiverAttempt: delivery.RiverAttempt, EventDeliveryEpoch: delivery.Epoch,
			RequestGeneration: request.RequestedGeneration,
		}
		if _, err := service.admissions.AdmitAutomaticArtistMatchingRun(ctx, service.river, admission); err != nil {
			return fmt.Errorf("admit current automatic artist matching for group %s: %w", groupID, err)
		}
		if err := service.currentRequests.AcknowledgeCurrentRequest(ctx, groupID, request.RequestedGeneration); err != nil {
			return fmt.Errorf("acknowledge current automatic artist matching for group %s: %w", groupID, err)
		}
	}
	if deferred || len(deferredGroups) > 0 {
		return ErrIncomingMatchingReconciliationDeferred
	}
	return nil
}
