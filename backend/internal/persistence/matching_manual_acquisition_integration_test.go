//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestManualReleaseMatchingAdmissionIsIdempotentAndAtomicPostgres(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	if err := persistence.NewIncomingMatchCandidateRepository(db).Replace(ctx, fixture.evaluation(nil)); err != nil {
		t.Fatal(err)
	}
	header, _, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read captured evaluation: header=%+v err=%v", header, err)
	}
	var state persistence.IncomingGroupingState
	if err := db.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &state); err != nil {
		t.Fatal(err)
	}
	var source persistence.ProviderSource
	if err := db.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &source); err != nil {
		t.Fatal(err)
	}
	selected := uuid.New() // Explicit selection is not restricted to the automatic catalog.
	requestID := uuid.New()
	plan := persistence.ManualMatchingRunPlan{
		GroupingEpoch: state.Revision, PolicyVersion: "matching-rapidfuzz-v1",
		Plan:          json.RawMessage(`{"schema_version":1,"evaluation":"manual"}`),
		GroupContext:  json.RawMessage(`{"group_id":"` + fixture.group.ID.String() + `","revision":"` + fixture.group.Revision + `"}`),
		SourceContext: json.RawMessage(`{"provider_id":"` + source.ProviderID.String() + `","provider_source_id":"` + source.ID.String() + `","endpoint":"` + source.Endpoint + `","configuration_identity":"` + source.ConfigurationIdentity + `"}`),
	}
	makeChild := func(id uuid.UUID) persistence.MatchingProviderRequest {
		mbid := selected.String()
		key := "release-lookup:" + mbid
		identity := source.ConfigurationIdentity
		return persistence.MatchingProviderRequest{Operation: &persistence.Operation{ID: id, Kind: "provider_release_lookup", State: "queued", Stage: "queued", InputSnapshot: json.RawMessage(`{"schema_version":1,"mbid":"` + mbid + `"}`), ProviderSourceID: &source.ID, ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint, ProviderConfigurationIdentity: &identity, ProviderCacheKey: &key}, Args: service.ProviderFetchOperationArgs{OperationID: id}}
	}
	repo := persistence.NewSetupManagerRepository(db)
	riverClient := openScanEnqueueRiver(t, db)
	admit := func(child persistence.MatchingProviderRequest, p persistence.ManualMatchingRunPlan) (*persistence.Operation, error) {
		return repo.AdmitManualReleaseMatchingRun(ctx, riverClient, requestID, fixture.group.ID, fixture.group.Revision, &header.EvaluationRevision, selected, child, p)
	}
	staleEvalID := uuid.New()
	staleEvaluation := "stale-evaluation"
	if _, err := repo.AdmitManualReleaseMatchingRun(ctx, riverClient, staleEvalID, fixture.group.ID, fixture.group.Revision, &staleEvaluation, selected, makeChild(uuid.New()), plan); err == nil {
		t.Fatal("admission accepted a stale evaluation revision")
	}
	if _, err := repo.AdmitManualReleaseMatchingRun(ctx, riverClient, uuid.New(), fixture.group.ID, fixture.group.Revision, nil, selected, makeChild(uuid.New()), plan); err == nil {
		t.Fatal("admission accepted an absent-evaluation fence when an evaluation exists")
	}
	var staleRuns int
	if err := db.NewRaw(`SELECT count(*) FROM matching_run WHERE plan_admission_key=?`, "manual:"+staleEvalID.String()).Scan(ctx, &staleRuns); err != nil || staleRuns != 0 {
		t.Fatalf("stale evaluation admission persisted %d runs: %v", staleRuns, err)
	}
	child := makeChild(uuid.New())
	parent, err := admit(child, plan)
	if err != nil {
		t.Fatalf("admit manual lookup: %v", err)
	}
	if parent.RiverJobID == nil || parent.Stage != "manual_lookup" {
		t.Fatalf("manual parent delivery = %+v", parent)
	}
	// A retry may prepare a fresh provider operation, but request identity binds
	// the same canonical user intent and returns the original parent/job.
	duplicate, err := admit(makeChild(uuid.New()), plan)
	if err != nil || duplicate.ID != parent.ID || duplicate.RiverJobID == nil || *duplicate.RiverJobID != *parent.RiverJobID {
		t.Fatalf("idempotent retry = %+v err=%v", duplicate, err)
	}
	changedPlan := plan
	changedPlan.GroupContext = json.RawMessage(`{"group_id":"` + fixture.group.ID.String() + `","revision":"different"}`)
	if _, err := admit(makeChild(uuid.New()), changedPlan); err == nil {
		t.Fatal("changed target reused manual request ID")
	}
	snapshot, exists, err := repo.GetManualReleaseMatchingRun(ctx, requestID)
	if err != nil || !exists || snapshot.RunID != parent.ID || snapshot.SelectedReleaseMBID != selected || !snapshot.Pending || snapshot.LookupOperationID != child.Operation.ID {
		t.Fatalf("manual snapshot = %+v exists=%v err=%v", snapshot, exists, err)
	}
	var generations, runs, dependencies int
	for query, dest := range map[string]*int{
		`SELECT high_water FROM incoming_matching_generation WHERE group_id=?`:    &generations,
		`SELECT count(*) FROM matching_run WHERE id=?`:                            &runs,
		`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?`: &dependencies,
	} {
		var err error
		if query == `SELECT high_water FROM incoming_matching_generation WHERE group_id=?` {
			err = db.NewRaw(query, fixture.group.ID).Scan(ctx, dest)
		} else {
			err = db.NewRaw(query, parent.ID).Scan(ctx, dest)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if generations != 1 || runs != 1 || dependencies != 1 {
		t.Fatalf("generation/run/dependency=%d/%d/%d", generations, runs, dependencies)
	}
}

func TestManualReleaseMatchingAdmissionRollsBackFailedEnqueuePostgres(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	if err := persistence.NewIncomingMatchCandidateRepository(db).Replace(ctx, fixture.evaluation(nil)); err != nil {
		t.Fatal(err)
	}
	header, _, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read evaluation: %+v %v", header, err)
	}
	var state persistence.IncomingGroupingState
	if err := db.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &state); err != nil {
		t.Fatal(err)
	}
	var source persistence.ProviderSource
	if err := db.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &source); err != nil {
		t.Fatal(err)
	}
	requestID, selected, childID := uuid.New(), uuid.New(), uuid.New()
	identity, cacheKey := source.ConfigurationIdentity, "release-lookup:"+selected.String()
	child := persistence.MatchingProviderRequest{Operation: &persistence.Operation{ID: childID, Kind: "provider_release_lookup", State: "queued", Stage: "queued", InputSnapshot: json.RawMessage(`{"schema_version":1,"mbid":"` + selected.String() + `"}`), ProviderSourceID: &source.ID, ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint, ProviderConfigurationIdentity: &identity, ProviderCacheKey: &cacheKey}, Args: service.ProviderFetchOperationArgs{OperationID: childID}}
	plan := persistence.ManualMatchingRunPlan{GroupingEpoch: state.Revision, PolicyVersion: "matching-rapidfuzz-v1", Plan: json.RawMessage(`{"plan":1}`), GroupContext: json.RawMessage(`{"group_id":"` + fixture.group.ID.String() + `","revision":"` + fixture.group.Revision + `"}`), SourceContext: json.RawMessage(`{"provider_id":"` + source.ProviderID.String() + `","provider_source_id":"` + source.ID.String() + `","endpoint":"` + source.Endpoint + `","configuration_identity":"` + source.ConfigurationIdentity + `"}`)}
	delegate := openScanEnqueueRiver(t, db)
	failing := &matchingCoordinatorFailingInserter{delegate: delegate, failAt: 2, err: errors.New("provider enqueue failure")}
	_, err = persistence.NewSetupManagerRepository(db).AdmitManualReleaseMatchingRun(ctx, failing, requestID, fixture.group.ID, fixture.group.Revision, &header.EvaluationRevision, selected, child, plan)
	if err == nil {
		t.Fatal("admission succeeded after child enqueue failure")
	}
	for query, args := range map[string][]any{
		`SELECT count(*) FROM manual_matching_admission WHERE request_id=?`:                                                    {requestID},
		`SELECT count(*) FROM operation WHERE id=? OR id IN (SELECT run_id FROM manual_matching_admission WHERE request_id=?)`: {childID, requestID},
		`SELECT count(*) FROM matching_run WHERE plan_admission_key=?`:                                                         {"manual:" + requestID.String()},
		`SELECT count(*) FROM incoming_matching_generation WHERE group_id=?`:                                                   {fixture.group.ID},
	} {
		var count int
		if err := db.NewRaw(query, args...).Scan(ctx, &count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed admission left %d rows for %s", count, query)
		}
	}
}

