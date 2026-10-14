//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
)

func TestAutomaticFallbackCurrentGenerationAdmissionFencePostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAutomaticMatchingTargetFenceFixture(t)
	var target struct {
		GroupID  uuid.UUID `bun:"group_id"`
		Revision string    `bun:"captured_group_revision"`
	}
	if err := fixture.db.NewRaw(`SELECT group_id,captured_group_revision FROM matching_run_target WHERE run_id=?`, fixture.runID).Scan(fixture.ctx, &target); err != nil {
		t.Fatalf("read fixture target: %v", err)
	}
	var event persistence.IncomingMatchingReconcileEvent
	if err := fixture.db.NewSelect().Model(&event).Where("state = ? AND delivery_claimed", "dispatched").Order("created_at DESC").Limit(1).Scan(fixture.ctx); err != nil {
		t.Fatalf("read claimed reconciliation event: %v", err)
	}
	var source struct {
		ID         uuid.UUID `bun:"provider_source_id"`
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := fixture.db.NewRaw(`SELECT id AS provider_source_id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=(SELECT provider_source_id FROM matching_run WHERE id=?)`, fixture.runID).Scan(fixture.ctx, &source); err != nil {
		t.Fatalf("read fixture provider source: %v", err)
	}
	var eventEpoch int64
	if err := fixture.db.NewRaw(`SELECT delivery_epoch FROM incoming_matching_reconcile_event WHERE id=?`, event.ID).Scan(fixture.ctx, &eventEpoch); err != nil {
		t.Fatalf("read reconciliation delivery epoch: %v", err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE media_metadata_result SET observed_tags='{"ALBUM":["Captured Album"],"TITLE":["Captured Track"]}'::jsonb`).Exec(fixture.ctx); err != nil {
		t.Fatalf("set artistless fallback metadata: %v", err)
	}
	tags := []map[string][]string{{"ALBUM": {"Captured Album"}, "TITLE": {"Captured Track"}}, {"ALBUM": {"Captured Album"}, "TITLE": {"Captured Track"}}}
	planned, artistBearing := policy.PlanFallbackQueries(tags)
	if artistBearing || len(planned.Stages) == 0 {
		t.Fatalf("fixture did not produce an artistless positive fallback plan: artistBearing=%v plan=%+v", artistBearing, planned)
	}
	plan := policy.TypedFallbackQueryPlan{SchemaVersion: planned.SchemaVersion, Stages: planned.Stages}
	stage := plan.Stages[0]
	providerKind, cachePrefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	if !ok {
		t.Fatalf("fallback stage %q has no provider mapping", stage.Kind)
	}
	makeAdmission := func(generation int64) persistence.AutomaticFallbackMatchingAdmission {
		parentID, providerOperationID := uuid.New(), uuid.New()
		key, identity := cachePrefix+":0:10:"+stage.Query, source.Identity
		providerID, sourceID := source.ProviderID, source.ID
		input, err := json.Marshal(struct {
			SchemaVersion int    `json:"schema_version"`
			Query         string `json:"query"`
			Offset        int    `json:"offset"`
			Limit         int    `json:"limit"`
		}{1, stage.Query, 0, 10})
		if err != nil {
			t.Fatalf("marshal prepared fallback request: %v", err)
		}
		provider := &persistence.Operation{ID: providerOperationID, Kind: providerKind, State: "queued", Stage: "queued", InputSnapshot: input, ProviderSourceID: &sourceID, ProviderID: &providerID, ProviderConfigurationIdentity: &identity, ProviderEndpoint: source.Endpoint, ProviderCacheKey: &key, Attempt: 1}
		admissionPlan := plan
		admissionPlan.Stages = append([]policy.TypedFallbackQueryStage(nil), plan.Stages...)
		return persistence.AutomaticFallbackMatchingAdmission{
			Parent:     &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`), Attempt: 1},
			ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
			Provider:   persistence.MatchingProviderRequest{Operation: provider, Args: service.ProviderFetchOperationArgs{OperationID: providerOperationID}},
			Source:     persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: source.ProviderID, Endpoint: source.Endpoint, ConfigurationIdentity: source.Identity},
			GroupID:    target.GroupID, ExpectedGroupRevision: target.Revision, PolicyVersion: service.MatchingPolicyVersion,
			Plan: plan, EventID: event.ID, EventRiverJobID: *event.RegisteredJobID,
			EventRiverAttempt: event.HighestDeliveryAttempt, EventDeliveryEpoch: eventEpoch, RequestGeneration: generation,
		}
	}
	request := func() int64 {
		t.Helper()
		if err := fixture.db.RunInTx(fixture.ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return persistence.RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{target.GroupID})
		}); err != nil {
			t.Fatalf("request current reconciliation: %v", err)
		}
		var requested int64
		if err := fixture.db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, target.GroupID).Scan(fixture.ctx, &requested); err != nil {
			t.Fatalf("read requested generation: %v", err)
		}
		return requested
	}
	if got := request(); got < 1 {
		t.Fatalf("current request generation must be positive, got %d", got)
	}
	var observed int64
	if err := fixture.db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, target.GroupID).Scan(fixture.ctx, &observed); err != nil {
		t.Fatalf("capture observed current generation: %v", err)
	}
	client := openScanEnqueueRiver(t, fixture.db)
	first, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, makeAdmission(observed))
	if err != nil || first.AlreadyAdmitted || first.Operation == nil {
		t.Fatalf("admit observed current fallback generation %d: result=%+v err=%v", observed, first, err)
	}
	if first.Operation.ID != first.OriginalRunID || first.OriginalOperationID != first.OriginalRunID {
		t.Fatalf("admission result did not return the admitted parent as its original run: result=%+v", first)
	}
	var accepted struct {
		RunID                    uuid.UUID `bun:"run_id"`
		ReconciliationGeneration int64     `bun:"reconciliation_generation"`
	}
	if err := fixture.db.NewRaw(`SELECT run_id,reconciliation_generation FROM matching_run_target WHERE run_id=? AND group_id=?`, first.OriginalRunID, target.GroupID).Scan(fixture.ctx, &accepted); err != nil || accepted.ReconciliationGeneration != observed {
		t.Fatalf("accepted run did not persist observed current generation: target=%+v err=%v", accepted, err)
	}

	stale := makeAdmission(observed)
	if next := request(); next != observed+1 {
		t.Fatalf("new current request did not advance generation: old=%d new=%d", observed, next)
	}
	before := readFallbackCurrentGenerationState(t, fixture, target.GroupID)
	if _, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, stale); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("stale fallback request generation was not rejected as a conflict: %v", err)
	}
	after := readFallbackCurrentGenerationState(t, fixture, target.GroupID)
	if before != after {
		t.Fatalf("stale admission changed durable state: before=%+v after=%+v", before, after)
	}

	current := makeAdmission(observed + 1)
	second, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, current)
	if err != nil || second.AlreadyAdmitted || second.OriginalRunID == first.OriginalRunID {
		t.Fatalf("admit newer current fallback generation: result=%+v err=%v", second, err)
	}
	assertFallbackCurrentTargetGeneration(t, fixture, second.OriginalRunID, target.GroupID, observed+1)
	// Capture the original receipt, immutable acquisition intent, provider-child
	// links, and authority tuple before replaying the exact same admission.
	originalReceipt := readFallbackAdmissionReceipt(t, fixture, second.OriginalRunID)
	originalAcquisition, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, second.OriginalRunID)
	if err != nil || !exists {
		t.Fatalf("read original admission acquisition: exists=%v err=%v", exists, err)
	}
	originalLinks := readFallbackProviderLinks(t, fixture, second.OriginalRunID)
	if len(originalLinks) == 0 {
		t.Fatalf("original fallback admission captured no provider-child links")
	}
	originalAuthority := readFallbackAuthorityTuple(t, fixture, second.OriginalRunID)
	beforeReplay := readFallbackCurrentGenerationState(t, fixture, target.GroupID)
	replay, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, current)
	if err != nil || !replay.AlreadyAdmitted || replay.OriginalRunID != second.OriginalRunID {
		t.Fatalf("replay exact current-generation admission: result=%+v err=%v", replay, err)
	}
	if replay.OriginalOperationID != second.OriginalOperationID || replay.Operation == nil || replay.Operation.ID != second.Operation.ID {
		t.Fatalf("replay did not return the original admitted parent: original=%+v replay=%+v", second, replay)
	}
	if afterReplay := readFallbackCurrentGenerationState(t, fixture, target.GroupID); afterReplay != beforeReplay {
		t.Fatalf("exact admission replay created durable work: before=%+v after=%+v", beforeReplay, afterReplay)
	}
	if preserved := readFallbackAdmissionReceipt(t, fixture, second.OriginalRunID); !preserved.equal(originalReceipt) {
		t.Fatalf("exact admission replay mutated the original receipt: before=%+v after=%+v", originalReceipt, preserved)
	}
	replayedAcquisition, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, second.OriginalRunID)
	if err != nil || !exists {
		t.Fatalf("reread replayed admission acquisition: exists=%v err=%v", exists, err)
	}
	assertFallbackAcquisitionImmutable(t, originalAcquisition, replayedAcquisition)
	if afterLinks := readFallbackProviderLinks(t, fixture, second.OriginalRunID); !reflect.DeepEqual(afterLinks, originalLinks) {
		t.Fatalf("exact admission replay changed the provider-child links: before=%+v after=%+v", originalLinks, afterLinks)
	}
	if afterAuthority := readFallbackAuthorityTuple(t, fixture, second.OriginalRunID); !reflect.DeepEqual(afterAuthority, originalAuthority) {
		t.Fatalf("exact admission replay changed the authority tuple: before=%+v after=%+v", originalAuthority, afterAuthority)
	}

	if next := request(); next != observed+2 {
		t.Fatalf("third current request did not advance generation: expected=%d got=%d", observed+2, next)
	}
	third, err := fixture.repository.AdmitAutomaticFallbackMatchingRun(fixture.ctx, client, makeAdmission(observed+2))
	if err != nil || third.AlreadyAdmitted || third.OriginalRunID == second.OriginalRunID {
		t.Fatalf("unchanged inputs at newer current generation were deduplicated: result=%+v err=%v", third, err)
	}
	assertFallbackCurrentTargetGeneration(t, fixture, third.OriginalRunID, target.GroupID, observed+2)
	newerReceipt := readFallbackAdmissionReceipt(t, fixture, third.OriginalRunID)
	if bytes.Equal(originalReceipt.Fingerprint, newerReceipt.Fingerprint) {
		t.Fatalf("unchanged inputs at a newer current generation reused the original admission fingerprint: %x", newerReceipt.Fingerprint)
	}
	// A fresh generation must leave the prior run's receipt, acquisition intent,
	// provider-child links, and authority tuple untouched.
	if preserved := readFallbackAdmissionReceipt(t, fixture, second.OriginalRunID); !preserved.equal(originalReceipt) {
		t.Fatalf("newer generation mutated the original admission receipt: before=%+v after=%+v", originalReceipt, preserved)
	}
	preservedAcquisition, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, second.OriginalRunID)
	if err != nil || !exists {
		t.Fatalf("reread prior admission acquisition after fresh generation: exists=%v err=%v", exists, err)
	}
	assertFallbackAcquisitionImmutable(t, originalAcquisition, preservedAcquisition)
	if preservedLinks := readFallbackProviderLinks(t, fixture, second.OriginalRunID); !reflect.DeepEqual(preservedLinks, originalLinks) {
		t.Fatalf("newer generation mutated the original provider-child links: before=%+v after=%+v", originalLinks, preservedLinks)
	}
	if preservedAuthority := readFallbackAuthorityTuple(t, fixture, second.OriginalRunID); !reflect.DeepEqual(preservedAuthority, originalAuthority) {
		t.Fatalf("newer generation mutated the original authority tuple: before=%+v after=%+v", originalAuthority, preservedAuthority)
	}
	newerAcquisition, exists, err := fixture.repository.GetMatchingAcquisition(fixture.ctx, third.OriginalRunID)
	if err != nil || !exists {
		t.Fatalf("read newer admission acquisition: exists=%v err=%v", exists, err)
	}
	assertFallbackAcquisitionSemanticsUnchanged(t, originalAcquisition, newerAcquisition)
}

