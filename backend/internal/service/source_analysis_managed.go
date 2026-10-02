package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// sourceAnalysisFFProbeName is the managed executable an analysis probes with,
// matched without the platform suffix a managed package adds.
const sourceAnalysisFFProbeName = "ffprobe"

// resolveManagedFFProbe resolves the installation the snapshot pins to the
// absolute ffprobe executable and the version that executable actually reports.
// The installation must be a ready FFmpeg package of this platform with the
// managed path its release identity implies, the managed tools directory must be
// configured, and the fresh read-only version query must succeed. The release
// identity from the catalog is never used as the version: the fresh query is.
func (s *SourceAnalysis) resolveManagedFFProbe(ctx context.Context, snapshot persistence.SourceAnalysisSnapshot) (string, string, error) {
	installation, err := s.repository.GetInstallation(ctx, snapshot.AnalysisInstallationID)
	if err != nil {
		return "", "", fmt.Errorf("analyze source location: read the selected installation: %w: %w", ErrSourceAnalysisToolUnavailable, err)
	}
	if installation.PackageKind != string(tools.PackageFFmpeg) || installation.State != "ready" ||
		installation.PlatformGOOS != s.platform.GOOS || installation.PlatformGOARCH != s.platform.GOARCH {
		return "", "", fmt.Errorf("analyze source location: the selected installation is not a ready ffmpeg package for this platform: %w", ErrSourceAnalysisToolUnavailable)
	}
	relative, err := tools.ManagedRelativePath(tools.PackageFFmpeg, installation.ReleaseIdentity)
	if err != nil || filepath.Clean(installation.RelativePath) != relative {
		return "", "", fmt.Errorf("analyze source location: the selected installation has an invalid managed path: %w", ErrSourceAnalysisToolUnavailable)
	}
	root, exists, err := s.toolsDirectory.GetToolsDirectory(ctx)
	if err != nil {
		return "", "", fmt.Errorf("analyze source location: read the managed tools directory: %w", err)
	}
	if !exists || root == "" {
		return "", "", fmt.Errorf("analyze source location: the managed tools directory is unavailable: %w", ErrSourceAnalysisToolUnavailable)
	}
	executable, err := sourceAnalysisFFProbeExecutable(root, relative, s.platform.GOOS)
	if err != nil {
		return "", "", fmt.Errorf("analyze source location: %w: %w", ErrSourceAnalysisToolUnavailable, err)
	}
	// The version query is run again, read-only, so the recorded version is the
	// one this executable reports now and a tool removed or broken after
	// materialization is refused before any file is probed. It is never the
	// cached catalog version.
	versions, err := s.verifier.VerifyInstallation(ctx, root, relative, tools.PackageFFmpeg, installation.ReleaseIdentity, s.platform.GOOS)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", "", fmt.Errorf("analyze source location: %w", ctxErr)
		}
		return "", "", fmt.Errorf("analyze source location: verify the selected installation: %w: %w", ErrSourceAnalysisToolUnavailable, err)
	}
	version, ok := sourceAnalysisFFProbeVersion(versions, s.platform.GOOS)
	if !ok || version == "" {
		return "", "", fmt.Errorf("analyze source location: the managed ffprobe reported no version: %w", ErrSourceAnalysisToolUnavailable)
	}
	return executable, version, nil
}

// sourceAnalysisFFProbeExecutable names the ffprobe of a managed FFmpeg package,
// including the executable suffix the platform adds.
func sourceAnalysisFFProbeExecutable(root, relative, goos string) (string, error) {
	for _, name := range tools.ExpectedExecutables(tools.PackageFFmpeg, goos) {
		if strings.TrimSuffix(name, filepath.Ext(name)) == sourceAnalysisFFProbeName {
			return filepath.Join(root, relative, name), nil
		}
	}
	return "", fmt.Errorf("a managed ffmpeg package has no %s executable", sourceAnalysisFFProbeName)
}

// sourceAnalysisFFProbeVersion reads the reported version of the managed ffprobe
// out of the verification result, keyed by the executable name the platform adds.
func sourceAnalysisFFProbeVersion(versions map[string]string, goos string) (string, bool) {
	for _, name := range tools.ExpectedExecutables(tools.PackageFFmpeg, goos) {
		if strings.TrimSuffix(name, filepath.Ext(name)) == sourceAnalysisFFProbeName {
			version, ok := versions[name]
			return version, ok
		}
	}
	return "", false
}
