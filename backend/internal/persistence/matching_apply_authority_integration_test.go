//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// This worker-only test recreates a persisted pending apply request, changes
// group authority through the real manual-confirmation and unlink APIs, then
// delivers the old job. It intentionally does not exercise publisher admission.
func TestMatchingApplyWorkerDoesNotOverrideManualUnlinkPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	candidates := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, .95), fixture.candidate(fixture.memberB, .95),
	})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := candidates.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist unique matching evaluation: %v", err)
	}
	header, _, err := candidates.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read persisted evaluation: header=%+v err=%v", header, err)
	}

	// Manual confirmation consumes a complete typed proposal. Keep the unique
	// automatic decision in the evaluation; only add the fixture's review data.
	var releaseKey, recordingKey string
	if err := db.NewRaw(`SELECT r.provider_key,rec.provider_key FROM provider_release r
		JOIN provider_release_track tr ON tr.provider_id=r.provider_id AND tr.release_id=r.id
		JOIN provider_recording rec ON rec.provider_id=tr.provider_id AND rec.id=tr.recording_id
		WHERE r.id=? AND tr.id=?`, fixture.releaseID, fixture.trackID).Scan(ctx, &releaseKey, &recordingKey); err != nil {
		t.Fatalf("read provider keys for manual proposal: %v", err)
	}
	proposal, err := json.Marshal(map[string]any{
		"version": 1, "group_id": fixture.group.ID,
		"selected_release": map[string]any{"provider_release_id": fixture.releaseID, "release_id": releaseKey, "title": "Fixture Album", "tracklist_complete": true,
			"positions": []any{map[string]any{"track_id": fixture.trackID, "medium": 0, "position": 0, "recording_id": recordingKey, "displayed_number": "1", "title": "Fixture Track"}}},
		"releases": []any{map[string]any{"provider_release_id": fixture.releaseID, "release_id": releaseKey, "title": "Fixture Album", "tracklist_complete": true,
			"positions": []any{map[string]any{"track_id": fixture.trackID, "medium": 0, "position": 0, "recording_id": recordingKey, "displayed_number": "1", "title": "Fixture Track"}}}},
		"files": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var evidence map[string]json.RawMessage
	if err := json.Unmarshal(evaluation.Groups[0].Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	evidence["review_proposal"] = proposal
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE incoming_match_evaluation SET evaluation_evidence=?::jsonb WHERE group_id=?`, string(evidenceJSON), fixture.group.ID).Exec(ctx); err != nil {
		t.Fatalf("persist review proposal: %v", err)
	}
	assignments := []persistence.MatchingManualAssignment{{FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1, Position: 1}}

	// Persist a pending request and its target captured at automatic authority
	// revision zero. This is the worker boundary under test, not full publisher
	// admission.
	setup := persistence.NewSetupManagerRepository(db)
	parentID := uuid.New()
	finishedAt := time.Now().UTC()
	if err := setup.CreateOperation(ctx, &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "succeeded", Stage: "complete", InputSnapshot: json.RawMessage(`{}`), FinishedAt: &finishedAt}); err != nil {
		t.Fatalf("create original matching operation: %v", err)
	}
	const executionEpoch = int64(1)
	if _, err := db.NewRaw(`INSERT INTO matching_run(
		id, operation_id, plan_admission_key, plan_digest, immutable_plan, grouping_revision,
		grouping_epoch, provider_source_id, provider_configuration_identity, policy_version,
		state, execution_epoch
	) SELECT ?, ?, ?, ?, '{}'::jsonb, revision, ?, ?, 'incoming-match-integration',
		'matching-rapidfuzz-v1', 'succeeded', ? FROM incoming_grouping_state WHERE singleton=true`,
		parentID, parentID, "authority-test-"+parentID.String(), make([]byte, 32), fixture.groupingEpoch, fixture.source, executionEpoch).Exec(ctx); err != nil {
		t.Fatalf("persist original matching run: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO matching_run_target(run_id,group_id,captured_group_revision,target_generation) VALUES (?,?,?,1)`, parentID, fixture.group.ID, fixture.group.Revision).Exec(ctx); err != nil {
		t.Fatalf("persist matching run target: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO incoming_matching_generation(group_id,high_water,current_run_id) VALUES (?,1,?)
		ON CONFLICT (group_id) DO UPDATE SET high_water=1,current_run_id=EXCLUDED.current_run_id`, fixture.group.ID, parentID).Exec(ctx); err != nil {
		t.Fatalf("persist current matching generation: %v", err)
	}
	operationID := uuid.New()
	intent, err := json.Marshal(struct {
		SchemaVersion      int       `json:"schema_version"`
		ParentRunID        uuid.UUID `json:"parent_run_id"`
		GroupID            uuid.UUID `json:"group_id"`
		EvaluationRevision string    `json:"evaluation_revision"`
		ExecutionEpoch     int64     `json:"execution_epoch"`
	}{1, parentID, fixture.group.ID, header.EvaluationRevision, executionEpoch})
	if err != nil {
		t.Fatal(err)
	}
	riverClient := openScanEnqueueRiver(t, db)
	operation := &persistence.Operation{ID: operationID, Kind: persistence.MatchingApplyOperationKind, State: "queued", Stage: "queued", InputSnapshot: intent}
	if err := setup.CreateOperationAndEnqueue(ctx, operation, riverClient, persistence.MatchingApplyOperationArgs{OperationID: operationID}, nil); err != nil {
		t.Fatalf("create durable apply operation and delivery: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO matching_apply_request(group_id,parent_run_id,evaluation_revision,apply_operation_id) VALUES (?,?,?,?)`, fixture.group.ID, parentID, header.EvaluationRevision, operationID).Exec(ctx); err != nil {
		t.Fatalf("persist apply request: %v", err)
	}
	jobID := *operation.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("mark apply delivery running: %v", err)
	}

	preview, err := candidates.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments)
	if err != nil {
		t.Fatalf("preview manual confirmation: %v", err)
	}
	if _, err := candidates.ConfirmMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignments, preview.PreviewDigest, uuid.New()); err != nil {
		t.Fatalf("confirm manual assignment: %v", err)
	}
	var groupRevision string
	if err := db.NewRaw(`SELECT revision FROM incoming_group WHERE id=?`, fixture.group.ID).Scan(ctx, &groupRevision); err != nil {
		t.Fatal(err)
	}
	var sourceID uuid.UUID
	if err := db.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=?`, fixture.workA).Scan(ctx, &sourceID); err != nil {
		t.Fatal(err)
	}
	var authority persistence.IncomingGroupMatchingAuthority
	if err := db.NewRaw(`SELECT group_id,mode,revision FROM incoming_group_matching_authority WHERE group_id=?`, fixture.group.ID).Scan(ctx, &authority); err != nil {
		t.Fatal(err)
	}
	if authority.Mode != "manual" || authority.Revision != 1 {
		t.Fatalf("manual authority after confirmation = %+v", authority)
	}
	if _, err := candidates.UnlinkIncomingGroupMatching(ctx, fixture.group.ID, groupRevision, authority.Revision, []uuid.UUID{sourceID}, uuid.New()); err != nil {
		t.Fatalf("unlink manual association: %v", err)
	}

	beforeCounts := matchingApplyPersistedCounts(t, ctx, db, fixture.provider)
	var linksBefore int
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE media_variant_id=?`, sourceID).Scan(ctx, &linksBefore); err != nil || linksBefore != 0 {
		t.Fatalf("manual unlink left %d source links (err=%v), want none", linksBefore, err)
	}
	worker := jobs.NewMatchingApplyWorker(setup, service.NewOperations(setup))
	job := &river.Job[persistence.MatchingApplyOperationArgs]{JobRow: &rivertype.JobRow{ID: jobID, Attempt: 1}, Args: persistence.MatchingApplyOperationArgs{OperationID: operationID}}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("deliver old matching apply job: %v", err)
	}
	completed, err := setup.GetOperation(ctx, operationID)
	if err != nil || completed.State != "succeeded" || completed.Stage != "review_required" {
		t.Fatalf("old apply operation = %+v, err=%v; want succeeded/review_required", completed, err)
	}
	var receiptOutcome string
	if err := db.NewRaw(`SELECT outcome FROM matching_apply_receipt WHERE apply_operation_id=? AND group_id=?`, operationID, fixture.group.ID).Scan(ctx, &receiptOutcome); err != nil || receiptOutcome != "review_required" {
		t.Fatalf("apply receipt outcome=%q err=%v, want review_required", receiptOutcome, err)
	}
	var linksAfter int
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE media_variant_id=?`, sourceID).Scan(ctx, &linksAfter); err != nil || linksAfter != 0 {
		t.Fatalf("old apply restored %d source links (err=%v), want none", linksAfter, err)
	}
	if afterCounts := matchingApplyPersistedCounts(t, ctx, db, fixture.provider); afterCounts != beforeCounts {
		t.Fatalf("old apply changed materialized counts: before=%+v after=%+v", beforeCounts, afterCounts)
	}

	job.Attempt = 2
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("redeliver completed old apply job: %v", err)
	}
	var replayOutcome string
	if err := db.NewRaw(`SELECT outcome FROM matching_apply_receipt WHERE apply_operation_id=? AND group_id=?`, operationID, fixture.group.ID).Scan(ctx, &replayOutcome); err != nil || replayOutcome != receiptOutcome {
		t.Fatalf("replayed receipt outcome=%q err=%v, want stable %q", replayOutcome, err, receiptOutcome)
	}
	if replayCounts := matchingApplyPersistedCounts(t, ctx, db, fixture.provider); replayCounts != beforeCounts {
		t.Fatalf("redelivered apply changed materialized counts: before=%+v after=%+v", beforeCounts, replayCounts)
	}
}
