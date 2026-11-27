//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	appsettings "github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/tagencoding"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestMatchingTargetFenceDeliveryRejectsChangedCapturedSourcePostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	original, originalDigest, exists, err := fixture.repository.GetMatchingAcquisitionMetadataCapture(fixture.ctx, fixture.runID, "admitted")
	if err != nil || !exists || len(original) == 0 {
		t.Fatalf("read original metadata capture: exists=%v err=%v", exists, err)
	}
	var groupID uuid.UUID
	if err := fixture.db.NewRaw(`SELECT group_id FROM matching_run_target WHERE run_id=?`, fixture.runID).Scan(fixture.ctx, &groupID); err != nil {
		t.Fatalf("read target group: %v", err)
	}
	admitted, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, fixture.runID)
	if err != nil || !exists {
		t.Fatalf("read artist layout admission: exists=%v err=%v", exists, err)
	}
	// Freeze and recheck the layout target before the readiness mutation: a
	// threshold or tag-encoding-only change must each break the captured digest,
	// and restoring the value must restore the exact baseline.
	assertMatchingLayoutTargetEvidenceFences(t, fixture, groupID, admitted.Intent.LayoutTarget)
	result, err := fixture.db.NewRaw(`UPDATE incoming_group_member SET ready=NOT ready
		WHERE group_id=? AND variant_id=(SELECT variant_id FROM incoming_group_member WHERE group_id=? ORDER BY variant_id LIMIT 1)`, groupID, groupID).Exec(fixture.ctx)
	if err != nil {
		t.Fatalf("change a captured target member: %v", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		t.Fatalf("change captured target member: rows=%d err=%v", changed, err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, fixture.jobID).Exec(fixture.ctx); err != nil {
		t.Fatalf("start stale matching delivery job: %v", err)
	}
	_, outcome, err := fixture.repository.ClaimMatchingDelivery(fixture.ctx, fixture.runID, 1, fixture.jobID, 1)
	if !errors.Is(err, persistence.ErrIncomingMatchConflict) || outcome != persistence.MatchingDeliveryNotClaimed {
		t.Fatalf("claim accepted changed captured target: outcome=%v err=%v", outcome, err)
	}
	// Reads still return the saved values so callers can diagnose the stale run.
	readBack, digest, exists, err := fixture.repository.GetMatchingAcquisitionMetadataCapture(fixture.ctx, fixture.runID, "admitted")
	if err != nil || !exists || digest != originalDigest || !equalJSONForTest(t, readBack, original) {
		t.Fatalf("changed source replaced immutable capture: exists=%v digest=%q err=%v", exists, digest, err)
	}
}

type automaticTargetFenceFixture struct {
	ctx        context.Context
	db         *bun.DB
	repository *persistence.SetupManagerRepository
	runID      uuid.UUID
	jobID      int64
}

func newAutomaticMatchingTargetFenceFixture(t *testing.T) automaticTargetFenceFixture {
	return newAutomaticMatchingTargetFenceFixtureWithDatabase(t, nil)
}

func newAutomaticMatchingTargetFenceFixtureWithDatabase(t *testing.T, database *bun.DB) automaticTargetFenceFixture {
	t.Helper()
	ctx := context.Background()
	db := database
	if db == nil {
		db = testpostgres.OpenMigratedWithRiver(t)
	}
	incoming := newIncomingMatchFixture(t, ctx, db)
	setup := persistence.NewSetupManagerRepository(db)
	inventory := persistence.NewSourceInventoryRepository(db)
	client := openScanEnqueueRiver(t, db)
	var event persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&event).Where("reason = ?", "analysis_success").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read source-analysis reconciliation event: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch real reconciliation event: %v", err)
	}
	eventPtr, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil || eventPtr == nil || eventPtr.RegisteredJobID == nil {
		t.Fatalf("read dispatched reconciliation event: event=%+v err=%v", eventPtr, err)
	}
	event = *eventPtr
	var source struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := db.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, incoming.source).Scan(ctx, &source); err != nil {
		t.Fatalf("read provider source fence: %v", err)
	}
	epoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *event.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim source-analysis reconciliation event: claimed=%v err=%v", claimed, err)
	}
	query := json.RawMessage(`{"schema_version":1,"query":"Fixture Artist","offset":0,"limit":1}`)
	providerID, sourceID := source.ProviderID, incoming.source
	identity, endpoint, key := source.Identity, source.Endpoint, "artist-search:0:1:Fixture Artist"
	parentID, providerOperationID := uuid.New(), uuid.New()
	parent := &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	provider := &persistence.Operation{
		ID: providerOperationID, Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: matchingCoordinatorStringPtr(identity),
		ProviderEndpoint: endpoint, ProviderCacheKey: &key, Attempt: 1,
	}
	admitted, err := setup.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Provider: persistence.MatchingProviderRequest{Operation: provider, Args: service.ProviderFetchOperationArgs{OperationID: providerOperationID}},
		GroupID:  incoming.group.ID, ExpectedGroupRevision: incoming.group.Revision, GroupingEpoch: incoming.groupingEpoch,
		PolicyVersion: service.MatchingPolicyVersion, ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: event.ID, EventRiverJobID: *event.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: epoch,
	})
	if err != nil || admitted.AlreadyAdmitted || admitted.Operation == nil {
		t.Fatalf("admit version-2 automatic run: result=%+v err=%v", admitted, err)
	}
	var jobID int64
	if err := db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, parentID).Scan(ctx, &jobID); err != nil {
		t.Fatalf("read registered matching delivery job: %v", err)
	}
	return automaticTargetFenceFixture{ctx: ctx, db: db, repository: setup, runID: parentID, jobID: jobID}
}

