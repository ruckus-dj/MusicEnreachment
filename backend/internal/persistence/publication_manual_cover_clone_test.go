package persistence

import "testing"

func TestPublicationInputsCloneDetachesCoverAuthority(t *testing.T) {
	input := PublicationInputs{ManualCover: &PublicationManualCoverInput{AuthorityMode: "manual", AuthorityRevision: 4}}
	clone := input.Clone()
	clone.ManualCover.AuthorityMode = "automatic"
	clone.ManualCover.AuthorityRevision++
	if input.ManualCover.AuthorityMode != "manual" || input.ManualCover.AuthorityRevision != 4 {
		t.Fatal("cloning publication inputs retained mutable cover authority")
	}
}
