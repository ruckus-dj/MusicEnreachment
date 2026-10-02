package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// ActiveSourceAnalysisOperationID returns the operation ID of the analysis of
// one source location that is still queued or running, or nil when the location
// has no active analysis. Only an analyze_source operation that targets both the
// root and the exact location is returned: a scan of the same root, or an
// analysis of another file, is not the active analysis of this location, so an
// inspector never adopts a foreign operation.
//
// The read is deliberately quiet: a location without an active analysis is a
// normal answer, not a missing row.
func (repository *SourceInventoryRepository) ActiveSourceAnalysisOperationID(ctx context.Context, rootID, locationID uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	err := repository.db.NewRaw(
		`SELECT id FROM operation
		 WHERE kind = ? AND state IN ('queued', 'running')
		   AND target_source_root_id = ? AND target_source_location_id = ?
		 ORDER BY created_at DESC
		 LIMIT 1`,
		analysisSourceOperationKind, rootID, locationID,
	).Scan(ctx, &id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read active source analysis operation: %w", err)
	}
	return &id, nil
}
