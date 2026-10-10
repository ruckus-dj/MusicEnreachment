//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestIncomingMatchCandidatesPersistAndFenceWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	assertIncomingMatchResult(t, ctx, repository, fixture.group.ID, false, nil)
	if err := repository.Replace(ctx, fixture.evaluation(nil)); err != nil {
		t.Fatalf("publish evaluated-empty result: %v", err)
	}
	header, candidates := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	var providerFences persistence.IncomingMatchProviderFence
	if err := json.Unmarshal(header.ProviderFences, &providerFences); err != nil {
		t.Fatalf("decode persisted provider fences: %v", err)
	}
	if len(candidates) != 0 || len(providerFences.ReleaseKeys) != 2 || providerFences.ReleaseKeys[0] != fixture.releaseKeys[0] || providerFences.ReleaseKeys[1] != fixture.releaseKeys[1] {
		t.Fatalf("evaluated-empty result did not preserve exact inspected keys: candidates=%#v fences=%#v", candidates, providerFences)
	}
	emptySnapshot, err := persistence.NewProviderCacheRepository(db).GetMusicBrainzMatchingSnapshot(ctx,
		"https://musicbrainz.example/ws/2", "incoming-match-integration", nil, nil)
	if err != nil || len(emptySnapshot.Releases) != 0 || len(emptySnapshot.Responses) != 0 || emptySnapshot.AcceptedProjectionRevision == "" {
		t.Fatalf("empty cache-only search snapshot = %#v, err=%v", emptySnapshot, err)
	}

	first := fixture.candidate(fixture.memberA, 0.12345678901234567)
	second := fixture.candidate(fixture.memberB, 0.91)
	position := persistence.IncomingMatchPosition{TrackID: fixture.trackID, Medium: 0, Position: 0}
	first.Positions = []persistence.IncomingMatchPosition{position}
	second.Positions = []persistence.IncomingMatchPosition{position}
	if err := repository.Replace(ctx, fixture.evaluation([]persistence.IncomingMatchCandidate{first, second})); err != nil {
		t.Fatalf("persist candidates: %v", err)
	}
	_, got := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	scoresByMember := make(map[uuid.UUID]float64, len(got))
	for _, candidate := range got {
		scoresByMember[candidate.FileMemberID] = candidate.Score
	}
	if len(got) != 2 || scoresByMember[first.FileMemberID] != first.Score || scoresByMember[second.FileMemberID] != second.Score {
		t.Fatalf("persisted scores/candidates = %#v, want exact full-precision scores", got)
	}
	if got[0].FileMemberID == got[1].FileMemberID || len(got[0].Positions) != 1 || len(got[1].Positions) != 1 || got[0].Positions[0] != position || got[1].Positions[0] != position {
		t.Fatalf("same provider position was not retained for distinct file members: %#v", got)
	}

	// A failed insert after the old rows have been deleted must roll back the
	// previous accepted result. The candidate is otherwise a valid provider hit.
	bad := fixture.candidate(fixture.memberA, .5)
	bad.Positions = []persistence.IncomingMatchPosition{{TrackID: uuid.New(), Medium: 0, Position: 0}}
	if err := repository.Replace(ctx, fixture.evaluation([]persistence.IncomingMatchCandidate{bad})); err == nil {
		t.Fatal("candidate with a track outside the accepted release unexpectedly replaced prior result")
	}
	_, after := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	afterScores := make(map[uuid.UUID]float64, len(after))
	for _, candidate := range after {
		afterScores[candidate.FileMemberID] = candidate.Score
	}
	if len(after) != 2 || afterScores[first.FileMemberID] != first.Score || afterScores[second.FileMemberID] != second.Score {
		t.Fatalf("failed replacement partially wiped previous data: %#v", after)
	}

	// A successful refresh of the accepted query changes the source projection
	// revision. Existing candidates remain readable, but the old query result can
	// no longer publish over them.
	cache := persistence.NewProviderCacheRepository(db)
	ticket, err := cache.BeginRefresh(ctx, fixture.source, fixture.cacheKey, "incoming-match-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(),
		Payload: json.RawMessage(`{"releases":[{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","score":99}]}`), Graph: fixture.graph,
	}); err != nil {
		t.Fatalf("refresh accepted provider projection: %v", err)
	}
	if err := repository.Replace(ctx, fixture.evaluation([]persistence.IncomingMatchCandidate{first})); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("stale provider projection error = %v, want conflict", err)
	}
	_, after = mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	afterScores = make(map[uuid.UUID]float64, len(after))
	for _, candidate := range after {
		afterScores[candidate.FileMemberID] = candidate.Score
	}
	if len(after) != 2 || afterScores[first.FileMemberID] != first.Score || afterScores[second.FileMemberID] != second.Score {
		t.Fatalf("stale provider projection changed previous result: %#v", after)
	}

	if _, err := db.ExecContext(ctx, `UPDATE incoming_grouping_state SET revision=revision+1 WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	if err := repository.Replace(ctx, fixture.evaluation([]persistence.IncomingMatchCandidate{first})); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("stale epoch error = %v, want conflict", err)
	}
	_, after = mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	if len(after) != 2 {
		t.Fatalf("stale epoch changed previous result: %#v", after)
	}
}

func TestApplyCurrentMatchingEvaluationMaterializesGroupAtomicallyPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, 0.95),
		fixture.candidate(fixture.memberB, 0.95),
	})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := repository.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist unique matching evaluation: %v", err)
	}
	header, _, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read evaluation header: header=%+v err=%v", header, err)
	}
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision); err != nil {
		t.Fatalf("apply current matching evaluation: %v", err)
	}
	var localReleaseCount, localTrackCount, releaseTrackCount, trackSourceCount int
	counts := []struct {
		query  string
		target *int
	}{
		{`SELECT count(*) FROM provider_local_release WHERE provider_id=?`, &localReleaseCount},
		{`SELECT count(*) FROM provider_local_track WHERE provider_id=?`, &localTrackCount},
		{`SELECT count(*) FROM release_track WHERE release_id IN (SELECT local_release_id FROM provider_local_release WHERE provider_id=?)`, &releaseTrackCount},
		{`SELECT count(*) FROM track_source ts JOIN provider_local_track plt ON plt.local_track_id=ts.track_id WHERE plt.provider_id=? AND ts.decision_method='automatic' AND ts.selected=false`, &trackSourceCount},
	}
	for _, count := range counts {
		if err := db.NewRaw(count.query, fixture.provider).Scan(ctx, count.target); err != nil {
			t.Fatalf("count applied rows (%s): %v", count.query, err)
		}
	}
	if localReleaseCount != 1 || localTrackCount != 1 || releaseTrackCount != 1 || trackSourceCount != 2 {
		t.Fatalf("applied rows release=%d tracks=%d release_tracks=%d sources=%d, want 1/1/1/2", localReleaseCount, localTrackCount, releaseTrackCount, trackSourceCount)
	}
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision); err != nil {
		t.Fatalf("repeat apply: %v", err)
	}
	var afterRepeat int
	if err := db.NewRaw(`SELECT count(*) FROM track_source ts JOIN provider_local_track plt ON plt.local_track_id=ts.track_id WHERE plt.provider_id=? AND ts.decision_method='automatic' AND ts.selected=false`, fixture.provider).Scan(ctx, &afterRepeat); err != nil || afterRepeat != trackSourceCount {
		t.Fatalf("repeat application source rows=%d err=%v, want stable count %d", afterRepeat, err, trackSourceCount)
	}
}

func TestApplyCurrentMatchingEvaluationRejectsStaleRevisionAndManualVariantConflictPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, 0.95),
		fixture.candidate(fixture.memberB, 0.95),
	})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := repository.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist unique matching evaluation: %v", err)
	}
	header, _, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read evaluation header: header=%+v err=%v", header, err)
	}
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, "stale-revision"); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("apply stale evaluation revision = %v, want conflict", err)
	}
	var variantID uuid.UUID
	if err := db.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=?`, fixture.workA).Scan(ctx, &variantID); err != nil {
		t.Fatalf("read manually protected source variant: %v", err)
	}
	manualTrackID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, manualTrackID, "Manual target").Exec(ctx); err != nil {
		t.Fatalf("create manual target track: %v", err)
	}
	if err := persistence.NewCollectionRepository(db).UpsertTrackSourceDecision(ctx, persistence.TrackSourceDecision{
		TrackID: manualTrackID, MediaVariantID: variantID, Compatible: true, Method: "manual", Metadata: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("protect source variant with manual association: %v", err)
	}
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision); err == nil {
		t.Fatal("apply succeeded despite a manual source-to-other-track association")
	}
	var automaticMappings int
	if err := db.NewRaw(`SELECT count(*) FROM provider_local_release WHERE provider_id=?`, fixture.provider).Scan(ctx, &automaticMappings); err != nil || automaticMappings != 0 {
		t.Fatalf("failed group left provider release mappings=%d err=%v, want 0", automaticMappings, err)
	}
	var protected bool
	if err := db.NewRaw(`SELECT decision_method='manual' AND selected=false FROM track_source WHERE track_id=? AND media_variant_id=?`, manualTrackID, variantID).Scan(ctx, &protected); err != nil || !protected {
		t.Fatalf("manual source state preserved=%v err=%v, want preserved unselected manual association", protected, err)
	}
}

