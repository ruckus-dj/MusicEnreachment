package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

// MatchingTarget is the exact grouping revision captured by an admission.
type MatchingTarget struct {
	GroupID  uuid.UUID `json:"group_id"`
	Revision string    `json:"revision"`
}

// MatchingProviderRequest carries a typed provider operation already prepared
// by ProviderFetchService and its ID-only River arguments.
type MatchingProviderRequest struct {
	Operation *Operation
	Args      river.JobArgs
}

// MatchingEvaluationWakeArgs is the durable ID-only wake used when a provider
// dependency settles. The worker is intentionally not registered in this slice.
type MatchingEvaluationWakeArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (MatchingEvaluationWakeArgs) Kind() string { return "matching_evaluation_v1" }

// MatchingAdmission is the immutable portion of a matching run.
type MatchingAdmission struct {
	AdmissionKey  string
	GroupingEpoch int64
	Targets       []MatchingTarget
	Plan          json.RawMessage
	PolicyVersion string
	Parent        *Operation
	ParentArgs    river.JobArgs
	Providers     []MatchingProviderRequest
}

// AdmitMatchingRun atomically fences the selected groups, stores the parent,
// provider requests and dependency rows, and inserts every ID-only delivery.
func (repository *SetupManagerRepository) AdmitMatchingRun(ctx context.Context, client RiverInserter, admission MatchingAdmission) (*Operation, error) {
	if client == nil || admission.Parent == nil || len(admission.Targets) == 0 || admission.AdmissionKey == "" || len(admission.Plan) == 0 {
		return nil, fmt.Errorf("matching admission is incomplete")
	}
	if admission.Parent.ID == uuid.Nil {
		admission.Parent.ID = uuid.New()
	}
	if admission.Parent.Kind != "matching_evaluation_v1" {
		return nil, fmt.Errorf("matching parent operation kind is invalid")
	}
	targets := append([]MatchingTarget(nil), admission.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].GroupID.String() < targets[j].GroupID.String() })
	for i, target := range targets {
		if target.GroupID == uuid.Nil || target.Revision == "" || (i > 0 && targets[i-1].GroupID == target.GroupID) {
			return nil, fmt.Errorf("matching admission targets are invalid")
		}
	}
	digest := sha256.Sum256(admission.Plan)
	var result *Operation
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("matching admission: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("matching admission: lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("matching admission: lock provider configuration: %w", err)
		}
		if len(admission.Providers) == 0 {
			return fmt.Errorf("matching admission: provider requests are required")
		}
		first := admission.Providers[0].Operation
		if first == nil || first.ProviderSourceID == nil || first.ProviderConfigurationIdentity == nil || first.ProviderID == nil || first.ProviderEndpoint == "" {
			return fmt.Errorf("matching admission: captured provider source is required")
		}
		var configIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configIdentity); err != nil || configIdentity != *first.ProviderConfigurationIdentity {
			return fmt.Errorf("matching admission: provider configuration changed")
		}
		for _, request := range admission.Providers {
			operation := request.Operation
			if operation == nil || operation.Kind == "" || operation.ProviderSourceID == nil || *operation.ProviderSourceID != *first.ProviderSourceID ||
				operation.ProviderConfigurationIdentity == nil || *operation.ProviderConfigurationIdentity != configIdentity ||
				operation.ProviderID == nil || *operation.ProviderID != *first.ProviderID || operation.ProviderEndpoint != first.ProviderEndpoint || operation.ProviderCacheKey == nil {
				return fmt.Errorf("matching admission: provider request fence is invalid")
			}
		}
		var source struct {
			ProviderID            uuid.UUID `bun:"provider_id"`
			Endpoint              string    `bun:"endpoint"`
			ConfigurationIdentity string    `bun:"configuration_identity"`
		}
		if err := tx.NewRaw(`SELECT provider_id, endpoint, configuration_identity FROM provider_source WHERE id=?`, *first.ProviderSourceID).Scan(ctx, &source); err != nil ||
			source.ProviderID != *first.ProviderID || source.Endpoint != first.ProviderEndpoint || source.ConfigurationIdentity != configIdentity {
			return fmt.Errorf("matching admission: provider source changed")
		}
		var current matchingRunAdmissionRow
		err := tx.NewRaw(`SELECT mr.operation_id, mr.plan_digest FROM matching_run mr WHERE mr.plan_admission_key=? FOR UPDATE`, admission.AdmissionKey).Scan(ctx, &current)
		if err == nil {
			if !equalDigest(current.PlanDigest, digest[:]) {
				return fmt.Errorf("matching admission key was already used for a different plan")
			}
			result, err = repository.GetOperationForUpdate(ctx, tx, current.OperationID)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("matching admission: check idempotency key: %w", err)
		}
		var grouping struct {
			Revision     int64 `bun:"revision"`
			NeedsRefresh bool  `bun:"needs_refresh"`
		}
		if err := tx.NewRaw(`SELECT revision, needs_refresh FROM incoming_grouping_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &grouping); err != nil {
			return fmt.Errorf("matching admission: lock grouping revision: %w", err)
		}
		if grouping.NeedsRefresh || grouping.Revision != admission.GroupingEpoch {
			return fmt.Errorf("matching admission: incoming grouping changed")
		}
		epoch := grouping.Revision
		for _, target := range targets {
			var revision string
			if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? FOR UPDATE`, target.GroupID).Scan(ctx, &revision); err != nil || revision != target.Revision {
				return fmt.Errorf("matching admission: selected group changed")
			}
		}
		if err := repository.createOperationAndEnqueueTx(ctx, tx, admission.Parent, client, admission.ParentArgs, nil); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_run(id, operation_id, plan_admission_key, plan_digest, immutable_plan, grouping_revision, grouping_epoch, provider_source_id, provider_configuration_identity, policy_version, state) VALUES (?, ?, ?, ?, ?::jsonb, ?, ?, ?, ?, ?, 'queued')`, admission.Parent.ID, admission.Parent.ID, admission.AdmissionKey, digest[:], string(admission.Plan), epoch, epoch, *first.ProviderSourceID, configIdentity, admission.PolicyVersion).Exec(ctx); err != nil {
			return fmt.Errorf("matching admission: create run: %w", err)
		}
		if admission.Parent.RiverJobID == nil || *admission.Parent.RiverJobID <= 0 {
			return fmt.Errorf("matching admission: parent River delivery identity is missing")
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id, river_job_id) VALUES (?, ?)`, admission.Parent.ID, *admission.Parent.RiverJobID).Exec(ctx); err != nil {
			return fmt.Errorf("matching admission: register initial parent delivery: %w", err)
		}
		for _, target := range targets {
			if _, err := tx.NewRaw(`INSERT INTO incoming_matching_generation(group_id, high_water) VALUES (?, 0) ON CONFLICT (group_id) DO NOTHING`, target.GroupID).Exec(ctx); err != nil {
				return fmt.Errorf("matching admission: initialize target generation: %w", err)
			}
			var generation int64
			if err := tx.NewRaw(`UPDATE incoming_matching_generation SET high_water=high_water+1, current_run_id=?, updated_at=now() WHERE group_id=? RETURNING high_water`, admission.Parent.ID, target.GroupID).Scan(ctx, &generation); err != nil {
				return fmt.Errorf("matching admission: advance target generation: %w", err)
			}
			if _, err := tx.NewRaw(`INSERT INTO matching_run_target(run_id, group_id, captured_group_revision, target_generation) VALUES (?, ?, ?, ?)`, admission.Parent.ID, target.GroupID, target.Revision, generation).Exec(ctx); err != nil {
				return fmt.Errorf("matching admission: save target: %w", err)
			}
		}
		for _, request := range admission.Providers {
			if err := repository.createOperationAndEnqueueTx(ctx, tx, request.Operation, client, request.Args, nil); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO matching_provider_dependency(parent_run_id, request_id, cache_key) VALUES (?, ?, ?)`, admission.Parent.ID, request.Operation.ID, *request.Operation.ProviderCacheKey).Exec(ctx); err != nil {
				return fmt.Errorf("matching admission: register provider dependency: %w", err)
			}
		}
		result = admission.Parent
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// RetryMatchingEvaluationOperationAndEnqueue advances only the run's delivery
// epoch. It deliberately preserves captured target generations: a retry of a
// superseded run is rejected instead of making it current again.
func (repository *SetupManagerRepository) RetryMatchingEvaluationOperationAndEnqueue(
	ctx context.Context,
	id uuid.UUID,
	client RiverInserter,
	args river.JobArgs,
	options *river.InsertOpts,
) (*Operation, error) {
	if client == nil || args == nil || args.Kind() != "matching_evaluation_v1" {
		return nil, fmt.Errorf("retry matching evaluation: matching River delivery is required")
	}
	var operation *Operation
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("retry matching evaluation: lock output gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("retry matching evaluation: lock provider configuration: %w", err)
		}
		locked, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if locked.Kind != "matching_evaluation_v1" || locked.State != "failed" {
			return fmt.Errorf("only failed matching evaluations can be retried")
		}
		var run struct {
			State    string    `bun:"state"`
			SourceID uuid.UUID `bun:"provider_source_id"`
			Identity string    `bun:"provider_configuration_identity"`
		}
		if err := tx.NewRaw(`SELECT state, provider_source_id, provider_configuration_identity FROM matching_run WHERE id=? FOR UPDATE`, id).Scan(ctx, &run); err != nil {
			return fmt.Errorf("retry matching evaluation: lock run: %w", err)
		}
		if run.State != "failed" {
			return fmt.Errorf("retry matching evaluation: run is not failed")
		}
		var identity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil || identity != run.Identity {
			return fmt.Errorf("retry matching evaluation: provider configuration changed")
		}
		var sourceIdentity string
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=?`, run.SourceID).Scan(ctx, &sourceIdentity); err != nil || sourceIdentity != run.Identity {
			return fmt.Errorf("retry matching evaluation: provider source changed")
		}
		var stale bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM matching_run_target target
			LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
			LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
			WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
				generation.high_water IS DISTINCT FROM target.target_generation OR
				current_group.revision IS DISTINCT FROM target.captured_group_revision)
		) OR EXISTS (SELECT 1 FROM incoming_grouping_state WHERE singleton=true AND needs_refresh=true)`, id).Scan(ctx, &stale); err != nil {
			return fmt.Errorf("retry matching evaluation: validate captured targets: %w", err)
		}
		if stale {
			return fmt.Errorf("retry matching evaluation: captured groups were superseded")
		}
		result, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("insert matching retry River job: %w", err)
		}
		if result == nil || result.Job == nil || result.Job.ID <= 0 {
			return fmt.Errorf("retry matching evaluation: River delivery identity is missing")
		}
		previousStage := locked.Stage
		locked.State = "queued"
		locked.Stage = "retry:" + previousStage
		locked.SafeError = nil
		locked.StartedAt = nil
		locked.FinishedAt = nil
		locked.BytesCompleted = 0
		locked.BytesTotal = nil
		locked.Attempt++
		locked.RiverJobID = &result.Job.ID
		locked.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(locked).Column("state", "stage", "bytes_completed", "bytes_total", "safe_error", "river_job_id", "attempt", "started_at", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("retry matching evaluation: update operation: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE matching_run SET state='queued', execution_epoch=execution_epoch+1,
			current_river_job_id=?, updated_at=now() WHERE id=?`, result.Job.ID, id).Exec(ctx); err != nil {
			return fmt.Errorf("retry matching evaluation: advance execution epoch: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id, river_job_id) VALUES (?, ?)`, id, result.Job.ID).Exec(ctx); err != nil {
			return fmt.Errorf("retry matching evaluation: register delivery: %w", err)
		}
		operation = locked
		return nil
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}

type matchingRunAdmissionRow struct {
	OperationID uuid.UUID `bun:"operation_id"`
	PlanDigest  []byte    `bun:"plan_digest"`
}

func equalDigest(a, b []byte) bool { return hex.EncodeToString(a) == hex.EncodeToString(b) }
