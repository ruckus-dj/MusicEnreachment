package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestFrozenInventoryMatchesAcceptedProjectionsEmptySeal(t *testing.T) {
	if !frozenInventoryMatchesAcceptedProjections(nil, nil) {
		t.Fatal("empty review inventory did not match empty accepted projections")
	}
	if !frozenInventoryMatchesAcceptedProjections([]MatchingRelease{}, []MatchingFallbackLookupReceipt{}) {
		t.Fatal("empty review inventory did not match empty accepted projection list")
	}
	if frozenInventoryMatchesAcceptedProjections([]MatchingRelease{{ID: uuid.New(), ProviderKey: "release-1"}}, nil) {
		t.Fatal("review inventory without receipts matched")
	}
	if frozenInventoryMatchesAcceptedProjections(nil, []MatchingFallbackLookupReceipt{{AcceptedProjection: json.RawMessage(`{}`)}}) {
		t.Fatal("receipt without a projection matched an empty inventory")
	}
}

func TestFrozenInventoryMatchesAcceptedProjectionsNormalizesEmptyTrackLists(t *testing.T) {
	id := uuid.New()
	release := MatchingRelease{ID: id, ProviderKey: "release-1", Complete: true}
	receipt := MatchingFallbackLookupReceipt{AcceptedProjection: frozenProjection(t, release)}
	emptyTracks := release
	emptyTracks.Tracks = []MatchingReleaseTrack{}

	if !frozenInventoryMatchesAcceptedProjections([]MatchingRelease{emptyTracks}, []MatchingFallbackLookupReceipt{receipt}) {
		t.Fatal("empty track list did not match a nil track list")
	}
	if len(emptyTracks.Tracks) != 0 {
		t.Fatal("comparison mutated the review inventory track list")
	}
}

func TestFrozenInventoryMatchesAcceptedProjectionsConvertsZeroBasedCoordinates(t *testing.T) {
	id, trackID := uuid.New(), uuid.New()
	release := MatchingRelease{ID: id, ProviderKey: "release-1", Complete: true, Tracks: []MatchingReleaseTrack{{
		ID: trackID, Medium: 0, Position: 0, DisplayedNumber: "1", RecordingProviderKey: "recording-1", RecordingTitle: "Song",
	}}}
	receipt := MatchingFallbackLookupReceipt{AcceptedProjection: frozenProjection(t, release)}
	reviewed := release
	reviewed.Tracks = []MatchingReleaseTrack{{
		ID: trackID, Medium: 1, Position: 1, DisplayedNumber: "1", RecordingProviderKey: "recording-1", RecordingTitle: "Song",
	}}

	if !frozenInventoryMatchesAcceptedProjections([]MatchingRelease{reviewed}, []MatchingFallbackLookupReceipt{receipt}) {
		t.Fatal("accepted zero-based coordinates did not match the one-based review inventory")
	}
	if reviewed.Tracks[0].Medium != 1 || reviewed.Tracks[0].Position != 1 {
		t.Fatal("comparison mutated the review inventory coordinates")
	}
	zeroBasedInventory := release
	if frozenInventoryMatchesAcceptedProjections([]MatchingRelease{zeroBasedInventory}, []MatchingFallbackLookupReceipt{receipt}) {
		t.Fatal("zero-based review inventory matched accepted projections")
	}
}

func TestFrozenInventoryMatchesAcceptedProjectionsKeepsIdentityAndDuplicateChecks(t *testing.T) {
	id, otherID := uuid.New(), uuid.New()
	release := MatchingRelease{ID: id, ProviderKey: "release-1", Complete: true}
	receipt := MatchingFallbackLookupReceipt{AcceptedProjection: frozenProjection(t, release)}
	inventory := []MatchingRelease{release}

	if frozenInventoryMatchesAcceptedProjections(inventory, []MatchingFallbackLookupReceipt{receipt, receipt}) {
		t.Fatal("duplicate accepted projection provider keys matched")
	}
	missingID := MatchingRelease{ProviderKey: "release-1", Complete: true}
	if frozenInventoryMatchesAcceptedProjections(inventory, []MatchingFallbackLookupReceipt{{AcceptedProjection: frozenProjection(t, missingID)}}) {
		t.Fatal("accepted projection without a release identity matched")
	}
	missingKey := MatchingRelease{ID: id, Complete: true}
	if frozenInventoryMatchesAcceptedProjections(inventory, []MatchingFallbackLookupReceipt{{AcceptedProjection: frozenProjection(t, missingKey)}}) {
		t.Fatal("accepted projection without a provider key matched")
	}
	otherInventory := []MatchingRelease{{ID: otherID, ProviderKey: "release-1", Complete: true}}
	if frozenInventoryMatchesAcceptedProjections(otherInventory, []MatchingFallbackLookupReceipt{receipt}) {
		t.Fatal("review inventory with a different release identity matched")
	}
	if frozenInventoryMatchesAcceptedProjections(inventory, nil) {
		t.Fatal("review inventory without accepted projections matched")
	}
}

func frozenProjection(t *testing.T, release MatchingRelease) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(ProviderMatchingSnapshot{Releases: []MatchingRelease{release}})
	if err != nil {
		t.Fatalf("marshal accepted projection: %v", err)
	}
	return encoded
}

func TestFrozenAuthorityMatchesNoQuerySealAndRejectsIdentityChanges(t *testing.T) {
	runID, sourceID, providerID, groupID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	targetCapture := json.RawMessage(`{"group":"frozen"}`)
	derived := MatchingFallbackSealAuthority{
		SchemaVersion: 3, Disposition: "qualified", RunID: runID, WaitRevision: 4,
		Target: MatchingRunFenceSnapshot{
			Version: 2, GroupID: groupID, GroupRevision: "revision-1",
			Capture: targetCapture, CaptureDigest: "capture-digest",
		},
		SourceID: sourceID, ProviderID: providerID, Endpoint: "https://provider.example",
		SourceConfiguration: "source-config", RunProviderConfiguration: "run-config", Digest: "seal-digest",
	}
	caller := FrozenMatchingAuthorityIdentity{
		SealSchemaVersion: 3, RunID: runID, WaitRevision: 4, SealDigest: derived.Digest,
		SealDisposition: derived.Disposition, TargetFenceVersion: 2, TargetCaptureDigest: derived.Target.CaptureDigest,
		TargetCapture: targetCapture, SourceID: sourceID, ProviderID: providerID, Endpoint: derived.Endpoint,
		SourceConfiguration: derived.SourceConfiguration, RunProviderConfiguration: derived.RunProviderConfiguration,
		RunSourceID: sourceID, RunProviderID: providerID, RunEndpoint: derived.Endpoint,
		RunConfiguration: derived.RunProviderConfiguration, GroupID: groupID, GroupRevision: derived.Target.GroupRevision,
	}
	if !frozenAuthorityMatches(caller, derived) {
		t.Fatal("valid no-query caller authority did not match its persisted seal")
	}

	tamperedSource := caller
	tamperedSource.SourceID = uuid.New()
	if frozenAuthorityMatches(tamperedSource, derived) {
		t.Fatal("caller with a different source identity matched the persisted seal")
	}

	tamperedTarget := caller
	tamperedTarget.TargetCaptureDigest = "different-capture-digest"
	if frozenAuthorityMatches(tamperedTarget, derived) {
		t.Fatal("caller with a different target capture digest matched the persisted seal")
	}
}