type fallbackAdmissionReceipt struct {
	Fingerprint         []byte    `bun:"fingerprint"`
	OriginalRunID       uuid.UUID `bun:"original_run_id"`
	OriginalOperationID uuid.UUID `bun:"original_operation_id"`
}

func (receipt fallbackAdmissionReceipt) equal(other fallbackAdmissionReceipt) bool {
	return bytes.Equal(receipt.Fingerprint, other.Fingerprint) &&
		receipt.OriginalRunID == other.OriginalRunID &&
		receipt.OriginalOperationID == other.OriginalOperationID
}

func readFallbackAdmissionReceipt(t *testing.T, fixture automaticTargetFenceFixture, runID uuid.UUID) fallbackAdmissionReceipt {
	t.Helper()
	var receipt fallbackAdmissionReceipt
	if err := fixture.db.NewRaw(`SELECT fingerprint,original_run_id,original_operation_id FROM incoming_matching_automatic_admission WHERE original_run_id=?`, runID).Scan(fixture.ctx, &receipt); err != nil {
		t.Fatalf("read admission receipt for run %s: %v", runID, err)
	}
	if len(receipt.Fingerprint) != 32 {
		t.Fatalf("admission receipt for run %s has malformed fingerprint: %x", runID, receipt.Fingerprint)
	}
	return receipt
}

