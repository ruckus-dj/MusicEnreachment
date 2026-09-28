package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type InstallationOperationEnqueuer interface {
	CreateInstallationOperationAndEnqueue(context.Context, *persistence.ToolInstallation, *persistence.Operation, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error
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
}

func NewInstallOperations(repository InstallationOperationEnqueuer, catalog Catalog, platform tools.Platform, riverClient persistence.RiverInserter) *InstallOperations {
	return &InstallOperations{repository: repository, catalog: catalog, platform: platform, river: riverClient}
}

func (s *InstallOperations) Start(ctx context.Context, packageKind tools.PackageKind, releaseIdentity string, confirmedConflicts []string) (*persistence.Operation, error) {
	if !s.platform.Supported() {
		return nil, fmt.Errorf("unsupported instance platform")
	}
	source := sourceName(packageKind, s.platform)
	release, err := s.catalog.Resolve(ctx, packageKind, s.platform, releaseIdentity)
	if err != nil {
		return nil, fmt.Errorf("resolve selected release: %w", err)
	}
	if release.Identity != releaseIdentity || !validPackageArtifacts(packageKind, s.platform, release.Artifacts) {
		return nil, fmt.Errorf("selected release has an invalid package artifact set")
	}
	relative, err := tools.ManagedRelativePath(packageKind, release.Identity)
	if err != nil {
		return nil, err
	}
	targetIdentity := fmt.Sprintf("%s:%s:%s:%s:%s", packageKind, source, release.Identity, s.platform.GOOS, s.platform.GOARCH)
	snapshot := InstallInputSnapshot{
		TargetIdentity:     targetIdentity,
		SchemaVersion:      1,
		PackageKind:        packageKind,
		SourceName:         source,
		ReleaseIdentity:    release.Identity,
		ArtifactIdentities: make([]InstallArtifactIdentity, 0, len(release.Artifacts)),
		ConfirmedConflicts: append([]string(nil), confirmedConflicts...),
	}
	for _, artifact := range release.Artifacts {
		snapshot.ArtifactIdentities = append(snapshot.ArtifactIdentities, InstallArtifactIdentity{
			Name: artifact.Name, ChecksumSHA256: artifact.ChecksumSHA256,
			ChecksumAvailable: artifact.ChecksumSHA256 != "" || artifact.ChecksumURL != "",
		})
	}
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
		ID: installationID, PackageKind: string(packageKind),
		PlatformGOOS: s.platform.GOOS, PlatformGOARCH: s.platform.GOARCH,
		SourceName: source, ReleaseIdentity: release.Identity,
		RelativePath: relative, State: "preparing",
		ArtifactIdentities: artifactSnapshot,
	}
	if s.river == nil {
		return nil, fmt.Errorf("river client is required for installation")
	}
	if err := s.repository.CreateInstallationOperationAndEnqueue(
		ctx, installation, operation, s.river, OperationJobArgs{OperationID: operation.ID}, nil,
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
