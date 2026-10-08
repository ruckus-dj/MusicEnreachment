//go:build integration

package migrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/migrate"
)

// analysisVariantMigration is the version (file-name timestamp) of the saved
// analysis-result migration whose up and down pair this test exercises. The
// rollback test isolates exactly this migration by name, never "the last element
// of the collection", so a later migration cannot silently change what it runs.
const analysisVariantMigration = "20261005000000"
const automaticSourceAnalysisMigration = "20261006000000"

func TestAutomaticSourceAnalysisRollbackRefusesStoredResultsWithoutSchemaChangesWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, automaticSourceAnalysisMigration))
	migration := migrationNamed(t, collection, automaticSourceAnalysisMigration)
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	digest := make([]byte, 32)
	digest[0] = 0x5a
	variantID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES(?,4096,?,now(),'SHA-256',?)`, variantID, digest, uuid.New()); err != nil {
		t.Fatalf("insert populated analysis result: %v", err)
	}

	rollback := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if _, err := rollback.Rollback(ctx); err == nil {
		t.Fatal("rollback with persisted analysis results succeeded, want refusal")
	}
	if relation := relationName(t, database, "media_variant"); relation == nil {
		t.Fatal("media_variant was dropped despite rollback refusal")
	}
	if !columnExists(t, database, "media_variant", "source_sha256") {
		t.Fatal("source_sha256 column was removed despite rollback refusal")
	}
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM media_variant WHERE id=? AND source_sha256=?`, variantID, digest).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("stored result after refused rollback = %d, %v; want it unchanged", count, err)
	}
}

// TestSourceMediaVariantMigrationRollbackWithPostgreSQL applies every migration
// before the analysis-result pair, then the pair as its own migration group, and
// proves the down migration restores the pre-analysis schema: no media_variant
// table, no new operation columns, no location variant column, and the old kind
// check and root guard back in place.
func TestSourceMediaVariantMigrationRollbackWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()

	full, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	found := false
	for _, migration := range full.Sorted() {
		if migration.Name == analysisVariantMigration {
			found = true
		}
	}
	if !found {
		t.Fatalf("migration %s is missing from the collection", analysisVariantMigration)
	}

	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, full, analysisVariantMigration))
	if relation := relationName(t, database, "media_variant"); relation != nil {
		t.Fatalf("media_variant exists before its migration: %s", *relation)
	}

	// Keep this rollback focused on the migration under test. Later migrations
	// intentionally add dependencies to the schema and must not be batched here.
	migration := migrationNamed(t, full, analysisVariantMigration)
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	if relation := relationName(t, database, "media_variant"); relation == nil {
		t.Fatal("media_variant is missing after its migration")
	}
	for _, column := range []string{"size_bytes", "analysis_policy_version", "ffprobe_version", "ffprobe_json", "observed_tags", "inspected_at", "applied_operation_id"} {
		if !columnExists(t, database, "media_variant", column) {
			t.Fatalf("media_variant.%s is missing after its migration", column)
		}
	}
	for _, column := range []string{"target_source_location_id", "analysis_installation_id", "analysis_media_variant_id"} {
		if !columnExists(t, database, "operation", column) {
			t.Fatalf("operation.%s is missing after its migration", column)
		}
	}
	if !columnExists(t, database, "source_location", "media_variant_id") {
		t.Fatal("source_location.media_variant_id is missing after its migration")
	}

	rollbackMigration(t, ctx, database, migration)

	if relation := relationName(t, database, "media_variant"); relation != nil {
		t.Fatalf("media_variant survives the rollback: %s", *relation)
	}
	for _, column := range []string{"target_source_location_id", "analysis_installation_id", "analysis_media_variant_id"} {
		if columnExists(t, database, "operation", column) {
			t.Fatalf("operation.%s survives the rollback", column)
		}
	}
	if columnExists(t, database, "source_location", "media_variant_id") {
		t.Fatal("source_location.media_variant_id survives the rollback")
	}
	kindConstraint := constraintDefinition(t, database, "operation_kind_check")
	if strings.Contains(kindConstraint, "analyze_source") {
		t.Fatalf("operation kind constraint after rollback = %s, want no analyze_source kind", kindConstraint)
	}
	if !indexExists(t, database, "operation_one_active_source_root_scan") {
		t.Fatal("the restored scan-only root guard index is missing after the rollback")
	}
	if indexExists(t, database, "operation_one_active_source_root_operation") {
		t.Fatal("the combined root guard index survives the rollback")
	}
	if constraintDefinition(t, database, "operation_active_tools_operations_exclusive") == "" {
		t.Fatal("the exclusion constraint is missing after the rollback")
	}
}

