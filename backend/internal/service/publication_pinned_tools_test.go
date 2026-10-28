package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type pinnedToolRepo struct {
	selections   []persistence.SourceAnalysisToolSelection
	installation *persistence.ToolInstallation
	root         string
	valid        bool
	guard        persistence.PublicationDeliveryGuard
}

func (r *pinnedToolRepo) PublicationToolsForDelivery(_ context.Context, guard persistence.PublicationDeliveryGuard) ([]persistence.SourceAnalysisToolSelection, *persistence.ToolInstallation, string, bool, error) {
	r.guard = guard
	return r.selections, r.installation, r.root, r.valid, nil
}
func (r *pinnedToolRepo) CheckPublicationDelivery(_ context.Context, guard persistence.PublicationDeliveryGuard) (bool, error) {
	r.guard = guard
	return r.valid, nil
}

type pinnedToolRunner func(context.Context, string, ...string) ([]byte, error)

func (r pinnedToolRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r(ctx, name, args...)
}

func TestPublicationPinnedToolsVerifyFrozenPair(t *testing.T) {
	for _, test := range []struct {
		name    string
		banner  string
		mutate  func(string, string)
		reverse bool
		wantOK  bool
	}{
		{name: "exact frozen banner", banner: "ffmpeg version 7.0 build", wantOK: true},
		{name: "wrong executable", banner: "not-ffmpeg version 7.0 build"},
		{name: "changed banner", banner: "ffmpeg version 8.0 build"},
		{name: "release identity release-1 differs from binary version 7.0", banner: "ffmpeg version 7.0 build", wantOK: true},
		{name: "selection order is irrelevant", banner: "ffmpeg version 7.0 build", reverse: true, wantOK: true},
		{name: "replacement during command", banner: "ffmpeg version 7.0 build", mutate: func(_ string, ffprobe string) {
			_ = os.Remove(ffprobe)
			_ = os.WriteFile(ffprobe, []byte("replacement"), 0o755)
		}},
		{name: "in-place change during ffprobe query", banner: "ffmpeg version 7.0 build", mutate: func(_ string, ffmpeg string) {
			_ = os.WriteFile(ffmpeg, []byte("truncated"), 0o755)
			changed := time.Now().Add(2 * time.Second)
			_ = os.Chtimes(ffmpeg, changed, changed)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, selections, repo := pinnedToolFixture(t, true)
			calls := 0
			runner := pinnedToolRunner(func(_ context.Context, name string, _ ...string) ([]byte, error) {
				calls++
				if test.mutate != nil && (test.name == "in-place change during ffprobe query" && filepath.Base(name) == "ffprobe" || test.name != "in-place change during ffprobe query" && calls == 1) {
					mutatedPath := filepath.Join(root, "ffmpeg", "release-1", "ffprobe")
					if test.name == "in-place change during ffprobe query" {
						mutatedPath = filepath.Join(root, "ffmpeg", "release-1", "ffmpeg")
					}
					test.mutate(name, mutatedPath)
				}
				if filepath.Base(name) == "ffprobe" {
					return []byte("ffprobe version 7.0 build\n"), nil
				}
				return []byte(test.banner + "\n"), nil
			})
			if test.reverse {
				selections[0], selections[1] = selections[1], selections[0]
			}
			resolver := NewPublicationPinnedToolsResolver(repo, runner, tools.Platform{GOOS: "linux", GOARCH: "amd64"})
			pair, ok, err := resolver.Resolve(context.Background(), persistence.PublicationDeliveryGuard{}, selections)
			if (err == nil && ok) != test.wantOK {
				t.Fatalf("Resolve() ok=%v err=%v, want ok=%v", ok, err, test.wantOK)
			}
			if test.name == "in-place change during ffprobe query" && (pair.FFmpeg != "" || pair.FFprobe != "") {
				t.Fatalf("Resolve() returned a partial pair after executable mutation: %+v", pair)
			}
		})
	}
}

func TestGuardedPublicationPinnedToolsResolvesOnlyKnownPairMembers(t *testing.T) {
	_, selections, repo := pinnedToolFixture(t, true)
	guard := persistence.PublicationDeliveryGuard{ApplicationAttempt: 9}
	resolver := NewPublicationPinnedToolsResolver(repo, pinnedToolRunner(func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if filepath.Base(name) == "ffprobe" {
			return []byte("ffprobe version 7.0 build\n"), nil
		}
		return []byte("ffmpeg version 7.0 build\n"), nil
	}), tools.Platform{GOOS: "linux", GOARCH: "amd64"})
	adapter := NewGuardedPublicationPinnedTools(resolver, guard)
	path, err := adapter.Executable(context.Background(), selections, "ffprobe")
	if err != nil || filepath.Base(path) != "ffprobe" || repo.guard != guard {
		t.Fatalf("Executable(ffprobe) = %q, %v; guard = %+v", path, err, repo.guard)
	}
	if _, err := adapter.Executable(context.Background(), selections, "ffmpeg-extra"); err == nil {
		t.Fatal("unsupported executable name was accepted")
	}
}

