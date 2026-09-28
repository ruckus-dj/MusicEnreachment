package persistence

import (
	"context"
	"database/sql"
	"fmt"
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
		for name, value := range values {
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
