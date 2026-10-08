package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const (
	SourceAnalysisArtifactAcquiring       = "acquiring"
	SourceAnalysisArtifactReady           = "ready"
	SourceAnalysisArtifactCleanupEligible = "cleanup_eligible"
	SourceAnalysisArtifactCleanupFailed   = "cleanup_failed"
)

// SourceAnalysisArtifactFence identifies the running delivery that owns an
// artifact acquisition. JobID is the River job id, not an application UUID.
type SourceAnalysisArtifactFence struct {
	WorkID           uuid.UUID
	OperationID      uuid.UUID
	OperationAttempt int
	JobID            int64
}

// SourceAnalysisArtifact is the database ownership record for one staged copy.
// RelativeOutputPath is always relative to the current managed output directory.
type SourceAnalysisArtifact struct {
	bun.BaseModel `bun:"table:source_analysis_artifact"`

	ID                    uuid.UUID  `bun:"id,pk,type:uuid"`
	WorkID                uuid.UUID  `bun:"work_id,type:uuid"`
	RelativeOutputPath    string     `bun:"relative_output_path"`
	SourceSizeBytes       int64      `bun:"source_size_bytes"`
	SourceMtime           time.Time  `bun:"source_mtime"`
	OwnerOperationID      uuid.UUID  `bun:"owner_operation_id,type:uuid"`
	OwnerOperationAttempt int        `bun:"owner_operation_attempt"`
	OwnerJobID            int64      `bun:"owner_job_id"`
	State                 string     `bun:"state"`
	CleanupError          *string    `bun:"cleanup_error,nullzero"`
	CleanupAt             *time.Time `bun:"cleanup_at,nullzero"`
	CreatedAt             time.Time  `bun:"created_at,nullzero"`
	UpdatedAt             time.Time  `bun:"updated_at,nullzero"`
}

// SourceAnalysisArtifactRepository owns the database registry for staged
// source-analysis copies. Filesystem operations are intentionally outside it.
type SourceAnalysisArtifactRepository struct {
	db bun.IDB
}

func NewSourceAnalysisArtifactRepository(db *bun.DB) *SourceAnalysisArtifactRepository {
	return &SourceAnalysisArtifactRepository{db: db}
}

