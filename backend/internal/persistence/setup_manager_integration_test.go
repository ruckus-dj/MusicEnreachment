//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type transactionTestArgs struct{}

func (transactionTestArgs) Kind() string { return "setup_manager_integration_test" }

func TestSetupManagerPersistenceWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	installation := &persistence.ToolInstallation{
		ID:                 uuid.New(),
		PackageKind:        "ffmpeg",
		PlatformGOOS:       "linux",
		PlatformGOARCH:     "amd64",
		SourceName:         "btbn",
		ReleaseIdentity:    "7.1",
		RelativePath:       "ffmpeg/7.1",
		State:              "preparing",
		ArtifactIdentities: json.RawMessage(`{"archive":"ffmpeg-7.1"}`),
	}
	if err := repository.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create installation: %v", err)
	}
	duplicate := *installation
	duplicate.ID = uuid.New()
	if err := repository.CreateInstallation(ctx, &duplicate); err == nil {
		t.Fatal("duplicate installation identity was accepted")
	}
	installations, err := repository.ListInstallations(ctx, "ffmpeg", "linux", "amd64")
	if err != nil || len(installations) != 1 {
		t.Fatalf("list installations = %d, %v; want 1, nil", len(installations), err)
	}
	if _, err := repository.GetInstallationByIdentity(ctx, "ffmpeg", "btbn", "7.1", "linux", "amd64"); err != nil {
		t.Fatalf("get installation by identity: %v", err)
	}
	verifiedAt := time.Now().UTC()
	if err := repository.MarkInstallationReady(ctx, installation.ID, json.RawMessage(`{"ffmpeg":"7.1","ffprobe":"7.1"}`), verifiedAt); err != nil {
		t.Fatalf("mark installation ready: %v", err)
	}
	ready, err := repository.GetInstallation(ctx, installation.ID)
	if err != nil || ready.State != "ready" || ready.VerifiedAt == nil {
		t.Fatalf("ready installation = %#v, %v", ready, err)
	}

	snapshot, err := json.Marshal(map[string]string{"target_identity": "ffmpeg:btbn:7.1:linux:amd64"})
	if err != nil {
		t.Fatalf("marshal operation snapshot: %v", err)
	}
	operation := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "queued", Stage: "download", InputSnapshot: json.RawMessage(snapshot), TargetInstallationID: &installation.ID}
	if err := repository.CreateOperation(ctx, operation); err != nil {
		t.Fatalf("create operation: %v", err)
	}
	duplicateOperation := *operation
	duplicateOperation.ID = uuid.New()
	if err := repository.CreateOperation(ctx, &duplicateOperation); err == nil {
		t.Fatal("conflicting active operation was accepted")
	}
	moveOperation := &persistence.Operation{
		ID:            uuid.New(),
		Kind:          "move_tools_root",
		State:         "queued",
		Stage:         "preflight",
		InputSnapshot: json.RawMessage(`{"old_root":"/tools","new_root":"/new-tools"}`),
	}
	if err := repository.CreateOperation(ctx, moveOperation); err == nil {
		t.Fatal("tools-root move concurrent with installation was accepted")
	}
	operations, err := repository.ListOperations(ctx, "queued")
	if err != nil || len(operations) != 1 {
		t.Fatalf("list queued operations = %d, %v; want 1, nil", len(operations), err)
	}
	err = database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		locked, err := repository.GetInstallationForUpdate(ctx, tx, installation.ID)
		if err != nil || locked.ID != installation.ID {
			t.Fatalf("lock installation = %#v, %v", locked, err)
		}
		conflicts, err := repository.ListActiveOperationConflictsForUpdate(ctx, tx, "ffmpeg:btbn:7.1:linux:amd64")
		if err != nil || len(conflicts) != 1 {
			t.Fatalf("lock operation conflicts = %d, %v; want 1, nil", len(conflicts), err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("lock persistence rows: %v", err)
	}

	assertTransactionalRiverEnqueue(t, ctx, database, repository)
}

func assertTransactionalRiverEnqueue(t *testing.T, ctx context.Context, database *bun.DB, repository *persistence.SetupManagerRepository) {
	t.Helper()
	driver := riverdatabasesql.New(database.DB)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatalf("create River migrator: %v", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}
	client, err := river.NewClient(driver, &river.Config{})
	if err != nil {
		t.Fatalf("create River client: %v", err)
	}
	committedSnapshot := json.RawMessage(`{"target_identity":"fpcalc:chromaprint:1.5.1:linux:amd64"}`)
	committedOperation := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "queued", Stage: "download", InputSnapshot: committedSnapshot}
	err = repository.CreateOperationAndEnqueue(ctx, committedOperation, client, transactionTestArgs{}, nil)
	if err != nil {
		t.Fatalf("commit operation and River job transaction: %v", err)
	}
	var committedOperations int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM operation WHERE id = ?", committedOperation.ID).Scan(&committedOperations); err != nil {
		t.Fatalf("count committed operation: %v", err)
	}
	if committedOperations != 1 {
		t.Fatalf("committed operations = %d, want 1", committedOperations)
	}

	rolledBackSnapshot := committedSnapshot
	rolledBackOperation := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "queued", Stage: "download", InputSnapshot: rolledBackSnapshot}
	err = repository.CreateOperationAndEnqueue(ctx, rolledBackOperation, client, transactionTestArgs{}, nil)
	if err == nil {
		t.Fatal("conflicting operation and River job were committed")
	}
	var rolledBackOperations int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM operation WHERE id = ?", rolledBackOperation.ID).Scan(&rolledBackOperations); err != nil {
		t.Fatalf("count rolled-back operation: %v", err)
	}
	if rolledBackOperations != 0 {
		t.Fatalf("rolled-back operations = %d, want 0", rolledBackOperations)
	}
	var jobs int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM river_job WHERE kind = ?", transactionTestArgs{}.Kind()).Scan(&jobs); err != nil {
		t.Fatalf("count River jobs: %v", err)
	}
	if jobs != 1 {
		t.Fatalf("River jobs = %d, want 1 committed job", jobs)
	}

	_, err = database.ExecContext(ctx, "INSERT INTO operation (id, kind, state, stage, input_snapshot) VALUES (?, 'install', 'failed', 'download', '{}'::jsonb)", uuid.New())
	if err == nil {
		t.Fatal("failed operation without safe error was accepted")
	}
}
