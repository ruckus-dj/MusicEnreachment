//go:build integration

package migrations_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const fallbackStageReceiptMigration = "20261130000000"

func TestFallbackStageReceiptMigrationPreservesLegacyReceiptWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, fallbackStageReceiptMigration))

	runID, revision := insertFallbackReceiptMigrationScope(t, ctx, database)
	legacyDigest := make([]byte, 32)
	for index := range legacyDigest {
		legacyDigest[index] = byte(index + 1)
	}
	legacyContext := `{"version":2,"context":{"run_id":"` + runID.String() + `","acquisition_revision":1,"legacy_payload":{"keep":"original"}}}`
	legacyEdges := `[{"member_id":"` + uuid.NewString() + `","score":0.91}]`
	// Exercise the real schema-28 receipt row/FK shape directly; migrations must
	// preserve historical receipt columns without fabricating provider acceptance.
	if _, err := database.NewRaw(`INSERT INTO matching_fallback_calculation_receipt
		(run_id,acquisition_revision,stage_index,schema_version,calculator_version,digest_version,input_digest,context,edge_calculations)
		VALUES (?, ?, 0, 2, 1, 2, ?, ?::jsonb, ?::jsonb)`, runID, revision, legacyDigest, legacyContext, legacyEdges).Exec(ctx); err != nil {
		t.Fatalf("seed historical schema-2 calculation receipt: %v", err)
	}

	migration := migrationNamed(t, collection, fallbackStageReceiptMigration)
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
	assertFallbackCalculationReceiptUnchanged(t, ctx, database, runID, revision, legacyDigest, legacyContext, legacyEdges)
	rollbackMigration(t, ctx, database, migration)
	assertFallbackCalculationReceiptUnchanged(t, ctx, database, runID, revision, legacyDigest, legacyContext, legacyEdges)
	assertLegacyCalculationReceiptConstraints(t, ctx, database)
}

func TestFallbackStageReceiptMigrationRollbackRejectsNewStageReceiptWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, fallbackStageReceiptMigration))
	migration := migrationNamed(t, collection, fallbackStageReceiptMigration)
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	runID, revision := insertFallbackReceiptMigrationScope(t, ctx, database)
	if _, err := database.NewRaw(`INSERT INTO matching_fallback_calculation_receipt
		(run_id,acquisition_revision,stage_index,schema_version,calculator_version,digest_version,input_digest,context,edge_calculations)
		VALUES (?, ?, 1, 3, 3, 3, ?, '{}'::jsonb, '[]'::jsonb)`, runID, revision, make([]byte, 32)).Exec(ctx); err != nil {
		t.Fatalf("seed stage-1 schema-3 calculation receipt: %v", err)
	}
	err := rollbackFallbackStageReceiptMigration(ctx, database, collection)
	if err == nil || !strings.Contains(err.Error(), "cannot roll back populated nonzero fallback stage") {
		t.Fatalf("rollback with stage-1 receipt error = %v, want migration safety guard", err)
	}
	var schemaVersion, stageIndex int
	if err := database.NewRaw(`SELECT schema_version,stage_index FROM matching_fallback_calculation_receipt WHERE run_id=?`, runID).Scan(ctx, &schemaVersion, &stageIndex); err != nil {
		t.Fatalf("read retained stage-1 receipt after blocked rollback: %v", err)
	}
	if schemaVersion != 3 || stageIndex != 1 {
		t.Fatalf("blocked rollback changed new receipt identity: schema=%d stage=%d", schemaVersion, stageIndex)
	}
}

