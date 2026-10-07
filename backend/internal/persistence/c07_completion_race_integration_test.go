//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// The completion hook pauses CompleteSetupIfCurrent after it has acquired its
// coordination locks and immediately before it writes the completion marker.
// Competing repository calls therefore have to wait in PostgreSQL, rather than
// merely being scheduled after completion has happened.
func TestC07CompletionSerializesPostSetupAdmission(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), analysisRaceTimeout)
	defer cancel()
	fixture := newC07CompletionFixture(t, ctx, db)
	client := c07RiverClient(t, ctx, db)

	gate := pauseC07Completion(t, db)
	completed := make(chan error, 1)
	go func() { completed <- fixture.store.CompleteSetupIfCurrent(ctx, fixture.expected, "done") }()
	awaitSignal(t, gate.entered, "completion to reach its final setting write")

	admitted := make(chan *persistence.Operation, 1)
	go func() {
		admitted <- c07Admission(t, ctx, fixture.repo, client, fixture.toolsRoot, "ffmpeg", "post-setup")
	}()
	awaitAnyPostgresLockWait(t, ctx, db)
	close(gate.release)
	if err := awaitResult(t, completed, "setup completion"); err != nil {
		t.Fatalf("complete setup: %v", err)
	}
	op := c07AwaitValue(t, admitted, "post-setup admission")
	if op == nil {
		t.Fatal("admission after setup completion was refused")
	}
	assertC07CompletionRows(t, ctx, db, 3, 1)
	assertC07ActiveIDs(t, ctx, db, fixture.ffmpegID, fixture.fpcalcID)
}

func TestC07CompletionSerializesPostSetupFinalization(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), analysisRaceTimeout)
	defer cancel()
	fixture := newC07CompletionFixture(t, ctx, db)
	client := c07RiverClient(t, ctx, db)
	const completedAt = "2026-10-08T12:00:00Z"
	if err := fixture.store.Set(ctx, settings.SetupCompletedAtKey, completedAt); err != nil {
		t.Fatalf("seed existing completion marker: %v", err)
	}
	op := c07Admission(t, ctx, fixture.repo, client, fixture.toolsRoot, "fpcalc", "post-setup")
	if op == nil {
		t.Fatal("post-setup admission was refused")
	}

	gate := pauseC07Query(t, db, "setup_completed_at")
	completed := make(chan error, 1)
	go func() { completed <- fixture.store.CompleteSetupIfCurrent(ctx, fixture.expected, "done") }()
	awaitSignal(t, gate.entered, "idempotent completion to reach its existing marker")

	finalized := make(chan error, 1)
	go func() {
		_, err := fixture.repo.FinalizeInstallation(ctx, op.ID, op.Attempt, *op.RiverJobID,
			*op.TargetInstallationID, "fpcalc", "linux", "amd64", settings.ActiveFPCalcInstallationKey,
			json.RawMessage(`{"fpcalc":"synthetic"}`), time.Now().UTC())
		finalized <- err
	}()
	awaitAnyPostgresLockWait(t, ctx, db)
	close(gate.release)
	if err := awaitResult(t, completed, "setup completion"); err != nil {
		t.Fatalf("complete setup: %v", err)
	}
	if err := awaitResult(t, finalized, "post-setup finalization"); err != nil {
		t.Fatalf("finalize post-setup package: %v", err)
	}
	assertC07CompletionRows(t, ctx, db, 3, 1)
	assertC07ActiveIDs(t, ctx, db, fixture.ffmpegID, fixture.fpcalcID)
	var persistedCompletedAt string
	if err := db.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", settings.SetupCompletedAtKey).Scan(ctx, &persistedCompletedAt); err != nil || persistedCompletedAt != completedAt {
		t.Fatalf("completion timestamp = %q, %v; want preserved %q", persistedCompletedAt, err, completedAt)
	}
	installation, err := fixture.repo.GetInstallation(ctx, *op.TargetInstallationID)
	if err != nil || installation.State != "ready" {
		t.Fatalf("additional installation = %#v, %v; want ready", installation, err)
	}
}

