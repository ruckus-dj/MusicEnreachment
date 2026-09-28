//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type testToolsDirectory string

func (directory testToolsDirectory) GetToolsDirectory(context.Context) (string, bool, error) {
	return string(directory), true, nil
}

type acceptingInstallationVerifier struct{}

func (acceptingInstallationVerifier) VerifyInstallation(context.Context, string, string, tools.PackageKind, string, string) (map[string]string, error) {
	return map[string]string{"ffmpeg": "ffmpeg version 7.1", "ffprobe": "ffprobe version 7.1"}, nil
}

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
	duplicateOperation.InputSnapshot = json.RawMessage(`{"target_identity":"different-client-value"}`)
	if err := repository.CreateOperation(ctx, &duplicateOperation); err == nil {
		t.Fatal("conflicting operation for the same installation was accepted")
	}
	differentTarget := &persistence.ToolInstallation{ID: uuid.New(), PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64", SourceName: "chromaprint", ReleaseIdentity: "1.5.1", RelativePath: "fpcalc/1.5.1", State: "preparing"}
	if err := repository.CreateInstallation(ctx, differentTarget); err != nil {
		t.Fatalf("create different target installation: %v", err)
	}
	differentTargetOperation := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "queued", Stage: "download", InputSnapshot: json.RawMessage(`{"target_identity":"fpcalc:chromaprint:1.5.1:linux:amd64"}`), TargetInstallationID: &differentTarget.ID}
	if err := repository.CreateOperation(ctx, differentTargetOperation); err != nil {
		t.Fatalf("create operation for a different installation: %v", err)
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
	if err != nil || len(operations) != 2 {
		t.Fatalf("list queued operations = %d, %v; want 2, nil", len(operations), err)
	}
	err = database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		locked, err := repository.GetInstallationForUpdate(ctx, tx, installation.ID)
		if err != nil || locked.ID != installation.ID {
			t.Fatalf("lock installation = %#v, %v", locked, err)
		}
		conflicts, err := repository.ListActiveOperationConflictsForUpdate(ctx, tx, installation.ID)
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

func TestActivateInstallationWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	ready := createReadyInstallation(t, ctx, repository, "ffmpeg", "linux", "amd64", "7.1")
	activations := service.NewInstallations(repository, settings.Platform{GOOS: "linux", GOARCH: "amd64"}, testToolsDirectory("/tools"), acceptingInstallationVerifier{})
	if err := activations.Activate(ctx, "fpcalc", ready.ID); err == nil {
		t.Fatal("activation accepted the wrong package")
	}
	if err := service.NewInstallations(repository, settings.Platform{GOOS: "darwin", GOARCH: "arm64"}, testToolsDirectory("/tools"), acceptingInstallationVerifier{}).Activate(ctx, "ffmpeg", ready.ID); err == nil {
		t.Fatal("activation accepted the wrong platform")
	}
	unready := &persistence.ToolInstallation{ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64", SourceName: "test", ReleaseIdentity: "6.0", RelativePath: "ffmpeg/6.0", State: "preparing"}
	if err := repository.CreateInstallation(ctx, unready); err != nil {
		t.Fatalf("create unready installation: %v", err)
	}
	if err := activations.Activate(ctx, "ffmpeg", unready.ID); err == nil {
		t.Fatal("activation accepted an unready installation")
	}
	if err := activations.Activate(ctx, "ffmpeg", ready.ID); err != nil {
		t.Fatalf("activate ready installation: %v", err)
	}
	var active string
	if err := database.QueryRowContext(ctx, "SELECT setting_value FROM app_setting WHERE setting_name = ?", settings.ActiveFFmpegInstallationKey).Scan(&active); err != nil || active != ready.ID.String() {
		t.Fatalf("active installation = %q, %v; want %q", active, err, ready.ID)
	}

	second := createReadyInstallation(t, ctx, repository, "ffmpeg", "linux", "amd64", "8.0")
	errors := make(chan error, 2)
	go func() { errors <- activations.Activate(ctx, "ffmpeg", ready.ID) }()
	go func() { errors <- activations.Activate(ctx, "ffmpeg", second.ID) }()
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent activation: %v", err)
		}
	}
	if err := database.QueryRowContext(ctx, "SELECT setting_value FROM app_setting WHERE setting_name = ?", settings.ActiveFFmpegInstallationKey).Scan(&active); err != nil || (active != ready.ID.String() && active != second.ID.String()) {
		t.Fatalf("concurrent active installation = %q, %v", active, err)
	}
}