// TestSourceMediaVariantSchemaWithPostgreSQL exercises the schema the way the
// later steps will: nullable location links, media_variant value checks, the
// analyze_source target shape, root exclusivity across scan and analysis, the
// resource holds, and the untouched install/move constraints.
func TestSourceMediaVariantSchemaWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()

	t.Run("locations_start_without_a_variant", func(t *testing.T) {
		rootID := newVariantRoot(t, ctx, database, "/srv/variant-null")
		locationID := newVariantLocation(t, ctx, database, rootID, "album/track.flac")
		var variantID *uuid.UUID
		if err := database.NewRaw("SELECT media_variant_id FROM source_location WHERE id = ?", locationID).Scan(ctx, &variantID); err != nil {
			t.Fatalf("read the initial variant link: %v", err)
		}
		if variantID != nil {
			t.Fatalf("a freshly scanned location has variant %s, want NULL", *variantID)
		}
		var variants int
		if err := database.NewRaw("SELECT count(*) FROM media_variant").Scan(ctx, &variants); err != nil {
			t.Fatalf("count variants: %v", err)
		}
		if variants != 0 {
			t.Fatalf("the migration created %d artificial variants, want 0", variants)
		}
	})

	t.Run("media_variant_value_checks", func(t *testing.T) {
		operationID := newTerminalAnalysisOperation(t, ctx, database, newVariantRoot(t, ctx, database, "/srv/variant-values"))
		valid := newVariant(t, ctx, database, 0, operationID)
		if valid == uuid.Nil {
			t.Fatal("a variant with size 0 was not stored")
		}
		requireViolation(t, tryVariant(t, ctx, database, -1, operationID),
			"media_variant_size_not_negative")
		requireViolation(t, tryVariantPolicyVersion(t, ctx, database, 0, operationID),
			"media_variant_analysis_policy_version_positive")
		requireViolation(t, tryVariantVersion(t, ctx, database, ""),
			"media_variant_ffprobe_version_not_empty")
		if err := tryVariantSHAOnly(t, ctx, database); err != nil {
			t.Fatalf("insert SHA-only variant without probe provenance: %v", err)
		}
		var nullableProbeRows int
		if err := database.NewRaw(`SELECT count(*) FROM media_variant WHERE ffprobe_version IS NULL AND ffprobe_json IS NULL AND analysis_policy_version IS NULL AND observed_tags IS NULL AND inspected_at IS NULL AND applied_operation_id IS NULL`).Scan(ctx, &nullableProbeRows); err != nil {
			t.Fatalf("count null-probe variants: %v", err)
		}
		if nullableProbeRows != 1 {
			t.Fatalf("null-probe variants = %d, want 1", nullableProbeRows)
		}
		_, err := database.ExecContext(ctx, `INSERT INTO media_variant (id, size_bytes, ffprobe_version) VALUES (?, 128, '7.1')`, uuid.New())
		if err == nil || !strings.Contains(err.Error(), "media_variant_probe_group_all_or_none") {
			t.Fatalf("partial probe provenance error = %v, want media_variant_probe_group_all_or_none violation", err)
		}
	})

	t.Run("analyze_source_target_shape", func(t *testing.T) {
		rootID := newVariantRoot(t, ctx, database, "/srv/variant-shape")
		missingMode := tryOperation(t, ctx, database, &persistence.Operation{
			Kind: "analyze_source", State: "queued", Stage: "queued",
			TargetSourceRootID: &rootID,
		})
		requireViolation(t, missingMode, "operation_normalized_analysis_shape")

		batchSnapshot := json.RawMessage(`{"schema_version":1,"mode":"batch","work_ids":["` + uuid.NewString() + `"],"tools":[],"sha256_enabled":true,"cache_only_reuse":false,"rerun_target":false,"tools_read_required":false}`)
		missingRoot := tryOperation(t, ctx, database, &persistence.Operation{
			Kind: "analyze_source", State: "queued", Stage: "queued",
			InputSnapshot: batchSnapshot, SourceAnalysisMode: "batch",
		})
		requireViolation(t, missingRoot, "operation_analysis_target_shape")

		locationID := newVariantLocation(t, ctx, database, rootID, "album/track.flac")
		missingWork := uuid.New()
		singleSnapshot := json.RawMessage(`{"schema_version":1,"mode":"single_step","work_ids":["` + missingWork.String() + `"],"target_work_id":"` + missingWork.String() + `","target_step":"sha256","tools":[],"sha256_enabled":true,"cache_only_reuse":false,"rerun_target":false,"tools_read_required":false}`)
		noSingleTarget := tryOperation(t, ctx, database, &persistence.Operation{
			Kind: "analyze_source", State: "queued", Stage: "queued",
			InputSnapshot: singleSnapshot, SourceAnalysisMode: "single_step",
			TargetSourceRootID: &rootID, TargetSourceLocationID: &locationID,
		})
		requireViolation(t, noSingleTarget, "operation_normalized_analysis_shape")
	})

	t.Run("active_analysis_blocks_location_deletion", func(t *testing.T) {
		rootID := newVariantRoot(t, ctx, database, "/srv/variant-location-delete")
		locationID := newVariantLocation(t, ctx, database, rootID, "album/track.flac")
		operationID, workID := insertHeldBatchAnalysis(t, ctx, database, rootID, locationID)

		deleteErr := execError(ctx, database, "DELETE FROM source_location WHERE id = ?", locationID)
		var postgresErr pgdriver.Error
		if !errors.As(deleteErr, &postgresErr) || postgresErr.Field('C') != "P0001" ||
			!strings.Contains(deleteErr.Error(), "active source analysis work cannot be retired") {
			t.Fatalf("delete active analysis location error = %v, want active-work retirement guard", deleteErr)
		}

		if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, "DELETE FROM operation_source_work_hold WHERE operation_id = ? AND work_id = ?", operationID, workID); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE operation SET state='succeeded',stage='finished',finished_at=now(),updated_at=now(),target_source_root_id=NULL WHERE id=?`, operationID)
			return err
		}); err != nil {
			t.Fatalf("finish the analysis operation: %v", err)
		}
		if _, err := database.ExecContext(ctx, "DELETE FROM source_location WHERE id = ?", locationID); err != nil {
			t.Fatalf("delete the location of a terminal operation: %v", err)
		}
		var stored *uuid.UUID
		if err := database.NewRaw("SELECT target_source_location_id FROM operation WHERE id = ?", operationID).Scan(ctx, &stored); err != nil {
			t.Fatalf("read the cleared target: %v", err)
		}
		if stored != nil {
			t.Fatalf("terminal operation target = %s after the location deletion, want NULL", *stored)
		}
	})

	t.Run("scan_exclusivity_is_preserved_and_analyses_are_per_work", func(t *testing.T) {
		rootID := newVariantRoot(t, ctx, database, "/srv/variant-exclusive")
		scanSnapshot := json.RawMessage(`{"schema_version":3,"source_root_id":"` + rootID.String() + `","configured_path":"/srv/variant-exclusive","scan_generation":1,"sha256_enabled":false,"tools":[]}`)
		scanID := insertOperation(t, ctx, database, &persistence.Operation{
			Kind: "scan_source", State: "queued", Stage: "queued",
			InputSnapshot: scanSnapshot, TargetSourceRootID: &rootID,
		})
		requireRootConflict(t, tryOperation(t, ctx, database, &persistence.Operation{
			Kind: "scan_source", State: "queued", Stage: "queued",
			InputSnapshot: scanSnapshot, TargetSourceRootID: &rootID,
		}))
		if _, err := database.ExecContext(ctx, `UPDATE operation
			SET state='succeeded',stage='succeeded',finished_at=now(),updated_at=now() WHERE id=?`, scanID); err != nil {
			t.Fatalf("finish the source scan: %v", err)
		}

		locationA := newVariantLocation(t, ctx, database, rootID, "album/a.flac")
		locationB := newVariantLocation(t, ctx, database, rootID, "album/b.flac")
		_, workA := insertHeldBatchAnalysis(t, ctx, database, rootID, locationA)
		if err := tryHeldBatchAnalysis(t, ctx, database, rootID, locationB); err != nil {
			t.Fatalf("distinct work under an active analysis root was rejected: %v", err)
		}
		requireViolation(t, tryHoldExistingWork(ctx, database, rootID, workA),
			"operation_source_work_hold_one_active_work")
	})

	t.Run("install_and_move_constraints_unaffected", func(t *testing.T) {
		first := newVariantInstallation(t, ctx, database, "install-1")
		second := newVariantInstallation(t, ctx, database, "install-2")
		installSnapshot := json.RawMessage(`{"target_identity":"install"}`)
		insertOperation(t, ctx, database, &persistence.Operation{
			Kind: "install", State: "queued", Stage: "queued",
			InputSnapshot: installSnapshot, TargetInstallationID: &first,
		})
		insertOperation(t, ctx, database, &persistence.Operation{
			Kind: "install", State: "queued", Stage: "queued",
			InputSnapshot: installSnapshot, TargetInstallationID: &second,
		})
		requireInstallConflict(t, tryOperation(t, ctx, database, &persistence.Operation{
			Kind: "install", State: "queued", Stage: "queued",
			InputSnapshot: installSnapshot, TargetInstallationID: &first,
		}))

		moveRoot := newVariantRoot(t, ctx, database, "/srv/variant-move")
		insertOperation(t, ctx, database, &persistence.Operation{
			Kind: "scan_source", State: "queued", Stage: "queued",
			TargetSourceRootID: &moveRoot,
		})
		requireViolation(t, tryOperation(t, ctx, database, &persistence.Operation{
			Kind: "move_tools_root", State: "queued", Stage: "queued",
		}), "operation_active_tools_operations_exclusive")
	})

	t.Run("normalized_selected_result_refuses_deletion", func(t *testing.T) {
		rootID := newVariantRoot(t, ctx, database, "/srv/variant-selected-result")
		locationID := newVariantLocation(t, ctx, database, rootID, "album/track.flac")
		workID := uuid.New()
		if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_work
			(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id)
			VALUES (?,?,?,'/srv/variant-selected-result','/srv/variant-selected-result','album/track.flac',1024,now(),true,?)`,
			workID, locationID, rootID, uuid.New()); err != nil {
			t.Fatalf("insert normalized analysis work: %v", err)
		}
		variantID := newVariant(t, ctx, database, 1024, uuid.New())
		if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step
			(work_id,step,state,success_sha_variant_id,success_reuse_origin)
			VALUES (?,'sha256','succeeded',?,'executed')`, workID, variantID); err != nil {
			t.Fatalf("select successful normalized SHA-256 result: %v", err)
		}
		requireViolation(t, execError(ctx, database, "DELETE FROM media_variant WHERE id = ?", variantID),
			"source_analysis_step_success_sha_variant_id_fkey")
	})

	t.Run("location_link_refuses_deletion", func(t *testing.T) {
		locationRoot := newVariantRoot(t, ctx, database, "/srv/variant-hold-location")
		locationID := newVariantLocation(t, ctx, database, locationRoot, "album/track.flac")
		linkedVariantID := newVariant(t, ctx, database, 2048, uuid.New())
		if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
			Set("media_variant_id = ?", linkedVariantID).Set("updated_at = now()").
			Where("id = ?", locationID).Exec(ctx); err != nil {
			t.Fatalf("link a location to the variant: %v", err)
		}
		requireViolation(t, execError(ctx, database, "DELETE FROM media_variant WHERE id = ?", linkedVariantID),
			"source_location_media_variant_id_fkey")
		if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
			Set("media_variant_id = NULL").Set("updated_at = now()").
			Where("id = ?", locationID).Exec(ctx); err != nil {
			t.Fatalf("unlink the location from the variant: %v", err)
		}
		if _, err := database.ExecContext(ctx, "DELETE FROM media_variant WHERE id = ?", linkedVariantID); err != nil {
			t.Fatalf("delete the unlinked variant: %v", err)
		}
	})
}

// storedAnalysisOperation is the durable shape a terminal analysis keeps after
// its source root is deleted: the row and its input snapshot remain, while its
// normalized live targets are already cleared at settlement.
type storedAnalysisOperation struct {
	Kind       string          `bun:"kind"`
	State      string          `bun:"state"`
	Input      json.RawMessage `bun:"input_snapshot"`
	RootTarget *uuid.UUID      `bun:"target_source_root_id"`
	Location   *uuid.UUID      `bun:"target_source_location_id"`
	SafeError  *string         `bun:"safe_error"`
}

func readStoredAnalysis(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) storedAnalysisOperation {
	t.Helper()
	var stored storedAnalysisOperation
	if err := database.NewRaw(
		"SELECT kind, state, input_snapshot, target_source_root_id, target_source_location_id, safe_error FROM operation WHERE id = ?",
		operationID,
	).Scan(ctx, &stored); err != nil {
		t.Fatalf("read the analysis operation after root deletion: %v", err)
	}
	return stored
}

// TestSourceMediaVariantTerminalRootDeletionWithPostgreSQL is the regression
// for the root-deletion boundary of the new kind. Deleting a source root that
// still has a terminal analysis must succeed: the durable operation snapshot
// survives independently of live targets. An earlier shape check required
// target_source_root_id for every analyze_source state, so root deletion could
// invalidate terminal history; failed history is covered as well as succeeded,
// and an active analysis with its normalized work hold must still refuse deletion.
func TestSourceMediaVariantTerminalRootDeletionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()

	t.Run("succeeded_analysis_survives_root_deletion", func(t *testing.T) {
		rootID := newVariantRoot(t, ctx, database, "/srv/delete-succeeded")
		newVariantLocation(t, ctx, database, rootID, "album/track.flac")
		finished := time.Now().UTC()
		snapshot := terminalAnalysisSnapshot(rootID)
		operationID := insertOperation(t, ctx, database, &persistence.Operation{
			Kind: "analyze_source", State: "succeeded", Stage: "applying",
			InputSnapshot: snapshot, SourceAnalysisMode: "batch", FinishedAt: &finished,
		})

		if _, err := database.ExecContext(ctx, "DELETE FROM source_root WHERE id = ?", rootID); err != nil {
			t.Fatalf("delete a root with a succeeded analysis: %v", err)
		}

		stored := readStoredAnalysis(t, ctx, database, operationID)
		if stored.Kind != "analyze_source" || stored.State != "succeeded" {
			t.Fatalf("operation after root deletion = %s/%s, want analyze_source/succeeded", stored.Kind, stored.State)
		}
		requireSourceSnapshot(t, stored.Input, rootID)
		if stored.RootTarget != nil || stored.Location != nil {
			t.Fatalf("terminal operation retained live targets after root deletion: root=%v location=%v", stored.RootTarget, stored.Location)
		}
	})

	t.Run("failed_analysis_survives_root_deletion", func(t *testing.T) {
		rootID := newVariantRoot(t, ctx, database, "/srv/delete-failed")
		newVariantLocation(t, ctx, database, rootID, "album/track.flac")
		finished := time.Now().UTC()
		safeError := "ffprobe exited with status 1"
		snapshot := terminalAnalysisSnapshot(rootID)
		operationID := insertOperation(t, ctx, database, &persistence.Operation{
			Kind: "analyze_source", State: "failed", Stage: "probing",
			InputSnapshot: snapshot, SourceAnalysisMode: "batch",
			SafeError: &safeError, FinishedAt: &finished,
		})

		if _, err := database.ExecContext(ctx, "DELETE FROM source_root WHERE id = ?", rootID); err != nil {
			t.Fatalf("delete a root with a failed analysis: %v", err)
		}

		stored := readStoredAnalysis(t, ctx, database, operationID)
		if stored.Kind != "analyze_source" || stored.State != "failed" {
			t.Fatalf("operation after root deletion = %s/%s, want analyze_source/failed", stored.Kind, stored.State)
		}
		if stored.SafeError == nil || *stored.SafeError != safeError {
			t.Fatalf("safe_error after root deletion = %v, want %q", stored.SafeError, safeError)
		}
		requireSourceSnapshot(t, stored.Input, rootID)
		if stored.RootTarget != nil || stored.Location != nil {
			t.Fatalf("failed analysis kept live targets %v / %v after root deletion, want both NULL", stored.RootTarget, stored.Location)
		}
	})

	t.Run("active_analysis_blocks_root_deletion", func(t *testing.T) {
		rootID := newVariantRoot(t, ctx, database, "/srv/delete-active")
		locationID := newVariantLocation(t, ctx, database, rootID, "album/track.flac")
		operationID, _ := insertHeldBatchAnalysis(t, ctx, database, rootID, locationID)

		err := execError(ctx, database, "DELETE FROM source_root WHERE id = ?", rootID)
		if err == nil {
			t.Fatal("deleting a root with an active analysis was accepted")
		}
		requirePostgresCheckViolation(t, err)

		// The refused cascade must leave the live row and its guard values intact.
		stored := readStoredAnalysis(t, ctx, database, operationID)
		if stored.State != "queued" || stored.RootTarget == nil || *stored.RootTarget != rootID || stored.Location != nil {
			t.Fatalf("active analysis after the refused deletion = %+v, want queued with its batch root target and no location target", stored)
		}
	})
}

func requireSourceSnapshot(t *testing.T, raw json.RawMessage, rootID uuid.UUID) {
	t.Helper()
	var snapshot struct {
		SourceRootID string `json:"source_root_id"`
		Mode         string `json:"mode"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode the retained operation snapshot: %v", err)
	}
	if snapshot.SourceRootID != rootID.String() || snapshot.Mode != "batch" {
		t.Fatalf("operation snapshot after root deletion = %+v, want batch source_root_id=%s", snapshot, rootID)
	}
}

