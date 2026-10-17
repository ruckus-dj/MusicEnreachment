package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestPublicationExposureIntentValidation(t *testing.T) {
	base := PublicationExposureIntent{
		Version: 1, StagedPath: "private/staged.flac", TargetCanonicalKey: "album/track.flac",
		TargetRelativePath: "Album/Track.flac", Expectation: "absent", PrepOwner: uuid.New(),
		OutputRootParent: PublicationDirectoryIdentity{Platform: "darwin", Volume: "v", FileID: "1"},
		TargetParent:     PublicationDirectoryIdentity{Platform: "darwin", Volume: "v", FileID: "2"},
	}
	tests := []struct {
		name string
		edit func(*PublicationExposureIntent)
	}{
		{"valid", func(*PublicationExposureIntent) {}},
		{"unsupported version", func(i *PublicationExposureIntent) { i.Version++ }},
		{"traversal", func(i *PublicationExposureIntent) { i.StagedPath = "private/../x" }},
		{"missing owner", func(i *PublicationExposureIntent) { i.PrepOwner = uuid.Nil }},
		{"foreign replacement", func(i *PublicationExposureIntent) { i.Expectation = "foreign_replacement" }},
		{"bad identity", func(i *PublicationExposureIntent) { i.TargetParent.FileID = " " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			intent := base
			test.edit(&intent)
			valid := validExposureIntent(intent)
			if (test.name == "valid") != valid {
				t.Fatalf("validExposureIntent() = %v", valid)
			}
		})
	}
}

func TestPublicationExposureRequiresStrictStageWitnessAndPreparationChain(t *testing.T) {
	size, mtime := int64(10), int64(20)
	outputSize, outputMtime := int64(97), int64(40)
	lossless := true
	audio, _ := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: PersistedAudioFacts{Codec: "flac", Lossless: &lossless}})
	obs := PublicationStageObservation{Version: 1, RelativePath: "private/out.flac", Ownership: ArtifactOwnership{ExpectedSize: &outputSize, ExpectedMTime: &outputMtime}, PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActualAudio: audio, ActualMetadata: json.RawMessage(`{}`)}
	intent := PublicationExposureIntent{
		Version: 1, StagedPath: obs.RelativePath, TargetCanonicalKey: "album/track.flac",
		TargetRelativePath: "Album/Track.flac", Expectation: "absent",
	}
	if !stageMatchesIntent(obs, intent) {
		t.Fatal("complete staged witness rejected")
	}
	obs.PayloadSHA256 = "not-a-digest"
	if stageMatchesIntent(obs, intent) {
		t.Fatal("malformed staged digest accepted")
	}

	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
	owner := uuid.New()
	identity := PublicationDirectoryIdentity{Platform: "darwin", Volume: "vol", FileID: "parent"}
	dir := PublicationDirectoryIdentity{Platform: "darwin", Volume: "vol", FileID: "private"}
	inputSize, inputMTime := size, mtime
	plan := PublicationPreparationRecord{Version: 1, Kind: "publication_preparation", Step: "intent", OperationID: guard.OperationID, ClaimUUID: guard.ClaimUUID, OwnerToken: owner, PrivateDirRel: "private", InputRel: "private/source.wav", OutputRel: "private/out.flac", ParentIdentity: identity}
	created := plan
	created.Step = "directory_created"
	created.DirectoryIdentity = &dir
	ready := created
	ready.Step = "input_ready"
	ready.InputSize = &inputSize
	ready.InputMTime = &inputMTime
	entries := []PublicationPreparationRecord{plan, created, ready}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	intent.PrepOwner, intent.OutputRootParent, intent.TargetParent = owner, identity, identity
	obs.PayloadSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if !validExposureIntent(intent) {
		t.Fatal("complete exposure intent rejected")
	}
	if !validPublicationStageObservation(obs) {
		t.Fatal("complete staged observation rejected")
	}
	if !stageMatchesIntent(obs, intent) {
		t.Fatal("staged observation does not match exposure intent")
	}
	if !preparationOwnsIntent(raw, guard, intent, obs) {
		t.Fatal("valid preparation ownership chain rejected after intent and stage witness passed validation")
	}
	intent.StagedPath = plan.InputRel
	if preparationOwnsIntent(raw, guard, intent, obs) {
		t.Fatal("input path accepted as staged output")
	}
	intent.StagedPath = plan.OutputRel
	intent.PrepOwner = uuid.New()
	if preparationOwnsIntent(raw, guard, intent, obs) {
		t.Fatal("caller-selected preparation owner accepted")
	}
}