func equalJSONForTest(t *testing.T, left, right any) bool {
	t.Helper()
	leftBytes, err := json.Marshal(left)
	if err != nil {
		t.Fatalf("marshal left JSON value: %v", err)
	}
	rightBytes, err := json.Marshal(right)
	if err != nil {
		t.Fatalf("marshal right JSON value: %v", err)
	}
	return string(leftBytes) == string(rightBytes)
}

func assertMatchingLayoutTargetEvidenceFences(t *testing.T, fixture automaticTargetFenceFixture, groupID uuid.UUID, baseline json.RawMessage) {
	t.Helper()
	ctx := fixture.ctx
	tx, err := fixture.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	const policy = service.MatchingLayoutPolicyVersion
	captures, threshold, err := persistence.DecodeMatchingLayoutTarget(baseline, groupID, policy)
	if err != nil || len(captures) == 0 || captures[0].CaptureAnalysisID == nil || threshold != 0.70 {
		t.Fatalf("capture layout target baseline: captures=%d threshold=%v err=%v", len(captures), threshold, err)
	}
	checkBaseline := func() {
		t.Helper()
		ready, err := persistence.RecheckMatchingLayoutTargetTx(ctx, tx, groupID, policy, baseline)
		if err != nil || len(ready) != len(captures) {
			t.Fatalf("baseline layout recheck: captures=%d err=%v", len(ready), err)
		}
	}
	checkChange := func(label string, inspect func(), query string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, `SAVEPOINT layout_evidence_test`); err != nil {
			t.Fatal(err)
		}
		result, err := tx.NewRaw(query, args...).Exec(ctx)
		if err != nil {
			t.Fatalf("change %s: %v", label, err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			t.Fatalf("change %s: rows=%d err=%v", label, changed, err)
		}
		if _, err := persistence.RecheckMatchingLayoutTargetTx(ctx, tx, groupID, policy, baseline); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
			t.Fatalf("%s-only change was not rejected: %v", label, err)
		}
		if inspect != nil {
			inspect()
		}
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT layout_evidence_test`); err != nil {
			t.Fatal(err)
		}
		checkBaseline()
	}
	checkBaseline()
	checkChange("threshold", nil, `INSERT INTO app_setting(setting_name,setting_value) VALUES(?,?)
		ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, appsettings.MatchingPairThresholdKey, "0.80")
	correctionID := uuid.NewString()
	revision, err := json.Marshal(tagencoding.Report{
		Version:      tagencoding.ReportVersion,
		Automatic:    tagencoding.Overlay{Version: tagencoding.OverlayVersion, Entries: []tagencoding.Entry{}},
		Candidates:   []tagencoding.Candidate{},
		UserRevision: correctionID,
	})
	if err != nil {
		t.Fatalf("encode tag encoding revision: %v", err)
	}
	checkChange("tag encoding", func() {
		frozen, _, _, err := persistence.CaptureMatchingLayoutTargetTx(ctx, tx, groupID, policy)
		if err != nil {
			t.Fatal(err)
		}
		decoded, _, err := persistence.DecodeMatchingLayoutTarget(frozen, groupID, policy)
		if err != nil || len(decoded) != len(captures) {
			t.Fatalf("decode layout evidence: members=%d err=%v", len(decoded), err)
		}
		var saved tagencoding.Report
		if json.Unmarshal(decoded[0].TagEncoding, &saved) != nil || saved.UserRevision != correctionID {
			t.Fatalf("reloaded layout evidence did not carry the saved correction revision: %q", decoded[0].TagEncoding)
		}
	}, `UPDATE media_metadata_result SET tag_encoding=?::jsonb WHERE id=?`, string(revision), *captures[0].CaptureAnalysisID)
}

