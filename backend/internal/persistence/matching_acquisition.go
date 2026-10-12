package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/uptrace/bun"
)

const matchingAcquisitionIntentVersion = 1
const matchingAutomaticAcquisitionIntentVersion = 2
const matchingAutomaticFallbackAcquisitionIntentVersion = 3

const (
	MatchingAcquisitionArtistCatalogPage = "artist_catalog_page"
	MatchingAcquisitionReleaseHydration  = "release_hydration"
)

// StartMatchingReleaseHydration atomically records an explicit catalog shortlist
// and admits its release-lookup children. It is deliberately separate from the
// delivery-driven catalog cursor: policy callers must provide the shortlist.
func (repository *SetupManagerRepository) StartMatchingReleaseHydration(
	ctx context.Context,
	client RiverInserter,
	runID uuid.UUID,
	expectedRevision int64,
	phase MatchingAcquisitionPhase,
	children []MatchingProviderRequest,
) (bool, error) {
	return repository.startMatchingReleaseHydration(ctx, client, runID, expectedRevision, phase, children, nil)
}

// StartMatchingReleaseHydrationForDelivery atomically admits selected release
// lookups while consuming the currently claimed catalog-ranking delivery.
func (repository *SetupManagerRepository) StartMatchingReleaseHydrationForDelivery(
	ctx context.Context,
	client RiverInserter,
	runID uuid.UUID,
	attempt int,
	riverJobID int64,
	claim MatchingDeliveryClaim,
	expectedRevision int64,
	phase MatchingAcquisitionPhase,
	children []MatchingProviderRequest,
) (bool, error) {
	if attempt < 1 || riverJobID < 1 || claim.ExecutionEpoch < 1 || claim.RiverAttempt < 1 {
		return false, fmt.Errorf("start matching release hydration: invalid delivery fence")
	}
	delivery := &matchingHydrationDelivery{attempt: attempt, riverJobID: riverJobID, claim: claim}
	return repository.startMatchingReleaseHydration(ctx, client, runID, expectedRevision, phase, children, delivery)
}

type matchingHydrationDelivery struct {
	attempt    int
	riverJobID int64
	claim      MatchingDeliveryClaim
}

