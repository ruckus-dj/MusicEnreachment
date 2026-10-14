//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// This provider exposes only a previously accepted cache response. In
// particular, acquisition never makes a provider request while advancing.
type artistReviewCachedProvider struct {
	cache    *persistence.ProviderCacheRepository
	sourceID uuid.UUID
}

func (provider artistReviewCachedProvider) CachedResponse(ctx context.Context, key string) (*persistence.ProviderCachedResponse, error) {
	return provider.cache.GetCachedResponse(ctx, provider.sourceID, key)
}

func (artistReviewCachedProvider) PrepareOperation(context.Context, string, string, int, int, bool) (*persistence.Operation, error) {
	return nil, fmt.Errorf("unexpected provider browse preparation")
}

func TestAutomaticArtistSearchReviewIsPublishedFromAcceptedEmptyReceiptPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewSetupManagerRepository(db)
	inventory := persistence.NewSourceInventoryRepository(db)
	client := openScanEnqueueRiver(t, db)

	var event persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&event).Where("reason = ?", "analysis_success").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read analysis-success reconciliation event: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch reconciliation event: %v", err)
	}
	requests, err := inventory.ReadPendingCurrentRequests(ctx)
	if err != nil {
		t.Fatalf("read observed current matching requests: %v", err)
	}
	var requestGeneration int64
	for _, request := range requests {
		if request.GroupID == fixture.group.ID {
			requestGeneration = request.RequestedGeneration
			break
		}
	}
	if requestGeneration < 1 {
		t.Fatalf("fixture current request generation = %d, want an observed positive generation", requestGeneration)
	}
	dispatched, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("read dispatched reconciliation event: %v", err)
	}
	if dispatched.RegisteredJobID == nil {
		t.Fatal("dispatched event has no registered River job")
	}
	epoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *dispatched.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim reconciliation delivery: epoch=%d claimed=%v err=%v", epoch, claimed, err)
	}
	var source struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := db.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.source).Scan(ctx, &source); err != nil {
		t.Fatalf("read provider source fence: %v", err)
	}
	query := json.RawMessage(`{"schema_version":1,"query":"Fixture Artist","offset":0,"limit":1}`)
	cacheKey := "artist-search:0:1:Fixture Artist"
	parent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	providerOperation := &persistence.Operation{
		ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &fixture.source, ProviderID: &source.ProviderID,
		ProviderConfigurationIdentity: &source.Identity, ProviderEndpoint: source.Endpoint,
		ProviderCacheKey: &cacheKey, Attempt: 1,
	}
	admission, err := repository.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parent.ID},
		Provider: persistence.MatchingProviderRequest{Operation: providerOperation, Args: service.ProviderFetchOperationArgs{OperationID: providerOperation.ID}},
		GroupID:  fixture.group.ID, ExpectedGroupRevision: fixture.group.Revision,
		PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: event.ID, EventRiverJobID: *dispatched.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: epoch,
		RequestGeneration: requestGeneration,
	})
	if err != nil {
		t.Fatalf("admit real automatic artist matching run: %v", err)
	}
	if admission.Operation == nil || admission.Operation.RiverJobID == nil {
		t.Fatal("automatic admission did not return its registered evaluation operation")
	}

	providerJobID := *providerOperation.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, providerJobID).Exec(ctx); err != nil {
		t.Fatalf("prepare provider River delivery: %v", err)
	}
	providerEpoch, providerClaimed, err := repository.ClaimProviderOperationDelivery(ctx, providerOperation.ID, 1, providerJobID)
	if err != nil || !providerClaimed {
		t.Fatalf("claim provider operation delivery: epoch=%d claimed=%v err=%v", providerEpoch, providerClaimed, err)
	}
	if changed, err := repository.TransitionProviderOperationForDelivery(ctx, providerOperation.ID, 1, providerJobID, providerEpoch, func(operation *persistence.Operation) error {
		operation.State, operation.Stage = "running", "fetching"
		return nil
	}); err != nil || !changed {
		t.Fatalf("start provider operation delivery: changed=%v err=%v", changed, err)
	}
	cache := persistence.NewProviderCacheRepository(db)
	ticket, err := cache.BeginRefresh(ctx, fixture.source, cacheKey, source.Identity)
	if err != nil {
		t.Fatalf("begin accepted artist-search cache generation: %v", err)
	}
	acceptedPayload := json.RawMessage(`{"count":0,"offset":0,"artists":[]}`)
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Revision: "artist-search-r1", Payload: acceptedPayload,
		Delivery: &persistence.ProviderDeliveryFence{OperationID: providerOperation.ID, Attempt: 1, RiverJobID: providerJobID, ExecutionEpoch: providerEpoch},
	}); err != nil {
		t.Fatalf("persist accepted empty artist-search response: %v", err)
	}
	if changed, err := repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, providerOperation.ID, 1, providerJobID, providerEpoch, client, func(operation *persistence.Operation) error {
		operation.State, operation.Stage = "succeeded", "complete"
		finished := time.Now().UTC()
		operation.FinishedAt = &finished
		return nil
	}); err != nil || !changed {
		t.Fatalf("settle accepted provider delivery and wake matching: changed=%v err=%v", changed, err)
	}

	jobID := *admission.Operation.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("prepare registered matching River delivery: %v", err)
	}
	claim, outcome, err := repository.ClaimMatchingDelivery(ctx, admission.OriginalRunID, 1, jobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim admitted evaluation delivery: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	acquisition := service.NewMatchingAcquisitionService(repository, artistReviewCachedProvider{cache: cache, sourceID: fixture.source}, client)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_artist_review_member_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected member insert failure'; END $$`); err != nil {
		t.Fatalf("create review rollback trigger function: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_artist_review_member_insert BEFORE INSERT ON incoming_match_evaluation_member
		FOR EACH ROW EXECUTE FUNCTION reject_artist_review_member_insert()`); err != nil {
		t.Fatalf("create review rollback trigger: %v", err)
	}
	if changed, acquired, err := acquisition.Advance(ctx, admission.OriginalRunID, 1, jobID, claim); err == nil || !acquired || changed {
		t.Fatalf("injected member insert did not fail automatic review publication atomically: changed=%v acquired=%v err=%v", changed, acquired, err)
	}
	var rolledBackPhase string
	var rolledBackRevision int64
	if err := db.NewRaw(`SELECT current_phase,revision FROM matching_acquisition WHERE run_id=?`, admission.OriginalRunID).Scan(ctx, &rolledBackPhase, &rolledBackRevision); err != nil {
		t.Fatalf("read acquisition cursor after injected rollback: %v", err)
	}
	if rolledBackPhase != "admitted" || rolledBackRevision != 0 {
		t.Fatalf("failed review write advanced acquisition: phase=%s revision=%d", rolledBackPhase, rolledBackRevision)
	}
	var rolledBackHeader int
	if err := db.NewRaw(`SELECT count(*) FROM incoming_match_evaluation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &rolledBackHeader); err != nil || rolledBackHeader != 0 {
		t.Fatalf("failed review write leaked an evaluation header: count=%d err=%v", rolledBackHeader, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_artist_review_member_insert ON incoming_match_evaluation_member`); err != nil {
		t.Fatalf("drop review rollback trigger: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP FUNCTION reject_artist_review_member_insert()`); err != nil {
		t.Fatalf("drop review rollback trigger function: %v", err)
	}
	changed, acquired, err := acquisition.Advance(ctx, admission.OriginalRunID, 1, jobID, claim)
	if err != nil || !acquired || !changed {
		t.Fatalf("advance admitted empty artist search: changed=%v acquired=%v err=%v", changed, acquired, err)
	}

	var header persistence.IncomingMatchEvaluationHeader
	if err := db.NewRaw(`SELECT group_id,evaluation_revision,evaluation_evidence FROM incoming_match_evaluation WHERE group_id=?`, fixture.group.ID).Scan(ctx, &header); err != nil {
		t.Fatalf("read published automatic artist review header: %v", err)
	}
	if header.GroupID != fixture.group.ID || header.EvaluationRevision == "" {
		t.Fatalf("published artist review header = %+v", header)
	}
	var reviewEvidence struct {
		Proposal json.RawMessage `json:"review_proposal"`
	}
	if err := json.Unmarshal(header.EvaluationEvidence, &reviewEvidence); err != nil || len(reviewEvidence.Proposal) == 0 {
		t.Fatalf("published review proposal missing from evidence: %s err=%v", header.EvaluationEvidence, err)
	}
	apiReview, err := service.NewMatchingReviewService(persistence.NewIncomingMatchCandidateRepository(db)).Get(ctx, fixture.group.ID)
	if err != nil || !apiReview.Evaluated || !apiReview.ProposalAvailable {
		t.Fatalf("read persisted review through API projection: evaluated=%v available=%v err=%v", apiReview.Evaluated, apiReview.ProposalAvailable, err)
	}
	var proposal struct {
		Files []struct {
			RawTags map[string][]string `json:"raw_tags"`
		} `json:"files"`
	}
	if err := json.Unmarshal(apiReview.Proposal, &proposal); err != nil || len(proposal.Files) != len(fixture.group.Members) {
		t.Fatalf("API review proposal does not contain captured member files: %s err=%v", apiReview.Proposal, err)
	}
	for _, file := range proposal.Files {
		if len(file.RawTags) != 2 || len(file.RawTags["ARTIST"]) != 1 || file.RawTags["ARTIST"][0] != "Fixture Artist" ||
			len(file.RawTags["ALBUM"]) != 1 || file.RawTags["ALBUM"][0] != "Fixture Album" {
			t.Fatalf("API review proposal omitted full captured raw tags: %+v", file.RawTags)
		}
	}
	// response_provenance is a JSONB column: a top-level json.RawMessage scan
	// makes Bun treat the named []byte as a multi-row byte destination. Scan
	// into a tagged struct field, matching the production JSONB read pattern.
	var provenance struct {
		ResponseProvenance []byte `bun:"response_provenance"`
	}
	if err := db.NewRaw(`SELECT response_provenance FROM matching_acquisition_phase WHERE run_id=? AND phase='waiting_policy'`, admission.OriginalRunID).Scan(ctx, &provenance); err != nil {
		t.Fatalf("read persisted accepted artist-search receipt: %v", err)
	}
	var accepted []struct {
		RequestID uuid.UUID `json:"request_id"`
		CacheKey  string    `json:"cache_key"`
		PayloadSH string    `json:"payload_sha256"`
	}
	if err := json.Unmarshal(provenance.ResponseProvenance, &accepted); err != nil || len(accepted) != 1 || accepted[0].RequestID != providerOperation.ID || accepted[0].CacheKey != cacheKey {
		t.Fatalf("accepted provider receipt was not retained: %s err=%v", provenance.ResponseProvenance, err)
	}
	var acceptedResponse struct {
		Count   int               `json:"count"`
		Offset  int               `json:"offset"`
		Artists []json.RawMessage `json:"artists"`
	}
	if err := json.Unmarshal(acceptedPayload, &acceptedResponse); err != nil || acceptedResponse.Count != 0 || len(acceptedResponse.Artists) != 0 {
		t.Fatalf("accepted raw provider response was not empty: %+v err=%v", acceptedResponse, err)
	}
	var phaseCount, browseCount, releaseCount, fallbackCount, applyCount int
	queries := []struct {
		query string
		dest  *int
	}{
		{`SELECT count(*) FROM matching_acquisition_phase WHERE run_id=? AND phase='waiting_policy'`, &phaseCount},
		{`SELECT count(*) FROM operation WHERE kind IN ('provider_artist_release_browse','provider_release_search')`, &browseCount},
		{`SELECT count(*) FROM incoming_match_candidate WHERE group_id=?`, &releaseCount},
		{`SELECT count(*) FROM operation WHERE kind='matching_fallback_search_v1'`, &fallbackCount},
		{`SELECT count(*) FROM operation WHERE kind='matching_apply_v1'`, &applyCount},
	}
	for _, item := range queries {
		var err error
		if item.dest == &browseCount || item.dest == &fallbackCount || item.dest == &applyCount {
			err = db.NewRaw(item.query).Scan(ctx, item.dest)
		} else if item.dest == &releaseCount {
			err = db.NewRaw(item.query, fixture.group.ID).Scan(ctx, item.dest)
		} else {
			err = db.NewRaw(item.query, admission.OriginalRunID).Scan(ctx, item.dest)
		}
		if err != nil {
			t.Fatalf("verify no downstream acquisition/publication work: %v", err)
		}
	}
	if phaseCount != 1 || browseCount != 0 || releaseCount != 0 || fallbackCount != 0 || applyCount != 0 {
		t.Fatalf("empty artist-search downstream state: phases=%d browses=%d releases=%d fallbacks=%d applies=%d", phaseCount, browseCount, releaseCount, fallbackCount, applyCount)
	}
	var phaseReason string
	if err := db.NewRaw(`SELECT output->>'reason' FROM matching_acquisition_phase WHERE run_id=? AND phase='waiting_policy'`, admission.OriginalRunID).Scan(ctx, &phaseReason); err != nil || phaseReason != "artist_search_no_results" {
		t.Fatalf("empty artist-search disposition = %q err=%v", phaseReason, err)
	}
	changed, acquired, err = acquisition.Advance(ctx, admission.OriginalRunID, 1, jobID, claim)
	if err != nil || !acquired || changed {
		t.Fatalf("replay consumed empty-search delivery: changed=%v acquired=%v err=%v", changed, acquired, err)
	}
	replayedReview, err := service.NewMatchingReviewService(persistence.NewIncomingMatchCandidateRepository(db)).Get(ctx, fixture.group.ID)
	if err != nil || !replayedReview.Evaluated || !replayedReview.ProposalAvailable || replayedReview.EvaluationRevision != apiReview.EvaluationRevision || string(replayedReview.Proposal) != string(apiReview.Proposal) {
		t.Fatalf("consumed-delivery replay changed published API review: before=%+v after=%+v err=%v", apiReview, replayedReview, err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("simulate completed evaluation River delivery: %v", err)
	}
	var beforeOperation struct {
		State  string `bun:"state"`
		Stage  string `bun:"stage"`
		Run    string `bun:"run_state"`
		Phase  string `bun:"current_phase"`
		Rev    int64  `bun:"revision"`
		Header []byte `bun:"evaluation_evidence"`
	}
	if err := db.NewRaw(`SELECT operation.state,operation.stage,run.state AS run_state,acquisition.current_phase,
		acquisition.revision,evaluation.evaluation_evidence FROM operation
		JOIN matching_run run ON run.operation_id=operation.id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		JOIN incoming_match_evaluation evaluation ON evaluation.group_id=? WHERE operation.id=?`,
		fixture.group.ID, admission.OriginalOperationID).Scan(ctx, &beforeOperation); err != nil {
		t.Fatalf("capture published review runtime state: %v", err)
	}
	owned, err := repository.IsMatchingRuntimeScannerOwnedOperation(ctx, admission.OriginalOperationID)
	if err != nil || !owned {
		t.Fatalf("published current automatic artist review is not a startup wait owner: owned=%v err=%v state=%s", owned, err, automaticArtistReviewOwnershipState(t, ctx, db, admission.OriginalOperationID))
	}
	if err := jobs.ReconcileInterruptedOperations(ctx, repository, service.NewOperations(repository),
		func(context.Context, *int64) (bool, error) { return false, nil }, settings.New(persistence.NewSettingsRepository(db), nil)); err != nil {
		t.Fatalf("reconcile interrupted operations with a current accepted review: %v", err)
	}
	var afterOperation struct {
		State  string `bun:"state"`
		Stage  string `bun:"stage"`
		Run    string `bun:"run_state"`
		Phase  string `bun:"current_phase"`
		Rev    int64  `bun:"revision"`
		Header []byte `bun:"evaluation_evidence"`
	}
	if err := db.NewRaw(`SELECT operation.state,operation.stage,run.state AS run_state,acquisition.current_phase,
		acquisition.revision,evaluation.evaluation_evidence FROM operation
		JOIN matching_run run ON run.operation_id=operation.id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		JOIN incoming_match_evaluation evaluation ON evaluation.group_id=? WHERE operation.id=?`,
		fixture.group.ID, admission.OriginalOperationID).Scan(ctx, &afterOperation); err != nil {
		t.Fatalf("read published review runtime state after ownership check: %v", err)
	}
	if beforeOperation.State != afterOperation.State || beforeOperation.Stage != afterOperation.Stage || beforeOperation.Run != afterOperation.Run ||
		beforeOperation.Phase != afterOperation.Phase || beforeOperation.Rev != afterOperation.Rev || string(beforeOperation.Header) != string(afterOperation.Header) {
		t.Fatalf("ownership classification changed published review state: before=%+v after=%+v", beforeOperation, afterOperation)
	}
}

func TestAutomaticArtistSearchReviewRejectsHeaderPublishedAfterAdmissionPostgreSQL(t *testing.T) {
	t.Parallel()
	run := prepareAutomaticArtistReviewDelivery(t, json.RawMessage(`{"count":0,"offset":0,"artists":[]}`))

	// Publish a genuine current evaluation via the normal repository path after
	// admission captured the absence of a header. This is the CAS race under test;
	// do not rewrite target hashes or manufacture an invalid header row.
	results := persistence.NewIncomingMatchCandidateRepository(run.db)
	if err := results.Replace(run.ctx, run.source.evaluation(nil)); err != nil {
		t.Fatalf("seed valid current evaluation after admission: %v", err)
	}
	before, err := results.GetReviewSnapshot(run.ctx, run.source.group.ID)
	if err != nil || before == nil || before.Header == nil {
		t.Fatalf("read seeded valid evaluation header: snapshot=%+v err=%v", before, err)
	}
	phaseBefore, revisionBefore := readAutomaticArtistAcquisitionCursor(t, run)
	changed, acquired, err := run.acquisition.Advance(run.ctx, run.admission.OriginalRunID, 1, run.jobID, run.claim)
	if err == nil || !strings.Contains(err.Error(), "evaluation revision changed since automatic artist admission") || !acquired || changed {
		t.Fatalf("advance after evaluation header raced admission: changed=%v acquired=%v err=%v", changed, acquired, err)
	}
	after, err := results.GetReviewSnapshot(run.ctx, run.source.group.ID)
	if err != nil || after == nil || after.Header == nil || after.Header.EvaluationRevision != before.Header.EvaluationRevision {
		t.Fatalf("CAS rejection changed the valid current header: before=%+v after=%+v err=%v", before, after, err)
	}
	phaseAfter, revisionAfter := readAutomaticArtistAcquisitionCursor(t, run)
	if phaseAfter != phaseBefore || revisionAfter != revisionBefore || phaseAfter != "admitted" || revisionAfter != 0 {
		t.Fatalf("CAS rejection advanced acquisition: before=%s/%d after=%s/%d", phaseBefore, revisionBefore, phaseAfter, revisionAfter)
	}
}

func TestAutomaticArtistReviewStartupOwnershipUsesPublishedAcceptedPayloadPostgreSQL(t *testing.T) {
	t.Parallel()
	run := prepareAutomaticArtistReviewDelivery(t, json.RawMessage(`{"count":0,"offset":0,"artists":[]}`))
	if changed, acquired, err := run.acquisition.Advance(run.ctx, run.admission.OriginalRunID, 1, run.jobID, run.claim); err != nil || !acquired || !changed {
		t.Fatalf("publish accepted artist-search review: changed=%v acquired=%v err=%v", changed, acquired, err)
	}
	if _, err := run.db.NewRaw(`UPDATE provider_response_cache SET payload=?::jsonb WHERE provider_source_id=? AND cache_key=?`,
		`{"count":1,"offset":0,"artists":[{"id":"later-response"}]}`, run.source.source, "artist-search:0:10:Fixture Artist").Exec(run.ctx); err != nil {
		t.Fatalf("refresh mutable provider cache: %v", err)
	}
	owned, err := run.repository.IsMatchingRuntimeScannerOwnedOperation(run.ctx, run.admission.OriginalOperationID)
	if err != nil || !owned {
		t.Fatalf("accepted review lost ownership after cache refresh: owned=%v err=%v state=%s", owned, err, automaticArtistReviewOwnershipState(t, run.ctx, run.db, run.admission.OriginalOperationID))
	}
	if _, err := run.db.NewRaw(`UPDATE incoming_matching_generation SET high_water=high_water+1 WHERE group_id=?`, run.source.group.ID).Exec(run.ctx); err != nil {
		t.Fatalf("advance current matching generation: %v", err)
	}
	owned, err = run.repository.IsMatchingRuntimeScannerOwnedOperation(run.ctx, run.admission.OriginalOperationID)
	if err != nil || owned {
		t.Fatalf("stale accepted review retained ownership after generation advanced: owned=%v err=%v", owned, err)
	}
	if _, err := run.db.NewRaw(`DELETE FROM incoming_match_evaluation WHERE group_id=?`, run.source.group.ID).Exec(run.ctx); err != nil {
		t.Fatalf("remove published header: %v", err)
	}
	owned, err = run.repository.IsMatchingRuntimeScannerOwnedOperation(run.ctx, run.admission.OriginalOperationID)
	if err != nil || owned {
		t.Fatalf("missing published header retained ownership: owned=%v err=%v", owned, err)
	}
}

func automaticArtistReviewOwnershipState(t *testing.T, ctx context.Context, db *bun.DB, operationID uuid.UUID) string {
	t.Helper()
	var state struct {
		OperationState            string `bun:"operation_state"`
		OperationStage            string `bun:"operation_stage"`
		RunState                  string `bun:"run_state"`
		AcquisitionPhase          string `bun:"acquisition_phase"`
		PhaseName                 string `bun:"phase_name"`
		Reason                    string `bun:"reason"`
		ProvenanceCount           int    `bun:"provenance_count"`
		CurrentRun                bool   `bun:"current_run"`
		JobBinding                bool   `bun:"job_binding"`
		GenerationMatch           bool   `bun:"generation_match"`
		RequestedMatch            bool   `bun:"requested_match"`
		AuthorityRevision         int64  `bun:"authority_revision"`
		CapturedAuthorityRevision int64  `bun:"captured_authority_revision"`
		DependencyReady           bool   `bun:"dependency_ready"`
		DeliveryConsumed          bool   `bun:"delivery_consumed"`
	}
	err := db.NewRaw(`SELECT operation.state AS operation_state,operation.stage AS operation_stage,run.state AS run_state,
		acquisition.current_phase AS acquisition_phase,phase.phase AS phase_name,phase.output->>'reason' AS reason,
		jsonb_array_length(phase.response_provenance) AS provenance_count,
		generation.current_run_id=run.id AS current_run,
		run.current_river_job_id=operation.river_job_id AS job_binding,
		generation.high_water=target.target_generation AS generation_match,
		(target.reconciliation_generation IS NULL OR generation.requested_generation=target.reconciliation_generation) AS requested_match,
		evaluation.matching_authority_revision AS authority_revision,
		(target.target_capture->>'authority_revision')::bigint AS captured_authority_revision,
		EXISTS (SELECT 1 FROM matching_provider_dependency dependency WHERE dependency.parent_run_id=run.id AND dependency.terminal_outcome='succeeded') AS dependency_ready,
		EXISTS (SELECT 1 FROM matching_delivery delivery WHERE delivery.run_id=run.id AND delivery.river_job_id=run.current_river_job_id AND delivery.consumed_at IS NOT NULL) AS delivery_consumed
		FROM matching_run run JOIN operation ON operation.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		JOIN matching_acquisition_phase phase ON phase.run_id=run.id AND phase.revision=acquisition.revision
		JOIN matching_run_target target ON target.run_id=run.id
		JOIN incoming_match_evaluation evaluation ON evaluation.group_id=target.group_id
		JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
		WHERE operation.id=?`, operationID).Scan(ctx, &state)
	if err != nil {
		t.Fatalf("read bounded automatic artist ownership diagnostics: %v", err)
	}
	return fmt.Sprintf("op=%s/%s run=%s phase=%s/%s reason=%s provenance=%d current=%t job=%t generation=%t request=%t authority=%d/%d dependency=%t consumed=%t",
		state.OperationState, state.OperationStage, state.RunState, state.AcquisitionPhase, state.PhaseName, state.Reason,
		state.ProvenanceCount, state.CurrentRun, state.JobBinding, state.GenerationMatch, state.RequestedMatch,
		state.AuthorityRevision, state.CapturedAuthorityRevision, state.DependencyReady, state.DeliveryConsumed)
}

func TestAutomaticArtistSearchUnsuitableTopDoesNotBrowseSuitableSecondPostgreSQL(t *testing.T) {
	t.Parallel()
	payload := json.RawMessage(`{"count":2,"offset":0,"artists":[{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"ZZZ Totally Different Person"},{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"Fixture Artist"}]}`)
	run := prepareAutomaticArtistReviewDelivery(t, payload)
	snapshot, exists, err := run.repository.GetMatchingAcquisition(run.ctx, run.admission.OriginalRunID)
	if err != nil || !exists {
		t.Fatalf("read automatic acquisition capture: exists=%v err=%v", exists, err)
	}
	fit, available, err := service.MatchingArtistFit(snapshot.Intent.CapturedMembers, "Fixture Artist")
	if err != nil || !available || fit < 0.70 {
		t.Fatalf("second artist is not suitable for the captured raw tags: fit=%v available=%v err=%v", fit, available, err)
	}

	changed, acquired, err := run.acquisition.Advance(run.ctx, run.admission.OriginalRunID, 1, run.jobID, run.claim)
	if err != nil || !acquired || !changed {
		t.Fatalf("advance unsuitable top artist response: changed=%v acquired=%v err=%v", changed, acquired, err)
	}
	phase, revision := readAutomaticArtistAcquisitionCursor(t, run)
	if phase != "waiting_policy" || revision != 1 {
		t.Fatalf("unsuitable top artist cursor = %s/%d, want waiting_policy/1", phase, revision)
	}
	var reason string
	if err := run.db.NewRaw(`SELECT output->>'reason' FROM matching_acquisition_phase WHERE run_id=? AND revision=1`, run.admission.OriginalRunID).Scan(run.ctx, &reason); err != nil || reason != "artist_not_fit" {
		t.Fatalf("unsuitable top artist outcome = %q err=%v, want artist_not_fit", reason, err)
	}
	var browseCount, fallbackCount int
	if err := run.db.NewRaw(`SELECT count(*) FROM operation WHERE kind='provider_artist_release_browse'`).Scan(run.ctx, &browseCount); err != nil {
		t.Fatal(err)
	}
	if err := run.db.NewRaw(`SELECT count(*) FROM operation WHERE kind='matching_fallback_search_v1'`).Scan(run.ctx, &fallbackCount); err != nil {
		t.Fatal(err)
	}
	if browseCount != 0 || fallbackCount != 0 {
		t.Fatalf("unsuitable top artist started downstream work: browses=%d fallbacks=%d", browseCount, fallbackCount)
	}
	review, err := service.NewMatchingReviewService(persistence.NewIncomingMatchCandidateRepository(run.db)).Get(run.ctx, run.source.group.ID)
	if err != nil || !review.Evaluated || !review.ProposalAvailable {
		t.Fatalf("read fit-review proposal: evaluated=%v available=%v err=%v", review.Evaluated, review.ProposalAvailable, err)
	}
}

type automaticArtistReviewDelivery struct {
	ctx         context.Context
	db          *bun.DB
	source      *incomingMatchFixture
	repository  *persistence.SetupManagerRepository
	cache       *persistence.ProviderCacheRepository
	admission   persistence.AutomaticArtistMatchingAdmissionResult
	jobID       int64
	claim       persistence.MatchingDeliveryClaim
	acquisition *service.MatchingAcquisitionService
}

func prepareAutomaticArtistReviewDelivery(t *testing.T, payload json.RawMessage) *automaticArtistReviewDelivery {
	t.Helper()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	sourceFixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewSetupManagerRepository(db)
	inventory := persistence.NewSourceInventoryRepository(db)
	client := openScanEnqueueRiver(t, db)

	var event persistence.IncomingMatchingReconcileEvent
	if err := db.NewSelect().Model(&event).Where("reason = ?", "analysis_success").Order("created_at ASC").Order("id ASC").Limit(1).Scan(ctx); err != nil {
		t.Fatalf("read analysis-success reconciliation event: %v", err)
	}
	if err := inventory.DispatchPendingMatchingReconciliations(ctx, client); err != nil {
		t.Fatalf("dispatch reconciliation event: %v", err)
	}
	requests, err := inventory.ReadPendingCurrentRequests(ctx)
	if err != nil {
		t.Fatalf("read observed current matching requests: %v", err)
	}
	var requestGeneration int64
	for _, request := range requests {
		if request.GroupID == sourceFixture.group.ID {
			requestGeneration = request.RequestedGeneration
			break
		}
	}
	if requestGeneration < 1 {
		t.Fatalf("fixture current request generation = %d, want an observed positive generation", requestGeneration)
	}
	dispatched, err := inventory.GetIncomingMatchingReconciliationEvent(ctx, event.ID)
	if err != nil || dispatched.RegisteredJobID == nil {
		t.Fatalf("read dispatched reconciliation event: event=%+v err=%v", dispatched, err)
	}
	epoch, claimed, err := inventory.ClaimIncomingMatchingReconciliationDelivery(ctx, event.ID, *dispatched.RegisteredJobID, 1)
	if err != nil || !claimed {
		t.Fatalf("claim reconciliation delivery: epoch=%d claimed=%v err=%v", epoch, claimed, err)
	}
	var source struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := db.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, sourceFixture.source).Scan(ctx, &source); err != nil {
		t.Fatalf("read provider source fence: %v", err)
	}
	query := json.RawMessage(`{"schema_version":1,"query":"Fixture Artist","offset":0,"limit":10}`)
	cacheKey := "artist-search:0:10:Fixture Artist"
	parent := &persistence.Operation{ID: uuid.New(), Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	providerOperation := &persistence.Operation{
		ID: uuid.New(), Kind: "provider_artist_search", State: "queued", Stage: "queued", InputSnapshot: query,
		ProviderSourceID: &sourceFixture.source, ProviderID: &source.ProviderID,
		ProviderConfigurationIdentity: &source.Identity, ProviderEndpoint: source.Endpoint,
		ProviderCacheKey: &cacheKey, Attempt: 1,
	}
	admission, err := repository.AdmitAutomaticArtistMatchingRun(ctx, client, persistence.AutomaticArtistMatchingAdmission{
		Parent: parent, ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parent.ID},
		Provider: persistence.MatchingProviderRequest{Operation: providerOperation, Args: service.ProviderFetchOperationArgs{OperationID: providerOperation.ID}},
		GroupID:  sourceFixture.group.ID, ExpectedGroupRevision: sourceFixture.group.Revision,
		PolicyVersion: "matching-rapidfuzz-v1", ArtistQueryPlanVersion: 1, QueryDescriptor: query,
		EventID: event.ID, EventRiverJobID: *dispatched.RegisteredJobID, EventRiverAttempt: 1, EventDeliveryEpoch: epoch,
		RequestGeneration: requestGeneration,
	})
	if err != nil {
		t.Fatalf("admit real automatic artist matching run: %v", err)
	}
	if admission.Operation == nil || admission.Operation.RiverJobID == nil || providerOperation.RiverJobID == nil {
		t.Fatal("automatic admission did not return registered operations")
	}
	providerJobID := *providerOperation.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, providerJobID).Exec(ctx); err != nil {
		t.Fatalf("prepare provider River delivery: %v", err)
	}
	providerEpoch, providerClaimed, err := repository.ClaimProviderOperationDelivery(ctx, providerOperation.ID, 1, providerJobID)
	if err != nil || !providerClaimed {
		t.Fatalf("claim provider operation delivery: epoch=%d claimed=%v err=%v", providerEpoch, providerClaimed, err)
	}
	if changed, err := repository.TransitionProviderOperationForDelivery(ctx, providerOperation.ID, 1, providerJobID, providerEpoch, func(operation *persistence.Operation) error {
		operation.State, operation.Stage = "running", "fetching"
		return nil
	}); err != nil || !changed {
		t.Fatalf("start provider operation delivery: changed=%v err=%v", changed, err)
	}
	cache := persistence.NewProviderCacheRepository(db)
	ticket, err := cache.BeginRefresh(ctx, sourceFixture.source, cacheKey, source.Identity)
	if err != nil {
		t.Fatalf("begin accepted artist-search cache generation: %v", err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(), Revision: "artist-search-r1", Payload: payload,
		Delivery: &persistence.ProviderDeliveryFence{OperationID: providerOperation.ID, Attempt: 1, RiverJobID: providerJobID, ExecutionEpoch: providerEpoch},
	}); err != nil {
		t.Fatalf("persist accepted artist-search response: %v", err)
	}
	if changed, err := repository.TransitionProviderOperationForDeliveryWithMatchingWake(ctx, providerOperation.ID, 1, providerJobID, providerEpoch, client, func(operation *persistence.Operation) error {
		operation.State, operation.Stage = "succeeded", "complete"
		finished := time.Now().UTC()
		operation.FinishedAt = &finished
		return nil
	}); err != nil || !changed {
		t.Fatalf("settle accepted provider delivery and wake matching: changed=%v err=%v", changed, err)
	}
	jobID := *admission.Operation.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("prepare registered matching River delivery: %v", err)
	}
	claim, outcome, err := repository.ClaimMatchingDelivery(ctx, admission.OriginalRunID, 1, jobID, 1)
	if err != nil || outcome != persistence.MatchingDeliveryClaimed {
		t.Fatalf("claim admitted evaluation delivery: claim=%+v outcome=%v err=%v", claim, outcome, err)
	}
	return &automaticArtistReviewDelivery{
		ctx: ctx, db: db, source: sourceFixture, repository: repository, cache: cache,
		admission: admission, jobID: jobID, claim: claim,
		acquisition: service.NewMatchingAcquisitionService(repository, artistReviewCachedProvider{cache: cache, sourceID: sourceFixture.source}, client),
	}
}

func readAutomaticArtistAcquisitionCursor(t *testing.T, run *automaticArtistReviewDelivery) (string, int64) {
	t.Helper()
	var phase string
	var revision int64
	if err := run.db.NewRaw(`SELECT current_phase,revision FROM matching_acquisition WHERE run_id=?`, run.admission.OriginalRunID).Scan(run.ctx, &phase, &revision); err != nil {
		t.Fatalf("read matching acquisition cursor: %v", err)
	}
	return phase, revision
}
