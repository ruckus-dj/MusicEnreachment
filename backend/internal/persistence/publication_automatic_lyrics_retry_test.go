package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestAutomaticLyricsRetryInputRejectsManualAuthorityAndMixedModes(t *testing.T) {
	input := publicationInputsFixture(t, "rewrite")
	input.Format = publication.AudioFormatSource
	input.AutomaticLyricsRetry = &PublicationAutomaticLyricsRetryInput{
		ExpectedMetadataRevision: 3, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration,
		CurrentAssets: PublicationAssetOutcomes{Version: PublicationAssetOutcomesVersion, Cover: PublicationCoverOutcome{State: PublicationAssetAbsent}, Lyrics: PublicationLyricsOutcome{State: PublicationAssetAbsent}},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("valid retry input rejected: %v", err)
	}
	manual := librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false)
	input.AutomaticLyricsRetry.Desired = &manual
	if err := input.Validate(); err == nil {
		t.Fatal("explicit-empty manual authority permitted automatic retry")
	}
	input.AutomaticLyricsRetry.Desired = nil
	input.ManualLyrics = &PublicationManualLyricsInput{ExpectedMetadataRevision: 3, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration, State: "explicit_empty"}
	if err := input.Validate(); err == nil {
		t.Fatal("mixed manual and automatic retry modes were accepted")
	}
}

func TestAutomaticLyricsRetryJournalRequiresAcquisitionAndStrictRecords(t *testing.T) {
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New(), CandidateGeneration: uuid.New(), ExpectedPositionID: uuid.New(), Epoch: uuid.New()}
	input := publicationInputsFixture(t, "rewrite")
	input.Format = publication.AudioFormatSource
	input.Rewrite.CurrentGeneration = uuid.New()
	input.AutomaticLyricsRetry = &PublicationAutomaticLyricsRetryInput{ExpectedMetadataRevision: 2, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration, CurrentAssets: PublicationAssetOutcomes{Version: PublicationAssetOutcomesVersion, Cover: PublicationCoverOutcome{State: PublicationAssetAbsent}, Lyrics: PublicationLyricsOutcome{State: PublicationAssetAbsent}}}
	for _, raw := range []string{`[{"version":1,"kind":"unknown"}]`, `[{"version":1,"kind":"publication_automatic_lyrics_retry_outcome"}]`} {
		var entries []json.RawMessage
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			t.Fatal(err)
		}
		if _, err := parseAutomaticLyricsRetryPrefix(entries, guard, input); err == nil {
			t.Fatalf("accepted malformed retry journal %s", raw)
		}
	}
}

func TestAutomaticLyricsRetryInterruptedRebuildIntentIsRetainedWithoutReplayAuthority(t *testing.T) {
	_, guard, input, _, journal, _ := automaticLyricsRetryCommitFixture(t)
	entries, err := decodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	var preparation PublicationPreparationRecord
	if err := json.Unmarshal(entries[2], &preparation); err != nil {
		t.Fatal(err)
	}
	size, mtime := int64(88), int64(900)
	candidate := PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: strings.Repeat("c", 64)}
	copiedInput := clonePublicationFileIdentity(*preparation.CopiedInput)
	intent := PublicationCandidateRebuildRecord{
		Version: PublicationCandidateRebuildVersion, Kind: "publication_candidate_rebuild", Step: "intent", Guard: guard,
		OwnerToken: preparation.OwnerToken, PrivateDirRel: preparation.PrivateDirRel, InputRel: preparation.InputRel,
		OutputRel: preparation.OutputRel, DirectoryIdentity: *preparation.DirectoryIdentity,
		Candidate: &candidate, Input: &copiedInput,
	}
	interrupted := append(append([]json.RawMessage(nil), entries[:4]...), mustJSON(intent))
	var acquisition PublicationAutomaticLyricsRetryAcquisition
	if err := json.Unmarshal(interrupted[3], &acquisition); err != nil {
		t.Fatal(err)
	}
	acquisition.Source, acquisition.Classification = "lrclib", "found"
	acquisition.Lyrics, acquisition.Metadata.Duration = "[00:01.00]retry", 1
	lyricsDigest := sha256.Sum256([]byte(acquisition.Lyrics))
	acquisition.SHA256 = hex.EncodeToString(lyricsDigest[:])
	interrupted[3] = mustJSON(acquisition)
	state, err := parseAutomaticLyricsRetryPrefix(interrupted, guard, input)
	if err != nil || len(state.rebuilds) != 1 || state.rebuilds[0].Step != "intent" || state.outcome != nil || state.exposure != nil {
		t.Fatalf("interrupted rebuild intent was not retained as unresolved evidence: state=%+v err=%v", state, err)
	}
	if _, boundary, ok := canonicalAutomaticLyricsRetryCompletionPrefix(interrupted, guard, &TrackPublicationAttempt{OperationID: guard.OperationID, PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, ExpectedGeneration: &input.Rewrite.CurrentGeneration, CandidateGeneration: guard.CandidateGeneration, IntentKind: "rewrite", Phase: "preparing"}); ok || boundary != 0 {
		t.Fatalf("interrupted rebuild intent granted completion/replay authority: boundary=%d ok=%v", boundary, ok)
	}
	complete := intent
	complete.Step = "complete"
	candidateCopy := clonePublicationFileIdentity(candidate)
	complete.Candidate = &candidateCopy
	completed := append(append([]json.RawMessage(nil), interrupted...), mustJSON(complete))
	state, err = parseAutomaticLyricsRetryPrefix(completed, guard, input)
	if err != nil || len(state.rebuilds) != 2 || state.rebuilds[1].Step != "complete" {
		t.Fatalf("complete native rebuild evidence rejected: state=%+v err=%v", state, err)
	}
}

