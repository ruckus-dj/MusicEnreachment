//go:build integration

package persistence

import (
	"context"
	"errors"

	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func resetBoolPointer(value bool) *bool { return &value }

func nativeTestPath(t *testing.T, value string) string {
	t.Helper()
	path := filepath.FromSlash(value)
	if !strings.HasPrefix(value, "/") || filepath.VolumeName(path) != "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.VolumeName(t.TempDir()) + path)
}

func TestRunOutputResetRequestJournalsAndCommitsAtomically(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	initializeRiver(t, ctx, database)
	settings := NewSettingsRepository(database)
	if err := settings.Set(ctx, "output_directory", nativeTestPath(t, "/music/output-old")); err != nil {
		t.Fatal(err)
	}
	repository := NewSetupManagerRepository(database)
	queued := &Operation{ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: "queued", InputSnapshot: []byte(`{}`)}
	if err := repository.CreateOperation(ctx, queued); err != nil {
		t.Fatalf("create queued operation: %v", err)
	}
	created := false
	token, err := repository.RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedOldRoot: nativeTestPath(t, "/music/output-old"), NewRoot: nativeTestPath(t, "/music/output-new"),
		Prepare: func(_ context.Context, record func(OutputResetDirectory) error) error {
			created = true
			return record(OutputResetDirectory{Path: nativeTestPath(t, "/music/output-new/owned"), Phase: "intent"})
		}, Finish: func(_ context.Context, journal OutputResetJournal) error {
			if journal.Token == uuid.Nil || journal.State != "committed" || len(journal.CreatedDirectories) != 1 || journal.CreatedDirectories[0] != nativeTestPath(t, "/music/output-new/owned") {
				t.Fatalf("finish journal = %#v", journal)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created || token == uuid.Nil {
		t.Fatalf("reset callback/token = %v/%v", created, token)
	}
	var outputEpoch uuid.UUID
	if err := database.QueryRowContext(ctx, `SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(&outputEpoch); err != nil {
		t.Fatalf("read publication output epoch: %v", err)
	}
	if outputEpoch != token {
		t.Fatalf("publication output epoch = %s, want reset token %s", outputEpoch, token)
	}
	value, found, err := settings.Get(ctx, "output_directory")
	if err != nil || !found || value != nativeTestPath(t, "/music/output-new") {
		t.Fatalf("output directory = %q, %v, %v", value, found, err)
	}
	failed, err := repository.GetOperation(ctx, queued.ID)
	if err != nil || failed.State != "failed" || failed.SafeError == nil || *failed.SafeError == "" {
		t.Fatalf("queued operation after reset = %#v, %v; want failed with a safe error", failed, err)
	}
	if err := settings.Set(ctx, "output_directory", nativeTestPath(t, "/music/output-bypass")); err == nil {
		t.Fatal("direct output-root change succeeded")
	}
	if err := settings.Set(ctx, "output_directory", nativeTestPath(t, "/music/output-new")); err != nil {
		t.Fatalf("same-path setting update: %v", err)
	}
	var state string
	if err := database.NewRaw("SELECT state FROM output_reset_journal WHERE token = ?", token).Scan(ctx, &state); err != nil || state != "finished" {
		t.Fatalf("journal state = %q, %v", state, err)
	}
}

func TestOutputResetFailureBlocksAdmissionsUntilRecovery(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	initializeRiver(t, ctx, database)
	settings := NewSettingsRepository(database)
	if err := settings.Set(ctx, "output_directory", nativeTestPath(t, "/music/output-old")); err != nil {
		t.Fatal(err)
	}
	repository := NewSetupManagerRepository(database)
	prepareErr := errors.New("injected preparation failure")
	token, err := repository.RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedOldRoot: nativeTestPath(t, "/music/output-old"), NewRoot: nativeTestPath(t, "/music/output-new"),
		Prepare: func(_ context.Context, _ func(OutputResetDirectory) error) error { return prepareErr },
		Finish:  func(context.Context, OutputResetJournal) error { return nil },
	})
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
