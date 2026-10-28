package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type publicationPinnedToolsRepository interface {
	PublicationToolsForDelivery(context.Context, persistence.PublicationDeliveryGuard) ([]persistence.SourceAnalysisToolSelection, *persistence.ToolInstallation, string, bool, error)
	CheckPublicationDelivery(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
}

// PublicationPinnedToolsResolver resolves only the frozen ffmpeg/ffprobe pair. The
// runner is mandatory: callers cannot silently fall back to PATH/system tools.
type PublicationPinnedToolsResolver struct {
	repository publicationPinnedToolsRepository
	runner     tools.CommandRunner
	platform   tools.Platform
}

type PublicationToolExecutables struct {
	FFmpeg  string
	FFprobe string
}

// GuardedPublicationPinnedTools adapts the pair resolver to the staging
// interface, retaining the delivery guard for every lookup.
type GuardedPublicationPinnedTools struct {
	resolver *PublicationPinnedToolsResolver
	guard    persistence.PublicationDeliveryGuard
}

var _ PublicationPinnedTools = (*GuardedPublicationPinnedTools)(nil)

func NewPublicationPinnedToolsResolver(repository publicationPinnedToolsRepository, runner tools.CommandRunner, platform tools.Platform) *PublicationPinnedToolsResolver {
	return &PublicationPinnedToolsResolver{repository: repository, runner: runner, platform: platform}
}

func NewGuardedPublicationPinnedTools(resolver *PublicationPinnedToolsResolver, guard persistence.PublicationDeliveryGuard) *GuardedPublicationPinnedTools {
	if resolver == nil {
		return nil
	}
	return &GuardedPublicationPinnedTools{resolver: resolver, guard: guard}
}

func (adapter *GuardedPublicationPinnedTools) Executable(ctx context.Context, selections []persistence.SourceAnalysisToolSelection, name string) (string, error) {
	if adapter == nil || adapter.resolver == nil {
		return "", fmt.Errorf("publication pinned tools resolver is unavailable")
	}
	pair, valid, err := adapter.resolver.Resolve(ctx, adapter.guard, selections)
	if err != nil {
		return "", err
	}
	if !valid {
		return "", fmt.Errorf("publication pinned tools are unavailable")
	}
	switch name {
	case "ffmpeg":
		return pair.FFmpeg, nil
	case "ffprobe":
		return pair.FFprobe, nil
	default:
		return "", fmt.Errorf("unsupported publication executable %q", name)
	}
}

func (resolver *PublicationPinnedToolsResolver) Resolve(ctx context.Context, guard persistence.PublicationDeliveryGuard, supplied []persistence.SourceAnalysisToolSelection) (PublicationToolExecutables, bool, error) {
	if resolver == nil || resolver.repository == nil || resolver.runner == nil {
		return PublicationToolExecutables{}, false, fmt.Errorf("publication pinned tools require repository and command runner")
	}
	frozen, installation, root, valid, err := resolver.repository.PublicationToolsForDelivery(ctx, guard)
	if err != nil || !valid {
		return PublicationToolExecutables{}, false, err
	}
	if !samePublicationToolSelections(frozen, supplied) {
		return PublicationToolExecutables{}, false, fmt.Errorf("publication tools differ from frozen selections")
	}
	return resolver.resolveHeldSnapshot(ctx, frozen, installation, root, func(ctx context.Context) (bool, error) {
		return resolver.repository.CheckPublicationDelivery(ctx, guard)
	})
}

// resolveHeldSnapshot shares native executable verification between publication
// deliveries and reconciliation producers. The caller must already own durable
// tools-read authority and supply its independent final freshness check.
func (resolver *PublicationPinnedToolsResolver) resolveHeldSnapshot(ctx context.Context, frozen []persistence.SourceAnalysisToolSelection, installation *persistence.ToolInstallation, root string, checkAlive func(context.Context) (bool, error)) (PublicationToolExecutables, bool, error) {
	if resolver == nil || resolver.runner == nil || checkAlive == nil {
		return PublicationToolExecutables{}, false, fmt.Errorf("held publication tools require command runner and authority check")
	}
	if !resolver.platform.Supported() || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return PublicationToolExecutables{}, false, fmt.Errorf("publication tools root or platform is invalid")
	}
	if len(frozen) != 2 {
		return PublicationToolExecutables{}, false, fmt.Errorf("frozen ffmpeg/ffprobe pair is incomplete")
	}
	if installation == nil || installation.State != "ready" || installation.VerifiedAt == nil || installation.PlatformGOOS != resolver.platform.GOOS || installation.PlatformGOARCH != resolver.platform.GOARCH ||
		installation.PackageKind != "ffmpeg" || !filepath.IsAbs(root) {
		return PublicationToolExecutables{}, false, fmt.Errorf("pinned publication installation is not ready for this platform")
	}
	var versions map[string]string
	if err := json.Unmarshal(installation.ExecutableVerificationOutput, &versions); err != nil {
		return PublicationToolExecutables{}, false, fmt.Errorf("pinned publication installation metadata is invalid: %w", err)
	}
	version := installation.Version
	if version == "" {
		return PublicationToolExecutables{}, false, fmt.Errorf("pinned publication installation canonical version is invalid")
	}
	// Both members must be one package installation; the repository holds its
	// package fence while fetching the selection snapshot.
	for _, selection := range frozen {
		banner, ok := tools.VerifiedExecutableVersion(versions, tools.PackageFFmpeg, selection.Executable, resolver.platform.GOOS)
		if selection.InstallationID != installation.ID || selection.PackageKind != "ffmpeg" || selection.RelativePath != filepath.ToSlash(installation.RelativePath) || selection.Executable != "ffmpeg" && selection.Executable != "ffprobe" || !ok || banner != strings.TrimSpace(selection.VersionBanner) || selection.Version != version {
			return PublicationToolExecutables{}, false, fmt.Errorf("invalid frozen publication executable")
		}
	}
	// Each file's path derives solely from its frozen relative path and executable
	// name. Verify the whole pair before returning either path.
	paths := make(map[string]string, 2)
	identities := make(map[string]os.FileInfo, 2)
	evidence := make(map[string]string, 2)
	var packageIdentity os.FileInfo
	for _, selection := range frozen {
		if selection.InstallationID != frozen[0].InstallationID {
			return PublicationToolExecutables{}, false, fmt.Errorf("frozen publication executables use different installations")
		}
		name := publicationExecutableFileName(selection.Executable, resolver.platform.GOOS)
		if filepath.IsAbs(selection.RelativePath) || filepath.Clean(filepath.FromSlash(selection.RelativePath)) != filepath.FromSlash(selection.RelativePath) || strings.HasPrefix(filepath.Clean(filepath.FromSlash(selection.RelativePath)), ".."+string(filepath.Separator)) {
			return PublicationToolExecutables{}, false, fmt.Errorf("invalid frozen installation path")
		}
		directory := filepath.Join(root, filepath.FromSlash(selection.RelativePath))
		if err := tools.HasSymlinkAncestors(root, directory); err != nil {
			return PublicationToolExecutables{}, false, err
		}
		dirInfo, err := os.Lstat(directory)
		if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
			return PublicationToolExecutables{}, false, fmt.Errorf("managed publication package directory is not a real directory")
		}
		if packageIdentity == nil {
			packageIdentity = dirInfo
		} else if !os.SameFile(packageIdentity, dirInfo) {
			return PublicationToolExecutables{}, false, fmt.Errorf("managed publication package directory changed")
		}
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return PublicationToolExecutables{}, false, fmt.Errorf("managed publication executable is not a regular file")
		}
		paths[selection.Executable], identities[selection.Executable] = path, info
	}
	if paths["ffmpeg"] == "" || paths["ffprobe"] == "" {
		return PublicationToolExecutables{}, false, fmt.Errorf("frozen ffmpeg/ffprobe pair is incomplete")
	}
	for _, selection := range frozen {
		if err := ctx.Err(); err != nil {
			return PublicationToolExecutables{}, false, err
		}
		if !publicationExecutableIdentity(root, selection, resolver.platform.GOOS, packageIdentity, identities[selection.Executable]) {
			return PublicationToolExecutables{}, false, fmt.Errorf("managed publication executable changed before verification")
		}
		output, err := resolver.runner.Run(ctx, paths[selection.Executable], "-version")
		if ctx.Err() != nil {
			return PublicationToolExecutables{}, false, ctx.Err()
		}
		if err != nil {
			return PublicationToolExecutables{}, false, fmt.Errorf("verify managed publication executable: %w", err)
		}
		if strings.TrimSpace(string(output)) != strings.TrimSpace(selection.VersionBanner) {
			return PublicationToolExecutables{}, false, fmt.Errorf("managed publication executable version does not match frozen metadata")
		}
		// Fresh verification evidence uses the same platform-native keys as the
		// stored installation metadata that CanonicalPackageVersion consumes.
		evidence[publicationExecutableFileName(selection.Executable, resolver.platform.GOOS)] = string(output)
		if !publicationExecutableIdentity(root, selection, resolver.platform.GOOS, packageIdentity, identities[selection.Executable]) {
			return PublicationToolExecutables{}, false, fmt.Errorf("managed publication executable changed during verification")
		}
	}
	canonical, err := tools.CanonicalPackageVersion(evidence, tools.PackageFFmpeg, resolver.platform.GOOS)
	if err != nil || canonical != version {
		return PublicationToolExecutables{}, false, fmt.Errorf("managed publication package version does not match frozen metadata")
	}
	stillValid, err := checkAlive(ctx)
	if err != nil || !stillValid {
		return PublicationToolExecutables{}, false, err
	}
	for _, selection := range frozen {
		if !publicationExecutableIdentity(root, selection, resolver.platform.GOOS, packageIdentity, identities[selection.Executable]) {
			return PublicationToolExecutables{}, false, fmt.Errorf("managed publication executable changed before return")
		}
	}
	return PublicationToolExecutables{FFmpeg: paths["ffmpeg"], FFprobe: paths["ffprobe"]}, true, nil
}

