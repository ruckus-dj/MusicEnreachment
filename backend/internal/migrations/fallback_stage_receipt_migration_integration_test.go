//go:build integration

package migrations_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const fallbackStageReceiptMigration = "20261130000000"
const fallbackSearchDigestVersionMigration = "20261202000000"

func TestFallbackSearchDigestVersionMigrationPreservesOldWriterReceiptWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, fallbackSearchDigestVersionMigration))

	settingsRepository := persistence.NewSettingsRepository(database)
	settingsRegistry := settings.New(settingsRepository, nil)
	if err := settingsRegistry.SetMusicBrainzConfig(ctx, "public", ""); err != nil {
		t.Fatalf("set MusicBrainz config through settings registry: %v", err)
	}
	config, err := settingsRegistry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatalf("read MusicBrainz setup snapshot: %v", err)
	}
	runID, _ := insertFallbackReceiptMigrationScope(t, ctx, database, config.Identity)
	requestID := uuid.New()
	var sourceID uuid.UUID
	identity := config.Identity
	if err := database.NewRaw(`SELECT provider_source_id FROM matching_run WHERE id=?`, runID).Scan(ctx, &sourceID); err != nil {
		t.Fatalf("read fallback run source: %v", err)
	}
	query := `release:"Old Writer Album"`
	input := fmt.Sprintf(`{"schema_version":1,"query":%q,"offset":0,"limit":10}`, query)
	cacheKey := "release-search:0:10:" + query
	plan := fmt.Sprintf(`{"schema_version":1,"stages":[{"kind":"album","query":%q,"offset":0,"limit":10}]}`, query)
	intent := fmt.Sprintf(`{"schema_version":3,"fallback_query_plan":%s,"initial_provider_inputs":[{"operation_id":%q,"kind":"provider_release_search","cache_key":%q,"input":%s}]}`,
		plan, requestID, cacheKey, input)
	if _, err := database.NewRaw(`UPDATE matching_acquisition SET intent=?::jsonb,current_phase='fallback_search',revision=2 WHERE run_id=?`, intent, runID).Exec(ctx); err != nil {
		t.Fatalf("set original fallback intent: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_acquisition_phase(run_id,revision,phase,input,output,response_provenance)
		VALUES (?,2,'fallback_search',?::jsonb,?::jsonb,'[]'::jsonb)`, runID,
		fmt.Sprintf(`{"stage_index":0,"request_id":%q}`, requestID), fmt.Sprintf(`{"stage_index":0,"request_id":%q}`, requestID)).Exec(ctx); err != nil {
		t.Fatalf("record original fallback phase: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,provider_source_id,provider_configuration_identity,
		provider_cache_key,attempt,river_job_id,provider_execution_epoch,started_at)
		VALUES (?,'provider_release_search','running','fetching',?::jsonb,?,?,?,1,41,1,now())`,
		requestID, input, sourceID, identity, cacheKey).Exec(ctx); err != nil {
		t.Fatalf("create original provider request: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_provider_dependency(parent_run_id,request_id,cache_key) VALUES (?,?,?)`, runID, requestID, cacheKey).Exec(ctx); err != nil {
		t.Fatalf("register original provider dependency: %v", err)
	}
	canonicalInput, err := json.Marshal(map[string]any{"limit": 10, "offset": 0, "query": query, "schema_version": 1})
	if err != nil {
		t.Fatalf("marshal canonical request input: %v", err)
	}
	inputHash := sha256.Sum256(canonicalInput)
	if _, err := database.NewRaw(`INSERT INTO matching_fallback_request_registration
		(run_id,stage_index,role,request_id,provider_source_id,configuration_identity,kind,cache_key,canonical_input,input_sha256)
		VALUES (?,0,'search',?,?,?,'provider_release_search',?,?::jsonb,?)`,
		runID, requestID, sourceID, identity, cacheKey, input, inputHash[:]).Exec(ctx); err != nil {
		t.Fatalf("register original fallback request: %v", err)
	}
	providerCache := persistence.NewProviderCacheRepository(database)
	ticket, err := providerCache.BeginRefresh(ctx, sourceID, cacheKey, identity)
	if err != nil {
		t.Fatalf("reserve original provider response generation: %v", err)
	}
	payload := `{"count":1,"releases":[{"id":"release-1","title":"Original Title"}]}`
	var postgresPayloadText string
	if err := database.NewRaw(`SELECT ?::jsonb::text`, payload).Scan(ctx, &postgresPayloadText); err != nil {
		t.Fatalf("obtain PostgreSQL's original JSONB text: %v", err)
	}
	// The old writer hashed compact JSON marshaled from PostgreSQL's jsonb::text.
	oldWriterBytes, err := json.Marshal(json.RawMessage(postgresPayloadText))
	if err != nil {
		t.Fatalf("marshal old-writer payload bytes: %v", err)
	}
	oldWriterHash := sha256.Sum256(oldWriterBytes)
	if _, err := database.NewRaw(`INSERT INTO matching_fallback_search_receipt
		(run_id,request_id,stage_index,provider_source_id,configuration_identity,cache_key,request_input,payload,payload_sha256,
		cache_revision,cache_generation,fetch_order,execution_epoch,provider_attempt,river_job_id)
		VALUES (?,?,0,?,?,?,?::jsonb,?::jsonb,?,'old-revision',?,?,1,1,41)`,
		runID, requestID, sourceID, identity, cacheKey, input, payload, oldWriterHash[:], ticket.Generation, ticket.FetchOrder).Exec(ctx); err != nil {
		t.Fatalf("seed pre-migration old-writer receipt: %v", err)
	}

	migration := migrationNamed(t, collection, fallbackSearchDigestVersionMigration)
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
	var storedHash []byte
	var digestVersion int
	var storedPayload, storedInput string
	if err := database.NewRaw(`SELECT payload_sha256,payload_digest_version,payload::text,request_input::text
		FROM matching_fallback_search_receipt WHERE run_id=? AND request_id=?`, runID, requestID).
		Scan(ctx, &storedHash, &digestVersion, &storedPayload, &storedInput); err != nil {
		t.Fatalf("read migrated old-writer receipt: %v", err)
	}
	if !equalDigestBytes(storedHash, oldWriterHash[:]) || digestVersion != 1 || storedPayload != postgresPayloadText || !equalJSONDocument(storedInput, input) {
		t.Fatalf("migration changed old-writer receipt: hash=%x version=%d payload=%s input=%s", storedHash, digestVersion, storedPayload, storedInput)
	}
	receipt, err := persistence.NewSetupManagerRepository(database).GetMatchingFallbackSearchReceipt(ctx, runID, requestID)
	if err != nil {
		t.Fatalf("read migrated old-writer receipt through persistence: %v", err)
	}
	if receipt == nil || receipt.PayloadDigestVersion != 0 || !equalDigestHex(receipt.PayloadSHA256, oldWriterHash[:]) ||
		string(receipt.Payload) != string(oldWriterBytes) || !equalJSONDocument(string(receipt.RequestInput), input) {
		t.Fatalf("legacy DTO did not preserve old-writer authority: %#v", receipt)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal legacy receipt DTO: %v", err)
	}
	if strings.Contains(string(encoded), `"payload_digest_version"`) {
		t.Fatalf("legacy DTO unexpectedly reports a digest version: %s", encoded)
	}
	accepted := persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now(), Revision: "old-revision", Payload: json.RawMessage(payload),
		Delivery: &persistence.ProviderDeliveryFence{OperationID: requestID, Attempt: 1, RiverJobID: 41, ExecutionEpoch: 1},
	}
	if err := providerCache.ApplySuccessfulResponse(ctx, accepted); err != nil {
		t.Fatalf("recapture original accepted provider response: %v", err)
	}
	if err := providerCache.ApplySuccessfulResponse(ctx, accepted); err != nil {
		t.Fatalf("idempotently recapture original accepted provider response: %v", err)
	}
	assertOriginalV1SearchReceipt(t, ctx, database, runID, requestID, oldWriterHash[:], postgresPayloadText, input)

	changedPayload := accepted
	changedPayload.FetchedAt = accepted.FetchedAt.Add(time.Second)
	changedPayload.Payload = json.RawMessage(`{"count":1,"releases":[{"id":"release-1","title":"Changed Title"}]}`)
	if err := providerCache.ApplySuccessfulResponse(ctx, changedPayload); err == nil {
		t.Fatal("recapture with changed accepted payload unexpectedly succeeded")
	}
	changedProvenance := accepted
	changedProvenance.FetchedAt = accepted.FetchedAt.Add(2 * time.Second)
	changedProvenance.Revision = "changed-revision"
	if err := providerCache.ApplySuccessfulResponse(ctx, changedProvenance); err == nil {
		t.Fatal("recapture with changed accepted response provenance unexpectedly succeeded")
	}
	assertOriginalV1SearchReceipt(t, ctx, database, runID, requestID, oldWriterHash[:], postgresPayloadText, input)

	rollbackMigration(t, ctx, database, migration)
	var rolledBackHash []byte
	var rolledBackPayload, rolledBackInput string
	if err := database.NewRaw(`SELECT payload_sha256,payload::text,request_input::text FROM matching_fallback_search_receipt
		WHERE run_id=? AND request_id=?`, runID, requestID).Scan(ctx, &rolledBackHash, &rolledBackPayload, &rolledBackInput); err != nil {
		t.Fatalf("read old-writer receipt after down migration: %v", err)
	}
	if !equalDigestBytes(rolledBackHash, oldWriterHash[:]) || rolledBackPayload != postgresPayloadText || !equalJSONDocument(rolledBackInput, input) {
		t.Fatalf("down migration changed old-writer authority: hash=%x payload=%s input=%s", rolledBackHash, rolledBackPayload, rolledBackInput)
	}
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})
	var reappliedHash []byte
	var reappliedVersion int
	if err := database.NewRaw(`SELECT payload_sha256,payload_digest_version FROM matching_fallback_search_receipt
		WHERE run_id=? AND request_id=?`, runID, requestID).Scan(ctx, &reappliedHash, &reappliedVersion); err != nil {
		t.Fatalf("read old-writer receipt after up migration: %v", err)
	}
	if !equalDigestBytes(reappliedHash, oldWriterHash[:]) || reappliedVersion != 1 {
		t.Fatalf("up migration changed old-writer digest: hash=%x version=%d", reappliedHash, reappliedVersion)
	}
}

