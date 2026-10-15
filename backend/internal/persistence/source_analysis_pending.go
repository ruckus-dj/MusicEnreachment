package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SourceAnalysisPendingWork is current immutable work with only its pending steps.
type SourceAnalysisPendingWork struct {
	Work     SourceAnalysisWork
	Root     SourceRoot
	Location SourceLocation
	Steps    []SourceAnalysisStep
}

// ListPendingSourceAnalysisRoots returns enabled, current roots with pending
// work and no active normalized analysis. Failed work is deliberately excluded.
func (repository *SourceInventoryRepository) ListPendingSourceAnalysisRoots(ctx context.Context) ([]SourceRoot, error) {
	var roots []SourceRoot
	err := repository.db.NewSelect().Model(&roots).
		Where("source_root.enabled = TRUE").
		Where("source_root.inventory_path = source_root.configured_path").
		Where("NOT EXISTS (SELECT 1 FROM operation o WHERE o.target_source_root_id=source_root.id AND o.kind='analyze_source' AND o.state IN ('queued','running'))").
		Where(`EXISTS (SELECT 1 FROM source_analysis_work w JOIN source_analysis_step s ON s.work_id=w.id
			WHERE w.source_root_id=source_root.id AND w.configured_path=source_root.configured_path
			AND w.inventory_path=source_root.inventory_path AND w.current_location_id IS NOT NULL
			AND w.current_location_id=w.location_id AND s.state='pending')`).
		Order("source_root.id").Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending source analysis roots: %w", err)
	}
	return roots, nil
}

// ListPendingSourceAnalysisWork reads current, immutable work and only its
// pending step rows. Admission revalidates all of these facts under locks.
func (repository *SourceInventoryRepository) ListPendingSourceAnalysisWork(ctx context.Context, rootID uuid.UUID) ([]SourceAnalysisPendingWork, error) {
	return repository.readPendingSourceAnalysisWork(ctx, rootID, nil)
}

// ReadPendingSourceAnalysisWorkBatch rereads only the requested work identities.
func (repository *SourceInventoryRepository) ReadPendingSourceAnalysisWorkBatch(ctx context.Context, rootID uuid.UUID, workIDs []uuid.UUID) ([]SourceAnalysisPendingWork, error) {
	if len(workIDs) == 0 {
		return nil, nil
	}
	return repository.readPendingSourceAnalysisWork(ctx, rootID, workIDs)
}

func (repository *SourceInventoryRepository) readPendingSourceAnalysisWork(ctx context.Context, rootID uuid.UUID, workIDs []uuid.UUID) ([]SourceAnalysisPendingWork, error) {
	var rows []SourceAnalysisPendingWork
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		root := new(SourceRoot)
		if err := tx.NewSelect().Model(root).Where("id=?", rootID).Scan(ctx); err != nil {
			return fmt.Errorf("read pending source analysis root: %w", err)
		}
		if !root.Enabled || root.Stale() || root.InventoryPath == nil || *root.InventoryPath != root.ConfiguredPath {
			return nil
		}
		works := make([]SourceAnalysisWork, 0)
		query := tx.NewSelect().Model(&works).Where("source_root_id=?", rootID).
			Where("configured_path=? AND inventory_path=?", root.ConfiguredPath, *root.InventoryPath).
			Where("current_location_id IS NOT NULL AND current_location_id=location_id").
			Where("EXISTS (SELECT 1 FROM source_analysis_step s WHERE s.work_id=source_analysis_work.id AND s.state='pending')")
		if workIDs == nil {
			if err := query.Order("id").Scan(ctx); err != nil {
				return fmt.Errorf("list pending source analysis work: %w", err)
			}
		} else {
			for _, chunk := range uuidChunks(workIDs) {
				batch := make([]SourceAnalysisWork, 0, len(chunk))
				if err := tx.NewSelect().Model(&batch).Where("source_root_id=?", rootID).
					Where("configured_path=? AND inventory_path=?", root.ConfiguredPath, *root.InventoryPath).
					Where("current_location_id IS NOT NULL AND current_location_id=location_id").
					Where("EXISTS (SELECT 1 FROM source_analysis_step s WHERE s.work_id=source_analysis_work.id AND s.state='pending')").
					Where("id IN (?)", bun.List(chunk)).Order("id").Scan(ctx); err != nil {
					return fmt.Errorf("list pending source analysis work: %w", err)
				}
				works = append(works, batch...)
			}
		}
		if len(works) == 0 {
			return nil
		}
		locationIDs := make([]uuid.UUID, 0, len(works))
		pendingWorkIDs := make([]uuid.UUID, 0, len(works))
		for _, work := range works {
			locationIDs = append(locationIDs, work.LocationID)
			pendingWorkIDs = append(pendingWorkIDs, work.ID)
		}
		locations := make([]SourceLocation, 0, len(works))
		for _, chunk := range uuidChunks(locationIDs) {
			var batch []SourceLocation
			if err := tx.NewSelect().Model(&batch).Where("source_root_id=?", rootID).Where("id IN (?)", bun.List(chunk)).Scan(ctx); err != nil {
				return fmt.Errorf("read pending source analysis locations: %w", err)
			}
			locations = append(locations, batch...)
		}
		locationByID := make(map[uuid.UUID]SourceLocation, len(locations))
		for _, location := range locations {
			locationByID[location.ID] = location
		}
		steps := make([]SourceAnalysisStep, 0, len(works)*3)
		for _, chunk := range uuidChunks(pendingWorkIDs) {
			var batch []SourceAnalysisStep
			if err := tx.NewSelect().Model(&batch).Where("work_id IN (?)", bun.List(chunk)).Where("state='pending'").Order("work_id", "step").Scan(ctx); err != nil {
				return fmt.Errorf("list pending source analysis steps: %w", err)
			}
			steps = append(steps, batch...)
		}
		stepsByWork := make(map[uuid.UUID][]SourceAnalysisStep, len(works))
		for _, step := range steps {
			stepsByWork[step.WorkID] = append(stepsByWork[step.WorkID], step)
		}
		for _, work := range works {
			location, exists := locationByID[work.LocationID]
			if !exists {
				continue
			}
			if location.RelativePath != work.RelativePath || location.SizeBytes != work.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(work.Mtime)) {
				continue
			}
			workSteps := stepsByWork[work.ID]
			if len(workSteps) != 0 {
				rows = append(rows, SourceAnalysisPendingWork{Work: work, Root: *root, Location: location, Steps: workSteps})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Work.ID.String() < rows[j].Work.ID.String() })
	return rows, nil
}

