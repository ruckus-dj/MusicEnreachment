package persistence

import (
	"testing"

	"github.com/google/uuid"
)

func TestPublicationAnalysedMetadataCloneDetachesValues(t *testing.T) {
	duration := 183.456789
	input := publicationInputsFixture(t, "audio")
	input.Audio.AnalysedMetadata = &PublicationAnalysedMetadata{
		Tags:            map[string][]string{"ARTIST": {"Original"}, "EMPTY": {}},
		DurationSeconds: &duration, MetadataResultID: uuid.New(), WinningResultID: uuid.New(),
	}
	clone := input.Clone()
	clone.Audio.AnalysedMetadata.Tags["ARTIST"][0] = "Changed"
	clone.Audio.AnalysedMetadata.Tags["EMPTY"] = append(clone.Audio.AnalysedMetadata.Tags["EMPTY"], "Changed")
	*clone.Audio.AnalysedMetadata.DurationSeconds = 1
	if input.Audio.AnalysedMetadata.Tags["ARTIST"][0] != "Original" || len(input.Audio.AnalysedMetadata.Tags["EMPTY"]) != 0 || *input.Audio.AnalysedMetadata.DurationSeconds != duration {
		t.Fatalf("clone mutation leaked into original: %#v", input.Audio.AnalysedMetadata)
	}
}