func terminalAnalysisSnapshot(rootID uuid.UUID) json.RawMessage {
	return json.RawMessage(`{"schema_version":1,"mode":"batch","work_ids":[],"tools":[],"sha256_enabled":true,"cache_only_reuse":false,"rerun_target":false,"tools_read_required":false,"source_root_id":"` + rootID.String() + `"}`)
}

func newVariantRoot(t *testing.T, ctx context.Context, database *bun.DB, path string) uuid.UUID {
	t.Helper()
	root := &persistence.SourceRoot{
		ID: uuid.New(), ConfiguredPath: path, DisplayName: path, Enabled: true, Status: "unknown",
	}
	if _, err := database.NewInsert().Model(root).Exec(ctx); err != nil {
		t.Fatalf("insert source root %s: %v", path, err)
	}
	return root.ID
}

func newVariantLocation(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID, relativePath string) uuid.UUID {
	t.Helper()
	location := &persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: rootID, RelativePath: relativePath, SizeBytes: 1024,
		Mtime: time.Now().UTC().Truncate(time.Microsecond), LastSeenScanGeneration: 1, ProbeStatus: "audio",
	}
	if _, err := database.NewInsert().Model(location).Exec(ctx); err != nil {
		t.Fatalf("insert source location %s: %v", relativePath, err)
	}
	return location.ID
}

