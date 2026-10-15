//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

// A provider completion inserts a new, independently registered wake for the
// parent. The parent operation still points at the original delivery until the
// wake claims it; the worker must therefore consult matching_delivery rather
// than reject the wake because its River ID differs from operation.river_job_id.
func TestMatchingWorkerClaimsProviderWakeAfterOriginalDeliveryWaitsPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAnalysisDispatchFixtureWithMetadata(t)
	database := fixture.database
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client, listenerPool, err := PrepareWithWorkers(ctx, fixture.databaseURL, database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, &MatchingEvaluationWorker{})
		river.AddWorker(workers, NewProviderFetchWorker(nil, nil))
	})
	if err != nil {
		t.Fatalf("prepare River inserter with matching job kinds: %v", err)
	}
	t.Cleanup(listenerPool.Close)
	// Incoming groups require the selected metadata capture produced by the real
	// source-analysis delivery.
	analysis := fixture.startWithMetadata(t, ctx)
	awaitRiverCompletion(t, ctx, fixture.events, *analysis.RiverJobID)
	// Only source analysis is registered on the fixture's running client. The
	// matching deliveries below are deliberately claimed and invoked by this
	// test; stop the source consumer before it can fetch those unhandled kinds.
	stopRiverClient(t, fixture.client)
	grouping := persistence.NewIncomingGroupingRepository(database)
	groups := service.NewIncomingGroups(grouping)
	if err := groups.Refresh(ctx); err != nil {
		t.Fatalf("refresh groups from the real analyzed source: %v", err)
	}
	groupSnapshot, err := groups.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groupSnapshot.Groups) != 1 || len(groupSnapshot.Groups[0].Members) != 1 {
		t.Fatalf("real analyzed source groups = %+v, want one ready group with one member", groupSnapshot.Groups)
	}
	group := groupSnapshot.Groups[0]
	groupID, memberID := group.ID, group.Members[0]
	repository := persistence.NewSetupManagerRepository(database)
	registry := settings.New(persistence.NewSettingsRepository(database), nil)
	if err := registry.SetMusicBrainzConfig(ctx, "public", ""); err != nil {
		t.Fatalf("save MusicBrainz configuration: %v", err)
	}
	configuration, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	identity := configuration.Identity
	cache := persistence.NewProviderCacheRepository(database)
	source, err := cache.ResolveMusicBrainzSource(ctx, "https://musicbrainz.org/ws/2", identity)
	if err != nil {
		t.Fatalf("resolve current MusicBrainz source: %v", err)
	}

	var groupingEpoch int64
	if err := database.NewRaw(`SELECT revision FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &groupingEpoch); err != nil {
		t.Fatal(err)
	}

	parentID, providerOperationID := uuid.New(), uuid.New()
	providerMBID := uuid.NewString()
	providerSnapshot, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		MBID          string `json:"mbid"`
	}{1, providerMBID})
	if err != nil {
		t.Fatal(err)
	}
	cacheKey := "release-lookup:" + providerMBID
	requestPlan, err := json.Marshal(struct {
		Kind                          string          `json:"kind"`
		Snapshot                      json.RawMessage `json:"snapshot"`
		CacheKey                      string          `json:"cache_key"`
		ExplicitRefresh               bool            `json:"explicit_refresh"`
		ProviderSourceID              uuid.UUID       `json:"provider_source_id"`
		EndpointConfigurationIdentity string          `json:"endpoint_configuration_identity"`
		ProviderID                    uuid.UUID       `json:"provider_id"`
	}{"provider_release_lookup", providerSnapshot, cacheKey, false, source.ID, identity, source.ProviderID})
	if err != nil {
		t.Fatal(err)
	}
	initialPlan := struct {
		SchemaVersion        int                                  `json:"schema_version"`
		GroupingEpoch        int64                                `json:"grouping_epoch"`
		Targets              []persistence.MatchingTarget         `json:"targets"`
		PolicyVersion        string                               `json:"policy_version"`
		EvaluationQueryScope service.MatchingEvaluationQueryScope `json:"evaluation_query_scope"`
		ProviderRequests     []json.RawMessage                    `json:"provider_requests"`
	}{
		SchemaVersion: 2, GroupingEpoch: groupingEpoch,
		Targets:          []persistence.MatchingTarget{{GroupID: groupID, Revision: group.Revision}},
		PolicyVersion:    service.MatchingPolicyVersion,
		ProviderRequests: []json.RawMessage{requestPlan},
		EvaluationQueryScope: service.MatchingEvaluationQueryScope{
			GroupIDs:          []uuid.UUID{groupID},
			ReleaseKeysByFile: map[uuid.UUID][]string{memberID: {}},
			SearchKeysByFile:  map[uuid.UUID]service.MatchingSearchKeys{memberID: {Release: "release-search:worker", Recording: "recording-search:worker"}},
		},
	}
	plan, err := json.Marshal(initialPlan)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(struct {
		SchemaVersion int             `json:"schema_version"`
		Plan          json.RawMessage `json:"plan"`
	}{1, plan})
	if err != nil {
		t.Fatal(err)
	}
	child := &persistence.Operation{
		ID: providerOperationID, Kind: "provider_release_lookup", State: "queued", Stage: "fetch",
		InputSnapshot: providerSnapshot, ProviderSourceID: &source.ID,
		ProviderConfigurationIdentity: matchingWorkerString(identity), ProviderID: &source.ProviderID,
		ProviderEndpoint: source.Endpoint, ProviderCacheKey: &cacheKey,
	}
	parent := &persistence.Operation{ID: parentID, Kind: (service.MatchingEvaluationOperationArgs{}).Kind(), State: "queued", Stage: "waiting_provider", InputSnapshot: snapshot}
	admission := persistence.MatchingAdmission{
		AdmissionKey: "matching-worker-provider-wake", GroupingEpoch: groupingEpoch,
		Targets: []persistence.MatchingTarget{{GroupID: groupID, Revision: group.Revision}},
		Plan:    plan, PolicyVersion: service.MatchingPolicyVersion, Parent: parent,
		ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
		Providers:  []persistence.MatchingProviderRequest{{Operation: child, Args: service.ProviderFetchOperationArgs{OperationID: providerOperationID}}},
	}
	admitted, err := repository.AdmitMatchingRun(ctx, client, admission)
	if err != nil {
		t.Fatalf("admit matching parent and provider: %v", err)
	}
	if admitted.RiverJobID == nil || child.RiverJobID == nil {
		t.Fatal("admission did not persist parent and provider River deliveries")
	}
	initialJobID := *admitted.RiverJobID
	setMatchingRiverJobRunning(t, ctx, database, initialJobID)

	worker := NewMatchingEvaluationWorker(
		repository,
		service.NewMatchingEvaluationService(
			groups,
			grouping, cache,
			persistence.NewIncomingMatchCandidateRepository(database),
			registry,
		),
		service.NewOperations(repository),
	)
	firstErr := worker.Work(ctx, &river.Job[service.MatchingEvaluationOperationArgs]{
		JobRow: &rivertype.JobRow{ID: initialJobID, Attempt: 1},
		Args:   service.MatchingEvaluationOperationArgs{OperationID: parentID},
	})
	if firstErr != nil {
		t.Fatalf("run original matching delivery: %v", firstErr)
	}
	var initialConsumed bool
	if err := database.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, initialJobID).Scan(ctx, &initialConsumed); err != nil {
		t.Fatal(err)
	}
	if !initialConsumed {
		t.Fatal("original delivery did not enter the durable provider wait")
	}

	providerDelivery, err := repository.GetOperation(ctx, providerOperationID)
	if err != nil {
		t.Fatal(err)
	}
	epoch, claimed, err := repository.ClaimProviderOperationDelivery(ctx, providerOperationID, providerDelivery.Attempt, *providerDelivery.RiverJobID)
	if err != nil || !claimed {
		t.Fatalf("claim provider delivery: claimed=%t err=%v", claimed, err)
	}
	changed, err := repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, providerOperationID,
		providerDelivery.Attempt, *providerDelivery.RiverJobID, epoch, client, func(operation *persistence.Operation) error {
			operation.State, operation.Stage = "succeeded", "complete"
			finished := time.Now().UTC()
			operation.FinishedAt = &finished
			return nil
		})
	if err != nil || !changed {
		t.Fatalf("settle provider and insert matching wake: changed=%t err=%v", changed, err)
	}
	if _, err := cache.GetMusicBrainzMatchingSnapshot(ctx, "https://musicbrainz.org/ws/2", identity, nil, nil); err != nil {
		t.Fatalf("read matching snapshot using the saved MusicBrainz source: %v", err)
	}
	var wakeJobID int64
	if err := database.NewRaw(`SELECT river_job_id FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL ORDER BY river_job_id DESC LIMIT 1`, parentID).Scan(ctx, &wakeJobID); err != nil {
		t.Fatal(err)
	}
	if wakeJobID == initialJobID {
		t.Fatalf("provider wake reused original job ID %d", initialJobID)
	}
	setMatchingRiverJobRunning(t, ctx, database, wakeJobID)

	wakeErr := worker.Work(ctx, &river.Job[service.MatchingEvaluationOperationArgs]{
		JobRow: &rivertype.JobRow{ID: wakeJobID, Attempt: 1},
		Args:   service.MatchingEvaluationOperationArgs{OperationID: parentID},
	})
	if wakeErr != nil {
		t.Fatalf("evaluate matching parent from provider wake: %v", wakeErr)
	}
	operation, err := repository.GetOperation(ctx, parentID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != "succeeded" || operation.RiverJobID == nil || *operation.RiverJobID != wakeJobID {
		t.Fatalf("parent after wake evaluation = state %q River job %v, want succeeded on wake %d", operation.State, operation.RiverJobID, wakeJobID)
	}
	var wakeConsumed bool
	if err := database.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, wakeJobID).Scan(ctx, &wakeConsumed); err != nil {
		t.Fatal(err)
	}
	if !wakeConsumed {
		t.Fatal("successful provider wake was not marked consumed")
	}
	header, candidates, err := persistence.NewIncomingMatchCandidateRepository(database).Get(ctx, groupID)
	if err != nil || header == nil || len(candidates) != 0 {
		t.Fatalf("published matching header=%+v candidates=%d err=%v; want evaluated-empty result", header, len(candidates), err)
	}
}

func setMatchingRiverJobRunning(t *testing.T, ctx context.Context, database *bun.DB, jobID int64) {
	t.Helper()
	if _, err := database.NewRaw(`UPDATE river_job SET state='running', attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("mark River job %d running: %v", jobID, err)
	}
}

func matchingWorkerString(value string) *string { return &value }
