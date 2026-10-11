//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestManualReleaseMatchingWorkerPublishesAcceptedLookupPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	databaseURL := testpostgres.URL(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newIncomingMatchFixture(t, ctx, db)
	group := fixture.group
	groupID := group.ID
	selectedReleaseMBID, err := uuid.Parse(fixture.releaseKeys[0])
	if err != nil {
		t.Fatalf("parse fixture release MBID: %v", err)
	}

	repository := persistence.NewSetupManagerRepository(db)
	grouping := persistence.NewIncomingGroupingRepository(db)
	groups := service.NewIncomingGroups(grouping)
	cache := persistence.NewProviderCacheRepository(db)
	registry := settings.New(persistence.NewSettingsRepository(db), nil)
	operations := service.NewOperations(repository)
	providerFetch := service.NewProviderFetchService(registry, nil, cache)
	worker := jobs.NewMatchingEvaluationWorker(repository,
		service.NewMatchingEvaluationService(groups, grouping, cache, persistence.NewIncomingMatchCandidateRepository(db), registry), operations,
	)
	client, listenerPool, err := jobs.PrepareWithWorkers(ctx, databaseURL, db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, worker)
		river.AddWorker(workers, jobs.NewProviderFetchWorker(providerFetch, operations))
	})
	if err != nil {
		t.Fatalf("prepare River workers: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	worker.WithAcquisition(service.NewMatchingAcquisitionService(repository, providerFetch, client))

	var state persistence.IncomingGroupingState
	if err := db.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &state); err != nil {
		t.Fatal(err)
	}
	var source persistence.ProviderSource
	if err := db.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &source); err != nil {
		t.Fatal(err)
	}
	source.ConfigurationIdentity = uuid.NewString()
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity=? WHERE id=?`, source.ConfigurationIdentity, source.ID).Exec(ctx); err != nil {
		t.Fatalf("set valid worker configuration identity: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES
		('musicbrainz_mode','self-hosted'),('musicbrainz_base_url',?),('musicbrainz_config_identity',?)
		ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, source.Endpoint, source.ConfigurationIdentity).Exec(ctx); err != nil {
		t.Fatalf("save fixture MusicBrainz configuration: %v", err)
	}
	requestID, childID := uuid.New(), uuid.New()
	mbid := selectedReleaseMBID.String()
	cacheKey := "release-lookup:" + mbid
	identity := source.ConfigurationIdentity
	child := persistence.MatchingProviderRequest{
		Operation: &persistence.Operation{
			ID: childID, Kind: "provider_release_lookup", State: "queued", Stage: "queued",
			InputSnapshot:    json.RawMessage(`{"schema_version":1,"mbid":"` + mbid + `"}`),
			ProviderSourceID: &source.ID, ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint,
			ProviderConfigurationIdentity: &identity, ProviderCacheKey: &cacheKey,
		},
		Args: service.ProviderFetchOperationArgs{OperationID: childID},
	}
	plan := persistence.ManualMatchingRunPlan{
		GroupingEpoch: state.Revision, PolicyVersion: service.MatchingPolicyVersion,
		Plan:          json.RawMessage(`{"schema_version":1,"mode":"manual_review"}`),
		GroupContext:  json.RawMessage(`{"group_id":"` + groupID.String() + `","revision":"` + group.Revision + `"}`),
		SourceContext: json.RawMessage(`{"provider_id":"` + source.ProviderID.String() + `","provider_source_id":"` + source.ID.String() + `","endpoint":"` + source.Endpoint + `","configuration_identity":"` + identity + `"}`),
	}
	parent, err := repository.AdmitManualReleaseMatchingRun(ctx, client, requestID, groupID, group.Revision, nil, selectedReleaseMBID, child, plan)
	if err != nil {
		t.Fatalf("admit manual lookup against never-evaluated group: %v", err)
	}
	if parent.RiverJobID == nil || child.Operation.RiverJobID == nil {
		t.Fatal("manual admission did not enqueue parent and lookup deliveries")
	}

	// The accepted manual seal binds a successful cache write to the real admitted
	// lookup delivery. Claim the actual operation and start it under the same
	// execution epoch, publish the accepted response under that fence, then settle
	// the dependency with the still-current epoch. A second claim after the cache
	// write would advance the epoch and make the sealed delivery stale.
	providerDelivery, err := repository.GetOperation(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	epoch, claimed, err := repository.ClaimProviderOperationDelivery(ctx, childID, providerDelivery.Attempt, *providerDelivery.RiverJobID)
	if err != nil || !claimed {
		t.Fatalf("claim selected release lookup delivery: claimed=%t err=%v", claimed, err)
	}
	started, err := repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, childID,
		providerDelivery.Attempt, *providerDelivery.RiverJobID, epoch, client, func(operation *persistence.Operation) error {
			operation.State, operation.Stage = "running", "fetching_provider"
			return nil
		})
	if err != nil || !started {
		t.Fatalf("start selected release lookup delivery: started=%t err=%v", started, err)
	}
	lookupPayload := json.RawMessage(`{"release":"` + mbid + `"}`)
	ticket, err := cache.BeginRefresh(ctx, source.ID, cacheKey, identity)
	if err != nil {
		t.Fatalf("begin selected release cache refresh: %v", err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: lookupPayload, Graph: fixture.graph,
		Delivery: &persistence.ProviderDeliveryFence{OperationID: childID, Attempt: providerDelivery.Attempt, RiverJobID: *providerDelivery.RiverJobID, ExecutionEpoch: epoch},
	}); err != nil {
		t.Fatalf("publish selected release response: %v", err)
	}
	// The receipt must be frozen before the dependency settles, recording the hash
	// of the accepted raw payload projection as the cache stored it.
	stored, err := cache.GetCachedResponse(ctx, source.ID, cacheKey)
	if err != nil || stored == nil || len(stored.Payload) == 0 {
		t.Fatalf("read accepted selected release response: %+v err=%v", stored, err)
	}
	projectedDigest := sha256.Sum256(stored.Payload)
	var receiptOperationID uuid.UUID
	var receiptPayloadSHA []byte
	if err := db.NewRaw(`SELECT lookup_operation_id,payload_sha256 FROM manual_matching_lookup_seal WHERE request_id=? AND sealed_at IS NULL`, requestID).Scan(ctx, &receiptOperationID, &receiptPayloadSHA); err != nil {
		t.Fatalf("read frozen manual lookup receipt: %v", err)
	}
	if receiptOperationID != childID || hex.EncodeToString(receiptPayloadSHA) != hex.EncodeToString(projectedDigest[:]) {
		t.Fatalf("manual lookup receipt operation=%s payload=%x; want %s %x", receiptOperationID, receiptPayloadSHA, childID, projectedDigest[:])
	}
	changed, err := repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, childID,
		providerDelivery.Attempt, *providerDelivery.RiverJobID, epoch, client, func(operation *persistence.Operation) error {
			operation.State, operation.Stage = "succeeded", "complete"
			finished := time.Now().UTC()
			operation.FinishedAt = &finished
			return nil
		})
	if err != nil || !changed {
		t.Fatalf("settle selected lookup and wake parent: changed=%t err=%v", changed, err)
	}
	var parentJobID int64
	if err := db.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL ORDER BY river_job_id DESC LIMIT 1`, parent.ID).Scan(ctx, &parentJobID); err != nil {
		t.Fatalf("read matching wake delivery: %v", err)
	}
	setManualMatchingRiverJobRunning(t, ctx, db, parentJobID)
	if err := worker.Work(ctx, &river.Job[service.MatchingEvaluationOperationArgs]{
		JobRow: &rivertype.JobRow{ID: parentJobID, Attempt: 1},
		Args:   service.MatchingEvaluationOperationArgs{OperationID: parent.ID},
	}); err != nil {
		t.Fatalf("consume accepted manual lookup on real worker: %v", err)
	}

	completed, err := repository.GetOperation(ctx, parent.ID)
	if err != nil || completed.State != "succeeded" {
		t.Fatalf("manual matching parent = %+v err=%v, want succeeded", completed, err)
	}
	header, _, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, groupID)
	if err != nil || header == nil {
		t.Fatalf("read published manual evaluation: header=%+v err=%v", header, err)
	}
	var evidence struct {
		ManualAuthority struct {
			Version             int       `json:"version"`
			ExecutionMode       string    `json:"execution_mode"`
			SelectedReleaseMBID uuid.UUID `json:"selected_release_mbid"`
		} `json:"manual_authority"`
		ManualLookup struct {
			RequestID         uuid.UUID `json:"request_id"`
			GroupID           uuid.UUID `json:"group_id"`
			LookupOperationID uuid.UUID `json:"lookup_operation_id"`
			PayloadSHA256     string    `json:"payload_sha256"`
		} `json:"manual_lookup"`
	}
	if err := json.Unmarshal(header.EvaluationEvidence, &evidence); err != nil {
		t.Fatalf("decode published evaluation evidence: %v", err)
	}
	digest := projectedDigest
	if evidence.ManualAuthority.Version != 1 || evidence.ManualAuthority.ExecutionMode != "manual_review" || evidence.ManualAuthority.SelectedReleaseMBID != selectedReleaseMBID ||
		evidence.ManualLookup.RequestID != requestID || evidence.ManualLookup.GroupID != groupID || evidence.ManualLookup.LookupOperationID != childID || evidence.ManualLookup.PayloadSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("published manual evidence = %+v", evidence)
	}
	var automaticRequests, automaticOperations int
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE kind=?`, persistence.MatchingApplyOperationKind).Scan(ctx, &automaticOperations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM matching_apply_request`).Scan(ctx, &automaticRequests); err != nil {
		t.Fatal(err)
	}
	if automaticRequests != 0 || automaticOperations != 0 {
		t.Fatalf("manual review created automatic apply work: requests=%d operations=%d", automaticRequests, automaticOperations)
	}
	manual := service.NewMatchingManualService(persistence.NewIncomingMatchCandidateRepository(db))
	assignment := []service.MatchingManualAssignment{{FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1, Position: 1}}
	preview, err := manual.Preview(ctx, groupID, header.EvaluationRevision, fixture.releaseID, assignment)
	if err != nil {
		t.Fatalf("preview explicit subset against worker-published evaluation: %v", err)
	}
	if len(preview.Members) != 2 || len(preview.OmittedMemberIDs) != 1 || preview.OmittedMemberIDs[0] != fixture.memberB {
		t.Fatalf("saved worker evaluation did not retain omitted member in manual review: %+v", preview)
	}
	confirmed, err := manual.Confirm(ctx, groupID, preview.EvaluationRevision, fixture.releaseID, assignment, preview.PreviewDigest, uuid.New())
	if err != nil || len(confirmed.Members) != 2 || len(confirmed.OmittedMemberIDs) != 1 || confirmed.OmittedMemberIDs[0] != fixture.memberB {
		t.Fatalf("confirm explicit subset against worker-published evaluation: preview=%+v err=%v", confirmed, err)
	}
}

func setManualMatchingRiverJobRunning(t *testing.T, ctx context.Context, db *bun.DB, jobID int64) {
	t.Helper()
	if _, err := db.NewRaw(`UPDATE river_job SET state='running', attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("mark River job %d running: %v", jobID, err)
	}
}
