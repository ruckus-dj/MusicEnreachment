//go:build integration

package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// This acceptance test deliberately uses a non-empty old output directory: the
// setup save path must not probe it as an empty first-run destination.
func TestOutputResetAcceptanceSaveChangedRootPreservesRuntimeAndOldFiles(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	oldRoot := t.TempDir()
	oldFile := filepath.Join(oldRoot, "album", "track.flac")
	tools := t.TempDir()
	tools, err := settings.NormalizePath(tools)
	if err != nil {
		t.Fatal(err)
	}
	setup, registry, _ := outputResetAcceptanceSetup(t, database, oldRoot)
	if err := setup.SaveRuntime(ctx, tools, oldRoot, "mka"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(oldFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile, []byte("existing audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingRoot := filepath.Join(t.TempDir(), "not-yet-created", "output")
	missingRoot, err = settings.NormalizePath(missingRoot)
	if err != nil {
		t.Fatal(err)
	}
	outputUpdate := missingRoot
	formatUpdate := "source"
	if err := setup.SaveRuntimeRequest(ctx, nil, &outputUpdate, &formatUpdate); err != nil {
		t.Fatalf("save changed output root: %v", err)
	}
	values, err := registry.ReadRuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if values.OutputDirectory != missingRoot || values.ToolsDirectory != tools || values.PublicationFormat != "source" || values.OutputCaseSensitive == nil || values.OutputUnicodeNormalization == "" {
		t.Fatalf("runtime settings after reset = %+v", values)
	}
	if data, err := os.ReadFile(oldFile); err != nil || string(data) != "existing audio" {
		t.Fatalf("old output file changed: contents=%q err=%v", data, err)
	}
	var journals int
	if err := database.NewRaw("SELECT count(*) FROM output_reset_journal WHERE new_root = ?", missingRoot).Scan(ctx, &journals); err != nil || journals != 1 {
		t.Fatalf("journal rows=%d err=%v, want one committed reset", journals, err)
	}
}

func TestOutputResetAcceptanceSameCanonicalPopulatedRootDoesNotJournal(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	root := t.TempDir()
	setup, _, _ := outputResetAcceptanceSetup(t, database, root)
	if err := setup.SaveRuntime(ctx, t.TempDir(), root, "mka"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "already-here"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := root
	var journalsBefore int
	if err := database.NewRaw("SELECT count(*) FROM output_reset_journal WHERE new_root = ?", root).Scan(ctx, &journalsBefore); err != nil {
		t.Fatal(err)
	}
	if err := setup.SaveRuntimeRequest(ctx, nil, &output, nil); err != nil {
		t.Fatalf("same-root populated save: %v", err)
	}
	var journals int
	if err := database.NewRaw("SELECT count(*) FROM output_reset_journal WHERE new_root = ?", root).Scan(ctx, &journals); err != nil || journals != journalsBefore {
		t.Fatalf("same-root journal rows before=%d after=%d err=%v, want no additional journal", journalsBefore, journals, err)
	}
}

func TestOutputResetAcceptancePreparingRecoveryOwnsOnlyCreatedDirectories(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	oldRoot := t.TempDir()
	_, registry, _ := outputResetAcceptanceSetup(t, database, oldRoot)
	runtimeSettings, err := registry.ReadRuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	foreign := filepath.Join(base, "foreign")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(base, "reset", "output")
	newRoot, err = settings.NormalizePath(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	filesystem := settings.NewResetFilesystem()
	allowedDirectories, err := filesystem.PlannedDirectories(newRoot, []string{newRoot})
	if err != nil {
		t.Fatal(err)
	}
	repository := persistence.NewSetupManagerRepository(database)
	boom := errors.New("crash during preparation")
	_, err = repository.RunOutputResetRequest(ctx, persistence.OutputResetRequest{
		ExpectedOldRoot: runtimeSettings.OutputDirectory, ExpectedToolsRoot: runtimeSettings.ToolsDirectory, NewRoot: newRoot,
		AllowedDirectories: allowedDirectories,
		Prepare: func(ctx context.Context, record func(persistence.OutputResetDirectory) error) error {
			if err := filesystem.PrepareDurable(ctx, newRoot, []string{newRoot}, func(entry settings.ResetDirectoryRecord) error {
				return record(persistence.OutputResetDirectory{Path: entry.Path, Phase: entry.Phase, Identity: entry.Identity})
			}); err != nil {
				return err
			}
			return boom
		}, Finish: func(context.Context, persistence.OutputResetJournal) error { return nil },
	})
	if !errors.Is(err, boom) {
		t.Fatalf("prepare error=%v, want injected crash", err)
	}
	setupStore := persistence.NewSettingsRepository(database)
	serviceReset := service.NewOutputReset(repository, settings.NewResetFilesystem(), nil)
	if err := serviceReset.RecoverOutputReset(ctx); err != nil {
		t.Fatalf("recover preparing journal: %v", err)
	}
	if _, err := os.Stat(newRoot); !os.IsNotExist(err) {
		t.Fatalf("reset-created root survived recovery: %v", err)
	}
	if info, err := os.Stat(foreign); err != nil || !info.IsDir() {
		t.Fatalf("foreign preexisting directory removed: info=%v err=%v", info, err)
	}
	if value, found, err := setupStore.Get(ctx, settings.OutputDirectoryKey); err != nil || found && value != runtimeSettings.OutputDirectory {
		t.Fatalf("output setting after rollback=%q found=%v err=%v", value, found, err)
	}
}

func TestOutputResetAcceptanceCommittedRecoveryRetainsDirectories(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	oldRoot := t.TempDir()
	_, registry, _ := outputResetAcceptanceSetup(t, database, oldRoot)
	runtimeSettings, err := registry.ReadRuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(t.TempDir(), "output")
	newRoot, err = settings.NormalizePath(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	filesystem := settings.NewResetFilesystem()
	allowedDirectories, err := filesystem.PlannedDirectories(newRoot, []string{newRoot})
	if err != nil {
		t.Fatal(err)
	}
	repository := persistence.NewSetupManagerRepository(database)
	_, err = repository.RunOutputResetRequest(ctx, persistence.OutputResetRequest{
		ExpectedOldRoot: runtimeSettings.OutputDirectory, ExpectedToolsRoot: runtimeSettings.ToolsDirectory, NewRoot: newRoot,
		AllowedDirectories: allowedDirectories,
		Prepare: func(ctx context.Context, record func(persistence.OutputResetDirectory) error) error {
			return filesystem.PrepareDurable(ctx, newRoot, []string{newRoot}, func(entry settings.ResetDirectoryRecord) error {
				return record(persistence.OutputResetDirectory{Path: entry.Path, Phase: entry.Phase, Identity: entry.Identity})
			})
		}, Finish: func(context.Context, persistence.OutputResetJournal) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.NewOutputReset(repository, settings.NewResetFilesystem(), nil).RecoverOutputReset(ctx); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(newRoot); err != nil || !info.IsDir() {
		t.Fatalf("committed root not retained: info=%v err=%v", info, err)
	}
}

func outputResetAcceptanceSetup(t *testing.T, database *bun.DB, outputRoot string) (*service.SetupService, *settings.Registry, *persistence.SetupManagerRepository) {
	t.Helper()
	_ = openSourceScanRiver(t, database)
	store := persistence.NewSettingsRepository(database)
	registry := settings.New(store, nil)
	repository := persistence.NewSetupManagerRepository(database)
	platform := settings.PlatformState{Platform: settings.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}}
	setup := service.NewSetup(store, registry, platform, repository, nil).WithOutputReset(
		service.NewOutputReset(repository, nil, nil),
	)
	if err := setup.SaveRuntime(t.Context(), t.TempDir(), outputRoot, "mka"); err != nil {
		t.Fatal(err)
	}
	return setup, registry, repository
}
