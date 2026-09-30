package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const sourceScanCandidateColumns = "relative_path text, size_bytes bigint, mtime timestamptz, probe_status text, safe_error text"

// sourceLocationRows projects the candidate batch onto the location table. A row
// keeps the id of a location already stored for its relative path, which is what
// holds an unchanged location's identity stable across scans.
func sourceLocationRows(rootID uuid.UUID, generation int64, candidates []SourceScanCandidateInput) []SourceLocation {
	rows := make([]SourceLocation, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, SourceLocation{
			ID: uuid.New(), SourceRootID: rootID, RelativePath: candidate.RelativePath,
			SizeBytes: candidate.SizeBytes, Mtime: candidate.Mtime,
			LastSeenScanGeneration: generation, ProbeStatus: candidate.ProbeStatus, SafeError: candidate.SafeError,
		})
	}
	return rows
}

// storeSourceScanCandidates persists the batch as the durable candidate rows of
// one operation, so a later step can verify what was applied.
func storeSourceScanCandidates(ctx context.Context, database bun.IDB, operationID uuid.UUID, candidates []SourceScanCandidateInput) error {
	if len(candidates) == 0 {
		return nil
	}
	marshalled, err := marshalSourceScanCandidates(candidates)
	if err != nil {
		return err
	}
	if _, err := database.NewRaw(
		`INSERT INTO source_scan_candidate (id, operation_id, relative_path, size_bytes, mtime, probe_status, safe_error)
		 SELECT gen_random_uuid(), ?::uuid, batch.relative_path, batch.size_bytes, batch.mtime, batch.probe_status, batch.safe_error
		 FROM jsonb_to_recordset(?) AS batch(`+sourceScanCandidateColumns+`)`,
		operationID, marshalled,
	).Exec(ctx); err != nil {
		return fmt.Errorf("store scan candidates: %w", err)
	}
	return nil
}

func marshalSourceScanCandidates(candidates []SourceScanCandidateInput) ([]json.RawMessage, error) {
	marshalled := make([]json.RawMessage, 0, len(candidates))
	for _, candidate := range candidates {
		payload, err := json.Marshal(struct {
			RelativePath string    `json:"relative_path"`
			SizeBytes    int64     `json:"size_bytes"`
			Mtime        time.Time `json:"mtime"`
			ProbeStatus  string    `json:"probe_status"`
			SafeError    *string   `json:"safe_error"`
		}{
			RelativePath: candidate.RelativePath, SizeBytes: candidate.SizeBytes, Mtime: candidate.Mtime,
			ProbeStatus: candidate.ProbeStatus, SafeError: candidate.SafeError,
		})
		if err != nil {
			return nil, fmt.Errorf("prepare scan candidate %q: %w", candidate.RelativePath, err)
		}
		marshalled = append(marshalled, payload)
	}
	return marshalled, nil
}

// loadSourceScanCandidates reads the durable candidate rows of one operation.
// The apply feeds these rows to the generation writer, so what the traversal
// stored is exactly what the applied inventory contains; the candidate table's
// (operation_id, relative_path) uniqueness is what keeps the read deterministic.
func loadSourceScanCandidates(ctx context.Context, database bun.IDB, operationID uuid.UUID) ([]SourceScanCandidateInput, error) {
	candidates := make([]SourceScanCandidateInput, 0)
	if err := database.NewRaw(
		`SELECT relative_path, size_bytes, mtime, probe_status, safe_error
		 FROM source_scan_candidate WHERE operation_id = ? ORDER BY relative_path, id`,
		operationID,
	).Scan(ctx, &candidates); err != nil {
		return nil, fmt.Errorf("load scan candidates: %w", err)
	}
	return candidates, nil
}

// applySourceScanCandidates writes the candidate batch as the next generation of
// the locked root. A location already stored for a relative path keeps its id and
// only has its file facts confirmed; a location the batch does not contain is
// removed, so the pruning is scoped to the root and never to another root's rows.
func applySourceScanCandidates(ctx context.Context, tx bun.Tx, root SourceRoot, generation int64, candidates []SourceScanCandidateInput) error {
	if len(candidates) > 0 {
		if err := insertSourceLocations(ctx, tx, root, generation, candidates); err != nil {
			return err
		}
	}
	if _, err := tx.NewDelete().Model((*SourceLocation)(nil)).
		Where("source_root_id = ?", root.ID).
		Where("last_seen_scan_generation < ?", generation).
		Exec(ctx); err != nil {
		return fmt.Errorf("prune unseen source locations: %w", err)
	}
	var written int
	if err := tx.NewRaw("SELECT count(*) FROM source_location WHERE source_root_id = ? AND last_seen_scan_generation = ?", root.ID, generation).Scan(ctx, &written); err != nil {
		return fmt.Errorf("count written source locations: %w", err)
	}
	if written != len(candidates) {
		return fmt.Errorf("wrote %d source locations of %d candidates", written, len(candidates))
	}
	var remaining int
	if err := tx.NewRaw("SELECT count(*) FROM source_location WHERE source_root_id = ?", root.ID).Scan(ctx, &remaining); err != nil {
		return fmt.Errorf("count the applied generation: %w", err)
	}
	if remaining != len(candidates) {
		return fmt.Errorf("applied generation has %d locations of %d candidates", remaining, len(candidates))
	}
	return nil
}

// insertSourceLocations writes the batch as rows of the applied generation. A
// location already stored for a relative path keeps its id and only has its file
// facts confirmed.
func insertSourceLocations(ctx context.Context, tx bun.Tx, root SourceRoot, generation int64, candidates []SourceScanCandidateInput) error {
	rows := sourceLocationRows(root.ID, generation, candidates)
	if _, err := tx.NewInsert().Model(&rows).
		On("CONFLICT (source_root_id, relative_path) DO UPDATE").
		Set("size_bytes = EXCLUDED.size_bytes").
		Set("mtime = EXCLUDED.mtime").
		Set("probe_status = EXCLUDED.probe_status").
		Set("safe_error = EXCLUDED.safe_error").
		Set("last_seen_scan_generation = EXCLUDED.last_seen_scan_generation").
		Set("updated_at = now()").
		Exec(ctx); err != nil {
		return fmt.Errorf("write source locations: %w", err)
	}
	return nil
}