// assertFallbackAcquisitionSemanticsUnchanged proves that a newer current
// generation reuses the same captured authority (group revision, metadata,
// plan, provider source) and differs only in the freshly generated operation
// ids that the durable intent records for each admission.
func assertFallbackAcquisitionSemanticsUnchanged(t *testing.T, original, newer persistence.MatchingAcquisitionSnapshot) {
	t.Helper()
	left, right := original.Intent, newer.Intent
	if left.SchemaVersion != right.SchemaVersion || left.FenceVersion != right.FenceVersion ||
		left.GroupingEpoch != right.GroupingEpoch || left.PlanDigest != right.PlanDigest ||
		left.CaptureDigest != right.CaptureDigest || left.PolicyVersion != right.PolicyVersion ||
		left.TargetCaptureDigest != right.TargetCaptureDigest ||
		!bytes.Equal(left.FallbackQueryPlan, right.FallbackQueryPlan) ||
		!reflect.DeepEqual(left.FallbackProviderSource, right.FallbackProviderSource) ||
		!reflect.DeepEqual(left.Targets, right.Targets) ||
		!reflect.DeepEqual(left.CapturedMembers, right.CapturedMembers) {
		t.Fatalf("newer current generation changed the saved acquisition semantics: original=%+v newer=%+v", left, right)
	}
	if len(left.InitialProviderInputs) != len(right.InitialProviderInputs) {
		t.Fatalf("newer current generation changed the initial provider request count: original=%d newer=%d", len(left.InitialProviderInputs), len(right.InitialProviderInputs))
	}
	for index := range left.InitialProviderInputs {
		originalInput, newerInput := left.InitialProviderInputs[index], right.InitialProviderInputs[index]
		if originalInput.Kind != newerInput.Kind || originalInput.CacheKey != newerInput.CacheKey || !bytes.Equal(originalInput.Input, newerInput.Input) {
			t.Fatalf("newer current generation changed the initial provider request at %d: original=%+v newer=%+v", index, originalInput, newerInput)
		}
		if originalInput.OperationID == newerInput.OperationID {
			t.Fatalf("newer current generation reused the generated provider operation id at %d: %s", index, originalInput.OperationID)
		}
	}
}