func matchingApplyEvidence(t *testing.T, fixture *incomingMatchFixture, trackID uuid.UUID) json.RawMessage {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(fixture.evaluationEvidence(), &envelope); err != nil {
		t.Fatal(err)
	}
	decision, err := json.Marshal(struct {
		Version         int       `json:"version"`
		GroupID         uuid.UUID `json:"group_id"`
		PolicyVersion   string    `json:"policy_version"`
		Outcome         string    `json:"outcome"`
		SelectedRelease struct {
			ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
			ProviderReleaseKey string    `json:"provider_release_key"`
		} `json:"selected_release"`
		Assignments []struct {
			FileMemberID    uuid.UUID `json:"file_member_id"`
			ProviderTrackID uuid.UUID `json:"provider_track_id"`
			Medium          int       `json:"medium"`
			Position        int       `json:"position"`
		} `json:"assignments"`
	}{
		Version: 1, GroupID: fixture.group.ID, PolicyVersion: "policy-v1", Outcome: "unique",
		SelectedRelease: struct {
			ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
			ProviderReleaseKey string    `json:"provider_release_key"`
		}{ProviderReleaseID: fixture.releaseID, ProviderReleaseKey: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		Assignments: []struct {
			FileMemberID    uuid.UUID `json:"file_member_id"`
			ProviderTrackID uuid.UUID `json:"provider_track_id"`
			Medium          int       `json:"medium"`
			Position        int       `json:"position"`
		}{
			{FileMemberID: fixture.memberA, ProviderTrackID: trackID, Medium: 1, Position: 1},
			{FileMemberID: fixture.memberB, ProviderTrackID: trackID, Medium: 1, Position: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope["evaluation_decision"] = decision
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestMatchingPublisherCommitsEvaluationAndParentTogetherPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	identity := "incoming-match-integration"
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
		PolicyVersion: "policy-v1",
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
		}{{Kind: "provider_release_lookup", Snapshot: json.RawMessage(`{"query":"fixture"}`), CacheKey: "publisher-dependency", ProviderSourceID: fixture.source, EndpointConfigurationIdentity: identity, ProviderID: fixture.provider}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parentID, childID := uuid.New(), uuid.New()
	childCacheKey := "publisher-dependency"
	parent := &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{"plan":{}}`)}
	admission := persistence.MatchingAdmission{
		AdmissionKey: "publisher-success", GroupingEpoch: fixture.groupingEpoch,
		Targets: []persistence.MatchingTarget{{GroupID: fixture.group.ID, Revision: fixture.group.Revision}},
		Plan:    plan, PolicyVersion: "policy-v1", Parent: parent,
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Providers: []persistence.MatchingProviderRequest{{Operation: &persistence.Operation{
			ID: childID, Kind: "provider_release_lookup", State: "queued", Stage: "fetch", InputSnapshot: json.RawMessage(`{"query":"fixture"}`),
			ProviderSourceID: &fixture.source, ProviderConfigurationIdentity: &identity,
			ProviderID: &fixture.provider, ProviderEndpoint: "https://musicbrainz.example/ws/2", ProviderCacheKey: &childCacheKey,
		}, Args: service.ProviderFetchOperationArgs{OperationID: childID}}},
	}
	repository := persistence.NewSetupManagerRepository(db)
	river := openScanEnqueueRiver(t, db)
	admitted, err := repository.AdmitMatchingRun(ctx, river, admission)
	if err != nil || admitted.RiverJobID == nil {
		t.Fatalf("admit matching plan: op=%+v err=%v", admitted, err)
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
	assertClaimRejected := func(label string, operationAttempt, riverAttempt int) {
		t.Helper()
		if _, outcome, err := repository.ClaimMatchingDelivery(ctx, parentID, operationAttempt, jobID, riverAttempt); err != nil || outcome != persistence.MatchingDeliveryNotClaimed {
			t.Fatalf("%s claim outcome=%v err=%v, want not claimed", label, outcome, err)
		}
		var epoch int64
		var activeJob *int64
		if err := db.NewRaw(`SELECT execution_epoch, active_river_job_id FROM matching_run WHERE id=?`, parentID).Scan(ctx, &epoch, &activeJob); err != nil || epoch != 0 || activeJob != nil {
			t.Fatalf("%s mutated run ownership: epoch=%d active_job=%v err=%v", label, epoch, activeJob, err)
		}
	}
	if _, err := db.NewRaw(`UPDATE app_setting SET setting_value='stale-config' WHERE setting_name='musicbrainz_config_identity'`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertClaimRejected("superseded config identity", admitted.Attempt, 1)
	if _, err := db.NewRaw(`UPDATE app_setting SET setting_value=? WHERE setting_name='musicbrainz_config_identity'`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity='stale-source' WHERE id=?`, fixture.source).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertClaimRejected("superseded provider source", admitted.Attempt, 1)
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity=? WHERE id=?`, identity, fixture.source).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE incoming_group SET revision=revision || '-stale' WHERE id=?`, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertClaimRejected("superseded group revision", admitted.Attempt, 1)
	if _, err := db.NewRaw(`UPDATE incoming_group SET revision=? WHERE id=?`, fixture.group.Revision, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE incoming_matching_generation SET high_water=high_water+1 WHERE group_id=?`, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertClaimRejected("superseded group generation", admitted.Attempt, 1)
	if _, err := db.NewRaw(`UPDATE incoming_matching_generation SET high_water=high_water-1 WHERE group_id=?`, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE operation SET attempt=attempt+1 WHERE id=?`, parentID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertClaimRejected("previous parent attempt registration", admitted.Attempt+1, 1)
	if _, err := db.NewRaw(`UPDATE operation SET attempt=attempt-1 WHERE id=?`, parentID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	claim, outcome, err := repository.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, jobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim matching delivery: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	if _, outcome, err := repository.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, jobID, 1); err != nil || outcome != persistence.MatchingDeliveryBusy {
		t.Fatalf("duplicate active claim outcome=%v err=%v, want busy", outcome, err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='retryable' WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, outcome, err := repository.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, jobID, 1); err != nil || outcome != persistence.MatchingDeliveryNotClaimed {
		t.Fatalf("retryable same River attempt outcome=%v err=%v, want not claimed", outcome, err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='running', attempt=2 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, outcome, err := repository.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, jobID, 1); err != nil || outcome != persistence.MatchingDeliveryNotClaimed {
		t.Fatalf("delayed caller for old River attempt outcome=%v err=%v, want not claimed", outcome, err)
	}
	recovered, outcome, err := repository.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, jobID, 2)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed || recovered.ExecutionEpoch != claim.ExecutionEpoch+1 {
		t.Fatalf("recover abandoned River attempt: claim=%+v outcome=%v err=%v", recovered, outcome, err)
	}
	// A delivery with an obsolete execution epoch must not publish, even while
	// its parent and registered delivery are otherwise current.
	if changed, err := repository.PublishMatchingEvaluationForDelivery(ctx, parentID, admitted.Attempt, jobID, recovered.ExecutionEpoch-1, fixture.evaluation(nil), map[uuid.UUID]*string{fixture.group.ID: nil}); err == nil || changed {
		t.Fatalf("stale execution epoch publication = changed:%v err:%v, want rejection", changed, err)
	}
	assertMatchingPublicationStillRunning(t, ctx, db, repository, parentID, jobID, fixture.group.ID)
	// A current delivery also cannot complete while an admitted child remains
	// unsettled.
	if changed, err := repository.PublishMatchingEvaluationForDelivery(ctx, parentID, admitted.Attempt, jobID, recovered.ExecutionEpoch, fixture.evaluation(nil), map[uuid.UUID]*string{fixture.group.ID: nil}); err == nil || changed {
		t.Fatalf("publication with pending provider dependency = changed:%v err:%v, want rejection", changed, err)
	}
	assertMatchingPublicationStillRunning(t, ctx, db, repository, parentID, jobID, fixture.group.ID)
	const pendingBarrierLock int64 = 77913421
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION hold_matching_pending_consume() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(77913421); RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER hold_matching_pending_consume BEFORE UPDATE OF consumed_at ON matching_delivery FOR EACH ROW EXECUTE FUNCTION hold_matching_pending_consume()`); err != nil {
		t.Fatal(err)
	}
	barrierConnection, err := db.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := barrierConnection.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pendingBarrierLock); err != nil {
		t.Fatal(err)
	}
	type pendingResult struct {
		consumed bool
		err      error
	}
	pendingDone := make(chan pendingResult, 1)
	go func() {
		consumed, err := repository.ConsumePendingMatchingDelivery(ctx, parentID, admitted.Attempt, jobID, recovered)
		pendingDone <- pendingResult{consumed: consumed, err: err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE state='active' AND wait_event_type='Lock' AND query LIKE '%UPDATE matching_delivery%')`).Scan(ctx, &blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending consumer did not reach PostgreSQL barrier")
		}
		time.Sleep(time.Millisecond)
	}
	var wakeJobID int64
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`UPDATE matching_provider_dependency SET terminal_outcome='succeeded', settled_at=now() WHERE parent_run_id=?`, parentID).Exec(ctx); err != nil {
			return err
		}
		wake, err := river.InsertTx(ctx, tx.Tx, persistence.MatchingEvaluationWakeArgs{OperationID: parentID}, nil)
		if err != nil {
			return err
		}
		wakeJobID = wake.Job.ID
		_, err = tx.NewRaw(`INSERT INTO matching_delivery(run_id, river_job_id, operation_attempt) VALUES (?, ?, ?)`, parentID, wakeJobID, admitted.Attempt).Exec(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := barrierConnection.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, pendingBarrierLock); err != nil {
		t.Fatal(err)
	}
	if err := barrierConnection.Close(); err != nil {
		t.Fatal(err)
	}
	result := <-pendingDone
	if result.err != nil || !result.consumed {
		t.Fatalf("consume pending delivery during child settlement: consumed=%v err=%v", result.consumed, result.err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER hold_matching_pending_consume ON matching_delivery`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP FUNCTION hold_matching_pending_consume()`); err != nil {
		t.Fatal(err)
	}
	assertMatchingPublicationWaiting(t, ctx, db, repository, parentID, fixture.group.ID)
	if _, err := db.NewRaw(`UPDATE river_job SET state='running', attempt=1 WHERE id=?`, wakeJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	claim, outcome, err = repository.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, wakeJobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim dependency wake: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	jobID = wakeJobID

	// Force failure only at parent completion, after candidate/header writes have
	// occurred, and prove the transaction rolled back all of those writes.
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_matching_parent_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id = '`+parentID.String()+`'::uuid AND NEW.state = 'succeeded' THEN RAISE EXCEPTION 'forced matching completion failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatalf("create completion failure trigger function: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_matching_parent_completion BEFORE UPDATE ON operation FOR EACH ROW EXECUTE FUNCTION reject_matching_parent_completion()`); err != nil {
		t.Fatalf("create completion failure trigger: %v", err)
	}
	rollbackCandidate := fixture.candidate(fixture.memberA, .75)
	rollbackCandidate.Positions = []persistence.IncomingMatchPosition{{TrackID: fixture.trackID, Medium: 0, Position: 0}}
	if changed, err := repository.PublishMatchingEvaluationForDelivery(ctx, parentID, admitted.Attempt, jobID, claim.ExecutionEpoch, fixture.evaluation([]persistence.IncomingMatchCandidate{rollbackCandidate}), map[uuid.UUID]*string{fixture.group.ID: nil}); err == nil || changed {
		t.Fatalf("forced completion failure publication = changed:%v err:%v, want rejection", changed, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_matching_parent_completion ON operation`); err != nil {
		t.Fatalf("drop completion failure trigger: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP FUNCTION reject_matching_parent_completion()`); err != nil {
		t.Fatalf("drop completion failure trigger function: %v", err)
	}
	assertMatchingPublicationStillRunning(t, ctx, db, repository, parentID, jobID, fixture.group.ID)
	changed, err := repository.PublishMatchingEvaluationForDelivery(ctx, parentID, admitted.Attempt, jobID, claim.ExecutionEpoch, fixture.evaluation(nil), map[uuid.UUID]*string{fixture.group.ID: nil})
	if err != nil || !changed {
		t.Fatalf("publish matching result: changed=%v err=%v", changed, err)
	}
	stored, candidates, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, fixture.group.ID)
	if err != nil || stored == nil || len(candidates) != 0 {
		t.Fatalf("published evaluated-empty result: header=%+v candidates=%+v err=%v", stored, candidates, err)
	}
	completed, err := repository.GetOperation(ctx, parentID)
	if err != nil || completed.State != "succeeded" {
		t.Fatalf("parent completion = %+v err=%v", completed, err)
	}
	var state string
	var consumed bool
	if err := db.NewRaw(`SELECT run.state, delivery.consumed_at IS NOT NULL FROM matching_run run JOIN matching_delivery delivery ON delivery.run_id=run.id WHERE run.id=? AND delivery.river_job_id=?`, parentID, jobID).Scan(ctx, &state, &consumed); err != nil || state != "succeeded" || !consumed {
		t.Fatalf("run/delivery completion state=%q consumed=%v err=%v", state, consumed, err)
	}
}

func assertMatchingPublicationStillRunning(t *testing.T, ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, parentID uuid.UUID, jobID int64, groupID uuid.UUID) {
	t.Helper()
	parent, err := repository.GetOperation(ctx, parentID)
	if err != nil || parent.State != "running" {
		t.Fatalf("rejected publication parent = %+v err=%v, want running", parent, err)
	}
	stored, candidates, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, groupID)
	if err != nil || stored != nil || len(candidates) != 0 {
		t.Fatalf("rejected publication changed evaluated result: header=%+v candidates=%+v err=%v", stored, candidates, err)
	}
	var state string
	var consumed bool
	if err := db.NewRaw(`SELECT run.state, delivery.consumed_at IS NOT NULL FROM matching_run run JOIN matching_delivery delivery ON delivery.run_id=run.id WHERE run.id=? AND delivery.river_job_id=?`, parentID, jobID).Scan(ctx, &state, &consumed); err != nil || state != "running" || consumed {
		t.Fatalf("rejected publication run/delivery state=%q consumed=%v err=%v, want running/unconsumed", state, consumed, err)
	}
}

func assertMatchingPublicationWaiting(t *testing.T, ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, parentID, groupID uuid.UUID) {
	t.Helper()
	parent, err := repository.GetOperation(ctx, parentID)
	if err != nil || parent.State != "running" || parent.Stage != "waiting_provider" {
		t.Fatalf("waiting parent = %+v err=%v", parent, err)
	}
	var state string
	if err := db.NewRaw(`SELECT state FROM matching_run WHERE id=?`, parentID).Scan(ctx, &state); err != nil || state != "waiting_provider" {
		t.Fatalf("waiting run state=%q err=%v", state, err)
	}
	stored, candidates, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, groupID)
	if err != nil || stored != nil || len(candidates) != 0 {
		t.Fatalf("waiting publication changed candidates: header=%+v candidates=%+v err=%v", stored, candidates, err)
	}
}

func assertIncomingMatchResult(t *testing.T, ctx context.Context, repository *persistence.IncomingMatchCandidateRepository, groupID uuid.UUID, evaluated bool, want []persistence.IncomingMatchCandidate) {
	t.Helper()
	header, candidates, err := repository.Get(ctx, groupID)
	if err != nil {
		t.Fatalf("read incoming match result: %v", err)
	}
	if (header != nil) != evaluated || len(candidates) != len(want) {
		t.Fatalf("result header=%#v candidates=%#v, evaluated=%v want=%#v", header, candidates, evaluated, want)
	}
}

func mustIncomingMatchResult(t *testing.T, ctx context.Context, repository *persistence.IncomingMatchCandidateRepository, groupID uuid.UUID) (*persistence.IncomingMatchEvaluationHeader, []persistence.IncomingMatchCandidate) {
	t.Helper()
	header, candidates, err := repository.Get(ctx, groupID)
	if err != nil || header == nil {
		t.Fatalf("read evaluated result: header=%#v err=%v", header, err)
	}
	return header, candidates
}

type incomingMatchFixture struct {
	group                 persistence.IncomingGroupingGroup
	memberA, memberB      uuid.UUID
	workA, workB          uuid.UUID
	provider, source      uuid.UUID
	releaseID, trackID    uuid.UUID
	sourceSHA256A         []byte
	sourceSHA256B         []byte
	groupingEpoch         int64
	groupsRevision        string
	acceptedProjectionRev string
	cacheKey              string
	cacheRevision         *string
	releaseKeys           []string
	graph                 persistence.ProviderGraph
}

func newIncomingMatchFixture(t *testing.T, ctx context.Context, db *bun.DB) *incomingMatchFixture {
	t.Helper()
	inventory := persistence.NewSourceInventoryRepository(db)
	root := createInventoryRoot(t, ctx, inventory, "/srv/incoming-match-candidates")
	root.ProcessingMode = service.SourceProcessingModeStaged
	if _, err := db.NewUpdate().Model((*persistence.SourceRoot)(nil)).Set("processing_mode = ?", root.ProcessingMode).Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("set staged fixture processing mode: %v", err)
	}
	establishInventory(t, ctx, db, root)
	var works []*persistence.SourceAnalysisWork
	for _, path := range []string{"album/one.flac", "album/two.flac"} {
		location := insertAnalysisLocation(t, ctx, db, root.ID, path, 4096, probeMtime())
		work := normalizedWork(t, ctx, inventory, root, location, false,
			persistence.SourceAnalysisStepInput{Step: persistence.SourceStepMetadata, State: "pending"},
		)
		operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, false, false, nil, true)
		if err := inventory.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, openScanEnqueueRiver(t, db), service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
			t.Fatalf("admit metadata-only source analysis: %v", err)
		}
		delivery := persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}
		started, _, mode, err := inventory.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID, delivery, "darwin", "arm64")
		if err != nil {
			t.Fatalf("start metadata-only source analysis: %v", err)
		}
		if started.State != "running" || mode != service.SourceProcessingModeStaged {
			t.Fatalf("metadata-only source analysis delivery = state %q mode %q", started.State, mode)
		}
		attempt, err := inventory.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
			WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
			JobID: *operation.RiverJobID, Step: persistence.SourceStepMetadata,
		})
		if err != nil {
			t.Fatalf("claim metadata analysis step: %v", err)
		}
		fence := persistence.SourceAnalysisArtifactFence{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID}
		artifactRepository := persistence.NewSourceAnalysisArtifactRepository(db)
		artifactID := uuid.New()
		if _, err := artifactRepository.Acquire(ctx, artifactID, fence); err != nil {
			t.Fatalf("acquire staged metadata artifact: %v", err)
		}
		if _, err := artifactRepository.MarkReady(ctx, artifactID, fence, location.SizeBytes); err != nil {
			t.Fatalf("mark staged metadata artifact ready: %v", err)
		}
		if _, err := inventory.ApplySourceMetadata(ctx, persistence.SourceMetadataApply{
			WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
			JobID: *operation.RiverJobID, StepAttempt: attempt,
			ObservedTags: json.RawMessage(`{"ARTIST":["Fixture Artist"],"ALBUM":["Fixture Album"]}`),
			Provenance:   json.RawMessage(`{"source":"integration-test"}`), ObservedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("apply current metadata capture: %v", err)
		}
		if err := inventory.SettleNormalizedSourceAnalysisDelivery(ctx, operation.ID, delivery, "succeeded", "complete", ""); err != nil {
			t.Fatalf("settle completed metadata analysis: %v", err)
		}
		works = append(works, work)
	}
	grouping := persistence.NewIncomingGroupingRepository(db)
	captures, err := grouping.CurrentCaptures(ctx)
	if err != nil {
		t.Fatalf("read actual incoming captures before grouping: %v", err)
	}
	sourceSHA256ByWork := make(map[uuid.UUID][]byte, len(captures))
	for _, capture := range captures {
		sourceSHA256ByWork[capture.WorkID] = append([]byte(nil), capture.SourceSHA256...)
	}
	if len(captures) != len(works) {
		t.Fatalf("current incoming captures = %d, want %d", len(captures), len(works))
	}
	for _, capture := range captures {
		if !capture.Ready {
			t.Fatalf("analysis fixture for work %s is not ready: %+v", capture.WorkID, capture)
		}
	}
	if err := service.NewIncomingGroups(grouping).Refresh(ctx); err != nil {
		t.Fatalf("refresh groups from persisted captures: %v", err)
	}
	state, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read grouping state: %v", err)
	}
	groups, err := grouping.List(ctx)
	if err != nil {
		t.Fatalf("read persisted groups: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Members) != 2 {
		t.Fatalf("real captures grouped as %#v, want one group with both files", groups)
	}
	groupSnapshot, err := service.NewIncomingGroups(grouping).Snapshot(ctx)
	if err != nil {
		t.Fatalf("read service grouping snapshot: %v", err)
	}
	if len(groupSnapshot.Groups) != 1 || len(groupSnapshot.Groups[0].Members) != 2 {
		t.Fatalf("service grouping snapshot = %+v, want one group with both files", groupSnapshot.Groups)
	}
	groupRevision, err := service.IncomingGroupsRevision(groupSnapshot.Groups)
	if err != nil {
		t.Fatalf("calculate accepted groups revision: %v", err)
	}

	const identity = "incoming-match-integration"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES('musicbrainz_config_identity', ?) ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	cache := persistence.NewProviderCacheRepository(db)
	provider := persistence.Provider{ID: uuid.New(), Code: "musicbrainz"}
	if err := cache.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := persistence.ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "incoming-match", Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: identity}
	if err := cache.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const trackKey = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	rawRelease := json.RawMessage(`{"id":"` + releaseKey + `","title":"Fixture Album","artist-credit":[{"name":"Fixture Artist","artist":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"Fixture Artist"}}],"media":[{"position":1,"tracks":[{"id":"` + trackKey + `","number":"1","recording":{"id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","title":"Fixture Track"}}]}]}`)
	graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: rawRelease}})
	if err != nil {
		t.Fatalf("normalize real provider release fixture: %v", err)
	}
	const cacheKey = "release-search:0:1:fixture"
	ticket, err := cache.BeginRefresh(ctx, source.ID, cacheKey, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(),
		Payload: json.RawMessage(`{"releases":[{"id":"` + releaseKey + `","score":100}]}`), Graph: graph,
	}); err != nil {
		t.Fatalf("persist accepted provider response and projection: %v", err)
	}
	const secondReleaseKey = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	secondRaw := json.RawMessage(`{"id":"` + secondReleaseKey + `","title":"Other Fixture Album"}`)
	secondGraph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: secondRaw}})
	if err != nil {
		t.Fatalf("normalize second inspected release fixture: %v", err)
	}
	secondTicket, err := cache.BeginRefresh(ctx, source.ID, "release-lookup:"+secondReleaseKey, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: secondTicket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[]}`), Graph: secondGraph,
	}); err != nil {
		t.Fatalf("persist second inspected release projection: %v", err)
	}
	releaseKeys := []string{releaseKey, secondReleaseKey}
	queryKeys := []string{cacheKey, "recording-search:a", "recording-search:b", "release-search:b"}
	snapshot, err := cache.GetMusicBrainzMatchingSnapshot(ctx, source.Endpoint, identity, releaseKeys, queryKeys)
	if err != nil || len(snapshot.Releases) != 2 || len(snapshot.Releases[0].Tracks) != 1 {
		t.Fatalf("read accepted provider projection: snapshot=%#v err=%v", snapshot, err)
	}
	// Candidate ReleaseID is the persistence identity, not the provider MBID
	// exposed in MatchingRelease.ProviderKey. Resolve the same ID used by
	// persisted candidates and their release/track relations.
	var releaseID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, provider.ID, snapshot.Releases[0].ProviderKey).Scan(ctx, &releaseID); err != nil {
		t.Fatalf("resolve persisted release ID for projected key %q: %v", snapshot.Releases[0].ProviderKey, err)
	}
	trackID := snapshot.Releases[0].Tracks[0].ID
	cacheResponse, err := cache.GetCachedResponse(ctx, source.ID, cacheKey)
	if err != nil || cacheResponse == nil {
		t.Fatalf("read accepted cache revision: %v", err)
	}
	cacheRevision := cacheResponse.Revision
	return &incomingMatchFixture{
		group: persistence.IncomingGroupingGroup{
			ID: groupSnapshot.Groups[0].ID, Revision: groupSnapshot.Groups[0].Revision,
			Members: append([]uuid.UUID(nil), groupSnapshot.Groups[0].Members...),
		},
		memberA: works[0].ID, memberB: works[1].ID, workA: works[0].ID, workB: works[1].ID,
		provider: provider.ID, source: source.ID, releaseID: releaseID, trackID: trackID, releaseKeys: releaseKeys,
		sourceSHA256A: sourceSHA256ByWork[works[0].ID], sourceSHA256B: sourceSHA256ByWork[works[1].ID],
		groupingEpoch: state.Revision, groupsRevision: groupRevision,
		acceptedProjectionRev: snapshot.AcceptedProjectionRevision, cacheKey: cacheKey, cacheRevision: cacheRevision, graph: graph,
	}
}

func (f *incomingMatchFixture) evaluation(candidates []persistence.IncomingMatchCandidate) persistence.IncomingMatchEvaluation {
	return persistence.IncomingMatchEvaluation{
		ExpectedGroupsRevision: f.groupsRevision, ExpectedGroupingEpoch: f.groupingEpoch, PolicyVersion: "policy-v1",
		Provider: persistence.IncomingMatchProviderFence{
			ProviderID: f.provider, ProviderSourceID: f.source, Namespace: "incoming-match",
			Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: "incoming-match-integration",
			CacheKey: f.cacheKey, CacheRevision: f.cacheRevision, AcceptedProjectionRevision: f.acceptedProjectionRev,
			CacheRevisions: map[string]*string{
				f.cacheKey:           f.cacheRevision,
				"release-search:b":   nil,
				"recording-search:a": nil, "recording-search:b": nil,
			},
			ReleaseKeys: append([]string(nil), f.releaseKeys...),
		},
		Groups: []persistence.IncomingMatchGroupEvaluation{{GroupID: f.group.ID, GroupRevision: f.group.Revision,
			Members: []persistence.IncomingMatchMemberFence{
				{FileMemberID: f.memberA, WorkID: f.workA, SourceSHA256: append([]byte(nil), f.sourceSHA256A...)},
				{FileMemberID: f.memberB, WorkID: f.workB, SourceSHA256: append([]byte(nil), f.sourceSHA256B...)},
			},
			Candidates: candidates,
			Evidence:   f.evaluationEvidence(),
		}},
	}
}

func (f *incomingMatchFixture) evaluationEvidence() json.RawMessage {
	type queryEvidence struct {
		FileMemberID      uuid.UUID `json:"file_member_id"`
		ReleaseQueryKey   string    `json:"release_query_key"`
		RecordingQueryKey string    `json:"recording_query_key"`
	}
	evidence, _ := json.Marshal(struct {
		Files []queryEvidence `json:"files"`
	}{Files: []queryEvidence{
		{FileMemberID: f.memberA, ReleaseQueryKey: f.cacheKey, RecordingQueryKey: "recording-search:a"},
		{FileMemberID: f.memberB, ReleaseQueryKey: "release-search:b", RecordingQueryKey: "recording-search:b"},
	}})
	return evidence
}

func (f *incomingMatchFixture) candidate(member uuid.UUID, score float64) persistence.IncomingMatchCandidate {
	return persistence.IncomingMatchCandidate{
		FileMemberID: member, ProviderID: f.provider, ProviderSourceID: f.source,
		ReleaseID: f.releaseID, Score: score, Evidence: json.RawMessage(`{"kind":"test"}`),
	}
}