func (repository *SetupManagerRepository) startMatchingReleaseHydration(
	ctx context.Context,
	client RiverInserter,
	runID uuid.UUID,
	expectedRevision int64,
	phase MatchingAcquisitionPhase,
	children []MatchingProviderRequest,
	delivery *matchingHydrationDelivery,
) (bool, error) {
	if client == nil || runID == uuid.Nil || expectedRevision < 0 || phase.Revision != expectedRevision+1 || phase.Phase != MatchingAcquisitionReleaseHydration {
		return false, fmt.Errorf("start matching release hydration: invalid transition fence")
	}
	if err := validateAcquisitionJSON(phase.Input, "input"); err != nil {
		return false, err
	}
	if err := validateAcquisitionJSON(phase.Output, "output"); err != nil {
		return false, err
	}
	if string(phase.ResponseProvenance) != "[]" {
		return false, fmt.Errorf("start matching release hydration: initial provenance must be empty")
	}
	var input struct {
		ReleaseIDs []string `json:"release_ids"`
	}
	if err := json.Unmarshal(phase.Input, &input); err != nil || len(input.ReleaseIDs) == 0 || len(input.ReleaseIDs) > 10 || len(children) != len(input.ReleaseIDs) {
		return false, fmt.Errorf("start matching release hydration: shortlist is invalid")
	}
	var output struct {
		Lookups []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	if err := json.Unmarshal(phase.Output, &output); err != nil || len(output.Lookups) != len(children) {
		return false, fmt.Errorf("start matching release hydration: lookup descriptors are invalid")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return err
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return err
		}
		var source matchingAcquisitionSourceFence
		var fenceVersion int
		if err := tx.NewRaw(`SELECT run.provider_source_id, run.provider_configuration_identity, source.provider_id, source.endpoint, run.fence_version
			FROM matching_run run JOIN provider_source source ON source.id=run.provider_source_id WHERE run.id=?`, runID).Scan(ctx,
			&source.SourceID, &source.Identity, &source.ProviderID, &source.Endpoint, &fenceVersion); err != nil {
			return err
		}
		var configIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configIdentity); err != nil || configIdentity != source.Identity {
			return fmt.Errorf("start matching release hydration: provider configuration changed")
		}
		var sourceIdentity string
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? FOR SHARE`, source.SourceID).Scan(ctx, &sourceIdentity); err != nil || sourceIdentity != source.Identity {
			return fmt.Errorf("start matching release hydration: provider source configuration changed")
		}
		// Target groups/generations are the outer row-lock fence for matching
		// delivery work. Acquire them before locking acquisition, operation, or
		// run rows so a hydration wake cannot invert the claim lock order.
		var lockedGroups []uuid.UUID
		if err := tx.NewRaw(`SELECT generation.group_id FROM incoming_matching_generation generation
			JOIN matching_run_target target ON target.group_id=generation.group_id AND target.run_id=?
			JOIN incoming_group current_group ON current_group.id=target.group_id
			ORDER BY generation.group_id FOR UPDATE OF generation, current_group`, runID).Scan(ctx, &lockedGroups); err != nil {
			return err
		}
		var currentSource matchingAcquisitionSourceFence
		if err := tx.NewRaw(`SELECT run.provider_source_id, run.provider_configuration_identity, source.provider_id, source.endpoint
			FROM matching_run run JOIN provider_source source ON source.id=run.provider_source_id WHERE run.id=?`, runID).Scan(ctx, &currentSource); err != nil {
			return err
		}
		if currentSource != source {
			return nil
		}
		var acquisition struct {
			Phase    string `bun:"current_phase"`
			Revision int64  `bun:"revision"`
		}
		if err := tx.NewRaw(`SELECT current_phase, revision FROM matching_acquisition WHERE run_id=? FOR UPDATE`, runID).Scan(ctx, &acquisition); err != nil {
			return err
		}
		if acquisition.Phase != "catalog_ready" || acquisition.Revision != expectedRevision {
			return nil
		}
		var stale bool
		switch fenceVersion {
		case 2:
			if err := validateMatchingRunFence(ctx, tx, runID, fenceVersion); err != nil {
				return err
			}
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM matching_run_target target
				LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
				LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
				WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
					generation.high_water IS DISTINCT FROM target.target_generation OR current_group.revision IS DISTINCT FROM target.captured_group_revision))`, runID).Scan(ctx, &stale); err != nil {
				return err
			}
		case 1:
			if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM matching_run_target target
			LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
			LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
			WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
				generation.high_water IS DISTINCT FROM target.target_generation OR current_group.revision IS DISTINCT FROM target.captured_group_revision))
			OR EXISTS (SELECT 1 FROM incoming_grouping_state WHERE singleton=true AND needs_refresh=true)`, runID).Scan(ctx, &stale); err != nil {
				return err
			}
		default:
			return fmt.Errorf("start matching release hydration: unsupported fence version %d", fenceVersion)
		}
		if stale {
			return fmt.Errorf("start matching release hydration: captured targets are stale")
		}
		_, catalogIDs, err := matchingAcquisitionCatalogManifest(ctx, tx, runID)
		if err != nil {
			return err
		}
		seen := make(map[string]bool, len(input.ReleaseIDs))
		seenOperations := make(map[uuid.UUID]bool, len(children))
		seenCacheKeys := make(map[string]bool, len(children))
		for index, id := range input.ReleaseIDs {
			parsed, parseErr := uuid.Parse(id)
			if parseErr != nil || parsed.String() != id || seen[id] {
				return fmt.Errorf("start matching release hydration: release ids must be canonical and unique")
			}
			if index > 0 && input.ReleaseIDs[index-1] >= id {
				return fmt.Errorf("start matching release hydration: shortlist must be canonically ordered")
			}
			if _, ok := catalogIDs[id]; !ok {
				return fmt.Errorf("start matching release hydration: selected release is outside accepted catalog")
			}
			seen[id] = true
			child := children[index].Operation
			if child == nil || child.Kind != "provider_release_lookup" || child.ProviderSourceID == nil || *child.ProviderSourceID != source.SourceID || child.ProviderConfigurationIdentity == nil || *child.ProviderConfigurationIdentity != source.Identity || child.ProviderID == nil || *child.ProviderID != source.ProviderID || child.ProviderEndpoint != source.Endpoint || child.ProviderCacheKey == nil || *child.ProviderCacheKey != "release-lookup:"+id || child.ProviderExplicitRefresh {
				return fmt.Errorf("start matching release hydration: lookup child fence is invalid")
			}
			if child.ID == uuid.Nil || seenOperations[child.ID] || seenCacheKeys[*child.ProviderCacheKey] {
				return fmt.Errorf("start matching release hydration: duplicate lookup child")
			}
			seenOperations[child.ID] = true
			seenCacheKeys[*child.ProviderCacheKey] = true
			if output.Lookups[index].ReleaseID != id || output.Lookups[index].RequestID != child.ID || output.Lookups[index].CacheKey != *child.ProviderCacheKey {
				return fmt.Errorf("start matching release hydration: lookup descriptor differs from prepared child")
			}
			var intent struct {
				MBID string `json:"mbid"`
			}
			if json.Unmarshal(child.InputSnapshot, &intent) != nil || !strings.EqualFold(intent.MBID, id) || children[index].Args == nil || children[index].Args.Kind() != "provider_fetch_v1" {
				return fmt.Errorf("start matching release hydration: lookup child identity is invalid")
			}
			var args struct {
				OperationID uuid.UUID `json:"operation_id"`
			}
			encodedArgs, err := json.Marshal(children[index].Args)
			if err != nil || json.Unmarshal(encodedArgs, &args) != nil || args.OperationID != child.ID {
				return fmt.Errorf("start matching release hydration: lookup child delivery identity is invalid")
			}
		}
		var operation *Operation
		operation, err = repository.GetOperationForUpdate(ctx, tx, runID)
		if err != nil {
			return err
		}
		if operation.Kind != "matching_evaluation_v1" || (delivery == nil && operation.State != "running" && operation.State != "queued") || (delivery != nil && (operation.State != "running" || operation.Attempt != delivery.attempt || operation.RiverJobID == nil || *operation.RiverJobID != delivery.riverJobID)) {
			return fmt.Errorf("start matching release hydration: parent operation is not waiting for policy")
		}
		var runState string
		if err := tx.NewRaw(`SELECT state FROM matching_run WHERE id=? FOR UPDATE`, runID).Scan(ctx, &runState); err != nil {
			return err
		}
		if delivery == nil {
			if runState != "waiting_policy" {
				return fmt.Errorf("start matching release hydration: matching run is not waiting for policy")
			}
		} else if runState != "running" {
			return nil
		}
		if delivery != nil {
			var run matchingClaimRun
			if err := tx.NewRaw(`SELECT state, execution_epoch, active_river_job_id, active_river_attempt FROM matching_run WHERE id=? FOR UPDATE`, runID).Scan(ctx, &run); err != nil {
				return err
			}
			if run.State != "running" || run.ExecutionEpoch != delivery.claim.ExecutionEpoch || run.ActiveRiverJobID == nil || *run.ActiveRiverJobID != delivery.riverJobID || run.ActiveRiverAttempt == nil || *run.ActiveRiverAttempt != delivery.claim.RiverAttempt {
				return nil
			}
			var registered bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_delivery WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL)`, runID, delivery.riverJobID, delivery.attempt).Scan(ctx, &registered); err != nil {
				return err
			}
			if !registered {
				return nil
			}
		}
		var pending int
		if err := tx.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND terminal_outcome IS NULL`, runID).Scan(ctx, &pending); err != nil {
			return err
		}
		if pending != 0 {
			return fmt.Errorf("start matching release hydration: previous provider children are still pending")
		}
		if delivery != nil {
			result, err := tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL`, time.Now().UTC(), runID, delivery.riverJobID, delivery.attempt).Exec(ctx)
			if err != nil {
				return err
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				return nil
			}
		}
		for _, child := range children {
			if err := repository.createOperationAndEnqueueTx(ctx, tx, child.Operation, client, child.Args, nil); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO matching_provider_dependency(parent_run_id, request_id, cache_key) VALUES (?, ?, ?)`, runID, child.Operation.ID, *child.Operation.ProviderCacheKey).Exec(ctx); err != nil {
				return err
			}
		}
		result, err := tx.NewRaw(`UPDATE matching_acquisition SET current_phase=?, revision=? WHERE run_id=? AND revision=? AND current_phase='catalog_ready'`, phase.Phase, phase.Revision, runID, expectedRevision).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return fmt.Errorf("start matching release hydration: acquisition revision changed during admission")
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id, revision, phase, input, output, response_provenance) VALUES (?, ?, ?, ?::jsonb, ?::jsonb, '[]'::jsonb)`, runID, phase.Revision, phase.Phase, string(phase.Input), string(phase.Output)).Exec(ctx); err != nil {
			return err
		}
		now := time.Now().UTC()
		var runUpdate string
		if delivery == nil {
			runUpdate = `UPDATE matching_run SET state='waiting_provider', updated_at=? WHERE id=?`
		} else {
			runUpdate = `UPDATE matching_run SET state='waiting_provider', active_river_job_id=NULL, active_river_attempt=NULL, active_execution_epoch=NULL, updated_at=? WHERE id=? AND state='running' AND execution_epoch=? AND active_river_job_id=? AND active_river_attempt=?`
		}
		var runResult sql.Result
		if delivery == nil {
			runResult, err = tx.NewRaw(runUpdate, now, runID).Exec(ctx)
		} else {
			runResult, err = tx.NewRaw(runUpdate, now, runID, delivery.claim.ExecutionEpoch, delivery.riverJobID, delivery.claim.RiverAttempt).Exec(ctx)
		}
		if err != nil {
			return err
		}
		if delivery != nil {
			if affected, err := runResult.RowsAffected(); err != nil || affected != 1 {
				return fmt.Errorf("start matching release hydration: delivery claim changed during admission")
			}
		}
		operation.Stage, operation.UpdatedAt = "waiting_provider", now
		if _, err := tx.NewUpdate().Model(operation).Column("stage", "updated_at").WherePK().Exec(ctx); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

type MatchingAcquisitionIntent struct {
	SchemaVersion          int                                  `json:"schema_version"`
	FenceVersion           int                                  `json:"fence_version,omitempty"`
	GroupingEpoch          int64                                `json:"grouping_epoch"`
	PlanDigest             string                               `json:"evaluation_plan_digest"`
	CaptureDigest          string                               `json:"capture_digest"`
	CapturedMembers        []MatchingAcquisitionCapturedMember  `json:"captured_members"`
	PolicyVersion          string                               `json:"policy_version"`
	TargetCaptureDigest    string                               `json:"target_capture_digest,omitempty"`
	QueryDescriptor        json.RawMessage                      `json:"query_descriptor,omitempty"`
	ArtistQueryPlanVersion int                                  `json:"artist_query_plan_version,omitempty"`
	FallbackQueryPlan      json.RawMessage                      `json:"fallback_query_plan,omitempty"`
	Targets                []MatchingTarget                     `json:"targets"`
	InitialProviderInputs  []MatchingAcquisitionProviderRequest `json:"initial_provider_inputs"`
}

// MatchingAcquisitionCapturedMember is the exact persisted metadata snapshot
// attached to one selected group member at admission. It is intentionally
// neutral data: later matching stages must not reread source files or metadata.
type MatchingAcquisitionCapturedMember struct {
	MemberID            uuid.UUID       `json:"member_id"`
	WorkID              uuid.UUID       `json:"work_id"`
	CaptureAnalysisID   *uuid.UUID      `json:"capture_analysis_id,omitempty"`
	AnalysisInputSHA256 string          `json:"analysis_input_sha256,omitempty"`
	Tags                json.RawMessage `json:"tags"`
}