// assertFallbackAcquisitionImmutable proves that an admission replay or a newer
// current generation leaves a prior run's append-only acquisition ledger and
// captured intent byte-for-byte unchanged, including its provider child ids.
func assertFallbackAcquisitionImmutable(t *testing.T, before, after persistence.MatchingAcquisitionSnapshot) {
	t.Helper()
	if before.Revision != after.Revision || before.Phase != after.Phase ||
		!bytes.Equal(before.Input, after.Input) || !bytes.Equal(before.Output, after.Output) ||
		!reflect.DeepEqual(before.Intent, after.Intent) {
		t.Fatalf("prior admission acquisition was mutated: before=%+v after=%+v", before, after)
	}
}

// fallbackProviderLink is one exact parent-to-child link: the dependency row and
// the immutable fallback request registration that binds the same request id.
type fallbackProviderLink struct {
	RequestID             uuid.UUID       `bun:"request_id"`
	CacheKey              string          `bun:"cache_key"`
	RegistrationCacheKey  string          `bun:"registration_cache_key"`
	CanonicalInput        json.RawMessage `bun:"canonical_input"`
	InputSHA256           []byte          `bun:"input_sha256"`
	StageIndex            int             `bun:"stage_index"`
	Role                  string          `bun:"role"`
	Kind                  string          `bun:"kind"`
	ProviderSourceID      uuid.UUID       `bun:"provider_source_id"`
	ConfigurationIdentity string          `bun:"configuration_identity"`
}

func readFallbackProviderLinks(t *testing.T, fixture automaticTargetFenceFixture, runID uuid.UUID) []fallbackProviderLink {
	t.Helper()
	var links []fallbackProviderLink
	err := fixture.db.NewRaw(`SELECT dependency.request_id,dependency.cache_key,
		registration.cache_key AS registration_cache_key,registration.canonical_input,registration.input_sha256,
		registration.stage_index,registration.role,registration.kind,registration.provider_source_id,registration.configuration_identity
		FROM matching_provider_dependency dependency
		JOIN matching_fallback_request_registration registration
			ON registration.run_id=dependency.parent_run_id AND registration.request_id=dependency.request_id
		WHERE dependency.parent_run_id=? ORDER BY dependency.request_id`, runID).Scan(fixture.ctx, &links)
	if err != nil {
		t.Fatalf("read provider-child links for run %s: %v", runID, err)
	}
	return links
}

