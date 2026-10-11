//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

const matchingAcquisitionGuardArtistID = "11111111-1111-4111-8111-111111111111"

func TestMatchingAcquisitionRejectsInvalidInitialBrowseCursorPostgres(t *testing.T) {
	for _, test := range []struct {
		name   string
		offset int
	}{{name: "nonzero initial cursor", offset: 100}, {name: "valid initial cursor", offset: 0}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newMatchingAcquisitionGuardFixture(t)
			fixture.settleInitialArtistAndClaimWake()
			phase, child := fixture.firstBrowse(test.offset)
			if test.offset != 0 {
				assertMatchingAcquisitionGuardRejectsWithoutMutation(t, fixture, phase, []persistence.MatchingProviderRequest{child})
				return
			}
			changed, err := fixture.repository.AdvanceMatchingAcquisitionForDelivery(fixture.ctx, fixture.river, fixture.parentID,
				fixture.parent.Attempt, fixture.parentJobID, fixture.claim, 0, phase, []persistence.MatchingProviderRequest{child})
			if err != nil || !changed {
				t.Fatalf("advance valid initial browse: changed=%v err=%v", changed, err)
			}
		})
	}
}

func TestMatchingAcquisitionRejectsTamperedContinuationPostgres(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*persistence.MatchingAcquisitionPhase)
	}{
		{name: "artist id", tamper: func(phase *persistence.MatchingAcquisitionPhase) {
			phase.Output = json.RawMessage(`{"artist_mbid":"22222222-2222-4222-8222-222222222222","next_offset":100,"complete":false,"catalog_count":200,"covered_releases":100,"release_ids":["release-0","release-1"],"next_request_id":"` + uuid.NewString() + `","next_cache_key":"artist-release-browse:` + matchingAcquisitionGuardArtistID + `:100:100"}`)
		}},
		{name: "next request id", tamper: func(phase *persistence.MatchingAcquisitionPhase) {
			phase.Output = json.RawMessage(`{"artist_mbid":"` + matchingAcquisitionGuardArtistID + `","next_offset":100,"complete":false,"catalog_count":200,"covered_releases":100,"release_ids":["release-0","release-1"],"next_request_id":"22222222-2222-4222-8222-222222222222","next_cache_key":"artist-release-browse:` + matchingAcquisitionGuardArtistID + `:100:100"}`)
		}},
		{name: "input cursor", tamper: func(phase *persistence.MatchingAcquisitionPhase) {
			phase.Input = json.RawMessage(`{"artist_mbid":"` + matchingAcquisitionGuardArtistID + `","offset":1,"limit":100}`)
		}},
		{name: "input limit", tamper: func(phase *persistence.MatchingAcquisitionPhase) {
			phase.Input = json.RawMessage(`{"artist_mbid":"` + matchingAcquisitionGuardArtistID + `","offset":100,"limit":99}`)
		}},
		{name: "accepted payload hash", tamper: func(phase *persistence.MatchingAcquisitionPhase) {
			var provenance []map[string]string
			if err := json.Unmarshal(phase.ResponseProvenance, &provenance); err != nil {
				panic(err)
			}
			provenance[0]["payload_sha256"] = "0000000000000000000000000000000000000000000000000000000000000000"
			phase.ResponseProvenance, _ = json.Marshal(provenance)
		}},
		{name: "valid continuation", tamper: func(*persistence.MatchingAcquisitionPhase) {}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newMatchingAcquisitionGuardFixture(t)
			fixture.settleInitialArtistAndClaimWake()
			fixture.advanceFirstCatalogPage()
			fixture.settleFirstBrowseAndClaimWake()
			phase, child := fixture.nextCatalogPage()
			test.tamper(&phase)
			if test.name != "valid continuation" {
				assertMatchingAcquisitionGuardRejectsWithoutMutation(t, fixture, phase, []persistence.MatchingProviderRequest{child})
			}
			changed, err := fixture.repository.AdvanceMatchingAcquisitionForDelivery(fixture.ctx, fixture.river, fixture.parentID,
				fixture.parent.Attempt, fixture.parentJobID, fixture.claim, 1, phase, []persistence.MatchingProviderRequest{child})
			if test.name == "valid continuation" {
				if err != nil || !changed {
					t.Fatalf("advance valid continuation: changed=%v err=%v", changed, err)
				}
			} else if err == nil || changed {
				t.Fatalf("tampered continuation was accepted: changed=%v err=%v", changed, err)
			}
		})
	}
}

