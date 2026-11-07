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

// SourceRootStatusAvailable marks a root whose configured path was readable at
// the last completed attempt. The other two values come from the schema check.
const (
	SourceRootStatusAvailable              = "available"
	SourceRootStatusUnavailable            = "unavailable"
	SourceProbeStatusNotAnalyzed           = "not_analyzed"
	SourceEnumerationRootUnavailableReason = "The configured source directory is unavailable or no longer readable."
)

// ErrSourceRootActiveScan reports an edit, a deletion or a scan start refused
// because a scan of the root is queued or running. The state is read under the
// same lock the write takes, so a scan cannot slip in between the check and the
// write.
var ErrSourceRootActiveScan = errors.New("source root has an active scan")

// ErrSourceRootConfirmation reports that the deletion confirmation no longer
// matches the root or its inventory at the point of deletion.
var ErrSourceRootConfirmation = errors.New("source root deletion confirmation does not match")

// Probe statuses written into source_location by a scan apply.
const (
	SourceProbeStatusAudio      = "audio"
	SourceProbeStatusNoAudio    = "no_audio"
	SourceProbeStatusProbeError = "probe_error"
)

// SourceScanCandidateInput is one traversed file a scan wants to apply. It
// carries no identity: identity belongs to the location row the apply reuses.
type SourceScanCandidateInput struct {
	RelativePath             string
	SizeBytes                int64
	Mtime                    time.Time
	ProbeStatus              string
	SafeError                *string
	SourceSHA256             []byte
	SHA256CalculatedAt       *time.Time
	SHA256AppliedOperationID *uuid.UUID
	AudioStreamCount         *int
	FFProbeVersion           *string         `bun:"ffprobe_version"`
	FFProbeJSON              json.RawMessage `bun:"ffprobe_json"`
	AnalysisPolicyVersion    *int
	ObservedTags             json.RawMessage
	InspectedAt              *time.Time
	ProbeAppliedOperationID  *uuid.UUID
	PreparedAnalysis         *SourceScanPreparedAnalysis `bun:"prepared_analysis,type:jsonb"`
}

// SourceLocationCursor is the stable pagination key of the location list. It
// holds both ordering columns so a page boundary is exact when two rows share a
// path (a path differing only by case, or a re-created file).
type SourceLocationCursor struct {
	RelativePath string
	ID           uuid.UUID
}

// SourceScanApply names one successful scan generation. The candidate rows of
// the operation are already durable, so the apply reads them inside its own
// transaction instead of trusting a slice another writer could have replaced
// after traversal. It accepts the generation only while the root still carries
// ExpectedConfiguredPath: a path change between traversal and apply discards
// the whole generation instead of attributing old files to a new path.
type SourceScanApply struct {
	OperationID            uuid.UUID
	ExpectedConfiguredPath string
	ExpectedAttempt        int
	ExpectedJobID          int64
	SHA256Enabled          *bool
}

// SourceEnumerationScope is one unreadable portion of an enumeration. The root
// scope has an empty path; all other paths are root-relative slash paths.
type SourceEnumerationScope struct {
	RelativePath string
	Kind         string
}

// SourceEnumerationApply carries only the observations that could not be stored
// in candidate rows. Candidate rows remain the durable source of file facts.
type SourceEnumerationApply struct {
	OperationID            uuid.UUID
	ExpectedConfiguredPath string
	ExpectedAttempt        int
	ExpectedJobID          int64
	SHA256Enabled          bool
	Scopes                 []SourceEnumerationScope
	FailureSafeError       string
}

// SourceScanUnavailable names a scan that found its registered directory
// unreadable, together with the safe reason to record on the root. The reason is
// what the UI shows instead of a raw diagnostic, so it must not be empty.
type SourceScanUnavailable struct {
	OperationID     uuid.UUID
	SafeError       string
	ExpectedAttempt int
	ExpectedJobID   int64
}

type SourceInventoryRepository struct {
	db bun.IDB
}

func NewSourceInventoryRepository(db *bun.DB) *SourceInventoryRepository {
	return &SourceInventoryRepository{db: db}
}

// Stale reports whether the last successful inventory describes a configured
// path other than the current one. A stale root must not present its old
// locations as files of the new path.
func (root *SourceRoot) Stale() bool {
	return root.InventoryPath != nil && *root.InventoryPath != root.ConfiguredPath
}