type MatchingAcquisitionProviderRequest struct {
	OperationID uuid.UUID       `json:"operation_id"`
	Kind        string          `json:"kind"`
	CacheKey    string          `json:"cache_key"`
	Input       json.RawMessage `json:"input"`
}

type matchingAcquisitionSourceFence struct {
	SourceID   uuid.UUID `bun:"provider_source_id"`
	Identity   string    `bun:"provider_configuration_identity"`
	ProviderID uuid.UUID `bun:"provider_id"`
	Endpoint   string    `bun:"endpoint"`
}

// MatchingAcquisitionPhase is an append-only record of one response-driven
// acquisition transition. Its child requests are prepared, typed operations;
// persistence admits them only after validating their captured provider fences.
type MatchingAcquisitionPhase struct {
	Revision           int64
	Phase              string
	Input              json.RawMessage
	Output             json.RawMessage
	ResponseProvenance json.RawMessage
}

type MatchingAcquisitionSnapshot struct {
	Intent   MatchingAcquisitionIntent
	Revision int64
	Phase    string
	Input    json.RawMessage
	Output   json.RawMessage
	// FailedProviderDependency reports whether any provider child registered to
	// this run settled with a terminal failure. It is a read-only ledger check:
	// a failed dependency must fence the parent, never be read as success.
	FailedProviderDependency bool
}

func (repository *SetupManagerRepository) GetMatchingAcquisition(ctx context.Context, runID uuid.UUID) (MatchingAcquisitionSnapshot, bool, error) {
	var snapshot MatchingAcquisitionSnapshot
	var intent, intentDigest, planDigest, planBytes []byte
	var intentVersion int
	err := repository.db.NewRaw(`SELECT acquisition.intent, acquisition.revision, acquisition.current_phase,
		phase.input, phase.output,
		EXISTS (SELECT 1 FROM matching_provider_dependency dependency
			WHERE dependency.parent_run_id=acquisition.run_id AND dependency.terminal_outcome='failed'),
		acquisition.intent_digest, run.plan_digest, run.immutable_plan, acquisition.intent_version
		FROM matching_acquisition acquisition
		JOIN matching_run run ON run.id=acquisition.run_id
		JOIN matching_acquisition_phase phase ON phase.run_id=acquisition.run_id AND phase.revision=acquisition.revision
		WHERE acquisition.run_id=?`, runID).Scan(ctx, &intent, &snapshot.Revision, &snapshot.Phase, &snapshot.Input, &snapshot.Output,
		&snapshot.FailedProviderDependency, &intentDigest, &planDigest, &planBytes, &intentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return MatchingAcquisitionSnapshot{}, false, nil
	}
	if err != nil {
		return MatchingAcquisitionSnapshot{}, false, fmt.Errorf("read matching acquisition: %w", err)
	}
	if err := json.Unmarshal(intent, &snapshot.Intent); err != nil || intentVersion != snapshot.Intent.SchemaVersion {
		return MatchingAcquisitionSnapshot{}, false, fmt.Errorf("read matching acquisition: saved intent is invalid")
	}
	if err := repository.validateMatchingAcquisitionRead(ctx, runID, snapshot.Intent, intentDigest, planDigest, planBytes); err != nil {
		return MatchingAcquisitionSnapshot{}, false, fmt.Errorf("read matching acquisition: %w", err)
	}
	return snapshot, true, nil
}

// GetMatchingAcquisitionCatalogCacheKeys returns the exact accepted response
// keys recorded by the durable artist catalog phases.
func (repository *SetupManagerRepository) GetMatchingAcquisitionCatalogCacheKeys(ctx context.Context, runID uuid.UUID) ([]string, error) {
	var rows []struct {
		Provenance []byte `bun:"response_provenance"`
	}
	if err := repository.db.NewRaw(`SELECT response_provenance FROM matching_acquisition_phase WHERE run_id=? AND phase IN ('artist_catalog_page','catalog_ready') ORDER BY revision`, runID).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("read matching acquisition catalog response keys: %w", err)
	}
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		var provenance []matchingAcquisitionResponseProvenance
		if json.Unmarshal(row.Provenance, &provenance) != nil || len(provenance) != 1 || provenance[0].CacheKey == "" {
			return nil, fmt.Errorf("read matching acquisition catalog response keys: provenance is invalid")
		}
		keys = append(keys, provenance[0].CacheKey)
	}
	sort.Strings(keys)
	return keys, nil
}

func matchingAcquisitionIntentFromAdmission(admission MatchingAdmission, planDigest [32]byte, captured []MatchingAcquisitionCapturedMember) (MatchingAcquisitionIntent, error) {
	if len(admission.Providers) != 1 || admission.Providers[0].Operation == nil || admission.Providers[0].Operation.Kind != "provider_artist_search" {
		return MatchingAcquisitionIntent{}, fmt.Errorf("matching acquisition currently requires exactly one initial artist search")
	}
	initial := admission.Providers[0].Operation
	var search struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}
	if initial.ProviderCacheKey == nil || json.Unmarshal(initial.InputSnapshot, &search) != nil || search.SchemaVersion != 1 ||
		strings.TrimSpace(search.Query) == "" || search.Offset != 0 || search.Limit < 1 || search.Limit > 100 ||
		*initial.ProviderCacheKey != fmt.Sprintf("artist-search:0:%d:%s", search.Limit, strings.TrimSpace(search.Query)) {
		return MatchingAcquisitionIntent{}, fmt.Errorf("matching acquisition initial artist search intent is invalid")
	}
	targets := append([]MatchingTarget(nil), admission.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].GroupID.String() < targets[j].GroupID.String() })
	captured = append([]MatchingAcquisitionCapturedMember(nil), captured...)
	sort.Slice(captured, func(i, j int) bool { return captured[i].MemberID.String() < captured[j].MemberID.String() })
	captured, err := normalizeCapturedMatchingMembers(captured)
	if err != nil {
		return MatchingAcquisitionIntent{}, fmt.Errorf("canonicalize matching metadata capture: %w", err)
	}
	captureDigest, err := matchingMetadataCaptureDigest(captured)
	if err != nil {
		return MatchingAcquisitionIntent{}, fmt.Errorf("digest matching metadata capture: %w", err)
	}
	intent := MatchingAcquisitionIntent{
		SchemaVersion: matchingAcquisitionIntentVersion, GroupingEpoch: admission.GroupingEpoch,
		PlanDigest: fmt.Sprintf("%x", planDigest[:]), CaptureDigest: captureDigest,
		CapturedMembers: captured, PolicyVersion: admission.PolicyVersion,
		Targets:               targets,
		InitialProviderInputs: make([]MatchingAcquisitionProviderRequest, 0, len(admission.Providers)),
	}
	for _, provider := range admission.Providers {
		if provider.Operation == nil || provider.Operation.ProviderCacheKey == nil {
			return MatchingAcquisitionIntent{}, fmt.Errorf("matching admission: acquisition provider input is incomplete")
		}
		intent.InitialProviderInputs = append(intent.InitialProviderInputs, MatchingAcquisitionProviderRequest{
			OperationID: provider.Operation.ID, Kind: provider.Operation.Kind,
			CacheKey: *provider.Operation.ProviderCacheKey,
			Input:    append(json.RawMessage(nil), provider.Operation.InputSnapshot...),
		})
	}
	return intent, nil
}

