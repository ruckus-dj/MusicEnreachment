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
	"github.com/ruckus/MusicEnreachment/backend/internal/matchingfit"
	"github.com/uptrace/bun"
)

// ParkMatchingAcquisitionForDelivery records an evidence-backed acquisition
// policy wait and consumes its current delivery atomically.
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
	if err := json.Unmarshal(phase.Output, &output); err != nil || output.State != "waiting_policy" ||
		(output.Reason != "artist_search_no_results" && output.Reason != "artist_not_fit" && output.Reason != "catalog_no_results" && output.Reason != "no_candidates") {
		return false, fmt.Errorf("park matching acquisition: empty search outcome is invalid")
	}
	catalogEmpty := output.Reason == "catalog_no_results" || output.Reason == "no_candidates"
	artistNotFit := output.Reason == "artist_not_fit"

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
		var fenceVersion int
		if err := tx.NewRaw(`SELECT run.provider_source_id, run.provider_configuration_identity, source.provider_id, source.endpoint, run.fence_version
			FROM matching_run run JOIN provider_source source ON source.id=run.provider_source_id WHERE run.id=?`, operationID).Scan(ctx,
			&sourceFence.SourceID, &sourceFence.Identity, &sourceFence.ProviderID, &sourceFence.Endpoint, &fenceVersion); err != nil {
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
					generation.high_water IS DISTINCT FROM target.target_generation OR current_group.revision IS DISTINCT FROM target.captured_group_revision))`, operationID).Scan(ctx, &stale); err != nil {
				return fmt.Errorf("park matching acquisition: validate target generations: %w", err)
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
				return fmt.Errorf("park matching acquisition: validate target generations: %w", err)
			}
		default:
			return fmt.Errorf("park matching acquisition: unsupported fence version %d", fenceVersion)
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
			CurrentRiverJobID    *int64 `bun:"current_river_job_id"`
			ActiveRiverJobID     *int64 `bun:"active_river_job_id"`
			ActiveRiverAttempt   *int   `bun:"active_river_attempt"`
			ActiveExecutionEpoch *int64 `bun:"active_execution_epoch"`
		}
		if err := tx.NewRaw(`SELECT state, execution_epoch, current_river_job_id, active_river_job_id, active_river_attempt, active_execution_epoch
			FROM matching_run WHERE id=? FOR NO KEY UPDATE`, operationID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("park matching acquisition: lock run: %w", err)
		}
		if run.State != "running" || run.ExecutionEpoch != claim.ExecutionEpoch || run.CurrentRiverJobID == nil || *run.CurrentRiverJobID != riverJobID || run.ActiveRiverJobID == nil || *run.ActiveRiverJobID != riverJobID ||
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
			Digest   []byte `bun:"intent_digest"`
		}
		if err := tx.NewRaw(`SELECT intent_version, current_phase, revision, intent, intent_digest FROM matching_acquisition WHERE run_id=? FOR UPDATE`, operationID).Scan(ctx, &acquisition); err != nil {
			return fmt.Errorf("park matching acquisition: lock acquisition intent: %w", err)
		}
		expectedPhase := "admitted"
		if catalogEmpty {
			expectedPhase = "catalog_ready"
		}
		if !supportedMatchingAcquisitionIntentVersion(acquisition.Version) || acquisition.Phase != expectedPhase || acquisition.Revision != expectedRevision {
			return nil
		}
		var intent MatchingAcquisitionIntent
		if json.Unmarshal(acquisition.Intent, &intent) != nil || intent.SchemaVersion != acquisition.Version {
			return fmt.Errorf("park matching acquisition: saved acquisition intent is invalid")
		}
		var runPlan struct {
			Digest []byte `bun:"plan_digest"`
			Plan   []byte `bun:"immutable_plan"`
		}
		if err := tx.NewRaw(`SELECT plan_digest, immutable_plan FROM matching_run WHERE id=?`, operationID).Scan(ctx, &runPlan); err != nil {
			return fmt.Errorf("park matching acquisition: read immutable matching plan: %w", err)
		}
		if err := validateCanonicalMatchingAcquisitionIntent(intent, acquisition.Digest, runPlan.Digest, runPlan.Plan); err != nil {
			return fmt.Errorf("park matching acquisition: %w", err)
		}
		var targets []MatchingTarget
		if err := tx.NewRaw(`SELECT group_id, captured_group_revision AS revision FROM matching_run_target WHERE run_id=? ORDER BY group_id`, operationID).Scan(ctx, &targets); err != nil {
			return fmt.Errorf("park matching acquisition: read immutable targets: %w", err)
		}
		if !equalMatchingTargets(intent.Targets, targets) {
			return fmt.Errorf("park matching acquisition: saved targets differ from immutable run targets")
		}
		previous := new(MatchingAcquisitionPhase)
		if err := tx.NewRaw(`SELECT revision, phase, input, output, response_provenance FROM matching_acquisition_phase WHERE run_id=? AND revision=?`, operationID, expectedRevision).Scan(ctx, previous); err != nil {
			return fmt.Errorf("park matching acquisition: read previous phase: %w", err)
		}
		if previous.Revision != expectedRevision || previous.Phase != expectedPhase {
			return fmt.Errorf("park matching acquisition: saved phase cursor is inconsistent")
		}
		var acceptedSearchPayload json.RawMessage
		if catalogEmpty {
			if !json.Valid(phase.Input) || !json.Valid(previous.Output) || string(phase.Input) != string(previous.Output) {
				return fmt.Errorf("park matching acquisition: catalog wait does not match the saved manifest")
			}
			kind, payload, err := validateMatchingAcquisitionResponse(ctx, tx, operationID, provenance[0])
			if err != nil {
				return err
			}
			var page struct {
				Count    *int               `json:"release-count"`
				Offset   *int               `json:"release-offset"`
				Releases *[]json.RawMessage `json:"releases"`
			}
			var manifest struct {
				Count *int     `json:"catalog_count"`
				IDs   []string `json:"release_ids"`
			}
			if kind != "provider_artist_release_browse" || json.Unmarshal(payload, &page) != nil || page.Count == nil || *page.Count != 0 || page.Offset == nil || *page.Offset != 0 || page.Releases == nil || len(*page.Releases) != 0 ||
				json.Unmarshal(phase.Input, &manifest) != nil || manifest.Count == nil || *manifest.Count != 0 || len(manifest.IDs) != 0 {
				return fmt.Errorf("park matching acquisition: accepted catalog does not prove an empty result")
			}
			resultHash := sha256.Sum256(payload)
			if fmt.Sprintf("%x", resultHash[:]) != provenance[0].PayloadSHA {
				return fmt.Errorf("park matching acquisition: accepted catalog payload changed")
			}
			acceptedSearchPayload = payload
		} else {
			if len(intent.InitialProviderInputs) != 1 {
				return fmt.Errorf("park matching acquisition: saved initial search intent is invalid")
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
			kind, payload, err := validateMatchingAcquisitionResponse(ctx, tx, operationID, provenance[0])
			if err != nil {
				return err
			}
			var accepted struct {
				Artists *[]json.RawMessage `json:"artists"`
				Count   *int               `json:"count"`
				Offset  *int               `json:"offset"`
			}
			if kind != "provider_artist_search" || json.Unmarshal(payload, &accepted) != nil || accepted.Artists == nil ||
				accepted.Count == nil || accepted.Offset == nil || *accepted.Offset != request.Offset {
				return fmt.Errorf("park matching acquisition: accepted search result is invalid")
			}
			if artistNotFit {
				if *accepted.Count < 1 || len(*accepted.Artists) == 0 {
					return fmt.Errorf("park matching acquisition: accepted search does not contain a result for artist-fit review")
				}
				var top struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}
				if json.Unmarshal((*accepted.Artists)[0], &top) != nil || strings.TrimSpace(top.Name) == "" {
					return fmt.Errorf("park matching acquisition: accepted top artist is invalid")
				}
				parsedID, parseErr := uuid.Parse(top.ID)
				if parseErr != nil || parsedID == uuid.Nil || parsedID.String() != strings.ToLower(top.ID) {
					return fmt.Errorf("park matching acquisition: accepted top artist id is invalid")
				}
				if len(intent.CapturedMembers) == 0 {
					return fmt.Errorf("park matching acquisition: saved metadata capture is empty")
				}
				tags := make([]json.RawMessage, 0, len(intent.CapturedMembers))
				for _, member := range intent.CapturedMembers {
					tags = append(tags, member.Tags)
				}
				fit, available, fitErr := matchingfit.Fit(tags, top.Name)
				if fitErr != nil {
					return fmt.Errorf("park matching acquisition: evaluate accepted artist fit: %w", fitErr)
				}
				if available && fit >= 0.70 {
					return fmt.Errorf("park matching acquisition: accepted artist satisfies the fit policy")
				}
			} else if *accepted.Count != 0 || len(*accepted.Artists) != 0 {
				return fmt.Errorf("park matching acquisition: accepted search does not prove an empty initial result")
			}
			resultHash := sha256.Sum256(payload)
			if fmt.Sprintf("%x", resultHash[:]) != provenance[0].PayloadSHA {
				return fmt.Errorf("park matching acquisition: accepted search payload changed")
			}
			acceptedSearchPayload = payload
		}
		legacyAutomaticArtistSearch := acquisition.Version == matchingAutomaticAcquisitionIntentVersion && !catalogEmpty && !intent.EvaluationRevisionCaptured
		if acquisition.Version == matchingAutomaticAcquisitionIntentVersion && !catalogEmpty && !legacyAutomaticArtistSearch {
			if err := persistAutomaticArtistSearchReview(ctx, tx, operationID, intent, phase.Input, provenance[0], acceptedSearchPayload, sourceFence); err != nil {
				return fmt.Errorf("park matching acquisition: persist artist-search review: %w", err)
			}
		}
		if legacyAutomaticArtistSearch {
			for _, target := range targets {
				authority, err := lockIncomingGroupMatchingAuthority(ctx, tx, target.GroupID)
				if err != nil {
					return fmt.Errorf("park matching acquisition: lock current matching authority: %w", err)
				}
				var manual bool
				if err := tx.NewRaw(`SELECT manual FROM incoming_group WHERE id=?`, target.GroupID).Scan(ctx, &manual); err != nil {
					return fmt.Errorf("park matching acquisition: read current matching mode: %w", err)
				}
				if !manual && authority.Mode == "automatic" {
					if err := RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{target.GroupID}); err != nil {
						return fmt.Errorf("park matching acquisition: request current automatic evaluation: %w", err)
					}
				}
			}
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
