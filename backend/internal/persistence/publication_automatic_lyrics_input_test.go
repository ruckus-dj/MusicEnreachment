package persistence

import (
	"testing"

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
