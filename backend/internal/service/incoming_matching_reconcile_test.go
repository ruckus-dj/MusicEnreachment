package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type reconcileRefreshStub struct{ calls int }

func (stub *reconcileRefreshStub) RefreshIfNeeded(context.Context) error { stub.calls++; return nil }

type reconcileSnapshotStub struct {
	snapshot persistence.IncomingMatchingReconcileSnapshot
}

func (stub reconcileSnapshotStub) MatchingReconcileSnapshot(context.Context) (persistence.IncomingMatchingReconcileSnapshot, error) {
	return stub.snapshot, nil
}

type reconcileProviderStub struct {
	queries []string
	err     error
}

func (stub *reconcileProviderStub) PrepareOperation(_ context.Context, kind, query string, offset, limit int, refresh bool) (*persistence.Operation, error) {
	if stub.err != nil {
		return nil, stub.err
	}
	if kind != "artist_search" || offset != 0 || limit != 1 || refresh {
		return nil, fmt.Errorf("unexpected provider request %s %d %d %t", kind, offset, limit, refresh)
	}
	stub.queries = append(stub.queries, query)
	input, _ := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}{1, query, 0, 1})
	key := fmt.Sprintf("artist-search:0:1:%s", query)
	return &persistence.Operation{ID: uuid.New(), Kind: "provider_artist_search", InputSnapshot: input, ProviderCacheKey: &key}, nil
}

type reconcileAdmissionStub struct {
	seen       map[uuid.UUID]bool
	groups     []uuid.UUID
	failOnCall int
}

type reconcileCurrentRequestsStub struct {
	requests            []persistence.IncomingMatchingCurrentRequest
	acked               []persistence.IncomingMatchingCurrentRequest
	removedAck          []persistence.IncomingMatchingCurrentRequest
	removedAcknowledged bool
}

func (stub *reconcileCurrentRequestsStub) ReadPendingCurrentRequests(context.Context) ([]persistence.IncomingMatchingCurrentRequest, error) {
	return append([]persistence.IncomingMatchingCurrentRequest(nil), stub.requests...), nil
}

func (stub *reconcileCurrentRequestsStub) AcknowledgeCurrentRequest(_ context.Context, groupID uuid.UUID, generation int64) error {
	stub.acked = append(stub.acked, persistence.IncomingMatchingCurrentRequest{GroupID: groupID, RequestedGeneration: generation})
	return nil
}

func (stub *reconcileCurrentRequestsStub) AcknowledgeRemovedCurrentGroup(_ context.Context, groupID uuid.UUID, generation int64) (bool, error) {
	stub.removedAck = append(stub.removedAck, persistence.IncomingMatchingCurrentRequest{GroupID: groupID, RequestedGeneration: generation})
	return stub.removedAcknowledged, nil
}

func (stub *reconcileAdmissionStub) AdmitAutomaticArtistMatchingRun(_ context.Context, _ persistence.RiverInserter, admission persistence.AutomaticArtistMatchingAdmission) (persistence.AutomaticArtistMatchingAdmissionResult, error) {
	if admission.EventID == uuid.Nil || admission.EventRiverJobID != 17 || admission.EventRiverAttempt != 2 || admission.EventDeliveryEpoch != 9 ||
		admission.Parent.Kind != "matching_evaluation_v1" || admission.ParentArgs.(MatchingEvaluationOperationArgs).OperationID != admission.Parent.ID ||
		admission.Provider.Operation.Kind != "provider_artist_search" || admission.ArtistQueryPlanVersion != 1 {
		return persistence.AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("admission fields not fenced or canonical")
	}
	stub.groups = append(stub.groups, admission.GroupID)
	if stub.failOnCall > 0 && len(stub.groups) == stub.failOnCall {
		return persistence.AutomaticArtistMatchingAdmissionResult{}, errors.New("simulated transaction failure")
	}
	if stub.seen == nil {
		stub.seen = make(map[uuid.UUID]bool)
	}
	prior := stub.seen[admission.GroupID]
	stub.seen[admission.GroupID] = true
	return persistence.AutomaticArtistMatchingAdmissionResult{AlreadyAdmitted: prior}, nil
}

