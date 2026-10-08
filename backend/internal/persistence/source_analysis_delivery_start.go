package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	managedtools "github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/uptrace/bun"
)

// StartNormalizedSourceAnalysisDelivery atomically fences a queued River
// delivery, reads the current root mode and active executables, and takes the
// installation holds used by this attempt.
func (repository *SourceInventoryRepository) StartNormalizedSourceAnalysisDelivery(
	ctx context.Context,
	operationID uuid.UUID,
	delivery SourceAnalysisOperationDelivery,
	platformGOOS, platformGOARCH string,
) (*Operation, []SourceAnalysisToolSelection, string, error) {
	if operationID == uuid.Nil || delivery.Attempt < 1 || delivery.JobID < 1 {
		return nil, nil, "", fmt.Errorf("start source analysis delivery: valid delivery identity is required")
	}
	var started *Operation
	var selections []SourceAnalysisToolSelection
	var processingMode string
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("start source analysis delivery: lock output admission gate: %w", err)
		}
		operation := new(Operation)
		if err := tx.NewRaw(`SELECT * FROM operation WHERE id=?`, operationID).Scan(ctx, operation); err != nil {
			return fmt.Errorf("start source analysis delivery: read operation: %w", err)
		}
		if operation.Kind != "analyze_source" || operation.State != "queued" || operation.Attempt != delivery.Attempt || operation.RiverJobID == nil || *operation.RiverJobID != delivery.JobID || operation.TargetSourceRootID == nil {
			return fmt.Errorf("start source analysis delivery: %w", ErrSourceAnalysisStale)
		}
		var steps []string
		if err := tx.NewRaw(`SELECT step FROM source_analysis_step WHERE execution_operation_id=? AND execution_operation_attempt=? AND execution_job_id=? AND state='queued' ORDER BY step`, operationID, delivery.Attempt, delivery.JobID).Scan(ctx, &steps); err != nil {
			return fmt.Errorf("start source analysis delivery: read queued steps: %w", err)
		}
		needsProbe, needsFingerprint := false, false
		for _, step := range steps {
			needsProbe = needsProbe || step == string(SourceStepProbe)
			needsFingerprint = needsFingerprint || step == string(SourceStepFingerprint)
		}
		packageKinds := make([]string, 0, 2)
		if needsProbe {
			packageKinds = append(packageKinds, "ffmpeg")
		}
		if needsFingerprint {
			packageKinds = append(packageKinds, "fpcalc")
		}
		if len(packageKinds) > 0 {
			if err := lockToolsMoveReaders(ctx, tx); err != nil {
				return fmt.Errorf("start source analysis delivery: lock tools-root readers: %w", err)
			}
			if err := lockPackageSelections(ctx, tx, packageKinds); err != nil {
				return fmt.Errorf("start source analysis delivery: lock selected packages: %w", err)
			}
		}
		for _, required := range []struct {
			needed                    bool
			kind, setting, executable string
		}{
			{needsProbe, "ffmpeg", "active_ffmpeg_installation_id", "ffprobe"},
			{needsFingerprint, "fpcalc", "active_fpcalc_installation_id", "fpcalc"},
		} {
			if !required.needed {
				continue
			}
			var rawID string
			if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name=?`, required.setting).Scan(ctx, &rawID); err != nil {
				return fmt.Errorf("start source analysis delivery: read current %s selection: %w", required.kind, err)
			}
			id, err := uuid.Parse(rawID)
			if err != nil || id == uuid.Nil {
				return fmt.Errorf("start source analysis delivery: current %s selection is unavailable", required.kind)
			}
			selections = append(selections, SourceAnalysisToolSelection{PackageKind: required.kind, InstallationID: id})
		}
		installationIDs := make([]uuid.UUID, 0, len(selections))
		for _, selection := range selections {
			installationIDs = append(installationIDs, selection.InstallationID)
		}
		if len(installationIDs) > 0 {
			if err := lockCompatibleInstallations(ctx, tx, installationIDs); err != nil {
				return fmt.Errorf("start source analysis delivery: lock current installations: %w", err)
			}
		}
		for _, required := range []struct {
			needed                    bool
			kind, setting, executable string
		}{
			{needsProbe, "ffmpeg", "active_ffmpeg_installation_id", "ffprobe"},
			{needsFingerprint, "fpcalc", "active_fpcalc_installation_id", "fpcalc"},
		} {
			if !required.needed {
				continue
			}
			selectionIndex := -1
			for index := range selections {
				if selections[index].PackageKind == required.kind {
					selectionIndex = index
					break
				}
			}
			if selectionIndex < 0 {
				return fmt.Errorf("start source analysis delivery: current %s selection is unavailable", required.kind)
			}
			selection := selections[selectionIndex]
			id := selection.InstallationID
			installation := new(ToolInstallation)
			if err := tx.NewRaw(`SELECT * FROM tool_installation WHERE id=?`, id).Scan(ctx, installation); err != nil {
				return fmt.Errorf("start source analysis delivery: read current %s installation: %w", required.kind, err)
			}
			if installation.PackageKind != required.kind || installation.State != "ready" || installation.PlatformGOOS != platformGOOS || installation.PlatformGOARCH != platformGOARCH || installation.VerifiedAt == nil {
				return fmt.Errorf("start source analysis delivery: current %s installation is unavailable", required.kind)
			}
			var versions map[string]string
			if err := json.Unmarshal(installation.ExecutableVersions, &versions); err != nil {
				return fmt.Errorf("start source analysis delivery: decode current executable versions: %w", err)
			}
			banner, ok := managedtools.VerifiedExecutableVersion(versions, managedtools.PackageKind(required.kind), required.executable, installation.PlatformGOOS)
			if !ok {
				return fmt.Errorf("start source analysis delivery: current %s executable is not verified", required.kind)
			}
			version := ""
			if required.executable == "fpcalc" {
				parsed, err := managedtools.ParseFPCalcVersion(banner)
				if err != nil {
					return fmt.Errorf("start source analysis delivery: invalid fpcalc version: %w", err)
				}
				version, banner = parsed.Version, parsed.Banner
			} else {
				fields := strings.Fields(banner)
				if len(fields) < 3 || fields[0] != "ffprobe" || fields[1] != "version" {
					return fmt.Errorf("start source analysis delivery: invalid ffprobe version")
				}
				version = fields[2]
			}
			selections[selectionIndex] = SourceAnalysisToolSelection{PackageKind: required.kind, InstallationID: id, RelativePath: installation.RelativePath, Executable: required.executable, Version: version, VersionBanner: banner}
		}
		root := new(SourceRoot)
		if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, *operation.TargetSourceRootID).Scan(ctx, root); err != nil {
			return fmt.Errorf("start source analysis delivery: lock source root: %w", err)
		}
		if !root.Enabled || root.Stale() || root.InventoryPath == nil || *root.InventoryPath != root.ConfiguredPath {
			return fmt.Errorf("start source analysis delivery: %w", ErrSourceAnalysisStale)
		}
		type heldWork struct {
			WorkID     uuid.UUID `bun:"work_id,type:uuid"`
			LocationID uuid.UUID `bun:"location_id,type:uuid"`
		}
		var works []heldWork
		if err := tx.NewRaw(`SELECT DISTINCT w.id AS work_id,w.current_location_id AS location_id FROM source_analysis_step s JOIN source_analysis_work w ON w.id=s.work_id WHERE s.execution_operation_id=? AND s.execution_operation_attempt=? AND s.execution_job_id=? AND s.state='queued' AND w.source_root_id=? AND w.current_location_id IS NOT NULL AND w.current_location_id=w.location_id ORDER BY w.current_location_id`, operationID, delivery.Attempt, delivery.JobID, root.ID).Scan(ctx, &works); err != nil {
			return fmt.Errorf("start source analysis delivery: read queued work: %w", err)
		}
		var queuedWorkCount int
		if err := tx.NewRaw(`SELECT count(DISTINCT w.id) FROM source_analysis_step s JOIN source_analysis_work w ON w.id=s.work_id
			WHERE s.execution_operation_id=? AND s.execution_operation_attempt=? AND s.execution_job_id=? AND s.state='queued' AND w.source_root_id=?`,
			operationID, delivery.Attempt, delivery.JobID, root.ID).Scan(ctx, &queuedWorkCount); err != nil {
			return fmt.Errorf("start source analysis delivery: count queued work: %w", err)
		}
		if len(works) != queuedWorkCount {
			return fmt.Errorf("start source analysis delivery: %w", ErrSourceAnalysisStale)
		}
		for _, work := range works {
			location := new(SourceLocation)
			if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, work.LocationID, root.ID).Scan(ctx, location); err != nil {
				return fmt.Errorf("start source analysis delivery: lock source location: %w", err)
			}
			lockedWork := new(SourceAnalysisWork)
			if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? AND source_root_id=? FOR UPDATE`, work.WorkID, root.ID).Scan(ctx, lockedWork); err != nil {
				return fmt.Errorf("start source analysis delivery: lock source work: %w", err)
			}
			if lockedWork.CurrentLocationID == nil || *lockedWork.CurrentLocationID != lockedWork.LocationID || *lockedWork.CurrentLocationID != location.ID || lockedWork.ConfiguredPath != root.ConfiguredPath || lockedWork.InventoryPath != *root.InventoryPath || lockedWork.RelativePath != location.RelativePath || lockedWork.SizeBytes != location.SizeBytes || !sourceAnalysisMtime(lockedWork.Mtime).Equal(sourceAnalysisMtime(location.Mtime)) {
				return fmt.Errorf("start source analysis delivery: %w", ErrSourceAnalysisStale)
			}
		}
		locked := new(Operation)
		if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, operationID).Scan(ctx, locked); err != nil {
			return fmt.Errorf("start source analysis delivery: lock operation: %w", err)
		}
		if locked.State != "queued" || locked.Attempt != delivery.Attempt || locked.RiverJobID == nil || *locked.RiverJobID != delivery.JobID {
			return fmt.Errorf("start source analysis delivery: %w", ErrSourceAnalysisStale)
		}
		for _, work := range works {
			if _, err := tx.NewRaw(`INSERT INTO source_analysis_work_execution
				(work_id, operation_id, operation_attempt, job_id, processing_mode)
				VALUES (?, ?, ?, ?, ?) ON CONFLICT (work_id, operation_id, operation_attempt, job_id) DO NOTHING`,
				work.WorkID, operationID, delivery.Attempt, delivery.JobID, root.ProcessingMode).Exec(ctx); err != nil {
				return fmt.Errorf("start source analysis delivery: record work execution identity: %w", err)
			}
			var recordedMode string
			if err := tx.NewRaw(`SELECT processing_mode FROM source_analysis_work_execution
				WHERE work_id=? AND operation_id=? AND operation_attempt=? AND job_id=?`,
				work.WorkID, operationID, delivery.Attempt, delivery.JobID).Scan(ctx, &recordedMode); err != nil {
				return fmt.Errorf("start source analysis delivery: verify work execution identity: %w", err)
			}
			if recordedMode != root.ProcessingMode {
				return fmt.Errorf("start source analysis delivery: execution mode was already recorded differently")
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=?`, operationID).Exec(ctx); err != nil {
			return fmt.Errorf("start source analysis delivery: replace installation holds: %w", err)
		}
		for _, selection := range selections {
			if _, err := tx.NewRaw(`INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?) ON CONFLICT DO NOTHING`, operationID, selection.InstallationID).Exec(ctx); err != nil {
				return fmt.Errorf("start source analysis delivery: hold installation: %w", err)
			}
		}
		toolsReadRequired := len(selections) > 0
		if err := tx.NewRaw(`UPDATE operation SET state='running',stage='probing',tools_read_required=?,started_at=now(),updated_at=now() WHERE id=? AND state='queued' AND attempt=? AND river_job_id=? RETURNING *`, toolsReadRequired, operationID, delivery.Attempt, delivery.JobID).Scan(ctx, locked); err != nil {
			return fmt.Errorf("start source analysis delivery: transition operation: %w", err)
		}
		started, processingMode = locked, root.ProcessingMode
		return nil
	})
	if err != nil {
		return nil, nil, "", err
	}
	return started, selections, processingMode, nil
}
