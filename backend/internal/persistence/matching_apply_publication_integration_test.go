//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestMatchingPublisherAtomicallyAdmitsAutomaticApplyPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		review    bool
		failApply bool
	}{
		{name: "unique decision admits apply"},
		{name: "review decision does not admit apply", review: true},
		{name: "apply admission failure rolls back publication", failApply: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runMatchingApplyPublicationCase(t, test.review, test.failApply)
		})
	}
}

func runMatchingApplyPublicationCase(t *testing.T, review, failApply bool) {
	t.Helper()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	client := openScanEnqueueRiver(t, db)
	repository := persistence.NewSetupManagerRepository(db)
	var inserter persistence.RiverInserter = client
	if failApply {
		inserter = matchingApplyFailingInserter{delegate: client, err: errors.New("injected matching apply admission failure")}
	}
	repository.SetRiverInserter(inserter)

	identity := "incoming-match-integration"
	parentID, childID := uuid.New(), uuid.New()
	cacheKey := "publisher-dependency"
	plan, err := json.Marshal(struct {
		SchemaVersion        int                                  `json:"schema_version"`
		GroupingEpoch        int64                                `json:"grouping_epoch"`
		Targets              []persistence.MatchingTarget         `json:"targets"`
		PolicyVersion        string                               `json:"policy_version"`
		EvaluationQueryScope service.MatchingEvaluationQueryScope `json:"evaluation_query_scope"`
		ProviderRequests     []struct {
			Kind                          string          `json:"kind"`
			Snapshot                      json.RawMessage `json:"snapshot"`
			CacheKey                      string          `json:"cache_key"`
			ExplicitRefresh               bool            `json:"explicit_refresh"`
			ProviderSourceID              uuid.UUID       `json:"provider_source_id"`
			EndpointConfigurationIdentity string          `json:"endpoint_configuration_identity"`
			ProviderID                    uuid.UUID       `json:"provider_id"`
		} `json:"provider_requests"`
	}{
		SchemaVersion: 2, GroupingEpoch: fixture.groupingEpoch,
		Targets:       []persistence.MatchingTarget{{GroupID: fixture.group.ID, Revision: fixture.group.Revision}},
		PolicyVersion: service.MatchingPolicyVersion,
		EvaluationQueryScope: service.MatchingEvaluationQueryScope{
			GroupIDs:          []uuid.UUID{fixture.group.ID},
			ReleaseKeysByFile: map[uuid.UUID][]string{fixture.memberA: fixture.releaseKeys, fixture.memberB: fixture.releaseKeys},
			SearchKeysByFile: map[uuid.UUID]service.MatchingSearchKeys{
				fixture.memberA: {Release: fixture.cacheKey, Recording: "recording-search:a"},
				fixture.memberB: {Release: "release-search:b", Recording: "recording-search:b"},
			},
		},
		ProviderRequests: []struct {
			Kind                          string          `json:"kind"`
			Snapshot                      json.RawMessage `json:"snapshot"`
			CacheKey                      string          `json:"cache_key"`
			ExplicitRefresh               bool            `json:"explicit_refresh"`
			ProviderSourceID              uuid.UUID       `json:"provider_source_id"`
			EndpointConfigurationIdentity string          `json:"endpoint_configuration_identity"`
			ProviderID                    uuid.UUID       `json:"provider_id"`
		}{{Kind: "provider_release_lookup", Snapshot: json.RawMessage(`{"query":"fixture"}`), CacheKey: cacheKey, ProviderSourceID: fixture.source, EndpointConfigurationIdentity: identity, ProviderID: fixture.provider}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent := &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{"plan":{}}`)}
	admission := persistence.MatchingAdmission{
		AdmissionKey: "matching-apply-publisher-" + parentID.String(), GroupingEpoch: fixture.groupingEpoch,
		Targets: []persistence.MatchingTarget{{GroupID: fixture.group.ID, Revision: fixture.group.Revision}},
		Plan:    plan, PolicyVersion: service.MatchingPolicyVersion, Parent: parent,
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Providers: []persistence.MatchingProviderRequest{{Operation: &persistence.Operation{
			ID: childID, Kind: "provider_release_lookup", State: "queued", Stage: "fetch", InputSnapshot: json.RawMessage(`{"query":"fixture"}`),
			ProviderSourceID: &fixture.source, ProviderConfigurationIdentity: &identity,
			ProviderID: &fixture.provider, ProviderEndpoint: "https://musicbrainz.example/ws/2", ProviderCacheKey: &cacheKey,
		}, Args: service.ProviderFetchOperationArgs{OperationID: childID}}},
	}
	admitted, err := repository.AdmitMatchingRun(ctx, client, admission)
	if err != nil || admitted.RiverJobID == nil {
		t.Fatalf("admit matching run: operation=%+v err=%v", admitted, err)
	}
	jobID := *admitted.RiverJobID
	if _, err := db.NewRaw(`UPDATE operation SET state='running', stage='evaluating', started_at=now() WHERE id=?`, parentID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE matching_run SET state='queued' WHERE id=?`, parentID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='running', attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE operation SET state='succeeded', finished_at=now() WHERE id=?`, childID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE matching_provider_dependency SET terminal_outcome='succeeded', settled_at=now() WHERE parent_run_id=?`, parentID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	claim, outcome, err := repository.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, jobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim current matching delivery: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}

	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{fixture.candidate(fixture.memberA, .99), fixture.candidate(fixture.memberB, .99)})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if review {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(evaluation.Groups[0].Evidence, &envelope); err != nil {
			t.Fatal(err)
		}
		var decision map[string]json.RawMessage
		if err := json.Unmarshal(envelope["evaluation_decision"], &decision); err != nil {
			t.Fatal(err)
		}
		decision["outcome"] = json.RawMessage(`"review"`)
		decision["assignments"] = json.RawMessage(`[]`)
		envelope["evaluation_decision"], err = json.Marshal(decision)
		if err != nil {
			t.Fatal(err)
		}
		evaluation.Groups[0].Evidence, err = json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
	}

	beforeHeader, beforeCandidates, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, fixture.group.ID)
	if err != nil || beforeHeader != nil || len(beforeCandidates) != 0 {
		t.Fatalf("initial evaluation result: header=%+v candidates=%d err=%v", beforeHeader, len(beforeCandidates), err)
	}
	changed, publishErr := repository.PublishMatchingEvaluationForDelivery(ctx, parentID, admitted.Attempt, jobID, claim.ExecutionEpoch, evaluation, map[uuid.UUID]*string{fixture.group.ID: nil})
	if failApply {
		if publishErr == nil || changed {
			t.Fatalf("publication with failed apply admission = changed:%v err:%v, want rollback", changed, publishErr)
		}
		assertMatchingApplyPublicationRolledBack(t, ctx, db, repository, fixture, parentID, jobID, childID)
		// Retrying with the real inserter and the same prepared evaluation must commit.
		repository.SetRiverInserter(client)
		changed, err = repository.PublishMatchingEvaluationForDelivery(ctx, parentID, admitted.Attempt, jobID, claim.ExecutionEpoch, evaluation, map[uuid.UUID]*string{fixture.group.ID: nil})
		if err != nil || !changed {
			t.Fatalf("retry publication: changed=%v err=%v", changed, err)
		}
	} else if publishErr != nil || !changed {
		t.Fatalf("publish matching evaluation: changed=%v err=%v", changed, publishErr)
	}

	header, candidates, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, fixture.group.ID)
	if err != nil || header == nil || len(candidates) != 2 {
		t.Fatalf("published result: header=%+v candidates=%d err=%v", header, len(candidates), err)
	}
	parentAfter, err := repository.GetOperation(ctx, parentID)
	if err != nil || parentAfter.State != "succeeded" {
		t.Fatalf("parent after publication: operation=%+v err=%v", parentAfter, err)
	}
	var requests int
	if err := db.NewRaw(`SELECT count(*) FROM matching_apply_request WHERE group_id=? AND evaluation_revision=?`, fixture.group.ID, header.EvaluationRevision).Scan(ctx, &requests); err != nil {
		t.Fatal(err)
	}
	var applyChildren int
	if err := db.NewRaw(`SELECT count(*) FROM operation child JOIN matching_apply_request request ON request.apply_operation_id=child.id JOIN river_job job ON job.id=child.river_job_id WHERE child.kind=? AND request.group_id=? AND request.parent_run_id=? AND request.evaluation_revision=?`, persistence.MatchingApplyOperationKind, fixture.group.ID, parentID, header.EvaluationRevision).Scan(ctx, &applyChildren); err != nil {
		t.Fatal(err)
	}
	want := 1
	if review {
		want = 0
	}
	if requests != want || applyChildren != want {
		t.Fatalf("published decision requests=%d apply children=%d, want %d", requests, applyChildren, want)
	}
	var runState string
	var consumed bool
	if err := db.NewRaw(`SELECT run.state, delivery.consumed_at IS NOT NULL FROM matching_run run JOIN matching_delivery delivery ON delivery.run_id=run.id WHERE run.id=? AND delivery.river_job_id=?`, parentID, jobID).Scan(ctx, &runState, &consumed); err != nil || runState != "succeeded" || !consumed {
		t.Fatalf("published run/delivery state=%q consumed=%v err=%v", runState, consumed, err)
	}
}

