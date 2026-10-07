package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// AppSetting is the only persistence model for runtime configuration.
type AppSetting struct {
	bun.BaseModel `bun:"table:app_setting"`
	Name          string    `bun:"setting_name,pk"`
	Value         string    `bun:"setting_value"`
	UpdatedAt     time.Time `bun:"updated_at"`
}

type SettingsRepository struct{ db *bun.DB }

func NewSettingsRepository(db *bun.DB) *SettingsRepository { return &SettingsRepository{db: db} }

func (r *SettingsRepository) Get(ctx context.Context, name string) (string, bool, error) {
	var setting AppSetting
	err := r.db.NewSelect().Model(&setting).Where("setting_name = ?", name).Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get setting %q: %w", name, err)
	}
	return setting.Value, true, nil
}

func (r *SettingsRepository) Set(ctx context.Context, name, value string) error {
	return r.SetMany(ctx, map[string]string{name: value})
}

func (r *SettingsRepository) SetMany(ctx context.Context, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	values = cloneSettingValues(values)
	_, toolsChange := values["tools_directory"]
	_, outputChange := values["output_directory"]
	_, caseChange := values["output_case_sensitive"]
	_, unicodeChange := values["output_unicode_normalization"]
	runtimeChange := toolsChange || outputChange || caseChange || unicodeChange
	expectedTools, expectedOutput := "", ""
	expectedToolsCanonical, expectedOutputCanonical := "", ""
	if runtimeChange {
		if err := canonicalizeRuntimeRootValues(values); err != nil {
			return err
		}
		var err error
		expectedTools, expectedOutput, err = r.readRuntimeRoots(ctx)
		if err != nil {
			return err
		}
		expectedToolsCanonical, err = canonicalizeOptionalPersistedPath(expectedTools)
		if err != nil {
			return fmt.Errorf("tools_directory: %w", err)
		}
		expectedOutputCanonical, err = canonicalizeOptionalPersistedPath(expectedOutput)
		if err != nil {
			return fmt.Errorf("output_directory: %w", err)
		}
	}
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		packages := activeSettingPackages(values)
		if toolsChange {
			packages = []string{"ffmpeg", "fpcalc"}
		}
		if runtimeChange {
			if err := lockToolsMoveGateExclusive(ctx, tx); err != nil {
				return fmt.Errorf("lock tools move gate: %w", err)
			}
		} else if len(packages) > 0 {
			if err := lockToolsMoveGateShared(ctx, tx); err != nil {
				return fmt.Errorf("lock tools move gate: %w", err)
			}
		}
		if len(packages) > 0 {
			if err := lockInstallationPackages(ctx, tx, packages...); err != nil {
				return fmt.Errorf("lock active installations: %w", err)
			}
		}
		if runtimeChange {
			storedTools, storedOutput, caseSensitive, err := readRuntimeRoots(ctx, tx)
			if err != nil {
				return err
			}
			if storedTools != expectedTools || storedOutput != expectedOutput {
				return fmt.Errorf("runtime directories changed while settings were being validated")
			}
			currentTools, currentOutput := expectedToolsCanonical, expectedOutputCanonical
			nextTools, nextOutput := currentTools, currentOutput
			if value, ok := values["tools_directory"]; ok {
				nextTools = value
			}
			if value, ok := values["output_directory"]; ok {
				nextOutput = value
			}
			caseSensitive = runtimeCaseSensitivity(values, caseSensitive)
			if toolsChange && nextTools != currentTools {
				if err := rejectActiveRootMove(ctx, tx); err != nil {
					return err
				}
				var installationID string
				err = tx.NewRaw("SELECT id FROM tool_installation LIMIT 1 FOR UPDATE").Scan(ctx, &installationID)
				if err == nil {
					return fmt.Errorf("tools directory cannot change while installations exist; use the move operation")
				}
				if err != sql.ErrNoRows {
					return fmt.Errorf("check tool installations: %w", err)
				}
				var operationID string
				err = tx.NewRaw("SELECT id FROM operation WHERE kind='install' AND state IN ('queued','running') LIMIT 1").Scan(ctx, &operationID)
				if err == nil {
					return fmt.Errorf("tools directory cannot change while tools operations are active")
				}
				if err != sql.ErrNoRows {
					return fmt.Errorf("check active tools operations: %w", err)
				}
			}
			if err := validateRuntimeRootPair(nextTools, nextOutput, caseSensitive); err != nil {
				return err
			}
			if err := validateOutputAgainstActiveMoves(ctx, tx, nextOutput, caseSensitive); err != nil {
				return err
			}
		}
		if err := lockMusicBrainzIfNeeded(ctx, tx, values); err != nil {
			return err
		}
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			value := values[name]
			_, err := tx.NewInsert().Model(&AppSetting{Name: name, Value: value}).
				On("CONFLICT (setting_name) DO UPDATE").
				Set("setting_value = EXCLUDED.setting_value").
				Set("updated_at = now()").Exec(ctx)
			if err != nil {
				return fmt.Errorf("set setting %q: %w", name, err)
			}
		}
		return nil
	})
}

