package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
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

type incomingMatchingFallbackAdmission interface {
	AdmitAutomaticFallbackMatchingRun(context.Context, persistence.RiverInserter, persistence.AutomaticFallbackMatchingAdmission) (persistence.AutomaticArtistMatchingAdmissionResult, error)
}

type incomingMatchingFallbackSourceResolver interface {
	ResolveCurrentMusicBrainzSource(context.Context) (persistence.AutomaticFallbackProviderSource, error)
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
	localFallback   incomingMatchingOutageFallbackRepository
	localCatalog    incomingMatchingLocalLayoutCalculator
}

func NewIncomingMatchingReconciliation(groups incomingMatchingReconcileRefresher, snapshots incomingMatchingReconcileSnapshotReader, providers incomingMatchingProviderPreparer, admissions incomingMatchingAutomaticAdmission, river persistence.RiverInserter, currentRequests ...incomingMatchingCurrentRequestRepository) *IncomingMatchingReconciliation {
	service := &IncomingMatchingReconciliation{groups: groups, snapshots: snapshots, providers: providers, admissions: admissions, river: river}
	if len(currentRequests) > 0 {
		service.currentRequests = currentRequests[0]
	}
	return service
}

func (service *IncomingMatchingReconciliation) admitFallback(ctx context.Context, target IncomingMatchingFallbackTarget, delivery IncomingMatchingReconcileDelivery, generation int64) error {
	admissions, ok := service.admissions.(incomingMatchingFallbackAdmission)
	if !ok {
		return ErrIncomingMatchingReconciliationDeferred
	}
	resolver, ok := service.providers.(incomingMatchingFallbackSourceResolver)
	if !ok {
		return ErrIncomingMatchingReconciliationDeferred
	}
	source, err := resolver.ResolveCurrentMusicBrainzSource(ctx)
	if errors.Is(err, ErrMusicBrainzConfigurationMissing) {
		return ErrIncomingMatchingReconciliationDeferred
	}
	if err != nil {
		return fmt.Errorf("resolve fallback provider source: %w", err)
	}
	plan := policy.TypedFallbackQueryPlan{SchemaVersion: target.Plan.SchemaVersion, Stages: make([]policy.TypedFallbackQueryStage, len(target.Plan.Stages))}
	for i, stage := range target.Plan.Stages {
		plan.Stages[i] = policy.TypedFallbackQueryStage{Kind: stage.Kind, Query: stage.Query, Offset: stage.Offset, Limit: stage.Limit}
	}
	var provider persistence.MatchingProviderRequest
	stage := "waiting_policy"
	if len(plan.Stages) > 0 {
		first := plan.Stages[0]
		providerKind, _, supported := policy.ProviderKindForFallbackStage(first.Kind)
		if !supported {
			return fmt.Errorf("unsupported fallback query stage %q", first.Kind)
		}
		kind := strings.TrimPrefix(providerKind, "provider_")
		operation, err := service.providers.PrepareOperation(ctx, kind, first.Query, first.Offset, first.Limit, false)
		if errors.Is(err, ErrMusicBrainzConfigurationMissing) {
			return ErrIncomingMatchingReconciliationDeferred
		}
		if err != nil {
			return fmt.Errorf("prepare fallback provider request: %w", err)
		}
		provider = persistence.MatchingProviderRequest{Operation: operation, Args: ProviderFetchOperationArgs{OperationID: operation.ID}}
		stage = "waiting_provider"
	}
	parent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: stage}
	_, err = admissions.AdmitAutomaticFallbackMatchingRun(ctx, service.river, persistence.AutomaticFallbackMatchingAdmission{
		Parent: parent, ParentArgs: MatchingEvaluationOperationArgs{OperationID: parent.ID}, Provider: provider, Source: source,
		GroupID: target.GroupID, ExpectedGroupRevision: target.GroupRevision, PolicyVersion: MatchingPolicyVersion, Plan: plan,
		EventID: delivery.EventID, EventRiverJobID: delivery.RiverJobID, EventRiverAttempt: delivery.RiverAttempt,
		EventDeliveryEpoch: delivery.Epoch, RequestGeneration: generation,
	})
	if err != nil {
		return fmt.Errorf("persist automatic fallback admission: %w", err)
	}
	return nil
}
