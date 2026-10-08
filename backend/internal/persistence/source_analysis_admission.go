package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	managedtools "github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/uptrace/bun"
)

// CreateNormalizedSourceAnalysisOperationAndEnqueue admits one normalized
// operation atomically with its River job, work/tool holds, and queued step
// execution triples. It follows the shared tools gate/package, root, location,
// work, installation, operation and step lock order used by the corresponding
// mutation paths.
func (repository *SourceInventoryRepository) CreateNormalizedSourceAnalysisOperationAndEnqueue(
	ctx context.Context,
	operation *Operation,
	client RiverInserter,
	args river.JobArgs,
	options *river.InsertOpts,
) error {
	if client == nil {
		return fmt.Errorf("admit source analysis: River client is required")
	}
	snapshot, err := ValidateSourceAnalysisOperationContract(operation)
	if err != nil {
		return fmt.Errorf("admit source analysis: %w", err)
	}
	// Tool identities exist only for this transaction. Durable snapshots keep
	// explicit work/step intent; active tool selections are transient admission
	// metadata and are re-resolved for each delivery.
	snapshot.Tools = append([]SourceAnalysisToolSelection(nil), operation.SourceAnalysisTools...)
	snapshot.ToolsReadRequired = operation.ToolsReadRequired
	cacheOnly := false
	snapshot.CacheOnlyReuse = &cacheOnly
	if operation.State != "queued" || operation.TargetSourceRootID == nil || *operation.TargetSourceRootID == uuid.Nil || operation.Attempt < 0 {
		return fmt.Errorf("admit source analysis: queued operation and source root are required")
	}
	if snapshot.Mode == SourceAnalysisModeBatch && operation.TargetSourceLocationID != nil {
		return fmt.Errorf("admit source analysis: batch operation cannot target one location")
	}
	if snapshot.Mode == SourceAnalysisModeSingleStep && (operation.TargetSourceLocationID == nil || operation.TargetWorkID == nil) {
		return fmt.Errorf("admit source analysis: single-step operation must target its exact work location")
	}
	workIDs := append([]uuid.UUID(nil), snapshot.WorkIDs...)
	sort.Slice(workIDs, func(i, j int) bool { return strings.Compare(workIDs[i].String(), workIDs[j].String()) < 0 })
	var configuredRoot string
	if err := repository.db.NewSelect().Model((*SourceRoot)(nil)).Column("configured_path").Where("id=?", *operation.TargetSourceRootID).Scan(ctx, &configuredRoot); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("admit source analysis: configured root disappeared: %w", ErrSourceAnalysisStale)
		}
		return fmt.Errorf("admit source analysis: read configured root for coordination: %w", err)
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("admit source analysis: lock output admission gate: %w", err)
		}
		if snapshot.ToolsReadRequired {
			if err := lockToolsMoveReaders(ctx, tx); err != nil {
				return fmt.Errorf("admit source analysis: lock tools-root readers: %w", err)
			}
			moving, err := activeToolsMoveExists(ctx, tx)
			if err != nil {
				return fmt.Errorf("admit source analysis: check tools-root move: %w", err)
			}
			if moving {
				return fmt.Errorf("admit source analysis: %w", ErrToolsRootMoveActive)
			}
			packageKinds := make([]string, 0, len(snapshot.Tools))
			for _, tool := range snapshot.Tools {
				packageKinds = append(packageKinds, tool.PackageKind)
			}
			if err := lockPackageSelections(ctx, tx, packageKinds); err != nil {
				return fmt.Errorf("admit source analysis: lock selected packages: %w", err)
			}
		}
		root := new(SourceRoot)
		if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, *operation.TargetSourceRootID).Scan(ctx, root); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("admit source analysis: source root disappeared: %w", ErrSourceAnalysisStale)
			}
			return fmt.Errorf("admit source analysis: lock source root: %w", err)
		}
		activeKind, active, err := activeSourceRootMutationExists(ctx, tx, root.ID)
		if err != nil {
			return fmt.Errorf("admit source analysis: check active source-root operation: %w", err)
		}
		if active {
			return fmt.Errorf("admit source analysis: %w", sourceRootActiveOperationError(activeKind))
		}
		if snapshot.ToolsReadRequired {
			moving, err := activeToolsMoveExists(ctx, tx)
			if err != nil {
				return fmt.Errorf("admit source analysis: recheck tools-root move: %w", err)
			}
			if moving {
				return fmt.Errorf("admit source analysis: %w", ErrToolsRootMoveActive)
			}
		}
		if !root.Enabled || root.ConfiguredPath != configuredRoot || root.Stale() || root.InventoryPath == nil || *root.InventoryPath != root.ConfiguredPath {
			return fmt.Errorf("admit source analysis: %w", ErrSourceAnalysisStale)
		}
		type selectedStep struct {
			WorkID          uuid.UUID       `bun:"work_id,type:uuid"`
			Step            string          `bun:"step"`
			State           string          `bun:"state"`
			LastOperationID *uuid.UUID      `bun:"last_operation_id,type:uuid,nullzero"`
			InputSnapshot   json.RawMessage `bun:"input_snapshot,type:jsonb,nullzero"`
			ProposedInput   json.RawMessage `bun:"-"`
		}
		selected := make([]selectedStep, 0)
		needsProbe, needsFingerprint := false, false
		type selectedWork struct{ ID, LocationID uuid.UUID }
		selectedWorks := make([]selectedWork, 0, len(workIDs))
		for _, workID := range workIDs {
			var locationID uuid.UUID
			if err := tx.NewRaw(`SELECT current_location_id FROM source_analysis_work WHERE id=? AND source_root_id=? AND current_location_id IS NOT NULL AND current_location_id=location_id`, workID, root.ID).Scan(ctx, &locationID); err != nil {
				return fmt.Errorf("admit source analysis: read selected location: %w", ErrSourceAnalysisStale)
			}
			selectedWorks = append(selectedWorks, selectedWork{ID: workID, LocationID: locationID})
		}
		orderedLocations := append([]selectedWork(nil), selectedWorks...)
		sort.Slice(orderedLocations, func(i, j int) bool {
			return strings.Compare(orderedLocations[i].LocationID.String(), orderedLocations[j].LocationID.String()) < 0
		})
		for _, item := range orderedLocations {
			location := new(SourceLocation)
			if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=? FOR UPDATE`, item.LocationID, root.ID).Scan(ctx, location); err != nil {
				return fmt.Errorf("admit source analysis: lock selected location: %w", ErrSourceAnalysisStale)
			}
		}
		for _, item := range selectedWorks {
			location := new(SourceLocation)
			if err := tx.NewRaw(`SELECT * FROM source_location WHERE id=? AND source_root_id=?`, item.LocationID, root.ID).Scan(ctx, location); err != nil {
				return fmt.Errorf("admit source analysis: read locked selected location: %w", ErrSourceAnalysisStale)
			}
			work := new(SourceAnalysisWork)
			if err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE id=? AND source_root_id=? FOR UPDATE`, item.ID, root.ID).Scan(ctx, work); err != nil {
				return fmt.Errorf("admit source analysis: lock selected work %s: %w", item.ID, ErrSourceAnalysisStale)
			}
			var activeHold bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation_source_work_hold h
				JOIN operation o ON o.id=h.operation_id
				WHERE h.work_id=? AND o.state IN ('queued','running'))`, item.ID).Scan(ctx, &activeHold); err != nil {
				return fmt.Errorf("admit source analysis: check active work hold: %w", err)
			}
			if activeHold {
				return fmt.Errorf("admit source analysis: %w", ErrSourceRootActiveAnalysis)
			}
			if work.CurrentLocationID == nil || *work.CurrentLocationID != work.LocationID || *work.CurrentLocationID != location.ID || work.ConfiguredPath != root.ConfiguredPath || work.InventoryPath != *root.InventoryPath ||
				location.RelativePath != work.RelativePath || location.SizeBytes != work.SizeBytes ||
				!sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(work.Mtime)) {
				return fmt.Errorf("admit source analysis: %w", ErrSourceAnalysisStale)
			}
			query := tx.NewSelect().Model((*SourceAnalysisStep)(nil)).Column("work_id", "step", "state", "last_operation_id", "input_snapshot").Where("work_id = ?", item.ID)
			if snapshot.Mode == SourceAnalysisModeSingleStep {
				query.Where("step = ?", *snapshot.TargetStep)
			} else if snapshot.SelectedSteps != nil {
				requestedSteps := make([]string, 0)
				for _, selection := range snapshot.SelectedSteps {
					if selection.WorkID == item.ID {
						requestedSteps = append(requestedSteps, string(selection.Step))
					}
				}
				query.Where("step IN (?)", bun.List(requestedSteps))
			} else {
				query.Where("state = 'pending'")
			}
			var rows []selectedStep
			if err := query.Scan(ctx, &rows); err != nil {
				return fmt.Errorf("admit source analysis: select work steps: %w", err)
			}
			if len(rows) == 0 {
				return fmt.Errorf("admit source analysis: selected work has no claimable steps")
			}
			if snapshot.Mode == SourceAnalysisModeBatch && snapshot.SelectedSteps != nil {
				requestedCount := 0
				for _, selection := range snapshot.SelectedSteps {
					if selection.WorkID == item.ID {
						requestedCount++
					}
				}
				if len(rows) != requestedCount {
					return fmt.Errorf("admit source analysis: every selected step must still be pending")
				}
				for _, row := range rows {
					if row.State != "pending" {
						return fmt.Errorf("admit source analysis: every selected step must still be pending")
					}
				}
			}
			for _, row := range rows {
				if row.Step == string(SourceStepSHA256) && !work.SHA256Enabled {
					return fmt.Errorf("admit source analysis: sha256 is disabled for the selected work")
				}
				if snapshot.Mode == SourceAnalysisModeSingleStep {
					if *snapshot.RerunTarget {
						if row.Step != string(SourceStepFingerprint) || row.State != "succeeded" &&
							(row.State != "pending" || len(row.InputSnapshot) == 0) {
							return fmt.Errorf("admit source analysis: explicit rerun requires the selected successful or recovered fingerprint intent")
						}
					} else if row.State != "failed" && (row.State != "pending" || len(row.InputSnapshot) == 0 || string(row.InputSnapshot) == "null") {
						return fmt.Errorf("admit source analysis: single-step retry requires the exact failed step")
					}
				}
				projected, err := ProjectSourceAnalysisStepSnapshot(snapshot, work.ID, SourceStepName(row.Step))
				if err != nil {
					return fmt.Errorf("admit source analysis: project %s step input: %w", row.Step, err)
				}
				row.ProposedInput = projected
				if row.State == "pending" && len(row.InputSnapshot) != 0 {
					if !sameSourceAnalysisStepInput(row.InputSnapshot, projected, *work, row.Step) {
						return fmt.Errorf("admit source analysis: pending step intent differs from its admitted input")
					}
				}
				if snapshot.Mode == SourceAnalysisModeSingleStep && *snapshot.RerunTarget && row.State == "pending" && !sameSourceAnalysisStepInput(row.InputSnapshot, projected, *work, row.Step) {
					return fmt.Errorf("admit source analysis: recovered fingerprint intent does not match")
				}
				needsProbe = needsProbe || row.Step == string(SourceStepProbe)
				needsFingerprint = needsFingerprint || row.Step == string(SourceStepFingerprint)
				selected = append(selected, row)
			}
			if *snapshot.CacheOnlyReuse {
				for _, row := range rows {
					if row.Step != string(SourceStepProbe) && row.Step != string(SourceStepFingerprint) {
						continue
					}
					version := snapshot.CacheOnlyFPCalcVersion
					if row.Step == string(SourceStepProbe) {
						version = snapshot.CacheOnlyFFProbeVersion
					}
					if version == "" {
						return fmt.Errorf("admit source analysis: cache-only selected tool version is required")
					}
					var digest []byte
					if err := tx.NewRaw(`SELECT v.source_sha256 FROM source_analysis_step s JOIN media_variant v ON v.id=s.success_sha_variant_id WHERE s.work_id=? AND s.step='sha256' AND s.success_sha_variant_id IS NOT NULL`, item.ID).Scan(ctx, &digest); err != nil || len(digest) != 32 {
						return fmt.Errorf("admit source analysis: cache-only reuse requires the selected current SHA-256 result")
					}
					var cached bool
					if row.Step == string(SourceStepProbe) {
						if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_variant WHERE source_sha256=? AND ffprobe_version=? AND analysis_policy_version=?)`, digest, version, SourceAnalysisPolicyVersion).Scan(ctx, &cached); err != nil {
							return fmt.Errorf("admit source analysis: check probe cache: %w", err)
						}
					} else if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_fingerprint_result WHERE source_sha256=? AND fpcalc_version=?)`, digest, version).Scan(ctx, &cached); err != nil {
						return fmt.Errorf("admit source analysis: check fingerprint cache: %w", err)
					}
					if !cached {
						return fmt.Errorf("admit source analysis: no cache result matches the selected digest and tool version")
					}
				}
			}
			if snapshot.Mode == SourceAnalysisModeSingleStep && location.ID != *operation.TargetSourceLocationID {
				return fmt.Errorf("admit source analysis: target location does not match the selected work")
			}
		}
		if snapshot.ToolsReadRequired {
			installationIDs := make([]uuid.UUID, 0, len(snapshot.Tools))
			for _, tool := range snapshot.Tools {
				installationIDs = append(installationIDs, tool.InstallationID)
			}
			if err := lockCompatibleInstallations(ctx, tx, installationIDs); err != nil {
				return fmt.Errorf("admit source analysis: lock pinned installations: %w", err)
			}
		}
		for index := range selected {
			var current selectedStep
			if err := tx.NewRaw(`SELECT work_id,step,state,last_operation_id,input_snapshot FROM source_analysis_step WHERE work_id=? AND step=? FOR UPDATE`, selected[index].WorkID, selected[index].Step).Scan(ctx, &current); err != nil {
				return fmt.Errorf("admit source analysis: lock selected step: %w", err)
			}
			if current.State != selected[index].State || !bytes.Equal(current.InputSnapshot, selected[index].InputSnapshot) {
				return fmt.Errorf("admit source analysis: selected step changed during admission")
			}
			current.ProposedInput = selected[index].ProposedInput
			selected[index] = current
		}
		if needsProbe && !hasSourceAnalysisTool(snapshot.Tools, "ffmpeg", "ffprobe") && !*snapshot.CacheOnlyReuse ||
			needsFingerprint && !hasSourceAnalysisTool(snapshot.Tools, "fpcalc", "fpcalc") && !*snapshot.CacheOnlyReuse ||
			!needsProbe && !needsFingerprint && len(snapshot.Tools) != 0 {
			return fmt.Errorf("admit source analysis: pinned executable selection does not match queued steps")
		}
		if snapshot.ToolsReadRequired != (needsProbe || needsFingerprint) && !*snapshot.CacheOnlyReuse {
			return fmt.Errorf("admit source analysis: tools_read_required does not match selected steps")
		}
		if snapshot.ToolsReadRequired {
			if err := validatePinnedInstallations(ctx, tx, snapshot.Tools); err != nil {
				return fmt.Errorf("admit source analysis: %w", err)
			}
		}
		job, err := client.InsertTx(ctx, tx.Tx, args, options)
		if err != nil {
			return fmt.Errorf("admit source analysis: insert River job: %w", err)
		}
		operation.RiverJobID = &job.Job.ID
		if operation.Attempt == 0 {
			operation.Attempt = 1
		}
		if _, err := tx.NewInsert().Model(operation).Exec(ctx); err != nil {
			return fmt.Errorf("admit source analysis: insert operation: %w", err)
		}
		for _, workID := range workIDs {
			if _, err := tx.NewRaw(`INSERT INTO operation_source_work_hold(operation_id,work_id) VALUES (?,?)`, operation.ID, workID).Exec(ctx); err != nil {
				return fmt.Errorf("admit source analysis: hold work %s: %w", workID, err)
			}
		}
		seenInstallations := make(map[uuid.UUID]struct{}, len(snapshot.Tools))
		for _, tool := range snapshot.Tools {
			if _, exists := seenInstallations[tool.InstallationID]; exists {
				continue
			}
			seenInstallations[tool.InstallationID] = struct{}{}
			if _, err := tx.NewRaw(`INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?)`, operation.ID, tool.InstallationID).Exec(ctx); err != nil {
				return fmt.Errorf("admit source analysis: hold installation %s: %w", tool.InstallationID, err)
			}
		}
		for _, row := range selected {
			if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='queued',input_snapshot=?::jsonb,safe_error=NULL,skip_reason=NULL,
				execution_operation_id=?,execution_operation_attempt=?,execution_job_id=?,updated_at=now()
				WHERE work_id=? AND step=?`, row.ProposedInput, operation.ID, operation.Attempt, job.Job.ID, row.WorkID, row.Step).Exec(ctx); err != nil {
				return fmt.Errorf("admit source analysis: queue %s step: %w", row.Step, err)
			}
		}
		return nil
	})
}

