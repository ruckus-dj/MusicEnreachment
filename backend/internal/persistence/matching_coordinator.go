package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

// captureMatchingAdmissionMetadata captures deterministic per-member metadata.
// A nil scope captures every raw member of the target groups. A non-nil scope
// captures exactly those member IDs and still proves that no scoped member is
// missing, so an admission can bind the same active-ready subset it captured
// for its target. A duplicate member identity across groups resolves once.
func captureMatchingAdmissionMetadata(ctx context.Context, tx bun.Tx, targets []MatchingTarget, scope []uuid.UUID) ([]MatchingAcquisitionCapturedMember, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("matching admission: metadata capture requires selected groups")
	}
	expected := make(map[uuid.UUID]struct{})
	for _, memberID := range scope {
		if memberID == uuid.Nil {
			return nil, fmt.Errorf("matching admission: metadata capture scope is invalid")
		}
		if _, duplicate := expected[memberID]; duplicate {
			return nil, fmt.Errorf("matching admission: metadata capture scope is not unique")
		}
		expected[memberID] = struct{}{}
	}
	var rows []struct {
		MemberID          uuid.UUID       `bun:"member_id"`
		WorkID            uuid.UUID       `bun:"work_id"`
		CaptureAnalysisID *uuid.UUID      `bun:"capture_analysis_id"`
		AnalysisInput     json.RawMessage `bun:"analysis_input"`
		Tags              json.RawMessage `bun:"tags"`
	}
	query := `SELECT DISTINCT ON (member.variant_id)
		member.variant_id AS member_id, location.work_id,
		metadata.success_metadata_result_id AS capture_analysis_id,
		COALESCE(metadata.input_snapshot, '{}'::jsonb) AS analysis_input,
		COALESCE(metadata_result.observed_tags, '{}'::jsonb) AS tags
		FROM incoming_group_member member
		JOIN incoming_group_member_location location ON location.group_id=member.group_id AND location.variant_id=member.variant_id
		LEFT JOIN source_analysis_step metadata ON metadata.work_id=location.work_id AND metadata.step='metadata'
		LEFT JOIN media_metadata_result metadata_result ON metadata_result.id=metadata.success_metadata_result_id
		WHERE member.group_id IN (?)`
	args := []any{bun.List(targetGroupIDs(targets))}
	if scope != nil {
		query += ` AND member.variant_id IN (?)`
		args = append(args, bun.List(scope))
	}
	query += ` ORDER BY member.variant_id, location.work_id, location.source_root_id, location.relative_path`
	if err := tx.NewRaw(query, args...).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("matching admission: capture persisted metadata: %w", err)
	}
	if scope == nil {
		for _, target := range targets {
			var ids []uuid.UUID
			if err := tx.NewRaw(`SELECT variant_id FROM incoming_group_member WHERE group_id=? ORDER BY variant_id`, target.GroupID).Scan(ctx, &ids); err != nil {
				return nil, fmt.Errorf("matching admission: read selected members: %w", err)
			}
			for _, id := range ids {
				expected[id] = struct{}{}
			}
		}
	}
	if len(rows) != len(expected) {
		return nil, fmt.Errorf("matching admission: selected member metadata capture is incomplete")
	}
	captured := make([]MatchingAcquisitionCapturedMember, 0, len(rows))
	for _, row := range rows {
		if row.MemberID == uuid.Nil || row.WorkID == uuid.Nil {
			return nil, fmt.Errorf("matching admission: selected member work identity is unavailable")
		}
		if _, ok := expected[row.MemberID]; !ok {
			return nil, fmt.Errorf("matching admission: metadata capture contains an unselected member")
		}
		if len(row.Tags) == 0 || !json.Valid(row.Tags) {
			return nil, fmt.Errorf("matching admission: selected member tags are invalid")
		}
		member := MatchingAcquisitionCapturedMember{
			MemberID: row.MemberID, WorkID: row.WorkID, CaptureAnalysisID: row.CaptureAnalysisID,
			Tags: append(json.RawMessage(nil), row.Tags...),
		}
		if len(row.AnalysisInput) != 0 && string(row.AnalysisInput) != `{}` {
			digest := sha256.Sum256(row.AnalysisInput)
			member.AnalysisInputSHA256 = fmt.Sprintf("%x", digest[:])
		}
		captured = append(captured, member)
	}
	sort.Slice(captured, func(i, j int) bool { return captured[i].MemberID.String() < captured[j].MemberID.String() })
	return captured, nil
}

