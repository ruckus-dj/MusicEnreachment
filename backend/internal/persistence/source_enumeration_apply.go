package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ApplySourceEnumeration publishes stat-only candidates and creates pending work
// in the same transaction. It is intentionally independent of River admission.
func (repository *SourceInventoryRepository) ApplySourceEnumeration(ctx context.Context, apply SourceEnumerationApply) error {
	if apply.OperationID == uuid.Nil || apply.ExpectedAttempt < 1 || apply.ExpectedJobID < 1 {
		return fmt.Errorf("apply source enumeration: delivery identity is required")
	}
	for _, scope := range apply.Scopes {
		if !validSourceEnumerationScope(scope) {
			return fmt.Errorf("apply source enumeration: invalid unreadable scope")
		}
	}
	if len(apply.Scopes) > 0 && apply.FailureSafeError == "" {
		return fmt.Errorf("apply source enumeration: unreadable scopes require a safe failure reason")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("apply source enumeration: lock fingerprint mutations: %w", err)
		}
		rootID, err := operationTargetSourceRoot(ctx, tx, apply.OperationID)
		if err != nil {
			return fmt.Errorf("apply source enumeration: read operation target: %w", err)
		}
		if rootID == nil {
			return fmt.Errorf("apply source enumeration: operation must target a source root")
		}
		root := new(SourceRoot)
		if err := tx.NewRaw(`SELECT * FROM source_root WHERE id=? FOR UPDATE`, *rootID).Scan(ctx, root); err != nil {
			return fmt.Errorf("apply source enumeration: lock root: %w", err)
		}
		operation := new(Operation)
		if err := tx.NewRaw(`SELECT * FROM operation WHERE id=? FOR UPDATE`, apply.OperationID).Scan(ctx, operation); err != nil {
			return fmt.Errorf("apply source enumeration: lock operation: %w", err)
		}
		if operation.Kind != sourceScanOperationKind || operation.State != "running" || operation.TargetSourceRootID == nil || *operation.TargetSourceRootID != root.ID || operation.Attempt != apply.ExpectedAttempt || operation.RiverJobID == nil || *operation.RiverJobID != apply.ExpectedJobID {
			return fmt.Errorf("apply source enumeration: delivery identity changed")
		}
		if root.LastAppliedOperationID != nil && *root.LastAppliedOperationID == operation.ID {
			return nil
		}
		if root.ConfiguredPath != apply.ExpectedConfiguredPath {
			return fmt.Errorf("apply source enumeration: configured path changed since traversal")
		}
		rootUnavailable := false
		for _, scope := range apply.Scopes {
			if scope.Kind == "root" {
				rootUnavailable = true
				break
			}
		}
		candidates, err := loadSourceScanCandidates(ctx, tx, apply.OperationID)
		if err != nil {
			return fmt.Errorf("apply source enumeration: %w", err)
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].RelativePath < candidates[j].RelativePath })
		for i := range candidates {
			candidate := candidates[i]
			if candidate.ProbeStatus != SourceProbeStatusNotAnalyzed || candidate.SafeError != nil || candidate.SizeBytes < 0 || candidate.Mtime.IsZero() || !validSourceEnumerationCandidatePath(candidate.RelativePath) || candidate.PreparedAnalysis != nil || len(candidate.SourceSHA256) != 0 || candidate.SHA256CalculatedAt != nil || candidate.SHA256AppliedOperationID != nil || candidate.AudioStreamCount != nil || candidate.FFProbeVersion != nil || candidate.FFProbeJSON != nil || candidate.AnalysisPolicyVersion != nil || candidate.ObservedTags != nil || candidate.InspectedAt != nil || candidate.ProbeAppliedOperationID != nil {
				return fmt.Errorf("apply source enumeration: candidate %q is not stat-only", candidate.RelativePath)
			}
			if i > 0 && candidates[i-1].RelativePath == candidate.RelativePath {
				return fmt.Errorf("apply source enumeration: duplicate candidate path %q", candidate.RelativePath)
			}
		}
		filteredCandidates := candidates[:0]
		for _, candidate := range candidates {
			if !sourceEnumerationPathUnreadable(candidate.RelativePath, apply.Scopes) {
				filteredCandidates = append(filteredCandidates, candidate)
			}
		}
		candidates = filteredCandidates

		oldLocations := make([]SourceLocation, 0)
		if err := tx.NewSelect().Model(&oldLocations).Where("source_root_id=?", root.ID).Order("relative_path").For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("apply source enumeration: lock existing locations: %w", err)
		}
		candidateByPath := make(map[string]SourceScanCandidateInput, len(candidates))
		for _, candidate := range candidates {
			candidateByPath[candidate.RelativePath] = candidate
		}
		for _, location := range oldLocations {
			candidate, seen := candidateByPath[location.RelativePath]
			var old SourceAnalysisWork
			err := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE current_location_id=? FOR UPDATE`, location.ID).Scan(ctx, &old)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return fmt.Errorf("apply source enumeration: read old analysis work: %w", err)
			}
			unchanged := seen && !root.Stale() && location.SizeBytes == candidate.SizeBytes && sourceAnalysisMtime(location.Mtime).Equal(sourceAnalysisMtime(candidate.Mtime)) &&
				old.SourceRootID == root.ID && old.ConfiguredPath == root.ConfiguredPath && old.InventoryPath == root.ConfiguredPath && old.RelativePath == candidate.RelativePath && old.SizeBytes == candidate.SizeBytes && sourceAnalysisMtime(old.Mtime).Equal(sourceAnalysisMtime(candidate.Mtime))
			if unchanged {
				continue
			}
			var active int
			if err := tx.NewRaw(`SELECT count(*) FROM operation_source_work_hold h JOIN operation o ON o.id=h.operation_id WHERE h.work_id=? AND o.state IN ('queued','running')`, old.ID).Scan(ctx, &active); err != nil {
				return fmt.Errorf("apply source enumeration: check old work holds: %w", err)
			}
			if active > 0 {
				return fmt.Errorf("apply source enumeration: old analysis work has an active hold")
			}
			if err := retireSourceAnalysisWork(ctx, tx, old.ID); err != nil {
				return fmt.Errorf("apply source enumeration: safely retire old analysis work: %w", err)
			}
		}

		generation := root.ScanGeneration + 1
		for _, candidate := range candidates {
			var current SourceLocation
			err := tx.NewRaw(`SELECT * FROM source_location WHERE source_root_id=? AND relative_path=? FOR UPDATE`, root.ID, candidate.RelativePath).Scan(ctx, &current)
			isNew := err == sql.ErrNoRows
			if err != nil && !isNew {
				return fmt.Errorf("apply source enumeration: lock candidate location: %w", err)
			}
			unchanged := !isNew && !root.Stale() && current.SizeBytes == candidate.SizeBytes && sourceAnalysisMtime(current.Mtime).Equal(sourceAnalysisMtime(candidate.Mtime))
			if isNew {
				current = SourceLocation{ID: uuid.New(), SourceRootID: root.ID, RelativePath: candidate.RelativePath}
			}
			status, safeError, mediaVariant := SourceProbeStatusNotAnalyzed, (*string)(nil), (*uuid.UUID)(nil)
			if unchanged {
				status, safeError, mediaVariant = current.ProbeStatus, current.SafeError, current.MediaVariantID
			}
			if _, err := tx.NewRaw(`INSERT INTO source_location (id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,safe_error,media_variant_id)
				VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT (source_root_id,relative_path) DO UPDATE SET
				size_bytes=EXCLUDED.size_bytes,mtime=EXCLUDED.mtime,last_seen_scan_generation=EXCLUDED.last_seen_scan_generation,
				probe_status=EXCLUDED.probe_status,safe_error=EXCLUDED.safe_error,media_variant_id=EXCLUDED.media_variant_id,updated_at=now()`,
				current.ID, root.ID, candidate.RelativePath, candidate.SizeBytes, sourceAnalysisMtime(candidate.Mtime), generation, status, safeError, mediaVariant).Exec(ctx); err != nil {
				return fmt.Errorf("apply source enumeration: upsert location %q: %w", candidate.RelativePath, err)
			}
			var locationID uuid.UUID
			if err := tx.NewRaw(`SELECT id FROM source_location WHERE source_root_id=? AND relative_path=?`, root.ID, candidate.RelativePath).Scan(ctx, &locationID); err != nil {
				return fmt.Errorf("apply source enumeration: resolve location %q: %w", candidate.RelativePath, err)
			}
			var existing SourceAnalysisWork
			workErr := tx.NewRaw(`SELECT * FROM source_analysis_work WHERE current_location_id=?`, locationID).Scan(ctx, &existing)
			if workErr == nil && existing.SourceRootID == root.ID && existing.ConfiguredPath == root.ConfiguredPath && existing.InventoryPath == root.ConfiguredPath && existing.RelativePath == candidate.RelativePath && existing.SizeBytes == candidate.SizeBytes && sourceAnalysisMtime(existing.Mtime).Equal(sourceAnalysisMtime(candidate.Mtime)) {
				continue
			}
			if workErr != nil && workErr != sql.ErrNoRows {
				return fmt.Errorf("apply source enumeration: read current work: %w", workErr)
			}
			work := SourceAnalysisWork{ID: uuid.New(), LocationID: locationID, SourceRootID: root.ID,
				ConfiguredPath: root.ConfiguredPath, InventoryPath: root.ConfiguredPath, RelativePath: candidate.RelativePath,
				SizeBytes: candidate.SizeBytes, Mtime: sourceAnalysisMtime(candidate.Mtime), SHA256Enabled: apply.SHA256Enabled,
				OriginScanOperationID: operation.ID}
			if _, err := tx.NewInsert().Model(&work).Exec(ctx); err != nil {
				return fmt.Errorf("apply source enumeration: create work for %q: %w", candidate.RelativePath, err)
			}
			steps := []SourceAnalysisStep{
				{WorkID: work.ID, Step: string(SourceStepSHA256), State: "pending"},
				{WorkID: work.ID, Step: string(SourceStepProbe), State: "pending"},
				{WorkID: work.ID, Step: string(SourceStepFingerprint), State: "pending"},
			}
			if !apply.SHA256Enabled {
				steps[0].State = "not_requested"
			}
			if _, err := tx.NewInsert().Model(&steps).Exec(ctx); err != nil {
				return fmt.Errorf("apply source enumeration: create analysis steps for %q: %w", candidate.RelativePath, err)
			}
		}

		if err := discardUnreadableEnumerationCandidates(ctx, tx, apply.OperationID, apply.Scopes); err != nil {
			return fmt.Errorf("apply source enumeration: discard candidates from unreadable scopes: %w", err)
		}
		if err := pruneEnumeratedLocations(ctx, tx, root.ID, apply.OperationID); err != nil {
			return fmt.Errorf("apply source enumeration: reconcile missing locations: %w", err)
		}
		if _, err := tx.NewDelete().Model((*SourceScanCandidate)(nil)).Where("operation_id=?", operation.ID).Exec(ctx); err != nil {
			return fmt.Errorf("apply source enumeration: remove applied candidates: %w", err)
		}
		rootUpdate := tx.NewUpdate().Model((*SourceRoot)(nil)).Set("scan_generation=?", generation).
			Set("inventory_path=?", root.ConfiguredPath).Set("last_applied_operation_id=?", operation.ID).
			Set("updated_at=now()").Where("id=?", root.ID)
		if rootUnavailable {
			rootUpdate.Set("status=?", SourceRootStatusUnavailable).Set("safe_error=?", SourceEnumerationRootUnavailableReason)
		} else {
			rootUpdate.Set("last_successful_scan_at=now()").Set("status=?", SourceRootStatusAvailable).Set("safe_error=NULL")
		}
		if _, err := rootUpdate.Exec(ctx); err != nil {
			return fmt.Errorf("apply source enumeration: update root inventory: %w", err)
		}
		if len(apply.Scopes) > 0 {
			failure := apply.FailureSafeError
			operation.SafeError = &failure
			operation.Stage = "traversing"
			operation.UpdatedAt = time.Now().UTC()
			if _, err := tx.NewUpdate().Model(operation).Column("safe_error", "stage", "updated_at").WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("apply source enumeration: record unreadable-scope outcome: %w", err)
			}
		}
		return nil
	})
}

func discardUnreadableEnumerationCandidates(ctx context.Context, tx bun.Tx, operationID uuid.UUID, scopes []SourceEnumerationScope) error {
	for _, scope := range scopes {
		query := `DELETE FROM source_scan_candidate WHERE operation_id=? AND relative_path=?`
		args := []any{operationID, scope.RelativePath}
		switch scope.Kind {
		case "root":
			query = `DELETE FROM source_scan_candidate WHERE operation_id=?`
			args = []any{operationID}
		case "subtree":
			query = `DELETE FROM source_scan_candidate WHERE operation_id=? AND
				(relative_path=? OR left(relative_path,char_length(?)+1)=? || '/')`
			args = []any{operationID, scope.RelativePath, scope.RelativePath, scope.RelativePath}
		}
		if _, err := tx.NewRaw(query, args...).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func pruneEnumeratedLocations(ctx context.Context, tx bun.Tx, rootID, operationID uuid.UUID) error {
	locations := make([]SourceLocation, 0)
	if err := tx.NewSelect().Model(&locations).Where("source_root_id=?", rootID).Order("relative_path").For("UPDATE").Scan(ctx); err != nil {
		return err
	}
	for _, location := range locations {
		var exists bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM source_scan_candidate WHERE operation_id=? AND relative_path=?)`, operationID, location.RelativePath).Scan(ctx, &exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		var workID uuid.UUID
		err := tx.NewRaw(`SELECT id FROM source_analysis_work WHERE current_location_id=? FOR UPDATE`, location.ID).Scan(ctx, &workID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			if err := retireSourceAnalysisWork(ctx, tx, workID); err != nil {
				return err
			}
		}
		if _, err := tx.NewDelete().Model((*SourceLocation)(nil)).Where("id=?", location.ID).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func sourceEnumerationPathUnreadable(path string, scopes []SourceEnumerationScope) bool {
	for _, scope := range scopes {
		if scope.Kind == "root" || path == scope.RelativePath ||
			(scope.Kind == "subtree" && strings.HasPrefix(path, scope.RelativePath+"/")) {
			return true
		}
	}
	return false
}

func validSourceEnumerationScope(scope SourceEnumerationScope) bool {
	if scope.Kind == "root" {
		return scope.RelativePath == ""
	}
	return (scope.Kind == "subtree" || scope.Kind == "file") && validSourceEnumerationRelativePath(scope.RelativePath)
}

func validSourceEnumerationCandidatePath(path string) bool {
	if !validSourceEnumerationRelativePath(path) {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac", ".wav", ".aif", ".aiff", ".ape", ".wv", ".mp3", ".m4a", ".aac", ".ogg", ".opus", ".wma", ".mka":
		return true
	default:
		return false
	}
}

func validSourceEnumerationRelativePath(path string) bool {
	if path == "" || strings.Contains(path, "\\") || !fs.ValidPath(path) || path == "." {
		return false
	}
	return true
}
