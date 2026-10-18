package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestManualLyricsCommitReceiptBindsFrozenIntent(t *testing.T) {
	for _, state := range []string{"value", "explicit_empty"} {
		for _, outcome := range []string{PublicationOptionalLyricsExactRoundTrip, PublicationOptionalLyricsRebuiltWithoutLyrics} {
			t.Run(state+"/"+outcome, func(t *testing.T) {
				oldGeneration, newGeneration := uuid.New(), uuid.New()
				attempt := &TrackPublicationAttempt{PositionID: uuid.New(), Epoch: uuid.New(), ExpectedGeneration: &oldGeneration, CandidateGeneration: newGeneration}
				manual := &PublicationManualLyricsInput{ExpectedMetadataRevision: 17, ConfirmedCurrentGeneration: oldGeneration, State: state}
				if state == "value" {
					manual.Value = "[00:01.00]Привет\r\n"
				}
				input := PublicationInputs{ManualLyrics: manual, Rewrite: &PublicationRewriteInput{}}
				now := time.Date(2026, 10, 18, 12, 30, 0, 0, time.UTC)
				prefix := canonicalManualLyricsCommitPrefix{Outcome: publicationOptionalLyricsOutcomeJournalRecord{Outcome: outcome, FinalObservation: PublicationStageObservation{ActualAudio: json.RawMessage(`{}`)}}, Commit: replacementPublicationCommitWitness{Projection: TrackPublication{ValidatedAt: now}}}
				receipt := &manualLyricsCommitReceipt{PositionID: attempt.PositionID, Epoch: attempt.Epoch, DesiredRevision: 17, ConfirmedGeneration: oldGeneration, PubGeneration: newGeneration, DesiredState: state, RecordedAt: now}
				if state == "value" {
					digest := sha256.Sum256([]byte(manual.Value))
					hash := hex.EncodeToString(digest[:])
					receipt.DesiredSHA256 = &hash
				}
				if outcome == PublicationOptionalLyricsRebuiltWithoutLyrics {
					warning := manualLyricsReadbackMismatchWarning
					receipt.WarningCode = &warning
				}
				if !manualLyricsCommitReceiptMatches(receipt, prefix, attempt, input) {
					t.Fatal("receipt rejected the frozen intent mapping")
				}
				receipt.ConfirmedGeneration = newGeneration
				if manualLyricsCommitReceiptMatches(receipt, prefix, attempt, input) {
					t.Fatal("current G1 substituted for confirmed G0")
				}
				receipt.ConfirmedGeneration = oldGeneration
				receipt.DesiredRevision++
				if manualLyricsCommitReceiptMatches(receipt, prefix, attempt, input) {
					t.Fatal("receipt relabelled to a newer desired revision")
				}
			})
		}
	}
}

func TestReplacementPublicationCommitManualLyricsReceiptJSON(t *testing.T) {
	t.Run("version one omits nil receipt", func(t *testing.T) {
		witness := replacementPublicationCommitWitness{Version: replacementPublicationCommitVersion}
		encoded, err := json.Marshal(witness)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "manual_lyrics_receipt") {
			t.Fatalf("version one witness unexpectedly contains receipt: %s", encoded)
		}
	})

	t.Run("version two receipt round trips", func(t *testing.T) {
		desiredSHA256 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		warningCode := manualLyricsReadbackMismatchWarning
		recordedAt := time.Date(2026, time.October, 18, 12, 30, 0, 0, time.UTC)
		witness := replacementPublicationCommitWitness{
			Version: replacementManualLyricsCommitVersion,
			ManualLyricsReceipt: &manualLyricsCommitReceipt{
				PositionID:          uuid.MustParse("11111111-1111-4111-8111-111111111111"),
				Epoch:               uuid.MustParse("22222222-2222-4222-8222-222222222222"),
				DesiredRevision:     42,
				ConfirmedGeneration: uuid.MustParse("33333333-3333-4333-8333-333333333333"),
				PubGeneration:       uuid.MustParse("44444444-4444-4444-8444-444444444444"),
				DesiredState:        string(ManualLyricsValue),
				DesiredSHA256:       &desiredSHA256,
				WarningCode:         &warningCode,
				RecordedAt:          recordedAt,
			},
		}
		encoded, err := json.Marshal(witness)
		if err != nil {
			t.Fatal(err)
		}
		var decoded replacementPublicationCommitWitness
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Version != replacementManualLyricsCommitVersion || decoded.ManualLyricsReceipt == nil {
			t.Fatalf("receipt missing after round trip: %+v", decoded)
		}
		if !samePublicationExposureJSON(decoded.ManualLyricsReceipt, witness.ManualLyricsReceipt) {
			t.Fatalf("receipt changed during round trip: got %+v, want %+v", *decoded.ManualLyricsReceipt, *witness.ManualLyricsReceipt)
		}
	})
}
