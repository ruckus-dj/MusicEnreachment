package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestAutomaticAudioUpgradeSourceChoiceIncludesUnselectedLinkedSources(t *testing.T) {
	lossless := true
	channels := 2
	oldDepth, oldRate := 16, 44100
	newDepth, newRate := 24, 48000
	locationID, variantID := uuid.New(), uuid.New()
	better := persistence.FirstPublicationSource{
		Selection:        persistence.PublicationSourceSelection{LocationID: locationID, MediaVariantID: variantID},
		Selected:         false,
		ChannelLayout:    "stereo",
		AudioStreamCount: 1,
		Quality: persistence.PersistedAudioFacts{
			StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels,
			ChannelLayout: "stereo", BitDepth: &newDepth, SampleRate: &newRate,
			Container: publication.AudioContainerFLAC, SupportedSourceContainer: true,
		},
	}
	snapshot := &persistence.LibraryFirstPublicationContext{
		DefaultSourceBlockers: []string{"the prior selected source root is unavailable"},
		Sources:               []persistence.FirstPublicationSource{better},
	}
	current := persistence.PersistedAudioFacts{
		StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels,
		ChannelLayout: "stereo", BitDepth: &oldDepth, SampleRate: &oldRate,
		Container: publication.AudioContainerFLAC, SupportedSourceContainer: true,
	}
	if _, err := publication.PlanAudioTransform(publication.AudioFormatSource, better.Quality.AudioSourceFacts()); err != nil {
		t.Fatalf("invalid linked candidate fixture: %v", err)
	}
	if comparison := publication.CompareQuality(audioUpgradeQualityCandidate(better.Quality), audioUpgradeQualityCandidate(current)); comparison != publication.QualityBetter {
		t.Fatalf("fixture candidate comparison = %v, want strictly better", comparison)
	}
	choice, err := automaticAudioUpgradeSourceChoice(snapshot, current, publication.AudioFormatSource)
	if err != nil {
		t.Fatalf("choose unselected linked upgrade source: %v", err)
	}
	if choice.LocationID != locationID || choice.MediaVariantID != variantID || choice.StreamIndex != 0 {
		t.Fatalf("choice = %+v, want unselected linked source", choice)
	}
}