func newVariantInstallation(t *testing.T, ctx context.Context, database *bun.DB, identity string) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		SourceName: "variant-test", ReleaseIdentity: identity, RelativePath: "ffmpeg/" + identity,
		State: "ready", ExecutableVersions: json.RawMessage(`{}`),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &now,
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("insert tool installation %s: %v", identity, err)
	}
	return installation.ID
}

func newTerminalAnalysisOperation(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) uuid.UUID {
	t.Helper()
	finished := time.Now().UTC()
	return insertOperation(t, ctx, database, &persistence.Operation{
		Kind: "analyze_source", State: "succeeded", Stage: "applying",
		TargetSourceRootID: &rootID, FinishedAt: &finished,
	})
}

func newVariant(t *testing.T, ctx context.Context, database *bun.DB, sizeBytes int64, appliedOperationID uuid.UUID) uuid.UUID {
	t.Helper()
	variant := &persistence.MediaVariant{
		ID: uuid.New(), SizeBytes: sizeBytes, AnalysisPolicyVersion: 1, FFProbeVersion: "7.1",
		FFProbeJSON: json.RawMessage(`{}`), ObservedTags: json.RawMessage(`{}`),
		InspectedAt: time.Now().UTC(), AppliedOperationID: appliedOperationID,
	}
	if _, err := database.NewInsert().Model(variant).Exec(ctx); err != nil {
		t.Fatalf("insert media variant: %v", err)
	}
	return variant.ID
}