type matchingAcquisitionGuardFixture struct {
	t           *testing.T
	ctx         context.Context
	db          *bun.DB
	repository  *persistence.SetupManagerRepository
	river       persistence.RiverInserter
	cache       *persistence.ProviderCacheRepository
	parentID    uuid.UUID
	parent      *persistence.Operation
	parentJobID int64
	claim       persistence.MatchingDeliveryClaim
	source      persistence.ProviderSource
	identity    string
	artistID    uuid.UUID
	artistKey   string
	browseID    uuid.UUID
	browseKey   string
}

func newMatchingAcquisitionGuardFixture(t *testing.T) *matchingAcquisitionGuardFixture {
	t.Helper()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	incoming := newIncomingMatchFixture(t, ctx, db)
	const identity = "matching-acquisition-guard-config"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity',?) ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity=? WHERE id=?`, identity, incoming.source).Exec(ctx); err != nil {
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
	if err := db.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, incoming.source).Scan(ctx, &source); err != nil {
		t.Fatal(err)
	}
	parentID, artistRequestID := uuid.New(), uuid.New()
	artistKey := "artist-search:0:100:artist"
	parent := &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{"schema_version":1,"plan":{}}`)}
	artist := &persistence.Operation{ID: artistRequestID, Kind: "provider_artist_search", State: "queued", Stage: "fetch",
		InputSnapshot: json.RawMessage(`{"schema_version":1,"query":"artist","offset":0,"limit":100}`), ProviderSourceID: &source.ID,
		ProviderConfigurationIdentity: matchingCoordinatorStringPtr(identity), ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint, ProviderCacheKey: &artistKey}
	repository := persistence.NewSetupManagerRepository(db)
	river := openScanEnqueueRiver(t, db)
	admitted, err := repository.AdmitMatchingRun(ctx, river, persistence.MatchingAdmission{
		AdmissionKey: "matching-acquisition-guard", GroupingEpoch: grouping.Revision,
		Targets: []persistence.MatchingTarget{{GroupID: group.ID, Revision: group.Revision}},
		Plan:    matchingTestPlan(t, "matching acquisition guard", source, identity), AcquisitionIntentVersion: 1, PolicyVersion: service.MatchingPolicyVersion,
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Providers: []persistence.MatchingProviderRequest{{Operation: artist, Args: service.ProviderFetchOperationArgs{OperationID: artistRequestID}}},
	})
	if err != nil || admitted.RiverJobID == nil {
		t.Fatalf("admit acquisition: operation=%+v err=%v", admitted, err)
	}
	captured, captureDigest, exists, err := repository.GetMatchingAcquisitionMetadataCapture(ctx, parentID, "admitted")
	if err != nil || !exists || captureDigest == "" || len(captured) == 0 {
		t.Fatalf("read admitted metadata capture: members=%+v digest=%q exists=%v err=%v", captured, captureDigest, exists, err)
	}
	for _, member := range captured {
		if member.MemberID == uuid.Nil || member.WorkID == uuid.Nil || !json.Valid(member.Tags) {
			t.Fatalf("admitted metadata capture is incomplete: %+v", member)
		}
	}
	if _, _, _, err := repository.GetMatchingAcquisitionMetadataCapture(ctx, parentID, "catalog_ready"); err == nil {
		t.Fatal("metadata capture accepted an unexpected acquisition phase")
	}
	parentJobID := *admitted.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, parentJobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	claim, outcome, err := repository.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, parentJobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim initial parent: outcome=%v err=%v", outcome, err)
	}
	if consumed, err := repository.ConsumePendingMatchingDelivery(ctx, parentID, admitted.Attempt, parentJobID, claim); err != nil || !consumed {
		t.Fatalf("park initial parent: consumed=%v err=%v", consumed, err)
	}
	return &matchingAcquisitionGuardFixture{t: t, ctx: ctx, db: db, repository: repository, river: river, cache: persistence.NewProviderCacheRepository(db), parentID: parentID,
		parent: admitted, parentJobID: parentJobID, claim: claim, source: source, identity: identity, artistID: artistRequestID, artistKey: artistKey}
}

func (fixture *matchingAcquisitionGuardFixture) settleInitialArtistAndClaimWake() {
	fixture.t.Helper()
	ticket, err := fixture.cache.BeginRefresh(fixture.ctx, fixture.source.ID, fixture.artistKey, fixture.identity)
	if err != nil {
		fixture.t.Fatal(err)
	}
	payload := json.RawMessage(`{"artists":[{"id":"` + matchingAcquisitionGuardArtistID + `"}],"count":1,"offset":0}`)
	if err := fixture.cache.ApplySuccessfulResponse(fixture.ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload}); err != nil {
		fixture.t.Fatal(err)
	}
	var childJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, fixture.artistID).Scan(fixture.ctx, &childJobID); err != nil {
		fixture.t.Fatal(err)
	}
	fixture.settleProviderAndClaimParentWake(fixture.artistID, childJobID)
}

func (fixture *matchingAcquisitionGuardFixture) settleProviderAndClaimParentWake(requestID uuid.UUID, childJobID int64) {
	fixture.t.Helper()
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, childJobID).Exec(fixture.ctx); err != nil {
		fixture.t.Fatal(err)
	}
	epoch, claimed, err := fixture.repository.ClaimProviderOperationDelivery(fixture.ctx, requestID, 1, childJobID)
	if err != nil || !claimed {
		fixture.t.Fatalf("claim provider operation: claimed=%v err=%v", claimed, err)
	}
	if changed, err := fixture.repository.TransitionProviderOperationForDeliveryWithMatchingWake(fixture.ctx, requestID, 1, childJobID, epoch, fixture.river, func(operation *persistence.Operation) error {
		operation.State, operation.Stage = "succeeded", "provider_response_saved"
		finished := time.Now().UTC()
		operation.FinishedAt = &finished
		return nil
	}); err != nil || !changed {
		fixture.t.Fatalf("settle provider and wake parent: changed=%v err=%v", changed, err)
	}
	var parentJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? ORDER BY id DESC LIMIT 1`, fixture.parentID).Scan(fixture.ctx, &parentJobID); err != nil {
		fixture.t.Fatal(err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, parentJobID).Exec(fixture.ctx); err != nil {
		fixture.t.Fatal(err)
	}
	claim, outcome, err := fixture.repository.ClaimMatchingDelivery(fixture.ctx, fixture.parentID, fixture.parent.Attempt, parentJobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		fixture.t.Fatalf("claim parent wake: outcome=%v err=%v", outcome, err)
	}
	fixture.parentJobID, fixture.claim = parentJobID, claim
}