func TestSetupActivationStopsAfterCompletion(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	first := createReadyInstallation(t, ctx, repository, "fpcalc", "linux", "amd64", "1.5.1")
	activated, err := repository.ActivateInstallationDuringSetup(ctx, first.ID, "fpcalc", "linux", "amd64", settings.ActiveFPCalcInstallationKey)
	if err != nil || !activated {
		t.Fatalf("setup activation = %v, %v; want true, nil", activated, err)
	}
	settingsRepository := persistence.NewSettingsRepository(database)
	registry := settings.New(settingsRepository, nil)
	if err := registry.CompleteSetup(ctx); err != nil {
		t.Fatal(err)
	}

	second := createReadyInstallation(t, ctx, repository, "fpcalc", "linux", "amd64", "1.6.0")
	activated, err = repository.ActivateInstallationDuringSetup(ctx, second.ID, "fpcalc", "linux", "amd64", settings.ActiveFPCalcInstallationKey)
	if err != nil || activated {
		t.Fatalf("post-setup activation = %v, %v; want false, nil", activated, err)
	}
	var active string
	if err := database.QueryRowContext(ctx, "SELECT setting_value FROM app_setting WHERE setting_name = ?", settings.ActiveFPCalcInstallationKey).Scan(&active); err != nil || active != first.ID.String() {
		t.Fatalf("active installation changed after setup: %q, %v", active, err)
	}
}

func TestCommitToolsRootMoveSwitchesSettingAndOperationAtomically(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	settingsRepository := persistence.NewSettingsRepository(database)
	if err := settingsRepository.Set(ctx, settings.ToolsDirectoryKey, "/old-tools"); err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "running", Stage: "switch",
		InputSnapshot: json.RawMessage(`{"old_root":"/old-tools","new_root":"/new-tools"}`),
	}
	if err := repository.CreateOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	if err := repository.CommitToolsRootMove(ctx, operation.ID, "/old-tools", "/new-tools"); err != nil {
		t.Fatal(err)
	}
	current, _, err := settingsRepository.Get(ctx, settings.ToolsDirectoryKey)
	if err != nil || current != "/new-tools" {
		t.Fatalf("tools directory = %q, %v", current, err)
	}
	switched, err := repository.GetOperation(ctx, operation.ID)
	if err != nil || switched.State != "running" || switched.Stage != "switched" {
		t.Fatalf("switched move operation = %#v, %v", switched, err)
	}
	if err := repository.FinishToolsRootMove(ctx, operation.ID); err != nil {
		t.Fatal(err)
	}
	finished, err := repository.GetOperation(ctx, operation.ID)
	if err != nil || finished.State != "succeeded" || finished.Stage != "switched" || finished.FinishedAt == nil {
		t.Fatalf("finished move operation = %#v, %v", finished, err)
	}

	stale := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "running", Stage: "switch",
		InputSnapshot: json.RawMessage(`{"old_root":"/old-tools","new_root":"/other-tools"}`),
	}
	if err := repository.CreateOperation(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if err := repository.CommitToolsRootMove(ctx, stale.ID, "/old-tools", "/other-tools"); err == nil {
		t.Fatal("stale move switched the tools root")
	}
	current, _, err = settingsRepository.Get(ctx, settings.ToolsDirectoryKey)
	if err != nil || current != "/new-tools" {
		t.Fatalf("stale move changed tools directory to %q: %v", current, err)
	}
}

func TestDeleteInstallationWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	ready := createReadyInstallation(t, ctx, repository, "ffmpeg", "linux", "amd64", "7.1")
	root := t.TempDir()
	directory := filepath.Join(root, ready.RelativePath)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe", "operator-note.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := repository.ActivateInstallation(ctx, ready.ID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey); err != nil {
		t.Fatal(err)
	}
	called := false
	removeFiles := func(*persistence.ToolInstallation) error {
		called = true
		return nil
	}
	if err := repository.DeleteInstallation(ctx, ready.ID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey, removeFiles); err == nil {
		t.Fatal("active installation deletion succeeded")
	}
	if called {
		t.Fatal("filesystem callback ran for active installation")
	}

	if _, err := database.ExecContext(ctx, "DELETE FROM app_setting WHERE setting_name = ?", settings.ActiveFFmpegInstallationKey); err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "delete", State: "queued", Stage: "preflight",
		InputSnapshot: json.RawMessage(`{"target_identity":"ffmpeg:test:7.1:linux:amd64"}`), TargetInstallationID: &ready.ID,
	}
	if err := repository.CreateOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteInstallation(ctx, ready.ID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey, removeFiles); err == nil {
		t.Fatal("installation with active operation deletion succeeded")
	}
	if called {
		t.Fatal("filesystem callback ran for busy installation")
	}
	if _, err := database.ExecContext(ctx, "DELETE FROM operation WHERE id = ?", operation.ID); err != nil {
		t.Fatal(err)
	}

	if err := repository.DeleteInstallation(ctx, ready.ID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey, func(installation *persistence.ToolInstallation) error {
		called = true
		return tools.Delete(root, installation.RelativePath, "linux", nil, tools.PackageFFmpeg, installation.ID.String())
	}); err != nil {
		t.Fatalf("delete inactive installation: %v", err)
	}
	if !called {
		t.Fatal("filesystem callback was not called")
	}
	if _, err := repository.GetInstallation(ctx, ready.ID); err == nil {
		t.Fatal("deleted installation remains in database")
	}
	if _, err := os.Stat(filepath.Join(directory, "operator-note.txt")); err != nil {
		t.Fatalf("unknown file was deleted: %v", err)
	}
}

func TestActiveOperationExclusivityWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	first := createReadyInstallation(t, ctx, repository, "ffmpeg", "linux", "amd64", "7.1")
	second := createReadyInstallation(t, ctx, repository, "fpcalc", "linux", "amd64", "1.5.1")

	missingTarget := &persistence.Operation{ID: uuid.New(), Kind: "activate", State: "queued", Stage: "verify", InputSnapshot: json.RawMessage(`{"target_identity":"ffmpeg:missing"}`)}
	if err := repository.CreateOperation(ctx, missingTarget); err == nil {
		t.Fatal("activate operation without a target installation was accepted")
	}
	activate := &persistence.Operation{ID: uuid.New(), Kind: "activate", State: "queued", Stage: "verify", InputSnapshot: json.RawMessage(`{"target_identity":"ffmpeg:first"}`), TargetInstallationID: &first.ID}
	if err := repository.CreateOperation(ctx, activate); err != nil {
		t.Fatalf("create activate operation: %v", err)
	}
	sameInstallationDelete := &persistence.Operation{ID: uuid.New(), Kind: "delete", State: "queued", Stage: "delete", InputSnapshot: json.RawMessage(`{"target_identity":"different-client-value"}`), TargetInstallationID: &first.ID}
	if err := repository.CreateOperation(ctx, sameInstallationDelete); err == nil {
		t.Fatal("delete concurrent with activation of the same installation was accepted")
	}
	differentInstallationDelete := &persistence.Operation{ID: uuid.New(), Kind: "delete", State: "queued", Stage: "delete", InputSnapshot: json.RawMessage(`{"target_identity":"fpcalc:second"}`), TargetInstallationID: &second.ID}
	if err := repository.CreateOperation(ctx, differentInstallationDelete); err != nil {
		t.Fatalf("create operation for a different installation: %v", err)
	}
	move := &persistence.Operation{ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "preflight", InputSnapshot: json.RawMessage(`{"old_root":"/tools","new_root":"/new-tools"}`)}
	if err := repository.CreateOperation(ctx, move); err == nil {
		t.Fatal("move concurrent with activate/delete was accepted")
	}
	finishOperation(t, ctx, repository, activate.ID)
	finishOperation(t, ctx, repository, differentInstallationDelete.ID)

	if err := repository.CreateOperation(ctx, move); err != nil {
		t.Fatalf("create move operation: %v", err)
	}
	secondMove := *move
	secondMove.ID = uuid.New()
	if err := repository.CreateOperation(ctx, &secondMove); err == nil {
		t.Fatal("second active move operation was accepted")
	}
	installDuringMove := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "queued", Stage: "download", InputSnapshot: json.RawMessage(`{"target_identity":"fpcalc:during-move"}`), TargetInstallationID: &second.ID}
	if err := repository.CreateOperation(ctx, installDuringMove); err == nil {
		t.Fatal("install concurrent with move was accepted")
	}
}

func TestRepositoryStateTransitionsWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	installation := &persistence.ToolInstallation{ID: uuid.New(), PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64", SourceName: "test", ReleaseIdentity: "1.0", RelativePath: "fpcalc/1.0", State: "preparing"}
	if err := repository.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create installation: %v", err)
	}
	installation.RelativePath = "fpcalc/1.0-updated"
	if err := repository.UpdateInstallation(ctx, installation); err != nil {
		t.Fatalf("update installation: %v", err)
	}
	if err := repository.MarkInstallationFailed(ctx, installation.ID); err != nil {
		t.Fatalf("mark installation failed: %v", err)
	}
	if err := repository.MarkInstallationFailed(ctx, installation.ID); err == nil {
		t.Fatal("mark failed accepted a non-preparing installation")
	}
	missingTarget := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "queued", Stage: "download", InputSnapshot: json.RawMessage(`{"target_identity":"fpcalc:test:missing-target:linux:amd64"}`)}
	if err := repository.CreateOperation(ctx, missingTarget); err == nil {
		t.Fatal("install operation without a target installation was accepted")
	}

	concurrent := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "queued", Stage: "download", InputSnapshot: json.RawMessage(`{"target_identity":"fpcalc:test:concurrent:linux:amd64"}`), TargetInstallationID: &installation.ID}
	if err := repository.CreateOperation(ctx, concurrent); err != nil {
		t.Fatalf("create concurrent operation: %v", err)
	}
	oldUpdatedAt := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := database.ExecContext(ctx, "UPDATE operation SET updated_at = ? WHERE id = ?", oldUpdatedAt, concurrent.ID); err != nil {
		t.Fatalf("set old operation timestamp: %v", err)
	}
	transitionErrors := make(chan error, 2)
	for range 2 {
		go func() {
			transitionErrors <- repository.TransitionOperation(ctx, concurrent.ID, func(operation *persistence.Operation) error {
				if operation.State == "succeeded" || operation.State == "failed" {
					return fmt.Errorf("operation is already final")
				}
				now := time.Now().UTC()
				operation.State = "succeeded"
				operation.Stage = "complete"
				operation.FinishedAt = &now
				return nil
			})
		}()
	}
	var successes, failures int
	for range 2 {
		if err := <-transitionErrors; err != nil {
			failures++
		} else {
			successes++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent transitions: %d successes, %d failures; want 1 and 1", successes, failures)
	}
	transitioned, err := repository.GetOperation(ctx, concurrent.ID)
	if err != nil {
		t.Fatalf("get transitioned operation: %v", err)
	}
	if !transitioned.UpdatedAt.After(oldUpdatedAt) {
		t.Fatalf("transition updated_at = %v; want after %v", transitioned.UpdatedAt, oldUpdatedAt)
	}

	finishedAt := time.Now().UTC()
	operation := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "queued", Stage: "download", InputSnapshot: json.RawMessage(`{"target_identity":"fpcalc:test:1.0:linux:amd64"}`), TargetInstallationID: &installation.ID}
	if err := repository.CreateOperation(ctx, operation); err != nil {
		t.Fatalf("create operation: %v", err)
	}
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		locked, err := repository.GetOperationForUpdate(ctx, tx, operation.ID)
		if err != nil || locked.ID != operation.ID {
			t.Fatalf("lock operation = %#v, %v", locked, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("lock operation transaction: %v", err)
	}
	operation.State = "failed"
	operation.SafeError = stringPointer("safe failure")
	operation.FinishedAt = &finishedAt
	operation.UpdatedAt = oldUpdatedAt
	if err := repository.UpdateOperation(ctx, operation); err != nil {
		t.Fatalf("update operation: %v", err)
	}
	if !operation.UpdatedAt.After(oldUpdatedAt) {
		t.Fatalf("updated operation timestamp = %v; want after %v", operation.UpdatedAt, oldUpdatedAt)
	}
	if err := repository.DismissOperation(ctx, operation.ID); err != nil {
		t.Fatalf("dismiss failed operation: %v", err)
	}
	if _, err := repository.GetOperation(ctx, operation.ID); err == nil {
		t.Fatal("dismissed operation is still present")
	}

	succeeded := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "succeeded", Stage: "complete", InputSnapshot: json.RawMessage(`{"target_identity":"fpcalc:test:2.0:linux:amd64"}`), TargetInstallationID: &installation.ID, FinishedAt: &finishedAt}
	if err := repository.CreateOperation(ctx, succeeded); err != nil {
		t.Fatalf("create succeeded operation: %v", err)
	}
	if err := repository.DeleteSucceededBefore(ctx, finishedAt.Add(time.Second)); err != nil {
		t.Fatalf("cleanup succeeded operation: %v", err)
	}
	if _, err := repository.GetOperation(ctx, succeeded.ID); err == nil {
		t.Fatal("cleaned operation is still present")
	}
}