// GetMatchingAcquisitionMetadataCapture returns the immutable metadata captured
// at admission only when the durable acquisition is in the requested phase.
func (repository *SetupManagerRepository) GetMatchingAcquisitionMetadataCapture(ctx context.Context, runID uuid.UUID, expectedPhase string) ([]MatchingAcquisitionCapturedMember, string, bool, error) {
	if runID == uuid.Nil || expectedPhase == "" {
		return nil, "", false, fmt.Errorf("read matching metadata capture: run and expected phase are required")
	}
	var intentBytes, intentDigest, planDigest, planBytes []byte
	var intentVersion int
	var phase string
	err := repository.db.NewRaw(`SELECT acquisition.intent, acquisition.intent_digest, acquisition.current_phase,
		run.plan_digest, run.immutable_plan, acquisition.intent_version
		FROM matching_acquisition acquisition
		JOIN matching_run run ON run.id=acquisition.run_id
		WHERE acquisition.run_id=?`, runID).Scan(ctx, &intentBytes, &intentDigest, &phase, &planDigest, &planBytes, &intentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("read matching metadata capture: %w", err)
	}
	if phase != expectedPhase {
		return nil, "", false, fmt.Errorf("read matching metadata capture: acquisition phase changed")
	}
	var intent MatchingAcquisitionIntent
	if err := json.Unmarshal(intentBytes, &intent); err != nil || !supportedMatchingAcquisitionIntentVersion(intent.SchemaVersion) ||
		intentVersion != intent.SchemaVersion || intent.CaptureDigest == "" {
		return nil, "", false, fmt.Errorf("read matching metadata capture: saved capture is invalid")
	}
	if err := repository.validateMatchingAcquisitionRead(ctx, runID, intent, intentDigest, planDigest, planBytes); err != nil {
		return nil, "", false, fmt.Errorf("read matching metadata capture: %w", err)
	}
	return append([]MatchingAcquisitionCapturedMember(nil), intent.CapturedMembers...), intent.CaptureDigest, true, nil
}

// validateMatchingAcquisitionRead verifies only durable, immutable run inputs.
// It deliberately does not inspect mutable grouping/source tables: readers must
// continue to expose the original capture even when a later fence will reject it.
func (repository *SetupManagerRepository) validateMatchingAcquisitionRead(
	ctx context.Context,
	runID uuid.UUID,
	intent MatchingAcquisitionIntent,
	intentDigest, planDigest, planBytes []byte,
) error {
	if err := validateCanonicalMatchingAcquisitionIntent(intent, intentDigest, planDigest, planBytes); err != nil {
		return err
	}
	type immutableTarget struct {
		GroupID       uuid.UUID       `bun:"group_id"`
		Revision      string          `bun:"captured_group_revision"`
		Capture       json.RawMessage `bun:"target_capture"`
		CaptureDigest []byte          `bun:"target_capture_digest"`
	}
	var stored []immutableTarget
	if err := repository.db.NewRaw(`SELECT group_id,captured_group_revision,target_capture,target_capture_digest
		FROM matching_run_target WHERE run_id=? ORDER BY group_id`, runID).Scan(ctx, &stored); err != nil {
		return fmt.Errorf("read immutable targets: %w", err)
	}
	targets := make([]MatchingTarget, 0, len(stored))
	for _, target := range stored {
		targets = append(targets, MatchingTarget{GroupID: target.GroupID, Revision: target.Revision})
	}
	if !equalMatchingTargets(intent.Targets, targets) {
		return fmt.Errorf("saved targets differ from immutable run targets")
	}
	if intent.SchemaVersion != matchingAutomaticAcquisitionIntentVersion {
		return nil
	}
	if intent.FenceVersion != 2 || len(stored) != 1 || len(stored[0].Capture) == 0 || len(stored[0].CaptureDigest) != sha256.Size {
		return fmt.Errorf("version-2 immutable target capture is missing")
	}
	canonicalCapture, captureHash, err := automaticArtistTargetCaptureDigest(stored[0].Capture)
	if err != nil || !equalDigest(captureHash[:], stored[0].CaptureDigest) || fmt.Sprintf("%x", captureHash[:]) != intent.TargetCaptureDigest {
		return fmt.Errorf("immutable target capture digest changed")
	}
	var capture automaticArtistTargetCapture
	if json.Unmarshal(canonicalCapture, &capture) != nil || capture.SchemaVersion != 2 ||
		capture.GroupID != stored[0].GroupID || capture.GroupRevision != stored[0].Revision {
		return fmt.Errorf("immutable target capture is invalid")
	}
	return nil
}

func validateCanonicalMatchingAcquisitionIntent(intent MatchingAcquisitionIntent, intentDigest, planDigest, planBytes []byte) error {
	if !supportedMatchingAcquisitionIntentVersion(intent.SchemaVersion) {
		return fmt.Errorf("saved intent schema version is unsupported")
	}
	canonicalIntent := intent
	var err error
	canonicalIntent.CapturedMembers, err = normalizeCapturedMatchingMembers(intent.CapturedMembers)
	if err != nil {
		return fmt.Errorf("saved capture is invalid")
	}
	canonicalIntent.InitialProviderInputs = append([]MatchingAcquisitionProviderRequest(nil), intent.InitialProviderInputs...)
	for index := range canonicalIntent.InitialProviderInputs {
		request := &canonicalIntent.InitialProviderInputs[index]
		if intent.SchemaVersion == matchingAutomaticFallbackAcquisitionIntentVersion {
			if (request.Kind != "provider_release_search" && request.Kind != "provider_recording_search") || request.CacheKey == "" {
				return fmt.Errorf("saved initial provider input is invalid")
			}
		} else if request.Kind != "provider_artist_search" {
			return fmt.Errorf("saved initial provider input is invalid")
		}
		canonicalInput, err := canonicalMatchingJSON(request.Input)
		if err != nil {
			return fmt.Errorf("saved initial provider input is invalid")
		}
		request.Input = canonicalInput
	}
	encodedIntent, err := canonicalMatchingAcquisitionIntentBytes(canonicalIntent)
	if err != nil {
		return fmt.Errorf("saved intent is invalid")
	}
	actualIntentDigest := sha256.Sum256(encodedIntent)
	if !equalDigest(intentDigest, actualIntentDigest[:]) {
		return fmt.Errorf("saved intent digest changed")
	}
	actualCaptureDigest, err := matchingMetadataCaptureDigest(canonicalIntent.CapturedMembers)
	if err != nil || intent.CaptureDigest != actualCaptureDigest {
		return fmt.Errorf("saved capture digest changed")
	}
	canonicalPlan, err := canonicalMatchingJSON(planBytes)
	if err != nil {
		return fmt.Errorf("immutable matching plan is invalid")
	}
	actualPlanDigest := sha256.Sum256(canonicalPlan)
	if !equalDigest(planDigest, actualPlanDigest[:]) || intent.PlanDigest != fmt.Sprintf("%x", actualPlanDigest[:]) {
		return fmt.Errorf("saved intent does not match immutable matching plan")
	}
	var planEnvelope struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(planBytes, &planEnvelope); err != nil {
		return fmt.Errorf("immutable matching plan is invalid")
	}
	switch planEnvelope.SchemaVersion {
	case 1:
		if intent.SchemaVersion != matchingAcquisitionIntentVersion || intent.FenceVersion != 0 {
			return fmt.Errorf("saved intent scope differs from immutable matching plan")
		}
	case 2, 3:
		var plan matchingPublisherPlan
		if intent.SchemaVersion != matchingAcquisitionIntentVersion || intent.FenceVersion != 0 ||
			json.Unmarshal(planBytes, &plan) != nil || plan.GroupingEpoch != intent.GroupingEpoch ||
			plan.PolicyVersion != intent.PolicyVersion || !equalMatchingTargets(plan.Targets, intent.Targets) {
			return fmt.Errorf("saved intent scope differs from immutable matching plan")
		}
	case 4:
		var plan struct {
			SchemaVersion          int             `json:"schema_version"`
			Intent                 string          `json:"intent"`
			Mode                   string          `json:"mode"`
			TargetCaptureDigest    string          `json:"target_capture_digest"`
			PolicyVersion          string          `json:"policy_version"`
			ArtistQueryPlanVersion int             `json:"artist_query_plan_version"`
			QueryDescriptor        json.RawMessage `json:"query_descriptor"`
		}
		if json.Unmarshal(planBytes, &plan) != nil || intent.SchemaVersion != 2 || intent.FenceVersion != 2 ||
			plan.Intent != "automatic_artist_acquisition" || plan.Mode != "automatic" ||
			intent.TargetCaptureDigest == "" || intent.TargetCaptureDigest != plan.TargetCaptureDigest ||
			intent.PolicyVersion != plan.PolicyVersion || intent.ArtistQueryPlanVersion != plan.ArtistQueryPlanVersion ||
			len(intent.QueryDescriptor) == 0 || len(plan.QueryDescriptor) == 0 {
			return fmt.Errorf("saved intent scope differs from immutable matching plan")
		}
		intentQuery, intentQueryErr := canonicalMatchingJSON(intent.QueryDescriptor)
		planQuery, planQueryErr := canonicalMatchingJSON(plan.QueryDescriptor)
		if intentQueryErr != nil || planQueryErr != nil || !equalDigest(intentQuery, planQuery) {
			return fmt.Errorf("saved intent query differs from immutable matching plan")
		}
	case 5:
		var plan struct {
			Intent              string          `json:"intent"`
			Mode                string          `json:"mode"`
			TargetCaptureDigest string          `json:"target_capture_digest"`
			MetadataDigest      string          `json:"metadata_capture_digest"`
			PolicyVersion       string          `json:"policy_version"`
			FallbackQueryPlan   json.RawMessage `json:"fallback_query_plan"`
		}
		if json.Unmarshal(planBytes, &plan) != nil || intent.SchemaVersion != matchingAutomaticFallbackAcquisitionIntentVersion || intent.FenceVersion != 2 ||
			plan.Intent != "automatic_fallback_acquisition" || plan.Mode != "automatic" ||
			intent.TargetCaptureDigest == "" || intent.TargetCaptureDigest != plan.TargetCaptureDigest ||
			intent.CaptureDigest != plan.MetadataDigest || intent.PolicyVersion != plan.PolicyVersion || len(intent.FallbackQueryPlan) == 0 || len(plan.FallbackQueryPlan) == 0 {
			return fmt.Errorf("saved intent scope differs from immutable matching plan")
		}
		intentPlan, intentPlanErr := policy.CanonicalFallbackQueryPlan(intent.FallbackQueryPlan)
		planPlan, planPlanErr := policy.CanonicalFallbackQueryPlan(plan.FallbackQueryPlan)
		if intentPlanErr != nil || planPlanErr != nil || !equalDigest(intentPlan, planPlan) {
			return fmt.Errorf("saved intent fallback plan differs from immutable plan")
		}
		var typedPlan policy.TypedFallbackQueryPlan
		if json.Unmarshal(intentPlan, &typedPlan) != nil || (len(typedPlan.Stages) == 0 && len(intent.InitialProviderInputs) != 0) || (len(typedPlan.Stages) > 0 && len(intent.InitialProviderInputs) != 1) {
			return fmt.Errorf("saved fallback initial provider scope is invalid")
		}
		if len(typedPlan.Stages) > 0 && validateFallbackProviderInput(typedPlan.Stages[0], &Operation{Kind: intent.InitialProviderInputs[0].Kind, InputSnapshot: intent.InitialProviderInputs[0].Input, ProviderCacheKey: fallbackStringPointer(intent.InitialProviderInputs[0].CacheKey)}) != nil {
			return fmt.Errorf("saved fallback initial provider scope is invalid")
		}
	default:
		return fmt.Errorf("immutable matching plan version is unsupported")
	}
	return nil
}

