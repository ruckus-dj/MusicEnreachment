package persistence

import (
	"testing"

	"github.com/google/uuid"
)

func TestManualCoverAuthorityUsesSelectedGenerationNotRevision(t *testing.T) {
	selected := uuid.New()
	frozen := PublicationManualCoverInput{ReleaseID: uuid.New(), AuthorityRevision: 2, SelectedGeneration: selected}

	if !manualCoverAuthorityMatches("manual", &selected, frozen) {
		t.Fatal("selected manual cover generation should remain current")
	}
	other := uuid.New()
	if manualCoverAuthorityMatches("manual", &other, frozen) {
		t.Fatal("changed selected generation must be stale")
	}
	if manualCoverAuthorityMatches("automatic", &selected, frozen) {
		t.Fatal("automatic cover mode must be stale")
	}
	if manualCoverAuthorityMatches("manual", nil, frozen) {
		t.Fatal("missing selected generation must be stale")
	}
}

func TestManualCoverAuthorityModeIsFrozen(t *testing.T) {
	selected := uuid.New()
	manual := PublicationManualCoverInput{SelectedGeneration: selected, AuthorityMode: "manual"}
	automatic := PublicationManualCoverInput{SelectedGeneration: selected, AuthorityMode: "automatic"}
	legacy := PublicationManualCoverInput{SelectedGeneration: selected}
	if !manualCoverAuthorityMatches("manual", &selected, legacy) || !manualCoverAuthorityMatches("manual", &selected, manual) {
		t.Fatal("manual and legacy frozen authority should match manual mode")
	}
	if !manualCoverAuthorityMatches("automatic", &selected, automatic) {
		t.Fatal("automatic frozen authority should match automatic mode")
	}
	if manualCoverAuthorityMatches("automatic", &selected, manual) || manualCoverAuthorityMatches("manual", &selected, automatic) {
		t.Fatal("cover retry must reject a stale authority-mode switch")
	}
}

func TestManualLyricsReceiptCarryRequiresCurrentPublishedReceipt(t *testing.T) {
	epoch, previousGeneration, candidateGeneration := uuid.New(), uuid.New(), uuid.New()
	previous := TrackPublication{Epoch: epoch, Generation: previousGeneration}
	receipt := PublicationManualLyricsReceipt{Epoch: epoch, DesiredRevision: 7, PublishedGeneration: previousGeneration}
	if !manualLyricsReceiptCanCarry(receipt, previous, epoch, 7) {
		t.Fatal("matching receipt should be eligible for carry-forward")
	}
	for name, mutate := range map[string]func(*PublicationManualLyricsReceipt, *TrackPublication, *uuid.UUID, *int64){
		"epoch": func(r *PublicationManualLyricsReceipt, _ *TrackPublication, e *uuid.UUID, _ *int64) { *e = uuid.New() },
		"previous generation": func(r *PublicationManualLyricsReceipt, p *TrackPublication, _ *uuid.UUID, _ *int64) {
			p.Generation = candidateGeneration
		},
		"published generation": func(r *PublicationManualLyricsReceipt, _ *TrackPublication, _ *uuid.UUID, _ *int64) {
			r.PublishedGeneration = candidateGeneration
		},
		"desired revision": func(r *PublicationManualLyricsReceipt, _ *TrackPublication, _ *uuid.UUID, rev *int64) { *rev = 8 },
	} {
		t.Run(name, func(t *testing.T) {
			r, p, e, revision := receipt, previous, epoch, int64(7)
			mutate(&r, &p, &e, &revision)
			if manualLyricsReceiptCanCarry(r, p, e, revision) {
				t.Fatal("stale receipt must not carry")
			}
		})
	}
}