// Acquire records exclusive database ownership for the canonical staged path.
// It has no filesystem side effects; callers create the path only after this
// transaction succeeds.
func (repository *SourceAnalysisArtifactRepository) Acquire(ctx context.Context, artifactID uuid.UUID, fence SourceAnalysisArtifactFence) (*SourceAnalysisArtifact, error) {
	if artifactID == uuid.Nil || !validSourceAnalysisArtifactFence(fence) {
		return nil, fmt.Errorf("acquire source analysis artifact: valid artifact and delivery fence are required")
	}
	var artifact *SourceAnalysisArtifact
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		work, root, location, operation, err := lockSourceAnalysisArtifactOwner(ctx, tx, fence)
		if err != nil {
			return fmt.Errorf("acquire source analysis artifact: %w", err)
		}
		if root.ProcessingMode != "staged" || !root.Enabled || root.Stale() || root.InventoryPath == nil || *root.InventoryPath != root.ConfiguredPath ||
			work.SourceRootID != root.ID || work.LocationID != location.ID || work.ConfiguredPath != root.ConfiguredPath || work.InventoryPath != *root.InventoryPath ||
			location.SourceRootID != root.ID || location.RelativePath != work.RelativePath || location.SizeBytes != work.SizeBytes ||
			!sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(work.Mtime)) {
			return fmt.Errorf("acquire source analysis artifact: %w", ErrSourceAnalysisStale)
		}
		if operation.Kind != analysisSourceOperationKind || operation.State != "running" || operation.Attempt != fence.OperationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != fence.JobID || operation.SourceAnalysisMode == "" {
			return fmt.Errorf("acquire source analysis artifact: delivery is not current and running")
		}
		if err := verifySourceAnalysisArtifactExecution(ctx, tx, fence); err != nil {
			return err
		}

		artifact = new(SourceAnalysisArtifact)
		readErr := tx.NewRaw(`SELECT * FROM source_analysis_artifact WHERE id=? FOR UPDATE`, artifactID).Scan(ctx, artifact)
		if readErr == nil {
			if artifact.WorkID != work.ID || artifact.RelativeOutputPath != sourceAnalysisArtifactPath(root.ID, work.ID, artifactID) ||
				artifact.SourceSizeBytes != work.SizeBytes || !sourceAnalysisMtime(artifact.SourceMtime).Equal(sourceAnalysisMtime(work.Mtime)) ||
				!artifactOwnedByFence(artifact, fence) || (artifact.State != SourceAnalysisArtifactAcquiring && artifact.State != SourceAnalysisArtifactReady) {
				return fmt.Errorf("acquire source analysis artifact: artifact identity is already owned or incompatible")
			}
			return nil
		}
		if readErr != sql.ErrNoRows {
			return fmt.Errorf("acquire source analysis artifact: read existing artifact: %w", readErr)
		}

		artifact = &SourceAnalysisArtifact{
			ID: artifactID, WorkID: work.ID,
			RelativeOutputPath: sourceAnalysisArtifactPath(root.ID, work.ID, artifactID),
			SourceSizeBytes:    work.SizeBytes, SourceMtime: work.Mtime,
			OwnerOperationID: fence.OperationID, OwnerOperationAttempt: fence.OperationAttempt,
			OwnerJobID: fence.JobID, State: SourceAnalysisArtifactAcquiring,
		}
		if _, err := tx.NewInsert().Model(artifact).Exec(ctx); err != nil {
			return fmt.Errorf("acquire source analysis artifact: insert ownership record: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return artifact, nil
}

// MarkReady publishes the completed-copy state only for its exact current
// delivery. copiedBytes must equal the authoritative observed source size.
func (repository *SourceAnalysisArtifactRepository) MarkReady(ctx context.Context, artifactID uuid.UUID, fence SourceAnalysisArtifactFence, copiedBytes int64) (*SourceAnalysisArtifact, error) {
	if artifactID == uuid.Nil || !validSourceAnalysisArtifactFence(fence) {
		return nil, fmt.Errorf("mark source analysis artifact ready: valid artifact and delivery fence are required")
	}
	var artifact *SourceAnalysisArtifact
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		work, root, location, operation, err := lockSourceAnalysisArtifactOwner(ctx, tx, fence)
		if err != nil {
			return fmt.Errorf("mark source analysis artifact ready: %w", err)
		}
		if !sourceAnalysisArtifactOwnerCurrent(work, root, location) || operation.Kind != analysisSourceOperationKind || operation.State != "running" || operation.Attempt != fence.OperationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != fence.JobID || operation.SourceAnalysisMode == "" {
			return fmt.Errorf("mark source analysis artifact ready: delivery or source is stale")
		}
		if err := verifySourceAnalysisArtifactExecution(ctx, tx, fence); err != nil {
			return err
		}
		artifact = new(SourceAnalysisArtifact)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_artifact WHERE id=? FOR UPDATE`, artifactID).Scan(ctx, artifact); err != nil {
			return fmt.Errorf("mark source analysis artifact ready: read artifact: %w", err)
		}
		if artifact.WorkID != work.ID || !artifactOwnedByFence(artifact, fence) || artifact.RelativeOutputPath != sourceAnalysisArtifactPath(root.ID, work.ID, artifactID) ||
			artifact.SourceSizeBytes != work.SizeBytes || !sourceAnalysisMtime(artifact.SourceMtime).Equal(sourceAnalysisMtime(work.Mtime)) {
			return fmt.Errorf("mark source analysis artifact ready: artifact ownership or source identity mismatch")
		}
		if copiedBytes != artifact.SourceSizeBytes {
			return fmt.Errorf("mark source analysis artifact ready: copied length %d does not match expected length %d", copiedBytes, artifact.SourceSizeBytes)
		}
		if artifact.State == SourceAnalysisArtifactReady {
			return nil
		}
		if artifact.State != SourceAnalysisArtifactAcquiring {
			return fmt.Errorf("mark source analysis artifact ready: artifact is not acquiring")
		}
		if _, err := tx.NewRaw(`UPDATE source_analysis_artifact SET state='ready', updated_at=now() WHERE id=? AND state='acquiring'`, artifactID).Exec(ctx); err != nil {
			return fmt.Errorf("mark source analysis artifact ready: update state: %w", err)
		}
		artifact.State = SourceAnalysisArtifactReady
		return nil
	})
	if err != nil {
		return nil, err
	}
	return artifact, nil
}

// ForgetUncreated forgets only the exact fence's acquiring row. It deliberately
// never accesses the filesystem; callers may use it only when no file was
// created.
func (repository *SourceAnalysisArtifactRepository) ForgetUncreated(ctx context.Context, artifactID uuid.UUID, fence SourceAnalysisArtifactFence) error {
	if artifactID == uuid.Nil || !validSourceAnalysisArtifactFence(fence) {
		return fmt.Errorf("forget uncreated source analysis artifact: valid artifact and delivery fence are required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		work, _, _, _, err := lockSourceAnalysisArtifactOwner(ctx, tx, fence)
		if err != nil {
			return fmt.Errorf("forget uncreated source analysis artifact: %w", err)
		}
		artifact := new(SourceAnalysisArtifact)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_artifact WHERE id=? FOR UPDATE`, artifactID).Scan(ctx, artifact); err != nil {
			return fmt.Errorf("forget uncreated source analysis artifact: read artifact: %w", err)
		}
		if artifact.WorkID != work.ID || !artifactOwnedByFence(artifact, fence) || artifact.State != SourceAnalysisArtifactAcquiring {
			return fmt.Errorf("forget uncreated source analysis artifact: artifact is not an acquiring row owned by this delivery")
		}
		if _, err := tx.NewRaw(`DELETE FROM source_analysis_artifact WHERE id=? AND state='acquiring' AND owner_operation_id=? AND owner_operation_attempt=? AND owner_job_id=?`,
			artifactID, fence.OperationID, fence.OperationAttempt, fence.JobID).Exec(ctx); err != nil {
			return fmt.Errorf("forget uncreated source analysis artifact: delete ownership record: %w", err)
		}
		return nil
	})
}

