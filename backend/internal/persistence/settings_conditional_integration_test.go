//go:build integration

package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestConditionalRuntimeUpdateRejectsStaleBaselineAndPreservesOtherSettings(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := NewSettingsRepository(db)
	if err := repository.SetMany(ctx, map[string]string{
		settings.PublicationRemoveSourcesKey: "false",
	}); err != nil {
		t.Fatal(err)
	}
	defaultFormat := ""
	if err := repository.UpdateRuntimeIfCurrent(ctx, "", "", settings.RuntimeSettingsExpected{
		PublicationFormat: &defaultFormat,
	}, map[string]string{settings.PublicationFormatKey: "mka"}); err != nil {
		t.Fatalf("save against absent-row default: %v", err)
	}

	baselineFormat := "mka"
	if err := repository.UpdateRuntimeIfCurrent(ctx, "", "", settings.RuntimeSettingsExpected{
		PublicationFormat: &baselineFormat,
	}, map[string]string{settings.PublicationFormatKey: "source"}); err != nil {
		t.Fatalf("save against valid baseline: %v", err)
	}

	if err := repository.SetMany(ctx, map[string]string{
		settings.PublicationFormatKey:        "m4a",
		settings.PublicationRemoveSourcesKey: "true",
	}); err != nil {
		t.Fatal(err)
	}
	staleFormat := "source"
	err := repository.UpdateRuntimeIfCurrent(ctx, "", "", settings.RuntimeSettingsExpected{
		PublicationFormat: &staleFormat,
	}, map[string]string{settings.PublicationFormatKey: "mka"})
	if !errors.Is(err, settings.ErrSettingsConflict) {
		t.Fatalf("stale update error = %v, want settings conflict", err)
	}
	if got, _, err := repository.Get(ctx, settings.PublicationFormatKey); err != nil || got != "m4a" {
		t.Fatalf("format after stale update = %q, err=%v, want m4a", got, err)
	}
	if got, _, err := repository.Get(ctx, settings.PublicationRemoveSourcesKey); err != nil || got != "true" {
		t.Fatalf("unrelated setting after stale update = %q, err=%v, want true", got, err)
	}

	replacementFormat := "m4a"
	if err := repository.UpdateRuntimeIfCurrent(ctx, "", "", settings.RuntimeSettingsExpected{
		PublicationFormat: &replacementFormat,
	}, map[string]string{settings.PublicationFormatKey: "source"}); err != nil {
		t.Fatalf("replace against current baseline: %v", err)
	}
	if got, _, err := repository.Get(ctx, settings.PublicationFormatKey); err != nil || got != "source" {
		t.Fatalf("format after replacement = %q, err=%v, want source", got, err)
	}
	if got, _, err := repository.Get(ctx, settings.PublicationRemoveSourcesKey); err != nil || got != "true" {
		t.Fatalf("unrelated setting after replacement = %q, err=%v, want true", got, err)
	}

	initialThrottle := false
	newThrottle := true
	publicMode, emptyURL := "public", ""
	selfHostedMode, musicBrainzURL := "self-hosted", "https://mb.example.org/ws/2"
	if err := repository.UpdateMusicBrainzSettingsIfCurrent(ctx, settings.MusicBrainzSettingsExpected{
		Mode: &publicMode, BaseURL: &emptyURL, SelfHostedThrottle: &initialThrottle,
	}, &selfHostedMode, &musicBrainzURL, &newThrottle, nil); err != nil {
		t.Fatalf("save MusicBrainz target and throttle against valid baseline: %v", err)
	}
	if got, _, err := repository.Get(ctx, settings.MusicBrainzBaseURLKey); err != nil || got != "https://mb.example.org/ws/2" {
		t.Fatalf("MusicBrainz target after update = %q, err=%v", got, err)
	}
	if got, _, err := repository.Get(ctx, settings.MusicBrainzSelfHostedThrottleKey); err != nil || got != "true" {
		t.Fatalf("MusicBrainz throttle after update = %q, err=%v", got, err)
	}
	if _, exists, err := repository.Get(ctx, settings.MusicBrainzSelfHostedDelayKey); err != nil || exists {
		t.Fatalf("omitted MusicBrainz delay presence=%v err=%v, want absent", exists, err)
	}

	staleThrottle := false
	otherURL := "https://other.example.org/ws/2"
	err = repository.UpdateMusicBrainzSettingsIfCurrent(ctx, settings.MusicBrainzSettingsExpected{
		Mode: &selfHostedMode, BaseURL: &musicBrainzURL, SelfHostedThrottle: &staleThrottle,
	}, &selfHostedMode, &otherURL, &staleThrottle, nil)
	if !errors.Is(err, settings.ErrSettingsConflict) {
		t.Fatalf("stale MusicBrainz update error = %v, want settings conflict", err)
	}
	if got, _, err := repository.Get(ctx, settings.MusicBrainzBaseURLKey); err != nil || got != "https://mb.example.org/ws/2" {
		t.Fatalf("MusicBrainz target changed on stale update = %q, err=%v", got, err)
	}
	if got, _, err := repository.Get(ctx, settings.MusicBrainzSelfHostedThrottleKey); err != nil || got != "true" {
		t.Fatalf("MusicBrainz throttle changed on stale update = %q, err=%v", got, err)
	}
}
