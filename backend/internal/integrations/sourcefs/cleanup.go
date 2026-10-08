package sourcefs

import (
	"context"
	"regexp"
)

// CleanupOutcome describes whether a registered artifact was removed or was
// already absent beneath its managed staging directory.
type CleanupOutcome uint8

const (
	CleanupDeleted CleanupOutcome = iota + 1
	CleanupMissing
)

var registeredArtifactPath = regexp.MustCompile(`^analysis/staging/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Cleaner removes only canonical registered staged artifacts from an existing
// managed output root. It never creates directories or prunes parents.
type Cleaner struct{}

// NewCleaner returns the native registered-artifact cleaner.
func NewCleaner() Cleaner { return Cleaner{} }

// RemoveRegistered removes the registered artifact at canonicalRelativePath.
// Missing artifact or intermediate staging components are reported as
// CleanupMissing; an unavailable output root remains an error.
func (Cleaner) RemoveRegistered(ctx context.Context, outputRoot, canonicalRelativePath string) (CleanupOutcome, error) {
	if !registeredArtifactPath.MatchString(canonicalRelativePath) {
		return 0, ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return removeRegistered(ctx, outputRoot, canonicalRelativePath)
}