func TestAutomaticLyricsRetryReplacementWriterAcceptsOnlyFourOrderedFrontiers(t *testing.T) {
	_, guard, input, _, journal, _ := automaticLyricsRetryCommitFixture(t)
	entries, err := decodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	const replacementStart = 8
	for index := 0; index < 4; index++ {
		prefix := append([]json.RawMessage(nil), entries[:replacementStart+index]...)
		var evidence PublicationReplacementEvidence
		if err := json.Unmarshal(entries[replacementStart+index], &evidence); err != nil {
			t.Fatal(err)
		}
		t.Run(evidence.From+"_to_"+evidence.To, func(t *testing.T) {
			state, err := validateAutomaticLyricsRetryReplacementAppend(prefix, guard, input, evidence)
			if err != nil || len(state.replacement) != index {
				t.Fatalf("valid writer frontier %d rejected: replacements=%d err=%v", index, len(state.replacement), err)
			}
			wrongTransition := evidence
			wrongTransition.To = "invalid_frontier"
			if _, err := validateAutomaticLyricsRetryReplacementAppend(prefix, guard, input, wrongTransition); err == nil {
				t.Fatal("out-of-order proposed replacement append accepted")
			}
			wrongGuard := evidence
			wrongGuard.Guard.DeliveryToken += "-replay"
			if _, err := validateAutomaticLyricsRetryReplacementAppend(prefix, guard, input, wrongGuard); err == nil {
				t.Fatal("replacement append with replayed guard accepted")
			}
		})
	}
	finalFrontier := append([]json.RawMessage(nil), entries[:replacementStart+4]...)
	var fourth PublicationReplacementEvidence
	if err := json.Unmarshal(entries[replacementStart+3], &fourth); err != nil {
		t.Fatal(err)
	}
	if _, err := validateAutomaticLyricsRetryReplacementAppend(finalFrontier, guard, input, fourth); err == nil {
		t.Fatal("fifth replacement append accepted")
	}
	if _, err := validateAutomaticLyricsRetryReplacementAppend(entries, guard, input, fourth); err == nil {
		t.Fatal("append after retry commit accepted")
	}
}

func TestAutomaticLyricsRetryCommitPreservesSparseOldAudioAgainstRichStage(t *testing.T) {
	_, guard, input, _, journal, _ := automaticLyricsRetryCommitFixture(t)
	entries, err := decodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	state, err := parseAutomaticLyricsRetryPrefix(entries[:12], guard, input)
	if err != nil {
		t.Fatal(err)
	}
	var record replacementPublicationCommitWitness
	if err := json.Unmarshal(entries[12], &record); err != nil {
		t.Fatal(err)
	}
	old := new(TrackPublication)
	if err := json.Unmarshal(state.exposure.Current, old); err != nil {
		t.Fatal(err)
	}
	var oldAudio PublicationCurrentAudio
	if err := decodePublicationCurrentContract(old.ActualAudio, &oldAudio); err != nil {
		t.Fatal(err)
	}
	oldAudioRaw := mustJSON(oldAudio)
	state.exposure.Current = mustJSON(TrackPublication{
		PositionID: old.PositionID, Epoch: old.Epoch, Generation: old.Generation,
		RelativePath: old.RelativePath, Ownership: old.Ownership, ActualAudio: oldAudioRaw,
		ActualMetadata: old.ActualMetadata, ActualAssets: old.ActualAssets,
		QualityPolicyVersion: old.QualityPolicyVersion,
	})
	bitrate := 320000
	richAudio := oldAudio
	richAudio.Facts.Bitrate = &bitrate
	state.exposure.Observation.ActualAudio = mustJSON(richAudio)
	state.outcome.FinalObservation.ActualAudio = mustJSON(richAudio)
	for index := range state.replacement {
		state.replacement[index].Staged.ActualAudio = mustJSON(richAudio)
	}
	record.Projection.ActualAudio = oldAudioRaw
	if !validAutomaticLyricsRetryCommit(record, state, guard, input) {
		t.Fatal("commit rejected sparse G0 audio retained alongside richer staged observations")
	}
	record.Projection.ActualAudio = mustJSON(richAudio)
	if validAutomaticLyricsRetryCommit(record, state, guard, input) {
		t.Fatal("commit accepted staged audio instead of preserving the exposed G0 audio")
	}
}

