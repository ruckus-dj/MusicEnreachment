package persistence

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func commitEvidenceFixture(t *testing.T) (TrackPublicationAttempt, PublicationCommitOperationSnapshot, PublicationDeliveryGuard) {
	t.Helper()
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ApplicationAttempt: 1, RiverJobID: 1, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "delivery", Epoch: uuid.New(), CandidateGeneration: uuid.New(), ExpectedPositionID: uuid.New()}
	input := publicationInputsFixture(t, "audio")
	frozen, _ := json.Marshal(input)
	identity := PublicationDirectoryIdentity{Platform: "darwin", Volume: "vol", FileID: "id"}
	prep := []PublicationPreparationRecord{
		{Version: 1, Kind: "publication_preparation", OperationID: guard.OperationID, ClaimUUID: guard.ClaimUUID, OwnerToken: uuid.New(), Step: "intent", PrivateDirRel: "private", InputRel: "private/input", OutputRel: "private/stage", ParentIdentity: identity},
	}
	prep[0].DirectoryIdentity = nil
	prep[0].InputSize, prep[0].InputMTime = nil, nil
	created := prep[0]
	created.Step = "directory_created"
	created.DirectoryIdentity = &identity
	ready := created
	ready.Step = "input_ready"
	inputSize := int64(7)
	inputMTime := int64(2)
	ready.InputSize = &inputSize
	ready.InputMTime = &inputMTime
	n := int64(11)
	m := int64(3)
	stage := PublicationStageObservation{Version: 1, RelativePath: "private/stage", Ownership: ArtifactOwnership{ExpectedSize: &n, ExpectedMTime: &m}, PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActualAudio: json.RawMessage(`{"version":1,"facts":{"stream_index":0,"codec":"flac","lossless":true,"container":"flac","supported_source_container":true,"alac_capabilities_verified":false}}`), ActualMetadata: json.RawMessage(`{"version":1,"tags":{"Artist/Album/track.flac":["kept"]}}`)}
	intent := PublicationExposureIntent{Version: 1, StagedPath: stage.RelativePath, TargetCanonicalKey: "Artist/Album/track.flac", TargetRelativePath: "Artist/Album/track.flac", Expectation: "absent", PrepOwner: prep[0].OwnerToken, OutputRootParent: identity, TargetParent: identity}
	entries := []any{prep[0], created, ready,
		struct {
			Version     int                         `json:"version"`
			Kind        string                      `json:"kind"`
			From        string                      `json:"from"`
			To          string                      `json:"to"`
			Observation PublicationStageObservation `json:"observation"`
		}{1, "stage_observation", "preparing", "staged", stage},
		struct {
			Version     int                         `json:"version"`
			Kind        string                      `json:"kind"`
			From        string                      `json:"from"`
			To          string                      `json:"to"`
			Observation PublicationStageObservation `json:"observation"`
		}{1, "stage_observation", "staged", "validated", stage},
		publicationExposureJournalRecord{Version: 1, Kind: "publication_exposure", From: "validated", To: "exposing", Guard: guard, PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, CandidateGeneration: guard.CandidateGeneration, Intent: intent, Current: json.RawMessage(`null`), Observation: stage},
		publicationCandidateInstalledJournalRecord{Version: 1, Kind: "publication_candidate_installed", From: "exposing", To: "exposing", Guard: guard, PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, CandidateGeneration: guard.CandidateGeneration, Intent: intent, Observation: PublicationCandidateInstalledObservation{Version: 1, TargetRelativePath: intent.TargetRelativePath, Identity: PublicationFileIdentity{SizeBytes: &n, MTime: &m, SHA256: stage.PayloadSHA256}}},
	}
	journal, _ := json.Marshal(entries)
	manifest, _ := json.Marshal(struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}{1, []PublicationNamespaceNode{{CanonicalKey: "Artist", RelativePath: "Artist", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "Artist/Album", RelativePath: "Artist/Album", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: intent.TargetCanonicalKey, RelativePath: intent.TargetRelativePath, NodeKind: "file"}}})
	return TrackPublicationAttempt{OperationID: guard.OperationID, PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, CandidateGeneration: guard.CandidateGeneration, IntentKind: "audio", Phase: "exposing", FrozenInputs: frozen, NamespaceManifest: manifest, MutationJournal: journal}, PublicationCommitOperationSnapshot{State: "running", Stage: "exposing"}, guard
}

func TestAssessFirstPublicationCommitReturnsPartialSourceFreeProjection(t *testing.T) {
	attempt, operation, guard := commitEvidenceFixture(t)
	original, _ := json.Marshal(attempt)
	got, err := AssessFirstPublicationCommit(attempt, operation, nil, guard)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.Tags["Artist/Album/track.flac"][0] != "kept" {
		t.Fatalf("legitimate path-like tag was lost: %#v", got.Metadata)
	}
	if got.CurrentAudio.Version != 1 || got.Ownership.Version != 1 || got.EvidenceClassification != "evidence_consistency_only_namespace_not_revalidated" {
		t.Fatalf("unexpected projection: %#v", got)
	}
	projectionJSON := string(mustPublicationCommitJSON(t, got))
	if bytes.Contains([]byte(projectionJSON), []byte(attempt.FrozenInputs)) || bytes.Contains([]byte(projectionJSON), []byte(inputSourcePath(attempt))) {
		t.Fatal("projection retained frozen/source input references")
	}
	if !reflect.DeepEqual(got.MissingRequirements, []string{"asset_outcomes", "quality_policy_provenance", "namespace_revalidation", "live_delivery_fence"}) {
		t.Fatalf("unexpected requirements: %v", got.MissingRequirements)
	}
	if string(original) != string(mustPublicationCommitJSON(t, attempt)) {
		t.Fatal("assessment mutated caller input")
	}
}

