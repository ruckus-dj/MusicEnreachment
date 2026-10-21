package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// recordSourceCleanupCompletion records only a successful native removal or an
// explicit retry's authorized missing-leaf observation. It is current evidence,
// not cleanup operation history.
func recordSourceCleanupCompletion(ctx context.Context, tx bun.IDB, source publicationSourceCleanupSnapshot, kind string) error {
	if kind != "native_removed" && kind != "absent_after_explicit_retry" {
		return fmt.Errorf("record source cleanup completion: invalid outcome")
	}
	mtime, err := time.Parse(time.RFC3339Nano, source.Mtime)
	if err != nil || source.WorkID == uuid.Nil || source.SourceRootID == uuid.Nil || source.SourceLocationID == uuid.Nil || source.MediaVariantID == uuid.Nil || source.ConfiguredPath == "" || source.InventoryPath == "" || source.RelativePath == "" || source.SizeBytes < 0 {
		return fmt.Errorf("record source cleanup completion: invalid source identity")
	}
	_, err = tx.NewRaw(`INSERT INTO source_cleanup_completion
		(work_id,source_root_id,source_location_id,media_variant_id,configured_path,inventory_path,relative_path,size_bytes,mtime,kind,handled_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,clock_timestamp())
		ON CONFLICT (work_id) DO NOTHING`,
		source.WorkID, source.SourceRootID, source.SourceLocationID, source.MediaVariantID, source.ConfiguredPath,
		source.InventoryPath, source.RelativePath, source.SizeBytes, sourceAnalysisMtime(mtime), kind).Exec(ctx)
	if err != nil {
		return fmt.Errorf("record source cleanup completion: %w", err)
	}
	return nil
}

// filterHandledSourceCleanupCandidates removes only candidates whose complete
// current inventory identity matches a durable completion marker.
func filterHandledSourceCleanupCandidates(ctx context.Context, tx bun.IDB, candidates []PublicationSourceCleanupCandidate) ([]PublicationSourceCleanupCandidate, error) {
	filtered := make([]PublicationSourceCleanupCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		var handled bool
		err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM source_cleanup_completion c
			JOIN source_analysis_work w ON w.id=c.work_id
			JOIN source_location l ON l.id=w.current_location_id
			JOIN source_root r ON r.id=w.source_root_id
			JOIN source_work_variant_identity i ON i.work_id=w.id
			WHERE c.work_id=? AND c.source_root_id=? AND c.source_location_id=? AND c.media_variant_id=?
			AND c.configured_path=r.configured_path AND c.inventory_path=r.inventory_path
			AND c.relative_path=w.relative_path AND c.relative_path=l.relative_path AND c.size_bytes=w.size_bytes AND c.size_bytes=l.size_bytes
			AND date_trunc('microseconds',c.mtime)=date_trunc('microseconds',w.mtime)
			AND date_trunc('microseconds',c.mtime)=date_trunc('microseconds',l.mtime)
			AND w.source_root_id=c.source_root_id AND w.current_location_id=c.source_location_id
			AND l.source_root_id=c.source_root_id AND l.media_variant_id=c.media_variant_id AND i.media_variant_id=c.media_variant_id
		)`, candidate.WorkID, candidate.SourceRootID, candidate.SourceLocationID, candidate.MediaVariantID).Scan(ctx, &handled)
		if err != nil {
			return nil, fmt.Errorf("filter handled source cleanup candidates: %w", err)
		}
		if !handled {
			filtered = append(filtered, candidate)
		}
	}
	return filtered, nil
}

func filterHandledSourceCleanupCandidatesExcept(ctx context.Context, tx bun.IDB, candidates []PublicationSourceCleanupCandidate, retained map[string]bool) ([]PublicationSourceCleanupCandidate, error) {
	filtered, err := filterHandledSourceCleanupCandidates(ctx, tx, candidates)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(filtered))
	for _, candidate := range filtered {
		seen[cleanupIdentityKey(candidate)] = true
	}
	for _, candidate := range candidates {
		key := cleanupIdentityKey(candidate)
		if retained[key] && !seen[key] {
			filtered = append(filtered, candidate)
			seen[key] = true
		}
	}
	return filtered, nil
}

func filterHandledSourceCleanupAssessment(ctx context.Context, tx bun.IDB, assessment *PublicationSourceCleanupAssessment) error {
	original := assessment.Candidates
	filtered, err := filterHandledSourceCleanupCandidates(ctx, tx, original)
	if err != nil {
		return err
	}
	removedBlockers := make(map[string]bool)
	for _, candidate := range original {
		for _, blocker := range candidate.Blockers {
			removedBlockers[blocker] = true
		}
	}
	blockers := assessment.Blockers[:0]
	for _, blocker := range assessment.Blockers {
		if !removedBlockers[blocker] {
			blockers = append(blockers, blocker)
		}
	}
	for _, candidate := range filtered {
		blockers = append(blockers, candidate.Blockers...)
	}
	if len(filtered) == 0 && len(original) > 0 {
		blockers = append(blockers, "no unhandled current source alternatives")
	}
	assessment.Candidates = filtered
	assessment.Blockers = uniquePublicationCleanupStrings(blockers)
	return nil
}