func TestPublicationExposureRequiresTypedValidatedStageJournalObservation(t *testing.T) {
	size, mtime := int64(98), int64(41)
	obs := PublicationStageObservation{Version: 1, RelativePath: "private/output.flac", Ownership: ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime}, PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActualAudio: json.RawMessage(`{"version":1,"facts":{"codec":"flac","stream_index":0,"lossless":true}}`), ActualMetadata: json.RawMessage(`{}`)}
	first := obs
	first.Ownership = ArtifactOwnership{}
	first.ActualAudio = json.RawMessage(`{}`)
	first.ActualMetadata = json.RawMessage(`{}`)
	first.PayloadSHA256 = ""
	wrap := func(from, to string, observation PublicationStageObservation) json.RawMessage {
		entry, err := json.Marshal(struct {
			Version     int                         `json:"version"`
			Kind        string                      `json:"kind"`
			From        string                      `json:"from"`
			To          string                      `json:"to"`
			Observation PublicationStageObservation `json:"observation"`
		}{1, "stage_observation", from, to, observation})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	journal, _ := json.Marshal([]json.RawMessage{wrap("preparing", "staged", first), wrap("staged", "validated", obs)})
	if !validatedStageJournalObservation(journal, obs) {
		t.Fatal("pending initial observation with complete validated readback rejected")
	}
	forged := obs
	forgedSize := size + 1
	forged.Ownership.ExpectedSize = &forgedSize
	journal, _ = json.Marshal([]json.RawMessage{wrap("preparing", "staged", first), wrap("staged", "validated", forged)})
	if validatedStageJournalObservation(journal, obs) {
		t.Fatal("request observation differing from durable validated ownership accepted")
	}
	forged = obs
	forged.PayloadSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	journal, _ = json.Marshal([]json.RawMessage{wrap("preparing", "staged", first), wrap("staged", "validated", forged)})
	if validatedStageJournalObservation(journal, obs) {
		t.Fatal("request observation differing from durable validated record accepted")
	}
	journal, _ = json.Marshal([]json.RawMessage{wrap("preparing", "staged", first), json.RawMessage(`{"version":1,"kind":"stage_observation","from":"staged","to":"validated","observation":{"relative_path":"private/output.flac"}}`)})
	if validatedStageJournalObservation(journal, obs) {
		t.Fatal("malformed typed stage record accepted")
	}
}

func TestPublicationExposureReplayOnlyMatchesIdenticalJournalObservation(t *testing.T) {
	lossless := true
	audio, _ := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: PersistedAudioFacts{Codec: "flac", Lossless: &lossless}})
	obs := PublicationStageObservation{Version: 1, RelativePath: "staged/file.flac", ActualAudio: audio, ActualMetadata: json.RawMessage(`{}`)}
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
	attempt := &TrackPublicationAttempt{PositionID: uuid.New(), Epoch: uuid.New(), CandidateGeneration: uuid.New()}
	intent := PublicationExposureIntent{Version: 1}
	record := publicationExposureJournalRecord{Version: 1, Kind: "publication_exposure", From: "validated", To: "exposing", Guard: guard, PositionID: attempt.PositionID, Epoch: attempt.Epoch, CandidateGeneration: attempt.CandidateGeneration, Intent: intent, Current: json.RawMessage(`null`), Observation: obs}
	entry, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]json.RawMessage{entry})
	if got, err := exposureReplay(raw, guard, attempt, intent, obs); err != nil || got != PublicationExposureAlreadyRecorded {
		t.Fatalf("identical replay = %q, %v", got, err)
	}
	obs.RelativePath = "staged/other.flac"
	if got, err := exposureReplay(raw, guard, attempt, intent, obs); err != nil || got != PublicationExposureStale {
		t.Fatalf("different replay = %q, %v", got, err)
	}
	obs.RelativePath = "staged/file.flac"
	intent.Expectation = "expected_current"
	if got, err := exposureReplay(raw, guard, attempt, intent, obs); err != nil || got != PublicationExposureStale {
		t.Fatalf("different intent replay = %q, %v", got, err)
	}
}
