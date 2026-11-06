//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"

	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestOutputResetValidatesCompleteProposedRuntimeRootTuple(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, tools, work, output string
	}{
		{name: "tools-output overlap", tools: "/music/shared", work: "/music/work", output: "/music/shared/child"},
		{name: "work-output overlap", tools: "/music/tools", work: "/music/shared", output: "/music/shared/child"},
		{name: "work-tools overlap", tools: "/music/shared", work: "/music/shared/child", output: "/music/output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := testpostgres.OpenMigratedWithRiver(t)
			ctx := context.Background()
			initializeRiver(t, ctx, database)
			settings := NewSettingsRepository(database)
			oldOutput := nativeTestPath(t, "/music/output-old")
			for key, value := range map[string]string{
				"tools_directory":  nativeTestPath(t, "/music/old-tools"),
				"work_directory":   nativeTestPath(t, "/music/old-work"),
				"output_directory": oldOutput,
			} {
				if err := settings.Set(ctx, key, value); err != nil {
					t.Fatalf("set %s: %v", key, err)
				}
			}
			prepared := false
			_, err := NewSetupManagerRepository(database).RunOutputResetRequest(ctx, OutputResetRequest{
				ExpectedToolsRoot: nativeTestPath(t, "/music/old-tools"), ExpectedOldRoot: oldOutput,
				NewRoot: nativeTestPath(t, test.output), RuntimeValues: map[string]string{
					"tools_directory":  nativeTestPath(t, test.tools),
					"work_directory":   nativeTestPath(t, test.work),
					"output_directory": nativeTestPath(t, test.output),
				},
				Prepare: func(context.Context, func(OutputResetDirectory) error) error { prepared = true; return nil },
				Finish:  func(context.Context, OutputResetJournal) error { return nil },
			})
			if err == nil || prepared {
				t.Fatalf("overlapping proposed roots were accepted (prepared=%v)", prepared)
			}
			for key, want := range map[string]string{
				"tools_directory":  nativeTestPath(t, "/music/old-tools"),
				"work_directory":   nativeTestPath(t, "/music/old-work"),
				"output_directory": oldOutput,
			} {
				got, found, getErr := settings.Get(ctx, key)
				if getErr != nil || !found || got != want {
					t.Fatalf("setting %s = %q, %v, %v; want %q", key, got, found, getErr, want)
				}
			}
		})
	}
}

func TestOutputResetRejectsWorkDirectoryChangeWithRetainedAnalysisOperation(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	initializeRiver(t, ctx, database)
	settings := NewSettingsRepository(database)
	oldOutput := nativeTestPath(t, "/music/output-old")
	oldWork := nativeTestPath(t, "/music/old-work")
	tools := nativeTestPath(t, "/music/tools")
	for key, value := range map[string]string{"tools_directory": tools, "work_directory": oldWork, "output_directory": oldOutput} {
		if err := settings.Set(ctx, key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	root := &SourceRoot{
		ID: uuid.New(), DisplayName: "Analysis fixture", ConfiguredPath: nativeTestPath(t, "/music/source"),
		ProcessingMode: "in_place", Enabled: true,
	}
	if err := NewSourceInventoryRepository(database).CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create source root: %v", err)
	}
	if _, err := database.NewRaw(`UPDATE source_root SET inventory_path=configured_path,status='available',scan_generation=1,
		last_successful_scan_at=now() WHERE id=?`, root.ID).Exec(ctx); err != nil {
		t.Fatalf("establish source inventory: %v", err)
	}
	location := &SourceLocation{
		ID: uuid.New(), SourceRootID: root.ID, RelativePath: "track.flac", SizeBytes: 128,
		Mtime: time.Now().UTC().Truncate(time.Microsecond), LastSeenScanGeneration: 1, ProbeStatus: "not_analyzed",
	}
	if _, err := database.NewInsert().Model(location).Exec(ctx); err != nil {
		t.Fatalf("create source location: %v", err)
	}
	scanOperation := &Operation{ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: "queued", InputSnapshot: []byte(`{}`)}
	if err := NewSetupManagerRepository(database).CreateOperation(ctx, scanOperation); err != nil {
		t.Fatalf("create origin scan operation: %v", err)
	}
	workID := uuid.New()
	work := &SourceAnalysisWork{
		ID: workID, LocationID: location.ID, SourceRootID: root.ID, ConfiguredPath: root.ConfiguredPath,
		InventoryPath: root.ConfiguredPath, RelativePath: location.RelativePath, SizeBytes: location.SizeBytes,
		Mtime: location.Mtime, SHA256Enabled: true, OriginScanOperationID: scanOperation.ID,
	}
	if err := NewSourceInventoryRepository(database).StoreSourceAnalysisWork(ctx, work,
		[]SourceAnalysisStepInput{{Step: SourceStepSHA256, State: "pending"}}); err != nil {
		t.Fatalf("store active source analysis work: %v", err)
	}
	selectedSteps := []SourceAnalysisStepSelection{{WorkID: workID, Step: SourceStepSHA256}}
	snapshot, err := json.Marshal(SourceAnalysisOperationSnapshot{
		SchemaVersion: SourceAnalysisOperationSnapshotVersion, Mode: SourceAnalysisModeBatch,
		WorkIDs: []uuid.UUID{workID}, RerunTarget: resetBoolPointer(false), SelectedSteps: selectedSteps,
	})
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.New()
	operation := &Operation{
		ID: operationID, Kind: "analyze_source", State: "queued", Stage: "queued", Attempt: 1,
		TargetSourceRootID: &root.ID, SourceAnalysisMode: SourceAnalysisModeBatch, InputSnapshot: snapshot,
	}
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(operation).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`INSERT INTO operation_source_work_hold(operation_id, work_id) VALUES (?, ?)`, operationID, workID).Exec(ctx)
		return err
	}); err != nil {
		t.Fatalf("create active normalized analysis operation and work hold: %v", err)
	}
	prepared := false
	_, err = NewSetupManagerRepository(database).RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedToolsRoot: tools, ExpectedOldRoot: oldOutput, NewRoot: nativeTestPath(t, "/music/output-new"),
		RuntimeValues: map[string]string{"work_directory": nativeTestPath(t, "/music/new-work")},
		Prepare:       func(context.Context, func(OutputResetDirectory) error) error { prepared = true; return nil },
		Finish:        func(context.Context, OutputResetJournal) error { return nil },
	})
	if err == nil || prepared {
		t.Fatalf("work directory changed with retained operation (prepared=%v)", prepared)
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