func tryVariant(t *testing.T, ctx context.Context, database *bun.DB, sizeBytes int64, appliedOperationID uuid.UUID) error {
	t.Helper()
	_, err := database.NewInsert().Model(&persistence.MediaVariant{
		ID: uuid.New(), SizeBytes: sizeBytes, AnalysisPolicyVersion: 1, FFProbeVersion: "7.1",
		FFProbeJSON: json.RawMessage(`{}`), ObservedTags: json.RawMessage(`{}`),
		InspectedAt: time.Now().UTC(), AppliedOperationID: appliedOperationID,
	}).Exec(ctx)
	return err
}

func tryVariantPolicyVersion(t *testing.T, ctx context.Context, database *bun.DB, version int, appliedOperationID uuid.UUID) error {
	t.Helper()
	return execError(ctx, database,
		`INSERT INTO media_variant (id, size_bytes, analysis_policy_version, ffprobe_version, ffprobe_json, observed_tags, inspected_at, applied_operation_id)
		 VALUES (?, 128, ?, '7.1', '{}'::jsonb, '{}'::jsonb, now(), ?)`,
		uuid.New(), version, appliedOperationID)
}

func tryVariantVersion(t *testing.T, ctx context.Context, database *bun.DB, version string) error {
	t.Helper()
	return execError(ctx, database,
		`INSERT INTO media_variant (id, size_bytes, analysis_policy_version, ffprobe_version, ffprobe_json, observed_tags, inspected_at, applied_operation_id)
		 VALUES (?, 128, 1, ?, '{}'::jsonb, '{}'::jsonb, now(), ?)`,
		uuid.New(), version, uuid.New())
}

