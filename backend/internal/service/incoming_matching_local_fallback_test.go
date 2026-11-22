package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type reconcileOutageFallbackStub struct {
	runID      uuid.UUID
	generation int64
	target     persistence.MatchingLayoutTarget
	commits    []persistence.AutomaticLocalFallbackCommit
}

func (stub *reconcileOutageFallbackStub) CurrentMatchingMusicBrainzOutageFallback(_ context.Context, _ uuid.UUID, generation int64) (uuid.UUID, error) {
	if generation == stub.generation {
		return stub.runID, nil
	}
	return uuid.Nil, nil
}

func (stub *reconcileOutageFallbackStub) CaptureMatchingLayoutTarget(context.Context, uuid.UUID, string) (persistence.MatchingLayoutTarget, error) {
	return stub.target, nil
}

func (stub *reconcileOutageFallbackStub) ApplyAutomaticLocalFallback(_ context.Context, commit persistence.AutomaticLocalFallbackCommit) (persistence.AutomaticLocalFallbackResult, error) {
	stub.commits = append(stub.commits, commit)
	return persistence.AutomaticLocalFallbackResult{Applied: true}, nil
}

type reconcileLocalLayoutStub struct {
	layout MatchingLocalLayoutStage
}

func (stub reconcileLocalLayoutStub) CalculateLayout(context.Context, []persistence.IncomingGroupingCapture, float64) (MatchingLocalLayoutStage, error) {
	return stub.layout, nil
}

// TestCurrentReconciliationHandlesOutageLocalFallbackBeforeArtistAdmission
// protects the continuation boundary: an outage-specific generation must never
// also admit MusicBrainz, a later generation must continue to ordinary
// admission, and a proved absence must settle without partial application.
func TestCurrentReconciliationHandlesOutageLocalFallbackBeforeArtistAdmission(t *testing.T) {
	delivery := IncomingMatchingReconcileDelivery{EventID: uuid.New(), RiverJobID: 17, RiverAttempt: 2, Epoch: 9}
	releaseID := uuid.New()
	selected := MatchingLocalLayoutStage{
		Stage: MatchingLayoutStage{
			Decision:          MatchingLayoutSelection{Status: MatchingLayoutSelectionSelected},
			ProviderReleaseID: &releaseID,
			Assignments:       []MatchingLayoutAssignment{{FileMemberID: uuid.New(), ProviderTrackID: uuid.New()}},
		},
		HeaderDigest: "headers",
		ReleaseIDs:   []uuid.UUID{releaseID},
		LayoutDigest: "layout",
	}
	newReconciler := func(groupID uuid.UUID, requestGeneration int64, outage *reconcileOutageFallbackStub, layout MatchingLocalLayoutStage) (*IncomingMatchingReconciliation, *reconcileProviderStub, *reconcileAdmissionStub, *reconcileCurrentRequestsStub) {
		provider, admission := &reconcileProviderStub{}, &reconcileAdmissionStub{}
		requests := &reconcileCurrentRequestsStub{requests: []persistence.IncomingMatchingCurrentRequest{{GroupID: groupID, RequestedGeneration: requestGeneration}}}
		reconciler := NewIncomingMatchingReconciliation(&reconcileRefreshStub{}, reconcileSnapshotStub{reconcileTestSnapshot(t, map[uuid.UUID]string{groupID: "Artist"}, nil)}, provider, admission, reconcileRiverStub{}, requests).
			WithLocalFallback(outage, reconcileLocalLayoutStub{layout: layout})
		return reconciler, provider, admission, requests
	}

	t.Run("applies full layout without artist admission", func(t *testing.T) {
		groupID, runID := uuid.New(), uuid.New()
		outage := &reconcileOutageFallbackStub{runID: runID, generation: 8}
		reconciler, provider, admission, requests := newReconciler(groupID, 8, outage, selected)
		if err := reconciler.ReconcileCurrent(context.Background(), delivery); err != nil {
			t.Fatal(err)
		}
		if len(provider.queries) != 0 || len(admission.groups) != 0 || len(requests.acked) != 0 {
			t.Fatalf("queries=%v admissions=%v acknowledgements=%#v", provider.queries, admission.groups, requests.acked)
		}
		if len(outage.commits) != 1 || outage.commits[0].GroupID != groupID || outage.commits[0].OutageRunID != runID || outage.commits[0].ObservedRequestGeneration != 8 {
			t.Fatalf("commits=%#v", outage.commits)
		}
	})

	t.Run("later generation continues to artist admission", func(t *testing.T) {
		groupID := uuid.New()
		outage := &reconcileOutageFallbackStub{runID: uuid.New(), generation: 4}
		reconciler, provider, admission, requests := newReconciler(groupID, 8, outage, selected)
		if err := reconciler.ReconcileCurrent(context.Background(), delivery); err != nil {
			t.Fatal(err)
		}
		if len(provider.queries) != 1 || len(admission.groups) != 1 || admission.groups[0] != groupID || len(outage.commits) != 0 {
			t.Fatalf("queries=%v admissions=%v commits=%#v", provider.queries, admission.groups, outage.commits)
		}
		if len(requests.acked) != 1 || requests.acked[0].GroupID != groupID || requests.acked[0].RequestedGeneration != 8 {
			t.Fatalf("acknowledgements=%#v", requests.acked)
		}
	})

	t.Run("no usable layout settles without admitting", func(t *testing.T) {
		groupID := uuid.New()
		outage := &reconcileOutageFallbackStub{runID: uuid.New(), generation: 8}
		reconciler, provider, admission, requests := newReconciler(groupID, 8, outage, MatchingLocalLayoutStage{Stage: MatchingLayoutStage{Decision: MatchingLayoutSelection{Status: MatchingLayoutSelectionNone}}})
		if err := reconciler.ReconcileCurrent(context.Background(), delivery); err != nil {
			t.Fatal(err)
		}
		if len(provider.queries) != 0 || len(admission.groups) != 0 || len(outage.commits) != 0 {
			t.Fatalf("queries=%v admissions=%v commits=%#v", provider.queries, admission.groups, outage.commits)
		}
		if len(requests.acked) != 1 || requests.acked[0].GroupID != groupID || requests.acked[0].RequestedGeneration != 8 {
			t.Fatalf("acknowledgements=%#v", requests.acked)
		}
	})
}
