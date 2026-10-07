//go:build integration

package settings_test

import (
	"context"
	"sync"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestInitializePlatformRollsBackBothKeysWhenSecondInsertFails(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, "ALTER TABLE app_setting ADD CONSTRAINT reject_goarch CHECK (setting_name <> 'instance.goarch')"); err != nil {
		t.Fatal(err)
	}
	repository := persistence.NewSettingsRepository(database)
	registry := settings.New(repository, nil)

	if _, err := registry.InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"}); err == nil {
		t.Fatal("initialization succeeded despite rejected GOARCH")
	}

	if _, exists, err := repository.Get(ctx, settings.PlatformGOOSKey); err != nil || exists {
		t.Fatalf("GOOS remained after failed initialization: exists=%v, err=%v", exists, err)
	}
}

func TestInitializePlatformDoesNotCompletePartialPlatform(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSettingsRepository(database)
	if err := repository.Set(ctx, settings.PlatformGOOSKey, "linux"); err != nil {
		t.Fatal(err)
	}

	state, err := settings.New(repository, nil).InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Diagnostic {
		t.Fatal("partial persisted platform was accepted")
	}
	if _, exists, err := repository.Get(ctx, settings.PlatformGOARCHKey); err != nil || exists {
		t.Fatalf("GOARCH was inserted into partial platform: exists=%v, err=%v", exists, err)
	}
}

func TestInitializePlatformConcurrentStartsKeepOnePlatform(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSettingsRepository(database)
	registry := settings.New(repository, nil)
	candidates := []settings.Platform{{GOOS: "linux", GOARCH: "amd64"}, {GOOS: "darwin", GOARCH: "arm64"}}
	states := make([]settings.PlatformState, len(candidates))
	errors := make([]error, len(candidates))
	var group sync.WaitGroup
	for index, candidate := range candidates {
		group.Add(1)
		go func() {
			defer group.Done()
			states[index], errors[index] = registry.InitializePlatform(ctx, candidate)
		}()
	}
	group.Wait()
	for index, err := range errors {
		if err != nil {
			t.Fatalf("initialize candidate %d: %v", index, err)
		}
	}
	if states[0].Platform != states[1].Platform || states[0].Diagnostic == states[1].Diagnostic {
		t.Fatalf("concurrent starts returned incompatible states: %+v", states)
	}
	goos, _, err := repository.Get(ctx, settings.PlatformGOOSKey)
	if err != nil {
		t.Fatal(err)
	}
	goarch, _, err := repository.Get(ctx, settings.PlatformGOARCHKey)
	if err != nil {
		t.Fatal(err)
	}
	if (settings.Platform{GOOS: goos, GOARCH: goarch}) != states[0].Platform {
		t.Fatalf("persisted platform %s/%s differs from returned platform %+v", goos, goarch, states[0].Platform)
	}
}

func TestMusicBrainzVerificationRejectsStaleIdentityWithSameValues(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	registry := settings.New(persistence.NewSettingsRepository(database), nil)
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", "https://mb.example.com"); err != nil {
		t.Fatal(err)
	}
	checked, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SetMusicBrainzConfig(ctx, checked.Mode, checked.BaseURL); err != nil {
		t.Fatal(err)
	}

	if err := registry.SetMusicBrainzVerified(ctx, checked); err == nil {
		t.Fatal("stale successful check verified a newer configuration with the same values")
	}
	current, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil || current.VerifiedAt != nil || current.Identity == checked.Identity {
		t.Fatalf("stale verification changed configuration: %+v, %v", current, err)
	}
}

func TestMusicBrainzConcurrentConfigChangeCannotKeepOldVerification(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	registry := settings.New(persistence.NewSettingsRepository(database), nil)
	if err := registry.SetMusicBrainzConfig(ctx, "public", ""); err != nil {
		t.Fatal(err)
	}
	checked, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	verificationResult := make(chan error, 1)
	configResult := make(chan error, 1)
	go func() {
		<-start
		verificationResult <- registry.SetMusicBrainzVerified(ctx, checked)
	}()
	go func() {
		<-start
		configResult <- registry.SetMusicBrainzConfig(ctx, "self-hosted", "https://mb.example.com")
	}()
	close(start)
	verificationError, configError := <-verificationResult, <-configResult
	if configError != nil {
		t.Fatalf("concurrent config update: %v (verification: %v)", configError, verificationError)
	}
	current, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.Mode != "self-hosted" || current.VerifiedAt != nil {
		t.Fatalf("older check verified current configuration: %+v", current)
	}
}
