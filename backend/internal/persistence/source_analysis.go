package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// analysisSourceOperationKind is the stored kind of a technical analysis of one
// source location. It is repeated here next to the apply that writes its result,
// mirroring sourceScanOperationKind, because the schema constrains the value.
const analysisSourceOperationKind = "analyze_source"

// SourceAnalysisSnapshotVersion is the schema version of the immutable input
// snapshot of an analyze_source operation. The apply refuses a snapshot of any
// other version instead of guessing at its shape.
const SourceAnalysisSnapshotVersion = 1

// SourceAnalysisPolicyVersion is the normalization policy the current analysis
// writes. It travels in the snapshot so a later policy change cannot relabel a
// result produced under an older policy.
const SourceAnalysisPolicyVersion = 1

var (
	// ErrSourceLocationNotFound reports a location read that names a location
	// the root does not own, or no location at all. Root ownership and existence
	// are one answer: a caller asking for a location of another root must not be
	// able to tell it apart from a location that does not exist.
	ErrSourceLocationNotFound = errors.New("source location not found")
	// ErrMediaVariantNotFound reports a variant read that names no stored result.
	ErrMediaVariantNotFound = errors.New("media variant not found")
	// ErrSourceAnalysisStale reports an analysis result refused because the
	// immutable snapshot no longer matches the persisted state: the file or the
	// inventory path moved, the location was replaced, or the previous variant
	// the operation holds is no longer the current one. The caller must start a
	// new analysis of the current file instead of applying an old snapshot.
	ErrSourceAnalysisStale = errors.New("source analysis snapshot is stale")
)

// SourceAnalysisSnapshot is the immutable identity of one source analysis. It is
// written once by the analysis start into operation.input_snapshot and is never
// rewritten; the apply decodes exactly this value from the persisted operation
// and fences the current root and location against it before it writes a result.
// It names the file by its owning root, its exact relative path and the file
// facts, and it records the inventory path and the previous variant the analysis
// holds, so a path change or a concurrent replacement is detected rather than
// attributed to another file.
type SourceAnalysisSnapshot struct {
	SchemaVersion          int        `json:"schema_version"`
	SourceRootID           uuid.UUID  `json:"source_root_id"`
	SourceLocationID       uuid.UUID  `json:"source_location_id"`
	ConfiguredPath         string     `json:"configured_path"`
	InventoryPath          string     `json:"inventory_path"`
	RelativePath           string     `json:"relative_path"`
	SizeBytes              int64      `json:"size_bytes"`
	Mtime                  time.Time  `json:"mtime"`
	PreviousVariantID      *uuid.UUID `json:"previous_variant_id"`
	AnalysisPolicyVersion  int        `json:"analysis_policy_version"`
	AnalysisInstallationID uuid.UUID  `json:"analysis_installation_id"`
}

// DecodeSourceAnalysisSnapshot parses the persisted input snapshot of an
// analyze_source operation and validates its shape. A snapshot of another
// version or with a missing required identity is an internal contract failure,
// never a result that could be applied.
func DecodeSourceAnalysisSnapshot(raw json.RawMessage) (SourceAnalysisSnapshot, error) {
	var snapshot SourceAnalysisSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return SourceAnalysisSnapshot{}, fmt.Errorf("decode source analysis snapshot: %w", err)
	}
	if snapshot.SchemaVersion != SourceAnalysisSnapshotVersion {
		return SourceAnalysisSnapshot{}, fmt.Errorf("decode source analysis snapshot: unsupported schema version %d", snapshot.SchemaVersion)
	}
	if snapshot.SourceRootID == uuid.Nil || snapshot.SourceLocationID == uuid.Nil || snapshot.AnalysisInstallationID == uuid.Nil {
		return SourceAnalysisSnapshot{}, fmt.Errorf("decode source analysis snapshot: root, location and installation are required")
	}
	if snapshot.ConfiguredPath == "" || snapshot.InventoryPath == "" || snapshot.RelativePath == "" {
		return SourceAnalysisSnapshot{}, fmt.Errorf("decode source analysis snapshot: paths are required")
	}
	if snapshot.InventoryPath != snapshot.ConfiguredPath {
		return SourceAnalysisSnapshot{}, fmt.Errorf("decode source analysis snapshot: the root was already stale when the analysis started")
	}
	if snapshot.SizeBytes < 0 || snapshot.Mtime.IsZero() {
		return SourceAnalysisSnapshot{}, fmt.Errorf("decode source analysis snapshot: file size and mtime are required")
	}
	if snapshot.AnalysisPolicyVersion < 1 {
		return SourceAnalysisSnapshot{}, fmt.Errorf("decode source analysis snapshot: analysis policy version must be positive")
	}
	return snapshot, nil
}

