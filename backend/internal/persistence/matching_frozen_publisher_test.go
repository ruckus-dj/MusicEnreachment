package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

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
