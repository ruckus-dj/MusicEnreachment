package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestValidManualLyricsStageJournalAcceptsCanonicalFallbackPrefix(t *testing.T) {
	guard, input, preparation, rebuilds, outcome := manualLyricsFallbackStageFixture(t)
	journal := manualLyricsFallbackJournal(t, preparation, rebuilds, outcome)
	attempt := &TrackPublicationAttempt{}

	for _, length := range []int{6, 7, 8} {
		t.Run(string(rune('0'+length)), func(t *testing.T) {
			entries := append([]json.RawMessage(nil), journal[:length]...)
			raw := marshalManualLyricsStageTestValue(t, entries)
			if !validManualLyricsStageJournal(raw, guard, attempt, input) {
				t.Fatalf("valid fallback stage journal with %d entries was rejected", length)
			}
		})
	}

	if len(journal) != 8 {
		t.Fatalf("fixture journal length = %d, want 8", len(journal))
	}
	if !manualLyricsStageRecordMatches(journal[6], "preparing", "staged", outcome.FinalObservation) ||
		!manualLyricsStageRecordMatches(journal[7], "staged", "validated", outcome.FinalObservation) {
		t.Fatal("valid fallback journal did not have the required exact two-entry stage suffix")
	}
	baseEvidence := PublicationOptionalLyricsOutcomeEvidence{
		Kind: outcome.Outcome, ManualLyrics: outcome.ManualLyrics, Preparation: outcome.Preparation,
		InputIdentity: outcome.InputIdentity, OutputIdentity: outcome.OutputIdentity,
		FinalObservation: outcome.FinalObservation, Diagnostic: outcome.Diagnostic,
	}
	if !validOptionalLyricsOutcomeRecord(outcome, guard, input) {
		t.Fatal("fallback outcome record is invalid before stage-journal validation")
	}
	if !canonicalOptionalLyricsPreparation(preparation, baseEvidence, guard) {
		t.Fatal("fallback preparation chain is not canonical before stage-journal validation")
	}

	tests := []struct {
		name   string
		mutate func([]json.RawMessage)
	}{
		{name: "modified preparation", mutate: func(entries []json.RawMessage) {
			var ready PublicationPreparationRecord
			if err := json.Unmarshal(entries[2], &ready); err != nil {
				t.Fatal(err)
			}
			ready.OwnerToken = uuid.New()
			entries[2] = marshalManualLyricsStageTestValue(t, ready)
		}},
		{name: "manual revision", mutate: func(entries []json.RawMessage) {
			var record publicationOptionalLyricsOutcomeJournalRecord
			if err := json.Unmarshal(entries[5], &record); err != nil {
				t.Fatal(err)
			}
			record.ManualLyrics.ExpectedMetadataRevision++
			entries[5] = marshalManualLyricsStageTestValue(t, record)
		}},
		{name: "frozen generation", mutate: func(entries []json.RawMessage) {
			var record publicationOptionalLyricsOutcomeJournalRecord
			if err := json.Unmarshal(entries[5], &record); err != nil {
				t.Fatal(err)
			}
			record.ManualLyrics.ConfirmedCurrentGeneration = uuid.New()
			entries[5] = marshalManualLyricsStageTestValue(t, record)
		}},
		{name: "wrong owner preparation", mutate: func(entries []json.RawMessage) {
			var ready PublicationPreparationRecord
			if err := json.Unmarshal(entries[2], &ready); err != nil {
				t.Fatal(err)
			}
			ready.OwnerToken = uuid.New()
			entries[5] = marshalManualLyricsStageTestValue(t, func() publicationOptionalLyricsOutcomeJournalRecord {
				var record publicationOptionalLyricsOutcomeJournalRecord
				if err := json.Unmarshal(entries[5], &record); err != nil {
					t.Fatal(err)
				}
				record.Preparation = ready
				return record
			}())
		}},
		{name: "null preparation", mutate: func(entries []json.RawMessage) { entries[1] = json.RawMessage(`null`) }},
		{name: "trailing record", mutate: func(entries []json.RawMessage) { entries[7] = json.RawMessage(`{}`) }},
		{name: "duplicate ordering", mutate: func(entries []json.RawMessage) { entries[3], entries[4] = entries[4], entries[3] }},
	}
	for _, test := range tests {
		t.Run("reject "+test.name, func(t *testing.T) {
			entries := append([]json.RawMessage(nil), journal...)
			test.mutate(entries)
			if validManualLyricsStageJournal(marshalManualLyricsStageTestValue(t, entries), guard, attempt, input) {
				t.Fatal("invalid fallback stage journal was accepted")
			}
		})
	}
}

