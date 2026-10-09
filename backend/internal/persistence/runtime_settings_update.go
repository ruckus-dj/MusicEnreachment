package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
)

// validateRuntimeSettingValues mirrors the mutable values accepted by the
// settings registry. Keep this at the persistence boundary so transaction
// callbacks cannot write unvalidated or unrelated app settings.
func validateRuntimeSettingValues(values map[string]string) error {
	for name, value := range values {
		switch name {
		case "tools_directory", "output_directory":
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
			if value != "source" && value != "mka" {
				return fmt.Errorf("publication format must be 'source' or 'mka'")
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

func validateOutputAgainstActiveMovesSQL(ctx context.Context, tx *sql.Tx, outputRoot string, caseSensitive bool) error {
	if outputRoot == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT input_snapshot FROM operation WHERE kind='move_tools_root' AND state IN ('queued','running') ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read active tools root moves: %w", err)
	}
	defer func() { _ = rows.Close() }()
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
