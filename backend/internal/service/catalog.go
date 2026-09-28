package service

import (
	"context"
	"fmt"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
)

type Catalog interface {
	List(context.Context, tools.PackageKind, tools.Platform) ([]tools.Release, error)
	Resolve(context.Context, tools.PackageKind, tools.Platform, string) (tools.Release, error)
}

type CatalogService struct {
	catalog  Catalog
	platform tools.Platform
}

type ReleaseOption struct {
	Identity  string
	Source    string
	Artifacts []ArtifactOption
}

type ArtifactOption struct {
	Name             string
	ChecksumProvided bool
}

func NewCatalogService(catalog Catalog, platform tools.Platform) *CatalogService {
	return &CatalogService{catalog: catalog, platform: platform}
}

func (s *CatalogService) List(ctx context.Context, packageKind tools.PackageKind) ([]ReleaseOption, error) {
	if !s.platform.Supported() {
		return nil, fmt.Errorf("unsupported instance platform")
	}
	releases, err := s.catalog.List(ctx, packageKind, s.platform)
	if err != nil {
		return nil, err
	}
	source := sourceName(packageKind, s.platform)
	options := make([]ReleaseOption, 0, len(releases))
	for _, release := range releases {
		option := ReleaseOption{Identity: release.Identity, Source: source, Artifacts: make([]ArtifactOption, 0, len(release.Artifacts))}
		for _, artifact := range release.Artifacts {
			option.Artifacts = append(option.Artifacts, ArtifactOption{
				Name:             artifact.Name,
				ChecksumProvided: artifact.ChecksumURL != "" || artifact.ChecksumSHA256 != "",
			})
		}
		options = append(options, option)
	}
	return options, nil
}

func (s *CatalogService) Resolve(ctx context.Context, packageKind tools.PackageKind, identity string) (tools.Release, error) {
	if !s.platform.Supported() {
		return tools.Release{}, fmt.Errorf("unsupported instance platform")
	}
	return s.catalog.Resolve(ctx, packageKind, s.platform, identity)
}

func sourceName(packageKind tools.PackageKind, platform tools.Platform) string {
	if packageKind == tools.PackageFFmpeg && platform.GOOS == "darwin" {
		return "martin-riedl"
	}
	if packageKind == tools.PackageFFmpeg {
		return "btbn"
	}
	return "chromaprint"
}