func cloneSettingValues(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}

func canonicalizeRuntimeRootValues(values map[string]string) error {
	for _, name := range []string{"tools_directory", "output_directory"} {
		value, ok := values[name]
		if !ok || value == "" {
			continue
		}
		canonical, err := canonicalizePersistedPath(value)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		values[name] = canonical
	}
	return nil
}

func canonicalizeOptionalPersistedPath(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return canonicalizePersistedPath(value)
}

// canonicalizePersistedPath resolves symlinks in the existing path prefix before
// a path is admitted. It runs before opening the settings transaction.
func canonicalizePersistedPath(value string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("runtime directory is not an absolute path")
	}
	path := filepath.Clean(value)
	for existing := path; ; existing = filepath.Dir(existing) {
		if resolved, err := filepath.EvalSymlinks(existing); err == nil {
			relative, relativeErr := filepath.Rel(existing, path)
			if relativeErr == nil && relative != "." {
				return filepath.Join(resolved, relative), nil
			}
			return resolved, nil
		}
		if existing == filepath.Dir(existing) {
			break
		}
	}
	return path, nil
}

func (r *SettingsRepository) readRuntimeRoots(ctx context.Context) (string, string, error) {
	var roots struct {
		Tools  string `bun:"tools_directory"`
		Output string `bun:"output_directory"`
	}
	if err := r.db.NewRaw(`SELECT
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name = 'tools_directory'), '') AS tools_directory,
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name = 'output_directory'), '') AS output_directory
		FROM app_setting WHERE setting_name IN ('tools_directory', 'output_directory')`).Scan(ctx, &roots); err != nil {
		return "", "", fmt.Errorf("read runtime directories before settings update: %w", err)
	}
	return roots.Tools, roots.Output, nil
}

func activeSettingPackages(values map[string]string) []string {
	packages := make([]string, 0, 2)
	if _, ok := values["active_ffmpeg_installation_id"]; ok {
		packages = append(packages, "ffmpeg")
	}
	if _, ok := values["active_fpcalc_installation_id"]; ok {
		packages = append(packages, "fpcalc")
	}
	return packages
}

func lockMusicBrainzIfNeeded(ctx context.Context, tx bun.Tx, values map[string]string) error {
	for _, name := range []string{"musicbrainz_mode", "musicbrainz_base_url", "musicbrainz_config_identity", "musicbrainz_verified_at"} {
		if _, present := values[name]; present {
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "musicbrainz-config"); err != nil {
				return fmt.Errorf("lock MusicBrainz configuration: %w", err)
			}
			break
		}
	}
	return nil
}

func rejectActiveRootMove(ctx context.Context, tx bun.Tx) error {
	active, err := activeToolsMove(ctx, tx)
	if err != nil {
		return fmt.Errorf("check active tools root move: %w", err)
	}
	if active {
		return fmt.Errorf("tools directory cannot change while a tools root move is active")
	}
	return nil
}

