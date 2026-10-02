package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type moveRepositoryFixture struct {
	installations []persistence.ToolInstallation
	operation     *persistence.Operation
}

func (repository *moveRepositoryFixture) ListInstallations(context.Context, string, string, string) ([]persistence.ToolInstallation, error) {
	return repository.installations, nil
}

func (repository *moveRepositoryFixture) CreateToolsMoveOperationAndEnqueue(_ context.Context, operation *persistence.Operation, _ persistence.RiverInserter, _ river.JobArgs, _ *river.InsertOpts) error {
	repository.operation = operation
	return nil
}

type unusedMoveRiverClient struct{}

func (unusedMoveRiverClient) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("not called by test fixture")
}

func TestMovePreflightConfirmsExactConflictsAndRevalidatesSnapshot(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	oldRoot, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newRoot, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, settings.ToolsDirectoryKey, oldRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(newRoot, "ffmpeg", "8.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		source := filepath.Join(oldRoot, "ffmpeg", "8.0", name)
		if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	targetConflict := filepath.Join(newRoot, "ffmpeg", "8.0", "ffmpeg")
	if err := os.WriteFile(targetConflict, []byte("unknown target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "operator-note.txt"), []byte("preserve"), 0o644); err != nil {
		t.Fatal(err)
	}
	repository := &moveRepositoryFixture{installations: []persistence.ToolInstallation{{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "btbn", ReleaseIdentity: "8.0", RelativePath: "ffmpeg/8.0", State: "ready",
	}}}
	moveService := service.NewMoveTools(repository, settings.New(store, nil), tools.Platform{GOOS: "linux", GOARCH: "amd64"}, unusedMoveRiverClient{})

	plan, err := moveService.Preflight(ctx, newRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0] != targetConflict || len(plan.Snapshot.Files) != 2 {
		t.Fatalf("move preflight = %#v", plan)
	}
	if _, err := moveService.Start(ctx, plan, nil); err == nil {
		t.Fatal("move started without exact conflict confirmation")
	}
	if repository.operation != nil {
		t.Fatal("invalid conflict confirmation enqueued a move")
	}
	if _, err := moveService.Start(ctx, plan, []string{targetConflict}); err != nil {
		t.Fatal(err)
	}
	if repository.operation == nil || repository.operation.Kind != "move_tools_root" {
		t.Fatalf("move operation = %#v", repository.operation)
	}
	var snapshot service.MoveSnapshot
	if err := json.Unmarshal(repository.operation.InputSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ConfirmedConflicts) != 1 || snapshot.ConfirmedConflicts[0] != targetConflict {
		t.Fatalf("persisted conflict confirmation = %#v", snapshot.ConfirmedConflicts)
	}
}

func TestMovePreflightRejectsStaleSourceIdentity(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	oldRoot, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newRoot, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, settings.ToolsDirectoryKey, oldRoot); err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(oldRoot, "fpcalc", "1.6.1", "fpcalc")
	if err := os.MkdirAll(filepath.Dir(releasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releasePath, []byte("before"), 0o755); err != nil {
		t.Fatal(err)
	}
	repository := &moveRepositoryFixture{installations: []persistence.ToolInstallation{{
		ID: uuid.New(), PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: "1.6.1", RelativePath: "fpcalc/1.6.1", State: "ready",
	}}}
	moveService := service.NewMoveTools(repository, settings.New(store, nil), tools.Platform{GOOS: "linux", GOARCH: "amd64"}, unusedMoveRiverClient{})
	plan, err := moveService.Preflight(ctx, newRoot, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releasePath, []byte("changed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := moveService.Start(ctx, plan, nil); err == nil {
		t.Fatal("move started with stale source file identity")
	}
	if repository.operation != nil {
		t.Fatal("stale move was enqueued")
	}
}
