package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const analysisSourceOperationKind = "analyze_source"

// SourceAnalysisPolicyVersion is the normalization policy used for persisted
// probe results.
const SourceAnalysisPolicyVersion = 1

var (
	// ErrSourceLocationNotFound reports a location read that names a location
	// the root does not own, or no location at all. Root ownership and existence
	// are one answer: a caller asking for a location of another root must not be
	// able to tell it apart from a location that does not exist.
	ErrSourceLocationNotFound = errors.New("source location not found")
	// ErrMediaVariantNotFound reports a variant read that names no stored result.
	ErrMediaVariantNotFound = errors.New("media variant not found")
	// ErrSourceAnalysisStale reports normalized analysis work refused because its
	// source root, location, or execution fence no longer matches persisted state.
	ErrSourceAnalysisStale = errors.New("source analysis snapshot is stale")
)

// GetSourceLocation reads one location of a root. A location of another root is
// reported as ErrSourceLocationNotFound, so a caller can never address a file
// through a root it does not belong to.
func (repository *SourceInventoryRepository) GetSourceLocation(ctx context.Context, rootID, locationID uuid.UUID) (*SourceLocation, error) {
	location := new(SourceLocation)
	if err := repository.db.NewSelect().Model(location).
		Where("id = ?", locationID).Where("source_root_id = ?", rootID).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("get source location: %w", ErrSourceLocationNotFound)
		}
		return nil, fmt.Errorf("get source location: %w", err)
	}
	return location, nil
}

// GetMediaVariant reads the saved technical result of one analysis.
func (repository *SourceInventoryRepository) GetMediaVariant(ctx context.Context, id uuid.UUID) (*MediaVariant, error) {
	variant := new(MediaVariant)
	if err := repository.db.NewSelect().Model(variant).Where("id = ?", id).Where("ffprobe_version IS NOT NULL").Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("get media variant: %w", ErrMediaVariantNotFound)
		}
		return nil, fmt.Errorf("get media variant: %w", err)
	}
	return variant, nil
}

// GetSourceMediaVariant reads the nullable full result shape, preserving the
// distinction between SHA-only and a successful ffprobe result.
func (repository *SourceInventoryRepository) GetSourceMediaVariant(ctx context.Context, id uuid.UUID) (*SourceMediaVariant, error) {
	variant := new(SourceMediaVariant)
	if err := repository.db.NewSelect().Model(variant).Where("id = ?", id).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("get source media variant: %w", ErrMediaVariantNotFound)
		}
		return nil, fmt.Errorf("get source media variant: %w", err)
	}
	return variant, nil
}

// deleteOrphanedMediaVariants removes every variant that no location links and
// no operation holds. A location link and an operation hold are the only reasons
// a variant must survive, so this is the single cleanup the normalized analysis
// applies, scan reconciliation and root deletion call: a variant is deleted only inside
// the transaction that removed its last reference, never through the nullable
// foreign key because unlink was already written explicitly.
func deleteOrphanedMediaVariants(ctx context.Context, tx bun.IDB) error {
	if _, err := tx.NewRaw(
		`DELETE FROM media_variant AS variant
			 WHERE variant.source_sha256 IS NULL
			   AND NOT EXISTS (SELECT 1 FROM source_location AS location WHERE location.media_variant_id = variant.id)
			   AND NOT EXISTS (SELECT 1 FROM source_analysis_step AS step
			       WHERE step.success_sha_variant_id = variant.id OR step.success_probe_variant_id = variant.id)
			   AND NOT EXISTS (SELECT 1 FROM media_probe_cache AS cache WHERE cache.result_id = variant.id)
			   AND NOT EXISTS (SELECT 1 FROM operation_source_work_hold AS hold
			       JOIN source_analysis_step AS step ON step.work_id = hold.work_id
			       WHERE hold.operation_id IN (SELECT id FROM operation WHERE state IN ('queued', 'running'))
			         AND (step.success_sha_variant_id = variant.id OR step.success_probe_variant_id = variant.id))`,
	).Exec(ctx); err != nil {
		return fmt.Errorf("delete orphaned media variants: %w", err)
	}
	if _, err := tx.NewRaw(
		`DELETE FROM media_fingerprint_result AS result
			 WHERE NOT EXISTS (SELECT 1 FROM source_analysis_step AS step WHERE step.success_fingerprint_result_id = result.id)
			   AND result.source_sha256 IS NULL
			   AND NOT EXISTS (SELECT 1 FROM operation_source_work_hold AS hold
		       JOIN source_analysis_step AS step ON step.work_id = hold.work_id
		       WHERE hold.operation_id IN (SELECT id FROM operation WHERE state IN ('queued', 'running'))
		         AND step.success_fingerprint_result_id = result.id)`,
	).Exec(ctx); err != nil {
		return fmt.Errorf("delete orphaned source fingerprint results: %w", err)
	}
	if _, err := tx.NewRaw(
		`DELETE FROM media_metadata_result AS result
			 WHERE result.source_sha256 IS NULL
			   AND NOT EXISTS (SELECT 1 FROM source_analysis_step AS step WHERE step.success_metadata_result_id=result.id)
			   AND NOT EXISTS (SELECT 1 FROM operation_source_work_hold AS hold
			       JOIN source_analysis_step AS step ON step.work_id=hold.work_id
			       WHERE hold.operation_id IN (SELECT id FROM operation WHERE state IN ('queued','running'))
			         AND step.success_metadata_result_id=result.id)`,
	).Exec(ctx); err != nil {
		return fmt.Errorf("delete orphaned source metadata results: %w", err)
	}
	return nil
}

// sameOptionalUUID compares two nullable identifiers, treating nil as a value
// that must match nil.
func sameOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// sourceAnalysisMtime is the mtime at the precision the inventory stores it.
// PostgreSQL keeps microseconds, so the snapshot identity is compared at that
// precision instead of the nanosecond reading of a filesystem.
func sourceAnalysisMtime(value time.Time) time.Time {
	return value.Truncate(time.Microsecond)
}
