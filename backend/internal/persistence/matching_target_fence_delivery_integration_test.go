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
	// Freeze and recheck the layout target before the readiness mutation: a
	// threshold or tag-encoding-only change must each break the captured digest,
	// and restoring the value must restore the exact baseline.
	assertMatchingLayoutTargetEvidenceFences(t, fixture, groupID)
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

func assertMatchingLayoutTargetEvidenceFences(t *testing.T, fixture automaticTargetFenceFixture, groupID uuid.UUID) {
	t.Helper()
	ctx := fixture.ctx
	tx, err := fixture.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	const policy = service.MatchingLayoutPolicyVersion
	baseline, captures, threshold, err := persistence.CaptureMatchingLayoutTargetTx(ctx, tx, groupID, policy)
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
	checkChange := func(label, query string, args ...any) {
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
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT layout_evidence_test`); err != nil {
			t.Fatal(err)
		}
		checkBaseline()
	}
	checkBaseline()
	checkChange("threshold", `INSERT INTO app_setting(setting_name,setting_value) VALUES(?,?)
		ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, appsettings.MatchingPairThresholdKey, "0.80")
	revision, err := json.Marshal(tagencoding.Report{
		Version:      tagencoding.ReportVersion,
		Automatic:    tagencoding.Overlay{Version: tagencoding.OverlayVersion, Entries: []tagencoding.Entry{}},
		Candidates:   []tagencoding.Candidate{},
		UserRevision: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("encode tag encoding revision: %v", err)
	}
	checkChange("tag encoding", `UPDATE media_metadata_result SET tag_encoding=?::jsonb WHERE id=?`, string(revision), *captures[0].CaptureAnalysisID)
}
