//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestFallbackAcceptedReceiptIsStableAcrossCacheRefreshSQLPostgreSQL(t *testing.T) {
	t.Parallel()
	f := seedFallbackSQLRun(t, true)
	ctx := f.ctx
	cache := persistence.NewProviderCacheRepository(f.db)
	operation, err := f.repository.GetOperation(ctx, f.requestID)
	if err != nil || operation.RiverJobID == nil {
		t.Fatalf("read admitted provider operation: operation=%+v err=%v", operation, err)
	}
	epoch, claimed, err := f.repository.ClaimProviderOperationDelivery(ctx, operation.ID, operation.Attempt, *operation.RiverJobID)
	if err != nil || !claimed {
		t.Fatalf("claim admitted provider delivery: claimed=%v err=%v", claimed, err)
	}
	if _, err := f.db.NewRaw(`UPDATE operation SET state='running',stage='fetching_provider',started_at=now() WHERE id=?`, operation.ID).Exec(ctx); err != nil {
		t.Fatalf("start provider delivery: %v", err)
	}
	ticket, err := cache.BeginRefresh(ctx, f.sourceID, f.cacheKey, f.identity)
	if err != nil {
		t.Fatalf("reserve accepted response: %v", err)
	}
	accepted := json.RawMessage(`{"created":"2026-10-12T00:00:00Z","count":1,"offset":0,"recordings":[{"id":"recording-a","title":"Fallback Track"}]}`)
	fence := persistence.ProviderDeliveryFence{OperationID: operation.ID, Attempt: operation.Attempt, RiverJobID: *operation.RiverJobID, ExecutionEpoch: epoch}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), Payload: accepted,
		Graph: persistence.ProviderGraph{}, Delivery: &fence,
	}); err != nil {
		t.Fatalf("accept provider response and capture receipt: %v", err)
	}
	receipt, err := f.repository.GetMatchingFallbackSearchReceipt(ctx, f.runID, operation.ID)
	if err != nil || receipt == nil {
		t.Fatalf("read accepted receipt: receipt=%+v err=%v", receipt, err)
	}
	wantDigest := sha256.Sum256(receipt.Payload)
	if receipt.PayloadSHA256 != hex.EncodeToString(wantDigest[:]) || receipt.CacheGeneration != ticket.Generation || receipt.ExecutionEpoch != epoch {
		t.Fatalf("receipt is not bound to accepted response fence: %+v", receipt)
	}
	replayed, err := cache.ReplayAutomaticFallbackSearchReceipt(ctx, fence)
	if err != nil || replayed == nil || replayed.PayloadSHA256 != receipt.PayloadSHA256 || string(replayed.Payload) != string(receipt.Payload) {
		t.Fatalf("replay accepted receipt: replay=%+v err=%v", replayed, err)
	}

	refresh, err := cache.BeginRefresh(ctx, f.sourceID, f.cacheKey, f.identity)
	if err != nil {
		t.Fatalf("reserve later cache refresh: %v", err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: refresh, FetchedAt: time.Date(2026, 10, 12, 0, 1, 0, 0, time.UTC),
		Payload: json.RawMessage(`{"created":"2026-10-12T00:01:00Z","count":0,"offset":0,"recordings":[]}`),
	}); err != nil {
		t.Fatalf("replace mutable cache response: %v", err)
	}
	after, err := f.repository.GetMatchingFallbackSearchReceipt(ctx, f.runID, operation.ID)
	current, cacheErr := cache.GetCachedResponse(ctx, f.sourceID, f.cacheKey)
	if err != nil || cacheErr != nil || after == nil || current == nil || after.PayloadSHA256 != receipt.PayloadSHA256 ||
		after.CacheGeneration != receipt.CacheGeneration || string(after.Payload) != string(receipt.Payload) || string(current.Payload) == string(receipt.Payload) {
		t.Fatalf("cache refresh did not leave the receipt immutable: before=%+v after=%+v current=%+v err=%v cacheErr=%v", receipt, after, current, err, cacheErr)
	}
}