func validSourceAnalysisArtifactFence(fence SourceAnalysisArtifactFence) bool {
	return fence.WorkID != uuid.Nil && fence.OperationID != uuid.Nil && fence.OperationAttempt > 0 && fence.JobID > 0
}

func sourceAnalysisArtifactPath(rootID, workID, artifactID uuid.UUID) string {
	return path.Join("analysis", "staging", strings.ToLower(rootID.String()), strings.ToLower(workID.String()), strings.ToLower(artifactID.String()))
}

func artifactOwnedByFence(artifact *SourceAnalysisArtifact, fence SourceAnalysisArtifactFence) bool {
	return artifact.OwnerOperationID == fence.OperationID && artifact.OwnerOperationAttempt == fence.OperationAttempt && artifact.OwnerJobID == fence.JobID
}

func sourceAnalysisArtifactOwnerCurrent(work *SourceAnalysisWork, root *SourceRoot, location *SourceLocation) bool {
	return root.ProcessingMode == "staged" && root.Enabled && !root.Stale() && root.InventoryPath != nil && *root.InventoryPath == root.ConfiguredPath &&
		work.SourceRootID == root.ID && work.ConfiguredPath == root.ConfiguredPath && work.InventoryPath == *root.InventoryPath && work.LocationID == location.ID &&
		location.SourceRootID == root.ID && location.RelativePath == work.RelativePath && location.SizeBytes == work.SizeBytes &&
		sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(work.Mtime))
}

// Lock order is root, location, work, operation, artifact. The initial work
// lookup is intentionally unlocked and is revalidated after acquiring locks.
func lockSourceAnalysisArtifactOwner(ctx context.Context, tx bun.Tx, fence SourceAnalysisArtifactFence) (*SourceAnalysisWork, *SourceRoot, *SourceLocation, *Operation, error) {
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("lock output admission gate: %w", err)
	}
	initial := new(SourceAnalysisWork)
	if err := tx.NewSelect().Model(initial).Column("source_root_id").Where("id=?", fence.WorkID).Scan(ctx); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read work identity: %w", err)
	}
	root := new(SourceRoot)
	if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, initial.SourceRootID).Scan(ctx, root); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("lock source root: %w", err)
	}
	location := new(SourceLocation)
	if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=(SELECT location_id FROM source_analysis_work WHERE id=?) AND source_root_id=? FOR UPDATE`, fence.WorkID, root.ID).Scan(ctx, location); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("lock source location: %w", err)
	}
	work := new(SourceAnalysisWork)
	if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? AND source_root_id=? AND location_id=? FOR UPDATE`, fence.WorkID, root.ID, location.ID).Scan(ctx, work); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("lock source analysis work: %w", err)
	}
	operation := new(Operation)
	if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, fence.OperationID).Scan(ctx, operation); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("lock operation: %w", err)
	}
	var held bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM operation_source_work_hold WHERE operation_id=? AND work_id=?)`, fence.OperationID, fence.WorkID).Scan(ctx, &held); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read work hold: %w", err)
	}
	if !held {
		return nil, nil, nil, nil, fmt.Errorf("delivery does not hold source analysis work")
	}
	return work, root, location, operation, nil
}

func verifySourceAnalysisArtifactExecution(ctx context.Context, tx bun.Tx, fence SourceAnalysisArtifactFence) error {
	var executing bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM source_analysis_step
		WHERE work_id=? AND execution_operation_id=? AND execution_operation_attempt=? AND execution_job_id=?
		  AND state IN ('queued','running')
	)`, fence.WorkID, fence.OperationID, fence.OperationAttempt, fence.JobID).Scan(ctx, &executing); err != nil {
		return fmt.Errorf("read source analysis execution fence: %w", err)
	}
	if !executing {
		return fmt.Errorf("source analysis delivery has no current work execution")
	}
	return nil
}