// SourceAnalysisApply is one successful technical analysis about to be
// persisted. RelativePath, SizeBytes and Mtime are the file identity the caller
// observed while analyzing; the apply requires them to agree with the immutable
// snapshot before it writes anything.
type SourceAnalysisApply struct {
	OperationID  uuid.UUID
	RelativePath string
	SizeBytes    int64
	Mtime        time.Time

	AnalysisPolicyVersion int
	FFProbeVersion        string
	FFProbeJSON           json.RawMessage
	ObservedTags          json.RawMessage
	InspectedAt           time.Time
}

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

// ApplyAnalysisResult writes one successful analysis as the current result of
// its location and finishes the operation, all in one transaction.
//
// The operation row, its source root and its location are locked in that order,
// which is the order a scan apply, a root edit and a root deletion use. Holding
// those locks, the method decodes the immutable snapshot from the persisted
// operation and fences everything against it: the result identity the caller
// observed must equal the snapshot file identity; the operation targets must
// equal the snapshot; the held previous variant must equal the snapshot variant;
// and the root and location rows must still be the enabled, non-stale, audio
// file the snapshot names, with the previous variant still current. Any
// disagreement is ErrSourceAnalysisStale and nothing is written, so the previous
// variant and its link stay exactly as they were.
//
// On success the new variant is inserted, the location points at it, the
// operation becomes succeeded, and both read holds (the previous variant and the
// FFmpeg installation) are cleared in the same transaction. The previous variant
// is then removed if no location links it and no operation holds it, which is
// the orphan cleanup the plan requires. The committed operation is returned
// after the transaction commits, so a caller can notify subscribers only once
// the result is durable.
//
// Applying an operation that already succeeded is a no-op: it returns the
// committed operation without inserting another variant or touching any row, so
// a duplicate River delivery cannot probe the file again or duplicate a result.
func (repository *SourceInventoryRepository) ApplyAnalysisResult(ctx context.Context, apply SourceAnalysisApply) (*Operation, error) {
	if apply.FFProbeVersion == "" {
		return nil, fmt.Errorf("apply analysis result: ffprobe version is required")
	}
	// A missing raw result is not an empty one: an analysis that did not capture
	// the ffprobe JSON must not become a successful technical snapshot.
	if len(apply.FFProbeJSON) == 0 {
		return nil, fmt.Errorf("apply analysis result: the raw ffprobe result is required")
	}
	if apply.ObservedTags == nil {
		apply.ObservedTags = json.RawMessage(`{}`)
	}
	var committed *Operation
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// The operation table lock comes first, the order an analysis start, a
		// root edit, a root deletion and a scan/analysis retry use. An apply that
		// took its operation row lock before this would hold the row while waiting
		// for the source root, while a root mutation holds the root and then waits
		// for the active operation row: a deadlock the shared table lock prevents.
		if _, err := tx.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("apply analysis result: lock operations: %w", err)
		}
		operation := new(Operation)
		if err := tx.NewRaw("SELECT * FROM operation WHERE id = ? FOR UPDATE", apply.OperationID).Scan(ctx, operation); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("apply analysis result: operation does not exist")
			}
			return fmt.Errorf("apply analysis result: lock operation: %w", err)
		}
		if operation.Kind != analysisSourceOperationKind {
			return fmt.Errorf("apply analysis result: operation is not a source analysis")
		}
		if operation.State == "succeeded" {
			// A duplicate delivery of an already applied analysis is a no-op: the
			// result is durable and must not be written twice.
			committed = operation
			return nil
		}
		if operation.State != "running" {
			return fmt.Errorf("apply analysis result: operation must be running to apply a result")
		}
		snapshot, err := DecodeSourceAnalysisSnapshot(operation.InputSnapshot)
		if err != nil {
			return fmt.Errorf("apply analysis result: %w", err)
		}
		// The immutable snapshot must agree with the operation columns step 6
		// wrote alongside it, including the read hold it names.
		if operation.TargetSourceRootID == nil || *operation.TargetSourceRootID != snapshot.SourceRootID ||
			operation.TargetSourceLocationID == nil || *operation.TargetSourceLocationID != snapshot.SourceLocationID ||
			operation.AnalysisInstallationID == nil || *operation.AnalysisInstallationID != snapshot.AnalysisInstallationID ||
			!sameOptionalUUID(operation.AnalysisMediaVariantID, snapshot.PreviousVariantID) {
			return fmt.Errorf("apply analysis result: %w", ErrSourceAnalysisStale)
		}
		if apply.AnalysisPolicyVersion != snapshot.AnalysisPolicyVersion {
			return fmt.Errorf("apply analysis result: analysis policy version %d does not match the snapshot %d", apply.AnalysisPolicyVersion, snapshot.AnalysisPolicyVersion)
		}
		// The result identity the caller observed must be the snapshot identity.
		if apply.RelativePath != snapshot.RelativePath || apply.SizeBytes != snapshot.SizeBytes ||
			!sourceAnalysisMtime(apply.Mtime).Equal(sourceAnalysisMtime(snapshot.Mtime)) {
			return fmt.Errorf("apply analysis result: %w", ErrSourceAnalysisStale)
		}
		root := new(SourceRoot)
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", snapshot.SourceRootID).Scan(ctx, root); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("apply analysis result: %w", ErrSourceAnalysisStale)
			}
			return fmt.Errorf("apply analysis result: lock source root: %w", err)
		}
		if !root.Enabled || root.Stale() || root.ConfiguredPath != snapshot.ConfiguredPath ||
			root.InventoryPath == nil || *root.InventoryPath != snapshot.InventoryPath {
			return fmt.Errorf("apply analysis result: %w", ErrSourceAnalysisStale)
		}
		location := new(SourceLocation)
		if err := tx.NewRaw("SELECT * FROM source_location WHERE id = ? FOR UPDATE", snapshot.SourceLocationID).Scan(ctx, location); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("apply analysis result: %w", ErrSourceAnalysisStale)
			}
			return fmt.Errorf("apply analysis result: lock source location: %w", err)
		}
		if location.SourceRootID != snapshot.SourceRootID || location.RelativePath != snapshot.RelativePath ||
			location.SizeBytes != snapshot.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(snapshot.Mtime)) ||
			location.ProbeStatus != SourceProbeStatusAudio ||
			!sameOptionalUUID(location.MediaVariantID, snapshot.PreviousVariantID) {
			return fmt.Errorf("apply analysis result: %w", ErrSourceAnalysisStale)
		}
		variant := &MediaVariant{
			ID:                    uuid.New(),
			SizeBytes:             snapshot.SizeBytes,
			AnalysisPolicyVersion: snapshot.AnalysisPolicyVersion,
			FFProbeVersion:        apply.FFProbeVersion,
			FFProbeJSON:           apply.FFProbeJSON,
			ObservedTags:          apply.ObservedTags,
			InspectedAt:           apply.InspectedAt,
			AppliedOperationID:    operation.ID,
		}
		if _, err := tx.NewInsert().Model(variant).Exec(ctx); err != nil {
			return fmt.Errorf("apply analysis result: insert media variant: %w", err)
		}
		if _, err := tx.NewUpdate().Model((*SourceLocation)(nil)).
			Set("media_variant_id = ?", variant.ID).
			Set("updated_at = now()").
			Where("id = ?", location.ID).Exec(ctx); err != nil {
			return fmt.Errorf("apply analysis result: link the location to its variant: %w", err)
		}
		now := time.Now().UTC()
		operation.State = "succeeded"
		operation.Stage = "applying"
		operation.FinishedAt = &now
		operation.SafeError = nil
		operation.AnalysisMediaVariantID = nil
		operation.AnalysisInstallationID = nil
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).
			Column("state", "stage", "safe_error", "analysis_media_variant_id", "analysis_installation_id", "finished_at", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("apply analysis result: finish the operation: %w", err)
		}
		if err := deleteOrphanedMediaVariants(ctx, tx); err != nil {
			return fmt.Errorf("apply analysis result: %w", err)
		}
		committed = operation
		return nil
	})
	if err != nil {
		return nil, err
	}
	return committed, nil
}