func insertFallbackReceiptMigrationScope(t *testing.T, ctx context.Context, database *bun.DB) (uuid.UUID, int64) {
	t.Helper()
	runID, sourceID, providerID := uuid.New(), uuid.New(), uuid.New()
	if _, err := database.NewRaw(`INSERT INTO provider(id,code) VALUES (?,?)`, providerID, "fallback-migration-test-"+providerID.String()).Exec(ctx); err != nil {
		t.Fatalf("create receipt provider: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?,?,?,?)`,
		sourceID, providerID, "migration-test", "https://example.invalid", "legacy-config").Exec(ctx); err != nil {
		t.Fatalf("create receipt provider source: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at)
		VALUES (?, 'matching_evaluation_v1', 'succeeded', 'complete', '{}'::jsonb, now())`, runID).Exec(ctx); err != nil {
		t.Fatalf("create receipt matching operation: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_run(
		id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,
		provider_source_id,provider_configuration_identity,policy_version,state
	) VALUES (?,?,?,?, '{}'::jsonb,0,0,?,?,?,'succeeded')`, runID, runID, "migration-test-"+runID.String(),
		make([]byte, 32), sourceID, "legacy-config", "migration-test-policy").Exec(ctx); err != nil {
		t.Fatalf("create receipt matching run: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_acquisition(run_id,intent_version,intent,intent_digest,current_phase,revision)
		VALUES (?,3,'{}'::jsonb,?,'fallback_applicability',1)`, runID, make([]byte, 32)).Exec(ctx); err != nil {
		t.Fatalf("create receipt acquisition: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_acquisition_phase(run_id,revision,phase,input,output,response_provenance)
		VALUES (?,1,'fallback_applicability','{"stage_index":0}'::jsonb,'{"stage_index":0}'::jsonb,'[]'::jsonb)`, runID).Exec(ctx); err != nil {
		t.Fatalf("create receipt applicability phase: %v", err)
	}
	return runID, 1
}

func assertFallbackCalculationReceiptUnchanged(t *testing.T, ctx context.Context, database *bun.DB, runID uuid.UUID, revision int64, digest []byte, expectedContext, expectedEdges string) {
	t.Helper()
	var gotRunID uuid.UUID
	var gotRevision int64
	var gotStage, schemaVersion, calculatorVersion, digestVersion int
	var gotDigest []byte
	var gotContext, gotEdges string
	if err := database.NewRaw(`SELECT run_id,acquisition_revision,stage_index,schema_version,calculator_version,digest_version,
		input_digest,context::text,edge_calculations::text FROM matching_fallback_calculation_receipt WHERE run_id=?`, runID).Scan(ctx,
		&gotRunID, &gotRevision, &gotStage, &schemaVersion, &calculatorVersion, &digestVersion, &gotDigest, &gotContext, &gotEdges); err != nil {
		t.Fatalf("read calculation receipt after migration: %v", err)
	}
	if gotRunID != runID || gotRevision != revision || gotStage != 0 || schemaVersion != 2 || calculatorVersion != 1 || digestVersion != 2 ||
		hex.EncodeToString(gotDigest) != hex.EncodeToString(digest) || !equalJSONDocument(gotContext, expectedContext) || !equalJSONDocument(gotEdges, expectedEdges) {
		t.Fatalf("legacy receipt changed: scope=%s/%d stage=%d versions=%d/%d/%d digest=%x context=%s edges=%s",
			gotRunID, gotRevision, gotStage, schemaVersion, calculatorVersion, digestVersion, gotDigest, gotContext, gotEdges)
	}
}

func assertLegacyCalculationReceiptConstraints(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	var runID uuid.UUID
	if err := database.NewRaw(`SELECT run_id FROM matching_fallback_calculation_receipt LIMIT 1`).Scan(ctx, &runID); err != nil {
		t.Fatalf("read historical receipt run: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_acquisition_phase(run_id,revision,phase,input,output,response_provenance)
		VALUES (?,2,'fallback_applicability','{}'::jsonb,'{}'::jsonb,'[]'::jsonb)`, runID).Exec(ctx); err != nil {
		t.Fatalf("create schema-3 constraint probe phase: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_fallback_calculation_receipt
		(run_id,acquisition_revision,stage_index,schema_version,calculator_version,digest_version,input_digest,context,edge_calculations)
		VALUES (?,2,0,3,3,3,decode(repeat('00',32),'hex'),'{}'::jsonb,'[]'::jsonb)`, runID).Exec(ctx); err == nil {
		t.Fatal("legacy schema accepted a schema-3 calculation receipt after rollback")
	}
}

func equalJSONDocument(left, right string) bool {
	var parsedLeft, parsedRight any
	if json.Unmarshal([]byte(left), &parsedLeft) != nil || json.Unmarshal([]byte(right), &parsedRight) != nil {
		return false
	}
	canonicalLeft, err := json.Marshal(parsedLeft)
	if err != nil {
		return false
	}
	canonicalRight, err := json.Marshal(parsedRight)
	return err == nil && string(canonicalLeft) == string(canonicalRight)
}

func rollbackFallbackStageReceiptMigration(ctx context.Context, database *bun.DB, collection *migrate.Migrations) error {
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		return err
	}
	_, err := migrator.Rollback(ctx)
	return err
}
