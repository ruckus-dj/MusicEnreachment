package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"

	appsettings "github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// validateRuntimeSettingValues mirrors the mutable values accepted by the
// settings registry. Keep this at the persistence boundary so transaction
// callbacks cannot write unvalidated or unrelated app settings.
func validateRuntimeSettingValues(values map[string]string) error {
	for name, value := range values {
		switch name {
		case "tools_directory", "output_directory", "work_directory":
			if value != "" && (!filepath.IsAbs(value) || filepath.Clean(value) != value) {
				return fmt.Errorf("runtime directory is not a normalized absolute path")
			}
		case "output_case_sensitive":
			if _, err := strconv.ParseBool(value); err != nil {
				return fmt.Errorf("invalid output case sensitivity: %w", err)
			}
		case "output_unicode_normalization":
			switch value {
			case "none", "nfc", "nfd", "unknown":
			default:
				return fmt.Errorf("invalid filesystem Unicode normalization")
			}
		case "publication_format":
			if value != "source" && value != "mka" && value != "m4a" {
				return fmt.Errorf("publication format must be 'source', 'mka', or 'm4a'")
			}
		case "publication_path_template":
			if value == "" {
				return fmt.Errorf("publication path template must not be empty")
			}
		case "publication_remove_sources":
			if _, err := strconv.ParseBool(value); err != nil {
				return fmt.Errorf("invalid publication source removal setting: %w", err)
			}
		case "source_file_concurrency":
			concurrency, err := strconv.Atoi(value)
			if err != nil || concurrency < 1 {
				return fmt.Errorf("source file concurrency must be a positive integer")
			}
		default:
			return fmt.Errorf("unsupported runtime setting %q", name)
		}
	}
	return nil
}

// validateRuntimeUpdateSQL applies the root-move and overlap checks used by
// SettingsRepository.UpdateRuntime using the already-held output-reset
// connection. It intentionally does not acquire either coordination gate.
func validateRuntimeUpdateSQL(ctx context.Context, tx *sql.Tx, expectedToolsRoot, expectedOutputRoot, newOutputRoot string, values map[string]string) error {
	if err := validateRuntimeSettingValues(values); err != nil {
		return err
	}
	var toolsRoot, outputRoot, caseValue string
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name='tools_directory'), ''),
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name='output_directory'), ''),
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name='output_case_sensitive'), 'true')
		FROM app_setting WHERE setting_name IN ('tools_directory','output_directory','output_case_sensitive')`).Scan(&toolsRoot, &outputRoot, &caseValue); err != nil {
		return fmt.Errorf("read runtime roots for output reset: %w", err)
	}
	if toolsRoot != expectedToolsRoot || outputRoot != expectedOutputRoot {
		return fmt.Errorf("runtime directories changed since output reset was prepared")
	}
	nextToolsRoot := toolsRoot
	if candidate, ok := values["tools_directory"]; ok {
		nextToolsRoot = candidate
	}
	caseSensitive := caseValue != "false"
	if candidate, ok := values["output_case_sensitive"]; ok {
		caseSensitive = candidate != "false"
	}
	if err := validateRuntimeRootPair(nextToolsRoot, newOutputRoot, caseSensitive); err != nil {
		return err
	}
	if nextToolsRoot != toolsRoot {
		var activeMove bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation WHERE kind='move_tools_root' AND state IN ('queued','running'))`).Scan(&activeMove); err != nil {
			return fmt.Errorf("check active tools root move: %w", err)
		}
		if activeMove {
			return fmt.Errorf("tools directory cannot change while a tools root move is active")
		}
		var installationID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM tool_installation LIMIT 1 FOR UPDATE`).Scan(&installationID)
		if err == nil {
			return fmt.Errorf("tools directory cannot change while installations exist; use the move operation")
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check tool installations: %w", err)
		}
		var operationID string
		err = tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE kind='install' AND state IN ('queued','running') LIMIT 1`).Scan(&operationID)
		if err == nil {
			return fmt.Errorf("tools directory cannot change while tools operations are active")
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check active tools operations: %w", err)
		}
	}
	if err := validateOutputAgainstActiveMovesSQL(ctx, tx, newOutputRoot, caseSensitive); err != nil {
		return err
	}
	return nil
}

