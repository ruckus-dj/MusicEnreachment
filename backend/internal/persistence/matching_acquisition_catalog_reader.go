package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// GetMatchingAcquisitionCatalogPhases returns the immutable artist browse
// phases belonging to a completed acquisition run. The snapshot keeps run
// ownership, intent, and phase rows coherent while a concurrent transition may
// append a later phase.
func (repository *SetupManagerRepository) GetMatchingAcquisitionCatalogPhases(ctx context.Context, runID uuid.UUID) ([]MatchingAcquisitionPhase, error) {
	if runID == uuid.Nil {
		return nil, fmt.Errorf("read matching acquisition catalog phases: run id is required")
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("read matching acquisition catalog phases: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var run struct {
		OperationID uuid.UUID `bun:"operation_id"`
		Kind        string    `bun:"kind"`
		PlanDigest  []byte    `bun:"plan_digest"`
		Plan        []byte    `bun:"immutable_plan"`
	}
	var intentBytes, intentDigest []byte
	var version int
	var currentPhase string
	var currentRevision int64
	err = tx.NewRaw(`SELECT run.operation_id, operation.kind, run.plan_digest, run.immutable_plan,
		acquisition.intent, acquisition.intent_digest, acquisition.intent_version,
		acquisition.current_phase, acquisition.revision
		FROM matching_run run
		JOIN operation ON operation.id=run.operation_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		WHERE run.id=?`, runID).Scan(ctx, &run.OperationID, &run.Kind, &run.PlanDigest, &run.Plan,
		&intentBytes, &intentDigest, &version, &currentPhase, &currentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read matching acquisition catalog phases: acquisition run is missing")
	}
	if err != nil {
		return nil, fmt.Errorf("read matching acquisition catalog phases: read run: %w", err)
	}
	if run.OperationID != runID || run.Kind != "matching_evaluation_v1" || version != matchingAcquisitionIntentVersion {
		return nil, fmt.Errorf("read matching acquisition catalog phases: run is not owned by an acquisition")
	}
	var intent MatchingAcquisitionIntent
	if json.Unmarshal(intentBytes, &intent) != nil || intent.SchemaVersion != matchingAcquisitionIntentVersion {
		return nil, fmt.Errorf("read matching acquisition catalog phases: saved intent is invalid")
	}
	if err := validateCanonicalMatchingAcquisitionIntent(intent, intentDigest, run.PlanDigest, run.Plan); err != nil {
		return nil, fmt.Errorf("read matching acquisition catalog phases: %w", err)
	}
	var targets []MatchingTarget
	if err := tx.NewRaw(`SELECT group_id, captured_group_revision AS revision FROM matching_run_target WHERE run_id=? ORDER BY group_id`, runID).Scan(ctx, &targets); err != nil {
		return nil, fmt.Errorf("read matching acquisition catalog phases: read immutable targets: %w", err)
	}
	if !equalMatchingTargets(intent.Targets, targets) {
		return nil, fmt.Errorf("read matching acquisition catalog phases: saved targets differ from immutable run targets")
	}
	if currentPhase != "catalog_ready" || currentRevision < 1 {
		return nil, fmt.Errorf("read matching acquisition catalog phases: catalog completion phase changed")
	}

	var all []MatchingAcquisitionPhase
	if err := tx.NewRaw(`SELECT revision, phase, input, output, response_provenance
		FROM matching_acquisition_phase WHERE run_id=? ORDER BY revision`, runID).Scan(ctx, &all); err != nil {
		return nil, fmt.Errorf("read matching acquisition catalog phases: read phase ledger: %w", err)
	}
	if int64(len(all)) != currentRevision+1 || len(all) == 0 {
		return nil, fmt.Errorf("read matching acquisition catalog phases: phase ledger revision is inconsistent")
	}
	for index, phase := range all {
		if phase.Revision != int64(index) {
			return nil, fmt.Errorf("read matching acquisition catalog phases: phase ledger revision is inconsistent")
		}
	}
	latest := all[len(all)-1]
	if latest.Revision != currentRevision || latest.Phase != currentPhase {
		return nil, fmt.Errorf("read matching acquisition catalog phases: current phase differs from phase ledger")
	}
	var manifest struct {
		Complete   bool     `json:"complete"`
		Count      *int     `json:"catalog_count"`
		Covered    *int     `json:"covered_releases"`
		ReleaseIDs []string `json:"release_ids"`
	}
	if json.Unmarshal(latest.Output, &manifest) != nil || !manifest.Complete || manifest.Count == nil || manifest.Covered == nil ||
		*manifest.Count < 0 || *manifest.Count != *manifest.Covered || len(manifest.ReleaseIDs) != *manifest.Count {
		return nil, fmt.Errorf("read matching acquisition catalog phases: completion manifest is invalid")
	}

	phases := make([]MatchingAcquisitionPhase, 0, len(all))
	for _, phase := range all {
		if phase.Phase == MatchingAcquisitionArtistCatalogPage || phase.Phase == "catalog_ready" {
			phases = append(phases, phase)
		}
	}
	if len(phases) == 0 || phases[len(phases)-1].Revision != currentRevision || phases[len(phases)-1].Phase != "catalog_ready" {
		return nil, fmt.Errorf("read matching acquisition catalog phases: completed catalog phase is missing")
	}
	return phases, nil
}
