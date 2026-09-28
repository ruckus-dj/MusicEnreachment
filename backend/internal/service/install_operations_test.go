package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type installCatalogFixture struct {
	release tools.Release
}

func (fixture installCatalogFixture) List(context.Context, tools.PackageKind, tools.Platform) ([]tools.Release, error) {
	return []tools.Release{fixture.release}, nil
}

func (fixture installCatalogFixture) Resolve(_ context.Context, _ tools.PackageKind, _ tools.Platform, identity string) (tools.Release, error) {
	if identity != fixture.release.Identity {
		return tools.Release{}, context.Canceled
	}
	return fixture.release, nil
}

type installEnqueuerFixture struct {
	installation *persistence.ToolInstallation
	operation    *persistence.Operation
	args         river.JobArgs
}

func (fixture *installEnqueuerFixture) CreateInstallationOperationAndEnqueue(_ context.Context, installation *persistence.ToolInstallation, operation *persistence.Operation, _ persistence.RiverInserter, args river.JobArgs, _ *river.InsertOpts) error {
	fixture.installation = installation
	fixture.operation = operation
	fixture.args = args
	return nil
}

func (*installEnqueuerFixture) ListInstallations(context.Context, string, string, string) ([]persistence.ToolInstallation, error) {
	return nil, nil
}

type unusedRiverClient struct{}

func (unusedRiverClient) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, nil
}

func TestInstallStartStoresIdentitiesAndQueuesOperationOnlyArgs(t *testing.T) {
	enqueuer := new(installEnqueuerFixture)
	catalog := installCatalogFixture{release: tools.Release{
		Identity: "v1.6.1",
		Artifacts: []tools.Artifact{{
			Name:           "chromaprint-fpcalc-1.6.1-linux-x86_64.tar.gz",
			URL:            "https://github.com/acoustid/chromaprint/releases/download/v1.6.1/fpcalc.tar.gz",
			ChecksumSHA256: "published-digest",
		}},
	}}
	installs := service.NewInstallOperations(enqueuer, catalog, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, unusedRiverClient{}, toolsDirectoryFixture(t.TempDir()))
	operation, err := installs.Start(context.Background(), tools.PackageFPCalc, "v1.6.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if enqueuer.installation == nil || enqueuer.operation == nil {
		t.Fatal("installation and operation were not enqueued")
	}
	if enqueuer.installation.State != "preparing" || enqueuer.installation.RelativePath != "fpcalc/v1.6.1" {
		t.Fatalf("installation target = %#v", enqueuer.installation)
	}
	args, ok := enqueuer.args.(service.OperationJobArgs)
	if !ok || args.OperationID != operation.ID {
		t.Fatalf("River args = %#v, want operation ID only", enqueuer.args)
	}
	if kind := args.Kind(); kind != "operation_v1" {
		t.Fatalf("River kind = %q", kind)
	}
	var snapshot service.InstallInputSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.TargetIdentity != "fpcalc:chromaprint:v1.6.1:linux:amd64" ||
		len(snapshot.ArtifactIdentities) != 1 || snapshot.ArtifactIdentities[0].Name != catalog.release.Artifacts[0].Name {
		t.Fatalf("installation snapshot = %#v", snapshot)
	}
	if strings.Contains(string(operation.InputSnapshot), "https://") || strings.Contains(string(enqueuer.installation.ArtifactIdentities), "https://") {
		t.Fatal("download URL leaked into durable installation state")
	}
}

func TestInstallStartRequiresCompleteLogicalPackage(t *testing.T) {
	enqueuer := new(installEnqueuerFixture)
	catalog := installCatalogFixture{release: tools.Release{Identity: "9.0.2", Artifacts: []tools.Artifact{{Name: "ffmpeg.zip"}}}}
	installs := service.NewInstallOperations(enqueuer, catalog, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}, unusedRiverClient{}, toolsDirectoryFixture(t.TempDir()))
	if _, err := installs.Start(context.Background(), tools.PackageFFmpeg, "9.0.2", nil); err == nil {
		t.Fatal("incomplete macOS FFmpeg package was accepted")
	}
	if enqueuer.operation != nil || enqueuer.installation != nil {
		t.Fatal("invalid package was persisted")
	}
}

func TestInstallStartRejectsStalePreflightAfterUnknownTargetAppears(t *testing.T) {
	enqueuer := new(installEnqueuerFixture)
	catalog := installCatalogFixture{release: tools.Release{
		Identity:  "8.0",
		Artifacts: []tools.Artifact{{Name: "ffmpeg-8.0-linux64-gpl.tar.xz"}},
	}}
	root := t.TempDir()
	installs := service.NewInstallOperations(enqueuer, catalog, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, unusedRiverClient{}, toolsDirectoryFixture(root))
	plan, err := installs.Preflight(context.Background(), tools.PackageFFmpeg, "8.0")
	if err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(root, "ffmpeg", "8.0", "ffmpeg")
	if err := os.MkdirAll(filepath.Dir(conflict), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflict, []byte("unknown"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installs.StartFromPreflight(context.Background(), plan, nil); err == nil {
		t.Fatal("installation used stale preflight after an unknown target appeared")
	}
	if enqueuer.operation != nil {
		t.Fatal("stale preflight created an operation")
	}
}