func TestAutomaticLyricsRetryProjectionIsBoundedAndCurrentGenerationScoped(t *testing.T) {
	generation := uuid.New()
	acquisition := PublicationAutomaticLyricsRetryAcquisition{
		Version: automaticLyricsRetryVersion, Kind: automaticLyricsRetryAcquisitionKind,
		Authority: PublicationAutomaticLyricsRetryInput{ExpectedMetadataRevision: 8},
		Source:    "none", Classification: "no_result",
	}
	outcome := PublicationAutomaticLyricsRetryOutcome{Version: automaticLyricsRetryVersion, Kind: automaticLyricsRetryOutcomeKind, Outcome: PublicationAutomaticLyricsRetryNoResult}
	projection, err := automaticLyricsRetryProjection(acquisition, outcome, generation)
	if err != nil {
		t.Fatalf("automatic lyrics retry projection rejected: %v", err)
	}
	if !validPublicationInitialLyricsProjection(*projection, generation) || projection.Source != "none" || projection.Outcome != PublicationAutomaticLyricsRetryNoResult || projection.AuthorityMetadataRevision != 8 {
		t.Fatalf("unexpected bounded retry projection: %#v", projection)
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), "lyric text must not persist") {
		t.Fatalf("projection retained lyrics content: %s", encoded)
	}
	if _, err := automaticLyricsRetryProjection(acquisition, outcome, uuid.New()); err != nil {
		t.Fatalf("new generation projection should be independently valid: %v", err)
	}
}

func TestAutomaticLyricsRetryCompleteCommitAndCleanupJournal(t *testing.T) {
	attempt, guard, input, current, journal, cleanup := automaticLyricsRetryCommitFixture(t)
	entries, err := decodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	prefix, boundary, ok := canonicalAutomaticLyricsRetryCompletionPrefix(entries, guard, &attempt)
	if !ok || boundary != 13 || prefix.commit.ManualLyricsReceipt != nil {
		state, parseErr := parseAutomaticLyricsRetryPrefix(entries[:13], guard, input)
		base := append(append(append(append([]json.RawMessage(nil), entries[:3]...), entries[5:7]...), entries[7]), entries[8:12]...)
		_, sharedOK := validateCanonicalReplacementPrefixSharedRaw(base, guard, &attempt)
		t.Fatalf("valid retry prefix rejected: boundary=%d ok=%v state=%#v parseErr=%v shared=%v", boundary, ok, state, parseErr, sharedOK)
	}
	if !retryCommitReplayMatches(entries, guard, &attempt, current) {
		t.Fatal("valid retry commit did not replay against its current projection")
	}
	withCleanup := append(append([]json.RawMessage(nil), entries...), cleanup...)
	if !retryCommitReplayMatches(withCleanup, guard, &attempt, current) {
		t.Fatal("valid cleanup suffix rejected for retry commit")
	}
	if !sameJSONValue(current.InitialLyrics, prefix.commit.Projection.InitialLyrics) {
		t.Fatal("current retry projection differs from commit witness")
	}
	for _, mutate := range []func(*publicationReplacementCleanupRecord){
		func(r *publicationReplacementCleanupRecord) { r.Name = "foreign" },
		func(r *publicationReplacementCleanupRecord) { r.Result = "retained" },
		func(r *publicationReplacementCleanupRecord) { r.Guard.DeliveryToken = "foreign" },
	} {
		bad := append([]json.RawMessage(nil), withCleanup...)
		var record publicationReplacementCleanupRecord
		if err := json.Unmarshal(bad[13], &record); err != nil {
			t.Fatal(err)
		}
		mutate(&record)
		bad[13], _ = json.Marshal(record)
		if retryCommitReplayMatches(bad, guard, &attempt, current) {
			t.Fatalf("malformed cleanup suffix accepted: %#v", record)
		}
	}
	_ = input
}

