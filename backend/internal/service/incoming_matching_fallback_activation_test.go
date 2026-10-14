package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type fallbackActivationProvider struct {
	prepared []string
	plan     persistence.AutomaticFallbackProviderSource
}

func (provider *fallbackActivationProvider) PrepareOperation(_ context.Context, kind, query string, offset, limit int, refresh bool) (*persistence.Operation, error) {
	provider.prepared = append(provider.prepared, kind+":"+query)
	input, _ := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}{1, query, offset, limit})
	prefix := "release-search"
	providerKind := "provider_release_search"
	if kind == "recording_search" {
		prefix, providerKind = "recording-search", "provider_recording_search"
	}
	key := prefix + ":0:10:" + query
	sourceID, providerID, identity := provider.plan.ID, provider.plan.ProviderID, provider.plan.ConfigurationIdentity
	return &persistence.Operation{ID: uuid.New(), Kind: providerKind, InputSnapshot: input, ProviderCacheKey: &key,
		ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderEndpoint: provider.plan.Endpoint, ProviderConfigurationIdentity: &identity}, nil
}

func (provider *fallbackActivationProvider) ResolveCurrentMusicBrainzSource(context.Context) (persistence.AutomaticFallbackProviderSource, error) {
	return provider.plan, nil
}

type fallbackActivationAdmission struct {
	artist    int
	fallbacks []persistence.AutomaticFallbackMatchingAdmission
}

func (admission *fallbackActivationAdmission) AdmitAutomaticArtistMatchingRun(context.Context, persistence.RiverInserter, persistence.AutomaticArtistMatchingAdmission) (persistence.AutomaticArtistMatchingAdmissionResult, error) {
	admission.artist++
	return persistence.AutomaticArtistMatchingAdmissionResult{}, nil
}

func (admission *fallbackActivationAdmission) AdmitAutomaticFallbackMatchingRun(_ context.Context, _ persistence.RiverInserter, value persistence.AutomaticFallbackMatchingAdmission) (persistence.AutomaticArtistMatchingAdmissionResult, error) {
	admission.fallbacks = append(admission.fallbacks, value)
	return persistence.AutomaticArtistMatchingAdmissionResult{}, nil
}

func TestCurrentReconcileActivatesArtistlessFallbackAtObservedGeneration(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	snapshot := fallbackActivationSnapshot(groupID, memberID, `{"ALBUM":["Album"],"TITLE":["Track"]}`)
	requests := &reconcileCurrentRequestsStub{requests: []persistence.IncomingMatchingCurrentRequest{{GroupID: groupID, RequestedGeneration: 12}}}
	provider := &fallbackActivationProvider{plan: fallbackActivationSource()}
	admission := &fallbackActivationAdmission{}
	reconciler := NewIncomingMatchingReconciliation(&reconcileRefreshStub{}, reconcileSnapshotStub{snapshot}, provider, admission, reconcileRiverStub{}, requests)
	if err := reconciler.ReconcileCurrent(context.Background(), IncomingMatchingReconcileDelivery{EventID: uuid.New(), RiverJobID: 17, RiverAttempt: 2, Epoch: 9}); err != nil {
		t.Fatal(err)
	}
	if admission.artist != 0 || len(admission.fallbacks) != 1 || admission.fallbacks[0].RequestGeneration != 12 {
		t.Fatalf("artist admissions=%d fallback admissions=%+v", admission.artist, admission.fallbacks)
	}
	if len(admission.fallbacks[0].Plan.Stages) != 3 || admission.fallbacks[0].Provider.Operation == nil || len(provider.prepared) != 1 {
		t.Fatalf("fallback plan=%+v provider requests=%v", admission.fallbacks[0].Plan, provider.prepared)
	}
	if len(requests.acked) != 1 || requests.acked[0].RequestedGeneration != 12 {
		t.Fatalf("acknowledged requests=%+v", requests.acked)
	}
}

func TestCurrentReconcileAdmitsEmptyFallbackWithoutProviderChild(t *testing.T) {
	groupID, memberID := uuid.New(), uuid.New()
	snapshot := fallbackActivationSnapshot(groupID, memberID, `{}`)
	requests := &reconcileCurrentRequestsStub{requests: []persistence.IncomingMatchingCurrentRequest{{GroupID: groupID, RequestedGeneration: 3}}}
	provider := &fallbackActivationProvider{plan: fallbackActivationSource()}
	admission := &fallbackActivationAdmission{}
	reconciler := NewIncomingMatchingReconciliation(&reconcileRefreshStub{}, reconcileSnapshotStub{snapshot}, provider, admission, reconcileRiverStub{}, requests)
	if err := reconciler.ReconcileCurrent(context.Background(), IncomingMatchingReconcileDelivery{EventID: uuid.New(), RiverJobID: 17, RiverAttempt: 2, Epoch: 9}); err != nil {
		t.Fatal(err)
	}
	if len(admission.fallbacks) != 1 || len(admission.fallbacks[0].Plan.Stages) != 0 || admission.fallbacks[0].Provider.Operation != nil || len(provider.prepared) != 0 {
		t.Fatalf("fallback admission=%+v prepared=%v", admission.fallbacks, provider.prepared)
	}
}

func fallbackActivationSnapshot(groupID, memberID uuid.UUID, tags string) persistence.IncomingMatchingReconcileSnapshot {
	capture := persistence.IncomingGroupingCapture{VariantID: memberID, CaptureAnalysisID: reconcileUUIDPointer(uuid.New()), Ready: true, Tags: json.RawMessage(tags)}
	return persistence.IncomingMatchingReconcileSnapshot{Groups: []persistence.IncomingMatchingReconcileGroup{{
		Group:          persistence.IncomingGroupingGroup{ID: groupID, Revision: "revision-" + groupID.String(), Members: []uuid.UUID{memberID}},
		Authority:      persistence.IncomingGroupMatchingAuthority{GroupID: groupID, Mode: "automatic"},
		MemberCaptures: map[uuid.UUID][]persistence.IncomingGroupingCapture{memberID: {capture}},
	}}}
}

func fallbackActivationSource() persistence.AutomaticFallbackProviderSource {
	return persistence.AutomaticFallbackProviderSource{ID: uuid.New(), ProviderID: uuid.New(), Endpoint: "https://musicbrainz.org/ws/2", ConfigurationIdentity: "identity"}
}
