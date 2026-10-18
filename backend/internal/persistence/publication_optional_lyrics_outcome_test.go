package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestOptionalLyricsRebuiltRemovalRequiresObservedAbsence(t *testing.T) {
	_, prep, evidence, guard := optionalLyricsRebuildFixture()
	input := publicationInputsFixture(t, "rewrite")
	input.Version = PublicationInputsVersion
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	input.ManualLyrics = &PublicationManualLyricsInput{State: "explicit_empty", ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration}
	input.Rewrite.Ownership = clonePublicationFileIdentity(evidence.InputIdentity)
	input.Rewrite.Ownership.MTime = prep.InputMTime
	marshal := func(value any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	identity := clonePublicationFileIdentity(evidence.InputIdentity)
	evidence.Kind = PublicationOptionalLyricsRebuiltWithoutLyrics
	evidence.ManualLyrics = *input.ManualLyrics
	evidence.Diagnostic = "optional lyrics did not round-trip; rebuilt output without lyrics"
	evidence.OutputIdentity = identity
	evidence.FinalObservation = PublicationStageObservation{
		Version: 1, RelativePath: prep.OutputRel,
		Ownership: ArtifactOwnership{ExpectedSize: identity.SizeBytes, ExpectedMTime: identity.MTime}, PayloadSHA256: identity.SHA256,
		ActualAudio:          marshal(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts}),
		ActualMetadata:       marshal(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{}}),
		ActualAssets:         marshal(PublicationAssetOutcomes{Version: 1, Cover: PublicationCoverOutcome{State: PublicationAssetAbsent}, Lyrics: PublicationLyricsOutcome{State: PublicationAssetAbsent}}),
		QualityPolicyVersion: input.QualityPolicyVersion,
	}
	if !validOptionalLyricsOutcomeEvidence(evidence, guard, input) {
		t.Fatal("verified rebuilt removal was rejected")
	}
	evidence.FinalObservation.ActualMetadata = marshal(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"LYRICS": {"[00:01.00]still present"}}})
	if validOptionalLyricsOutcomeEvidence(evidence, guard, input) {
		t.Fatal("removal accepted observed lyrics")
	}
}

func optionalLyricsRebuildFixture() ([]PublicationCandidateRebuildRecord, PublicationPreparationRecord, PublicationOptionalLyricsOutcomeEvidence, PublicationDeliveryGuard) {
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
	owner := uuid.New()
	directory := PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk", FileID: "private-dir"}
	sourceSize, copiedSize, copiedMTime := int64(42), int64(42), int64(200)
	sourceMTime := int64(100)
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	copied := &PublicationFileIdentity{SizeBytes: &copiedSize, MTime: &copiedMTime}
	prep := PublicationPreparationRecord{Version: PublicationPreparationVersion, Kind: "publication_preparation", Step: "input_ready", OperationID: guard.OperationID, ClaimUUID: guard.ClaimUUID, OwnerToken: owner, PrivateDirRel: "private/op", InputRel: "private/op/input.flac", OutputRel: "private/op/output.flac", ParentIdentity: directory, DirectoryIdentity: &directory, InputSize: &sourceSize, InputMTime: &sourceMTime, CopiedInput: copied}
	identity := func(size, mtime int64) *PublicationFileIdentity {
		return &PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hash}
	}
	rebuild := func(step string, candidate *PublicationFileIdentity) PublicationCandidateRebuildRecord {
		return PublicationCandidateRebuildRecord{Version: PublicationCandidateRebuildVersion, Kind: "publication_candidate_rebuild", Step: step, Guard: guard, OwnerToken: owner, PrivateDirRel: prep.PrivateDirRel, InputRel: prep.InputRel, OutputRel: prep.OutputRel, DirectoryIdentity: directory, Candidate: candidate, Input: copied}
	}
	evidence := PublicationOptionalLyricsOutcomeEvidence{Preparation: prep, InputIdentity: *identity(copiedSize, copiedMTime)}
	return []PublicationCandidateRebuildRecord{rebuild("intent", identity(99, 300)), rebuild("complete", identity(99, 300))}, prep, evidence, guard
}