func TestAutomaticLyricsRetryCommittedFallbackReplayAndCleanup(t *testing.T) {
	attempt, guard, _, current, journal, cleanup := automaticLyricsRetryCommitFixture(t)
	entries, err := decodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	var preparation PublicationPreparationRecord
	var acquisition PublicationAutomaticLyricsRetryAcquisition
	var outcome PublicationAutomaticLyricsRetryOutcome
	var commit replacementPublicationCommitWitness
	for _, item := range []struct {
		index  int
		target any
	}{{2, &preparation}, {3, &acquisition}, {4, &outcome}, {12, &commit}} {
		if err := json.Unmarshal(entries[item.index], item.target); err != nil {
			t.Fatal(err)
		}
	}
	acquisition.Source, acquisition.Classification = "lrclib", "found"
	acquisition.Lyrics, acquisition.Metadata.Duration = "[00:01.00]retry", 1.25
	digest := sha256.Sum256([]byte(acquisition.Lyrics))
	acquisition.SHA256 = hex.EncodeToString(digest[:])
	outcome.Outcome, outcome.Warning = PublicationOptionalLyricsRebuiltWithoutLyrics, "readback_mismatch"
	projection, err := automaticLyricsRetryProjection(acquisition, outcome, guard.CandidateGeneration)
	if err != nil {
		t.Fatal(err)
	}
	current.InitialLyrics = mustJSON(projection)
	commit.Projection = current
	entries[3], entries[4], entries[12] = mustJSON(acquisition), mustJSON(outcome), mustJSON(commit)
	failedCandidate := clonePublicationFileIdentity(outcome.OutputIdentity)
	failedCandidate.SHA256 = strings.Repeat("c", 64)
	copied := clonePublicationFileIdentity(outcome.InputIdentity)
	intent := PublicationCandidateRebuildRecord{Version: PublicationCandidateRebuildVersion, Kind: "publication_candidate_rebuild", Step: "intent", Guard: guard,
		OwnerToken: preparation.OwnerToken, PrivateDirRel: preparation.PrivateDirRel, InputRel: preparation.InputRel, OutputRel: preparation.OutputRel,
		DirectoryIdentity: *preparation.DirectoryIdentity, Candidate: &failedCandidate, Input: &copied}
	complete := intent
	complete.Step = "complete"
	complete.Candidate = &outcome.OutputIdentity
	withRebuild := append(append([]json.RawMessage(nil), entries[:4]...), mustJSON(intent), mustJSON(complete))
	withRebuild = append(withRebuild, entries[4:]...)
	claim := PublicationExecutionClaim{OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt, RiverJobID: guard.RiverJobID, RiverAttempt: guard.RiverAttempt,
		ClaimUUID: guard.ClaimUUID, BootUUID: guard.BootUUID, DeliveryToken: guard.DeliveryToken, Epoch: guard.Epoch, State: "recovery_required"}
	for suffix := 0; suffix <= len(cleanup); suffix++ {
		candidate := append(append([]json.RawMessage(nil), withRebuild...), cleanup[:suffix]...)
		attempt.MutationJournal = mustJSON(candidate)
		if _, boundary, ok := canonicalAutomaticLyricsRetryCompletionPrefix(candidate, guard, &attempt); !ok || boundary != 15 {
			t.Fatalf("fallback cleanup suffix %d: boundary=%d valid=%v", suffix, boundary, ok)
		}
		if !retryCommitReplayMatches(candidate, guard, &attempt, current) {
			t.Fatalf("fallback replay rejected suffix %d", suffix)
		}
		if frontier, err := publicationRecoveryJournalFrontier(attempt, claim); err != nil || frontier != "committed" {
			t.Fatalf("fallback recovery suffix %d: frontier=%q err=%v", suffix, frontier, err)
		}
	}
	bad := append([]json.RawMessage(nil), withRebuild...)
	complete.Candidate = &failedCandidate
	bad[5] = mustJSON(complete)
	if retryCommitReplayMatches(bad, guard, &attempt, current) {
		t.Fatal("altered rebuild completion accepted on committed replay")
	}
	bad = append(append([]json.RawMessage(nil), withRebuild...), cleanup...)
	var cleanupRecord publicationReplacementCleanupRecord
	if err := json.Unmarshal(bad[15], &cleanupRecord); err != nil {
		t.Fatal(err)
	}
	cleanupRecord.Name = "foreign"
	bad[15] = mustJSON(cleanupRecord)
	if retryCommitReplayMatches(bad, guard, &attempt, current) {
		t.Fatal("malformed fallback cleanup accepted on committed replay")
	}
}

