package persistence

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const sourceScanCandidateColumns = "relative_path text, size_bytes bigint, mtime timestamptz, probe_status text, safe_error text, source_sha256 text, sha256_calculated_at timestamptz, sha256_applied_operation_id uuid, audio_stream_count integer, ffprobe_version text, ffprobe_json jsonb, analysis_policy_version integer, observed_tags jsonb, inspected_at timestamptz, probe_applied_operation_id uuid"

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
		`INSERT INTO source_scan_candidate (id, operation_id, relative_path, size_bytes, mtime, probe_status, safe_error,
		 source_sha256, sha256_calculated_at, sha256_applied_operation_id, audio_stream_count, ffprobe_version, ffprobe_json,
		 analysis_policy_version, observed_tags, inspected_at, probe_applied_operation_id)
		 SELECT gen_random_uuid(), ?::uuid, batch.relative_path, batch.size_bytes, batch.mtime, batch.probe_status, batch.safe_error,
		 decode(batch.source_sha256,'hex'), batch.sha256_calculated_at, batch.sha256_applied_operation_id, batch.audio_stream_count,
		 batch.ffprobe_version, batch.ffprobe_json, batch.analysis_policy_version, batch.observed_tags, batch.inspected_at, batch.probe_applied_operation_id
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
		var digestHex *string
		if len(candidate.SourceSHA256) > 0 {
			encoded := hex.EncodeToString(candidate.SourceSHA256)
			digestHex = &encoded
		}
		payload, err := json.Marshal(struct {
			RelativePath             string          `json:"relative_path"`
			SizeBytes                int64           `json:"size_bytes"`
			Mtime                    time.Time       `json:"mtime"`
			ProbeStatus              string          `json:"probe_status"`
			SafeError                *string         `json:"safe_error"`
			SourceSHA256             *string         `json:"source_sha256"`
			SHA256CalculatedAt       *time.Time      `json:"sha256_calculated_at"`
			SHA256AppliedOperationID *uuid.UUID      `json:"sha256_applied_operation_id"`
			AudioStreamCount         *int            `json:"audio_stream_count"`
			FFProbeVersion           *string         `json:"ffprobe_version"`
			FFProbeJSON              json.RawMessage `json:"ffprobe_json"`
			AnalysisPolicyVersion    *int            `json:"analysis_policy_version"`
			ObservedTags             json.RawMessage `json:"observed_tags"`
			InspectedAt              *time.Time      `json:"inspected_at"`
			ProbeAppliedOperationID  *uuid.UUID      `json:"probe_applied_operation_id"`
		}{
			RelativePath: candidate.RelativePath, SizeBytes: candidate.SizeBytes, Mtime: candidate.Mtime,
			ProbeStatus: candidate.ProbeStatus, SafeError: candidate.SafeError,
			SourceSHA256: digestHex, SHA256CalculatedAt: candidate.SHA256CalculatedAt,
			SHA256AppliedOperationID: candidate.SHA256AppliedOperationID, AudioStreamCount: candidate.AudioStreamCount,
			FFProbeVersion: candidate.FFProbeVersion, FFProbeJSON: candidate.FFProbeJSON,
			AnalysisPolicyVersion: candidate.AnalysisPolicyVersion, ObservedTags: candidate.ObservedTags,
			InspectedAt: candidate.InspectedAt, ProbeAppliedOperationID: candidate.ProbeAppliedOperationID,
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
		`SELECT relative_path, size_bytes, mtime, probe_status, safe_error, source_sha256, sha256_calculated_at,
		 sha256_applied_operation_id, audio_stream_count, ffprobe_version, ffprobe_json, analysis_policy_version,
		 observed_tags, inspected_at, probe_applied_operation_id
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
	// A root whose configured path changed describes an inventory of another
	// directory: every link of the previous path is invalidated before the new
	// generation is written, even when a file keeps its relative path, size and
	// mtime, because it is a different file at a different location.
	if root.Stale() {
		if _, err := tx.NewUpdate().Model((*SourceLocation)(nil)).
			Set("media_variant_id = NULL").Set("updated_at = now()").
			Where("source_root_id = ?", root.ID).Where("media_variant_id IS NOT NULL").Exec(ctx); err != nil {
			return fmt.Errorf("unlink the variants of the previous inventory path: %w", err)
		}
	}
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
	// Unseen, changed and path-invalidated locations have released their
	// variants; a variant nothing links and no operation holds is removed here,
	// inside the same transaction, so reconciliation never leaves an orphan.
	if err := deleteOrphanedMediaVariants(ctx, tx); err != nil {
		return err
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
		// An unchanged file keeps its current identity, including a SHA-only or
		// no-audio result. A file whose size or mtime moved loses the link.
		// The link is
		// dropped explicitly here; it is never nulled implicitly by the variant
		// cleanup.
		Set("media_variant_id = CASE WHEN source_location.size_bytes = EXCLUDED.size_bytes AND source_location.mtime = EXCLUDED.mtime AND (source_location.probe_status = EXCLUDED.probe_status OR (source_location.probe_status = 'probe_error' AND EXCLUDED.probe_status = 'audio')) THEN source_location.media_variant_id ELSE NULL END").
		Set("updated_at = now()").
		Exec(ctx); err != nil {
		return fmt.Errorf("write source locations: %w", err)
	}
	return nil
}