// updateRuntimeSettingsSQL contains the complete transactional runtime
// validation and write path shared by ordinary runtime updates and the
// coordinated same-root output-semantics refresh. Callers hold work-directory,
// output-admission (when required), and tools-move coordination gates already.
func updateRuntimeSettingsSQL(ctx context.Context, tx *sql.Tx, expectedToolsRoot, expectedOutputRoot string, expected *appsettings.RuntimeSettingsExpected, values map[string]string) error {
	if err := validateRuntimeSettingValues(values); err != nil {
		return err
	}
	var toolsRoot, outputRoot, caseValue string
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name='tools_directory'), ''),
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name='output_directory'), ''),
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name='output_case_sensitive'), 'true')
		FROM app_setting WHERE setting_name IN ('tools_directory','output_directory','output_case_sensitive')`).
		Scan(&toolsRoot, &outputRoot, &caseValue); err != nil {
		return fmt.Errorf("read runtime roots: %w", err)
	}
	if toolsRoot != expectedToolsRoot {
		if expected != nil && expected.ToolsDirectory != nil {
			return fmt.Errorf("%w: %s changed since this draft was opened", appsettings.ErrSettingsConflict, appsettings.ToolsDirectoryKey)
		}
		return fmt.Errorf("tools directory changed since runtime update was prepared")
	}
	if outputRoot != expectedOutputRoot {
		if expected != nil && expected.OutputDirectory != nil {
			return fmt.Errorf("%w: %s changed since this draft was opened", appsettings.ErrSettingsConflict, appsettings.OutputDirectoryKey)
		}
		return fmt.Errorf("output directory changed since runtime update was prepared")
	}
	if expected != nil {
		if err := checkRuntimeSettingsExpected(ctx, tx, *expected); err != nil {
			return err
		}
	}
	nextToolsRoot, nextOutputRoot := toolsRoot, outputRoot
	if value, ok := values[appsettings.ToolsDirectoryKey]; ok {
		nextToolsRoot = value
	}
	if value, ok := values[appsettings.OutputDirectoryKey]; ok {
		nextOutputRoot = value
	}
	if _, outputChanges := values[appsettings.OutputDirectoryKey]; outputChanges && outputRootChangeRequiresReset(outputRoot, nextOutputRoot) {
		return fmt.Errorf("output directory changes must use the output reset workflow")
	}
	caseSensitive := caseValue != "false"
	if value, ok := values[appsettings.OutputCaseSensitiveKey]; ok {
		caseSensitive = value != "false"
	}
	if nextToolsRoot != toolsRoot {
		var activeMove bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation WHERE kind='move_tools_root' AND state IN ('queued','running'))`).Scan(&activeMove); err != nil {
			return fmt.Errorf("check active tools root move: %w", err)
		}
		if activeMove {
			return fmt.Errorf("tools directory cannot change while a tools root move is active")
		}
		if err := lockRuntimePackageActivationsSQL(ctx, tx, activeSettingPackages(values)); err != nil {
			return err
		}
		var installationID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM tool_installation LIMIT 1 FOR UPDATE`).Scan(&installationID)
		if err == nil {
			return fmt.Errorf("tools directory cannot change while installations exist; use the move operation")
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check tool installations: %w", err)
		}
		var operationID string
		err = tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE kind='install' AND state IN ('queued','running') LIMIT 1`).Scan(&operationID)
		if err == nil {
			return fmt.Errorf("tools directory cannot change while tools operations are active")
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check active tools operations: %w", err)
		}
	}
	if err := validateRuntimeRootPair(nextToolsRoot, nextOutputRoot, caseSensitive); err != nil {
		return err
	}
	var workRoot string
	if value, changed := values[appsettings.WorkDirectoryKey]; changed {
		workRoot = value
	} else if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT setting_value FROM app_setting WHERE setting_name='work_directory'), '')`).Scan(&workRoot); err != nil {
		return fmt.Errorf("read work directory: %w", err)
	}
	if workRoot != "" {
		if err := validateWorkDirectoryAgainstManagedRootsSQL(ctx, tx, workRoot, nextToolsRoot, nextOutputRoot, caseSensitive); err != nil {
			return err
		}
		if err := validateWorkDirectoryAgainstActiveMovesSQL(ctx, tx, workRoot, caseSensitive); err != nil {
			return err
		}
	}
	if workRoot, changed := values[appsettings.WorkDirectoryKey]; changed {
		var currentWork string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT setting_value FROM app_setting WHERE setting_name='work_directory'), '')`).Scan(&currentWork); err != nil {
			return fmt.Errorf("read current work directory: %w", err)
		}
		if currentWork != workRoot {
			var retained bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM source_analysis_artifact) OR EXISTS(
				SELECT 1 FROM operation WHERE kind='analyze_source' AND state IN ('queued','running'))`).Scan(&retained); err != nil {
				return fmt.Errorf("check staged work directory ownership: %w", err)
			}
			if retained {
				return fmt.Errorf("work directory cannot change while staged copies or operations exist")
			}
		}
		if err := validateWorkDirectoryAgainstManagedRootsSQL(ctx, tx, workRoot, nextToolsRoot, nextOutputRoot, caseSensitive); err != nil {
			return err
		}
		if err := validateWorkDirectoryAgainstActiveMovesSQL(ctx, tx, workRoot, caseSensitive); err != nil {
			return err
		}
	}
	if err := validateOutputAgainstActiveMovesSQL(ctx, tx, nextOutputRoot, caseSensitive); err != nil {
		return err
	}
	if value, changed := values[appsettings.PublicationRemoveSourcesKey]; changed && value == "false" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM publication_source_cleanup_request`); err != nil {
			return fmt.Errorf("discard pending publication source cleanup requests: %w", err)
		}
	}
	return setRuntimeValuesSQL(ctx, tx, values)
}