// CreateSourceRoot creates a root with generation 0 and no inventory. A supplied
// processing mode overrides the transitional database default; an empty mode
// leaves that default for legacy writers and migration fixtures. A root with a
// duplicate normalized configured path is rejected by the schema.
func (repository *SourceInventoryRepository) CreateSourceRoot(ctx context.Context, root *SourceRoot) error {
	if root.ID == uuid.Nil {
		root.ID = uuid.New()
	}
	if root.Status == "" {
		root.Status = "unknown"
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockWorkDirectoryGate(ctx, tx); err != nil {
			return err
		}
		if err := validateSourceRootAgainstWorkDirectory(ctx, tx, root.ConfiguredPath); err != nil {
			return err
		}
		insert := tx.NewInsert().Model(root)
		if root.ProcessingMode != "" {
			// The in_place database default is transitional for pre-mode writers and
			// migration fixtures; the source-root service always supplies a mode.
			// ProcessingMode is scan-only on the model so empty legacy values leave
			// the database default intact. Include every model column explicitly here
			// because Bun's Value appends an expression rather than selecting the
			// scan-only field from the model.
			insert = insert.Column(
				"id", "configured_path", "display_name", "processing_mode", "enabled",
				"scan_generation", "inventory_path", "last_successful_scan_at",
				"last_applied_operation_id", "status", "safe_error", "created_at", "updated_at",
			)
		}
		if _, err := insert.Exec(ctx); err != nil {
			return fmt.Errorf("create source root: %w", err)
		}
		return nil
	})
}

func (repository *SourceInventoryRepository) GetSourceRoot(ctx context.Context, id uuid.UUID) (*SourceRoot, error) {
	root := new(SourceRoot)
	if err := repository.db.NewSelect().Model(root).ColumnExpr("source_root.*").Where("id = ?", id).Scan(ctx); err != nil {
		return nil, fmt.Errorf("get source root: %w", err)
	}
	return root, nil
}

func (repository *SourceInventoryRepository) ListSourceRoots(ctx context.Context) ([]SourceRoot, error) {
	roots := make([]SourceRoot, 0)
	if err := repository.db.NewSelect().Model(&roots).
		ColumnExpr("source_root.*").
		Order("configured_path ASC").Order("id ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("list source roots: %w", err)
	}
	return roots, nil
}

// UpdateSourceRoot edits the operator-owned fields of a root: its display name,
// processing mode, enabled flag and configured path. The caller passes the values it read;
// the edit is refused when another writer advanced the root's generation between
// that read and this call, which is the value every scan apply moves. The row is
// locked for the read, the comparison and the write, so an edit racing a scan
// apply loses instead of resurrecting a generation the apply already advanced;
// a competing path is rejected by the schema's unique constraint.
//
// Changing configured_path deliberately leaves inventory_path, scan_generation
// and locations alone: the previous inventory stays readable but reports Stale()
// until a successful scan of the new path replaces it, which is what keeps the
// old files from being attributed to the new path.
//
// A configured_path or enabled change is refused with ErrSourceRootActiveScan
// while a scan of the root is queued or running: the path the running scan
// carries is the one it would publish, and a disabled root must not keep a scan
// it no longer owns. A processing-mode change is serialized without blocking
// queued work, which reads the current mode at execution. The operation source-root
// lock makes the check atomic against a scan or analysis starting.
func (repository *SourceInventoryRepository) UpdateSourceRoot(ctx context.Context, cas *SourceRoot) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockWorkDirectoryGate(ctx, tx); err != nil {
			return err
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("update source root: lock fingerprint mutations: %w", err)
		}
		current := new(SourceRoot)
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", cas.ID).Scan(ctx, current); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("update source root: root does not exist")
			}
			return fmt.Errorf("update source root: lock source root: %w", err)
		}
		if current.ScanGeneration != cas.ScanGeneration {
			return fmt.Errorf("update source root: root changed since it was read")
		}
		if cas.ConfiguredPath != current.ConfiguredPath || cas.Enabled != current.Enabled {
			active, err := activeSourceScan(ctx, tx, cas.ID)
			if err != nil {
				return fmt.Errorf("update source root: check active scan: %w", err)
			}
			if active {
				return fmt.Errorf("update source root: %w", ErrSourceRootActiveScan)
			}
		}
		if cas.ConfiguredPath != current.ConfiguredPath {
			if err := validateSourceRootAgainstWorkDirectory(ctx, tx, cas.ConfiguredPath); err != nil {
				return err
			}
			held, err := sourceHoldExists(ctx, tx, current.ID, uuid.Nil, uuid.Nil)
			if err != nil {
				return fmt.Errorf("update source root: check publication holds: %w", err)
			}
			if held {
				return fmt.Errorf("update source root: %w", ErrSourceRootActiveScan)
			}
		}
		update := tx.NewUpdate().Model((*SourceRoot)(nil)).
			Set("display_name = ?", cas.DisplayName).
			Set("configured_path = ?", cas.ConfiguredPath).
			Set("enabled = ?", cas.Enabled).
			Set("updated_at = now()").
			Where("id = ?", cas.ID)
		if cas.ProcessingMode != "" {
			update = update.Set("processing_mode = ?", cas.ProcessingMode)
		}
		if _, err := update.Exec(ctx); err != nil {
			return fmt.Errorf("update source root: %w", err)
		}
		if err := InvalidateIncomingGrouping(ctx, tx); err != nil {
			return fmt.Errorf("update source root: %w", err)
		}
		return nil
	})
}