// activeSourceRootMutationExists is called only after locking the source root.
// A scan is root-exclusive; analyses are admitted independently and deduplicated
// by their locked work/step fences.
func activeSourceRootMutationExists(ctx context.Context, tx bun.IDB, rootID uuid.UUID) (string, bool, error) {
	var kind string
	err := tx.NewRaw(`SELECT kind FROM operation
		WHERE target_source_root_id = ? AND kind = 'scan_source'
		AND state IN ('queued', 'running') LIMIT 1`, rootID).Scan(ctx, &kind)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return kind, true, nil
}

func sourceRootActiveOperationError(kind string) error {
	if kind == "scan_source" {
		return ErrSourceRootActiveScan
	}
	return ErrSourceRootActiveAnalysis
}

func validatePinnedInstallations(ctx context.Context, tx bun.Tx, tools []SourceAnalysisToolSelection) error {
	type executableVersions map[string]string
	for _, selected := range tools {
		installation := new(ToolInstallation)
		if err := tx.NewRaw(`SELECT * FROM tool_installation WHERE id=?`, selected.InstallationID).Scan(ctx, installation); err != nil {
			return fmt.Errorf("pinned tool installation is unavailable: %w", err)
		}
		if installation.PackageKind != selected.PackageKind || installation.State != "ready" || installation.RelativePath != selected.RelativePath {
			return fmt.Errorf("pinned tool installation metadata changed")
		}
		var versions executableVersions
		if err := json.Unmarshal(installation.ExecutableVersions, &versions); err != nil {
			return fmt.Errorf("decode verified executable versions: %w", err)
		}
		banner, ok := managedtools.VerifiedExecutableVersion(versions, managedtools.PackageKind(selected.PackageKind), selected.Executable, installation.PlatformGOOS)
		if !ok || banner != selected.VersionBanner || selected.Version == "" || !strings.Contains(selected.VersionBanner, selected.Version) {
			return fmt.Errorf("pinned executable version does not match verified installation metadata")
		}
	}
	return nil
}
