//go:build integration

package settings_test

import (
	"context"
	"sync"
	"testing"
	"time"

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
	if err := registry.SetMusicBrainzConfig(ctx, checked.Mode, "https://different.example.com"); err != nil {
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

// TestReadRuntimeSettingsWaitsForMusicBrainzConfigLockAndReadsCommittedRevision
// proves the public runtime snapshot waits for the MusicBrainz configuration
// gate and returns one committed revision. The blocker commits a newer
// non-MusicBrainz setting, MusicBrainz configuration revision, and opposite
// credential-presence state while the reader is blocked. A single coherent
// snapshot must therefore contain only the committed revision.
func TestReadRuntimeSettingsWaitsForMusicBrainzConfigLockAndReadsCommittedRevision(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSettingsRepository(database)
	registry := settings.New(repository, nil)

	const (
		baselineIdentity = "11111111-1111-4111-8111-111111111111"
		revisionIdentity = "22222222-2222-4222-8222-222222222222"
		githubSentinel   = "test-github-token-sentinel"
		acoustidSentinel = "test-acoustid-application-key-sentinel"
	)
	if _, err := database.ExecContext(ctx, `INSERT INTO app_setting(setting_name, setting_value, updated_at) VALUES
		(?, ?, now()),
		(?, ?, now()),
		(?, ?, now()),
		(?, ?, now()),
		(?, ?, now())
		ON CONFLICT (setting_name) DO UPDATE SET setting_value = EXCLUDED.setting_value, updated_at = now()`,
		settings.PublicationFormatKey, "source",
		settings.MusicBrainzModeKey, "public",
		settings.MusicBrainzBaseURLKey, "",
		settings.MusicBrainzConfigIdentityKey, baselineIdentity,
		settings.GitHubTokenKey, githubSentinel,
	); err != nil {
		t.Fatalf("seed runtime settings baseline: %v", err)
	}

	before, err := registry.ReadRuntimeSettings(ctx)
	if err != nil {
		t.Fatalf("read runtime settings baseline: %v", err)
	}
	if before.PublicationFormat != "source" {
		t.Fatalf("baseline publication format = %q, want source", before.PublicationFormat)
	}
	if before.MusicBrainzMode != "public" || before.MusicBrainzBaseURL != "" || before.MusicBrainzVerifiedAt != nil {
		t.Fatalf("baseline MusicBrainz configuration = %q/%q/%v, want public/empty/unverified",
			before.MusicBrainzMode, before.MusicBrainzBaseURL, before.MusicBrainzVerifiedAt)
	}
	if !before.HasGitHubToken || before.HasAcoustIDApplicationKey {
		t.Fatalf("baseline credential presence = github:%v acoustid:%v, want github:true acoustid:false",
			before.HasGitHubToken, before.HasAcoustIDApplicationKey)
	}

	blockerCtx, cancelBlocker := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelBlocker()
	blocker, err := database.BeginTx(blockerCtx, nil)
	if err != nil {
		t.Fatalf("begin runtime settings blocker transaction: %v", err)
	}
	blockerOpen := true
	defer func() {
		if blockerOpen {
			_ = blocker.Rollback()
		}
	}()
	if _, err := blocker.ExecContext(blockerCtx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "musicbrainz-config"); err != nil {
		t.Fatalf("lock MusicBrainz configuration: %v", err)
	}
	var blockerPID int
	if err := blocker.QueryRowContext(blockerCtx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatalf("read blocker backend id: %v", err)
	}

	type snapshotResult struct {
		runtimeSettings settings.RuntimeSettings
		err             error
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelRead()
	readResult := make(chan snapshotResult, 1)
	go func() {
		runtimeSettings, err := registry.ReadRuntimeSettings(readCtx)
		readResult <- snapshotResult{runtimeSettings: runtimeSettings, err: err}
	}()

	// Wait until the reader's backend is actually waiting on this blocker's
	// MusicBrainz advisory lock. A query hook would only prove that SQL was
	// issued; pg_blocking_pids proves the lock order delayed the read.
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelWait()
	for {
		select {
		case result := <-readResult:
			t.Fatalf("runtime settings read returned before the MusicBrainz lock was released: %v", result.err)
		default:
		}
		var waiting bool
		if err := database.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database()
			  AND wait_event_type = 'Lock'
			  AND ? = ANY(pg_blocking_pids(pid))
			  AND query LIKE '%pg_advisory_xact_lock_shared%'
		)`, blockerPID).Scan(waitCtx, &waiting); err != nil {
			t.Fatalf("observe runtime settings lock wait: %v", err)
		}
		if waiting {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("runtime settings read did not wait for the MusicBrainz configuration lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case result := <-readResult:
		t.Fatalf("runtime settings read returned after the lock wait was observed but before the lock was released: %v", result.err)
	default:
	}

	if _, err := blocker.ExecContext(blockerCtx, `INSERT INTO app_setting(setting_name, setting_value, updated_at) VALUES
		(?, ?, now()),
		(?, ?, now()),
		(?, ?, now()),
		(?, ?, now()),
		(?, ?, now()),
		(?, ?, now())
		ON CONFLICT (setting_name) DO UPDATE SET setting_value = EXCLUDED.setting_value, updated_at = now()`,
		settings.PublicationFormatKey, "mka",
		settings.MusicBrainzModeKey, "self-hosted",
		settings.MusicBrainzBaseURLKey, "https://mb.example.test/ws/2",
		settings.MusicBrainzConfigIdentityKey, revisionIdentity,
		settings.MusicBrainzVerifiedAtKey, "2026-11-23T12:00:00Z",
		settings.AcoustIDApplicationKey, acoustidSentinel,
	); err != nil {
		t.Fatalf("write revised runtime settings: %v", err)
	}
	if _, err := blocker.ExecContext(blockerCtx, `DELETE FROM app_setting WHERE setting_name = ?`, settings.GitHubTokenKey); err != nil {
		t.Fatalf("remove GitHub token: %v", err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("commit revised runtime settings: %v", err)
	}
	blockerOpen = false

	var after snapshotResult
	select {
	case after = <-readResult:
	case <-readCtx.Done():
		t.Fatal("runtime settings read did not finish after the MusicBrainz lock was released")
	}
	if after.err != nil {
		t.Fatalf("read runtime settings after the lock was released: %v", after.err)
	}
	if after.runtimeSettings.PublicationFormat != "mka" {
		t.Fatalf("publication format after the lock = %q, want mka; the snapshot mixed revisions",
			after.runtimeSettings.PublicationFormat)
	}
	if after.runtimeSettings.MusicBrainzMode != "self-hosted" ||
		after.runtimeSettings.MusicBrainzBaseURL != "https://mb.example.test/ws/2" {
		t.Fatalf("MusicBrainz configuration after the lock = %q/%q, want self-hosted/revised URL",
			after.runtimeSettings.MusicBrainzMode, after.runtimeSettings.MusicBrainzBaseURL)
	}
	verifiedAt := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	if after.runtimeSettings.MusicBrainzVerifiedAt == nil || !after.runtimeSettings.MusicBrainzVerifiedAt.Equal(verifiedAt) {
		t.Fatalf("MusicBrainz verification after the lock = %v, want %s",
			after.runtimeSettings.MusicBrainzVerifiedAt, verifiedAt)
	}
	if !after.runtimeSettings.HasAcoustIDApplicationKey || after.runtimeSettings.HasGitHubToken {
		t.Fatalf("credential presence after the lock = github:%v acoustid:%v, want github:false acoustid:true",
			after.runtimeSettings.HasGitHubToken, after.runtimeSettings.HasAcoustIDApplicationKey)
	}
}