func TestC07InitialFinalizationBeforeCompletionUsesFreshActiveIDs(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), analysisRaceTimeout)
	defer cancel()
	fixture := newC07CompletionFixtureWithoutFPCalc(t, ctx, db)
	client := c07RiverClient(t, ctx, db)
	op := c07Admission(t, ctx, fixture.repo, client, fixture.toolsRoot, "fpcalc", "initial")
	if op == nil {
		t.Fatal("initial fpcalc admission was refused")
	}
	fixture.fpcalcID = *op.TargetInstallationID
	fixture.expected[settings.ActiveFPCalcInstallationKey] = fixture.fpcalcID.String()
	gate := pauseC07Query(t, db, `UPDATE "tool_installation"`)
	finalized := make(chan error, 1)
	go func() {
		_, err := fixture.repo.FinalizeInstallation(ctx, op.ID, op.Attempt, *op.RiverJobID,
			*op.TargetInstallationID, "fpcalc", "linux", "amd64", settings.ActiveFPCalcInstallationKey,
			json.RawMessage(`{"fpcalc":"synthetic"}`), time.Now().UTC())
		finalized <- err
	}()
	awaitSignal(t, gate.entered, "initial finalization to update its installation")
	completed := make(chan error, 1)
	go func() { completed <- fixture.store.CompleteSetupIfCurrent(ctx, fixture.expected, "done") }()
	awaitAnyPostgresLockWait(t, ctx, db)
	close(gate.release)
	if err := awaitResult(t, finalized, "initial finalization"); err != nil {
		t.Fatalf("finalize initial fpcalc: %v", err)
	}
	if err := awaitResult(t, completed, "setup completion"); err != nil {
		t.Fatalf("complete setup against the newly activated installation: %v", err)
	}
	assertC07ActiveIDs(t, ctx, db, fixture.ffmpegID, fixture.fpcalcID)
}

func TestC07PreCompletionSecondPackageAdmissionLeavesNoOrphans(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newC07CompletionFixture(t, ctx, db)
	client := c07RiverClient(t, ctx, db)
	if op := c07Admission(t, ctx, fixture.repo, client, fixture.toolsRoot, "ffmpeg", "second-initial"); op != nil {
		t.Fatalf("second initial ffmpeg admission succeeded before completion: %s", op.ID)
	}
	assertC07CompletionRows(t, ctx, db, 2, 0)
	if err := fixture.store.CompleteSetupIfCurrent(ctx, fixture.expected, "done"); err != nil {
		t.Fatalf("complete setup after refusing duplicate initial admission: %v", err)
	}
	assertC07CompletionRows(t, ctx, db, 2, 0)
	assertC07ActiveIDs(t, ctx, db, fixture.ffmpegID, fixture.fpcalcID)
}

type c07CompletionFixture struct {
	store     *persistence.SettingsRepository
	repo      *persistence.SetupManagerRepository
	toolsRoot string
	ffmpegID  uuid.UUID
	fpcalcID  uuid.UUID
	expected  map[string]string
}

func newC07CompletionFixture(t *testing.T, ctx context.Context, db *bun.DB) c07CompletionFixture {
	t.Helper()
	fixture := newC07CompletionFixtureWithoutFPCalc(t, ctx, db)
	fpcalc := c07VerifiedReadyInstallation(t, ctx, fixture.repo, "fpcalc", "1.6", json.RawMessage(`{"fpcalc":"1.6"}`))
	activated, err := fixture.repo.ActivateInstallationDuringSetup(ctx, fpcalc.ID, "fpcalc", "linux", "amd64", settings.ActiveFPCalcInstallationKey)
	if err != nil || !activated {
		t.Fatalf("activate initial fpcalc: activated=%t err=%v", activated, err)
	}
	fixture.fpcalcID = fpcalc.ID
	fixture.expected[settings.ActiveFPCalcInstallationKey] = fpcalc.ID.String()
	return fixture
}

