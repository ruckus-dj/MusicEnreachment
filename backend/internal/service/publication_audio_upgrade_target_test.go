package service

import (
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestAudioUpgradeTargetRelativePathPreservesCurrentBasenameAndDirectory(t *testing.T) {
	lossless := true
	facts := publication.AudioSourceFacts{
		StreamIndex: 0, Codec: "flac", Lossless: &lossless,
		Container: publication.AudioContainerFLAC, SupportedSourceContainer: true,
	}

	target, err := audioUpgradeTargetRelativePath("library/album/track.old", publication.AudioFormatMKA, facts)
	if err != nil {
		t.Fatal(err)
	}
	if target != "library/album/track.mka" {
		t.Fatalf("target = %q, want %q", target, "library/album/track.mka")
	}
}

func TestAudioUpgradeTargetRelativePathMayEqualCurrentForSourceFormat(t *testing.T) {
	lossless := true
	facts := publication.AudioSourceFacts{
		StreamIndex: 0, Codec: "flac", Lossless: &lossless,
		Container: publication.AudioContainerFLAC, SupportedSourceContainer: true,
	}

	target, err := audioUpgradeTargetRelativePath("library/track.flac", publication.AudioFormatSource, facts)
	if err != nil {
		t.Fatal(err)
	}
	if target != "library/track.flac" {
		t.Fatalf("target = %q, want current path %q", target, "library/track.flac")
	}
}
