package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"

	"github.com/google/uuid"
	appsettings "github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

type settingRowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *SettingsRepository) UpdateMusicBrainzSettingsIfCurrent(
	ctx context.Context,
	expected appsettings.MusicBrainzSettingsExpected,
	mode, baseURL *string,
	throttle *bool,
	delaySeconds *float64,
) error {
	if (mode == nil) != (baseURL == nil) || (expected.Mode == nil) != (expected.BaseURL == nil) {
		return fmt.Errorf("MusicBrainz mode and base URL must be supplied together")
	}
	if (mode == nil) != (expected.Mode == nil) {
		return fmt.Errorf("expected MusicBrainz target must match the target update")
	}
	if (throttle != nil && expected.SelfHostedThrottle == nil) || (delaySeconds != nil && expected.SelfHostedDelaySeconds == nil) {
		return fmt.Errorf("expected MusicBrainz provider values are required for every changed field")
	}
	if mode == nil && throttle == nil && delaySeconds == nil {
		return nil
	}
	values := make(map[string]string, 6)
	if mode != nil {
		values[appsettings.MusicBrainzModeKey] = *mode
		values[appsettings.MusicBrainzBaseURLKey] = *baseURL
		values[appsettings.MusicBrainzConfigIdentityKey] = uuid.NewString()
		values[appsettings.MusicBrainzVerifiedAtKey] = ""
	}
	if throttle != nil {
		values[appsettings.MusicBrainzSelfHostedThrottleKey] = strconv.FormatBool(*throttle)
	}
	if delaySeconds != nil {
		values[appsettings.MusicBrainzSelfHostedDelayKey] = strconv.FormatFloat(*delaySeconds, 'f', -1, 64)
	}
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if mode != nil {
			if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
				return fmt.Errorf("lock output admission for MusicBrainz configuration: %w", err)
			}
			if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
				return fmt.Errorf("lock source mutations for MusicBrainz configuration: %w", err)
			}
		}
		if err := lockMusicBrainzIfNeeded(ctx, tx, values); err != nil {
			return err
		}
		if err := checkMusicBrainzSettingsExpected(ctx, tx, expected, throttle != nil, delaySeconds != nil); err != nil {
			return err
		}
		changed := false
		if mode != nil {
			var err error
			changed, err = preserveOrRotateMusicBrainzIdentity(ctx, tx, values)
			if err != nil {
				return fmt.Errorf("compare MusicBrainz provider target: %w", err)
			}
		}
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if _, err := tx.NewInsert().Model(&AppSetting{Name: name, Value: values[name]}).
				On("CONFLICT (setting_name) DO UPDATE").
				Set("setting_value = EXCLUDED.setting_value").
				Set("updated_at = now()").Exec(ctx); err != nil {
				return fmt.Errorf("set setting %q: %w", name, err)
			}
		}
		if mode != nil {
			if err := syncMusicBrainzProviderSource(ctx, tx); err != nil {
				return fmt.Errorf("synchronize MusicBrainz provider source: %w", err)
			}
		}
		if changed {
			if err := requestAllAutomaticIncomingMatchingReconciliationWith(ctx, tx); err != nil {
				return err
			}
		}
		return nil
	})
}

func checkMusicBrainzSettingsExpected(ctx context.Context, tx bun.Tx, expected appsettings.MusicBrainzSettingsExpected, checkThrottle, checkDelay bool) error {
	checks := []struct {
		name, expected, defaultValue string
	}{}
	if expected.Mode != nil {
		checks = append(checks,
			struct{ name, expected, defaultValue string }{appsettings.MusicBrainzModeKey, *expected.Mode, "public"},
			struct{ name, expected, defaultValue string }{appsettings.MusicBrainzBaseURLKey, *expected.BaseURL, ""},
		)
	}
	if checkThrottle {
		checks = append(checks, struct{ name, expected, defaultValue string }{
			appsettings.MusicBrainzSelfHostedThrottleKey,
			strconv.FormatBool(*expected.SelfHostedThrottle), strconv.FormatBool(false),
		})
	}
	if checkDelay {
		checks = append(checks, struct{ name, expected, defaultValue string }{
			appsettings.MusicBrainzSelfHostedDelayKey,
			strconv.FormatFloat(*expected.SelfHostedDelaySeconds, 'f', -1, 64),
			strconv.FormatFloat(0.5, 'f', -1, 64),
		})
	}
	for _, check := range checks {
		if err := checkExpectedSetting(ctx, tx, "?", check.name, check.expected, check.defaultValue); err != nil {
			return err
		}
	}
	return nil
}

func checkRuntimeSettingsExpected(ctx context.Context, reader settingRowReader, expected appsettings.RuntimeSettingsExpected) error {
	defaults := appsettings.DefaultRuntimeSettingsExpected()
	checks := []struct {
		name         string
		value        *string
		defaultValue string
	}{
		{name: appsettings.ToolsDirectoryKey, value: expected.ToolsDirectory, defaultValue: *defaults.ToolsDirectory},
		{name: appsettings.WorkDirectoryKey, value: expected.WorkDirectory, defaultValue: *defaults.WorkDirectory},
		{name: appsettings.OutputDirectoryKey, value: expected.OutputDirectory, defaultValue: *defaults.OutputDirectory},
		{name: appsettings.PublicationFormatKey, value: expected.PublicationFormat, defaultValue: *defaults.PublicationFormat},
		{name: appsettings.PublicationPathTemplateKey, value: expected.PublicationPathTemplate, defaultValue: *defaults.PublicationPathTemplate},
	}
	for _, check := range checks {
		if check.value == nil {
			continue
		}
		if err := checkExpectedSetting(ctx, reader, "$1", check.name, *check.value, check.defaultValue); err != nil {
			return err
		}
	}
	if expected.PublicationRemoveSources != nil {
		if err := checkExpectedSetting(ctx, reader, "$1", appsettings.PublicationRemoveSourcesKey, strconv.FormatBool(*expected.PublicationRemoveSources), strconv.FormatBool(*defaults.PublicationRemoveSources)); err != nil {
			return err
		}
	}
	if expected.SourceFileConcurrency != nil {
		if err := checkExpectedSetting(ctx, reader, "$1", appsettings.SourceFileConcurrencyKey, strconv.Itoa(*expected.SourceFileConcurrency), strconv.Itoa(*defaults.SourceFileConcurrency)); err != nil {
			return err
		}
	}
	return nil
}

func checkExpectedSetting(ctx context.Context, reader settingRowReader, placeholder, name, expected, defaultValue string) error {
	var actual string
	query := "SELECT setting_value FROM app_setting WHERE setting_name = " + placeholder
	err := reader.QueryRowContext(ctx, query, name).Scan(&actual)
	if err != nil {
		if err == sql.ErrNoRows {
			actual = defaultValue
		} else {
			return fmt.Errorf("read expected setting %q: %w", name, err)
		}
	}
	if actual != expected {
		return fmt.Errorf("%w: %s changed since this draft was opened", appsettings.ErrSettingsConflict, name)
	}
	return nil
}