func TestManualReleaseMatchingAdmissionSupportsNeverEvaluatedGroupPostgres(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	var state persistence.IncomingGroupingState
	if err := db.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &state); err != nil {
		t.Fatal(err)
	}
	var source persistence.ProviderSource
	if err := db.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &source); err != nil {
		t.Fatal(err)
	}
	selected := uuid.New() // Explicit selection is not restricted to an automatic catalog.
	requestID := uuid.New()
	identity, cacheKey := source.ConfigurationIdentity, "release-lookup:"+selected.String()
	childID := uuid.New()
	child := persistence.MatchingProviderRequest{Operation: &persistence.Operation{ID: childID, Kind: "provider_release_lookup", State: "queued", Stage: "queued", InputSnapshot: json.RawMessage(`{"schema_version":1,"mbid":"` + selected.String() + `"}`), ProviderSourceID: &source.ID, ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint, ProviderConfigurationIdentity: &identity, ProviderCacheKey: &cacheKey}, Args: service.ProviderFetchOperationArgs{OperationID: childID}}
	plan := persistence.ManualMatchingRunPlan{GroupingEpoch: state.Revision, PolicyVersion: "matching-rapidfuzz-v1", Plan: json.RawMessage(`{"plan":1}`), GroupContext: json.RawMessage(`{"group_id":"` + fixture.group.ID.String() + `","revision":"` + fixture.group.Revision + `"}`), SourceContext: json.RawMessage(`{"provider_id":"` + source.ProviderID.String() + `","provider_source_id":"` + source.ID.String() + `","endpoint":"` + source.Endpoint + `","configuration_identity":"` + source.ConfigurationIdentity + `"}`)}
	repo := persistence.NewSetupManagerRepository(db)
	riverClient := openScanEnqueueRiver(t, db)
	nonexistentEvaluation := "not-evaluated"
	if _, err := repo.AdmitManualReleaseMatchingRun(ctx, riverClient, uuid.New(), fixture.group.ID, fixture.group.Revision, &nonexistentEvaluation, selected, child, plan); err == nil {
		t.Fatal("admission accepted an evaluation revision for a never-evaluated group")
	}
	parent, err := repo.AdmitManualReleaseMatchingRun(ctx, riverClient, requestID, fixture.group.ID, fixture.group.Revision, nil, selected, child, plan)
	if err != nil {
		t.Fatalf("admit manual lookup without prior evaluation: %v", err)
	}
	snapshot, exists, err := repo.GetManualReleaseMatchingRun(ctx, requestID)
	if err != nil || !exists || snapshot.RunID != parent.ID || snapshot.ExpectedEvaluationRevision != nil {
		t.Fatalf("manual snapshot = %+v exists=%v err=%v", snapshot, exists, err)
	}
}