// UpdateRuntime serializes a direct tools-root change with every tools
// mutation. expectedToolsRoot is empty when no root is currently configured.
func (r *SettingsRepository) UpdateRuntime(ctx context.Context, expectedToolsRoot, expectedOutputRoot string, values map[string]string) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateExclusive(ctx, tx); err != nil {
			return fmt.Errorf("lock tools operations: %w", err)
		}
		currentTools, currentOutput, caseSensitive, err := readRuntimeRoots(ctx, tx)
		if err != nil {
			return err
		}
		if currentTools != expectedToolsRoot {
			return fmt.Errorf("tools directory changed since runtime update was prepared")
		}
		if currentOutput != expectedOutputRoot {
			return fmt.Errorf("output directory changed since runtime update was prepared")
		}
		nextTools, nextOutput := currentTools, currentOutput
		if value, ok := values["tools_directory"]; ok {
			nextTools = value
		}
		if value, ok := values["output_directory"]; ok {
			nextOutput = value
		}
		caseSensitive = runtimeCaseSensitivity(values, caseSensitive)
		if nextTools != currentTools {
			if err := rejectActiveRootMove(ctx, tx); err != nil {
				return err
			}
			if err := lockInstallationPackages(ctx, tx, activeSettingPackages(values)...); err != nil {
				return fmt.Errorf("lock active installations: %w", err)
			}
			var installationID string
			err := tx.NewRaw("SELECT id FROM tool_installation LIMIT 1 FOR UPDATE").Scan(ctx, &installationID)
			if err == nil {
				return fmt.Errorf("tools directory cannot change while installations exist; use the move operation")
			}
			if err != sql.ErrNoRows {
				return fmt.Errorf("check tool installations: %w", err)
			}
			var operationID string
			err = tx.NewRaw("SELECT id FROM operation WHERE kind = 'install' AND state IN ('queued', 'running') LIMIT 1").Scan(ctx, &operationID)
			if err == nil {
				return fmt.Errorf("tools directory cannot change while tools operations are active")
			}
			if err != sql.ErrNoRows {
				return fmt.Errorf("check active tools operations: %w", err)
			}
		}
		if err := validateRuntimeRootPair(nextTools, nextOutput, caseSensitive); err != nil {
			return err
		}
		if err := validateOutputAgainstActiveMoves(ctx, tx, nextOutput, caseSensitive); err != nil {
			return err
		}
		return setManyTx(ctx, tx, values)
	})
}

func runtimeCaseSensitivity(values map[string]string, current bool) bool {
	if value, ok := values["output_case_sensitive"]; ok {
		return value != "false"
	}
	return current
}

func readRuntimeRoots(ctx context.Context, tx bun.Tx) (toolsRoot, outputRoot string, caseSensitive bool, resultErr error) {
	caseSensitive = true
	for _, setting := range []struct {
		name   string
		target *string
	}{{"tools_directory", &toolsRoot}, {"output_directory", &outputRoot}} {
		err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", setting.name).Scan(ctx, setting.target)
		if err != nil && err != sql.ErrNoRows {
			return "", "", false, fmt.Errorf("read %s: %w", setting.name, err)
		}
	}
	var storedCase string
	err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "output_case_sensitive").Scan(ctx, &storedCase)
	if err != nil && err != sql.ErrNoRows {
		return "", "", false, fmt.Errorf("read output filesystem semantics: %w", err)
	}
	if err == nil && storedCase == "false" {
		caseSensitive = false
	}
	return toolsRoot, outputRoot, caseSensitive, nil
}

func validateRuntimeRootPair(toolsRoot, outputRoot string, caseSensitive bool) error {
	for _, root := range []string{toolsRoot, outputRoot} {
		if root != "" && (!filepath.IsAbs(root) || filepath.Clean(root) != root) {
			return fmt.Errorf("runtime directory is not a normalized absolute path")
		}
	}
	if toolsRoot != "" && outputRoot != "" && persistedPathsOverlap(toolsRoot, outputRoot, caseSensitive) {
		return fmt.Errorf("tools directory overlaps with output directory")
	}
	return nil
}