func TestIncomingMatchingReconciliationPlansAllGroupsAndReplaysAdmissions(t *testing.T) {
	firstID, secondID := uuid.New(), uuid.New()
	snapshot := reconcileTestSnapshot(t, map[uuid.UUID]string{firstID: "First", secondID: "Second"}, nil)
	refresh, provider, admission := &reconcileRefreshStub{}, &reconcileProviderStub{}, &reconcileAdmissionStub{}
	service := NewIncomingMatchingReconciliation(refresh, reconcileSnapshotStub{snapshot}, provider, admission, reconcileRiverStub{})
	delivery := IncomingMatchingReconcileDelivery{EventID: uuid.New(), RiverJobID: 17, RiverAttempt: 2, Epoch: 9}
	if err := service.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if refresh.calls != 2 || len(provider.queries) != 4 || len(admission.groups) != 4 || !admission.seen[firstID] || !admission.seen[secondID] {
		t.Fatalf("refresh=%d queries=%v admitted=%v", refresh.calls, provider.queries, admission.groups)
	}
}

func TestIncomingMatchingReconciliationDefersWithoutArtistsAfterProcessingPlannedGroups(t *testing.T) {
	plannedID, deferredID := uuid.New(), uuid.New()
	snapshot := reconcileTestSnapshot(t, map[uuid.UUID]string{plannedID: "Artist", deferredID: ""}, nil)
	provider, admission := &reconcileProviderStub{}, &reconcileAdmissionStub{}
	service := NewIncomingMatchingReconciliation(&reconcileRefreshStub{}, reconcileSnapshotStub{snapshot}, provider, admission, reconcileRiverStub{})
	err := service.Reconcile(context.Background(), IncomingMatchingReconcileDelivery{EventID: uuid.New(), RiverJobID: 17, RiverAttempt: 2, Epoch: 9})
	if !errors.Is(err, ErrIncomingMatchingReconciliationDeferred) || len(admission.groups) != 1 || admission.groups[0] != plannedID {
		t.Fatalf("error=%v admissions=%v", err, admission.groups)
	}
}

func TestIncomingMatchingReconciliationDefersWhenMusicBrainzConfigMissing(t *testing.T) {
	groupID := uuid.New()
	snapshot := reconcileTestSnapshot(t, map[uuid.UUID]string{groupID: "Artist"}, nil)
	provider := &reconcileProviderStub{err: ErrMusicBrainzConfigurationMissing}
	admission := &reconcileAdmissionStub{}
	service := NewIncomingMatchingReconciliation(&reconcileRefreshStub{}, reconcileSnapshotStub{snapshot}, provider, admission, reconcileRiverStub{})
	err := service.Reconcile(context.Background(), IncomingMatchingReconcileDelivery{EventID: uuid.New(), RiverJobID: 17, RiverAttempt: 2, Epoch: 9})
	if !errors.Is(err, ErrIncomingMatchingReconciliationDeferred) || len(admission.groups) != 0 {
		t.Fatalf("error=%v admissions=%v", err, admission.groups)
	}
}

func TestIncomingMatchingReconciliationReturnsInfrastructureFailureForRetry(t *testing.T) {
	firstID, secondID := uuid.New(), uuid.New()
	snapshot := reconcileTestSnapshot(t, map[uuid.UUID]string{firstID: "First", secondID: "Second"}, nil)
	service := NewIncomingMatchingReconciliation(&reconcileRefreshStub{}, reconcileSnapshotStub{snapshot}, &reconcileProviderStub{}, &reconcileAdmissionStub{failOnCall: 2}, reconcileRiverStub{})
	if err := service.Reconcile(context.Background(), IncomingMatchingReconcileDelivery{EventID: uuid.New(), RiverJobID: 17, RiverAttempt: 2, Epoch: 9}); err == nil || errors.Is(err, ErrIncomingMatchingReconciliationDeferred) {
		t.Fatalf("error=%v, want retryable admission error", err)
	}
}