func normalizeCapturedMatchingMembers(members []MatchingAcquisitionCapturedMember) ([]MatchingAcquisitionCapturedMember, error) {
	normalized := append([]MatchingAcquisitionCapturedMember(nil), members...)
	for index := range normalized {
		canonicalTags, err := canonicalMatchingJSON(normalized[index].Tags)
		if err != nil {
			return nil, fmt.Errorf("canonicalize captured matching member tags: %w", err)
		}
		normalized[index].Tags = canonicalTags
	}
	return normalized, nil
}

func matchingMetadataCaptureDigest(members []MatchingAcquisitionCapturedMember) (string, error) {
	var err error
	members, err = normalizeCapturedMatchingMembers(members)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(members)
	if err != nil {
		return "", fmt.Errorf("encode matching metadata capture: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:]), nil
}

func targetGroupIDs(targets []MatchingTarget) []uuid.UUID {
	ids := make([]uuid.UUID, len(targets))
	for i, target := range targets {
		ids[i] = target.GroupID
	}
	return ids
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
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("retry matching evaluation: lock source mutation gate: %w", err)
		}
		locked, err := repository.GetOperationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if locked.Kind != "matching_evaluation_v1" || locked.State != "failed" {
			return fmt.Errorf("only failed matching evaluations can be retried")
		}
		var run struct {
			State        string     `bun:"state"`
			SourceID     uuid.UUID  `bun:"provider_source_id"`
			Identity     string     `bun:"provider_configuration_identity"`
			FenceVersion int        `bun:"fence_version"`
			QuiescedAt   *time.Time `bun:"quiesced_at"`
		}
		if err := tx.NewRaw(`SELECT state, provider_source_id, provider_configuration_identity, fence_version, quiesced_at FROM matching_run WHERE id=? FOR UPDATE`, id).Scan(ctx, &run); err != nil {
			return fmt.Errorf("retry matching evaluation: lock run: %w", err)
		}
		if run.State != "failed" {
			return fmt.Errorf("retry matching evaluation: run is not failed")
		}
		if run.QuiescedAt != nil {
			return fmt.Errorf("retry matching evaluation: matching run is quiesced")
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
		switch run.FenceVersion {
		case 2:
			if err := validateMatchingRunFence(ctx, tx, id, run.FenceVersion); err != nil {
				return err
			}
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM matching_run_target target
				LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
				LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
				WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
					generation.high_water IS DISTINCT FROM target.target_generation OR
					current_group.revision IS DISTINCT FROM target.captured_group_revision)
			)`, id).Scan(ctx, &stale); err != nil {
				return fmt.Errorf("retry matching evaluation: validate captured targets: %w", err)
			}
		case 1:
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
		default:
			return fmt.Errorf("retry matching evaluation: unsupported fence version %d", run.FenceVersion)
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
				current_river_job_id=?, active_river_job_id=NULL, active_river_attempt=NULL,
				active_execution_epoch=NULL, updated_at=now() WHERE id=?`, result.Job.ID, id).Exec(ctx); err != nil {
			return fmt.Errorf("retry matching evaluation: advance execution epoch: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id, river_job_id, operation_attempt) VALUES (?, ?, ?)`, id, result.Job.ID, locked.Attempt).Exec(ctx); err != nil {
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

func equalDigest(a, b []byte) bool { return hex.EncodeToString(a) == hex.EncodeToString(b) }
