package service_test

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type catalogFixture struct {
	releases []tools.Release
	platform tools.Platform
}

func (fixture *catalogFixture) List(_ context.Context, _ tools.PackageKind, platform tools.Platform) ([]tools.Release, error) {
	fixture.platform = platform
	return fixture.releases, nil
}

func (fixture *catalogFixture) Resolve(_ context.Context, _ tools.PackageKind, platform tools.Platform, identity string) (tools.Release, error) {
	fixture.platform = platform
	for _, release := range fixture.releases {
		if release.Identity == identity {
			return release, nil
		}
	}
	return tools.Release{}, context.Canceled
}

func TestCatalogServiceUsesPinnedPlatformAndHidesArtifactURLs(t *testing.T) {
	fixture := &catalogFixture{releases: []tools.Release{{
		Identity: "9.0.2",
		Artifacts: []tools.Artifact{
			{Name: "ffmpeg.zip", URL: "https://ffmpeg.martin-riedl.de/download/ffmpeg.zip", ChecksumURL: "https://ffmpeg.martin-riedl.de/download/ffmpeg.zip.sha256"},
			{Name: "ffprobe.zip", URL: "https://ffmpeg.martin-riedl.de/download/ffprobe.zip"},
		},
	}}}
	catalog := service.NewCatalogService(fixture, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})

	options, err := catalog.List(context.Background(), tools.PackageFFmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.platform != (tools.Platform{GOOS: "darwin", GOARCH: "arm64"}) {
		t.Fatalf("catalog queried platform %#v", fixture.platform)
	}
	if len(options) != 1 || options[0].Source != "martin-riedl" || len(options[0].Artifacts) != 2 {
		t.Fatalf("release options = %#v", options)
	}
	if !options[0].Artifacts[0].ChecksumProvided || options[0].Artifacts[1].ChecksumProvided {
		t.Fatalf("checksum metadata = %#v", options[0].Artifacts)
	}
}

func TestCatalogServiceRejectsUnsupportedInstancePlatform(t *testing.T) {
	catalog := service.NewCatalogService(&catalogFixture{}, tools.Platform{GOOS: "windows", GOARCH: "arm64"})
	if _, err := catalog.List(context.Background(), tools.PackageFPCalc); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}

func TestCatalogServiceReportsIntelMacLimitEvenWithoutReleases(t *testing.T) {
	for _, test := range []struct {
		platform tools.Platform
		kind     tools.PackageKind
		notice   bool
	}{
		{platform: tools.Platform{GOOS: "darwin", GOARCH: "amd64"}, kind: tools.PackageFFmpeg, notice: true},
		{platform: tools.Platform{GOOS: "darwin", GOARCH: "arm64"}, kind: tools.PackageFFmpeg},
		{platform: tools.Platform{GOOS: "darwin", GOARCH: "amd64"}, kind: tools.PackageFPCalc},
		{platform: tools.Platform{GOOS: "linux", GOARCH: "amd64"}, kind: tools.PackageFFmpeg},
	} {
		result, err := service.NewCatalogService(&catalogFixture{}, test.platform).ListWithNotice(context.Background(), test.kind)
		if err != nil {
			t.Fatal(err)
		}
		if (result.Notice != "") != test.notice || len(result.Releases) != 0 {
			t.Errorf("catalog result for %s/%s %s = %#v", test.platform.GOOS, test.platform.GOARCH, test.kind, result)
		}
	}
}