func newC07CompletionFixtureWithoutFPCalc(t *testing.T, ctx context.Context, db *bun.DB) c07CompletionFixture {
	t.Helper()
	store := persistence.NewSettingsRepository(db)
	repo := persistence.NewSetupManagerRepository(db)
	root := c07ToolsRoot(t, ctx, db)
	values := map[string]string{
		settings.PlatformGOOSKey: "linux", settings.PlatformGOARCHKey: "amd64",
		settings.ToolsDirectoryKey: root, settings.OutputDirectoryKey: c07NormalizedTempDir(t),
		settings.OutputCaseSensitiveKey: "true", settings.OutputUnicodeNormalizationKey: "none",
		settings.PublicationFormatKey: "mka", settings.MusicBrainzModeKey: "public",
		settings.MusicBrainzBaseURLKey: "", settings.MusicBrainzConfigIdentityKey: "",
		settings.MusicBrainzVerifiedAtKey: time.Now().UTC().Format(time.RFC3339Nano),
	}
	for key, value := range values {
		if key == settings.ToolsDirectoryKey {
			continue // c07ToolsRoot already seeded this value.
		}
		if err := store.Set(ctx, key, value); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	ffmpeg := c07VerifiedReadyInstallation(t, ctx, repo, "ffmpeg", "8.0", json.RawMessage(`{"ffmpeg":"8.0","ffprobe":"8.0"}`))
	activated, err := repo.ActivateInstallationDuringSetup(ctx, ffmpeg.ID, "ffmpeg", "linux", "amd64", settings.ActiveFFmpegInstallationKey)
	if err != nil || !activated {
		t.Fatalf("activate initial ffmpeg: activated=%t err=%v", activated, err)
	}
	expected := map[string]string{
		settings.PlatformGOOSKey: "linux", settings.PlatformGOARCHKey: "amd64",
		settings.ToolsDirectoryKey: root, settings.OutputDirectoryKey: values[settings.OutputDirectoryKey],
		settings.OutputCaseSensitiveKey: "true", settings.OutputUnicodeNormalizationKey: "none",
		settings.PublicationFormatKey: "mka", settings.ActiveFFmpegInstallationKey: ffmpeg.ID.String(),
		settings.MusicBrainzModeKey: "public", settings.MusicBrainzBaseURLKey: "",
		settings.MusicBrainzConfigIdentityKey: "",
	}
	return c07CompletionFixture{store: store, repo: repo, toolsRoot: root, ffmpegID: ffmpeg.ID, expected: expected}
}

func c07NormalizedTempDir(t *testing.T) string {
	t.Helper()
	path, err := settings.NormalizePath(t.TempDir())
	if err != nil {
		t.Fatalf("normalize output directory: %v", err)
	}
	return path
}

func assertC07CompletionRows(t *testing.T, ctx context.Context, db *bun.DB, installations, operations int) {
	t.Helper()
	var gotInstallations, gotOperations, jobs int
	if err := db.NewRaw("SELECT count(*) FROM tool_installation").Scan(ctx, &gotInstallations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw("SELECT count(*) FROM operation").Scan(ctx, &gotOperations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw("SELECT count(*) FROM river_job WHERE kind = ?", "operation_v1").Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if gotInstallations != installations || gotOperations != operations || jobs != operations {
		t.Fatalf("persisted installations/operations/operation jobs = %d/%d/%d; want %d/%d/%d", gotInstallations, gotOperations, jobs, installations, operations, operations)
	}
}

func c07VerifiedReadyInstallation(t *testing.T, ctx context.Context, repo *persistence.SetupManagerRepository, kind, release string, versions json.RawMessage) *persistence.ToolInstallation {
	t.Helper()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: kind, PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "test", ReleaseIdentity: release, RelativePath: kind + "/" + release, State: "preparing",
	}
	if err := repo.CreateInstallation(ctx, installation); err != nil {
		t.Fatalf("create %s installation: %v", kind, err)
	}
	if err := repo.MarkInstallationReady(ctx, installation.ID, versions, time.Now().UTC()); err != nil {
		t.Fatalf("mark %s installation ready: %v", kind, err)
	}
	return installation
}

type c07CompletionGate struct {
	entered chan struct{}
	release chan struct{}
}

type c07CompletionPause struct {
	gate   *c07CompletionGate
	needle string
	armed  atomic.Bool
	once   sync.Once
}

func pauseC07Completion(t *testing.T, db *bun.DB) *c07CompletionGate {
	t.Helper()
	return pauseC07Query(t, db, "musicbrainz_verified_at")
}

func pauseC07Query(t *testing.T, db *bun.DB, needle string) *c07CompletionGate {
	t.Helper()
	gate := &c07CompletionGate{entered: make(chan struct{}), release: make(chan struct{})}
	hook := &c07CompletionPause{gate: gate, needle: needle}
	db.AddQueryHook(hook)
	hook.armed.Store(true)
	t.Cleanup(func() {
		select {
		case <-gate.release:
		default:
			close(gate.release)
		}
	})
	return gate
}

func (h *c07CompletionPause) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if h.armed.Load() && strings.Contains(event.Query, h.needle) {
		h.once.Do(func() {
			close(h.gate.entered)
			<-h.gate.release
		})
	}
	return ctx
}

func (*c07CompletionPause) AfterQuery(context.Context, *bun.QueryEvent) {}

func awaitAnyPostgresLockWait(t *testing.T, ctx context.Context, db *bun.DB) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked int
		if err := db.NewRaw("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()").Scan(ctx, &blocked); err != nil {
			t.Fatalf("inspect PostgreSQL lock wait: %v", err)
		}
		if blocked > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("competing operation did not wait on PostgreSQL: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func c07AwaitValue[T any](t *testing.T, result <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(analysisRaceTimeout):
		var zero T
		t.Fatalf("timed out waiting for %s", what)
		return zero
	}
}

func assertC07ActiveIDs(t *testing.T, ctx context.Context, db *bun.DB, ffmpeg, fpcalc uuid.UUID) {
	t.Helper()
	for key, expected := range map[string]uuid.UUID{
		settings.ActiveFFmpegInstallationKey: ffmpeg,
		settings.ActiveFPCalcInstallationKey: fpcalc,
	} {
		var actual string
		if err := db.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", key).Scan(ctx, &actual); err != nil || actual != expected.String() {
			t.Errorf("active setting %s = %q, %v; want %s", key, actual, err, expected)
		}
	}
}