func TestOptionalLyricsRebuildAcceptsIdenticalCandidateMeasurements(t *testing.T) {
	records, prep, evidence, guard := optionalLyricsRebuildFixture()
	if !validOptionalLyricsRebuildPair(records, prep, evidence, guard) {
		t.Fatal("identical pre/post candidate measurements are valid rebuild evidence")
	}
}

func TestOptionalLyricsRebuildRejectsForeignPreparationOwnerOrDirectory(t *testing.T) {
	for _, mutate := range []func(*PublicationPreparationRecord){
		func(prep *PublicationPreparationRecord) { prep.OwnerToken = uuid.New() },
		func(prep *PublicationPreparationRecord) { prep.DirectoryIdentity.FileID = "foreign-dir" },
	} {
		records, prep, evidence, guard := optionalLyricsRebuildFixture()
		mutate(&prep)
		if validOptionalLyricsRebuildPair(records, prep, evidence, guard) {
			t.Fatal("foreign preparation ownership paired with rebuild evidence")
		}
	}
}

func TestOptionalLyricsPreparedInputHashMustMatchCopiedAndFrozenIdentity(t *testing.T) {
	records, prep, evidence, guard := optionalLyricsRebuildFixture()
	wrong := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	prep.CopiedInput.SHA256 = records[0].Candidate.SHA256
	evidence.Preparation = prep
	evidence.InputIdentity.SHA256 = wrong
	if validOptionalLyricsRebuildPair(records, prep, evidence, guard) {
		t.Fatal("prepared input with a mismatched hash was accepted")
	}
}

func TestOptionalLyricsPreparedInputMustMatchFrozenRewrite(t *testing.T) {
	_, prep, evidence, _ := optionalLyricsRebuildFixture()
	size := *prep.InputSize
	hash := evidence.InputIdentity.SHA256
	frozenMTime := int64(100)
	rewrite := &PublicationRewriteInput{Ownership: PublicationFileIdentity{SizeBytes: &size, MTime: &frozenMTime, SHA256: hash}}
	if !optionalLyricsInputMatchesFrozenRewrite(evidence, rewrite) {
		t.Fatal("prepared copy with matching size and hash but a different copy mtime was rejected")
	}
	wrongSize := size + 1
	copyMismatch := evidence
	copyMismatch.Preparation = prep
	copiedIdentity := clonePublicationFileIdentity(*prep.CopiedInput)
	copyMismatch.Preparation.CopiedInput = &copiedIdentity
	copyMismatch.Preparation.CopiedInput.SizeBytes = &wrongSize
	copyMismatch.InputIdentity.SizeBytes = &wrongSize
	if optionalLyricsInputMatchesFrozenRewrite(copyMismatch, rewrite) {
		t.Fatal("copied input size differing from frozen size was accepted")
	}
	rewrite.Ownership.SizeBytes = &wrongSize
	if optionalLyricsInputMatchesFrozenRewrite(evidence, rewrite) {
		t.Fatal("prepared source size differing from frozen size was accepted")
	}
	wrongHash := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	rewrite.Ownership.SizeBytes = &size
	rewrite.Ownership.SHA256 = wrongHash
	if optionalLyricsInputMatchesFrozenRewrite(evidence, rewrite) {
		t.Fatal("prepared input hash differing from frozen hash was accepted")
	}
}

func TestCanonicalOptionalLyricsPreparationRejectsReplayWithDifferentReadyRecord(t *testing.T) {
	_, prep, evidence, guard := optionalLyricsRebuildFixture()
	intent := prep
	intent.Step, intent.DirectoryIdentity, intent.InputSize, intent.InputMTime, intent.CopiedInput = "intent", nil, nil, nil, nil
	created := intent
	created.Step = "directory_created"
	created.DirectoryIdentity = prep.DirectoryIdentity
	preparations := []PublicationPreparationRecord{intent, created, prep}
	if !canonicalOptionalLyricsPreparation(preparations, evidence, guard) {
		t.Fatal("canonical preparation chain rejected")
	}
	bad := prep
	bad.OwnerToken = uuid.New()
	preparations[2] = bad
	if canonicalOptionalLyricsPreparation(preparations, evidence, guard) {
		t.Fatal("replayed evidence with different preparation owner was accepted")
	}
}
