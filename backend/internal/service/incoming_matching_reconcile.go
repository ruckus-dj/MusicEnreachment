package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var ErrIncomingMatchingReconciliationDeferred = errors.New("incoming matching reconciliation deferred")

type incomingMatchingReconcileSnapshotReader interface {
	MatchingReconcileSnapshot(context.Context) (persistence.IncomingMatchingReconcileSnapshot, error)
}

type incomingMatchingAutomaticAdmission interface {
	AdmitAutomaticArtistMatchingRun(context.Context, persistence.RiverInserter, persistence.AutomaticArtistMatchingAdmission) (persistence.AutomaticArtistMatchingAdmissionResult, error)
}

type incomingMatchingReconcileRefresher interface {
	RefreshIfNeeded(context.Context) error
}

type incomingMatchingProviderPreparer interface {
	PrepareOperation(context.Context, string, string, int, int, bool) (*persistence.Operation, error)
}

// IncomingMatchingReconcileDelivery is the exact event delivery fence owned by
// one River attempt. No user-controlled or provider data is carried in job args.
type IncomingMatchingReconcileDelivery struct {
	EventID      uuid.UUID
	RiverJobID   int64
	RiverAttempt int
	Epoch        int64
}

// IncomingMatchingReconciliation admits idempotent artist matching runs from
// the persisted inventory projection. Provider preparation performs no HTTP.
type IncomingMatchingReconciliation struct {
	groups          incomingMatchingReconcileRefresher
	snapshots       incomingMatchingReconcileSnapshotReader
	providers       incomingMatchingProviderPreparer
	admissions      incomingMatchingAutomaticAdmission
	river           persistence.RiverInserter
	currentRequests incomingMatchingCurrentRequestRepository
}

func NewIncomingMatchingReconciliation(groups incomingMatchingReconcileRefresher, snapshots incomingMatchingReconcileSnapshotReader, providers incomingMatchingProviderPreparer, admissions incomingMatchingAutomaticAdmission, river persistence.RiverInserter, currentRequests ...incomingMatchingCurrentRequestRepository) *IncomingMatchingReconciliation {
	service := &IncomingMatchingReconciliation{groups: groups, snapshots: snapshots, providers: providers, admissions: admissions, river: river}
	if len(currentRequests) > 0 {
		service.currentRequests = currentRequests[0]
	}
	return service
}

func (service *IncomingMatchingReconciliation) Reconcile(ctx context.Context, delivery IncomingMatchingReconcileDelivery) error {
	if service == nil || service.groups == nil || service.snapshots == nil || service.providers == nil || service.admissions == nil || service.river == nil ||
		delivery.EventID == uuid.Nil || delivery.RiverJobID < 1 || delivery.RiverAttempt < 1 || delivery.Epoch < 1 {
		return fmt.Errorf("incoming matching reconciliation service or delivery is incomplete")
	}
	if err := service.groups.RefreshIfNeeded(ctx); err != nil {
		return fmt.Errorf("refresh persisted incoming grouping: %w", err)
	}
	snapshot, err := service.snapshots.MatchingReconcileSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("read persisted incoming matching projection: %w", err)
	}
	plan, err := PlanIncomingMatchingReconcile(snapshot)
	if err != nil {
		return fmt.Errorf("plan incoming matching reconciliation: %w", err)
	}
	for _, target := range plan.PlannedGroups {
		providerOperation, err := service.providers.PrepareOperation(ctx, "artist_search", target.Query, 0, 1, false)
		if errors.Is(err, ErrMusicBrainzConfigurationMissing) {
			return ErrIncomingMatchingReconciliationDeferred
		}
		if err != nil {
			return fmt.Errorf("prepare automatic artist search for group %s: %w", target.GroupID, err)
		}
		admission := persistence.AutomaticArtistMatchingAdmission{
			Parent:     &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider"},
			ParentArgs: MatchingEvaluationOperationArgs{OperationID: uuid.Nil},
			Provider:   persistence.MatchingProviderRequest{Operation: providerOperation, Args: ProviderFetchOperationArgs{OperationID: providerOperation.ID}},
			GroupID:    target.GroupID, ExpectedGroupRevision: target.GroupRevision,
			PolicyVersion: MatchingPolicyVersion, ArtistQueryPlanVersion: 1,
			QueryDescriptor: append([]byte(nil), providerOperation.InputSnapshot...),
			EventID:         delivery.EventID, EventRiverJobID: delivery.RiverJobID,
			EventRiverAttempt: delivery.RiverAttempt, EventDeliveryEpoch: delivery.Epoch,
		}
		admission.ParentArgs = MatchingEvaluationOperationArgs{OperationID: admission.Parent.ID}
		if _, err := service.admissions.AdmitAutomaticArtistMatchingRun(ctx, service.river, admission); err != nil {
			return fmt.Errorf("admit automatic artist matching for group %s: %w", target.GroupID, err)
		}
	}
	if len(plan.DeferredGroupIDs) > 0 {
		return ErrIncomingMatchingReconciliationDeferred
	}
	return nil
}