func TestIncomingMatchingCurrentReconciliationDefersArtistlessGroup(t *testing.T) {
	groupID := uuid.New()
	requests := &reconcileCurrentRequestsStub{requests: []persistence.IncomingMatchingCurrentRequest{{GroupID: groupID, RequestedGeneration: 3}}}
	provider, admission := &reconcileProviderStub{}, &reconcileAdmissionStub{}
	reconciler := NewIncomingMatchingReconciliation(&reconcileRefreshStub{}, reconcileSnapshotStub{reconcileTestSnapshot(t, map[uuid.UUID]string{groupID: ""}, nil)}, provider, admission, reconcileRiverStub{}, requests)
	err := reconciler.ReconcileCurrent(context.Background(), IncomingMatchingReconcileDelivery{EventID: uuid.New(), RiverJobID: 17, RiverAttempt: 2, Epoch: 9})
	if !errors.Is(err, ErrIncomingMatchingReconciliationDeferred) || len(provider.queries) != 0 || len(admission.groups) != 0 || len(requests.acked) != 0 {
		t.Fatalf("error=%v queries=%v admissions=%v acknowledgements=%#v", err, provider.queries, admission.groups, requests.acked)
	}
}

func TestIncomingMatchingCurrentReconciliationAcksObservedGenerationOnly(t *testing.T) {
	groupID := uuid.New()
	requests := &reconcileCurrentRequestsStub{requests: []persistence.IncomingMatchingCurrentRequest{{GroupID: groupID, RequestedGeneration: 7, ReconciledGeneration: 4}}}
	provider, admission := &reconcileProviderStub{}, &reconcileAdmissionStub{}
	snapshot := reconcileTestSnapshot(t, map[uuid.UUID]string{groupID: "Artist"}, nil)
	reconciler := NewIncomingMatchingReconciliation(&reconcileRefreshStub{}, reconcileSnapshotStub{snapshot}, provider, admission, reconcileRiverStub{}, requests)
	if err := reconciler.ReconcileCurrent(context.Background(), IncomingMatchingReconcileDelivery{EventID: uuid.New(), RiverJobID: 17, RiverAttempt: 2, Epoch: 9}); err != nil {
		t.Fatal(err)
	}
	if len(admission.groups) != 1 || len(requests.acked) != 1 || requests.acked[0].GroupID != groupID || requests.acked[0].RequestedGeneration != 7 {
		t.Fatalf("admissions=%v acknowledgements=%#v", admission.groups, requests.acked)
	}
}

func reconcileTestSnapshot(t *testing.T, groups map[uuid.UUID]string, _ []uuid.UUID) persistence.IncomingMatchingReconcileSnapshot {
	t.Helper()
	snapshot := persistence.IncomingMatchingReconcileSnapshot{}
	for groupID, artist := range groups {
		memberID := uuid.New()
		tags := `{"ARTIST":["` + artist + `"]}`
		if artist == "" {
			tags = `{}`
		}
		capture := persistence.IncomingGroupingCapture{VariantID: memberID, CaptureAnalysisID: reconcileUUIDPointer(uuid.New()), Ready: true, Tags: json.RawMessage(tags)}
		snapshot.Groups = append(snapshot.Groups, persistence.IncomingMatchingReconcileGroup{
			Group:          persistence.IncomingGroupingGroup{ID: groupID, Revision: "revision-" + groupID.String(), Members: []uuid.UUID{memberID}},
			Authority:      persistence.IncomingGroupMatchingAuthority{GroupID: groupID, Mode: "automatic"},
			MemberCaptures: map[uuid.UUID][]persistence.IncomingGroupingCapture{memberID: {capture}},
		})
	}
	return snapshot
}

func reconcileUUIDPointer(value uuid.UUID) *uuid.UUID { return &value }

type reconcileRiverStub struct{}

func (reconcileRiverStub) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("unexpected River insertion")
}