func TestAutomaticLyricsRetryParserRetainsEveryReplacementBoundary(t *testing.T) {
	attempt, guard, input, _, journal, _ := automaticLyricsRetryCommitFixture(t)
	entries, err := decodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	for count := 1; count <= 3; count++ {
		prefix := entries[:8+count]
		state, err := parseAutomaticLyricsRetryPrefix(prefix, guard, input)
		if err != nil || len(state.replacement) != count || state.commit != nil {
			t.Fatalf("replacement boundary %d not retained: count=%d commit=%v err=%v", count, len(state.replacement), state.commit != nil, err)
		}
		attempt.Phase = "exposing"
		attempt.FrozenInputs, _ = json.Marshal(input)
		attempt.MutationJournal = mustJSON(prefix)
		claim := retryRecoveryClaim(guard)
		current := retryFixtureOldCurrent(t, prefix)
		snapshot := PublicationRecoverySnapshot{Attempt: attempt, Operation: PublicationCommitOperationSnapshot{State: "running", Stage: "exposing"}, Claim: claim, OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt, RiverJobID: guard.RiverJobID, CurrentEpoch: guard.Epoch, CurrentKnown: true, Current: &current}
		assessment, err := AssessPublicationRecoveryEvidence(snapshot)
		if err != nil || assessment.JournalFrontier != "exposing" {
			t.Fatalf("replacement boundary %d was not retained for recovery: %#v err=%v", count, assessment, err)
		}
	}
}

func TestAutomaticLyricsRetryNoResultWarningsStayOnResolution(t *testing.T) {
	_, guard, input, _, journal, _ := automaticLyricsRetryCommitFixture(t)
	entries, _ := decodeJournal(journal)
	tests := []struct{ classification, warning string }{
		{"metadata_incomplete", "lyrics_lookup_metadata_incomplete"},
		{"lookup_failed", "lyrics_lookup_failed"},
		{"invalid_synced_text", "lyrics_lookup_invalid_synced_text"},
	}
	for _, test := range tests {
		var acquisition PublicationAutomaticLyricsRetryAcquisition
		if err := json.Unmarshal(entries[3], &acquisition); err != nil {
			t.Fatal(err)
		}
		acquisition.Classification, acquisition.Warning = test.classification, test.warning
		trial := append([]json.RawMessage(nil), entries[:5]...)
		trial[3] = mustJSON(acquisition)
		state, err := parseAutomaticLyricsRetryPrefix(trial, guard, input)
		if err != nil || state.outcome == nil || state.outcome.Warning != "" {
			t.Fatalf("warning %q was not kept solely on acquisition: outcome=%#v err=%v", test.warning, state.outcome, err)
		}
		projection, err := automaticLyricsRetryProjection(acquisition, *state.outcome, guard.CandidateGeneration)
		if err != nil || projection.ResolutionWarningCode != test.warning || projection.OutcomeWarningCode != "" {
			t.Fatalf("warning projection is not truthful: %#v err=%v", projection, err)
		}
	}
}

