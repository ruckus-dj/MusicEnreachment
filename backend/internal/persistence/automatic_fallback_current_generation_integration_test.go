//go:build integration

package persistence_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"reflect"
	"testing"
)

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
