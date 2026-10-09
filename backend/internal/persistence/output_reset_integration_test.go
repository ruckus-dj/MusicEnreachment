//go:build integration

package persistence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func resetBoolPointer(value bool) *bool { return &value }

func TestRunOutputResetJournalsAndCommitsAtomically(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	initializeRiver(t, ctx, database)
	settings := NewSettingsRepository(database)
	if err := settings.Set(ctx, "output_directory", "/music/output-old"); err != nil {
		t.Fatal(err)
	}
	repository := NewSetupManagerRepository(database)
	queued := &Operation{ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: "queued", InputSnapshot: []byte(`{}`)}
	if err := repository.CreateOperation(ctx, queued); err != nil {
		t.Fatalf("create queued operation: %v", err)
	}
	created := false
	token, err := repository.RunOutputReset(ctx, "/music/output-old", "/music/output-new",
		func(_ context.Context, record func(string) error) error {
			created = true
			return record("/music/output-new/owned")
		}, func(_ context.Context, journal OutputResetJournal) error {
			if journal.Token == uuid.Nil || journal.State != "committed" || len(journal.CreatedDirectories) != 1 || journal.CreatedDirectories[0] != "/music/output-new/owned" {
				t.Fatalf("finish journal = %#v", journal)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !created || token == uuid.Nil {
		t.Fatalf("reset callback/token = %v/%v", created, token)
	}
	value, found, err := settings.Get(ctx, "output_directory")
	if err != nil || !found || value != "/music/output-new" {
		t.Fatalf("output directory = %q, %v, %v", value, found, err)
	}
	failed, err := repository.GetOperation(ctx, queued.ID)
	if err != nil || failed.State != "failed" || failed.SafeError == nil || *failed.SafeError == "" {
		t.Fatalf("queued operation after reset = %#v, %v; want failed with a safe error", failed, err)
	}
	if err := settings.Set(ctx, "output_directory", "/music/output-bypass"); err == nil {
		t.Fatal("direct output-root change succeeded")
	}
	if err := settings.Set(ctx, "output_directory", "/music/output-new"); err != nil {
		t.Fatalf("same-path setting update: %v", err)
	}
	var state string
	if err := database.NewRaw("SELECT state FROM output_reset_journal WHERE token = ?", token).Scan(ctx, &state); err != nil || state != "finished" {
		t.Fatalf("journal state = %q, %v", state, err)
	}
}

func TestOutputResetPersistsOnlyPreflightedMissingAncestorChain(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	initializeRiver(t, ctx, database)
	existing, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(existing, "missing", "output")
	filesystem := settings.NewResetFilesystem()
	planned, err := filesystem.PlannedDirectories(newRoot, []string{newRoot})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewSetupManagerRepository(database)
	_, err = repository.RunOutputResetRequest(ctx, OutputResetRequest{
		NewRoot:            newRoot,
		AllowedDirectories: planned,
		Prepare: func(ctx context.Context, record func(OutputResetDirectory) error) error {
			return filesystem.PrepareDurable(ctx, newRoot, []string{newRoot}, func(entry settings.ResetDirectoryRecord) error {
				return record(OutputResetDirectory{Path: entry.Path, Phase: entry.Phase, Identity: entry.Identity})
			})
		},
		Finish: func(context.Context, OutputResetJournal) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(newRoot); err != nil || !info.IsDir() {
		t.Fatalf("new output root was not prepared: %v", err)
	}
	if err := filesystem.RollbackManifestForRoot(ctx, newRoot, nil); err != nil {
		t.Fatal(err)
	}
}

func TestOutputResetRejectsUnplannedMissingAncestorWithoutCreatingIt(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	initializeRiver(t, ctx, database)
	settings := NewSettingsRepository(database)
	if err := settings.Set(ctx, "output_directory", "/music/output-old"); err != nil {
		t.Fatal(err)
	}
	existing, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ancestor := filepath.Join(existing, "unplanned")
	newRoot := filepath.Join(ancestor, "output")
	repository := NewSetupManagerRepository(database)

	token, err := repository.RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedOldRoot: "/music/output-old",
		NewRoot:         newRoot,
		Prepare: func(_ context.Context, record func(OutputResetDirectory) error) error {
			return record(OutputResetDirectory{Path: ancestor, Phase: "intent"})
		},
		Finish: func(context.Context, OutputResetJournal) error { return nil },
	})
	if err == nil {
		t.Fatal("reset accepted an ancestor absent from the planned directory list")
	}
	if !strings.Contains(err.Error(), "output directory record must be a normalized descendant of the new output root or a planned ancestor") {
		t.Fatalf("reset error = %v, want rejection of an unplanned directory", err)
	}
	if token == uuid.Nil {
		t.Fatal("failed reset did not return its journal token")
	}
	if _, err := os.Lstat(ancestor); !os.IsNotExist(err) {
		t.Fatalf("unplanned ancestor status = %v, want it to remain absent", err)
	}
	if value, found, err := settings.Get(ctx, "output_directory"); err != nil || !found || value != "/music/output-old" {
		t.Fatalf("output directory after rejected reset = %q, %v, %v; want unchanged", value, found, err)
	}
	journal, err := repository.ReadUnresolvedOutputReset(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if journal == nil || journal.Token != token || journal.State != "preparing" {
		t.Fatalf("unresolved journal = %#v, want failed preparation journal %v", journal, token)
	}
	if err := repository.RecoverOutputReset(ctx, func(context.Context, OutputResetJournal) error { return nil }); err != nil {
		t.Fatalf("recover rejected reset journal: %v", err)
	}
}

func TestOutputResetFailureBlocksAdmissionsUntilRecovery(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	initializeRiver(t, ctx, database)
	settings := NewSettingsRepository(database)
	if err := settings.Set(ctx, "output_directory", "/music/output-old"); err != nil {
		t.Fatal(err)
	}
	repository := NewSetupManagerRepository(database)
	prepareErr := errors.New("injected preparation failure")
	token, err := repository.RunOutputReset(ctx, "/music/output-old", "/music/output-new",
		func(_ context.Context, _ func(string) error) error { return prepareErr }, func(context.Context, OutputResetJournal) error { return nil })
	if !errors.Is(err, prepareErr) {
		t.Fatalf("reset error = %v, want %v", err, prepareErr)
	}
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error { return AcquireOutputAdmissionGate(ctx, tx) }); err == nil {
		t.Fatal("output admission succeeded with an unresolved preparing journal")
	}
	if err := repository.RecoverOutputReset(ctx, func(_ context.Context, journal OutputResetJournal) error {
		if journal.Token != token || journal.State != "preparing" {
			t.Fatalf("recovery journal = %#v", journal)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error { return AcquireOutputAdmissionGate(ctx, tx) }); err != nil {
		t.Fatalf("output admission after recovery: %v", err)
	}
}

func TestRunOutputResetAllowsInitialOutputDirectoryAndRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	initializeRiver(t, ctx, database)
	settings := NewSettingsRepository(database)
	repository := NewSetupManagerRepository(database)

	// An unconfigured installation may initialize its output root through reset.
	token, err := repository.RunOutputReset(ctx, "", "/music/initial",
		func(context.Context, func(string) error) error { return nil },
		func(_ context.Context, journal OutputResetJournal) error {
			if journal.State != "committed" {
				t.Fatalf("initialization journal state = %q", journal.State)
			}
			return nil
		})
	if err != nil || token == uuid.Nil {
		t.Fatalf("initialize output directory = %v, %v", token, err)
	}
	value, found, err := settings.Get(ctx, "output_directory")
	if err != nil || !found || value != "/music/initial" {
		t.Fatalf("initialized output directory = %q, %v, %v", value, found, err)
	}
	if err := settings.Set(ctx, "output_directory", "/music/bypass"); err == nil {
		t.Fatal("direct output-root change succeeded")
	}
	if err := settings.Set(ctx, "output_directory", "/music/initial"); err != nil {
		t.Fatalf("same-path setting update: %v", err)
	}

	tests := []struct {
		name, oldRoot, newRoot string
		prepare                func(context.Context, func(string) error) error
		finish                 func(context.Context, OutputResetJournal) error
	}{
		{name: "same path", oldRoot: "/music/initial", newRoot: "/music/initial", prepare: func(context.Context, func(string) error) error { return nil }, finish: func(context.Context, OutputResetJournal) error { return nil }},
		{name: "relative path", oldRoot: "/music/initial", newRoot: "relative", prepare: func(context.Context, func(string) error) error { return nil }, finish: func(context.Context, OutputResetJournal) error { return nil }},
		{name: "missing prepare callback", oldRoot: "/music/initial", newRoot: "/music/next", finish: func(context.Context, OutputResetJournal) error { return nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := repository.RunOutputReset(ctx, test.oldRoot, test.newRoot, test.prepare, test.finish)
			if err == nil {
				t.Fatal("invalid reset request succeeded")
			}
		})
	}
}

func TestOutputResetRejectsStaleToolsRootWithoutChangingRuntimeSettings(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	initializeRiver(t, ctx, database)
	settings := NewSettingsRepository(database)
	for name, value := range map[string]string{
		"tools_directory":              "/music/tools-current",
		"output_directory":             "/music/output-current",
		"output_case_sensitive":        "true",
		"output_unicode_normalization": "none",
		"publication_format":           "source",
	} {
		if err := settings.Set(ctx, name, value); err != nil {
			t.Fatalf("set %s: %v", name, err)
		}
	}
	before := runtimeSettingsSnapshot(t, ctx, database)
	_, err := NewSetupManagerRepository(database).RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedOldRoot:   "/music/output-current",
		ExpectedToolsRoot: "/music/tools-stale",
		NewRoot:           "/music/output-next",
		RuntimeValues: map[string]string{
			"tools_directory":              "/music/tools-next",
			"output_case_sensitive":        "false",
			"output_unicode_normalization": "nfc",
			"publication_format":           "mka",
		},
		Prepare: func(context.Context, func(OutputResetDirectory) error) error { return nil },
		Finish:  func(context.Context, OutputResetJournal) error { return nil },
	})
	if err == nil {
		t.Fatal("reset with a stale tools root succeeded")
	}
	if after := runtimeSettingsSnapshot(t, ctx, database); !equalStringMaps(before, after) {
		t.Fatalf("runtime settings after stale request = %#v, want unchanged %#v", after, before)
	}
	var journals int
	if err := database.NewRaw("SELECT count(*) FROM output_reset_journal").Scan(ctx, &journals); err != nil || journals != 0 {
		t.Fatalf("journal count = %d, %v; want no preparing journal", journals, err)
	}
}

func TestOutputResetCommitsRuntimeSemanticsTogetherWithOutputRoot(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	initializeRiver(t, ctx, database)
	settings := NewSettingsRepository(database)
	for name, value := range map[string]string{
		"tools_directory":              "/music/tools-current",
		"output_directory":             "/music/output-current",
		"output_case_sensitive":        "true",
		"output_unicode_normalization": "none",
		"publication_format":           "source",
	} {
		if err := settings.Set(ctx, name, value); err != nil {
			t.Fatalf("set %s: %v", name, err)
		}
	}
	_, err := NewSetupManagerRepository(database).RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedOldRoot:   "/music/output-current",
		ExpectedToolsRoot: "/music/tools-current",
		NewRoot:           "/music/output-next",
		RuntimeValues: map[string]string{
			"tools_directory":    "/music/tools-next",
			"publication_format": "mka",
		},
		OutputCaseSensitive:        resetBoolPointer(false),
		OutputUnicodeNormalization: stringPointer("nfc"),
		Prepare:                    func(context.Context, func(OutputResetDirectory) error) error { return nil },
		Finish:                     func(context.Context, OutputResetJournal) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	got := runtimeSettingsSnapshot(t, ctx, database)
	want := map[string]string{
		"tools_directory":              "/music/tools-next",
		"output_directory":             "/music/output-next",
		"output_case_sensitive":        "false",
		"output_unicode_normalization": "nfc",
		"publication_format":           "mka",
	}
	if !equalStringMaps(got, want) {
		t.Fatalf("committed runtime settings = %#v, want %#v", got, want)
	}
}

func runtimeSettingsSnapshot(t *testing.T, ctx context.Context, database *bun.DB) map[string]string {
	t.Helper()
	var rows []AppSetting
	if err := database.NewSelect().Model(&rows).Where("setting_name IN (?)", bun.In([]string{
		"tools_directory", "output_directory", "output_case_sensitive", "output_unicode_normalization", "publication_format",
	})).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.Name] = row.Value
	}
	return values
}

func equalStringMaps(first, second map[string]string) bool {
	if len(first) != len(second) {
		return false
	}
	for key, value := range first {
		if second[key] != value {
			return false
		}
	}
	return true
}