func TestPublicationPinnedToolsRejectsDuplicateCallerSelectionsBeforeCommands(t *testing.T) {
	_, _, repo := pinnedToolFixture(t, true)
	selection := repo.selections[0]
	duplicates := []persistence.SourceAnalysisToolSelection{selection, selection}
	calls := 0
	resolver := NewPublicationPinnedToolsResolver(repo, pinnedToolRunner(func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return nil, nil
	}), tools.Platform{GOOS: "linux", GOARCH: "amd64"})
	if _, ok, err := resolver.Resolve(context.Background(), persistence.PublicationDeliveryGuard{}, duplicates); err == nil || ok {
		t.Fatal("duplicate caller selections unexpectedly resolved")
	}
	if calls != 0 {
		t.Fatalf("runner called %d times for duplicate selections", calls)
	}
}

func TestPublicationPinnedToolsRejectMissingPairSymlinkAndCallerOverride(t *testing.T) {
	t.Run("missing ffprobe", func(t *testing.T) {
		_, _, repo := pinnedToolFixture(t, false)
		resolver := NewPublicationPinnedToolsResolver(repo, pinnedToolRunner(func(context.Context, string, ...string) ([]byte, error) {
			return []byte("ffmpeg version 7.0 build"), nil
		}), tools.Platform{GOOS: "linux", GOARCH: "amd64"})
		if _, ok, err := resolver.Resolve(context.Background(), persistence.PublicationDeliveryGuard{}, repo.selections); err == nil || ok {
			t.Fatal("missing pair unexpectedly resolved")
		}
	})
	t.Run("symlink leaf", func(t *testing.T) {
		root, _, repo := pinnedToolFixture(t, true)
		probe := filepath.Join(root, "ffmpeg", "release-1", "ffprobe")
		_ = os.Remove(probe)
		if err := os.Symlink("ffmpeg", probe); err != nil {
			t.Skip(err)
		}
		resolver := NewPublicationPinnedToolsResolver(repo, pinnedToolRunner(func(context.Context, string, ...string) ([]byte, error) {
			return []byte("ffmpeg version 7.0 build"), nil
		}), tools.Platform{GOOS: "linux", GOARCH: "amd64"})
		if _, ok, err := resolver.Resolve(context.Background(), persistence.PublicationDeliveryGuard{}, repo.selections); err == nil || ok {
			t.Fatal("symlink unexpectedly resolved")
		}
	})
	t.Run("symlink parent", func(t *testing.T) {
		root := t.TempDir()
		real := filepath.Join(root, "real")
		if err := os.MkdirAll(filepath.Join(real, "release-1"), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "ffmpeg")
		if err := os.Symlink(real, link); err != nil {
			t.Skip(err)
		}
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			_ = os.WriteFile(filepath.Join(real, "release-1", name), []byte("tool"), 0o755)
		}
		repo := pinnedRepoFor(root, filepath.Join(link, "release-1"))
		resolver := NewPublicationPinnedToolsResolver(repo, pinnedToolRunner(func(context.Context, string, ...string) ([]byte, error) {
			return []byte("ffmpeg version 7.0 build"), nil
		}), tools.Platform{GOOS: "linux", GOARCH: "amd64"})
		if _, ok, err := resolver.Resolve(context.Background(), persistence.PublicationDeliveryGuard{}, repo.selections); err == nil || ok {
			t.Fatal("symlink parent unexpectedly resolved")
		}
	})
	t.Run("caller override", func(t *testing.T) {
		_, _, repo := pinnedToolFixture(t, true)
		changed := append([]persistence.SourceAnalysisToolSelection(nil), repo.selections...)
		changed[0].RelativePath = "elsewhere"
		resolver := NewPublicationPinnedToolsResolver(repo, pinnedToolRunner(func(context.Context, string, ...string) ([]byte, error) { return nil, nil }), tools.Platform{GOOS: "linux", GOARCH: "amd64"})
		if _, ok, err := resolver.Resolve(context.Background(), persistence.PublicationDeliveryGuard{}, changed); err == nil || ok {
			t.Fatal("caller path override unexpectedly resolved")
		}
	})
}

func pinnedToolFixture(t *testing.T, both bool) (string, []persistence.SourceAnalysisToolSelection, *pinnedToolRepo) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "ffmpeg", "release-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte("fake executable"), 0o755)
	if both {
		_ = os.WriteFile(filepath.Join(dir, "ffprobe"), []byte("fake executable"), 0o755)
	}
	repo := pinnedRepoFor(root, dir)
	return root, repo.selections, repo
}

func pinnedRepoFor(root, dir string) *pinnedToolRepo {
	id := uuid.New()
	versions, _ := json.Marshal(map[string]string{"ffmpeg": "ffmpeg version 7.0 build", "ffprobe": "ffprobe version 7.0 build"})
	now := time.Now()
	return &pinnedToolRepo{
		root: root, valid: true,
		installation: &persistence.ToolInstallation{ID: id, PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64", RelativePath: "ffmpeg/release-1", Version: "ffmpeg 7.0 + ffprobe 7.0", State: "ready", VerifiedAt: &now, ExecutableVerificationOutput: versions},
		selections: []persistence.SourceAnalysisToolSelection{
			{PackageKind: "ffmpeg", InstallationID: id, RelativePath: "ffmpeg/release-1", Executable: "ffmpeg", Version: "ffmpeg 7.0 + ffprobe 7.0", VersionBanner: "ffmpeg version 7.0 build"},
			{PackageKind: "ffmpeg", InstallationID: id, RelativePath: "ffmpeg/release-1", Executable: "ffprobe", Version: "ffmpeg 7.0 + ffprobe 7.0", VersionBanner: "ffprobe version 7.0 build"},
		},
	}
}