// tryVariantSHAOnly inserts the valid non-probe half of the single provenance
// model: the whole SHA-256 group is populated while the entire probe group stays
// NULL. media_variant_probe_group_all_or_none accepts it because every probe
// column is NULL, unlike the partial probe group below that names ffprobe_version
// but no applied_operation_id.
func tryVariantSHAOnly(t *testing.T, ctx context.Context, database *bun.DB) error {
	t.Helper()
	digest := make([]byte, 32)
	digest[0] = 0x1b
	return execError(ctx, database,
		`INSERT INTO media_variant (id, size_bytes, source_sha256, sha256_calculated_at, sha256_algorithm, sha256_applied_operation_id)
		 VALUES (?, 128, ?, now(), 'SHA-256', ?)`,
		uuid.New(), digest, uuid.New())
}

func insertOperation(t *testing.T, ctx context.Context, database *bun.DB, operation *persistence.Operation) uuid.UUID {
	t.Helper()
	if err := tryOperation(t, ctx, database, operation); err != nil {
		t.Fatalf("insert %s operation: %v", operation.Kind, err)
	}
	return operation.ID
}

func insertHeldBatchAnalysis(t *testing.T, ctx context.Context, database *bun.DB, rootID, locationID uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	operationID, workID, err := storeHeldBatchAnalysis(ctx, database, rootID, locationID)
	if err != nil {
		t.Fatalf("insert active normalized batch with work hold: %v", err)
	}
	return operationID, workID
}