func manualLyricsFallbackStageFixture(t *testing.T) (PublicationDeliveryGuard, PublicationInputs, []PublicationPreparationRecord, []PublicationCandidateRebuildRecord, publicationOptionalLyricsOutcomeJournalRecord) {
	t.Helper()
	rebuilds, ready, evidence, guard := optionalLyricsRebuildFixture()
	input := publicationInputsFixture(t, "rewrite")
	input.Version = PublicationInputsVersion
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	input.Format = publication.AudioFormatSource
	input.Rewrite.RelativePath = "Artist/Album/track.flac"
	input.ManualLyrics = &PublicationManualLyricsInput{
		State: "value", Value: "[ar:Artist]\n[00:01.00]Lyrics\n",
		ExpectedMetadataRevision: 4, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration,
	}
	hash := evidence.InputIdentity.SHA256
	input.Rewrite.Ownership.SHA256 = hash
	*input.Rewrite.Ownership.SizeBytes = *evidence.InputIdentity.SizeBytes
	mtime := *ready.InputMTime
	input.Rewrite.Ownership.MTime = &mtime
	evidence.Preparation = ready
	evidence.Kind = PublicationOptionalLyricsRebuiltWithoutLyrics
	evidence.Diagnostic = "optional lyrics did not round-trip; rebuilt output without lyrics"
	evidence.ManualLyrics = *input.ManualLyrics
	evidence.InputIdentity = clonePublicationFileIdentity(evidence.InputIdentity)
	evidence.OutputIdentity = PublicationFileIdentity{SizeBytes: manualLyricsStageInt64Pointer(99), MTime: manualLyricsStageInt64Pointer(300), SHA256: hash}
	evidence.FinalObservation = PublicationStageObservation{
		Version: 1, RelativePath: ready.OutputRel,
		Ownership: ArtifactOwnership{ExpectedSize: evidence.OutputIdentity.SizeBytes, ExpectedMTime: evidence.OutputIdentity.MTime}, PayloadSHA256: hash,
		ActualAudio:          marshalManualLyricsStageTestValue(t, PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts}),
		ActualMetadata:       marshalManualLyricsStageTestValue(t, PublicationCommitMetadata{Version: 1, Tags: input.DesiredTags}),
		ActualAssets:         marshalManualLyricsStageTestValue(t, PublicationAssetOutcomes{Version: 1, Cover: PublicationCoverOutcome{State: PublicationAssetAbsent}, Lyrics: PublicationLyricsOutcome{State: PublicationAssetAbsent}}),
		QualityPolicyVersion: input.QualityPolicyVersion,
	}
	if !validOptionalLyricsOutcomeEvidence(evidence, guard, input) {
		t.Fatal("fixture optional-lyrics outcome is invalid")
	}
	record := publicationOptionalLyricsOutcomeJournalRecord{Version: PublicationOptionalLyricsOutcomeVersion, Kind: "publication_optional_lyrics_outcome", Outcome: evidence.Kind, ManualLyrics: evidence.ManualLyrics, Preparation: ready, InputIdentity: evidence.InputIdentity, OutputIdentity: evidence.OutputIdentity, FinalObservation: evidence.FinalObservation, Diagnostic: evidence.Diagnostic}
	preparations := []PublicationPreparationRecord{ready, ready, ready}
	preparations[0].Step, preparations[0].DirectoryIdentity, preparations[0].InputSize, preparations[0].InputMTime, preparations[0].CopiedInput = "intent", nil, nil, nil, nil
	preparations[1].Step, preparations[1].InputSize, preparations[1].InputMTime, preparations[1].CopiedInput = "directory_created", nil, nil, nil
	baseEvidence := PublicationOptionalLyricsOutcomeEvidence{
		Kind: record.Outcome, ManualLyrics: record.ManualLyrics, Preparation: record.Preparation,
		InputIdentity: record.InputIdentity, OutputIdentity: record.OutputIdentity,
		FinalObservation: record.FinalObservation, Diagnostic: record.Diagnostic,
	}
	if !validOptionalLyricsOutcomeRecord(record, guard, input) {
		t.Fatal("fallback outcome fixture is invalid")
	}
	if !canonicalOptionalLyricsPreparation(preparations, baseEvidence, guard) {
		t.Fatal("fallback preparation fixture is not a canonical chain")
	}
	return guard, input, preparations, rebuilds, record
}

func manualLyricsFallbackJournal(t *testing.T, preparations []PublicationPreparationRecord, rebuilds []PublicationCandidateRebuildRecord, outcome publicationOptionalLyricsOutcomeJournalRecord) []json.RawMessage {
	t.Helper()
	entries := make([]json.RawMessage, 0, 8)
	for _, record := range preparations {
		entries = append(entries, marshalManualLyricsStageTestValue(t, record))
	}
	for _, record := range rebuilds {
		entries = append(entries, marshalManualLyricsStageTestValue(t, record))
	}
	entries = append(entries, marshalManualLyricsStageTestValue(t, outcome))
	for _, pair := range [][2]string{{"preparing", "staged"}, {"staged", "validated"}} {
		var err error
		entriesRaw, err := appendPublicationStageJournal(marshalManualLyricsStageTestValue(t, entries), pair[0], pair[1], outcome.FinalObservation)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(entriesRaw, &entries); err != nil {
			t.Fatal(err)
		}
	}
	return entries
}

func marshalManualLyricsStageTestValue(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func manualLyricsStageInt64Pointer(value int64) *int64 { return &value }