func lockRuntimePackageActivationsSQL(ctx context.Context, tx *sql.Tx, packageKinds []string) error {
	keys := make(map[string]int32, len(packageKinds))
	for _, kind := range packageKinds {
		switch kind {
		case "ffmpeg":
			keys[kind] = toolsPackageLockBase + 1
		case "fpcalc":
			keys[kind] = toolsPackageLockBase + 2
		default:
			return fmt.Errorf("unsupported package kind %q", kind)
		}
	}
	kinds := make([]string, 0, len(keys))
	for kind := range keys {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	for _, kind := range kinds {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, toolsCoordinationNamespace, keys[kind]); err != nil {
			return fmt.Errorf("lock active installations: %w", err)
		}
	}
	return nil
}

func lockPublicationSourceCleanupPolicySQL(ctx context.Context, tx *sql.Tx, shared bool) error {
	lock := "pg_advisory_xact_lock"
	if shared {
		lock = "pg_advisory_xact_lock_shared"
	}
	if _, err := tx.ExecContext(ctx, "SELECT "+lock+"($1, $2)", toolsCoordinationNamespace, publicationSourceCleanupPolicyGateKey); err != nil {
		return fmt.Errorf("lock publication source removal policy gate: %w", err)
	}
	return nil
}

func validateWorkDirectoryAgainstManagedRootsSQL(ctx context.Context, tx *sql.Tx, workRoot, toolsRoot, outputRoot string, caseSensitive bool) (resultErr error) {
	for _, managed := range []struct{ name, path string }{{"tools", toolsRoot}, {"output", outputRoot}} {
		if managed.path != "" && persistedPathsOverlap(workRoot, managed.path, caseSensitive) {
			return fmt.Errorf("work directory overlaps with %s directory", managed.name)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT configured_path FROM source_root ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read source roots for work directory: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close source roots for work directory: %w", err))
		}
	}()
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return fmt.Errorf("read source root for work directory: %w", err)
		}
		if persistedPathsOverlap(workRoot, root, caseSensitive) {
			return fmt.Errorf("work directory overlaps with a source root")
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read source roots for work directory: %w", err)
	}
	return nil
}

func validateWorkDirectoryAgainstActiveMovesSQL(ctx context.Context, tx *sql.Tx, workRoot string, caseSensitive bool) (resultErr error) {
	rows, err := tx.QueryContext(ctx, `SELECT input_snapshot FROM operation WHERE kind='move_tools_root' AND state IN ('queued','running')`)
	if err != nil {
		return fmt.Errorf("read active tools move roots: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close active tools move roots: %w", err))
		}
	}()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return fmt.Errorf("read active tools move roots: %w", err)
		}
		var roots struct {
			OldRoot string `json:"old_root"`
			NewRoot string `json:"new_root"`
		}
		if err := json.Unmarshal(raw, &roots); err != nil {
			return fmt.Errorf("decode active tools move roots: %w", err)
		}
		for _, root := range []string{roots.OldRoot, roots.NewRoot} {
			if root != "" && persistedPathsOverlap(workRoot, root, caseSensitive) {
				return fmt.Errorf("work directory overlaps with an active tools move root")
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read active tools move roots: %w", err)
	}
	return nil
}

func validateOutputAgainstActiveMovesSQL(ctx context.Context, tx *sql.Tx, outputRoot string, caseSensitive bool) (resultErr error) {
	if outputRoot == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT input_snapshot FROM operation WHERE kind='move_tools_root' AND state IN ('queued','running') ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read active tools root moves: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close active tools root moves: %w", err))
		}
	}()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return fmt.Errorf("read active tools root move: %w", err)
		}
		var snapshot struct {
			OldRoot string `json:"old_root"`
			NewRoot string `json:"new_root"`
		}
		if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.OldRoot == "" || snapshot.NewRoot == "" {
			return fmt.Errorf("active tools root move has an invalid snapshot")
		}
		if persistedPathsOverlap(outputRoot, snapshot.OldRoot, caseSensitive) || persistedPathsOverlap(outputRoot, snapshot.NewRoot, caseSensitive) {
			return fmt.Errorf("output directory overlaps with an active tools root move")
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read active tools root moves: %w", err)
	}
	return nil
}

func setRuntimeValuesSQL(ctx context.Context, tx *sql.Tx, values map[string]string) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value,updated_at)
			VALUES ($1,$2,now()) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value,updated_at=now()`, key, values[key]); err != nil {
			return fmt.Errorf("update runtime setting %q: %w", key, err)
		}
	}
	return nil
}