func tryHeldBatchAnalysis(t *testing.T, ctx context.Context, database *bun.DB, rootID, locationID uuid.UUID) error {
	t.Helper()
	_, _, err := storeHeldBatchAnalysis(ctx, database, rootID, locationID)
	return err
}

func storeHeldBatchAnalysis(ctx context.Context, database *bun.DB, rootID, locationID uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	operationID := uuid.New()
	workID := uuid.New()
	var configuredPath, relativePath string
	if err := database.NewRaw(`SELECT r.configured_path, l.relative_path
		FROM source_root r JOIN source_location l ON l.source_root_id=r.id
		WHERE r.id=? AND l.id=?`, rootID, locationID).Scan(ctx, &configuredPath, &relativePath); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	snapshot, err := json.Marshal(map[string]any{
		"schema_version": 1, "mode": "batch", "work_ids": []uuid.UUID{workID},
		"selected_steps": []map[string]any{{"work_id": workID, "step": "sha256"}}, "rerun_target": false,
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	err = database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		operation := &persistence.Operation{
			ID: operationID, Kind: "analyze_source", State: "queued", Stage: "queued",
			InputSnapshot: snapshot, TargetSourceRootID: &rootID, SourceAnalysisMode: "batch", Attempt: 1,
		}
		if _, err := tx.NewInsert().Model(operation).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_analysis_work
			(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id)
			VALUES (?,?,?,?,?,?,1,now(),true,?)`,
			workID, locationID, rootID, configuredPath, configuredPath, relativePath, uuid.New()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO operation_source_work_hold(operation_id,work_id) VALUES (?,?)`, operationID, workID)
		return err
	})
	return operationID, workID, err
}