func TestAutomaticLyricsRetryFoundReadbackFallbackIsStrictlyBounded(t *testing.T) {
	_, guard, input, _, journal, _ := automaticLyricsRetryCommitFixture(t)
	entries, _ := decodeJournal(journal)
	var acquisition PublicationAutomaticLyricsRetryAcquisition
	var outcome PublicationAutomaticLyricsRetryOutcome
	if err := json.Unmarshal(entries[3], &acquisition); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(entries[4], &outcome); err != nil {
		t.Fatal(err)
	}
	lyrics := "[00:01.00]retry lyrics\n"
	digest := sha256.Sum256([]byte(lyrics))
	acquisition.Source, acquisition.Classification, acquisition.Lyrics, acquisition.SHA256 = "lrclib", "found", lyrics, hex.EncodeToString(digest[:])
	acquisition.Metadata.Duration = 180
	outcome.Outcome, outcome.Warning = PublicationOptionalLyricsRebuiltWithoutLyrics, "readback_mismatch"
	assets := PublicationAssetOutcomes{Version: PublicationAssetOutcomesVersion, Cover: input.AutomaticLyricsRetry.CurrentAssets.Cover, Lyrics: PublicationLyricsOutcome{State: PublicationAssetAbsent}}
	outcome.FinalObservation.ActualAssets = mustJSON(assets)
	if !validAutomaticLyricsRetryOutcome(outcome, acquisition, guard, input) {
		t.Fatal("valid no-lyrics readback fallback was rejected")
	}
	for name, mutate := range map[string]func(*PublicationAutomaticLyricsRetryOutcome){
		"wrong warning": func(r *PublicationAutomaticLyricsRetryOutcome) { r.Warning = "lookup_failed" },
		"changed mandatory tag": func(r *PublicationAutomaticLyricsRetryOutcome) {
			r.FinalObservation.ActualMetadata = mustJSON(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"TITLE": {"changed"}}})
		},
		"embedded lyrics despite fallback": func(r *PublicationAutomaticLyricsRetryOutcome) {
			embedded := assets
			embedded.Lyrics = PublicationLyricsOutcome{State: PublicationAssetEmbedded, SHA256: acquisition.SHA256}
			r.FinalObservation.ActualAssets = mustJSON(embedded)
		},
	} {
		bad := outcome
		mutate(&bad)
		if validAutomaticLyricsRetryOutcome(bad, acquisition, guard, input) {
			t.Errorf("accepted invalid fallback: %s", name)
		}
	}
}

func TestAutomaticLyricsRetryRejectsChangedMandatoryTagsOnOutcomeAndReplay(t *testing.T) {
	attempt, guard, input, current, journal, _ := automaticLyricsRetryCommitFixture(t)
	entries, _ := decodeJournal(journal)
	var outcome PublicationAutomaticLyricsRetryOutcome
	if err := json.Unmarshal(entries[4], &outcome); err != nil {
		t.Fatal(err)
	}
	outcome.FinalObservation.ActualMetadata = mustJSON(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"TITLE": {"substituted"}}})
	entries[4] = mustJSON(outcome)
	if _, err := parseAutomaticLyricsRetryPrefix(entries[:5], guard, input); err == nil {
		t.Fatal("outcome with substituted mandatory metadata was accepted")
	}
	entries, _ = decodeJournal(journal)
	var exposure publicationExposureJournalRecord
	if err := json.Unmarshal(entries[7], &exposure); err != nil {
		t.Fatal(err)
	}
	var old TrackPublication
	if err := json.Unmarshal(exposure.Current, &old); err != nil {
		t.Fatal(err)
	}
	old.ActualMetadata = mustJSON(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"TITLE": {"foreign"}}})
	exposure.Current = mustJSON(old)
	entries[7] = mustJSON(exposure)
	if retryCommitReplayMatches(entries, guard, &attempt, current) {
		t.Fatal("committed replay accepted changed mandatory tags versus frozen old projection")
	}
}

func TestAutomaticLyricsRetryActiveRecoveryIsEvidenceOnly(t *testing.T) {
	attempt, guard, input, _, journal, _ := automaticLyricsRetryCommitFixture(t)
	attempt.Phase = "exposing"
	attempt.FrozenInputs, _ = json.Marshal(input)
	attempt.MutationJournal = journal
	// A pre-commit attempt must not contain replacement commit evidence.
	entries, _ := decodeJournal(journal)
	entries = entries[:8]
	attempt.MutationJournal, _ = json.Marshal(entries)
	claim := retryRecoveryClaim(guard)
	current := retryFixtureOldCurrent(t, entries)
	snapshot := PublicationRecoverySnapshot{Attempt: attempt, Operation: PublicationCommitOperationSnapshot{State: "running", Stage: "exposing"}, Claim: claim, OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt, RiverJobID: guard.RiverJobID, CurrentEpoch: guard.Epoch, CurrentKnown: true, Current: &current}
	assessment, err := AssessPublicationRecoveryEvidence(snapshot)
	if err != nil {
		t.Fatalf("active retry recovery evidence rejected: %v", err)
	}
	if assessment.Classification != "automatic_lyrics_retry_evidence_consistency_only_no_replay" || assessment.JournalFrontier != "exposing" {
		t.Fatalf("retry was not classified as evidence-only: %#v", assessment)
	}
	if fmt.Sprint(assessment.RequiredObservations) == "[]" {
		t.Fatal("retry recovery omitted required filesystem/quiescence observations")
	}
}