func createReadyInstallation(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, packageKind, goos, goarch, release string) *persistence.ToolInstallation {
	t.Helper()
	installation := &persistence.ToolInstallation{ID: uuid.New(), PackageKind: packageKind, PlatformGOOS: goos, PlatformGOARCH: goarch, SourceName: "test", ReleaseIdentity: release, RelativePath: packageKind + "/" + release, State: "preparing"}
	if err := repository.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create installation: %v", err)
	}
	if err := repository.MarkInstallationReady(ctx, installation.ID, json.RawMessage(`{"tool":"version"}`), time.Now().UTC()); err != nil {
		t.Fatalf("mark installation ready: %v", err)
	}
	return installation
}

func finishOperation(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, id uuid.UUID) {
	t.Helper()
	if err := repository.TransitionOperation(ctx, id, func(operation *persistence.Operation) error {
		now := time.Now().UTC()
		operation.State = "succeeded"
		operation.Stage = "complete"
		operation.FinishedAt = &now
		return nil
	}); err != nil {
		t.Fatalf("finish operation %s: %v", id, err)
	}
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
	transactionInstallation := createReadyInstallation(t, ctx, repository, "fpcalc", "linux", "amd64", "1.5.1")
	committedSnapshot := json.RawMessage(`{"target_identity":"fpcalc:chromaprint:1.5.1:linux:amd64"}`)
	committedOperation := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "queued", Stage: "download", InputSnapshot: committedSnapshot, TargetInstallationID: &transactionInstallation.ID}
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

	retryInstallation := createReadyInstallation(t, ctx, repository, "ffmpeg", "linux", "amd64", "8.0")
	finishedAt := time.Now().UTC()
	retryOperation := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "failed", Stage: "verify", InputSnapshot: json.RawMessage(`{"target_identity":"ffmpeg:test:8.0:linux:amd64"}`), TargetInstallationID: &retryInstallation.ID, SafeError: stringPointer("safe failure"), FinishedAt: &finishedAt}
	if err := repository.CreateOperation(ctx, retryOperation); err != nil {
		t.Fatalf("create failed operation: %v", err)
	}
	if _, err := database.ExecContext(ctx, "UPDATE tool_installation SET state = 'failed' WHERE id = ?", retryInstallation.ID); err != nil {
		t.Fatalf("mark retry target failed: %v", err)
	}
	oldUpdatedAt := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := database.ExecContext(ctx, "UPDATE operation SET updated_at = ? WHERE id = ?", oldUpdatedAt, retryOperation.ID); err != nil {
		t.Fatalf("set old retry timestamp: %v", err)
	}
	retried, err := repository.RetryOperationAndEnqueue(ctx, retryOperation.ID, client, transactionTestArgs{}, nil)
	if err != nil {
		t.Fatalf("retry operation: %v", err)
	}
	if retried.Attempt != 2 || retried.TargetInstallationID == nil || *retried.TargetInstallationID != retryInstallation.ID || retried.RiverJobID == nil {
		t.Fatalf("retried operation did not preserve its target and create attempt: %#v", retried)
	}
	if !retried.UpdatedAt.After(oldUpdatedAt) {
		t.Fatalf("retry updated_at = %v; want after %v", retried.UpdatedAt, oldUpdatedAt)
	}
	retriedInstallation, err := repository.GetInstallation(ctx, retryInstallation.ID)
	if err != nil || retriedInstallation.State != "preparing" {
		t.Fatalf("retry installation state = %#v, %v; want preparing", retriedInstallation, err)
	}
	installations, err := repository.ListInstallations(ctx, "ffmpeg", "linux", "amd64")
	if err != nil || len(installations) != 2 {
		t.Fatalf("retry installations = %d, %v; want two existing targets", len(installations), err)
	}

	rollbackInstallation := createReadyInstallation(t, ctx, repository, "fpcalc", "linux", "amd64", "1.6.0")
	blockingOperation := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "queued", Stage: "download", InputSnapshot: json.RawMessage(`{"target_identity":"fpcalc:test:1.6.0:linux:amd64"}`), TargetInstallationID: &rollbackInstallation.ID}
	if err := repository.CreateOperation(ctx, blockingOperation); err != nil {
		t.Fatalf("create retry blocker: %v", err)
	}
	failedRetry := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "failed", Stage: "verify", InputSnapshot: json.RawMessage(`{"target_identity":"different-client-value"}`), TargetInstallationID: &rollbackInstallation.ID, SafeError: stringPointer("safe failure"), FinishedAt: &finishedAt}
	if err := repository.CreateOperation(ctx, failedRetry); err != nil {
		t.Fatalf("create failed retry candidate: %v", err)
	}
	if _, err := repository.RetryOperationAndEnqueue(ctx, failedRetry.ID, client, transactionTestArgs{}, nil); err == nil {
		t.Fatal("retry conflicting with an active operation was committed")
	}
	failedAfterRollback, err := repository.GetOperation(ctx, failedRetry.ID)
	if err != nil || failedAfterRollback.State != "failed" || failedAfterRollback.Attempt != 1 || failedAfterRollback.RiverJobID != nil {
		t.Fatalf("rolled-back retry changed operation: %#v, %v", failedAfterRollback, err)
	}
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM river_job WHERE kind = ?", transactionTestArgs{}.Kind()).Scan(&jobs); err != nil {
		t.Fatalf("count River jobs after retry rollback: %v", err)
	}
	if jobs != 2 {
		t.Fatalf("River jobs after retry rollback = %d, want two committed jobs", jobs)
	}

	preparing := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: "1.7.0", RelativePath: "fpcalc/1.7.0",
		State: "preparing", ArtifactIdentities: json.RawMessage(`[{"name":"chromaprint-fpcalc-1.7.0-linux-x86_64.tar.gz"}]`),
	}
	installOperation := &persistence.Operation{
		ID: uuid.New(), Kind: "install", State: "queued", Stage: "queued",
		InputSnapshot:        json.RawMessage(`{"target_identity":"fpcalc:chromaprint:1.7.0:linux:amd64"}`),
		TargetInstallationID: &preparing.ID,
	}
	if err := repository.CreateInstallationOperationAndEnqueue(ctx, preparing, installOperation, client, transactionTestArgs{}, nil); err != nil {
		t.Fatalf("atomically create installation, operation, and job: %v", err)
	}
	if _, err := repository.GetInstallation(ctx, preparing.ID); err != nil {
		t.Fatalf("get atomically created installation: %v", err)
	}
	var jobsAfterInstall int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM river_job WHERE kind = ?", transactionTestArgs{}.Kind()).Scan(&jobsAfterInstall); err != nil {
		t.Fatal(err)
	}
	if jobsAfterInstall != 3 {
		t.Fatalf("River jobs after install start = %d, want 3", jobsAfterInstall)
	}

	duplicateTarget := *preparing
	duplicateTarget.ID = uuid.New()
	duplicateOperation := &persistence.Operation{
		ID: uuid.New(), Kind: "install", State: "queued", Stage: "queued",
		InputSnapshot: installOperation.InputSnapshot, TargetInstallationID: &duplicateTarget.ID,
	}
	if err := repository.CreateInstallationOperationAndEnqueue(ctx, &duplicateTarget, duplicateOperation, client, transactionTestArgs{}, nil); err == nil {
		t.Fatal("duplicate installation identity was enqueued")
	}
	var rolledBackInstallRows int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM tool_installation WHERE id = ?", duplicateTarget.ID).Scan(&rolledBackInstallRows); err != nil {
		t.Fatal(err)
	}
	if rolledBackInstallRows != 0 {
		t.Fatalf("duplicate installation rows = %d, want 0", rolledBackInstallRows)
	}
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM river_job WHERE kind = ?", transactionTestArgs{}.Kind()).Scan(&jobsAfterInstall); err != nil {
		t.Fatal(err)
	}
	if jobsAfterInstall != 3 {
		t.Fatalf("River job persisted after failed install creation: %d", jobsAfterInstall)
	}

	_, err = database.ExecContext(ctx, "INSERT INTO operation (id, kind, state, stage, input_snapshot) VALUES (?, 'install', 'failed', 'download', '{}'::jsonb)", uuid.New())
	if err == nil {
		t.Fatal("failed operation without safe error was accepted")
	}
}

func stringPointer(value string) *string { return &value }
