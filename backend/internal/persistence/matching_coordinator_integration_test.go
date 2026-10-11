//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestMatchingAcquisitionAdvancesArtistResponseToBrowseAtomicallyPostgres(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	const identity = "matching-acquisition-config"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity=? WHERE id=?`, identity, fixture.source).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var group persistence.IncomingGroupingGroup
	if err := db.NewRaw(`SELECT id,revision FROM incoming_group ORDER BY id LIMIT 1`).Scan(ctx, &group); err != nil {
		t.Fatal(err)
	}
	var grouping persistence.IncomingGroupingState
	if err := db.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &grouping); err != nil {
		t.Fatal(err)
	}
	var source persistence.ProviderSource
	if err := db.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &source); err != nil {
		t.Fatal(err)
	}
	const artistID = "11111111-1111-4111-8111-111111111111"
	artistCacheKey := "artist-search:0:100:artist"
	artistSnapshot := json.RawMessage(`{"schema_version":1,"query":"artist","offset":0,"limit":100}`)
	parentID, artistRequestID := uuid.New(), uuid.New()
	plan := matchingTestPlan(t, "artist acquisition", source, identity)
	parent := &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{"schema_version":1,"plan":{}}`)}
	artistOperation := &persistence.Operation{
		ID: artistRequestID, Kind: "provider_artist_search", State: "queued", Stage: "fetch", InputSnapshot: artistSnapshot,
		ProviderSourceID: &source.ID, ProviderConfigurationIdentity: matchingCoordinatorStringPtr(identity),
		ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint, ProviderCacheKey: &artistCacheKey,
	}
	repo := persistence.NewSetupManagerRepository(db)
	riverClient := openScanEnqueueRiver(t, db)
	admitted, err := repo.AdmitMatchingRun(ctx, riverClient, persistence.MatchingAdmission{
		AdmissionKey: "artist-acquisition", GroupingEpoch: grouping.Revision,
		Targets: []persistence.MatchingTarget{{GroupID: group.ID, Revision: group.Revision}},
		Plan:    plan, AcquisitionIntentVersion: 1, PolicyVersion: service.MatchingPolicyVersion,
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Providers: []persistence.MatchingProviderRequest{{Operation: artistOperation, Args: service.ProviderFetchOperationArgs{OperationID: artistRequestID}}},
	})
	if err != nil {
		t.Fatalf("admit acquisition intent: %v", err)
	}
	if admitted.RiverJobID == nil {
		t.Fatal("initial parent delivery has no River ID")
	}
	initialParentJobID := *admitted.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, initialParentJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	initialClaim, initialOutcome, err := repo.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, initialParentJobID, 1)
	if err != nil || initialOutcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim initial parent delivery: outcome=%v err=%v", initialOutcome, err)
	}
	if consumed, err := repo.ConsumePendingMatchingDelivery(ctx, parentID, admitted.Attempt, initialParentJobID, initialClaim); err != nil || !consumed {
		t.Fatalf("park parent behind artist child: consumed=%v err=%v", consumed, err)
	}
	cache := persistence.NewProviderCacheRepository(db)
	ticket, err := cache.BeginRefresh(ctx, source.ID, artistCacheKey, identity)
	if err != nil {
		t.Fatal(err)
	}
	artistPayload := json.RawMessage(`{"artists":[{"id":"` + artistID + `"}],"count":1,"offset":0}`)
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: artistPayload}); err != nil {
		t.Fatal(err)
	}
	accepted, err := cache.GetCachedResponse(ctx, source.ID, artistCacheKey)
	if err != nil || accepted == nil {
		t.Fatalf("read accepted artist response: response=%+v err=%v", accepted, err)
	}
	var artistJobID int64
	if err := db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, artistRequestID).Scan(ctx, &artistJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, artistJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	epoch, claimed, err := repo.ClaimProviderOperationDelivery(ctx, artistRequestID, 1, artistJobID)
	if err != nil || !claimed {
		t.Fatalf("claim artist child: claimed=%v err=%v", claimed, err)
	}
	if changed, err := repo.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, artistRequestID, 1, artistJobID, epoch, riverClient, func(operation *persistence.Operation) error {
		operation.State, operation.Stage = "succeeded", "provider_response_saved"
		finished := time.Now().UTC()
		operation.FinishedAt = &finished
		return nil
	}); err != nil || !changed {
		t.Fatalf("settle artist child and wake parent: changed=%v err=%v", changed, err)
	}
	var parentJobID int64
	if err := db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? ORDER BY id DESC LIMIT 1`, parentID).Scan(ctx, &parentJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, parentJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	claim, outcome, err := repo.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, parentJobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim parent wake: outcome=%v err=%v", outcome, err)
	}
	browseID := uuid.New()
	browseKey := "artist-release-browse:" + artistID + ":0:100"
	browseInput := json.RawMessage(`{"schema_version":1,"mbid":"` + artistID + `","offset":0,"limit":100}`)
	browse := persistence.MatchingProviderRequest{
		Operation: &persistence.Operation{
			ID: browseID, Kind: "provider_artist_release_browse", State: "queued", Stage: "fetch", InputSnapshot: browseInput,
			ProviderSourceID: &source.ID, ProviderConfigurationIdentity: matchingCoordinatorStringPtr(identity),
			ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint, ProviderCacheKey: &browseKey,
		},
		Args: service.ProviderFetchOperationArgs{OperationID: browseID},
	}
	digest := sha256.Sum256(accepted.Payload)
	phase := persistence.MatchingAcquisitionPhase{
		Revision: 1, Phase: persistence.MatchingAcquisitionArtistCatalogPage,
		Input:              json.RawMessage(`{"artist_mbid":"` + artistID + `","offset":0,"limit":100}`),
		Output:             json.RawMessage(`{"artist_mbid":"` + artistID + `","next_offset":0,"complete":false}`),
		ResponseProvenance: json.RawMessage(`[{"request_id":"` + artistRequestID.String() + `","cache_key":"` + artistCacheKey + `","payload_sha256":"` + fmt.Sprintf("%x", digest[:]) + `"}]`),
	}
	failing := &matchingCoordinatorFailingInserter{delegate: riverClient, failAt: 1, err: errors.New("injected browse enqueue failure")}
	if changed, err := repo.AdvanceMatchingAcquisitionForDelivery(ctx, failing, parentID, admitted.Attempt, parentJobID, claim, 0, phase, []persistence.MatchingProviderRequest{browse}); err == nil || changed {
		t.Fatalf("failed child insert advanced acquisition: changed=%v err=%v", changed, err)
	}
	var phases, browseOperations, consumed int
	if err := db.NewRaw(`SELECT count(*) FROM matching_acquisition_phase WHERE run_id=?`, parentID).Scan(ctx, &phases); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id=?`, browseID).Scan(ctx, &browseOperations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND river_job_id=? AND consumed_at IS NOT NULL`, parentID, parentJobID).Scan(ctx, &consumed); err != nil {
		t.Fatal(err)
	}
	if phases != 1 || browseOperations != 0 || consumed != 0 {
		t.Fatalf("failed atomic advance left phases/child/consumed-delivery=%d/%d/%d", phases, browseOperations, consumed)
	}
	changed, err := repo.AdvanceMatchingAcquisitionForDelivery(ctx, riverClient, parentID, admitted.Attempt, parentJobID, claim, 0, phase, []persistence.MatchingProviderRequest{browse})
	if err != nil || !changed {
		t.Fatalf("advance artist response to browse: changed=%v err=%v", changed, err)
	}
	var parentState, runState, currentPhase string
	var revision int64
	if err := db.NewRaw(`SELECT operation.state, run.state, acquisition.current_phase, acquisition.revision
		FROM operation JOIN matching_run run ON run.id=operation.id JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		WHERE operation.id=?`, parentID).Scan(ctx, &parentState, &runState, &currentPhase, &revision); err != nil {
		t.Fatal(err)
	}
	if parentState != "running" || runState != "waiting_provider" || currentPhase != persistence.MatchingAcquisitionArtistCatalogPage || revision != 1 {
		t.Fatalf("parent/run/phase/revision after advance=%q/%q/%q/%d", parentState, runState, currentPhase, revision)
	}
	storedBrowse, err := repo.GetOperation(ctx, browseID)
	if err != nil || storedBrowse.State != "queued" || storedBrowse.RiverJobID == nil {
		t.Fatalf("browse child after advance=%+v err=%v", storedBrowse, err)
	}
	// Reconstruct the repository as a restarted worker process; replaying the old
	// delivery cannot create a second child after the transaction committed.
	repo = persistence.NewSetupManagerRepository(db)
	if changed, err := repo.AdvanceMatchingAcquisitionForDelivery(ctx, riverClient, parentID, admitted.Attempt, parentJobID, claim, 0, phase, []persistence.MatchingProviderRequest{browse}); err != nil || changed {
		t.Fatalf("duplicate old delivery advanced twice: changed=%v err=%v", changed, err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND request_id=?`, parentID, browseID).Scan(ctx, &browseOperations); err != nil || browseOperations != 1 {
		t.Fatalf("browse dependency count=%d err=%v", browseOperations, err)
	}
}

func TestMatchingCoordinatorAdmissionIdempotencyAndFencesPostgres(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	const identity = "matching-admission-config"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity=? WHERE id=?`, identity, fixture.source).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var state persistence.IncomingGroupingState
	if err := db.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &state); err != nil {
		t.Fatal(err)
	}
	var group persistence.IncomingGroupingGroup
	if err := db.NewRaw(`SELECT id,revision FROM incoming_group ORDER BY id LIMIT 1`).Scan(ctx, &group); err != nil {
		t.Fatal(err)
	}
	var source persistence.ProviderSource
	if err := db.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &source); err != nil {
		t.Fatal(err)
	}
	parentID, requestID := uuid.New(), uuid.New()
	operation := func(id uuid.UUID, cacheKey string) *persistence.Operation {
		return &persistence.Operation{ID: id, Kind: "provider_release_lookup", State: "queued", Stage: "fetch", InputSnapshot: json.RawMessage(`{"query":"safe"}`), ProviderSourceID: &source.ID, ProviderConfigurationIdentity: matchingCoordinatorStringPtr(identity), ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint, ProviderCacheKey: &cacheKey}
	}
	planFor := func(intent string) json.RawMessage {
		encoded, err := json.Marshal(struct {
			SchemaVersion                 int       `json:"schema_version"`
			Intent                        string    `json:"intent"`
			ProviderSourceID              uuid.UUID `json:"provider_source_id"`
			EndpointConfigurationIdentity string    `json:"endpoint_configuration_identity"`
			ProviderID                    uuid.UUID `json:"provider_id"`
		}{1, intent, source.ID, identity, source.ProviderID})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	plan := planFor("safe")
	makeAdmissionFor := func(key string, plan json.RawMessage, parent uuid.UUID, children ...uuid.UUID) persistence.MatchingAdmission {
		providers := make([]persistence.MatchingProviderRequest, 0, len(children))
		for i, childID := range children {
			cacheKey := "release-lookup:fixture"
			if i > 0 {
				cacheKey = fmt.Sprintf("release-lookup:fixture:%d", i)
			}
			providers = append(providers, persistence.MatchingProviderRequest{Operation: operation(childID, cacheKey), Args: service.ProviderFetchOperationArgs{OperationID: childID}})
		}
		return persistence.MatchingAdmission{AdmissionKey: key, GroupingEpoch: state.Revision, Targets: []persistence.MatchingTarget{{GroupID: group.ID, Revision: group.Revision}}, Plan: plan, PolicyVersion: "matching-rapidfuzz-v1", Parent: &persistence.Operation{ID: parent, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{"plan":{}}`)}, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parent}, Providers: providers}
	}
	makeAdmission := func(plan json.RawMessage, parent uuid.UUID) persistence.MatchingAdmission {
		return makeAdmissionFor("same-admission", plan, parent, requestID)
	}
	repo := persistence.NewSetupManagerRepository(db)
	riverClient := openScanEnqueueRiver(t, db)
	cacheRepo := persistence.NewProviderCacheRepository(db)
	ticket, err := cacheRepo.BeginRefresh(ctx, source.ID, "release-lookup:fixture", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cacheRepo.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Revision: "accepted-v1", Payload: json.RawMessage(`{"accepted":"old"}`)}); err != nil {
		t.Fatal(err)
	}
	acceptedBefore, err := cacheRepo.GetCachedResponse(ctx, source.ID, "release-lookup:fixture")
	if err != nil || acceptedBefore == nil {
		t.Fatalf("read accepted response before admission: response=%+v err=%v", acceptedBefore, err)
	}
	first, err := repo.AdmitMatchingRun(ctx, riverClient, makeAdmission(plan, parentID))
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	type admissionResult struct {
		operation *persistence.Operation
		err       error
	}
	results := make(chan admissionResult, 2)
	var admissionWorkers sync.WaitGroup
	for range 2 {
		admissionWorkers.Add(1)
		go func() {
			defer admissionWorkers.Done()
			operation, err := repo.AdmitMatchingRun(ctx, riverClient, makeAdmission(plan, uuid.New()))
			results <- admissionResult{operation: operation, err: err}
		}()
	}
	admissionWorkers.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent repeat admission: %v", result.err)
		}
		if result.operation.ID != parentID {
			t.Fatalf("concurrent repeat admission ID=%s, want %s", result.operation.ID, parentID)
		}
	}
	if first.ID != parentID {
		t.Fatalf("initial admission ID=%s, want %s", first.ID, parentID)
	}
	var parents, children, deps, highWater, initialDeliveries int
	if err := db.NewRaw(`SELECT count(*) FROM matching_run WHERE plan_admission_key='same-admission'`).Scan(ctx, &parents); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`, parentID).Scan(ctx, &deps); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id=?`, requestID).Scan(ctx, &children); err != nil {
		t.Fatal(err)
	}
	if first.RiverJobID == nil || *first.RiverJobID <= 0 {
		t.Fatalf("admitted parent River delivery identity = %+v", first.RiverJobID)
	}
	if err := db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, *first.RiverJobID).Scan(ctx, &initialDeliveries); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT high_water FROM incoming_matching_generation WHERE group_id=?`, group.ID).Scan(ctx, &highWater); err != nil {
		t.Fatal(err)
	}
	if parents != 1 || children != 1 || deps != 1 || highWater != 1 || initialDeliveries != 1 {
		t.Fatalf("counts parent/child/dependency/generation/initial-delivery=%d/%d/%d/%d/%d", parents, children, deps, highWater, initialDeliveries)
	}
	if _, err := repo.AdmitMatchingRun(ctx, riverClient, makeAdmission(planFor("changed"), uuid.New())); err == nil {
		t.Fatal("different plan reused admission key")
	}
	var count int
	if err := db.NewRaw(`SELECT count(*) FROM matching_run WHERE plan_admission_key='same-admission'`).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("conflicting retry changed prior admission: %d %v", count, err)
	}
	if _, err := repo.AdmitMatchingRun(ctx, riverClient, makeAdmission(planFor("different-plan"), uuid.New())); err == nil {
		t.Fatal("different plan admitted")
	}
	// Terminal provider transition settles its durable dependency and records a
	// parent wake in the same transaction. A repeated terminal transition cannot
	// emit a second dependency wake.
	var jobID int64
	if err := db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, requestID).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	providerOperation, err := repo.GetOperation(ctx, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if providerOperation.State != "queued" || providerOperation.Kind != "provider_release_lookup" || providerOperation.RiverJobID == nil || *providerOperation.RiverJobID != jobID {
		t.Fatalf("stored provider delivery fence = %+v job=%d", providerOperation, jobID)
	}
	transition := func(op *persistence.Operation) error {
		op.State = "failed"
		op.Stage = "complete"
		op.SafeError = matchingCoordinatorStringPtr("provider unavailable")
		finished := time.Now().UTC()
		op.FinishedAt = &finished
		return nil
	}
	epoch, claimed, err := repo.ClaimProviderOperationDelivery(ctx, requestID, providerOperation.Attempt, jobID)
	if err != nil || !claimed {
		t.Fatalf("claim provider operation: claimed=%v err=%v", claimed, err)
	}
	failingWakeInserter := &matchingCoordinatorFailingInserter{delegate: riverClient, failAt: 1, err: errors.New("injected matching wake failure")}
	if changed, err := repo.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, requestID, providerOperation.Attempt, jobID, epoch, failingWakeInserter, transition); err == nil || changed {
		t.Fatalf("terminal transition with failed wake insert changed=%v err=%v, want rollback", changed, err)
	}
	providerOperation, err = repo.GetOperation(ctx, requestID)
	if err != nil || providerOperation.State != "queued" {
		t.Fatalf("failed wake insert did not roll back provider terminal state: operation=%+v err=%v", providerOperation, err)
	}
	changed, err := repo.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, requestID, providerOperation.Attempt, jobID, epoch, riverClient, transition)
	if err != nil || !changed {
		t.Fatalf("terminal transition changed=%v err=%v", changed, err)
	}
	changed, err = repo.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, requestID, providerOperation.Attempt, jobID, epoch, riverClient, transition)
	if err != nil || !changed {
		t.Fatalf("repeat terminal transition changed=%v err=%v", changed, err)
	}
	var settled, wakes int
	if err := db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE request_id=? AND terminal_outcome='failed' AND settled_at IS NOT NULL`, requestID).Scan(ctx, &settled); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &wakes); err != nil {
		t.Fatal(err)
	}
	if settled != 1 || wakes != 2 {
		t.Fatalf("settled dependencies/wakes=%d/%d, want 1/2 including initial delivery", settled, wakes)
	}
	acceptedAfter, err := cacheRepo.GetCachedResponse(ctx, source.ID, "release-lookup:fixture")
	if err != nil || acceptedAfter == nil || acceptedAfter.Revision == nil || *acceptedAfter.Revision != "accepted-v1" || !bytes.Equal(acceptedAfter.Payload, acceptedBefore.Payload) {
		t.Fatalf("failed provider transition changed accepted cache: before=%+v after=%+v err=%v", acceptedBefore, acceptedAfter, err)
	}
	if _, err := db.NewRaw(`UPDATE operation SET state='failed', stage='evaluation_failed', safe_error='temporary failure', finished_at=now() WHERE id=?`, parentID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE matching_run SET state='failed' WHERE id=?`, parentID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	retried, err := repo.RetryMatchingEvaluationOperationAndEnqueue(ctx, parentID, riverClient,
		service.MatchingEvaluationOperationArgs{OperationID: parentID}, nil)
	if err != nil {
		t.Fatalf("retry matching parent: %v", err)
	}
	var deliveryCount int
	var executionEpoch int64
	if err := db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &deliveryCount); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT execution_epoch FROM matching_run WHERE id=?`, parentID).Scan(ctx, &executionEpoch); err != nil {
		t.Fatal(err)
	}
	if retried.State != "queued" || retried.Attempt != first.Attempt+1 || retried.RiverJobID == nil || deliveryCount != 3 || executionEpoch != 1 {
		t.Fatalf("matching retry did not atomically register a new attempt: operation=%+v deliveries=%d epoch=%d", retried, deliveryCount, executionEpoch)
	}
	const changedIdentity = "matching-admission-config-new"
	if _, err := db.NewRaw(`UPDATE app_setting SET setting_value=? WHERE setting_name='musicbrainz_config_identity'`, changedIdentity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity=? WHERE id=?`, changedIdentity, source.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	staleParent := uuid.New()
	if _, err := repo.AdmitMatchingRun(ctx, riverClient, makeAdmissionFor("stale-config", plan, staleParent, uuid.New())); err == nil {
		t.Fatal("admission accepted a captured provider fence after configuration changed")
	}
	var staleRows int
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id=?`, staleParent).Scan(ctx, &staleRows); err != nil || staleRows != 0 {
		t.Fatalf("stale-config admission persisted parent=%d err=%v", staleRows, err)
	}
}

func TestMatchingCoordinatorAdmissionRollsBackWhenRiverInsertFailsPostgres(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	const identity = "matching-admission-rollback-config"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity=? WHERE id=?`, identity, fixture.source).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var state persistence.IncomingGroupingState
	if err := db.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &state); err != nil {
		t.Fatal(err)
	}
	var group persistence.IncomingGroupingGroup
	if err := db.NewRaw(`SELECT id,revision FROM incoming_group ORDER BY id LIMIT 1`).Scan(ctx, &group); err != nil {
		t.Fatal(err)
	}
	var source persistence.ProviderSource
	if err := db.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &source); err != nil {
		t.Fatal(err)
	}
	parentID, childID := uuid.New(), uuid.New()
	cacheKey := "release-lookup:rollback"
	admission := persistence.MatchingAdmission{
		AdmissionKey: "river-failure", GroupingEpoch: state.Revision,
		Targets: []persistence.MatchingTarget{{GroupID: group.ID, Revision: group.Revision}},
		Plan:    matchingTestPlan(t, "rollback", source, identity), PolicyVersion: "matching-rapidfuzz-v1",
		Parent:     &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{"plan":{}}`)},
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Providers:  []persistence.MatchingProviderRequest{{Operation: &persistence.Operation{ID: childID, Kind: "provider_release_lookup", State: "queued", Stage: "fetch", InputSnapshot: json.RawMessage(`{"query":"safe"}`), ProviderSourceID: &source.ID, ProviderConfigurationIdentity: matchingCoordinatorStringPtr(identity), ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint, ProviderCacheKey: &cacheKey}, Args: service.ProviderFetchOperationArgs{OperationID: childID}}},
	}
	riverClient := openScanEnqueueRiver(t, db)
	failing := &matchingCoordinatorFailingInserter{delegate: riverClient, failAt: 2, err: errors.New("injected provider insert failure")}
	if _, err := persistence.NewSetupManagerRepository(db).AdmitMatchingRun(ctx, failing, admission); err == nil {
		t.Fatal("admission succeeded after provider River insert failure")
	}
	for query, args := range map[string][]any{
		`SELECT count(*) FROM operation WHERE id IN (?,?)`:                           {parentID, childID},
		`SELECT count(*) FROM matching_run WHERE plan_admission_key='river-failure'`: nil,
		`SELECT count(*) FROM matching_delivery WHERE run_id=?`:                      {parentID},
		`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`:    {parentID},
		`SELECT count(*) FROM incoming_matching_generation WHERE group_id=?`:         {group.ID},
	} {
		var count int
		if err := db.NewRaw(query, args...).Scan(ctx, &count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed admission left %d records for %q", count, query)
		}
	}
}

func TestMatchingCoordinatorConcurrentChildFailuresRecordOneWakeEachPostgres(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	const identity = "matching-concurrent-config"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity=? WHERE id=?`, identity, fixture.source).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var state persistence.IncomingGroupingState
	if err := db.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &state); err != nil {
		t.Fatal(err)
	}
	var group persistence.IncomingGroupingGroup
	if err := db.NewRaw(`SELECT id,revision FROM incoming_group ORDER BY id LIMIT 1`).Scan(ctx, &group); err != nil {
		t.Fatal(err)
	}
	var source persistence.ProviderSource
	if err := db.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &source); err != nil {
		t.Fatal(err)
	}
	parentID, childIDs := uuid.New(), []uuid.UUID{uuid.New(), uuid.New()}
	children := make([]persistence.MatchingProviderRequest, 0, len(childIDs))
	for i, childID := range childIDs {
		cacheKey := fmt.Sprintf("release-lookup:concurrent:%d", i)
		children = append(children, persistence.MatchingProviderRequest{Operation: &persistence.Operation{ID: childID, Kind: "provider_release_lookup", State: "queued", Stage: "fetch", InputSnapshot: json.RawMessage(`{"query":"safe"}`), ProviderSourceID: &source.ID, ProviderConfigurationIdentity: matchingCoordinatorStringPtr(identity), ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint, ProviderCacheKey: &cacheKey}, Args: service.ProviderFetchOperationArgs{OperationID: childID}})
	}
	admission := persistence.MatchingAdmission{AdmissionKey: "concurrent-child-failures", GroupingEpoch: state.Revision, Targets: []persistence.MatchingTarget{{GroupID: group.ID, Revision: group.Revision}}, Plan: matchingTestPlan(t, "concurrent", source, identity), PolicyVersion: "matching-rapidfuzz-v1", Parent: &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{"plan":{}}`)}, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID}, Providers: children}
	repo := persistence.NewSetupManagerRepository(db)
	riverClient := openScanEnqueueRiver(t, db)
	if _, err := repo.AdmitMatchingRun(ctx, riverClient, admission); err != nil {
		t.Fatalf("admit: %v", err)
	}
	type delivery struct {
		id      uuid.UUID
		attempt int
		jobID   int64
		epoch   int64
	}
	deliveries := make([]delivery, 0, len(childIDs))
	for _, childID := range childIDs {
		operation, err := repo.GetOperation(ctx, childID)
		if err != nil {
			t.Fatal(err)
		}
		if operation.Kind != "provider_release_lookup" || operation.RiverJobID == nil {
			t.Fatalf("provider delivery identity = %+v", operation)
		}
		epoch, claimed, err := repo.ClaimProviderOperationDelivery(ctx, childID, operation.Attempt, *operation.RiverJobID)
		if err != nil || !claimed {
			t.Fatalf("claim %s: claimed=%v err=%v", childID, claimed, err)
		}
		deliveries = append(deliveries, delivery{id: childID, attempt: operation.Attempt, jobID: *operation.RiverJobID, epoch: epoch})
	}
	start := make(chan struct{})
	errs := make(chan error, len(deliveries))
	var workers sync.WaitGroup
	for _, item := range deliveries {
		workers.Add(1)
		go func(item delivery) {
			defer workers.Done()
			<-start
			changed, err := repo.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, item.id, item.attempt, item.jobID, item.epoch, riverClient, func(op *persistence.Operation) error {
				op.State, op.Stage = "failed", "complete"
				op.SafeError = matchingCoordinatorStringPtr("provider unavailable")
				finished := time.Now().UTC()
				op.FinishedAt = &finished
				return nil
			})
			if err != nil {
				errs <- err
			} else if !changed {
				errs <- fmt.Errorf("terminal transition for %s was not applied", item.id)
			}
		}(item)
	}
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for _, item := range deliveries {
		changed, err := repo.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, item.id, item.attempt, item.jobID, item.epoch, riverClient, func(op *persistence.Operation) error {
			op.State, op.Stage = "failed", "complete"
			op.SafeError = matchingCoordinatorStringPtr("provider unavailable")
			finished := time.Now().UTC()
			op.FinishedAt = &finished
			return nil
		})
		if err != nil || !changed {
			t.Fatalf("duplicate terminal transition for %s: changed=%v err=%v", item.id, changed, err)
		}
	}
	var settled, wakeCount int
	if err := db.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND terminal_outcome='failed' AND settled_at IS NOT NULL`, parentID).Scan(ctx, &settled); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=?`, parentID).Scan(ctx, &wakeCount); err != nil {
		t.Fatal(err)
	}
	if settled != len(childIDs) || wakeCount != len(childIDs)+1 {
		t.Fatalf("settled dependencies/deliveries=%d/%d, want %d/%d", settled, wakeCount, len(childIDs), len(childIDs)+1)
	}
}

func matchingTestPlan(t *testing.T, intent string, source persistence.ProviderSource, identity string) json.RawMessage {
	t.Helper()
	plan, err := json.Marshal(struct {
		SchemaVersion                 int       `json:"schema_version"`
		Intent                        string    `json:"intent"`
		ProviderSourceID              uuid.UUID `json:"provider_source_id"`
		EndpointConfigurationIdentity string    `json:"endpoint_configuration_identity"`
		ProviderID                    uuid.UUID `json:"provider_id"`
	}{1, intent, source.ID, identity, source.ProviderID})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

type matchingCoordinatorFailingInserter struct {
	delegate persistence.RiverInserter
	failAt   int
	calls    int
	err      error
}

func (inserter *matchingCoordinatorFailingInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	inserter.calls++
	if inserter.calls == inserter.failAt {
		return nil, inserter.err
	}
	return inserter.delegate.InsertTx(ctx, tx, args, options)
}

func matchingCoordinatorStringPtr(value string) *string { return &value }