func publicationExecutableFileName(executable, goos string) string {
	if goos == "windows" {
		return executable + ".exe"
	}
	return executable
}

func publicationExecutableIdentity(root string, selection persistence.SourceAnalysisToolSelection, goos string, expectedDir, expectedFile os.FileInfo) bool {
	name := publicationExecutableFileName(selection.Executable, goos)
	directory := filepath.Join(root, filepath.FromSlash(selection.RelativePath))
	if tools.HasSymlinkAncestors(root, directory) != nil {
		return false
	}
	dirInfo, err := os.Lstat(directory)
	if err != nil || !dirInfo.IsDir() || !samePublicationFileInfo(dirInfo, expectedDir) {
		return false
	}
	fileInfo, err := os.Lstat(filepath.Join(directory, name))
	return err == nil && fileInfo.Mode().IsRegular() && samePublicationFileInfo(fileInfo, expectedFile)
}

func samePublicationFileInfo(current, expected os.FileInfo) bool {
	return current != nil && expected != nil && os.SameFile(current, expected) &&
		current.Size() == expected.Size() && current.ModTime().Equal(expected.ModTime()) && current.Mode() == expected.Mode()
}

func samePublicationToolSelections(left, right []persistence.SourceAnalysisToolSelection) bool {
	if len(left) != len(right) {
		return false
	}
	byExecutable := make(map[string]persistence.SourceAnalysisToolSelection, len(left))
	for _, item := range left {
		if _, exists := byExecutable[item.Executable]; exists {
			return false
		}
		byExecutable[item.Executable] = item
	}
	for _, item := range right {
		selection, exists := byExecutable[item.Executable]
		if !exists || selection != item {
			return false
		}
		delete(byExecutable, item.Executable)
	}
	return len(byExecutable) == 0
}