func tryHoldExistingWork(ctx context.Context, database *bun.DB, rootID, workID uuid.UUID) error {
	operationID := uuid.New()
	snapshot, err := json.Marshal(map[string]any{
		"schema_version": 1, "mode": "batch", "work_ids": []uuid.UUID{workID},
		"selected_steps": []map[string]any{{"work_id": workID, "step": "sha256"}}, "rerun_target": false,
	})
	if err != nil {
		return err
	}
	return database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		operation := &persistence.Operation{
			ID: operationID, Kind: "analyze_source", State: "queued", Stage: "queued",
			InputSnapshot: snapshot, TargetSourceRootID: &rootID,
			SourceAnalysisMode: "batch", Attempt: 1,
		}
		if _, err := tx.NewInsert().Model(operation).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO operation_source_work_hold(operation_id,work_id) VALUES (?,?)`, operationID, workID)
		return err
	})
}

func tryOperation(t *testing.T, ctx context.Context, database *bun.DB, operation *persistence.Operation) error {
	t.Helper()
	if operation.ID == uuid.Nil {
		operation.ID = uuid.New()
	}
	if operation.InputSnapshot == nil {
		operation.InputSnapshot = json.RawMessage(`{}`)
	}
	if operation.Attempt == 0 {
		operation.Attempt = 1
	}
	_, err := database.NewInsert().Model(operation).Exec(ctx)
	return err
}

func execError(ctx context.Context, database *bun.DB, query string, args ...any) error {
	_, err := database.ExecContext(ctx, query, args...)
	return err
}

func requireViolation(t *testing.T, err error, constraint string) {
	t.Helper()
	if err == nil {
		t.Fatalf("statement succeeded, want a %s violation", constraint)
	}
	if !strings.Contains(err.Error(), constraint) {
		t.Fatalf("error = %v, want a %s violation", err, constraint)
	}
}

func requireRootConflict(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("a second active operation on one root was accepted")
	}
	if !strings.Contains(err.Error(), "operation_one_active_source_root_operation") &&
		!strings.Contains(err.Error(), "operation_one_active_source_root_scan") &&
		!strings.Contains(err.Error(), "operation_active_tools_operations_exclusive") {
		t.Fatalf("error = %v, want the active-root guard", err)
	}
}

func requireInstallConflict(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("a second active operation on one installation was accepted")
	}
	if !strings.Contains(err.Error(), "operation_one_active_installation") &&
		!strings.Contains(err.Error(), "operation_active_tools_operations_exclusive") {
		t.Fatalf("error = %v, want the active-installation guard", err)
	}
}

func relationName(t *testing.T, database *bun.DB, name string) *string {
	t.Helper()
	var relation *string
	if err := database.NewRaw("SELECT to_regclass(?)", "public."+name).Scan(context.Background(), &relation); err != nil {
		t.Fatalf("read relation %s: %v", name, err)
	}
	return relation
}

func columnExists(t *testing.T, database *bun.DB, table, column string) bool {
	t.Helper()
	var count int
	if err := database.NewRaw(
		"SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = ? AND column_name = ?",
		table, column,
	).Scan(context.Background(), &count); err != nil {
		t.Fatalf("read column %s.%s: %v", table, column, err)
	}
	return count == 1
}

func indexExists(t *testing.T, database *bun.DB, name string) bool {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = ?", name).
		Scan(context.Background(), &count); err != nil {
		t.Fatalf("read index %s: %v", name, err)
	}
	return count == 1
}

func constraintDefinition(t *testing.T, database *bun.DB, name string) string {
	t.Helper()
	var definition string
	if err := database.NewRaw("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = ?", name).
		Scan(context.Background(), &definition); err != nil {
		t.Fatalf("read constraint %s: %v", name, err)
	}
	return definition
}

func requirePostgresCheckViolation(t *testing.T, err error) {
	t.Helper()
	var pgErr pgdriver.Error
	if !errors.As(err, &pgErr) || pgErr.Field('C') != "23514" {
		t.Fatalf("error = %v, want a PostgreSQL check violation (SQLSTATE 23514)", err)
	}
}