func (fixture *matchingAcquisitionGuardFixture) firstBrowse(offset int) (persistence.MatchingAcquisitionPhase, persistence.MatchingProviderRequest) {
	fixture.t.Helper()
	browseID := uuid.New()
	browseKey := fmt.Sprintf("artist-release-browse:%s:%d:100", matchingAcquisitionGuardArtistID, offset)
	input := json.RawMessage(fmt.Sprintf(`{"schema_version":1,"mbid":"%s","offset":%d,"limit":100}`, matchingAcquisitionGuardArtistID, offset))
	child := fixture.browseChild(browseID, browseKey, input)
	accepted, err := fixture.cache.GetCachedResponse(fixture.ctx, fixture.source.ID, fixture.artistKey)
	if err != nil || accepted == nil {
		fixture.t.Fatalf("read initial accepted response: response=%+v err=%v", accepted, err)
	}
	digest := sha256.Sum256(accepted.Payload)
	phase := persistence.MatchingAcquisitionPhase{Revision: 1, Phase: persistence.MatchingAcquisitionArtistCatalogPage,
		Input:              json.RawMessage(fmt.Sprintf(`{"artist_mbid":"%s","offset":%d,"limit":100}`, matchingAcquisitionGuardArtistID, offset)),
		Output:             json.RawMessage(fmt.Sprintf(`{"artist_mbid":"%s","next_offset":%d,"complete":false}`, matchingAcquisitionGuardArtistID, offset)),
		ResponseProvenance: json.RawMessage(fmt.Sprintf(`[{"request_id":"%s","cache_key":"%s","payload_sha256":"%x"}]`, fixture.artistID, fixture.artistKey, digest))}
	return phase, child
}

