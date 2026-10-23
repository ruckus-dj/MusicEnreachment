package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ensureReadySourceWorkVariant captures a work's immutable identity only once it
// has durably completed all requested analysis. Callers hold the source
// mutation gate and the canonical source/work rows in their transaction.
func ensureReadySourceWorkVariant(ctx context.Context, tx bun.IDB, work SourceAnalysisWork, location SourceLocation) error {
	if work.ID == uuid.Nil || location.ID == uuid.Nil {
		return fmt.Errorf("ensure source work variant identity: work and location are required")
	}
	var ready bool
	err := tx.NewRaw(`SELECT COALESCE(
			((metadata.state='succeeded' AND metadata.success_metadata_result_id IS NOT NULL)
				OR (metadata.input_snapshot IS NULL AND (metadata.state='not_requested'
					OR (metadata.state='skipped' AND metadata.skip_reason IN ('disabled','not_requested')))))
		AND NOT EXISTS (SELECT 1 FROM source_analysis_step step
			WHERE step.work_id=work.id AND step.step<>'metadata'
			AND NOT (
				(step.state='succeeded' AND CASE step.step
					WHEN 'sha256' THEN step.success_sha_variant_id IS NOT NULL
					WHEN 'probe' THEN step.success_probe_variant_id IS NOT NULL
					WHEN 'fingerprint' THEN step.success_fingerprint_result_id IS NOT NULL
					ELSE false
				END)
				OR (step.input_snapshot IS NULL AND (step.state='not_requested'
					OR (step.state='skipped' AND step.skip_reason IN ('disabled','not_requested'))))
			)), false)
		FROM source_analysis_work work
		LEFT JOIN source_analysis_step metadata ON metadata.work_id=work.id AND metadata.step='metadata'
		WHERE work.id=? AND work.current_location_id=?`, work.ID, location.ID).Scan(ctx, &ready)
	if err != nil {
		return fmt.Errorf("ensure source work variant identity: check durable readiness: %w", err)
	}
	if !ready {
		return nil
	}
	var shaStep SourceAnalysisStep
	err = tx.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='sha256' AND state='succeeded'`, work.ID).Scan(ctx, &shaStep)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("ensure source work variant identity: read successful SHA step: %w", err)
	}
	var anchorID uuid.UUID
	if err == nil && shaStep.SuccessSHAVariantID != nil {
		anchorID = *shaStep.SuccessSHAVariantID
	} else {
		var existingID uuid.UUID
		if err := tx.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=?`, work.ID).Scan(ctx, &existingID); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("ensure source work variant identity: read existing identity: %w", err)
		}
		if existingID != uuid.Nil {
			anchorID = existingID
		} else {
			anchorID = uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO media_variant(id,size_bytes) VALUES(?,?)`, anchorID, work.SizeBytes).Exec(ctx); err != nil {
				return fmt.Errorf("ensure source work variant identity: create digest-less anchor: %w", err)
			}
		}
	}
	var mappedID uuid.UUID
	err = tx.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=? FOR UPDATE`, work.ID).Scan(ctx, &mappedID)
	switch err {
	case nil:
		if mappedID != anchorID {
			return fmt.Errorf("ensure source work variant identity: completed work cannot change identity")
		}
	case sql.ErrNoRows:
		if _, err := tx.NewRaw(`INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES(?,?)`, work.ID, anchorID).Exec(ctx); err != nil {
			return fmt.Errorf("ensure source work variant identity: persist identity: %w", err)
		}
	default:
		return fmt.Errorf("ensure source work variant identity: lock existing identity: %w", err)
	}
	if _, err := tx.NewRaw(`UPDATE source_location SET media_variant_id=?,updated_at=now() WHERE id=?`, anchorID, location.ID).Exec(ctx); err != nil {
		return fmt.Errorf("ensure source work variant identity: link current location: %w", err)
	}
	return nil
}