// DeleteSourceRoot removes a root and every one of its locations in one
// transaction. It refuses roots with registered staged artifacts because this
// persistence-only entry point cannot clean their managed files.
func (repository *SourceInventoryRepository) DeleteSourceRoot(ctx context.Context, id uuid.UUID, confirmedPath string, confirmedLocations int64) error {
	return repository.DeleteSourceRootWithCleanup(ctx, id, confirmedPath, confirmedLocations, nil)
}

// DeleteSourceRootWithCleanup removes root-owned analysis state and invokes the
// registered-artifact callback while the root, work-directory and output
// admission locks remain held. Filesystem removal precedes database deletion;
// a failed callback or transaction rolls back the database and missing files
// are accepted by the callback on a subsequent request.
func (repository *SourceInventoryRepository) DeleteSourceRootWithCleanup(ctx context.Context, id uuid.UUID, confirmedPath string, confirmedLocations int64, cleanup func(context.Context, string, []SourceAnalysisArtifact) error) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockWorkDirectoryGate(ctx, tx); err != nil {
			return fmt.Errorf("delete source root: lock work directory configuration: %w", err)
		}
		if err := advisoryXactLock(ctx, tx, false, toolsCoordinationNamespace, outputAdmissionGateKey); err != nil {
			return fmt.Errorf("delete source root: lock output admission: %w", err)
		}
		var unresolvedReset bool
		if err := tx.NewRaw("SELECT EXISTS (SELECT 1 FROM output_reset_journal WHERE state <> 'finished')").Scan(ctx, &unresolvedReset); err != nil {
			return fmt.Errorf("delete source root: check output reset: %w", err)
		}
		if unresolvedReset {
			return fmt.Errorf("delete source root: output reset recovery is required")
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("delete source root: lock fingerprint mutations: %w", err)
		}
		root := new(SourceRoot)
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", id).Scan(ctx, root); err != nil {
			return fmt.Errorf("lock source root: %w", err)
		}
		if root.ConfiguredPath != confirmedPath {
			return fmt.Errorf("delete source root: confirmed path no longer matches: %w", ErrSourceRootConfirmation)
		}
		locationCount, err := tx.NewSelect().Model((*SourceLocation)(nil)).Where("source_root_id = ?", id).Count(ctx)
		if err != nil {
			return fmt.Errorf("count source locations for deletion: %w", err)
		}
		if int64(locationCount) != confirmedLocations {
			return fmt.Errorf("delete source root: confirmed location count no longer matches: %w", ErrSourceRootConfirmation)
		}
		active, err := activeSourceScan(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("check active source scan: %w", err)
		}
		if active {
			return fmt.Errorf("delete source root: %w", ErrSourceRootActiveScan)
		}
		var activeWorkConsumer bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM operation_source_work_hold hold
			JOIN source_analysis_work work ON work.id=hold.work_id
			JOIN operation op ON op.id=hold.operation_id
			WHERE work.source_root_id=? AND op.state IN ('queued','running')
		)`, id).Scan(ctx, &activeWorkConsumer); err != nil {
			return fmt.Errorf("delete source root: check active work consumers: %w", err)
		}
		if activeWorkConsumer {
			return fmt.Errorf("delete source root: %w", ErrSourceRootActiveScan)
		}
		var liveAnalysisDelivery bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM source_analysis_work_execution execution
			JOIN source_analysis_work work ON work.id=execution.work_id
			JOIN river_job job ON job.id=execution.job_id
			JOIN operation op ON op.id=execution.operation_id
			WHERE work.source_root_id=? AND (job.state='running'
				OR (op.state IN ('queued','running') AND job.state IN ('available','pending','retryable','scheduled')))
		)`, id).Scan(ctx, &liveAnalysisDelivery); err != nil {
			return fmt.Errorf("delete source root: check live analysis deliveries: %w", err)
		}
		if liveAnalysisDelivery {
			return fmt.Errorf("delete source root: %w", ErrSourceRootActiveScan)
		}
		held, err := sourceHoldExists(ctx, tx, id, uuid.Nil, uuid.Nil)
		if err != nil {
			return fmt.Errorf("delete source root: check publication holds: %w", err)
		}
		if held {
			return fmt.Errorf("delete source root: %w", ErrSourceRootActiveScan)
		}
		var works []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM source_analysis_work WHERE source_root_id=? ORDER BY id FOR UPDATE`, id).Scan(ctx, &works); err != nil {
			return fmt.Errorf("delete source root: lock root analysis work: %w", err)
		}
		var failedOperationIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT op.id FROM operation op
			WHERE op.kind='analyze_source' AND op.state='failed'
			AND (op.target_source_root_id=? OR EXISTS (
				SELECT 1 FROM source_analysis_work work WHERE work.source_root_id=? AND (
					EXISTS (SELECT 1 FROM source_analysis_work_execution execution WHERE execution.work_id=work.id AND execution.operation_id=op.id)
					OR op.input_snapshot->'work_ids' @> jsonb_build_array(work.id::text)
				))) ORDER BY op.id FOR UPDATE`, id, id).Scan(ctx, &failedOperationIDs); err != nil {
			return fmt.Errorf("delete source root: lock failed analysis operations: %w", err)
		}
		artifacts := make([]SourceAnalysisArtifact, 0)
		if err := tx.NewRaw(`SELECT artifact.* FROM source_analysis_artifact artifact
			JOIN source_analysis_work work ON work.id=artifact.work_id
			WHERE work.source_root_id=? ORDER BY artifact.id FOR UPDATE OF artifact`, id).Scan(ctx, &artifacts); err != nil {
			return fmt.Errorf("delete source root: lock staged artifacts: %w", err)
		}
		for _, artifact := range artifacts {
			if artifact.RelativeOutputPath != sourceAnalysisArtifactPath(id, artifact.WorkID, artifact.ID) {
				return fmt.Errorf("delete source root: staged artifact %s path does not match ownership", artifact.ID)
			}
		}
		if len(failedOperationIDs) != 0 {
			var deliveryStates []string
			if err := tx.NewRaw(`SELECT job.state FROM river_job job
				WHERE job.kind='analyze_source_v1' AND EXISTS (SELECT 1 FROM operation op WHERE op.id IN (?)
					AND (op.river_job_id=job.id OR job.args->>'operation_id'=op.id::text))
				ORDER BY job.id FOR UPDATE OF job`, bun.List(failedOperationIDs)).Scan(ctx, &deliveryStates); err != nil {
				return fmt.Errorf("delete source root: lock failed analysis deliveries: %w", err)
			}
			for _, state := range deliveryStates {
				if state == "running" {
					return fmt.Errorf("delete source root: %w", ErrSourceRootActiveScan)
				}
			}
		}
		var activeCleanup bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM source_analysis_artifact artifact
			JOIN source_analysis_work work ON work.id=artifact.work_id
			WHERE work.source_root_id=? AND (
				EXISTS (SELECT 1 FROM source_analysis_artifact_cleanup_item item WHERE item.artifact_id=artifact.id AND item.state='claimed')
				OR EXISTS (SELECT 1 FROM operation cleanup_operation
					WHERE cleanup_operation.kind='cleanup_source_analysis_artifacts' AND cleanup_operation.state IN ('queued','running')
					AND cleanup_operation.input_snapshot->'artifact_ids' @> jsonb_build_array(artifact.id::text))
			)
		)`, id).Scan(ctx, &activeCleanup); err != nil {
			return fmt.Errorf("delete source root: check artifact cleanup claims: %w", err)
		}
		if activeCleanup {
			return fmt.Errorf("delete source root: %w", ErrSourceRootActiveScan)
		}
		if len(artifacts) != 0 && cleanup == nil {
			return fmt.Errorf("delete source root: registered staged artifacts require cleanup")
		}
		if cleanup != nil {
			workRoot := ""
			if len(artifacts) != 0 {
				value, err := readOptionalSetting(ctx, tx, "work_directory")
				if err != nil {
					return fmt.Errorf("delete source root: read work directory: %w", err)
				}
				if value == nil || *value == "" {
					return fmt.Errorf("delete source root: work directory is unavailable for staged artifact cleanup")
				}
				workRoot = *value
			}
			if err := cleanup(ctx, workRoot, artifacts); err != nil {
				return fmt.Errorf("delete source root: clean registered staged artifacts: %w", err)
			}
		}
		var affectedVariantIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT variant_id FROM (
				SELECT media_variant_id AS variant_id FROM source_location
				WHERE source_root_id=? AND media_variant_id IS NOT NULL
				UNION
				SELECT identity.media_variant_id AS variant_id
				FROM source_analysis_work work
				JOIN source_work_variant_identity identity ON identity.work_id=work.id
				WHERE work.source_root_id=?
			) affected ORDER BY variant_id`, id, id).Scan(ctx, &affectedVariantIDs); err != nil {
			return fmt.Errorf("delete source root: list associated media variants: %w", err)
		}
		if err := deleteRootAnalysisState(ctx, tx, works, failedOperationIDs); err != nil {
			return fmt.Errorf("delete source root: remove root analysis state: %w", err)
		}
		removed, err := tx.NewDelete().Model((*SourceRoot)(nil)).Where("id = ?", id).Exec(ctx)
		if err != nil {
			return fmt.Errorf("delete source root: %w", err)
		}
		if count, _ := removed.RowsAffected(); count != 1 {
			return fmt.Errorf("delete source root: root does not exist")
		}
		// A variant may have associated tracks even when its final source location
		// disappears. Remove only associations for variants captured from this root
		// that have no remaining location anywhere; surviving variants remain cached.
		if err := deleteTrackSourcesWithoutLocations(ctx, tx, affectedVariantIDs); err != nil {
			return fmt.Errorf("delete source root: clean track source associations: %w", err)
		}
		if err := deleteOrphanedMediaVariants(ctx, tx); err != nil {
			return fmt.Errorf("delete source root: %w", err)
		}
		if err := InvalidateIncomingGrouping(ctx, tx); err != nil {
			return fmt.Errorf("delete source root: %w", err)
		}
		return nil
	})
}

