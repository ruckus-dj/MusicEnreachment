package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, name := range []string{
			"musicbrainz_mode", "musicbrainz_base_url", "musicbrainz_config_identity", "musicbrainz_verified_at",
		} {
			if _, present := values[name]; present {
				if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "musicbrainz-config"); err != nil {
					return fmt.Errorf("lock MusicBrainz configuration: %w", err)
				}
				break
			}
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
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "setup-completion"); err != nil {
			return fmt.Errorf("lock setup completion: %w", err)
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
		// Match activation's ordering: completion, package activation, then rows.
		// The MusicBrainz lock also protects absent/default configuration keys.
		for _, name := range []string{"setup-completion", "active-installation:ffmpeg", "active-installation:fpcalc", "musicbrainz-config"} {
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", name); err != nil {
				return fmt.Errorf("lock setup requirement %q: %w", name, err)
			}
		}
		names := make([]string, 0, len(expected))
		for name := range expected {
			names = append(names, name)
		}
		var current []AppSetting
		if err := tx.NewSelect().Model(&current).Where("setting_name IN (?)", bun.List(names)).
			Order("setting_name").For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("lock current setup settings: %w", err)
		}
		values := map[string]string{
			"musicbrainz_mode": "public", "musicbrainz_base_url": "", "musicbrainz_config_identity": "",
		}
		for _, setting := range current {
			values[setting.Name] = setting.Value
		}
		for name, checked := range expected {
			if saved, exists := values[name]; !exists || saved != checked {
				return fmt.Errorf("setup configuration changed during final check")
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
