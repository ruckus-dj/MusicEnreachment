//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestToolsRootUpdateAndInstallEnqueueSerializeWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	settingsRepository := persistence.NewSettingsRepository(database)
	client := openScanEnqueueRiver(t, database)
	const rootA = "/srv/tools-a"
	const rootB = "/srv/tools-b"
	if err := settingsRepository.Set(ctx, "tools_directory", rootA); err != nil {
		t.Fatalf("set initial tools root: %v", err)
	}

	t.Run("root update commits before stale enqueue", func(t *testing.T) {
		installation, operation := toolsRootRaceInstall()
		err := runQueryRace(t, ctx, database,
			"LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE", "LOCK TABLE operation",
			func(ctx context.Context, tx bun.Tx) error {
				_, err := tx.NewInsert().Model(&persistence.AppSetting{Name: "tools_directory", Value: rootB}).
					On("CONFLICT (setting_name) DO UPDATE").Set("setting_value = EXCLUDED.setting_value").Exec(ctx)
				return err
			},
			func(ctx context.Context) error {
				return repository.CreateInstallationOperationAndEnqueue(ctx, rootA, installation, operation, client, service.OperationJobArgs{OperationID: operation.ID}, nil)
			})
		if err == nil {
			t.Fatal("enqueue with a preflight for the old root succeeded")
		}
		assertToolsRootRaceState(t, ctx, database, rootB, installation.ID, operation.ID, 0)
	})

	if err := settingsRepository.Set(ctx, "tools_directory", rootA); err != nil {
		t.Fatalf("restore initial tools root: %v", err)
	}
	t.Run("enqueue commits before root update", func(t *testing.T) {
		installation, operation := toolsRootRaceInstall()
		err := runQueryRace(t, ctx, database,
			"LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE", "LOCK TABLE operation",
			func(ctx context.Context, tx bun.Tx) error {
				result, err := client.InsertTx(ctx, tx.Tx, service.OperationJobArgs{OperationID: operation.ID}, nil)
				if err != nil {
					return err
				}
				operation.RiverJobID = &result.Job.ID
				if err := repository.CreateInstallationWith(ctx, tx, installation); err != nil {
					return err
				}
				return repository.CreateOperationWith(ctx, tx, operation)
			},
			func(ctx context.Context) error {
				return settingsRepository.UpdateRuntime(ctx, rootA, map[string]string{
					"tools_directory":  rootB,
					"output_directory": "/srv/output-b",
				})
			})
		if err == nil {
			t.Fatal("root update succeeded after install enqueue")
		}
		assertToolsRootRaceState(t, ctx, database, rootA, installation.ID, operation.ID, 1)
		if _, found, err := settingsRepository.Get(ctx, "output_directory"); err != nil || found {
			t.Fatalf("runtime update partially persisted output directory: found=%t err=%v", found, err)
		}
	})

	t.Run("database error rolls back all runtime values", func(t *testing.T) {
		if _, err := database.ExecContext(ctx, `
			CREATE FUNCTION reject_test_runtime_setting() RETURNS trigger AS $$
			BEGIN
				IF NEW.setting_name = 'zz_test_reject' THEN RAISE EXCEPTION 'test rejection'; END IF;
				RETURN NEW;
			END; $$ LANGUAGE plpgsql;
			CREATE TRIGGER reject_test_runtime_setting BEFORE INSERT OR UPDATE ON app_setting
			FOR EACH ROW EXECUTE FUNCTION reject_test_runtime_setting();`); err != nil {
			t.Fatalf("create rejecting settings trigger: %v", err)
		}
		t.Cleanup(func() {
			_, _ = database.ExecContext(ctx, `DROP TRIGGER IF EXISTS reject_test_runtime_setting ON app_setting`)
			_, _ = database.ExecContext(ctx, `DROP FUNCTION IF EXISTS reject_test_runtime_setting()`)
		})
		// The tools root stays rootA so the install/operation guard is skipped and
		// the rejected key reaches the trigger; otherwise the guard fails first.
		err := settingsRepository.UpdateRuntime(ctx, rootA, map[string]string{
			"tools_directory":  rootA,
			"output_directory": "/srv/output-rollback",
			"zz_test_reject":   "reject",
		})
		if err == nil {
			t.Fatal("runtime settings write with a rejected key succeeded")
		}
		if !strings.Contains(err.Error(), "test rejection") {
			t.Fatalf("runtime settings write error = %v, want the trigger rejection", err)
		}
		assertToolsRootRaceState(t, ctx, database, rootA, uuid.Nil, uuid.Nil, 0)
		if _, found, err := settingsRepository.Get(ctx, "output_directory"); err != nil || found {
			t.Fatalf("failed runtime update persisted output directory: found=%t err=%v", found, err)
		}
	})
}

func toolsRootRaceInstall() (*persistence.ToolInstallation, *persistence.Operation) {
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "chromaprint", ReleaseIdentity: "1.5.1", RelativePath: "fpcalc/1.5.1",
		State: "preparing", ArtifactIdentities: json.RawMessage(`{"archive":"fpcalc-1.5.1"}`),
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "install", State: "queued", Stage: "queued",
		InputSnapshot:        json.RawMessage(`{"target_identity":"fpcalc:chromaprint:1.5.1:linux:amd64"}`),
		TargetInstallationID: &installation.ID,
	}
	return installation, operation
}

func assertToolsRootRaceState(t *testing.T, ctx context.Context, database *bun.DB, root string, installationID, operationID uuid.UUID, expectedRows int) {
	t.Helper()
	var currentRoot string
	if err := database.QueryRowContext(ctx, "SELECT setting_value FROM app_setting WHERE setting_name = ?", "tools_directory").Scan(&currentRoot); err != nil {
		t.Fatalf("read tools root: %v", err)
	}
	if currentRoot != root {
		t.Fatalf("tools root = %q, want %q", currentRoot, root)
	}
	var installations, operations, jobs int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM tool_installation WHERE id = ?", installationID).Scan(&installations); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM operation WHERE id = ?", operationID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM river_job WHERE args->>'operation_id' = ?", operationID.String()).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if installations != expectedRows || operations != expectedRows || jobs != expectedRows {
		t.Fatalf("install/operation/job rows = %d/%d/%d, want %d each", installations, operations, jobs, expectedRows)
	}
}