// deleteTrackSourcesWithoutLocations removes associations for the affected
// variants only when no source location still refers to each variant. Callers
// must bound variantIDs to locations genuinely removed by that operation.
func deleteTrackSourcesWithoutLocations(ctx context.Context, tx bun.IDB, variantIDs []uuid.UUID) error {
	if len(variantIDs) == 0 {
		return nil
	}
	if _, err := tx.NewRaw(`DELETE FROM track_source association
		WHERE association.media_variant_id IN (?)
		AND NOT EXISTS (
			SELECT 1 FROM source_location location
			WHERE location.media_variant_id = association.media_variant_id
		)`, bun.List(variantIDs)).Exec(ctx); err != nil {
		return fmt.Errorf("delete track sources without locations: %w", err)
	}
	return nil
}

func deleteRootAnalysisState(ctx context.Context, tx bun.IDB, workIDs, operationIDs []uuid.UUID) error {
	if len(workIDs) > 0 {
		if _, err := tx.NewRaw(`DELETE FROM source_analysis_artifact_cleanup_item item
		USING source_analysis_artifact artifact
		WHERE item.artifact_id=artifact.id AND artifact.work_id IN (?)`, bun.List(workIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("delete artifact cleanup history: %w", err)
		}
		if _, err := tx.NewRaw("DELETE FROM source_analysis_artifact WHERE work_id IN (?)", bun.List(workIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("delete staged artifact ownership: %w", err)
		}
		if _, err := tx.NewRaw("DELETE FROM source_analysis_step WHERE work_id IN (?)", bun.List(workIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("delete source analysis steps: %w", err)
		}
		if _, err := tx.NewRaw("DELETE FROM source_analysis_work_execution WHERE work_id IN (?)", bun.List(workIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("delete source analysis execution history: %w", err)
		}
		if _, err := tx.NewRaw("DELETE FROM operation_source_work_hold WHERE work_id IN (?)", bun.List(workIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("delete source analysis work holds: %w", err)
		}
		if _, err := tx.NewRaw("DELETE FROM source_work_variant_identity WHERE work_id IN (?)", bun.List(workIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("delete source work variant identities: %w", err)
		}
		if _, err := tx.NewRaw("DELETE FROM source_analysis_work WHERE id IN (?)", bun.List(workIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("delete source analysis work: %w", err)
		}
	}
	if len(operationIDs) > 0 {
		if _, err := tx.NewRaw(`UPDATE river_job job SET state='cancelled',finalized_at=now()
			FROM operation op WHERE op.id IN (?) AND job.kind='analyze_source_v1'
			AND (job.id=op.river_job_id OR job.args->>'operation_id'=op.id::text)
			AND job.state IN ('available','pending','retryable','scheduled')`, bun.List(operationIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("cancel failed root analysis deliveries: %w", err)
		}
		if _, err := tx.NewRaw("DELETE FROM operation WHERE id IN (?)", bun.List(operationIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("delete failed root analysis operations: %w", err)
		}
	}
	return nil
}

// activeSourceScan reports whether a scan or an analysis of the root is still
// queued or running. Both kinds target the root, and the root exclusivity rule
// makes them mutually exclusive, so a root edit that changes its path or enabled
// state, a root deletion and a scan start all refuse while either is active.
// Callers hold the root row lock, which serializes this read against admission.
func activeSourceScan(ctx context.Context, database bun.IDB, rootID uuid.UUID) (bool, error) {
	var active uuid.UUID
	err := database.NewRaw("SELECT id FROM operation WHERE target_source_root_id = ? AND state IN ('queued', 'running') LIMIT 1", rootID).Scan(ctx, &active)
	if err == nil {
		return true, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	return false, nil
}

// CountSourceLocations counts the last successful inventory of a root. It never
// counts candidates of an unfinished scan.
func (repository *SourceInventoryRepository) CountSourceLocations(ctx context.Context, rootID uuid.UUID) (int64, error) {
	count, err := repository.db.NewSelect().Model((*SourceLocation)(nil)).Where("source_root_id = ?", rootID).Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count source locations: %w", err)
	}
	return int64(count), nil
}

// ListSourceLocationsPage returns one page ordered by (relative_path, id). A nil
// cursor starts at the first page; the returned next cursor is nil when the page
// is the last one.
func (repository *SourceInventoryRepository) ListSourceLocationsPage(ctx context.Context, rootID uuid.UUID, cursor *SourceLocationCursor, limit int) ([]SourceLocation, *SourceLocationCursor, error) {
	locations := make([]SourceLocation, 0, limit)
	query := repository.db.NewSelect().Model(&locations).Where("source_root_id = ?", rootID)
	if cursor != nil {
		query.Where("(relative_path, id) > (?, ?)", cursor.RelativePath, cursor.ID)
	}
	if err := query.Order("relative_path ASC").Order("id ASC").Limit(int64(limit)).Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("list source locations: %w", err)
	}
	if len(locations) < limit {
		return locations, nil, nil
	}
	last := locations[len(locations)-1]
	return locations, &SourceLocationCursor{RelativePath: last.RelativePath, ID: last.ID}, nil
}

// ReplaceSourceScanCandidates stores candidates of one operation as a whole
// batch. A retry re-runs the batch of the same operation, so the previous
// candidates of that operation are dropped in the same transaction.
func (repository *SourceInventoryRepository) ReplaceSourceScanCandidates(ctx context.Context, operationID uuid.UUID, candidates []SourceScanCandidateInput) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := deleteSourceScanCandidates(ctx, tx, operationID); err != nil {
			return err
		}
		return storeSourceScanCandidates(ctx, tx, operationID, candidates)
	})
}

// AppendSourceScanCandidates adds one batch of candidates to an operation
// without touching the batches already stored, so a traversal can persist
// results as it goes.
func (repository *SourceInventoryRepository) AppendSourceScanCandidates(ctx context.Context, operationID uuid.UUID, batch []SourceScanCandidateInput) error {
	return storeSourceScanCandidates(ctx, repository.db, operationID, batch)
}

// DeleteSourceScanCandidates drops the candidates of one operation. An empty
// candidate set is a normal outcome of a run that started after the previous
// one, so a missing operation is not an error.
func (repository *SourceInventoryRepository) DeleteSourceScanCandidates(ctx context.Context, operationID uuid.UUID) error {
	return deleteSourceScanCandidates(ctx, repository.db, operationID)
}

// ApplySourceScan installs the candidate batch as the next generation of the
// root. Everything in this method is one transaction: when the root is gone, the
// operation does not target it, the configured path moved, or the commit fails,
// the previous generation, its locations and inventory_path stay untouched. The
// generation advances and the pruning of unseen locations happen only after
// every candidate was written.
func (repository *SourceInventoryRepository) ApplySourceScan(ctx context.Context, apply SourceScanApply) error {
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("apply source scan: lock fingerprint mutations: %w", err)
		}
		rootID, err := operationTargetSourceRoot(ctx, tx, apply.OperationID)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("apply source scan: operation must be a scan of a source root")
			}
			return fmt.Errorf("apply source scan: read operation target: %w", err)
		}
		if rootID == nil {
			return fmt.Errorf("apply source scan: operation does not target a source root")
		}
		var root SourceRoot
		if err := tx.NewRaw("SELECT * FROM source_root WHERE id = ? FOR UPDATE", *rootID).Scan(ctx, &root); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("apply source scan: source root no longer exists")
			}
			return fmt.Errorf("apply source scan: lock source root: %w", err)
		}
		operation := new(Operation)
		if err := tx.NewRaw("SELECT * FROM operation WHERE id = ? FOR UPDATE", apply.OperationID).Scan(ctx, operation); err != nil {
			return fmt.Errorf("apply source scan: lock operation: %w", err)
		}
		if operation.Kind != sourceScanOperationKind || operation.State != "running" || operation.TargetSourceRootID == nil || *operation.TargetSourceRootID != root.ID || operation.Attempt != apply.ExpectedAttempt || operation.RiverJobID == nil || *operation.RiverJobID != apply.ExpectedJobID {
			return fmt.Errorf("apply source scan: delivery identity changed")
		}
		if root.ConfiguredPath != apply.ExpectedConfiguredPath {
			return fmt.Errorf("apply source scan: configured path changed since the scan started")
		}
		// The candidates of the generation are read from their durable rows inside
		// this transaction. The caller's slice is not consulted, so a traversal that
		// persisted nothing applies nothing, and rows another writer replaced after
		// traversal cannot be silently swapped for a different inventory.
		stored, err := loadSourceScanCandidates(ctx, tx, apply.OperationID)
		if err != nil {
			return fmt.Errorf("apply source scan: %w", err)
		}
		var missingVariantIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT DISTINCT location.media_variant_id
			FROM source_location location
			WHERE location.source_root_id = ? AND location.media_variant_id IS NOT NULL
			AND NOT EXISTS (
				SELECT 1 FROM source_scan_candidate candidate
				WHERE candidate.operation_id = ? AND candidate.relative_path = location.relative_path
			)
			ORDER BY location.media_variant_id`, root.ID, apply.OperationID).Scan(ctx, &missingVariantIDs); err != nil {
			return fmt.Errorf("apply source scan: list variants of missing locations: %w", err)
		}
		generation := root.ScanGeneration + 1
		if err := applySourceScanCandidates(ctx, tx, root, generation, stored); err != nil {
			return fmt.Errorf("apply source scan: %w", err)
		}
		for _, candidate := range stored {
			if candidate.PreparedAnalysis == nil {
				continue
			}
			if apply.SHA256Enabled == nil || apply.ExpectedAttempt < 1 || apply.ExpectedJobID < 1 {
				return fmt.Errorf("apply source scan: prepared analysis requires explicit policy and delivery identity")
			}
			if err := publishPreparedSourceScanAnalysis(ctx, tx, root, *operation, *apply.SHA256Enabled, stored); err != nil {
				return fmt.Errorf("apply source scan: %w", err)
			}
			break
		}
		if err := deleteTrackSourcesWithoutLocations(ctx, tx, missingVariantIDs); err != nil {
			return fmt.Errorf("apply source scan: clean track sources for missing locations: %w", err)
		}
		if err := deleteOrphanedMediaVariants(ctx, tx); err != nil {
			return fmt.Errorf("apply source scan: %w", err)
		}
		// Candidates are removed only now, after the generation was written in
		// full. A failed apply rolls the whole transaction back and leaves them
		// durable for the retry.
		if _, err := tx.NewDelete().Model((*SourceScanCandidate)(nil)).Where("operation_id = ?", apply.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("apply source scan: remove applied candidates: %w", err)
		}
		var publishedAt time.Time
		if err := tx.NewRaw("SELECT clock_timestamp()").Scan(ctx, &publishedAt); err != nil {
			return fmt.Errorf("apply source scan: read database clock: %w", err)
		}
		if _, err := tx.NewUpdate().Model((*SourceRoot)(nil)).
			Set("scan_generation = ?", generation).
			Set("inventory_path = ?", root.ConfiguredPath).
			Set("last_successful_scan_at = ?", publishedAt).
			Set("last_applied_operation_id = ?", apply.OperationID).
			Set("status = ?", SourceRootStatusAvailable).
			Set("safe_error = NULL").
			Set("updated_at = ?", publishedAt).
			Where("id = ?", root.ID).Exec(ctx); err != nil {
			return fmt.Errorf("apply source scan: record successful scan: %w", err)
		}
		if err := InvalidateIncomingGrouping(ctx, tx); err != nil {
			return fmt.Errorf("apply source scan: %w", err)
		}
		return nil
	})
}

// MarkSourceRootUnavailable records that the registered directory of a scan is
// currently unreadable, so the UI can show the last successful inventory
// together with a truthful reason. Only status, safe_error and updated_at change:
// scan_generation, inventory_path, last_successful_scan_at,
// last_applied_operation_id and every location stay exactly as the last
// successful scan left them, because an unavailable root keeps its inventory.
//
// The write is refused as a no-op when the operation's current attempt began
// before the last successful scan of the root: a report from a scan a newer
// generation already superseded must not overwrite the availability that
// generation established. The attempt origin must not move when the operation
// later reaches a terminal state, so it is taken from the earliest durable
// record of the current attempt: started_at when the attempt ran, otherwise
// created_at for a first attempt and updated_at for a retry, which the retry
// transaction writes while it increments attempt. The root row is locked for the
// check and the write, so this mark and a successful apply serialize on the row
// and the later writer decides the final state.
func (repository *SourceInventoryRepository) MarkSourceRootUnavailable(ctx context.Context, unavailable SourceScanUnavailable) error {
	if unavailable.SafeError == "" {
		return fmt.Errorf("mark source root unavailable: a safe error is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		operation := new(Operation)
		if err := tx.NewSelect().Model(operation).
			Where("id = ?", unavailable.OperationID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("mark source root unavailable: operation does not exist")
			}
			return fmt.Errorf("mark source root unavailable: read operation: %w", err)
		}
		if operation.TargetSourceRootID == nil {
			return fmt.Errorf("mark source root unavailable: operation does not target a source root")
		}
		var lockedRootID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM source_root WHERE id=? FOR UPDATE`, *operation.TargetSourceRootID).Scan(ctx, &lockedRootID); err != nil {
			return fmt.Errorf("mark source root unavailable: lock source root: %w", err)
		}
		if unavailable.ExpectedAttempt > 0 {
			locked := new(Operation)
			if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, unavailable.OperationID).Scan(ctx, locked); err != nil {
				return fmt.Errorf("mark source root unavailable: lock scan delivery: %w", err)
			}
			if locked.TargetSourceRootID == nil || *locked.TargetSourceRootID != *operation.TargetSourceRootID || locked.Attempt != unavailable.ExpectedAttempt || locked.RiverJobID == nil || *locked.RiverJobID != unavailable.ExpectedJobID {
				return fmt.Errorf("mark source root unavailable: %w", ErrSourceAnalysisStale)
			}
			operation = locked
		}
		attemptStartedAt := operation.CreatedAt
		if operation.Attempt > 1 {
			attemptStartedAt = operation.UpdatedAt
		}
		if operation.StartedAt != nil {
			attemptStartedAt = *operation.StartedAt
		}
		return markSourceRootUnavailable(ctx, tx, *operation.TargetSourceRootID, unavailable.SafeError, func(root *SourceRoot) bool {
			return root.LastSuccessfulScanAt != nil && !attemptStartedAt.After(*root.LastSuccessfulScanAt)
		})
	})
}

// operationTargetSourceRoot reads the root an operation targets, distinguishing
// a missing operation from a non-scan one without decoding the snapshot, whose
// shape belongs to the service layer.
func operationTargetSourceRoot(ctx context.Context, database bun.IDB, operationID uuid.UUID) (*uuid.UUID, error) {
	var target *uuid.UUID
	if err := database.NewRaw("SELECT target_source_root_id FROM operation WHERE id = ?", operationID).Scan(ctx, &target); err != nil {
		return nil, err
	}
	return target, nil
}

// deleteSourceScanCandidates drops the candidate rows of one operation.
func deleteSourceScanCandidates(ctx context.Context, database bun.IDB, operationID uuid.UUID) error {
	if _, err := database.NewDelete().Model((*SourceScanCandidate)(nil)).Where("operation_id = ?", operationID).Exec(ctx); err != nil {
		return fmt.Errorf("delete scan candidates: %w", err)
	}
	return nil
}
