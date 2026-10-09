//go:build integration

package service_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
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

func TestOutputResetAcceptanceIntentWithoutDurableIdentityFailsClosed(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	oldRoot := t.TempDir()
	_, registry, _ := outputResetAcceptanceSetup(t, database, oldRoot)
	before, err := registry.ReadRuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	newRoot, err := settings.NormalizePath(filepath.Join(base, "output"))
	if err != nil {
		t.Fatal(err)
	}
	filesystem := settings.NewResetFilesystem()
	allowed, err := filesystem.PlannedDirectories(newRoot, []string{newRoot})
	if err != nil {
		t.Fatal(err)
	}
	foreignBytes := []byte("not owned by reset")
	foreignFile := filepath.Join(newRoot, "foreign.txt")
	recordFailure := errors.New("injected durable identity write failure")
	repository := persistence.NewSetupManagerRepository(database)
	_, err = repository.RunOutputResetRequest(ctx, persistence.OutputResetRequest{
		ExpectedOldRoot: before.OutputDirectory, ExpectedToolsRoot: before.ToolsDirectory, NewRoot: newRoot,
		AllowedDirectories: allowed,
		Prepare: func(ctx context.Context, record func(persistence.OutputResetDirectory) error) error {
			// Persist only the intent (the real pre-mkdir journal transition), then
			// simulate the crash window after mkdir but before recording identity.
			if err := record(persistence.OutputResetDirectory{Path: newRoot, Phase: "intent"}); err != nil {
				return err
			}
			if err := os.MkdirAll(newRoot, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(foreignFile, foreignBytes, 0o600); err != nil {
				return err
			}
			return recordFailure
		}, Finish: func(context.Context, persistence.OutputResetJournal) error { return nil },
	})
	if !errors.Is(err, recordFailure) {
		t.Fatalf("prepare error=%v, want injected identity write failure", err)
	}

	reset := service.NewOutputReset(repository, settings.NewResetFilesystem(), nil)
	for attempt := 1; attempt <= 2; attempt++ {
		if err := reset.RecoverOutputReset(ctx); err == nil {
			t.Fatalf("recovery attempt %d unexpectedly adopted an intent-only directory", attempt)
		}
		if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return persistence.AcquireOutputAdmissionGate(ctx, tx)
		}); err == nil {
			t.Fatalf("output admission succeeded after failed recovery attempt %d", attempt)
		}
	}
	info, err := os.Stat(newRoot)
	if err != nil || !info.IsDir() {
		t.Fatalf("intent-only directory was removed: info=%v err=%v", info, err)
	}
	if data, err := os.ReadFile(foreignFile); err != nil || !bytes.Equal(data, foreignBytes) {
		t.Fatalf("foreign bytes changed: contents=%q err=%v", data, err)
	}
	after, err := registry.ReadRuntimeSettings(ctx)
	if err != nil || after.OutputDirectory != before.OutputDirectory || after.ToolsDirectory != before.ToolsDirectory {
		t.Fatalf("runtime settings changed: before=%+v after=%+v err=%v", before, after, err)
	}
	journal, err := repository.ReadUnresolvedOutputReset(ctx)
	if err != nil || journal == nil || journal.State != "preparing" {
		t.Fatalf("unresolved intent-only journal=%+v err=%v, want preparing", journal, err)
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

// The helper is run in a separate test process so killing it releases the
// PostgreSQL session/transaction exactly as a process crash would.
func TestOutputResetProcessCrashChild(t *testing.T) {
	selected := false
	for _, arg := range os.Args {
		if arg == "-test.run=^TestOutputResetProcessCrashChild$" {
			selected = true
		}
	}
	if !selected {
		return
	}
	var input struct{ URL, ToolsRoot, OldRoot, NewRoot string }
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(input.URL))), pgdialect.New())
	defer db.Close()
	repository := persistence.NewSetupManagerRepository(db)
	filesystem := settings.NewResetFilesystem()
	allowed, err := filesystem.PlannedDirectories(input.NewRoot, []string{input.NewRoot})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.RunOutputResetRequest(context.Background(), persistence.OutputResetRequest{
		ExpectedOldRoot: input.OldRoot, ExpectedToolsRoot: input.ToolsRoot, NewRoot: input.NewRoot, AllowedDirectories: allowed,
		Prepare: func(ctx context.Context, record func(persistence.OutputResetDirectory) error) error {
			if err := filesystem.PrepareDurable(ctx, input.NewRoot, []string{input.NewRoot}, func(entry settings.ResetDirectoryRecord) error {
				if err := record(persistence.OutputResetDirectory{Path: entry.Path, Phase: entry.Phase, Identity: entry.Identity}); err != nil {
					return err
				}
				return nil
			}); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(os.Stdout, "durable-prepare-complete"); err != nil {
				return err
			}
			var permission [1]byte
			if _, err := io.ReadFull(os.Stdin, permission[:]); err != nil {
				return fmt.Errorf("await parent permission: %w", err)
			}
			return nil
		}, Finish: func(context.Context, persistence.OutputResetJournal) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOutputResetProcessCrashAfterDurablePrepareRecoversOwnedDirectories(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	oldRoot := t.TempDir()
	_, registry, repository := outputResetAcceptanceSetup(t, database, oldRoot)
	runtimeSettings, err := registry.ReadRuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	foreign := filepath.Join(base, "foreign")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldRoot, "keep"), []byte("old output"), 0o600); err != nil {
		t.Fatal(err)
	}
	newRoot, err := settings.NormalizePath(filepath.Join(base, "new", "output"))
	if err != nil {
		t.Fatal(err)
	}
	plannedDirectories, err := settings.NewResetFilesystem().PlannedDirectories(newRoot, []string{newRoot})
	if err != nil {
		t.Fatal(err)
	}
	childCtx, cancelChild := context.WithTimeout(ctx, time.Minute)
	defer cancelChild()
	cmd := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestOutputResetProcessCrashChild$", "-test.count=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr lockedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitResult := make(chan error, 1)
	go func() { waitResult <- cmd.Wait() }()
	var waitOnce sync.Once
	wait := func() error {
		var result error
		waitOnce.Do(func() { result = <-waitResult })
		return result
	}
	reaped := false
	defer func() {
		if !reaped {
			_ = cmd.Process.Kill()
			select {
			case <-waitResult:
			case <-childCtx.Done():
			}
		}
		_ = stdin.Close()
	}()
	if err := json.NewEncoder(stdin).Encode(struct{ URL, ToolsRoot, OldRoot, NewRoot string }{
		testpostgres.URL(t, database), runtimeSettings.ToolsDirectory, runtimeSettings.OutputDirectory, newRoot,
	}); err != nil {
		t.Fatalf("send child request: %v; child stderr: %s", err, stderr.String())
	}
	ready := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "durable-prepare-complete" {
				ready <- nil
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ready <- err
			return
		}
		ready <- io.ErrUnexpectedEOF
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("child did not reach durable prepare: %v; child stderr: %s", err, stderr.String())
		}
	case <-childCtx.Done():
		t.Fatalf("child did not reach durable prepare: %v; child stderr: %s", childCtx.Err(), stderr.String())
	}
	journal, err := repository.ReadUnresolvedOutputReset(ctx)
	if err != nil || journal == nil || journal.State != "preparing" || journal.NewRoot != newRoot || journal.OldRoot != runtimeSettings.OutputDirectory {
		t.Fatalf("unresolved journal before child kill=%+v err=%v, want preparing reset for %q", journal, err, newRoot)
	}
	var manifest []persistence.OutputResetDirectory
	if err := json.Unmarshal(journal.DirectoryManifest, &manifest); err != nil {
		t.Fatalf("decode crash journal manifest %q: %v", journal.DirectoryManifest, err)
	}
	created := make(map[string]struct{}, len(plannedDirectories))
	for _, record := range manifest {
		if record.Phase == "created" {
			if record.Identity == "" {
				t.Fatalf("created directory has no durable identity: %+v", record)
			}
			created[record.Path] = struct{}{}
		}
	}
	for _, path := range plannedDirectories {
		if _, ok := created[path]; !ok {
			t.Fatalf("crash journal is missing the created identity for %q: %+v", path, manifest)
		}
	}
	currentRuntime, err := registry.ReadRuntimeSettings(ctx)
	if err != nil || currentRuntime.OutputDirectory != runtimeSettings.OutputDirectory || currentRuntime.ToolsDirectory != runtimeSettings.ToolsDirectory {
		t.Fatalf("runtime changed before child kill: settings=%+v err=%v", currentRuntime, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child before commit: %v; child stderr: %s", err, stderr.String())
	}
	if err := wait(); err == nil {
		reaped = true
		t.Fatalf("child exited successfully instead of being killed; child stderr: %s", stderr.String())
	} else {
		reaped = true
	}
	reset := service.NewOutputReset(repository, settings.NewResetFilesystem(), nil)
	for i := 0; i < 2; i++ {
		if err := reset.RecoverOutputReset(ctx); err != nil {
			t.Fatalf("recovery %d: %v", i+1, err)
		}
	}
	for _, record := range manifest {
		if record.Phase != "created" {
			continue
		}
		if _, err := os.Stat(record.Path); !os.IsNotExist(err) {
			t.Fatalf("crash-created owned directory %q survived recovery: %v", record.Path, err)
		}
	}
	if _, err := os.Stat(newRoot); !os.IsNotExist(err) {
		t.Fatalf("crash-created output root survived recovery: %v", err)
	}
	if info, err := os.Stat(foreign); err != nil || !info.IsDir() {
		t.Fatalf("foreign directory changed: info=%v err=%v", info, err)
	}
	if data, err := os.ReadFile(filepath.Join(oldRoot, "keep")); err != nil || string(data) != "old output" {
		t.Fatalf("old output changed: contents=%q err=%v", data, err)
	}
	var state string
	if err := database.NewRaw("SELECT state FROM output_reset_journal WHERE token = ?", journal.Token).Scan(ctx, &state); err != nil || state != "finished" {
		t.Fatalf("journal state after repeated recovery=%q err=%v", state, err)
	}
}

// lockedBuffer serializes the child process writes into cmd.Stderr with the
// parent test goroutine reads that inspect the captured output while the child
// is still running. The embedded-free design avoids bytes.Buffer's
// thread-unsafe methods being reached through method promotion.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
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
