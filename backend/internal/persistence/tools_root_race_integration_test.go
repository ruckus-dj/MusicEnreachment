//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestToolsRootUpdateAndInstallEnqueueSerializeWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
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
			"SELECT pg_advisory_xact_lock(1297371734, 1)", "pg_advisory_xact_lock_shared",
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
			"SELECT pg_advisory_xact_lock_shared(1297371734, 1)", "pg_advisory_xact_lock(",
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
				return settingsRepository.UpdateRuntime(ctx, rootA, "", map[string]string{
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
		if operation.RiverJobID == nil {
			t.Fatal("enqueued installation operation has no River job ID")
		}
		if _, err := database.NewRaw(`DELETE FROM river_job WHERE id=?`, *operation.RiverJobID).Exec(ctx); err != nil {
			t.Fatalf("delete completed race fixture River job: %v", err)
		}
		if _, err := database.NewDelete().Model(operation).WherePK().Exec(ctx); err != nil {
			t.Fatalf("delete completed race fixture operation: %v", err)
		}
		if _, err := database.NewDelete().Model(installation).WherePK().Exec(ctx); err != nil {
			t.Fatalf("delete completed race fixture installation: %v", err)
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
		err := settingsRepository.UpdateRuntime(ctx, rootA, "", map[string]string{
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

	t.Run("move enqueue and install retry reject stale roots", func(t *testing.T) {
		if err := settingsRepository.Set(ctx, "tools_directory", rootB); err != nil {
			t.Fatal(err)
		}
		move := &persistence.Operation{ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
			InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"/srv/tools-a","new_root":"/srv/tools-c"}`)}
		if err := repository.CreateToolsMoveOperationAndEnqueue(ctx, move, client, service.OperationJobArgs{OperationID: move.ID}, nil); err == nil {
			t.Fatal("move enqueue accepted a preflight for the old tools root")
		}

		installation := &persistence.ToolInstallation{
			ID: uuid.New(), PackageKind: "fpcalc", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
			SourceName: "chromaprint", ReleaseIdentity: "1.5.2", RelativePath: "fpcalc/1.5.2", State: "failed",
			ArtifactIdentities: json.RawMessage(`[]`),
		}
		if err := repository.CreateInstallation(ctx, installation); err != nil {
			t.Fatal(err)
		}
		reason := "previous installation failed"
		finished := time.Now().UTC()
		operation := &persistence.Operation{ID: uuid.New(), Kind: "install", State: "failed", Stage: "download", SafeError: &reason, FinishedAt: &finished,
			InputSnapshot:        json.RawMessage(`{"target_identity":"fpcalc:chromaprint:1.5.2:linux:amd64","schema_version":2,"tools_root":"/srv/tools-a"}`),
			TargetInstallationID: &installation.ID}
		if err := repository.CreateOperation(ctx, operation); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.RetryOperationAndEnqueue(ctx, operation.ID, client,
			service.OperationJobArgs{OperationID: operation.ID}, (*river.InsertOpts)(nil)); err == nil {
			t.Fatal("retry accepted an installation snapshot pinned to the old tools root")
		}
		stored, err := repository.GetOperation(ctx, operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.State != "failed" || stored.Attempt != operation.Attempt {
			t.Fatalf("stale retry changed operation state/attempt: %s/%d", stored.State, stored.Attempt)
		}
	})
}

func TestRuntimeRootsRespectActiveMoveAndKeepNonconflictingOutputUpdatesPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	settingsRepository := persistence.NewSettingsRepository(database)
	setupRepository := persistence.NewSetupManagerRepository(database)
	client := openScanEnqueueRiver(t, database)
	const (
		oldRoot = "/srv/tools"
		newRoot = "/srv/tools-next"
		output  = "/srv/output"
	)
	if err := settingsRepository.SetMany(ctx, map[string]string{
		"tools_directory": oldRoot, "output_directory": output, "output_case_sensitive": "true",
	}); err != nil {
		t.Fatal(err)
	}

	for name, candidate := range map[string]string{
		"equal":           newRoot,
		"parent":          "/srv",
		"child":           newRoot + "/nested",
		"filesystem root": "/",
	} {
		t.Run(name, func(t *testing.T) {
			move := &persistence.Operation{ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued", Attempt: 1,
				InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"/srv/tools","new_root":"/srv/tools-next"}`)}
			t.Cleanup(func() {
				_, _ = database.NewDelete().Model(move).WherePK().Exec(ctx)
				_ = settingsRepository.SetMany(ctx, map[string]string{"tools_directory": oldRoot, "output_directory": output, "output_case_sensitive": "true"})
				_, _ = database.NewRaw("DELETE FROM app_setting WHERE setting_name = 'output_unicode_normalization'").Exec(ctx)
			})
			if err := settingsRepository.SetMany(ctx, map[string]string{"tools_directory": oldRoot, "output_directory": output, "output_case_sensitive": "true"}); err != nil {
				t.Fatal(err)
			}
			if _, err := database.NewRaw("DELETE FROM app_setting WHERE setting_name = 'output_unicode_normalization'").Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := database.NewInsert().Model(move).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			err := settingsRepository.UpdateRuntime(ctx, oldRoot, output, map[string]string{
				"output_directory": candidate, "output_case_sensitive": "true", "output_unicode_normalization": "none",
			})
			if err == nil {
				t.Fatalf("conflicting output %q was accepted during move", candidate)
			}
			assertStoredRuntimeValue(t, ctx, database, "output_directory", output)
			assertStoredRuntimeAbsent(t, ctx, database, "output_unicode_normalization")
			if err := settingsRepository.UpdateRuntime(ctx, oldRoot, output, map[string]string{
				"output_directory": "/mnt/archive/output", "output_case_sensitive": "true", "output_unicode_normalization": "none",
			}); err != nil {
				t.Fatalf("nonconflicting output update during move: %v", err)
			}
			assertStoredRuntimeValue(t, ctx, database, "output_directory", "/mnt/archive/output")
			if _, err := database.NewDelete().Model(move).WherePK().Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if err := settingsRepository.UpdateRuntime(ctx, oldRoot, "/mnt/archive/output", map[string]string{
				"output_directory": output, "output_case_sensitive": "true", "output_unicode_normalization": "none",
			}); err != nil {
				t.Fatal(err)
			}
		})
	}

	for name, candidate := range map[string]string{"equal": newRoot, "parent": "/srv", "child": newRoot + "/nested", "filesystem root": "/"} {
		t.Run("move admission observes output committed first/"+name, func(t *testing.T) {
			move := &persistence.Operation{ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
				InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"/srv/tools","new_root":"/srv/tools-next"}`)}
			t.Cleanup(func() {
				_, _ = database.NewRaw("DELETE FROM river_job WHERE args->>'operation_id' = ?", move.ID.String()).Exec(ctx)
				_, _ = database.NewDelete().Model(move).WherePK().Exec(ctx)
				_ = settingsRepository.Set(ctx, "output_directory", output)
			})
			err := runQueryRace(t, ctx, database,
				"SELECT pg_advisory_xact_lock(1297371734, 1)", "pg_advisory_xact_lock(",
				func(ctx context.Context, tx bun.Tx) error {
					_, err := tx.NewInsert().Model(&persistence.AppSetting{Name: "output_directory", Value: candidate}).
						On("CONFLICT (setting_name) DO UPDATE").Set("setting_value = EXCLUDED.setting_value").Exec(ctx)
					return err
				},
				func(ctx context.Context) error {
					return setupRepository.CreateToolsMoveOperationAndEnqueue(ctx, move, client, service.OperationJobArgs{OperationID: move.ID}, nil)
				})
			if err == nil {
				t.Fatalf("move admission accepted output %q overlapping its roots", candidate)
			}
			assertNoMoveAdmission(t, ctx, database, move.ID)
			assertStoredRuntimeValue(t, ctx, database, "output_case_sensitive", "true")
			assertStoredRuntimeAbsent(t, ctx, database, "output_unicode_normalization")
			if err := settingsRepository.Set(ctx, "output_directory", output); err != nil {
				t.Fatal(err)
			}
		})
	}

	for name, candidate := range map[string]string{"equal": newRoot, "parent": "/srv", "child": newRoot + "/nested", "filesystem root": "/"} {
		t.Run("output update observes move admission committed first/"+name, func(t *testing.T) {
			move := &persistence.Operation{ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
				InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"/srv/tools","new_root":"/srv/tools-next"}`)}
			t.Cleanup(func() {
				_, _ = database.NewRaw("DELETE FROM river_job WHERE args->>'operation_id' = ?", move.ID.String()).Exec(ctx)
				_, _ = database.NewDelete().Model(move).WherePK().Exec(ctx)
				_ = settingsRepository.Set(ctx, "output_directory", output)
			})
			err := runQueryRace(t, ctx, database,
				"SELECT pg_advisory_xact_lock(1297371734, 1)", "pg_advisory_xact_lock(",
				func(ctx context.Context, tx bun.Tx) error {
					result, err := client.InsertTx(ctx, tx.Tx, service.OperationJobArgs{OperationID: move.ID}, nil)
					if err != nil {
						return err
					}
					move.RiverJobID = &result.Job.ID
					return setupRepository.CreateOperationWith(ctx, tx, move)
				},
				func(ctx context.Context) error {
					return settingsRepository.UpdateRuntime(ctx, oldRoot, output, map[string]string{
						"output_directory": candidate, "output_case_sensitive": "true", "output_unicode_normalization": "none",
					})
				})
			if err == nil {
				t.Fatalf("conflicting output update %q committed after move admission", candidate)
			}
			assertStoredRuntimeValue(t, ctx, database, "output_directory", output)
			assertStoredRuntimeAbsent(t, ctx, database, "output_unicode_normalization")
			if _, err := database.NewDelete().Model(move).WherePK().Exec(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("direct tools root update without installations", func(t *testing.T) {
		if err := settingsRepository.UpdateRuntime(ctx, oldRoot, output, map[string]string{"tools_directory": "/srv/tools-direct"}); err != nil {
			t.Fatalf("direct tools root change with no installations: %v", err)
		}
		assertStoredRuntimeValue(t, ctx, database, "tools_directory", "/srv/tools-direct")
	})

	t.Run("failed move target is revalidated on retry", func(t *testing.T) {
		const retryTarget = "/srv/retry-target"
		reason := "previous move failed"
		finished := time.Now().UTC()
		operation := &persistence.Operation{ID: uuid.New(), Kind: "move_tools_root", State: "failed", Stage: "copy",
			SafeError: &reason, FinishedAt: &finished,
			InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"/srv/tools-direct","new_root":"/srv/retry-target"}`)}
		if err := setupRepository.CreateOperation(ctx, operation); err != nil {
			t.Fatal(err)
		}
		if err := settingsRepository.Set(ctx, "output_directory", retryTarget); err != nil {
			t.Fatal(err)
		}
		if _, err := setupRepository.RetryOperationAndEnqueue(ctx, operation.ID, client, service.OperationJobArgs{OperationID: operation.ID}, nil); err == nil {
			t.Fatal("retry admitted a target that now overlaps output")
		}
		stored, err := setupRepository.GetOperation(ctx, operation.ID)
		if err != nil || stored.State != "failed" || stored.Attempt != 1 {
			t.Fatalf("rejected retry changed failed operation: operation=%#v err=%v", stored, err)
		}
		var jobs int
		if err := database.NewRaw("SELECT count(*) FROM river_job WHERE args->>'operation_id' = ?", operation.ID.String()).Scan(ctx, &jobs); err != nil || jobs != 0 {
			t.Fatalf("rejected retry left River jobs=%d err=%v", jobs, err)
		}
		if err := settingsRepository.Set(ctx, "output_directory", "/mnt/archive/output"); err != nil {
			t.Fatal(err)
		}
		retried, err := setupRepository.RetryOperationAndEnqueue(ctx, operation.ID, client, service.OperationJobArgs{OperationID: operation.ID}, nil)
		if err != nil {
			t.Fatalf("retry after conflict cleared: %v", err)
		}
		if retried.State != "queued" || retried.Attempt != 2 {
			t.Fatalf("retry result = %s/%d, want queued/2", retried.State, retried.Attempt)
		}
		if _, err := database.NewRaw("DELETE FROM river_job WHERE args->>'operation_id' = ?", operation.ID.String()).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := database.NewDelete().Model(operation).WherePK().Exec(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestGenericSettingsWritesSerializeActiveSelectionsAndCanonicalizeRootsPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	settingsRepository := persistence.NewSettingsRepository(database)

	t.Run("active selection waits for package mutation", func(t *testing.T) {
		err := runQueryRace(t, ctx, database,
			"SELECT pg_advisory_xact_lock(1297371734, 102)", "pg_advisory_xact_lock(",
			func(context.Context, bun.Tx) error { return nil },
			func(ctx context.Context) error {
				return settingsRepository.Set(ctx, "active_fpcalc_installation_id", uuid.NewString())
			})
		if err != nil {
			t.Fatalf("active selection after package mutation: %v", err)
		}
	})

	t.Run("active selection waits for move gate", func(t *testing.T) {
		err := runQueryRace(t, ctx, database,
			"SELECT pg_advisory_xact_lock(1297371734, 1)", "pg_advisory_xact_lock_shared",
			func(context.Context, bun.Tx) error { return nil },
			func(ctx context.Context) error {
				return settingsRepository.Set(ctx, "active_ffmpeg_installation_id", uuid.NewString())
			})
		if err != nil {
			t.Fatalf("active selection after move gate: %v", err)
		}
	})

	t.Run("symlink aliases cannot bypass generic root overlap validation", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, "tools")
		alias := filepath.Join(base, "alias")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, alias); err != nil {
			t.Fatal(err)
		}
		if err := settingsRepository.SetMany(ctx, map[string]string{
			"tools_directory": target, "output_directory": filepath.Join(base, "output"),
		}); err != nil {
			t.Fatal(err)
		}
		if err := settingsRepository.Set(ctx, "output_directory", alias); err == nil {
			t.Fatal("output symlink alias overlapping tools root was accepted")
		}
		if err := settingsRepository.SetMany(ctx, map[string]string{
			"tools_directory": filepath.Join(base, "output"), "output_directory": target,
		}); err != nil {
			t.Fatal(err)
		}
		if err := settingsRepository.Set(ctx, "tools_directory", alias); err == nil {
			t.Fatal("tools symlink alias overlapping output root was accepted")
		}
		canonicalOutput, err := filepath.EvalSymlinks(base)
		if err != nil {
			t.Fatal(err)
		}
		assertStoredRuntimeValue(t, ctx, database, "tools_directory", filepath.Join(canonicalOutput, "output"))
		canonicalTarget, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatal(err)
		}
		assertStoredRuntimeValue(t, ctx, database, "output_directory", canonicalTarget)
	})
}

func assertStoredRuntimeValue(t *testing.T, ctx context.Context, database *bun.DB, name, expected string) {
	t.Helper()
	var actual string
	if err := database.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", name).Scan(ctx, &actual); err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if actual != expected {
		t.Fatalf("%s = %q, want %q", name, actual, expected)
	}
}

func assertStoredRuntimeAbsent(t *testing.T, ctx context.Context, database *bun.DB, name string) {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM app_setting WHERE setting_name = ?", name).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%s persisted after rejected runtime update", name)
	}
}

func assertNoMoveAdmission(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) {
	t.Helper()
	var operations, jobs int
	if err := database.NewRaw("SELECT count(*) FROM operation WHERE id = ?", operationID).Scan(ctx, &operations); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw("SELECT count(*) FROM river_job WHERE args->>'operation_id' = ?", operationID.String()).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if operations != 0 || jobs != 0 {
		t.Fatalf("rejected move left operation/job rows: %d/%d", operations, jobs)
	}
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