func retryFixtureOldCurrent(t *testing.T, entries []json.RawMessage) TrackPublication {
	t.Helper()
	var exposure publicationExposureJournalRecord
	if len(entries) <= 7 || json.Unmarshal(entries[7], &exposure) != nil {
		t.Fatal("retry fixture lacks exposure old-current witness")
	}
	var current TrackPublication
	if json.Unmarshal(exposure.Current, &current) != nil {
		t.Fatal("decode retry fixture old current")
	}
	return current
}

func retryRecoveryClaim(guard PublicationDeliveryGuard) PublicationExecutionClaim {
	return PublicationExecutionClaim{OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt, RiverJobID: guard.RiverJobID, RiverAttempt: guard.RiverAttempt, ClaimUUID: guard.ClaimUUID, BootUUID: guard.BootUUID, DeliveryToken: guard.DeliveryToken, Epoch: guard.Epoch, State: "recovery_required"}
}

func automaticLyricsRetryCommitFixture(t *testing.T) (TrackPublicationAttempt, PublicationDeliveryGuard, PublicationInputs, TrackPublication, json.RawMessage, []json.RawMessage) {
	t.Helper()
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ApplicationAttempt: 1, RiverJobID: 11, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "retry-delivery", Epoch: uuid.New(), CandidateGeneration: uuid.New(), ExpectedPositionID: uuid.New()}
	input := publicationInputsFixture(t, "rewrite")
	input.Format = publication.AudioFormatSource
	input.Rewrite.RelativePath = "Artist/Album/track.flac"
	input.AutomaticLyricsRetry = &PublicationAutomaticLyricsRetryInput{ExpectedMetadataRevision: 7, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration, CurrentAssets: PublicationAssetOutcomes{Version: PublicationAssetOutcomesVersion, Cover: PublicationCoverOutcome{State: PublicationAssetAbsent}, Lyrics: PublicationLyricsOutcome{State: PublicationAssetAbsent}}}
	parent := PublicationDirectoryIdentity{Platform: "darwin", Volume: "vol", FileID: "parent"}
	dir := PublicationDirectoryIdentity{Platform: "darwin", Volume: "vol", FileID: "private"}
	inputSize, inputMTime, outputSize, outputMTime := int64(42), int64(100), int64(99), int64(200)
	inputHash := strings.Repeat("b", 64)
	outputHash := strings.Repeat("a", 64)
	owner := uuid.New()
	prep0 := PublicationPreparationRecord{Version: 1, Kind: "publication_preparation", Step: "intent", OperationID: guard.OperationID, ClaimUUID: guard.ClaimUUID, OwnerToken: owner, PrivateDirRel: "private/op", InputRel: "private/op/input.flac", OutputRel: "private/op/output.flac", ParentIdentity: parent}
	prep1 := prep0
	prep1.Step = "directory_created"
	prep1.DirectoryIdentity = &dir
	copied := PublicationFileIdentity{SizeBytes: &inputSize, MTime: &inputMTime, SHA256: inputHash}
	prep2 := prep1
	prep2.Step = "input_ready"
	prep2.InputSize = &inputSize
	prep2.InputMTime = &inputMTime
	prep2.CopiedInput = &copied
	stage := PublicationStageObservation{Version: 1, RelativePath: prep2.OutputRel, Ownership: ArtifactOwnership{ExpectedSize: &outputSize, ExpectedMTime: &outputMTime}, PayloadSHA256: outputHash, ActualAudio: mustJSON(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts}), ActualMetadata: mustJSON(PublicationCommitMetadata{Version: 1, Tags: input.DesiredTags}), ActualAssets: mustJSON(input.AutomaticLyricsRetry.CurrentAssets), QualityPolicyVersion: input.QualityPolicyVersion}
	intent := PublicationExposureIntent{Version: 1, StagedPath: stage.RelativePath, TargetCanonicalKey: "Artist/Album/track.flac", TargetRelativePath: "Artist/Album/track.flac", Expectation: "expected_current", PrepOwner: owner, OutputRootParent: parent, TargetParent: parent}
	oldGeneration := input.Rewrite.CurrentGeneration
	oldIdentity := clonePublicationFileIdentity(input.Rewrite.Ownership)
	oldIdentity.SizeBytes = &inputSize
	oldIdentity.MTime = &inputMTime
	oldIdentity.SHA256 = inputHash
	old := TrackPublication{PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, Generation: oldGeneration, RelativePath: intent.TargetRelativePath, Ownership: mustJSON(PublicationCurrentOwnership{Version: 1, Identity: oldIdentity}), ActualAudio: stage.ActualAudio, ActualMetadata: stage.ActualMetadata, ActualAssets: stage.ActualAssets, QualityPolicyVersion: input.QualityPolicyVersion}
	input.Rewrite.Ownership = oldIdentity
	acq := PublicationAutomaticLyricsRetryAcquisition{Version: automaticLyricsRetryVersion, Kind: automaticLyricsRetryAcquisitionKind, Guard: guard, Authority: *input.AutomaticLyricsRetry, Classification: "no_result", Metadata: lrclib.Metadata{}, Source: "none"}
	out := PublicationAutomaticLyricsRetryOutcome{Version: automaticLyricsRetryVersion, Kind: automaticLyricsRetryOutcomeKind, Guard: guard, Outcome: PublicationAutomaticLyricsRetryNoResult, Preparation: prep2, InputIdentity: copied, OutputIdentity: PublicationFileIdentity{SizeBytes: &outputSize, MTime: &outputMTime, SHA256: outputHash}, FinalObservation: stage}
	exposure := publicationExposureJournalRecord{Version: 1, Kind: "publication_exposure", From: "validated", To: "exposing", Guard: guard, PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, CandidateGeneration: guard.CandidateGeneration, ExpectedGeneration: &oldGeneration, Intent: intent, Current: mustJSON(old), Observation: stage}
	repl := make([]PublicationReplacementEvidence, 4)
	transitions := [][2]string{{"exposing", "backup_intent"}, {"backup_intent", "backup_ready"}, {"backup_ready", "replacement_intent"}, {"replacement_intent", "installed"}}
	for i, pair := range transitions {
		id := oldIdentity
		if i == 3 {
			id = PublicationFileIdentity{SizeBytes: &outputSize, MTime: &outputMTime, SHA256: outputHash}
		}
		repl[i] = PublicationReplacementEvidence{Version: 1, Kind: "publication_replacement", From: pair[0], To: pair[1], Guard: guard, PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, CandidateGeneration: guard.CandidateGeneration, ExpectedGeneration: &oldGeneration, Intent: intent, Staged: stage, Identity: id, BackupIdentity: oldIdentity, BackupPath: prep2.InputRel}
	}
	base := []any{prep0, prep1, prep2, acq, out, retryStageRecord("preparing", "staged", stage), retryStageRecord("staged", "validated", stage), exposure, repl[0], repl[1], repl[2], repl[3]}
	projection, err := automaticLyricsRetryProjection(acq, out, guard.CandidateGeneration)
	if err != nil {
		t.Fatal(err)
	}
	newCurrent := old
	newCurrent.Generation = guard.CandidateGeneration
	newCurrent.Ownership = mustJSON(PublicationCurrentOwnership{Version: 1, Identity: repl[3].Identity})
	newCurrent.InitialLyrics = mustJSON(projection)
	commit := replacementPublicationCommitWitness{Version: replacementPublicationCommitVersion, Kind: "publication_replacement_commit", Guard: guard, PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, ExpectedGeneration: oldGeneration, CandidateGeneration: guard.CandidateGeneration, Projection: newCurrent}
	base = append(base, commit)
	journal := make([]json.RawMessage, 0, len(base))
	for _, value := range base {
		journal = append(journal, mustJSON(value))
	}
	witness := PublicationReplacementCleanupWitness{PrivateDirRel: prep2.PrivateDirRel, BackupRel: prep2.InputRel, Directory: dir, Parent: parent, Backup: oldIdentity}
	cleanup := []json.RawMessage{mustJSON(publicationReplacementCleanupRecord{Version: publicationReplacementCleanupVersion, Kind: "publication_replacement_cleanup", Guard: guard, Witness: witness, Step: "backup_intent", Name: witness.BackupRel}), mustJSON(publicationReplacementCleanupRecord{Version: publicationReplacementCleanupVersion, Kind: "publication_replacement_cleanup", Guard: guard, Witness: witness, Step: "backup_result", Name: witness.BackupRel, Result: "removed"})}
	return TrackPublicationAttempt{OperationID: guard.OperationID, PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, ExpectedGeneration: &oldGeneration, CandidateGeneration: guard.CandidateGeneration, IntentKind: "rewrite", Phase: "committed", FrozenInputs: nil, MutationJournal: mustJSON(journal)}, guard, input, newCurrent, mustJSON(journal), cleanup
}

func retryStageRecord(from, to string, observation PublicationStageObservation) any {
	return struct {
		Version     int                         `json:"version"`
		Kind        string                      `json:"kind"`
		From        string                      `json:"from"`
		To          string                      `json:"to"`
		Observation PublicationStageObservation `json:"observation"`
	}{1, "stage_observation", from, to, observation}
}