func assertMatchingApplyPublicationRolledBack(t *testing.T, ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, fixture *incomingMatchFixture, parentID uuid.UUID, jobID int64, childID uuid.UUID) {
	t.Helper()
	header, candidates, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, fixture.group.ID)
	if err != nil || header != nil || len(candidates) != 0 {
		t.Fatalf("failed publication left evaluation header=%+v candidates=%d err=%v", header, len(candidates), err)
	}
	parent, err := repository.GetOperation(ctx, parentID)
	if err != nil || parent.State != "running" {
		t.Fatalf("failed publication parent=%+v err=%v, want running", parent, err)
	}
	var runState string
	var consumed bool
	if err := db.NewRaw(`SELECT run.state, delivery.consumed_at IS NOT NULL FROM matching_run run JOIN matching_delivery delivery ON delivery.run_id=run.id WHERE run.id=? AND delivery.river_job_id=?`, parentID, jobID).Scan(ctx, &runState, &consumed); err != nil || runState != "running" || consumed {
		t.Fatalf("failed publication delivery state=%q consumed=%v err=%v", runState, consumed, err)
	}
	var requests, applyChildren int
	if err := db.NewRaw(`SELECT count(*) FROM matching_apply_request WHERE group_id=?`, fixture.group.ID).Scan(ctx, &requests); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE kind=? AND id<>?`, persistence.MatchingApplyOperationKind, uuid.Nil).Scan(ctx, &applyChildren); err != nil {
		t.Fatal(err)
	}
	if requests != 0 || applyChildren != 0 {
		t.Fatalf("failed publication left apply requests=%d children=%d", requests, applyChildren)
	}
	var childState, dependencyOutcome string
	if err := db.NewRaw(`SELECT operation.state, dependency.terminal_outcome FROM operation JOIN matching_provider_dependency dependency ON dependency.request_id=operation.id WHERE operation.id=?`, childID).Scan(ctx, &childState, &dependencyOutcome); err != nil || childState != "succeeded" || dependencyOutcome != "succeeded" {
		t.Fatalf("failed publication changed settled child state=%q dependency=%q err=%v", childState, dependencyOutcome, err)
	}
}

type matchingApplyFailingInserter struct {
	delegate persistence.RiverInserter
	err      error
}

func (inserter matchingApplyFailingInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if args.Kind() == persistence.MatchingApplyOperationKind {
		return nil, inserter.err
	}
	return inserter.delegate.InsertTx(ctx, tx, args, options)
}