func matchingCoordinatorStringPtr(value string) *string { return &value }

func TestMatchingEvaluationRetryAdvancesDeliveryWithoutChangingTargetGenerationPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	ctx, db, repository := fixture.ctx, fixture.db, fixture.repository

	var before struct {
		Attempt          int        `bun:"attempt"`
		ExecutionEpoch   int64      `bun:"execution_epoch"`
		CurrentJobID     int64      `bun:"current_river_job_id"`
		TargetGeneration int64      `bun:"target_generation"`
		HighWater        int64      `bun:"high_water"`
		CurrentRunID     *uuid.UUID `bun:"current_run_id"`
	}
	if err := db.NewRaw(`SELECT operation.attempt, run.execution_epoch, run.current_river_job_id,
			target.target_generation, generation.high_water, generation.current_run_id
		FROM operation
		JOIN matching_run run ON run.id=operation.id
		JOIN matching_run_target target ON target.run_id=run.id
		JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
		WHERE operation.id=?`, fixture.runID).Scan(ctx, &before); err != nil {
		t.Fatalf("read retry baseline: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE operation SET state='failed', stage='evaluation_failed', safe_error='retry fixture', finished_at=now(), updated_at=now() WHERE id=?`, fixture.runID).Exec(ctx); err != nil {
		t.Fatalf("mark matching operation failed: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE matching_run SET state='failed', updated_at=now() WHERE id=?`, fixture.runID).Exec(ctx); err != nil {
		t.Fatalf("mark matching run failed: %v", err)
	}

	client := openScanEnqueueRiver(t, db)
	retried, err := repository.RetryMatchingEvaluationOperationAndEnqueue(ctx, fixture.runID, client,
		persistence.MatchingEvaluationWakeArgs{OperationID: fixture.runID}, nil)
	if err != nil {
		t.Fatalf("retry failed matching evaluation: %v", err)
	}
	if retried == nil || retried.State != "queued" || retried.Stage != "retry:evaluation_failed" ||
		retried.Attempt != before.Attempt+1 || retried.RiverJobID == nil || *retried.RiverJobID == before.CurrentJobID {
		t.Fatalf("retried operation = %+v; want queued retry with the next attempt and delivery", retried)
	}

	var after struct {
		State            string     `bun:"state"`
		ExecutionEpoch   int64      `bun:"execution_epoch"`
		CurrentJobID     int64      `bun:"current_river_job_id"`
		ActiveJobID      *int64     `bun:"active_river_job_id"`
		ActiveAttempt    *int       `bun:"active_river_attempt"`
		ActiveEpoch      *int64     `bun:"active_execution_epoch"`
		TargetGeneration int64      `bun:"target_generation"`
		HighWater        int64      `bun:"high_water"`
		CurrentRunID     *uuid.UUID `bun:"current_run_id"`
	}
	if err := db.NewRaw(`SELECT run.state, run.execution_epoch, run.current_river_job_id,
			run.active_river_job_id, run.active_river_attempt, run.active_execution_epoch,
			target.target_generation, generation.high_water, generation.current_run_id
		FROM matching_run run
		JOIN matching_run_target target ON target.run_id=run.id
		JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
		WHERE run.id=?`, fixture.runID).Scan(ctx, &after); err != nil {
		t.Fatalf("read retried matching run: %v", err)
	}
	if after.State != "queued" || after.ExecutionEpoch != before.ExecutionEpoch+1 ||
		after.CurrentJobID != *retried.RiverJobID || after.ActiveJobID != nil || after.ActiveAttempt != nil || after.ActiveEpoch != nil {
		t.Fatalf("retried run = %+v; want one queued delivery epoch and no active claim", after)
	}
	if after.TargetGeneration != before.TargetGeneration || after.HighWater != before.HighWater ||
		(after.CurrentRunID == nil) != (before.CurrentRunID == nil) ||
		after.CurrentRunID != nil && before.CurrentRunID != nil && *after.CurrentRunID != *before.CurrentRunID {
		t.Fatalf("retry changed captured target generation: before=%+v after=%+v", before, after)
	}
	var deliveryAttempt int
	if err := db.NewRaw(`SELECT operation_attempt FROM matching_delivery WHERE run_id=? AND river_job_id=?`,
		fixture.runID, *retried.RiverJobID).Scan(ctx, &deliveryAttempt); err != nil {
		t.Fatalf("read registered retry delivery: %v", err)
	}
	if deliveryAttempt != retried.Attempt {
		t.Fatalf("retry delivery attempt = %d, want operation attempt %d", deliveryAttempt, retried.Attempt)
	}
}
