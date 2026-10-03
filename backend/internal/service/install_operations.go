package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type InstallationOperationEnqueuer interface {
	CreateInstallationOperationAndEnqueue(context.Context, string, *persistence.ToolInstallation, *persistence.Operation, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error
	ListInstallations(context.Context, string, string, string) ([]persistence.ToolInstallation, error)
}

type InstallToolsDirectoryReader interface {
	GetToolsDirectory(context.Context) (string, bool, error)
}

type InstallPreflight struct {
	PackageKind     tools.PackageKind
	ReleaseIdentity string
	SourceName      string
	Root            string
	Targets         []string
	Conflicts       []string
	Fingerprint     string
}

type installPreflightIdentity struct {
	PackageKind     tools.PackageKind
	ReleaseIdentity string
	SourceName      string
	Root            string
	GOOS            string
	GOARCH          string
	Targets         []string
	Conflicts       []string
	Artifacts       []InstallArtifactIdentity
}

type InstallArtifactIdentity struct {
	Name              string `json:"name"`
	ChecksumSHA256    string `json:"checksum_sha256,omitempty"`
	ChecksumAvailable bool   `json:"checksum_available"`
}

type InstallInputSnapshot struct {
	TargetIdentity     string                    `json:"target_identity"`
	SchemaVersion      int                       `json:"schema_version"`
	PackageKind        tools.PackageKind         `json:"package_kind"`
	SourceName         string                    `json:"source_name"`
	ReleaseIdentity    string                    `json:"release_identity"`
	ArtifactIdentities []InstallArtifactIdentity `json:"artifact_identities"`
	ConfirmedConflicts []string                  `json:"confirmed_conflicts,omitempty"`
}

type InstallOperations struct {
	repository InstallationOperationEnqueuer
	catalog    Catalog
	platform   tools.Platform
	river      persistence.RiverInserter
	toolsRoot  InstallToolsDirectoryReader
}

func NewInstallOperations(repository InstallationOperationEnqueuer, catalog Catalog, platform tools.Platform, riverClient persistence.RiverInserter, roots ...InstallToolsDirectoryReader) *InstallOperations {
	var rootReader InstallToolsDirectoryReader
	if len(roots) > 0 {
		rootReader = roots[0]
	}
	return &InstallOperations{repository: repository, catalog: catalog, platform: platform, river: riverClient, toolsRoot: rootReader}
}

func (s *InstallOperations) Start(ctx context.Context, packageKind tools.PackageKind, releaseIdentity string, confirmedConflicts []string) (*persistence.Operation, error) {
	preflight, err := s.Preflight(ctx, packageKind, releaseIdentity)
	if err != nil {
		return nil, err
	}
	return s.StartFromPreflight(ctx, preflight, confirmedConflicts)
}

func (s *InstallOperations) Preflight(ctx context.Context, packageKind tools.PackageKind, releaseIdentity string) (InstallPreflight, error) {
	if !s.platform.Supported() {
		return InstallPreflight{}, fmt.Errorf("unsupported instance platform")
	}
	if s.toolsRoot == nil {
		return InstallPreflight{}, fmt.Errorf("tools directory is not configured")
	}
	root, exists, err := s.toolsRoot.GetToolsDirectory(ctx)
	if err != nil {
		return InstallPreflight{}, fmt.Errorf("read tools directory: %w", err)
	}
	if !exists || root == "" {
		return InstallPreflight{}, fmt.Errorf("tools directory is not configured")
	}
	source := sourceName(packageKind, s.platform)
	release, err := s.catalog.Resolve(ctx, packageKind, s.platform, releaseIdentity)
	if err != nil {
		return InstallPreflight{}, fmt.Errorf("resolve selected release: %w", err)
	}
	if release.Identity != releaseIdentity || !validPackageArtifacts(packageKind, s.platform, release.Artifacts) {
		return InstallPreflight{}, fmt.Errorf("selected release has an invalid package artifact set")
	}
	installations, err := s.repository.ListInstallations(ctx, "", s.platform.GOOS, s.platform.GOARCH)
	if err != nil {
		return InstallPreflight{}, fmt.Errorf("list managed installations: %w", err)
	}
	known := make(map[string]struct{})
	for _, installed := range installations {
		if installed.State != "ready" {
			continue
		}
		for _, name := range tools.ExpectedExecutables(tools.PackageKind(installed.PackageKind), s.platform.GOOS) {
			known[filepath.Join(root, installed.RelativePath, name)] = struct{}{}
		}
	}
	targets, err := tools.PreflightTargets(root, packageKind, release.Identity, s.platform.GOOS, known)
	if err != nil {
		return InstallPreflight{}, err
	}
	artifacts := make([]InstallArtifactIdentity, 0, len(release.Artifacts))
	for _, artifact := range release.Artifacts {
		artifacts = append(artifacts, InstallArtifactIdentity{
			Name: artifact.Name, ChecksumSHA256: artifact.ChecksumSHA256,
			ChecksumAvailable: artifact.ChecksumSHA256 != "" || artifact.ChecksumURL != "",
		})
	}
	identity := installPreflightIdentity{
		PackageKind: packageKind, ReleaseIdentity: release.Identity, SourceName: source,
		Root: root, GOOS: s.platform.GOOS, GOARCH: s.platform.GOARCH,
		Targets: targets.Targets, Conflicts: targets.Conflicts, Artifacts: artifacts,
	}
	fingerprintData, err := json.Marshal(identity)
	if err != nil {
		return InstallPreflight{}, fmt.Errorf("encode installation preflight: %w", err)
	}
	fingerprint := sha256.Sum256(fingerprintData)
	return InstallPreflight{
		PackageKind: packageKind, ReleaseIdentity: release.Identity, SourceName: source,
		Root: root, Targets: targets.Targets, Conflicts: targets.Conflicts,
		Fingerprint: hex.EncodeToString(fingerprint[:]),
	}, nil
}

func (s *InstallOperations) StartFromPreflight(ctx context.Context, preflight InstallPreflight, confirmedConflicts []string) (*persistence.Operation, error) {
	current, err := s.Preflight(ctx, preflight.PackageKind, preflight.ReleaseIdentity)
	if err != nil {
		return nil, err
	}
	if current.Fingerprint != preflight.Fingerprint {
		return nil, fmt.Errorf("installation preflight is stale")
	}
	confirmed := append([]string(nil), confirmedConflicts...)
	sort.Strings(confirmed)
	if !samePaths(current.Conflicts, confirmed) {
		return nil, fmt.Errorf("installation conflict confirmation does not match current targets")
	}
	source := current.SourceName
	release, err := s.catalog.Resolve(ctx, current.PackageKind, s.platform, current.ReleaseIdentity)
	if err != nil || !validPackageArtifacts(current.PackageKind, s.platform, release.Artifacts) {
		return nil, fmt.Errorf("selected release is no longer valid")
	}
	if release.Identity != current.ReleaseIdentity {
		return nil, fmt.Errorf("selected release identity changed")
	}
	artifacts := make([]InstallArtifactIdentity, 0, len(release.Artifacts))
	for _, artifact := range release.Artifacts {
		artifacts = append(artifacts, InstallArtifactIdentity{
			Name: artifact.Name, ChecksumSHA256: artifact.ChecksumSHA256,
			ChecksumAvailable: artifact.ChecksumSHA256 != "" || artifact.ChecksumURL != "",
		})
	}
	identity := installPreflightIdentity{
		PackageKind: current.PackageKind, ReleaseIdentity: release.Identity, SourceName: source,
		Root: current.Root, GOOS: s.platform.GOOS, GOARCH: s.platform.GOARCH,
		Targets: current.Targets, Conflicts: current.Conflicts, Artifacts: artifacts,
	}
	fingerprintData, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("encode installation preflight: %w", err)
	}
	fingerprint := sha256.Sum256(fingerprintData)
	if hex.EncodeToString(fingerprint[:]) != current.Fingerprint {
		return nil, fmt.Errorf("installation release changed after preflight")
	}
	relative, err := tools.ManagedRelativePath(current.PackageKind, release.Identity)
	if err != nil {
		return nil, err
	}
	targetIdentity := fmt.Sprintf("%s:%s:%s:%s:%s", current.PackageKind, source, release.Identity, s.platform.GOOS, s.platform.GOARCH)
	snapshot := InstallInputSnapshot{
		TargetIdentity:     targetIdentity,
		SchemaVersion:      1,
		PackageKind:        current.PackageKind,
		SourceName:         source,
		ReleaseIdentity:    release.Identity,
		ArtifactIdentities: make([]InstallArtifactIdentity, 0, len(release.Artifacts)),
		ConfirmedConflicts: confirmed,
	}
	snapshot.ArtifactIdentities = artifacts
	artifactSnapshot, err := json.Marshal(snapshot.ArtifactIdentities)
	if err != nil {
		return nil, fmt.Errorf("encode artifact identities: %w", err)
	}
	inputSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode installation snapshot: %w", err)
	}
	installationID := uuid.New()
	operation := &persistence.Operation{
		ID:                   uuid.New(),
		Kind:                 "install",
		State:                "queued",
		Stage:                "queued",
		InputSnapshot:        inputSnapshot,
		TargetInstallationID: &installationID,
	}
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: string(current.PackageKind),
		PlatformGOOS: s.platform.GOOS, PlatformGOARCH: s.platform.GOARCH,
		SourceName: source, ReleaseIdentity: release.Identity,
		RelativePath: relative, State: "preparing",
		ArtifactIdentities: artifactSnapshot,
	}
	if s.river == nil {
		return nil, fmt.Errorf("river client is required for installation")
	}
	if err := s.repository.CreateInstallationOperationAndEnqueue(
		ctx, current.Root, installation, operation, s.river, OperationJobArgs{OperationID: operation.ID}, nil,
	); err != nil {
		return nil, fmt.Errorf("start installation operation: %w", err)
	}
	return operation, nil
}

func validPackageArtifacts(kind tools.PackageKind, platform tools.Platform, artifacts []tools.Artifact) bool {
	if kind == tools.PackageFPCalc {
		return len(artifacts) == 1 && artifacts[0].Name != ""
	}
	if kind != tools.PackageFFmpeg {
		return false
	}
	if platform.GOOS != "darwin" {
		return len(artifacts) == 1 && artifacts[0].Name != ""
	}
	if len(artifacts) != 2 {
		return false
	}
	names := map[string]bool{}
	for _, artifact := range artifacts {
		names[artifact.Name] = true
	}
	return names["ffmpeg.zip"] && names["ffprobe.zip"]
}