func validateOutputAgainstActiveMoves(ctx context.Context, tx bun.Tx, outputRoot string, caseSensitive bool) error {
	if outputRoot == "" {
		return nil
	}
	var snapshots []struct {
		InputSnapshot []byte `bun:"input_snapshot"`
	}
	if err := tx.NewRaw("SELECT input_snapshot FROM operation WHERE kind = 'move_tools_root' AND state IN ('queued', 'running') ORDER BY id").Scan(ctx, &snapshots); err != nil {
		return fmt.Errorf("read active tools root moves: %w", err)
	}
	for _, row := range snapshots {
		var snapshot struct {
			OldRoot string `json:"old_root"`
			NewRoot string `json:"new_root"`
		}
		if err := json.Unmarshal(row.InputSnapshot, &snapshot); err != nil || snapshot.OldRoot == "" || snapshot.NewRoot == "" {
			return fmt.Errorf("active tools root move has an invalid snapshot")
		}
		if persistedPathsOverlap(outputRoot, snapshot.OldRoot, caseSensitive) || persistedPathsOverlap(outputRoot, snapshot.NewRoot, caseSensitive) {
			return fmt.Errorf("output directory overlaps with an active tools root move")
		}
	}
	return nil
}

// persistedPathsOverlap compares already-normalized local paths without doing
// filesystem work inside a database transaction. Case behavior comes from the
// persisted output filesystem probe; symlink resolution is performed before
// paths enter runtime settings or a move snapshot.
func persistedPathsOverlap(first, second string, caseSensitive bool) bool {
	first, second = filepath.Clean(first), filepath.Clean(second)
	if first == "." || second == "." {
		return false
	}
	firstVolume, firstPath := filepath.VolumeName(first), first[len(filepath.VolumeName(first)):]
	secondVolume, secondPath := filepath.VolumeName(second), second[len(filepath.VolumeName(second)):]
	if !pathPartEqual(firstVolume, secondVolume, caseSensitive) {
		return false
	}
	firstParts := pathParts(firstPath)
	secondParts := pathParts(secondPath)
	return pathPartsContain(firstParts, secondParts, caseSensitive) || pathPartsContain(secondParts, firstParts, caseSensitive)
}

func pathParts(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == filepath.Separator })
}

func pathPartEqual(first, second string, caseSensitive bool) bool {
	if caseSensitive {
		return first == second
	}
	return strings.EqualFold(first, second)
}

func pathPartsContain(parent, child []string, caseSensitive bool) bool {
	if len(parent) > len(child) {
		return false
	}
	for index := range parent {
		if !pathPartEqual(parent[index], child[index], caseSensitive) {
			return false
		}
	}
	return true
}

func validateToolsMoveRootsAgainstOutput(ctx context.Context, tx bun.Tx, oldRoot, newRoot string) error {
	toolsRoot, outputRoot, caseSensitive, err := readRuntimeRoots(ctx, tx)
	if err != nil {
		return err
	}
	if toolsRoot != oldRoot && toolsRoot != newRoot {
		return fmt.Errorf("tools directory no longer matches the move snapshot")
	}
	if err := validateRuntimeRootPair(oldRoot, outputRoot, caseSensitive); err != nil {
		return err
	}
	if err := validateRuntimeRootPair(newRoot, outputRoot, caseSensitive); err != nil {
		return fmt.Errorf("move target %w", err)
	}
	return nil
}

func setManyTx(ctx context.Context, tx bun.Tx, values map[string]string) error {
	if err := lockMusicBrainzIfNeeded(ctx, tx, values); err != nil {
		return err
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		value := values[name]
		_, err := tx.NewInsert().Model(&AppSetting{Name: name, Value: value}).
			On("CONFLICT (setting_name) DO UPDATE").
			Set("setting_value = EXCLUDED.setting_value").
			Set("updated_at = now()").Exec(ctx)
		if err != nil {
			return fmt.Errorf("set setting %q: %w", name, err)
		}
	}
	return nil
}