// fallbackAuthorityTuple is the persisted capture authority for one run target.
type fallbackAuthorityTuple struct {
	CapturedGroupRevision string          `bun:"captured_group_revision"`
	AuthorityRevision     int64           `bun:"authority_revision"`
	TargetCapture         json.RawMessage `bun:"target_capture"`
	TargetCaptureDigest   []byte          `bun:"target_capture_digest"`
}

func readFallbackAuthorityTuple(t *testing.T, fixture automaticTargetFenceFixture, runID uuid.UUID) fallbackAuthorityTuple {
	t.Helper()
	var tuple fallbackAuthorityTuple
	if err := fixture.db.NewRaw(`SELECT captured_group_revision,authority_revision,target_capture,target_capture_digest FROM matching_run_target WHERE run_id=?`, runID).Scan(fixture.ctx, &tuple); err != nil {
		t.Fatalf("read authority tuple for run %s: %v", runID, err)
	}
	if tuple.CapturedGroupRevision == "" || tuple.AuthorityRevision < 0 || len(tuple.TargetCaptureDigest) != 32 || len(tuple.TargetCapture) == 0 {
		t.Fatalf("authority tuple for run %s is malformed: %+v", runID, tuple)
	}
	return tuple
}

type fallbackCurrentGenerationState struct {
	Requested         int64  `bun:"requested_generation"`
	Reconciled        int64  `bun:"reconciled_generation"`
	HighWater         int64  `bun:"high_water"`
	CurrentRunID      string `bun:"current_run_id"`
	Runs              int    `bun:"runs"`
	Operations        int    `bun:"operations"`
	RiverJobs         int    `bun:"river_jobs"`
	Targets           int    `bun:"targets"`
	Dependencies      int    `bun:"dependencies"`
	Registrations     int    `bun:"registrations"`
	AdmissionReceipts int    `bun:"admission_receipts"`
}

func readFallbackCurrentGenerationState(t *testing.T, fixture automaticTargetFenceFixture, groupID uuid.UUID) fallbackCurrentGenerationState {
	t.Helper()
	var state fallbackCurrentGenerationState
	err := fixture.db.NewRaw(`SELECT generation.requested_generation,generation.reconciled_generation,generation.high_water,
		COALESCE(generation.current_run_id::text,'') AS current_run_id,
		(SELECT count(*) FROM matching_run run JOIN matching_run_target target ON target.run_id=run.id WHERE target.group_id=generation.group_id) AS runs,
		(SELECT count(*) FROM operation) AS operations,
		(SELECT count(*) FROM river_job) AS river_jobs,
		(SELECT count(*) FROM matching_run_target WHERE group_id=generation.group_id) AS targets,
		(SELECT count(*) FROM matching_provider_dependency dependency JOIN matching_run_target target ON target.run_id=dependency.parent_run_id WHERE target.group_id=generation.group_id) AS dependencies,
		(SELECT count(*) FROM matching_fallback_request_registration registration JOIN matching_run_target target ON target.run_id=registration.run_id WHERE target.group_id=generation.group_id) AS registrations,
		(SELECT count(*) FROM incoming_matching_automatic_admission receipt JOIN matching_run_target target ON target.run_id=receipt.original_run_id WHERE target.group_id=generation.group_id) AS admission_receipts
		FROM incoming_matching_generation generation WHERE generation.group_id=?`, groupID).Scan(fixture.ctx, &state)
	if err != nil {
		t.Fatalf("read current fallback generation state: %v", err)
	}
	return state
}

func assertFallbackCurrentTargetGeneration(t *testing.T, fixture automaticTargetFenceFixture, runID, groupID uuid.UUID, generation int64) {
	t.Helper()
	var target struct {
		ReconciliationGeneration sql.NullInt64 `bun:"reconciliation_generation"`
		TargetGeneration         int64         `bun:"target_generation"`
		RunID                    uuid.UUID     `bun:"run_id"`
	}
	if err := fixture.db.NewRaw(`SELECT run_id,target_generation,reconciliation_generation FROM matching_run_target WHERE run_id=? AND group_id=?`, runID, groupID).Scan(fixture.ctx, &target); err != nil {
		t.Fatalf("read target generation for run %s: %v", runID, err)
	}
	if target.RunID != runID || !target.ReconciliationGeneration.Valid || target.ReconciliationGeneration.Int64 != generation {
		t.Fatalf("run %s does not carry reconciliation generation %d: %+v", runID, generation, target)
	}
}
