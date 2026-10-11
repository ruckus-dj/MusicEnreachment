package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ParkMatchingAcquisitionForDelivery records an accepted empty initial artist
// search as a manual-review wait and consumes its current delivery atomically.
func (repository *SetupManagerRepository) ParkMatchingAcquisitionForDelivery(
	ctx context.Context,
	operationID uuid.UUID,
	attempt int,
	riverJobID int64,
	claim MatchingDeliveryClaim,
	expectedRevision int64,
	phase MatchingAcquisitionPhase,
) (bool, error) {
	if operationID == uuid.Nil || attempt < 1 || riverJobID < 1 || claim.ExecutionEpoch < 1 || claim.RiverAttempt < 1 ||
		expectedRevision < 0 || phase.Revision != expectedRevision+1 || phase.Phase != "waiting_policy" {
		return false, fmt.Errorf("park matching acquisition: invalid transition fence")
	}
	if err := validateAcquisitionJSON(phase.Input, "input"); err != nil {
		return false, fmt.Errorf("park matching acquisition: %w", err)
	}
	if err := validateAcquisitionJSON(phase.Output, "output"); err != nil {
		return false, fmt.Errorf("park matching acquisition: %w", err)
	}
	if len(phase.ResponseProvenance) == 0 || !json.Valid(phase.ResponseProvenance) || phase.ResponseProvenance[0] != '[' {
		return false, fmt.Errorf("park matching acquisition: response provenance must be a JSON array")
	}
	var provenance []matchingAcquisitionResponseProvenance
	if err := json.Unmarshal(phase.ResponseProvenance, &provenance); err != nil || len(provenance) != 1 {
		return false, fmt.Errorf("park matching acquisition: exactly one accepted search response is required")
	}
	var output struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(phase.Output, &output); err != nil || output.State != "waiting_policy" || output.Reason != "artist_search_no_results" {
		return false, fmt.Errorf("park matching acquisition: empty search outcome is invalid")
	}

	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("park matching acquisition: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("park matching acquisition: lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("park matching acquisition: lock provider configuration: %w", err)
		}
		var sourceFence matchingAcquisitionSourceFence
		if err := tx.NewRaw(`SELECT run.provider_source_id, run.provider_configuration_identity, source.provider_id, source.endpoint
			FROM matching_run run JOIN provider_source source ON source.id=run.provider_source_id WHERE run.id=?`, operationID).Scan(ctx, &sourceFence); err != nil {
			return fmt.Errorf("park matching acquisition: read run source fence: %w", err)
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
			return fmt.Errorf("park matching acquisition: lock target groups: %w", err)
		}
		var stale bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM matching_run_target target
			LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
			LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
			WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
				generation.high_water IS DISTINCT FROM target.target_generation OR
				current_group.revision IS DISTINCT FROM target.captured_group_revision)
			) OR EXISTS (SELECT 1 FROM incoming_grouping_state WHERE singleton=true AND needs_refresh=true)`, operationID).Scan(ctx, &stale); err != nil {
			return fmt.Errorf("park matching acquisition: validate target generations: %w", err)
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
			return fmt.Errorf("park matching acquisition: lock run: %w", err)
		}
		if run.State != "running" || run.ExecutionEpoch != claim.ExecutionEpoch || run.ActiveRiverJobID == nil || *run.ActiveRiverJobID != riverJobID ||
			run.ActiveRiverAttempt == nil || *run.ActiveRiverAttempt != claim.RiverAttempt || run.ActiveExecutionEpoch == nil || *run.ActiveExecutionEpoch != claim.ExecutionEpoch {
			return nil
		}
		var registered bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_delivery WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL)`, operationID, riverJobID, attempt).Scan(ctx, &registered); err != nil {
			return fmt.Errorf("park matching acquisition: check parent delivery: %w", err)
		}
		if !registered {
			return nil
		}
		var pending int
		if err := tx.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND terminal_outcome IS NULL`, operationID).Scan(ctx, &pending); err != nil {
			return fmt.Errorf("park matching acquisition: check provider dependencies: %w", err)
		}
		if pending != 0 {
			return nil
		}
		var acquisition struct {
			Version  int    `bun:"intent_version"`
			Phase    string `bun:"current_phase"`
			Revision int64  `bun:"revision"`
			Intent   []byte `bun:"intent"`
		}
		if err := tx.NewRaw(`SELECT intent_version, current_phase, revision, intent FROM matching_acquisition WHERE run_id=? FOR UPDATE`, operationID).Scan(ctx, &acquisition); err != nil {
			return fmt.Errorf("park matching acquisition: lock acquisition intent: %w", err)
		}
		if acquisition.Version != matchingAcquisitionIntentVersion || acquisition.Phase != "admitted" || acquisition.Revision != expectedRevision {
			return nil
		}
		var intent MatchingAcquisitionIntent
		if json.Unmarshal(acquisition.Intent, &intent) != nil || intent.SchemaVersion != matchingAcquisitionIntentVersion || len(intent.InitialProviderInputs) != 1 {
			return fmt.Errorf("park matching acquisition: saved acquisition intent is invalid")
		}
		initial := intent.InitialProviderInputs[0]
		if initial.Kind != "provider_artist_search" || initial.OperationID == uuid.Nil || initial.CacheKey == "" || !json.Valid(initial.Input) || string(initial.Input) != string(phase.Input) ||
			provenance[0].RequestID != initial.OperationID || provenance[0].CacheKey != initial.CacheKey {
			return fmt.Errorf("park matching acquisition: phase does not match the captured initial search")
		}
		var request struct {
			SchemaVersion int    `json:"schema_version"`
			Query         string `json:"query"`
			Offset        int    `json:"offset"`
			Limit         int    `json:"limit"`
		}
		if json.Unmarshal(initial.Input, &request) != nil || request.SchemaVersion != 1 || strings.TrimSpace(request.Query) == "" ||
			request.Offset != 0 || request.Limit < 1 || request.Limit > 100 ||
			initial.CacheKey != fmt.Sprintf("artist-search:0:%d:%s", request.Limit, strings.TrimSpace(request.Query)) {
			return fmt.Errorf("park matching acquisition: captured initial search is invalid")
		}
		previous := new(MatchingAcquisitionPhase)
		if err := tx.NewRaw(`SELECT revision, phase, input, output, response_provenance FROM matching_acquisition_phase WHERE run_id=? AND revision=?`, operationID, expectedRevision).Scan(ctx, previous); err != nil {
			return fmt.Errorf("park matching acquisition: read previous phase: %w", err)
		}
		if previous.Revision != expectedRevision || previous.Phase != "admitted" {
			return fmt.Errorf("park matching acquisition: saved phase cursor is inconsistent")
		}
		kind, payload, err := validateMatchingAcquisitionResponse(ctx, tx, operationID, provenance[0])
		if err != nil {
			return err
		}
		var accepted struct {
			Artists *[]json.RawMessage `json:"artists"`
			Count   *int               `json:"count"`
			Offset  *int               `json:"offset"`
		}
		if kind != "provider_artist_search" || json.Unmarshal(payload, &accepted) != nil || accepted.Artists == nil || len(*accepted.Artists) != 0 ||
			accepted.Count == nil || *accepted.Count != 0 || accepted.Offset == nil || *accepted.Offset != request.Offset {
			return fmt.Errorf("park matching acquisition: accepted search does not prove an empty initial result")
		}
		resultHash := sha256.Sum256(payload)
		if fmt.Sprintf("%x", resultHash[:]) != provenance[0].PayloadSHA {
			return fmt.Errorf("park matching acquisition: accepted search payload changed")
		}

		now := time.Now().UTC()
		result, err := tx.NewRaw(`UPDATE matching_acquisition SET current_phase=?, revision=? WHERE run_id=? AND revision=?`, phase.Phase, phase.Revision, operationID, expectedRevision).Exec(ctx)
		if err != nil {
			return fmt.Errorf("park matching acquisition: update phase cursor: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return nil
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id, revision, phase, input, output, response_provenance)
			VALUES (?, ?, ?, ?::jsonb, ?::jsonb, ?::jsonb)`, operationID, phase.Revision, phase.Phase, string(phase.Input), string(phase.Output), string(phase.ResponseProvenance)).Exec(ctx); err != nil {
			return fmt.Errorf("park matching acquisition: append phase: %w", err)
		}
		consume, err := tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL`, now, operationID, riverJobID, attempt).Exec(ctx)
		if err != nil {
			return fmt.Errorf("park matching acquisition: consume parent delivery: %w", err)
		}
		if affected, err := consume.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("park matching acquisition: parent delivery is no longer registered")
		}
		if _, err := tx.NewRaw(`UPDATE matching_run SET state='waiting_policy', active_river_job_id=NULL,
			active_river_attempt=NULL, active_execution_epoch=NULL, updated_at=? WHERE id=? AND execution_epoch=?`, now, operationID, claim.ExecutionEpoch).Exec(ctx); err != nil {
			return fmt.Errorf("park matching acquisition: park matching run: %w", err)
		}
		operation.Stage, operation.UpdatedAt = "manual_review", now
		if _, err := tx.NewUpdate().Model(operation).Column("stage", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("park matching acquisition: mark parent for manual review: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}