// SetMusicBrainzVerifiedIfCurrent commits a successful check only for the same
// configuration generation that was checked. Config writes share its DB lock.
func (r *SettingsRepository) SetMusicBrainzVerifiedIfCurrent(ctx context.Context, mode, baseURL, identity, verifiedAt string) (bool, error) {
	matched := false
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("lock MusicBrainz configuration: %w", err)
		}
		read := func(name, defaultValue string) (string, error) {
			var value string
			err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", name).Scan(ctx, &value)
			if err == sql.ErrNoRows {
				return defaultValue, nil
			}
			if err != nil {
				return "", fmt.Errorf("read MusicBrainz configuration: %w", err)
			}
			return value, nil
		}
		for _, expected := range []struct{ name, value, defaultValue string }{
			{"musicbrainz_mode", mode, "public"},
			{"musicbrainz_base_url", baseURL, ""},
			{"musicbrainz_config_identity", identity, ""},
		} {
			current, err := read(expected.name, expected.defaultValue)
			if err != nil {
				return err
			}
			if current != expected.value {
				return nil
			}
		}
		if _, err := tx.NewInsert().Model(&AppSetting{Name: "musicbrainz_verified_at", Value: verifiedAt}).
			On("CONFLICT (setting_name) DO UPDATE").
			Set("setting_value = EXCLUDED.setting_value").
			Set("updated_at = now()").Exec(ctx); err != nil {
			return fmt.Errorf("record MusicBrainz verification: %w", err)
		}
		matched = true
		return nil
	})
	return matched, err
}

// SetIfAbsent atomically writes a setting once and returns its persisted value.
func (r *SettingsRepository) SetIfAbsent(ctx context.Context, name, value string) (string, error) {
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewInsert().Model(&AppSetting{Name: name, Value: value}).On("CONFLICT (setting_name) DO NOTHING").Exec(ctx)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("set setting %q if absent: %w", name, err)
	}
	persisted, _, err := r.Get(ctx, name)
	return persisted, err
}

// InitializePlatform serializes first starts and persists both immutable keys together.
// An existing partial platform is diagnosed rather than silently completed.
func (r *SettingsRepository) InitializePlatform(ctx context.Context, goos, goarch string) (string, string, bool, error) {
	var persistedOS, persistedArch string
	complete := false
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "instance-platform"); err != nil {
			return fmt.Errorf("lock instance platform: %w", err)
		}
		read := func(name string) (string, bool, error) {
			var setting AppSetting
			err := tx.NewSelect().Model(&setting).Where("setting_name = ?", name).Scan(ctx)
			if err == sql.ErrNoRows {
				return "", false, nil
			}
			if err != nil {
				return "", false, fmt.Errorf("read instance platform setting %q: %w", name, err)
			}
			return setting.Value, true, nil
		}
		var hasOS, hasArch bool
		var err error
		persistedOS, hasOS, err = read("instance.goos")
		if err != nil {
			return err
		}
		persistedArch, hasArch, err = read("instance.goarch")
		if err != nil {
			return err
		}
		if hasOS != hasArch {
			return nil
		}
		if !hasOS {
			for _, setting := range []AppSetting{
				{Name: "instance.goos", Value: goos},
				{Name: "instance.goarch", Value: goarch},
			} {
				if _, err := tx.NewInsert().Model(&setting).Exec(ctx); err != nil {
					return fmt.Errorf("initialize instance platform: %w", err)
				}
			}
			persistedOS, persistedArch = goos, goarch
		}
		complete = true
		return nil
	})
	return persistedOS, persistedArch, complete, err
}

func (r *SettingsRepository) CompleteSetupOnce(ctx context.Context, value string) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("lock tools move gate: %w", err)
		}
		if err := lockSetupCompletion(ctx, tx); err != nil {
			return fmt.Errorf("lock setup completion: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, "ffmpeg", "fpcalc"); err != nil {
			return fmt.Errorf("lock active installations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("lock MusicBrainz configuration: %w", err)
		}
		var completedAt string
		completionErr := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "setup_completed_at").Scan(ctx, &completedAt)
		if completionErr != nil && completionErr != sql.ErrNoRows {
			return fmt.Errorf("read setup completion: %w", completionErr)
		}
		if completionErr == sql.ErrNoRows {
			for _, kind := range []string{"ffmpeg", "fpcalc"} {
				if err := refuseAmbiguousReadyInstallationsTx(ctx, tx, kind); err != nil {
					return fmt.Errorf("complete setup: %w", err)
				}
			}
		}
		_, err := tx.NewInsert().Model(&AppSetting{Name: "setup_completed_at", Value: value}).
			On("CONFLICT (setting_name) DO NOTHING").Exec(ctx)
		if err != nil {
			return fmt.Errorf("complete setup: %w", err)
		}
		return nil
	})
}

