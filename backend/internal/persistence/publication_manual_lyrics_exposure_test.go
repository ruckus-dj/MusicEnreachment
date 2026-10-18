package persistence

import (
	"encoding/json"
	"testing"
)

func TestManualLyricsValidatedPrefixRequiresBothStageEntries(t *testing.T) {
	guard, input, preparations, rebuilds, outcome := manualLyricsFallbackStageFixture(t)
	journal := manualLyricsFallbackJournal(t, preparations, rebuilds, outcome)
	attempt := &TrackPublicationAttempt{}

	for _, length := range []int{4, 5, 6, 7, 8} {
		t.Run(string(rune('0'+length)), func(t *testing.T) {
			entries := append([]json.RawMessage(nil), journal[:length]...)
			got := validManualLyricsValidatedStagePrefix(entries, guard, attempt, input)
			if got != (length == 8) {
				t.Fatalf("validated prefix length %d accepted = %v", length, got)
			}
		})
	}
}

func TestManualLyricsExposureReplayRequiresCanonicalValidatedPrefix(t *testing.T) {
	guard, input, preparations, rebuilds, outcome := manualLyricsFallbackStageFixture(t)
	entries := manualLyricsFallbackJournal(t, preparations, rebuilds, outcome)
	if len(entries) != 8 || !validManualLyricsValidatedStagePrefix(entries, guard, &TrackPublicationAttempt{}, input) {
		t.Fatal("replay fixture must contain the canonical eight-entry validated fallback prefix")
	}

	ready := preparations[2]
	intent := PublicationExposureIntent{
		Version: PublicationExposureVersion, StagedPath: ready.OutputRel,
		TargetCanonicalKey: input.Rewrite.RelativePath, TargetRelativePath: input.Rewrite.RelativePath,
		Expectation: "expected_current", PrepOwner: ready.OwnerToken,
		OutputRootParent: ready.ParentIdentity, TargetParent: ready.ParentIdentity,
	}
	currentSnapshot := json.RawMessage(`{"generation":"current"}`)
	expected := input.Rewrite.CurrentGeneration
	attempt := &TrackPublicationAttempt{
		PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, CandidateGeneration: guard.CandidateGeneration,
		ExpectedGeneration: &expected, MutationJournal: marshalManualLyricsStageTestValue(t, entries),
	}
	record := publicationExposureJournalRecord{
		Version: PublicationExposureVersion, Kind: "publication_exposure", From: "validated", To: "exposing",
		Guard: guard, PositionID: attempt.PositionID, Epoch: attempt.Epoch,
		CandidateGeneration: attempt.CandidateGeneration, ExpectedGeneration: &expected,
		Intent: intent, Current: currentSnapshot, Observation: outcome.FinalObservation,
	}
	withExposure, err := appendPublicationExposureJournal(attempt.MutationJournal, record)
	if err != nil {
		t.Fatal(err)
	}
	attempt.MutationJournal = withExposure

	if got, ok := manualLyricsExposureReplay(attempt, guard, intent, outcome.FinalObservation, input, currentSnapshot); !ok || got != PublicationExposureAlreadyRecorded {
		t.Fatalf("canonical 8-entry prefix replay = (%q, %v), want already recorded", got, ok)
	}

	decoded, err := decodeJournal(withExposure)
	if err != nil || len(decoded) != 9 {
		t.Fatalf("full replay journal length = %d, error = %v; want 9 entries", len(decoded), err)
	}
	for _, length := range []int{7, 8} {
		truncated, marshalErr := json.Marshal(decoded[:length])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		attempt.MutationJournal = truncated
		if got, ok := manualLyricsExposureReplay(attempt, guard, intent, outcome.FinalObservation, input, currentSnapshot); ok || got != PublicationExposureStale {
			t.Fatalf("truncated replay length %d = (%q, %v), want stale", length, got, ok)
		}
	}
}