func fallbackStringPointer(value string) *string { return &value }

func supportedMatchingAcquisitionIntentVersion(version int) bool {
	return version == matchingAcquisitionIntentVersion || version == matchingAutomaticAcquisitionIntentVersion || version == matchingAutomaticFallbackAcquisitionIntentVersion
}

func canonicalMatchingAcquisitionIntentBytes(intent MatchingAcquisitionIntent) ([]byte, error) {
	if len(intent.QueryDescriptor) != 0 {
		canonicalQuery, err := canonicalMatchingJSON(intent.QueryDescriptor)
		if err != nil {
			return nil, err
		}
		intent.QueryDescriptor = canonicalQuery
	}
	if len(intent.FallbackQueryPlan) != 0 {
		canonicalPlan, err := policy.CanonicalFallbackQueryPlan(intent.FallbackQueryPlan)
		if err != nil {
			return nil, err
		}
		intent.FallbackQueryPlan = canonicalPlan
	}
	intent.CapturedMembers = append([]MatchingAcquisitionCapturedMember(nil), intent.CapturedMembers...)
	for index := range intent.CapturedMembers {
		canonicalTags, err := canonicalMatchingJSON(intent.CapturedMembers[index].Tags)
		if err != nil {
			return nil, err
		}
		intent.CapturedMembers[index].Tags = canonicalTags
	}
	intent.InitialProviderInputs = append([]MatchingAcquisitionProviderRequest(nil), intent.InitialProviderInputs...)
	for index := range intent.InitialProviderInputs {
		canonicalInput, err := canonicalMatchingJSON(intent.InitialProviderInputs[index].Input)
		if err != nil {
			return nil, err
		}
		intent.InitialProviderInputs[index].Input = canonicalInput
	}
	return json.Marshal(intent)
}

func canonicalMatchingJSON(value json.RawMessage) (json.RawMessage, error) {
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("JSON has trailing data")
	}
	encoded, err := json.Marshal(decoded)
	return encoded, err
}