func TestFallbackSealRejectsSQLMutationAndRetainsDigestPostgreSQL(t *testing.T) {
	t.Parallel()
	f := seedFallbackSQLRun(t, false)
	ctx := f.ctx
	parent, err := f.repository.GetOperation(ctx, f.runID)
	if err != nil || parent.RiverJobID == nil {
		t.Fatalf("read admitted parent delivery: operation=%+v err=%v", parent, err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *parent.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("make admitted parent delivery claimable: %v", err)
	}
	claim, outcome, err := f.repository.ClaimMatchingDelivery(ctx, f.runID, parent.Attempt, *parent.RiverJobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim admitted parent delivery: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	client := openScanEnqueueRiver(t, f.db)
	changed, err := f.repository.CompleteEmptyFallbackPlanForDelivery(ctx, client, f.runID, parent.Attempt, *parent.RiverJobID, claim, 0)
	if err != nil || !changed {
		t.Fatalf("persist valid empty-plan wait: changed=%v err=%v", changed, err)
	}
	acquisition, found, err := f.repository.GetMatchingAcquisition(ctx, f.runID)
	if err != nil || !found || acquisition.Phase != "fallback_review_wait" {
		t.Fatalf("read persisted fallback review wait: acquisition=%+v found=%v err=%v", acquisition, found, err)
	}
	seal, sealed, err := f.repository.SealMatchingFallbackAuthority(ctx, f.runID, acquisition.Revision)
	if err != nil || !sealed || seal.Digest == "" {
		t.Fatalf("seal persisted fallback wait: seal=%+v sealed=%v err=%v", seal, sealed, err)
	}
	for _, mutation := range []struct {
		name  string
		query string
	}{
		{name: "update", query: `UPDATE matching_fallback_authority_seal SET authority='{}'::jsonb WHERE run_id=? AND wait_revision=?`},
		{name: "delete", query: `DELETE FROM matching_fallback_authority_seal WHERE run_id=? AND wait_revision=?`},
	} {
		if _, err := f.db.NewRaw(mutation.query, f.runID, acquisition.Revision).Exec(ctx); err == nil {
			t.Fatalf("seal %s bypass was accepted", mutation.name)
		} else {
			var postgresErr *pgconn.PgError
			if !errors.As(err, &postgresErr) || postgresErr.SQLState() != "P0001" {
				t.Fatalf("seal %s failed with unexpected error: %v", mutation.name, err)
			}
		}
		retained, exists, err := f.repository.GetMatchingFallbackAuthoritySeal(ctx, f.runID, acquisition.Revision)
		if err != nil || !exists || retained.Digest != seal.Digest {
			t.Fatalf("seal digest changed after rejected %s: retained=%+v exists=%v err=%v", mutation.name, retained, exists, err)
		}
	}
}

type fallbackSQLRun struct {
	ctx        context.Context
	db         *bun.DB
	repository *persistence.SetupManagerRepository
	runID      uuid.UUID
	requestID  uuid.UUID
	sourceID   uuid.UUID
	cacheKey   string
	identity   string
}

// seedFallbackSQLRun establishes one real CURRENT metadata capture and group,
// dispatches and claims a real reconciliation job, then admits a durable
// fallback run through the repository. It does not run HTTP or workers.
func seedFallbackSQLRun(t *testing.T, withSearch bool) fallbackSQLRun {
	t.Helper()
	ctx := context.Background()
	db := testpostgres.OpenMigratedWithRiver(t)
	repository := persistence.NewSetupManagerRepository(db)
	inventory := persistence.NewSourceInventoryRepository(db)
	rootPath, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := createInventoryRoot(t, ctx, inventory, rootPath)
	if _, err := db.NewRaw(`UPDATE source_root SET inventory_path=?,scan_generation=1,status='available',last_successful_scan_at=now() WHERE id=?`, rootPath, root.ID).Exec(ctx); err != nil {
		t.Fatalf("establish current root inventory: %v", err)
	}
	scanID, analysisID := uuid.New(), uuid.New()
	for id, kind := range map[uuid.UUID]string{scanID: "scan_source", analysisID: "analyze_source"} {
		if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,?, 'succeeded','complete','{}'::jsonb,now())`, id, kind).Exec(ctx); err != nil {
			t.Fatalf("seed completed %s provenance: %v", kind, err)
		}
	}
	location := insertAnalysisLocation(t, ctx, db, root.ID, "album/track-1.flac", 4096, probeMtime())
	workID, metadataID := uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO source_analysis_work
		(id,location_id,current_location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id)
		VALUES (?,?,?,?,?,?,?,?,?,false,?)`, workID, location.ID, location.ID, root.ID, rootPath, rootPath, location.RelativePath, location.SizeBytes, location.Mtime, scanID).Exec(ctx); err != nil {
		t.Fatalf("seed current source work: %v", err)
	}
	tags := json.RawMessage(`{}`)
	if withSearch {
		tags, err = json.Marshal(map[string][]string{"ALBUM": {"Fixture Album"}, "TITLE": {"Fallback Track"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.NewRaw(`INSERT INTO media_metadata_result(id,observed_tags,provenance,observed_at,winning_result_id,applied_operation_id)
		VALUES (?,?::jsonb,'{"source":"fixture"}'::jsonb,now(),?,?)`, metadataID, string(tags), metadataID, analysisID).Exec(ctx); err != nil {
		t.Fatalf("seed current metadata result: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO source_analysis_step(work_id,step,state,success_reuse_origin,success_metadata_result_id)
		VALUES (?,'metadata','succeeded','executed',?)`, workID, metadataID).Exec(ctx); err != nil {
		t.Fatalf("select current metadata result: %v", err)
	}
	captures, err := persistence.NewIncomingGroupingRepository(db).CurrentCaptures(ctx)
	if err != nil || len(captures) != 1 {
		t.Fatalf("read CURRENT metadata baseline: captures=%+v err=%v", captures, err)
	}
	capture := captures[0]
	if capture.WorkID != workID || capture.VariantID != workID || capture.LocationID != location.ID || !capture.Ready ||
		capture.CaptureAnalysisID == nil || *capture.CaptureAnalysisID != metadataID ||
		capture.MetadataWinner == nil || *capture.MetadataWinner != metadataID {
		t.Fatalf("fixture did not select CURRENT metadata for work %s: capture=%+v", workID, capture)
	}
	groupID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO incoming_group(id,revision,manual,diagnostics) VALUES (?,'fallback-sql-contract',false,'[]'::jsonb)`, groupID).Exec(ctx); err != nil {
		t.Fatalf("seed current incoming group: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO incoming_group_member(group_id,variant_id,ready) VALUES (?,?,true)`, groupID, workID).Exec(ctx); err != nil {
		t.Fatalf("seed current group member: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO incoming_group_member_location(group_id,variant_id,source_root_id,work_id,location_id,configured_path,inventory_path,relative_path,size_bytes,mtime)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, groupID, workID, root.ID, workID, location.ID, rootPath, rootPath, location.RelativePath, location.SizeBytes, location.Mtime).Exec(ctx); err != nil {
		t.Fatalf("seed current group location fence: %v", err)
	}
	tagSets := []map[string][]string{{"ALBUM": {"Fixture Album"}, "TITLE": {"Fallback Track"}}}
	if _, err := db.NewRaw(`UPDATE incoming_grouping_state SET needs_refresh=false WHERE singleton=true`).Exec(ctx); err != nil {
		t.Fatalf("mark current grouping fresh: %v", err)
	}
	const identity = "fallback-sql-contract"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatalf("seed provider configuration identity: %v", err)
	}
	cache := persistence.NewProviderCacheRepository(db)
	provider := persistence.Provider{ID: uuid.New(), Code: "musicbrainz"}
	if err := cache.CreateProvider(ctx, &provider); err != nil {
		t.Fatalf("seed MusicBrainz provider: %v", err)
	}
	source := persistence.ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "fallback-sql-contract", Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: identity}
	if err := cache.CreateProviderSource(ctx, &source); err != nil {
		t.Fatalf("seed MusicBrainz provider source: %v", err)
	}
	eventID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO incoming_matching_reconcile_event(id,reason,source_input_snapshot) VALUES (?,'startup','{}'::jsonb)`, eventID).Exec(ctx); err != nil {
		t.Fatalf("seed pending reconciliation event: %v", err)
	}
	client := openScanEnqueueRiver(t, db)
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("register real reconciliation delivery: %v", err)
	}
	event, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, eventID)
	if err != nil || event == nil || event.RegisteredJobID == nil {
		t.Fatalf("read registered reconciliation event: event=%+v err=%v", event, err)
	}
	epoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, eventID, *event.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim real reconciliation delivery: claimed=%v err=%v", claimed, err)
	}

	plan := policy.TypedFallbackQueryPlan{SchemaVersion: 1, Stages: []policy.TypedFallbackQueryStage{}}
	var providerRequest persistence.MatchingProviderRequest
	var requestID uuid.UUID
	var cacheKey string
	if withSearch {
		planned, artistBearing := policy.PlanFallbackQueries(tagSets)
		if artistBearing || len(planned.Stages) == 0 {
			t.Fatalf("fallback fixture did not produce an artist-free search plan: plan=%+v artistBearing=%v", planned, artistBearing)
		}
		plan = policy.TypedFallbackQueryPlan{SchemaVersion: planned.SchemaVersion, Stages: planned.Stages}
		stage := plan.Stages[0]
		kind, prefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
		if !ok {
			t.Fatalf("fallback stage %q has no provider mapping", stage.Kind)
		}
		cacheKey = fmt.Sprintf("%s:0:10:%s", prefix, stage.Query)
		input, err := json.Marshal(struct {
			SchemaVersion int    `json:"schema_version"`
			Query         string `json:"query"`
			Offset        int    `json:"offset"`
			Limit         int    `json:"limit"`
		}{1, stage.Query, 0, 10})
		if err != nil {
			t.Fatal(err)
		}
		requestID = uuid.New()
		providerID, sourceID, providerIdentity := provider.ID, source.ID, identity
		providerOperation := &persistence.Operation{ID: requestID, Kind: kind, State: "queued", Stage: "queued", InputSnapshot: input,
			ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &providerIdentity,
			ProviderEndpoint: source.Endpoint, ProviderCacheKey: &cacheKey, Attempt: 1}
		providerRequest = persistence.MatchingProviderRequest{Operation: providerOperation, Args: service.ProviderFetchOperationArgs{OperationID: requestID}}
	}
	runID := uuid.New()
	parent := &persistence.Operation{ID: runID, Kind: "matching_evaluation_v1", State: "queued", Stage: "queued", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	admitted, err := repository.AdmitAutomaticFallbackMatchingRun(ctx, client, persistence.AutomaticFallbackMatchingAdmission{
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: runID}, Provider: providerRequest,
		Source:  persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: provider.ID, Endpoint: source.Endpoint, ConfigurationIdentity: identity},
		GroupID: groupID, ExpectedGroupRevision: "fallback-sql-contract", PolicyVersion: service.MatchingPolicyVersion, Plan: plan,
		EventID: eventID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: epoch,
	})
	if err != nil || admitted.AlreadyAdmitted || admitted.Operation == nil {
		t.Fatalf("admit durable fallback SQL fixture: result=%+v err=%v", admitted, err)
	}
	return fallbackSQLRun{ctx: ctx, db: db, repository: repository, runID: runID, requestID: requestID, sourceID: source.ID, cacheKey: cacheKey, identity: identity}
}