func TestPublicationCommitMetadataRejectsUnknownAndNullFields(t *testing.T) {
	for _, raw := range []string{`{"version":1,"tags":{},"source_history":{}}`, `{"version":1,"tags":{"x":null}}`, `{"version":1,"tags":null}`, `null`} {
		var value PublicationCommitMetadata
		if err := strictMetadata(json.RawMessage(raw), &value); err == nil {
			t.Fatalf("accepted malformed metadata %s", raw)
		}
	}
}

func TestAssessFirstPublicationCommitRejectsMismatchedEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*TrackPublicationAttempt, *PublicationCommitOperationSnapshot, *PublicationDeliveryGuard)
	}{
		{"guard", func(_ *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, guard *PublicationDeliveryGuard) {
			guard.DeliveryToken = "other"
		}},
		{"hash", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			attempt.MutationJournal = bytesReplace(attempt.MutationJournal, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), []byte("not-a-digest"))
		}},
		{"path", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			attempt.MutationJournal = bytesReplace(attempt.MutationJournal, []byte(`"target_relative_path":"Artist/Album/track.flac"`), []byte(`"target_relative_path":"../outside"`))
		}},
		{"reordered stage", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			attempt.MutationJournal = bytesReplace(attempt.MutationJournal, []byte(`"from":"preparing","to":"staged"`), []byte(`"from":"staged","to":"validated"`))
		}},
		{"duplicate stage", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			var entries []json.RawMessage
			_ = json.Unmarshal(attempt.MutationJournal, &entries)
			entries = append(entries, entries[3])
			attempt.MutationJournal, _ = json.Marshal(entries)
		}},
		{"bare manifest", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			attempt.NamespaceManifest = bytesReplace(attempt.NamespaceManifest, []byte(`{"version":1,"nodes":`), nil)
			attempt.NamespaceManifest = bytesReplace(attempt.NamespaceManifest, []byte(`}`), nil)
		}},
		{"prep owner", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			attempt.MutationJournal = bytesReplace(attempt.MutationJournal, []byte(`"prep_owner":"`), []byte(`"prep_owner":"00000000-0000-0000-0000-000000000000`))
		}},
		{"installed identity", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			attempt.MutationJournal = bytesReplace(attempt.MutationJournal, []byte(`"size_bytes":11`), []byte(`"size_bytes":12`))
		}},
		{"first expectation not absent", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			attempt.MutationJournal = bytesReplace(attempt.MutationJournal, []byte(`"current_snapshot":null`), []byte(`"current_snapshot":{}`))
		}},
		{"first exposure expectation is expected current", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			var entries []json.RawMessage
			_ = json.Unmarshal(attempt.MutationJournal, &entries)
			var exposure publicationExposureJournalRecord
			_ = json.Unmarshal(entries[5], &exposure)
			exposure.Intent.Expectation = "expected_current"
			entries[5], _ = json.Marshal(exposure)
			attempt.MutationJournal, _ = json.Marshal(entries)
		}},
		{"exposure target differs from candidate target", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			var entries []json.RawMessage
			_ = json.Unmarshal(attempt.MutationJournal, &entries)
			var exposure publicationExposureJournalRecord
			_ = json.Unmarshal(entries[5], &exposure)
			exposure.Intent.TargetRelativePath = "Artist/Album/other.flac"
			entries[5], _ = json.Marshal(exposure)
			attempt.MutationJournal, _ = json.Marshal(entries)
		}},
		{"validated stage differs from preparation output", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			var entries []json.RawMessage
			_ = json.Unmarshal(attempt.MutationJournal, &entries)
			var ready PublicationPreparationRecord
			_ = json.Unmarshal(entries[2], &ready)
			ready.OutputRel = "private/other-stage"
			entries[2], _ = json.Marshal(ready)
			attempt.MutationJournal, _ = json.Marshal(entries)
		}},
		{"preparation after stage", func(attempt *TrackPublicationAttempt, _ *PublicationCommitOperationSnapshot, _ *PublicationDeliveryGuard) {
			var entries []json.RawMessage
			_ = json.Unmarshal(attempt.MutationJournal, &entries)
			entries[0], entries[3] = entries[3], entries[0]
			attempt.MutationJournal, _ = json.Marshal(entries)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempt, operation, guard := commitEvidenceFixture(t)
			test.change(&attempt, &operation, &guard)
			if _, err := AssessFirstPublicationCommit(attempt, operation, nil, guard); err == nil {
				t.Fatal("invalid evidence was accepted")
			}
		})
	}
}

func inputSourcePath(attempt TrackPublicationAttempt) string {
	input, _ := DecodePublicationInputs(attempt.FrozenInputs)
	return input.Audio.ConfiguredRootPath
}

func bytesReplace(value json.RawMessage, old, replacement []byte) json.RawMessage {
	return json.RawMessage(bytes.Replace(value, old, replacement, 1))
}

func mustPublicationCommitJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