func uuidChunks(ids []uuid.UUID) [][]uuid.UUID {
	const chunkSize = 500
	chunks := make([][]uuid.UUID, 0, (len(ids)+chunkSize-1)/chunkSize)
	for len(ids) > 0 {
		size := min(len(ids), chunkSize)
		chunks = append(chunks, ids[:size])
		ids = ids[size:]
	}
	return chunks
}

// FailPendingSourceAnalysisStep records a safe prerequisite failure without
// manufacturing an operation. It cannot overwrite a step another admission
// already queued or claimed.
func (repository *SourceInventoryRepository) FailPendingSourceAnalysisStep(ctx context.Context, rootID, workID uuid.UUID, step SourceStepName, safeError string) (bool, error) {
	if rootID == uuid.Nil || workID == uuid.Nil || !validSourceStep(step) || safeError == "" {
		return false, fmt.Errorf("fail pending source analysis step: valid identity and safe error are required")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveReaders(ctx, tx); err != nil {
			return fmt.Errorf("fail pending source analysis step: lock tools-root readers: %w", err)
		}
		moving, err := activeToolsMoveExists(ctx, tx)
		if err != nil {
			return fmt.Errorf("fail pending source analysis step: check tools-root move: %w", err)
		}
		if moving {
			return fmt.Errorf("fail pending source analysis step: %w", ErrToolsRootMoveActive)
		}
		root := new(SourceRoot)
		if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, rootID).Scan(ctx, root); err != nil {
			return fmt.Errorf("fail pending source analysis step: lock root: %w", err)
		}
		var active bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation WHERE target_source_root_id=? AND state IN ('queued','running') AND kind IN ('scan_source','analyze_source'))`, rootID).Scan(ctx, &active); err != nil {
			return fmt.Errorf("fail pending source analysis step: check active root operation: %w", err)
		}
		if active {
			return nil
		}
		var locationID uuid.UUID
		if err := tx.NewRaw(`SELECT location_id FROM source_analysis_work WHERE id=? AND source_root_id=?`, workID, rootID).Scan(ctx, &locationID); err != nil {
			return fmt.Errorf("fail pending source analysis step: read work: %w", err)
		}
		location := new(SourceLocation)
		if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, locationID, rootID).Scan(ctx, location); err != nil {
			return fmt.Errorf("fail pending source analysis step: lock location: %w", err)
		}
		work := new(SourceAnalysisWork)
		if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? AND source_root_id=? FOR UPDATE`, workID, rootID).Scan(ctx, work); err != nil {
			return fmt.Errorf("fail pending source analysis step: lock work: %w", err)
		}
		if !root.Enabled || root.Stale() || root.InventoryPath == nil || *root.InventoryPath != work.InventoryPath || root.ConfiguredPath != work.ConfiguredPath || work.CurrentLocationID == nil || *work.CurrentLocationID != work.LocationID || *work.CurrentLocationID != location.ID || location.RelativePath != work.RelativePath || location.SizeBytes != work.SizeBytes || !sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(work.Mtime)) {
			return nil
		}
		result, err := tx.NewRaw(`UPDATE source_analysis_step SET state='failed',safe_error=?,skip_reason=NULL,updated_at=now()
			WHERE work_id=? AND step=? AND state='pending'`, safeError, workID, step).Exec(ctx)
		if err != nil {
			return fmt.Errorf("fail pending source analysis step: update step: %w", err)
		}
		count, err := result.RowsAffected()
		changed = count != 0
		return err
	})
	return changed, err
}