func (fixture *matchingAcquisitionGuardFixture) advanceFirstCatalogPage() {
	fixture.t.Helper()
	phase, child := fixture.firstBrowse(0)
	changed, err := fixture.repository.AdvanceMatchingAcquisitionForDelivery(fixture.ctx, fixture.river, fixture.parentID, fixture.parent.Attempt, fixture.parentJobID, fixture.claim, 0, phase, []persistence.MatchingProviderRequest{child})
	if err != nil || !changed {
		fixture.t.Fatalf("advance first catalog page: changed=%v err=%v", changed, err)
	}
	fixture.browseID, fixture.browseKey = child.Operation.ID, *child.Operation.ProviderCacheKey
}

func (fixture *matchingAcquisitionGuardFixture) settleFirstBrowseAndClaimWake() {
	fixture.t.Helper()
	ticket, err := fixture.cache.BeginRefresh(fixture.ctx, fixture.source.ID, fixture.browseKey, fixture.identity)
	if err != nil {
		fixture.t.Fatal(err)
	}
	releases := make([]map[string]string, 100)
	releaseIDs := make([]string, 100)
	for index := range releases {
		releaseID := fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1)
		releases[index] = map[string]string{"id": releaseID}
		releaseIDs[index] = releaseID
	}
	payload, err := json.Marshal(map[string]any{"release-offset": 0, "release-count": 200, "releases": releases})
	if err != nil {
		fixture.t.Fatal(err)
	}
	if err := fixture.cache.ApplySuccessfulResponse(fixture.ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload}); err != nil {
		fixture.t.Fatal(err)
	}
	var jobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, fixture.browseID).Scan(fixture.ctx, &jobID); err != nil {
		fixture.t.Fatal(err)
	}
	fixture.settleProviderAndClaimParentWake(fixture.browseID, jobID)
}

func (fixture *matchingAcquisitionGuardFixture) nextCatalogPage() (persistence.MatchingAcquisitionPhase, persistence.MatchingProviderRequest) {
	fixture.t.Helper()
	accepted, err := fixture.cache.GetCachedResponse(fixture.ctx, fixture.source.ID, fixture.browseKey)
	if err != nil || accepted == nil {
		fixture.t.Fatalf("read accepted browse response: response=%+v err=%v", accepted, err)
	}
	digest := sha256.Sum256(accepted.Payload)
	requestID := uuid.New()
	key := fmt.Sprintf("artist-release-browse:%s:100:100", matchingAcquisitionGuardArtistID)
	input := json.RawMessage(fmt.Sprintf(`{"schema_version":1,"mbid":"%s","offset":100,"limit":100}`, matchingAcquisitionGuardArtistID))
	child := fixture.browseChild(requestID, key, input)
	releaseIDs := make([]string, 100)
	for index := range releaseIDs {
		releaseIDs[index] = fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1)
	}
	phase := persistence.MatchingAcquisitionPhase{Revision: 2, Phase: persistence.MatchingAcquisitionArtistCatalogPage,
		Input:              json.RawMessage(fmt.Sprintf(`{"artist_mbid":"%s","offset":0,"limit":100}`, matchingAcquisitionGuardArtistID)),
		Output:             mustMatchingAcquisitionGuardOutput(fixture.t, matchingAcquisitionGuardArtistID, requestID, key, releaseIDs),
		ResponseProvenance: json.RawMessage(fmt.Sprintf(`[{"request_id":"%s","cache_key":"%s","payload_sha256":"%x"}]`, fixture.browseID, fixture.browseKey, digest))}
	return phase, child
}