func assertOriginalV1SearchReceipt(t *testing.T, ctx context.Context, database *bun.DB, runID, requestID uuid.UUID, hash []byte, payload, input string) {
	t.Helper()
	var storedHash []byte
	var storedVersion int
	var storedPayload, storedInput string
	if err := database.NewRaw(`SELECT payload_sha256,payload_digest_version,payload::text,request_input::text
		FROM matching_fallback_search_receipt WHERE run_id=? AND request_id=?`, runID, requestID).
		Scan(ctx, &storedHash, &storedVersion, &storedPayload, &storedInput); err != nil {
		t.Fatalf("read original V1 search receipt after recapture: %v", err)
	}
	if !equalDigestBytes(storedHash, hash) || storedVersion != 1 || storedPayload != payload || !equalJSONDocument(storedInput, input) {
		t.Fatalf("recapture changed original V1 search receipt: hash=%x version=%d payload=%s input=%s", storedHash, storedVersion, storedPayload, storedInput)
	}
}

func equalDigestBytes(left, right []byte) bool { return string(left) == string(right) }

func equalDigestHex(left string, right []byte) bool { return left == hex.EncodeToString(right) }

func TestFallbackSearchDigestVersionRollbackRejectsVersionTwoReceiptWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	ctx := context.Background()
	collection := mustMigrations(t)
	applyMigrationsOneAtATime(t, ctx, database, migrationsBefore(t, collection, fallbackSearchDigestVersionMigration))
	migration := migrationNamed(t, collection, fallbackSearchDigestVersionMigration)
	applyMigrationsOneAtATime(t, ctx, database, []*migrate.Migration{migration})

	runID, _ := insertFallbackReceiptMigrationScope(t, ctx, database)
	requestID := uuid.New()
	var sourceID uuid.UUID
	if err := database.NewRaw(`SELECT provider_source_id FROM matching_run WHERE id=?`, runID).Scan(ctx, &sourceID); err != nil {
		t.Fatalf("read provider source: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO operation
		(id,kind,state,stage,input_snapshot,finished_at,provider_source_id,provider_configuration_identity,provider_cache_key)
		VALUES (?, 'provider_release_search', 'succeeded', 'complete', '{}'::jsonb, now(), ?, ?, ?)`,
		requestID, sourceID, "legacy-config", "legacy-search-key").Exec(ctx); err != nil {
		t.Fatalf("create search request operation: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_provider_dependency(parent_run_id,request_id,cache_key)
		VALUES (?,?,?)`, runID, requestID, "legacy-search-key").Exec(ctx); err != nil {
		t.Fatalf("create search dependency: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_fallback_search_receipt
		(run_id,request_id,stage_index,provider_source_id,configuration_identity,cache_key,request_input,payload,payload_sha256,
		cache_generation,fetch_order,execution_epoch,provider_attempt,river_job_id,payload_digest_version)
		VALUES (?,?,0,?,?,'legacy-search-key','{}'::jsonb,'{}'::jsonb,?,1,1,1,1,1,2)`,
		runID, requestID, sourceID, "legacy-config", make([]byte, 32)).Exec(ctx); err != nil {
		t.Fatalf("seed version-2 search receipt: %v", err)
	}

	err := rollbackFallbackStageReceiptMigration(ctx, database, collection)
	if err == nil || !strings.Contains(err.Error(), "cannot roll back search digest version while version-2 receipts exist") {
		t.Fatalf("rollback with version-2 receipt error = %v, want migration safety guard", err)
	}
	var columnExists bool
	if err := database.NewRaw(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name='matching_fallback_search_receipt' AND column_name='payload_digest_version')`).Scan(ctx, &columnExists); err != nil {
		t.Fatalf("check retained digest version column: %v", err)
	}
	if !columnExists {
		t.Fatal("blocked rollback removed payload_digest_version")
	}
}

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

func insertFallbackReceiptMigrationScope(t *testing.T, ctx context.Context, database *bun.DB, configurationIdentities ...string) (uuid.UUID, int64) {
	t.Helper()
	configurationIdentity := "legacy-config"
	if len(configurationIdentities) > 0 {
		configurationIdentity = configurationIdentities[0]
	}
	runID, sourceID, providerID := uuid.New(), uuid.New(), uuid.New()
	if _, err := database.NewRaw(`INSERT INTO provider(id,code) VALUES (?,?)`, providerID, "fallback-migration-test-"+providerID.String()).Exec(ctx); err != nil {
		t.Fatalf("create receipt provider: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?,?,?,?)`,
		sourceID, providerID, "migration-test", "https://example.invalid", configurationIdentity).Exec(ctx); err != nil {
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
		make([]byte, 32), sourceID, configurationIdentity, "migration-test-policy").Exec(ctx); err != nil {
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
