package persistence

import "testing"

func TestPublicationInputsCloneDetachesManualLyricsTags(t *testing.T) {
	input := PublicationInputs{DesiredTags: map[string][]string{"TITLE": {"original"}}}
	clone := input.Clone()
	clone.DesiredTags["TITLE"][0] = "mutated"
	if input.DesiredTags["TITLE"][0] != "original" {
		t.Fatalf("clone mutation changed original tags: %v", input.DesiredTags)
	}
}