func mustMatchingAcquisitionGuardOutput(t *testing.T, artistID string, requestID uuid.UUID, key string, releaseIDs []string) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"artist_mbid": artistID, "next_offset": 100, "complete": false, "catalog_count": 200,
		"covered_releases": 100, "release_ids": releaseIDs, "next_request_id": requestID, "next_cache_key": key})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func (fixture *matchingAcquisitionGuardFixture) browseChild(id uuid.UUID, key string, input json.RawMessage) persistence.MatchingProviderRequest {
	operation := &persistence.Operation{ID: id, Kind: "provider_artist_release_browse", State: "queued", Stage: "fetch", InputSnapshot: input,
		ProviderSourceID: &fixture.source.ID, ProviderConfigurationIdentity: matchingCoordinatorStringPtr(fixture.identity),
		ProviderID: &fixture.source.ProviderID, ProviderEndpoint: fixture.source.Endpoint, ProviderCacheKey: &key}
	return persistence.MatchingProviderRequest{Operation: operation, Args: service.ProviderFetchOperationArgs{OperationID: id}}
}

func assertMatchingAcquisitionGuardRejectsWithoutMutation(t *testing.T, fixture *matchingAcquisitionGuardFixture, phase persistence.MatchingAcquisitionPhase, children []persistence.MatchingProviderRequest) {
	t.Helper()
	before := matchingAcquisitionGuardState(t, fixture)
	changed, err := fixture.repository.AdvanceMatchingAcquisitionForDelivery(fixture.ctx, fixture.river, fixture.parentID, fixture.parent.Attempt, fixture.parentJobID, fixture.claim, before.revision, phase, children)
	if err == nil || changed {
		t.Fatalf("invalid acquisition phase accepted: changed=%v err=%v", changed, err)
	}
	after := matchingAcquisitionGuardState(t, fixture)
	if before != after {
		t.Fatalf("rejected acquisition changed durable state: before=%+v after=%+v", before, after)
	}
}

type matchingAcquisitionGuardDurableState struct {
	revision int64
	phases   int
	children int
	delivery int
	parent   string
	run      string
	current  string
}

func matchingAcquisitionGuardState(t *testing.T, fixture *matchingAcquisitionGuardFixture) matchingAcquisitionGuardDurableState {
	t.Helper()
	var state matchingAcquisitionGuardDurableState
	if err := fixture.db.NewRaw(`SELECT acquisition.revision,(SELECT count(*) FROM matching_acquisition_phase WHERE run_id=acquisition.run_id),
		(SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=acquisition.run_id),
		(SELECT count(*) FROM matching_delivery WHERE run_id=acquisition.run_id AND river_job_id=? AND consumed_at IS NOT NULL),
		operation.state,run.state,acquisition.current_phase
		FROM matching_acquisition acquisition JOIN operation ON operation.id=acquisition.run_id JOIN matching_run run ON run.id=acquisition.run_id
		WHERE acquisition.run_id=?`, fixture.parentJobID, fixture.parentID).Scan(fixture.ctx, &state.revision, &state.phases, &state.children, &state.delivery, &state.parent, &state.run, &state.current); err != nil {
		t.Fatal(err)
	}
	return state
}
