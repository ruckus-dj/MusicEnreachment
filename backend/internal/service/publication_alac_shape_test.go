package service

import (
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestALACProofPreservesPrivateSourceShape(t *testing.T) {
	number := func(v int64) *int64 { return &v }
	text := func(v string) *string { return &v }
	source := SourceTechnicalAudioStream{
		Index: number(2), CodecName: text("flac"), Channels: number(2),
		SampleRateHz: number(44100), BitsPerSample: number(24), ChannelLayout: text("stereo"),
	}
	// Optional frozen rate/layout do not replace the actual private-source proof.
	if err := validateALACSourceShape(source, persistence.PersistedAudioFacts{Codec: "flac"}); err != nil {
		t.Fatalf("valid private source: %v", err)
	}
	if err := compareALACShapes(source, source); err != nil {
		t.Fatalf("unchanged audio shape: %v", err)
	}
	channels, rate, depth := 2, 44100, 24
	frozen := persistence.PersistedAudioFacts{Codec: "flac", ChannelLayout: "stereo", Channels: &channels, SampleRate: &rate, BitDepth: &depth}
	for _, scenario := range []struct {
		name   string
		change func(*SourceTechnicalAudioStream)
	}{
		{"relabelled channels", func(s *SourceTechnicalAudioStream) { s.ChannelLayout = text("downmix") }},
		{"channel count", func(s *SourceTechnicalAudioStream) { s.Channels = number(1) }},
		{"sample rate", func(s *SourceTechnicalAudioStream) { s.SampleRateHz = number(48000) }},
		{"sample depth", func(s *SourceTechnicalAudioStream) { s.BitsPerSample = number(16) }},
		{"unknown layout", func(s *SourceTechnicalAudioStream) { s.ChannelLayout = nil }},
		{"unknown rate", func(s *SourceTechnicalAudioStream) { s.SampleRateHz = nil }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			candidate := source
			scenario.change(&candidate)
			if err := compareALACShapes(source, candidate); err == nil {
				t.Fatal("changed candidate shape was accepted")
			}
			if err := validateALACSourceShape(candidate, frozen); err == nil {
				t.Fatal("missing or conflicting private-source shape was accepted")
			}
		})
	}
}
