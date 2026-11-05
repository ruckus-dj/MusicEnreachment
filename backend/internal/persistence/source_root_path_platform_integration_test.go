//go:build integration

package persistence_test

import (
	"context"
)

// TestSourceRootConfiguredPathPlatformsWithPostgreSQL pins the two path
// boundaries the platform fix separates: the database stores any platform
// absolute path after the relaxation, while the service still refuses a
// relative path before it reaches the repository. It proves the strings, not the
// runtime filesystem: "C:\\Music" is a valid Windows path that cannot exist on
// this Unix runner.

// emptyManagedPaths is the empty managed-paths reader the service needs to reach
// its own relative-path rejection without an installed tools or output root.
type emptyManagedPaths struct{}

func (emptyManagedPaths) GetToolsDirectory(context.Context) (string, bool, error) {
	return "", false, nil
}
func (emptyManagedPaths) GetOutputDirectory(context.Context) (string, bool, error) {
	return "", false, nil
}
