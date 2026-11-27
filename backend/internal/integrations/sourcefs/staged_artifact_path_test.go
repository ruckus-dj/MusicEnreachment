package sourcefs

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestStagedArtifactPathPreservesPortableSourceExtension(t *testing.T) {
	root, work, artifact := uuid.New(), uuid.New(), uuid.New()
	stem := "analysis/staging/" + root.String() + "/" + work.String() + "/" + artifact.String()
	for _, tc := range []struct {
		name   string
		source string
		suffix string
	}{
		{"mp3", "album/track.mp3", ".mp3"},
		{"uppercase", "album.v1/live.mix.FLAC", ".FLAC"},
		{"unicode", "album/track.аудио", ".аудио"},
		{"no extension", "album.v1/track", ""},
		{"trailing dot", "album/track.", ""},
		{"trailing space", "album/track.mp3 ", ""},
		{"colon", "album/track.mp3:stream", ""},
		{"control character", "album/track.a\u0085b", ""},
		{"null byte", "album/track.a\x00b", ""},
		{"maximum leaf size", "album/track." + strings.Repeat("a", MaxStagedArtifactExtensionBytes), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := StagedArtifactPath(root, work, artifact, tc.source)
			if want := stem + tc.suffix; got != want {
				t.Errorf("StagedArtifactPath(%q) = %q, want %q", tc.source, got, want)
			}
			if !validRegisteredArtifactPath(got) || !MatchesStagedArtifactPath(stem, root, work, artifact, tc.source) {
				t.Errorf("registered or legacy path rejected for %q", tc.source)
			}
		})
	}
	if MatchesStagedArtifactPath(stem+".wav", root, work, artifact, "track.mp3") {
		t.Error("accepted a suffix different from the source extension")
	}
}
