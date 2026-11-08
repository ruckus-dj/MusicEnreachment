package persistence

import (
	"testing"

	"github.com/google/uuid"
)

func TestProviderTagCurrentUsesCompleteFrozenWitness(t *testing.T) {
	input := publicationProviderTagFixture(t)
	witness := input.ProviderTags.Current
	epoch := uuid.New()
	current := TrackPublication{
		PositionID: uuid.New(), Epoch: epoch, Generation: witness.Generation, RelativePath: witness.RelativePath,
		Ownership:      mustPublicationJSON(PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: witness.Ownership}),
		ActualAudio:    mustPublicationJSON(PublicationCurrentAudio{Version: 1, Facts: witness.AudioFacts}),
		ActualMetadata: mustPublicationJSON(PublicationCommitMetadata{Version: 1, Tags: witness.Tags}),
		ActualAssets:   mustPublicationJSON(witness.Assets),
	}
	if !providerTagCurrent(current, input, epoch) {
		t.Fatal("exact frozen CURRENT witness rejected")
	}

	current.ActualMetadata = mustPublicationJSON(PublicationCommitMetadata{
		Version: 1, Tags: map[string][]string{"TITLE": {"changed"}},
	})
	if providerTagCurrent(current, input, epoch) {
		t.Fatal("CURRENT with changed metadata accepted")
	}
}

func TestSourceRepublishCurrentRequiresExactWitnessAndBaselineAuthority(t *testing.T) {
	input := publicationAudioUpgradeFixture(t)
	witness := input.AudioUpgrade.Current
	epoch, positionID := uuid.New(), uuid.New()
	authority := PositionSourceAuthority{
		PositionID: positionID, Mode: "automatic", Revision: uuid.New(), PublicationEpoch: epoch,
		PublicationGeneration: witness.Generation, SourceLocationID: uuid.New(), MediaVariantID: uuid.New(),
	}
	input.Version = PublicationSourceRepublishInputsVersion
	input.SourceAuthorityMode = ""
	input.AudioUpgrade = nil
	input.InitialLyrics = nil
	tags := make(map[string][]string, len(witness.Tags))
	for key, values := range witness.Tags {
		if key != "LYRICS" {
			tags[key] = append([]string(nil), values...)
		}
	}
	input.DesiredTags = tags
	input.SourceRepublish = &PublicationSourceRepublishInput{
		Current: witness, Mode: "ordinary",
		SourceAuthority: PublicationSourceAuthority{
			Present: true, PositionID: authority.PositionID, Mode: authority.Mode, Revision: authority.Revision,
			PublicationEpoch: authority.PublicationEpoch, PublicationGeneration: authority.PublicationGeneration,
			SourceLocationID: authority.SourceLocationID, MediaVariantID: authority.MediaVariantID,
		},
		Association:     PublicationRepublishAssociation{ID: uuid.New(), LocationID: input.Audio.LocationID, MediaVariantID: input.Audio.MediaVariantID},
		Lyrics:          PublicationRepublishLyrics{State: "absent", Origin: "none"},
		LyricsAuthority: PublicationInitialLyricsInput{ExpectedMetadataRevision: 1},
		Cover:           PublicationRepublishCover{ReleaseID: uuid.New(), AuthorityRevision: 1, AuthorityMode: "manual", Derivation: "absent"},
	}
	if !validSourceRepublishPreparation(input) {
		t.Fatal("valid v4 payload with present baseline authority did not pass its guarded-reader eligibility check")
	}
	current := TrackPublication{
		PositionID: positionID, Epoch: epoch, Generation: witness.Generation, RelativePath: witness.RelativePath,
		Ownership:      mustPublicationJSON(PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: witness.Ownership}),
		ActualAudio:    mustPublicationJSON(PublicationCurrentAudio{Version: 1, Facts: witness.AudioFacts}),
		ActualMetadata: mustPublicationJSON(PublicationCommitMetadata{Version: 1, Tags: witness.Tags}),
		ActualAssets:   mustPublicationJSON(witness.Assets),
	}
	if !sourceRepublishCurrent(current, authority, input, epoch) {
		t.Fatal("matching CURRENT and baseline authority were rejected when selected source differs")
	}
	changedAuthority := authority
	changedAuthority.Revision = uuid.New()
	if sourceRepublishCurrent(current, changedAuthority, input, epoch) {
		t.Fatal("changed baseline authority revision was accepted")
	}
	current.ActualMetadata = mustPublicationJSON(PublicationCommitMetadata{
		Version: 1, Tags: map[string][]string{"TITLE": {"changed"}},
	})
	if sourceRepublishCurrent(current, authority, input, epoch) {
		t.Fatal("changed CURRENT metadata was accepted")
	}
}
