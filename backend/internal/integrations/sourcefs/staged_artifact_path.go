package sourcefs

import (
	"path"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const (
	// StagedArtifactExtensionPattern extracts a portable final extension, in
	// its original case. The registry also uses it in PostgreSQL path checks.
	// Explicit control ranges keep Go and PostgreSQL regex semantics aligned.
	StagedArtifactExtensionPattern = `\.[^./\\<>:"|?*\x01-\x1f\x7f-\x9f]*[^./\\<>:"|?*\x01-\x1f\x7f-\x9f ]$`
	// MaxStagedArtifactExtensionBytes leaves room for a UUID in a 255-byte leaf.
	MaxStagedArtifactExtensionBytes = 255 - 36
)

var stagedArtifactExtension = regexp.MustCompile(StagedArtifactExtensionPattern)

func stagedArtifactSuffix(relativeSource string) string {
	suffix := stagedArtifactExtension.FindString(relativeSource)
	if len(suffix) > MaxStagedArtifactExtensionBytes || strings.IndexByte(suffix, 0) >= 0 {
		return ""
	}
	return suffix
}

// StagedArtifactPath is the canonical managed path for a new analysis copy.
func StagedArtifactPath(rootID, workID, artifactID uuid.UUID, relativeSource string) string {
	return path.Join("analysis", "staging", rootID.String(), workID.String(), artifactID.String()+stagedArtifactSuffix(relativeSource))
}

// MatchesStagedArtifactPath accepts the exact current or pre-extension path
// for the registered identity, including legacy files that still need cleanup.
func MatchesStagedArtifactPath(relativePath string, rootID, workID, artifactID uuid.UUID, relativeSource string) bool {
	return relativePath == StagedArtifactPath(rootID, workID, artifactID, relativeSource) ||
		relativePath == StagedArtifactPath(rootID, workID, artifactID, "")
}