// CompleteSetupIfCurrent commits only the generation checked by the final
// command. No network or filesystem work runs inside this transaction.
func (r *SettingsRepository) CompleteSetupIfCurrent(ctx context.Context, expected map[string]string, value string) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("lock tools move gate: %w", err)
		}
		// Match install admission and activation: completion, package locks, then rows.
		// The MusicBrainz lock also protects absent/default configuration keys.
		if err := lockSetupCompletion(ctx, tx); err != nil {
			return fmt.Errorf("lock setup completion: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, "ffmpeg", "fpcalc"); err != nil {
			return fmt.Errorf("lock active installations: %w", err)
		}
		for _, name := range []string{"musicbrainz-config"} {
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", name); err != nil {
				return fmt.Errorf("lock setup requirement %q: %w", name, err)
			}
		}
		var completedAt string
		completionErr := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "setup_completed_at").Scan(ctx, &completedAt)
		if completionErr != nil && completionErr != sql.ErrNoRows {
			return fmt.Errorf("read setup completion: %w", completionErr)
		}
		if completionErr == sql.ErrNoRows {
			for _, kind := range []string{"ffmpeg", "fpcalc"} {
				if err := refuseAmbiguousReadyInstallationsTx(ctx, tx, kind); err != nil {
					return fmt.Errorf("complete setup: %w", err)
				}
			}
		}
		// Active IDs and their records stay locked until completion commits.
		// Recheck readiness here because workers can update installation rows
		// without writing a setting.
		for _, required := range []struct {
			kind, key   string
			executables []string
		}{
			{"ffmpeg", "active_ffmpeg_installation_id", []string{"ffmpeg", "ffprobe"}},
			{"fpcalc", "active_fpcalc_installation_id", []string{"fpcalc"}},
		} {
			var installation ToolInstallation
			if err := tx.NewSelect().Model(&installation).Where("id = ?", expected[required.key]).
				For("SHARE").Scan(ctx); err != nil {
				return fmt.Errorf("lock active %s installation: %w", required.kind, err)
			}
			if installation.PackageKind != required.kind || installation.State != "ready" ||
				installation.PlatformGOOS != expected["instance.goos"] || installation.PlatformGOARCH != expected["instance.goarch"] ||
				installation.VerifiedAt == nil {
				return fmt.Errorf("active %s installation is not verified and ready for this platform", required.kind)
			}
			var versions map[string]string
			if err := json.Unmarshal(installation.ExecutableVersions, &versions); err != nil {
				return fmt.Errorf("read active %s executable verification: %w", required.kind, err)
			}
			for _, executable := range required.executables {
				if installation.PlatformGOOS == "windows" {
					executable += ".exe"
				}
				if strings.TrimSpace(versions[executable]) == "" {
					return fmt.Errorf("active %s installation has no verified %s", required.kind, executable)
				}
			}
		}
		names := make([]string, 0, len(expected))
		for name := range expected {
			names = append(names, name)
		}
		slices.Sort(names)
		var current []AppSetting
		if err := tx.NewSelect().Model(&current).Where("setting_name IN (?)", bun.List(names)).
			Order("setting_name").For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("lock current setup settings: %w", err)
		}
		values := map[string]string{"musicbrainz_mode": "public", "musicbrainz_base_url": "", "musicbrainz_config_identity": ""}
		for _, setting := range current {
			values[setting.Name] = setting.Value
		}
		for name, checked := range expected {
			if saved, exists := values[name]; !exists || saved != checked {
				return fmt.Errorf("setup configuration changed during final check")
			}
		}
		if _, err := tx.NewInsert().Model(&AppSetting{Name: "musicbrainz_verified_at", Value: value}).
			On("CONFLICT (setting_name) DO UPDATE").Set("setting_value = EXCLUDED.setting_value").
			Set("updated_at = now()").Exec(ctx); err != nil {
			return fmt.Errorf("record final MusicBrainz verification: %w", err)
		}
		if _, err := tx.NewInsert().Model(&AppSetting{Name: "setup_completed_at", Value: value}).
			On("CONFLICT (setting_name) DO NOTHING").Exec(ctx); err != nil {
			return fmt.Errorf("complete setup: %w", err)
		}
		return nil
	})
}