func equalMatchingTargets(left, right []MatchingTarget) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func createMatchingAcquisitionLedger(ctx context.Context, tx bun.Tx, runID uuid.UUID, intent MatchingAcquisitionIntent) error {
	encoded, err := canonicalMatchingAcquisitionIntentBytes(intent)
	if err != nil {
		return fmt.Errorf("encode matching acquisition intent: %w", err)
	}
	digest := sha256.Sum256(encoded)
	if _, err := tx.NewRaw(`INSERT INTO matching_acquisition(run_id, intent_version, intent, intent_digest, current_phase)
		VALUES (?, ?, ?::jsonb, ?, 'admitted')`, runID, intent.SchemaVersion, string(encoded), digest[:]).Exec(ctx); err != nil {
		return err
	}
	input, _ := json.Marshal(map[string]string{"intent_digest": fmt.Sprintf("%x", digest[:])})
	if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id, revision, phase, input, output, response_provenance)
		VALUES (?, 0, 'admitted', ?::jsonb, '{"state":"admitted"}'::jsonb, '[]'::jsonb)`, runID, string(input)).Exec(ctx); err != nil {
		return err
	}
	return nil
}

// AdvanceMatchingAcquisitionForDelivery atomically appends an evidence-backed
// phase, admits its prepared provider children, registers their dependencies,
// and consumes the current parent delivery into waiting_provider. A failed
// River insert rolls back every part of that transition.
func (repository *SetupManagerRepository) AdvanceMatchingAcquisitionForDelivery(
	ctx context.Context,
	client RiverInserter,
	operationID uuid.UUID,
	attempt int,
	riverJobID int64,
	claim MatchingDeliveryClaim,
	expectedRevision int64,
	phase MatchingAcquisitionPhase,
	children []MatchingProviderRequest,
) (bool, error) {
	if client == nil || operationID == uuid.Nil || attempt < 1 || riverJobID < 1 || claim.ExecutionEpoch < 1 || claim.RiverAttempt < 1 ||
		expectedRevision < 0 || phase.Revision != expectedRevision+1 || phase.Phase == "" {
		return false, fmt.Errorf("advance matching acquisition: invalid transition fence")
	}
	if err := validateAcquisitionJSON(phase.Input, "input"); err != nil {
		return false, fmt.Errorf("advance matching acquisition: %w", err)
	}
	if err := validateAcquisitionJSON(phase.Output, "output"); err != nil {
		return false, fmt.Errorf("advance matching acquisition: %w", err)
	}
	if len(phase.ResponseProvenance) == 0 || !json.Valid(phase.ResponseProvenance) || phase.ResponseProvenance[0] != '[' {
		return false, fmt.Errorf("advance matching acquisition: response provenance must be a JSON array")
	}
	var provenance []matchingAcquisitionResponseProvenance
	if err := json.Unmarshal(phase.ResponseProvenance, &provenance); err != nil {
		return false, fmt.Errorf("advance matching acquisition: decode response provenance: %w", err)
	}
	if len(provenance) != 1 {
		return false, fmt.Errorf("advance matching acquisition: exactly one accepted page response is required")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("advance matching acquisition: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("advance matching acquisition: lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("advance matching acquisition: lock provider configuration: %w", err)
		}
		var sourceFence matchingAcquisitionSourceFence
		var fenceVersion int
		if err := tx.NewRaw(`SELECT run.provider_source_id, run.provider_configuration_identity, source.provider_id, source.endpoint, run.fence_version
			FROM matching_run run JOIN provider_source source ON source.id=run.provider_source_id WHERE run.id=?`, operationID).Scan(ctx,
			&sourceFence.SourceID, &sourceFence.Identity, &sourceFence.ProviderID, &sourceFence.Endpoint, &fenceVersion); err != nil {
			return fmt.Errorf("advance matching acquisition: read run source fence: %w", err)
		}
		var configIdentity, sourceIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configIdentity); err != nil || configIdentity != sourceFence.Identity {
			return nil
		}
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? FOR SHARE`, sourceFence.SourceID).Scan(ctx, &sourceIdentity); err != nil || sourceIdentity != sourceFence.Identity {
			return nil
		}
		var lockedGroups []uuid.UUID
		if err := tx.NewRaw(`SELECT generation.group_id FROM incoming_matching_generation generation
			JOIN matching_run_target target ON target.group_id=generation.group_id AND target.run_id=?
			JOIN incoming_group current_group ON current_group.id=target.group_id
			ORDER BY generation.group_id FOR UPDATE OF generation, current_group`, operationID).Scan(ctx, &lockedGroups); err != nil {
			return fmt.Errorf("advance matching acquisition: lock target groups: %w", err)
		}
		var stale bool
		switch fenceVersion {
		case 2:
			if err := validateMatchingRunFence(ctx, tx, operationID, fenceVersion); err != nil {
				return err
			}
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM matching_run_target target
				LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
				LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
				WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
					generation.high_water IS DISTINCT FROM target.target_generation OR
					current_group.revision IS DISTINCT FROM target.captured_group_revision))`, operationID).Scan(ctx, &stale); err != nil {
				return fmt.Errorf("advance matching acquisition: validate target generations: %w", err)
			}
		case 1:
			if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM matching_run_target target
			LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
			LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
			WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
				generation.high_water IS DISTINCT FROM target.target_generation OR
				current_group.revision IS DISTINCT FROM target.captured_group_revision)
			) OR EXISTS (SELECT 1 FROM incoming_grouping_state WHERE singleton=true AND needs_refresh=true)`, operationID).Scan(ctx, &stale); err != nil {
				return fmt.Errorf("advance matching acquisition: validate target generations: %w", err)
			}
		default:
			return fmt.Errorf("advance matching acquisition: unsupported fence version %d", fenceVersion)
		}
		if stale {
			return nil
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		if operation.Kind != "matching_evaluation_v1" || operation.State != "running" || operation.Attempt != attempt || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
			return nil
		}
		var run struct {
			State                string `bun:"state"`
			ExecutionEpoch       int64  `bun:"execution_epoch"`
			ActiveRiverJobID     *int64 `bun:"active_river_job_id"`
			ActiveRiverAttempt   *int   `bun:"active_river_attempt"`
			ActiveExecutionEpoch *int64 `bun:"active_execution_epoch"`
		}
		if err := tx.NewRaw(`SELECT state, execution_epoch, active_river_job_id, active_river_attempt, active_execution_epoch
			FROM matching_run WHERE id=? FOR NO KEY UPDATE`, operationID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("advance matching acquisition: lock run: %w", err)
		}
		if run.State != "running" || run.ExecutionEpoch != claim.ExecutionEpoch || run.ActiveRiverJobID == nil || *run.ActiveRiverJobID != riverJobID ||
			run.ActiveRiverAttempt == nil || *run.ActiveRiverAttempt != claim.RiverAttempt || run.ActiveExecutionEpoch == nil || *run.ActiveExecutionEpoch != claim.ExecutionEpoch {
			return nil
		}
		var registered bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_delivery WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL)`, operationID, riverJobID, attempt).Scan(ctx, &registered); err != nil {
			return err
		}
		if !registered {
			return nil
		}
		var pending int
		if err := tx.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND terminal_outcome IS NULL`, operationID).Scan(ctx, &pending); err != nil {
			return fmt.Errorf("advance matching acquisition: check previous provider children: %w", err)
		}
		if pending != 0 {
			return fmt.Errorf("advance matching acquisition: previous provider children are still pending")
		}
		var acquisition struct {
			Version  int    `bun:"intent_version"`
			Phase    string `bun:"current_phase"`
			Revision int64  `bun:"revision"`
			Intent   []byte `bun:"intent"`
		}
		if err := tx.NewRaw(`SELECT intent_version, current_phase, revision, intent FROM matching_acquisition WHERE run_id=? FOR UPDATE`, operationID).Scan(ctx, &acquisition); err != nil {
			return fmt.Errorf("advance matching acquisition: lock acquisition intent: %w", err)
		}
		if !supportedMatchingAcquisitionIntentVersion(acquisition.Version) || acquisition.Revision != expectedRevision ||
			!validMatchingAcquisitionTransition(acquisition.Phase, phase.Phase, children) {
			return nil
		}
		var intent MatchingAcquisitionIntent
		if err := json.Unmarshal(acquisition.Intent, &intent); err != nil || intent.SchemaVersion != acquisition.Version || len(intent.InitialProviderInputs) != 1 {
			return fmt.Errorf("advance matching acquisition: saved acquisition intent is invalid")
		}
		var previous MatchingAcquisitionPhase
		if err := tx.NewRaw(`SELECT revision, phase, input, output, response_provenance FROM matching_acquisition_phase WHERE run_id=? AND revision=?`, operationID, acquisition.Revision).Scan(ctx, &previous); err != nil {
			return fmt.Errorf("advance matching acquisition: read previous phase: %w", err)
		}
		if previous.Phase != acquisition.Phase {
			return fmt.Errorf("advance matching acquisition: saved phase cursor is inconsistent")
		}
		if acquisition.Phase == "admitted" {
			var input struct {
				Offset int `json:"offset"`
			}
			var output struct {
				ArtistMBID string `json:"artist_mbid"`
				NextOffset int    `json:"next_offset"`
			}
			if json.Unmarshal(phase.Input, &input) != nil || json.Unmarshal(phase.Output, &output) != nil || input.Offset != 0 || output.NextOffset != 0 ||
				provenance[0].RequestID != intent.InitialProviderInputs[0].OperationID || provenance[0].CacheKey != intent.InitialProviderInputs[0].CacheKey {
				return fmt.Errorf("advance matching acquisition: initial browse must start at the first catalog page of the captured top artist")
			}
		} else {
			var priorOutput struct {
				ArtistMBID string    `json:"artist_mbid"`
				NextOffset int       `json:"next_offset"`
				RequestID  uuid.UUID `json:"next_request_id"`
				CacheKey   string    `json:"next_cache_key"`
			}
			var nextInput struct {
				ArtistMBID string `json:"artist_mbid"`
				Offset     int    `json:"offset"`
				Limit      int    `json:"limit"`
			}
			if json.Unmarshal(previous.Output, &priorOutput) != nil || json.Unmarshal(phase.Input, &nextInput) != nil ||
				!strings.EqualFold(nextInput.ArtistMBID, priorOutput.ArtistMBID) || nextInput.Offset != priorOutput.NextOffset || nextInput.Limit < 1 || nextInput.Limit > 100 ||
				priorOutput.RequestID != uuid.Nil && priorOutput.RequestID != provenance[0].RequestID || priorOutput.CacheKey != "" && priorOutput.CacheKey != provenance[0].CacheKey {
				return fmt.Errorf("advance matching acquisition: browse page does not continue the persisted artist cursor")
			}
			var precedingRequestIDs []uuid.UUID
			if err := tx.NewRaw(`SELECT operation.id FROM matching_provider_dependency dependency
				JOIN operation ON operation.id=dependency.request_id
				WHERE dependency.parent_run_id=? AND dependency.terminal_outcome='succeeded' AND operation.kind='provider_artist_release_browse'
				AND operation.input_snapshot->>'mbid'=? AND COALESCE((operation.input_snapshot->>'offset')::integer, 0)=?
				AND (operation.input_snapshot->>'limit')::integer=? AND dependency.cache_key=?`, operationID, strings.ToLower(priorOutput.ArtistMBID), priorOutput.NextOffset, nextInput.Limit, provenance[0].CacheKey).Scan(ctx, &precedingRequestIDs); err != nil {
				return fmt.Errorf("advance matching acquisition: resolve persisted preceding child: %w", err)
			}
			if len(precedingRequestIDs) != 1 || precedingRequestIDs[0] != provenance[0].RequestID {
				return fmt.Errorf("advance matching acquisition: response is not the exact persisted preceding browse child")
			}
		}
		for _, response := range provenance {
			kind, payload, err := validateMatchingAcquisitionResponse(ctx, tx, operationID, response)
			if err != nil {
				return err
			}
			if acquisition.Phase == "admitted" {
				if kind != "provider_artist_search" || !responseIsTopArtist(payload, phase.Output) {
					return fmt.Errorf("advance matching acquisition: first browse target is not the top artist from accepted search")
				}
			} else if kind != "provider_artist_release_browse" || !responseCursorMatches(payload, phase.Input, phase.Output, previous.Output) {
				return fmt.Errorf("advance matching acquisition: next browse cursor does not follow the accepted page")
			}
		}
		var phaseOutput struct {
			Complete bool `json:"complete"`
		}
		if err := json.Unmarshal(phase.Output, &phaseOutput); err != nil {
			return fmt.Errorf("advance matching acquisition: decode page completion state: %w", err)
		}
		if phaseOutput.Complete {
			if phase.Phase != "catalog_ready" || len(children) != 0 || acquisition.Phase != MatchingAcquisitionArtistCatalogPage {
				return fmt.Errorf("advance matching acquisition: completed catalog page must enter catalog_ready without children")
			}
		} else {
			if phase.Phase != MatchingAcquisitionArtistCatalogPage || len(children) != 1 {
				return fmt.Errorf("advance matching acquisition: incomplete catalog page must admit exactly one next page")
			}
			if err := validateMatchingAcquisitionChildren(children, sourceFence, phase); err != nil {
				return err
			}
		}
		for _, child := range children {
			if err := repository.createOperationAndEnqueueTx(ctx, tx, child.Operation, client, child.Args, nil); err != nil {
				return fmt.Errorf("advance matching acquisition: admit provider child: %w", err)
			}
			if _, err := tx.NewRaw(`INSERT INTO matching_provider_dependency(parent_run_id, request_id, cache_key) VALUES (?, ?, ?)`, operationID, child.Operation.ID, *child.Operation.ProviderCacheKey).Exec(ctx); err != nil {
				return fmt.Errorf("advance matching acquisition: register provider child: %w", err)
			}
		}
		result, err := tx.NewRaw(`UPDATE matching_acquisition SET current_phase=?, revision=?
			WHERE run_id=? AND revision=?`, phase.Phase, phase.Revision, operationID, expectedRevision).Exec(ctx)
		if err != nil {
			return fmt.Errorf("advance matching acquisition: update phase cursor: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return nil
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id, revision, phase, input, output, response_provenance)
			VALUES (?, ?, ?, ?::jsonb, ?::jsonb, ?::jsonb)`, operationID, phase.Revision, phase.Phase, string(phase.Input), string(phase.Output), string(phase.ResponseProvenance)).Exec(ctx); err != nil {
			return fmt.Errorf("advance matching acquisition: append phase: %w", err)
		}
		now := time.Now().UTC()
		consume, err := tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL`, now, operationID, riverJobID, attempt).Exec(ctx)
		if err != nil {
			return fmt.Errorf("advance matching acquisition: consume parent delivery: %w", err)
		}
		if affected, err := consume.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("advance matching acquisition: parent delivery is no longer registered")
		}
		runState, operationStage := "waiting_provider", "waiting_provider"
		if phase.Phase == "catalog_ready" {
			inserted, err := client.InsertTx(ctx, tx.Tx, MatchingEvaluationWakeArgs{OperationID: operationID}, nil)
			if err != nil {
				return fmt.Errorf("advance matching acquisition: enqueue catalog evaluation wake: %w", err)
			}
			if inserted == nil || inserted.Job == nil || inserted.Job.ID < 1 {
				return fmt.Errorf("advance matching acquisition: catalog evaluation wake has no River job")
			}
			if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id, river_job_id, operation_attempt)
				VALUES (?, ?, ?)`, operationID, inserted.Job.ID, attempt).Exec(ctx); err != nil {
				return fmt.Errorf("advance matching acquisition: register catalog evaluation wake: %w", err)
			}
			runState, operationStage = "queued", "ranking_pending"
		}
		if _, err := tx.NewRaw(`UPDATE matching_run SET state=?, active_river_job_id=NULL,
			active_river_attempt=NULL, active_execution_epoch=NULL, updated_at=?
			WHERE id=? AND execution_epoch=?`, runState, now, operationID, claim.ExecutionEpoch).Exec(ctx); err != nil {
			return fmt.Errorf("advance matching acquisition: park matching run: %w", err)
		}
		operation.Stage, operation.UpdatedAt = operationStage, now
		if _, err := tx.NewUpdate().Model(operation).Column("stage", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("advance matching acquisition: park parent operation: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}

type matchingAcquisitionResponseProvenance struct {
	RequestID  uuid.UUID `json:"request_id"`
	CacheKey   string    `json:"cache_key"`
	PayloadSHA string    `json:"payload_sha256"`
}

func validateMatchingAcquisitionResponse(ctx context.Context, tx bun.Tx, runID uuid.UUID, response matchingAcquisitionResponseProvenance) (string, json.RawMessage, error) {
	if response.RequestID == uuid.Nil || response.CacheKey == "" || len(response.PayloadSHA) != 64 {
		return "", nil, fmt.Errorf("advance matching acquisition: response provenance is incomplete")
	}
	var accepted struct {
		Kind string          `bun:"kind"`
		Body json.RawMessage `bun:"payload"`
	}
	if err := tx.NewRaw(`SELECT operation.kind, cache.payload FROM matching_provider_dependency dependency
		JOIN operation ON operation.id=dependency.request_id
		JOIN matching_run run ON run.id=dependency.parent_run_id
	JOIN provider_response_cache cache ON cache.provider_source_id=run.provider_source_id AND cache.cache_key=dependency.cache_key
		WHERE dependency.parent_run_id=? AND dependency.request_id=? AND dependency.cache_key=?
		AND dependency.terminal_outcome='succeeded' AND cache.payload IS NOT NULL
		FOR SHARE OF cache`, runID, response.RequestID, response.CacheKey).Scan(ctx, &accepted); err != nil {
		return "", nil, fmt.Errorf("advance matching acquisition: response is not an accepted successful dependency: %w", err)
	}
	digest := sha256.Sum256(accepted.Body)
	if fmt.Sprintf("%x", digest[:]) != response.PayloadSHA {
		return "", nil, fmt.Errorf("advance matching acquisition: accepted response payload changed")
	}
	return accepted.Kind, accepted.Body, nil
}

func validMatchingAcquisitionTransition(current, next string, children []MatchingProviderRequest) bool {
	if current == MatchingAcquisitionArtistCatalogPage && next == "catalog_ready" && len(children) == 0 {
		return true
	}
	if next != MatchingAcquisitionArtistCatalogPage || len(children) != 1 {
		return false
	}
	if current != "admitted" && current != MatchingAcquisitionArtistCatalogPage {
		return false
	}
	for _, child := range children {
		if child.Operation == nil || child.Operation.Kind != "provider_artist_release_browse" {
			return false
		}
	}
	return true
}

func validateMatchingAcquisitionChildren(children []MatchingProviderRequest, source matchingAcquisitionSourceFence, phase MatchingAcquisitionPhase) error {
	type browseIntent struct {
		SchemaVersion int    `json:"schema_version"`
		MBID          string `json:"mbid"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}
	var output struct {
		ArtistMBID string    `json:"artist_mbid"`
		NextOffset int       `json:"next_offset"`
		Complete   bool      `json:"complete"`
		RequestID  uuid.UUID `json:"next_request_id"`
		CacheKey   string    `json:"next_cache_key"`
	}
	if err := json.Unmarshal(phase.Output, &output); err != nil || output.ArtistMBID == "" || output.NextOffset < 0 || output.Complete {
		return fmt.Errorf("advance matching acquisition: artist catalog page output is invalid or already complete")
	}
	var input struct {
		ArtistMBID string `json:"artist_mbid"`
		Offset     int    `json:"offset"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(phase.Input, &input); err != nil || !strings.EqualFold(input.ArtistMBID, output.ArtistMBID) || input.Offset < 0 || input.Limit < 1 || input.Limit > 100 {
		return fmt.Errorf("advance matching acquisition: artist catalog page input is invalid")
	}
	seenCacheKeys := make(map[string]struct{}, len(children))
	seenIDs := make(map[uuid.UUID]struct{}, len(children))
	for _, child := range children {
		operation := child.Operation
		if operation == nil || operation.ID == uuid.Nil || operation.State != "queued" || operation.ProviderSourceID == nil || *operation.ProviderSourceID != source.SourceID ||
			operation.ProviderConfigurationIdentity == nil || *operation.ProviderConfigurationIdentity != source.Identity || operation.ProviderID == nil || *operation.ProviderID != source.ProviderID ||
			operation.ProviderEndpoint != source.Endpoint || operation.ProviderCacheKey == nil || strings.TrimSpace(*operation.ProviderCacheKey) == "" || child.Args == nil || child.Args.Kind() != "provider_fetch_v1" {
			return fmt.Errorf("advance matching acquisition: provider child fence is invalid")
		}
		if _, exists := seenCacheKeys[*operation.ProviderCacheKey]; exists {
			return fmt.Errorf("advance matching acquisition: duplicate provider child cache key")
		}
		seenCacheKeys[*operation.ProviderCacheKey] = struct{}{}
		if _, exists := seenIDs[operation.ID]; exists {
			return fmt.Errorf("advance matching acquisition: duplicate provider child operation id")
		}
		seenIDs[operation.ID] = struct{}{}
		encodedArgs, err := json.Marshal(child.Args)
		if err != nil {
			return fmt.Errorf("advance matching acquisition: encode provider child River arguments: %w", err)
		}
		var args struct {
			OperationID uuid.UUID `json:"operation_id"`
		}
		if err := json.Unmarshal(encodedArgs, &args); err != nil || args.OperationID != operation.ID {
			return fmt.Errorf("advance matching acquisition: provider child River arguments do not match operation")
		}
		var request browseIntent
		if err := json.Unmarshal(operation.InputSnapshot, &request); err != nil || request.SchemaVersion != 1 || !strings.EqualFold(request.MBID, output.ArtistMBID) || request.Offset != output.NextOffset || request.Limit != input.Limit || output.RequestID != uuid.Nil && output.RequestID != operation.ID || output.CacheKey != "" && output.CacheKey != *operation.ProviderCacheKey {
			return fmt.Errorf("advance matching acquisition: browse child does not follow the saved artist cursor")
		}
		expectedCacheKey := fmt.Sprintf("artist-release-browse:%s:%d:%d", strings.ToLower(request.MBID), request.Offset, request.Limit)
		if *operation.ProviderCacheKey != expectedCacheKey {
			return fmt.Errorf("advance matching acquisition: browse child cache key does not match its saved cursor")
		}
	}
	return nil
}

func responseIsTopArtist(payload, phaseOutput json.RawMessage) bool {
	var response struct {
		Artists []struct {
			ID string `json:"id"`
		} `json:"artists"`
	}
	var output struct {
		ArtistMBID string `json:"artist_mbid"`
	}
	return json.Unmarshal(payload, &response) == nil && json.Unmarshal(phaseOutput, &output) == nil &&
		len(response.Artists) > 0 && strings.EqualFold(response.Artists[0].ID, output.ArtistMBID)
}

func responseCursorMatches(payload, phaseInput, phaseOutput, previousOutput json.RawMessage) bool {
	// The cached payload is the raw MusicBrainz artist release browse envelope,
	// which reports hyphenated release-count/release-offset keys. Parse them
	// strictly so an absent or null count/offset is never read as a real zero.
	var response struct {
		Offset   *int               `json:"release-offset"`
		Count    *int               `json:"release-count"`
		Releases *[]json.RawMessage `json:"releases"`
	}
	var input struct {
		Offset int `json:"offset"`
		Limit  int `json:"limit"`
	}
	var output struct {
		NextOffset   int      `json:"next_offset"`
		Complete     bool     `json:"complete"`
		CatalogCount *int     `json:"catalog_count"`
		Covered      int      `json:"covered_releases"`
		ReleaseIDs   []string `json:"release_ids"`
	}
	var previous struct {
		ReleaseIDs   []string `json:"release_ids"`
		CatalogCount *int     `json:"catalog_count"`
	}
	if json.Unmarshal(payload, &response) != nil || json.Unmarshal(phaseInput, &input) != nil || json.Unmarshal(phaseOutput, &output) != nil ||
		json.Unmarshal(previousOutput, &previous) != nil ||
		response.Offset == nil || response.Count == nil || response.Releases == nil ||
		input.Offset < 0 || input.Limit < 1 || *response.Offset != input.Offset || *response.Count < 0 || len(*response.Releases) > input.Limit ||
		len(*response.Releases) == 0 && *response.Offset < *response.Count || output.NextOffset != *response.Offset+len(*response.Releases) ||
		output.NextOffset > *response.Count || output.Complete != (output.NextOffset >= *response.Count) {
		return false
	}
	// Every appended catalog page must seal the accepted raw release count. An
	// omitted or null catalog_count is not a valid zero, and a sealed count that
	// differs from the accepted browse envelope is a tampered manifest.
	if output.CatalogCount == nil || *output.CatalogCount != *response.Count {
		return false
	}
	// The prior page's sealed count is the same catalog the accepted raw response
	// reports. It is legitimately absent only before the first browse establishes
	// it (offset zero with no prior releases); any continuation at a nonzero
	// offset must carry it, so a dropped count cannot silently reset the accepted
	// catalog size while keeping an exact release-id prefix.
	if previous.CatalogCount == nil {
		if input.Offset != 0 {
			return false
		}
	} else if *previous.CatalogCount != *response.Count {
		return false
	}
	if output.Covered != output.NextOffset || len(output.ReleaseIDs) != output.NextOffset || input.Offset != len(previous.ReleaseIDs) {
		return false
	}
	if len(output.ReleaseIDs) != len(previous.ReleaseIDs)+len(*response.Releases) {
		return false
	}
	seen := make(map[string]struct{}, len(output.ReleaseIDs))
	for index, id := range previous.ReleaseIDs {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id || output.ReleaseIDs[index] != id {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	for index, release := range *response.Releases {
		var item struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(release, &item) != nil {
			return false
		}
		parsed, err := uuid.Parse(item.ID)
		if err != nil || parsed.String() != strings.ToLower(item.ID) || output.ReleaseIDs[len(previous.ReleaseIDs)+index] != parsed.String() {
			return false
		}
		if _, duplicate := seen[parsed.String()]; duplicate {
			return false
		}
		seen[parsed.String()] = struct{}{}
	}
	return true
}

func validateAcquisitionJSON(value json.RawMessage, name string) error {
	var object map[string]json.RawMessage
	if len(value) == 0 || json.Unmarshal(value, &object) != nil || object == nil {
		return fmt.Errorf("matching acquisition %s must be a JSON object", name)
	}
	return nil
}