// deleteOrphanedMediaVariants removes every variant that no location links and
// no operation holds. A location link and an operation hold are the only reasons
// a variant must survive, so this is the single cleanup the analysis apply, a
// scan reconciliation and a root deletion call: a variant is deleted only inside
// the transaction that removed its last reference, never through the nullable
// foreign key because unlink was already written explicitly.
func deleteOrphanedMediaVariants(ctx context.Context, tx bun.IDB) error {
	if _, err := tx.NewRaw(
		`DELETE FROM media_variant AS variant
			 WHERE variant.source_sha256 IS NULL
			   AND NOT EXISTS (SELECT 1 FROM source_location AS location WHERE location.media_variant_id = variant.id)
			   AND NOT EXISTS (SELECT 1 FROM operation AS operation WHERE operation.analysis_media_variant_id = variant.id)
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
		   AND NOT EXISTS (SELECT 1 FROM media_fingerprint_cache AS cache WHERE cache.result_id = result.id)
		   AND NOT EXISTS (SELECT 1 FROM operation_source_work_hold AS hold
		       JOIN source_analysis_step AS step ON step.work_id = hold.work_id
		       WHERE hold.operation_id IN (SELECT id FROM operation WHERE state IN ('queued', 'running'))
		         AND step.success_fingerprint_result_id = result.id)`,
	).Exec(ctx); err != nil {
		return fmt.Errorf("delete orphaned source fingerprint results: %w", err)
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
