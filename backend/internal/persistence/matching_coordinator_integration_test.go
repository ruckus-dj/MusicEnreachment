//go:build integration

package persistence_test

import (
	"bytes"
	"context"
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
		return persistence.MatchingAdmission{AdmissionKey: key, GroupingEpoch: state.Revision, Targets: []persistence.MatchingTarget{{GroupID: group.ID, Revision: group.Revision}}, Plan: plan, PolicyVersion: "test", Parent: &persistence.Operation{ID: parent, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{"plan":{}}`)}, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parent}, Providers: providers}
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
		Plan:    matchingTestPlan(t, "rollback", source, identity), PolicyVersion: "test",
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
	admission := persistence.MatchingAdmission{AdmissionKey: "concurrent-child-failures", GroupingEpoch: state.Revision, Targets: []persistence.MatchingTarget{{GroupID: group.ID, Revision: group.Revision}}, Plan: matchingTestPlan(t, "concurrent", source, identity), PolicyVersion: "test", Parent: &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{"plan":{}}`)}, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID}, Providers: children}
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
