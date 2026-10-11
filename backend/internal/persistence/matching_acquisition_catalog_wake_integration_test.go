//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type failingMatchingAcquisitionInserter struct{ persistence.RiverInserter }

func (failingMatchingAcquisitionInserter) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("injected catalog wake insert failure")
}

func TestMatchingAcquisitionCatalogCompletionEnqueuesDurableWakeAtomicallyPostgres(t *testing.T) {
	t.Parallel()
	fixture := newMatchingAcquisitionGuardFixture(t)
	fixture.settleInitialArtistAndClaimWake()
	fixture.advanceFirstCatalogPage()
	fixture.settleFirstBrowseAndClaimWake()

	// A valid empty accepted page makes the first browse response the final page,
	// keeping this test focused on the transaction that seals the manifest.
	acceptedPage := json.RawMessage(`{"release-offset":0,"release-count":0,"releases":[]}`)
	if _, err := fixture.db.NewRaw(`UPDATE provider_response_cache SET payload=?::jsonb WHERE provider_source_id=? AND cache_key=?`,
		string(acceptedPage), fixture.source.ID, fixture.browseKey).Exec(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	accepted, err := fixture.cache.GetCachedResponse(fixture.ctx, fixture.source.ID, fixture.browseKey)
	if err != nil || accepted == nil {
		t.Fatalf("read accepted final page: response=%+v err=%v", accepted, err)
	}
	digest := sha256.Sum256(accepted.Payload)
	input := json.RawMessage(fmt.Sprintf(`{"artist_mbid":%q,"offset":0,"limit":100}`, matchingAcquisitionGuardArtistID))
	output := json.RawMessage(fmt.Sprintf(`{"artist_mbid":%q,"next_offset":0,"complete":true,"catalog_count":0,"covered_releases":0,"release_ids":[]}`, matchingAcquisitionGuardArtistID))
	phase := persistence.MatchingAcquisitionPhase{
		Revision: 2, Phase: "catalog_ready", Input: input, Output: output,
		ResponseProvenance: json.RawMessage(fmt.Sprintf(`[{"request_id":%q,"cache_key":%q,"payload_sha256":"%x"}]`, fixture.browseID, fixture.browseKey, digest)),
	}
	oldJobID, oldClaim := fixture.parentJobID, fixture.claim

	changed, err := fixture.repository.AdvanceMatchingAcquisitionForDelivery(fixture.ctx,
		failingMatchingAcquisitionInserter{RiverInserter: fixture.river}, fixture.parentID,
		fixture.parent.Attempt, oldJobID, oldClaim, 1, phase, nil)
	if err == nil || changed {
		t.Fatalf("failed wake insert changed=%v err=%v; want transaction rollback", changed, err)
	}
	assertCatalogWakeRollback(t, fixture)

	changed, err = fixture.repository.AdvanceMatchingAcquisitionForDelivery(fixture.ctx, fixture.river,
		fixture.parentID, fixture.parent.Attempt, oldJobID, oldClaim, 1, phase, nil)
	if err != nil || !changed {
		t.Fatalf("complete catalog and enqueue wake: changed=%v err=%v", changed, err)
	}

	snapshot, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, fixture.parentID)
	if err != nil || !exists || snapshot.Phase != "catalog_ready" || snapshot.Revision != 2 {
		t.Fatalf("catalog snapshot=%+v exists=%t err=%v", snapshot, exists, err)
	}
	operation, err := fixture.repository.GetOperation(fixture.ctx, fixture.parentID)
	if err != nil || operation.State != "running" || operation.Stage != "ranking_pending" {
		t.Fatalf("parent state/stage=%v err=%v; want running/ranking_pending", operation, err)
	}
	var runState string
	if err := fixture.db.NewRaw(`SELECT state FROM matching_run WHERE id=?`, fixture.parentID).Scan(fixture.ctx, &runState); err != nil || runState != "queued" {
		t.Fatalf("run state=%q err=%v; want queued", runState, err)
	}
	var wakeID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL`, fixture.parentID).Scan(fixture.ctx, &wakeID); err != nil {
		t.Fatalf("read registered wake: %v", err)
	}
	var rawArgs []byte
	if err := fixture.db.NewRaw(`SELECT args FROM river_job WHERE id=?`, wakeID).Scan(fixture.ctx, &rawArgs); err != nil {
		t.Fatalf("read wake args: %v", err)
	}
	var wakeArgs struct {
		OperationID uuid.UUID `json:"operation_id"`
	}
	if err := json.Unmarshal(rawArgs, &wakeArgs); err != nil || wakeArgs.OperationID != fixture.parentID {
		t.Fatalf("wake args=%+v err=%v; want original run %s", wakeArgs, err, fixture.parentID)
	}
	var wakeAttempt int
	if err := fixture.db.NewRaw(`SELECT operation_attempt FROM matching_delivery WHERE run_id=? AND river_job_id=?`, fixture.parentID, wakeID).Scan(fixture.ctx, &wakeAttempt); err != nil || wakeAttempt != fixture.parent.Attempt {
		t.Fatalf("wake operation attempt=%d err=%v; want unchanged attempt %d", wakeAttempt, err, fixture.parent.Attempt)
	}
	var consumedOld bool
	if err := fixture.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, fixture.parentID, oldJobID).Scan(fixture.ctx, &consumedOld); err != nil || !consumedOld {
		t.Fatalf("original delivery consumed=%t err=%v; want consumed", consumedOld, err)
	}

	// The durable registration is sufficient for a fresh repository instance to
	// claim the exact inserted wake after process-local state has been discarded.
	repository := persistence.NewSetupManagerRepository(fixture.db)
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, wakeID).Exec(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	claim, outcome, err := repository.ClaimMatchingDelivery(fixture.ctx, fixture.parentID, fixture.parent.Attempt, wakeID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed || claim.ExecutionEpoch <= oldClaim.ExecutionEpoch {
		t.Fatalf("claim catalog wake=%+v outcome=%v err=%v", claim, outcome, err)
	}

	changed, err = repository.AdvanceMatchingAcquisitionForDelivery(fixture.ctx, fixture.river,
		fixture.parentID, fixture.parent.Attempt, oldJobID, oldClaim, 1, phase, nil)
	if err != nil || changed {
		t.Fatalf("duplicate stale catalog completion changed=%v err=%v; want no-op", changed, err)
	}
	var registeredWakes int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL`, fixture.parentID).Scan(fixture.ctx, &registeredWakes); err != nil || registeredWakes != 1 {
		t.Fatalf("pending delivery count=%d err=%v; want exactly one", registeredWakes, err)
	}
}

func assertCatalogWakeRollback(t *testing.T, fixture *matchingAcquisitionGuardFixture) {
	t.Helper()
	snapshot, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, fixture.parentID)
	if err != nil || !exists || snapshot.Revision != 1 || snapshot.Phase != persistence.MatchingAcquisitionArtistCatalogPage {
		t.Fatalf("failed enqueue changed acquisition snapshot=%+v exists=%t err=%v", snapshot, exists, err)
	}
	var consumed bool
	if err := fixture.db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, fixture.parentID, fixture.parentJobID).Scan(fixture.ctx, &consumed); err != nil || consumed {
		t.Fatalf("failed enqueue consumed current delivery=%t err=%v", consumed, err)
	}
	var pending int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL`, fixture.parentID).Scan(fixture.ctx, &pending); err != nil || pending != 1 {
		t.Fatalf("failed enqueue pending registrations=%d err=%v; want only current delivery", pending, err)
	}
	var stage string
	if err := fixture.db.NewRaw(`SELECT stage FROM operation WHERE id=?`, fixture.parentID).Scan(fixture.ctx, &stage); err != nil || stage == "ranking_pending" {
		t.Fatalf("failed enqueue changed parent stage to %q err=%v", stage, err)
	}
}
