package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// upsertCanonicalMetadataResult keeps one current metadata row per SHA. The
// latest observation wins, with the winning capture UUID as a stable tie-breaker.
// Callers hold lockSourceFingerprintMutations before taking source-analysis row
// locks, so concurrent SHA promotion and metadata application share arbitration.
func upsertCanonicalMetadataResult(ctx context.Context, tx bun.IDB, digest []byte, candidate SourceMetadataResult) (*SourceMetadataResult, error) {
	if len(digest) != 32 || candidate.ID == uuid.Nil {
		return nil, fmt.Errorf("upsert canonical source metadata: valid digest and candidate identity are required")
	}
	var candidateDigest []byte
	var candidateExists bool
	if err := tx.NewRaw(`SELECT source_sha256 FROM media_metadata_result WHERE id=? FOR UPDATE`, candidate.ID).Scan(ctx, &candidateDigest); err == nil {
		candidateExists = true
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("upsert canonical source metadata: lock candidate: %w", err)
	}
	if len(candidateDigest) != 0 && string(candidateDigest) != string(digest) {
		return nil, fmt.Errorf("upsert canonical source metadata: candidate belongs to another SHA identity")
	}

	canonical := new(SourceMetadataResult)
	if err := tx.NewRaw(`SELECT * FROM media_metadata_result WHERE source_sha256=? FOR UPDATE`, digest).Scan(ctx, canonical); err != nil {
		if err != sql.ErrNoRows {
			return nil, fmt.Errorf("upsert canonical source metadata: lock canonical row: %w", err)
		}
		if candidateExists && len(candidateDigest) == 0 {
			if _, err := tx.NewRaw(`UPDATE media_metadata_result SET source_sha256=?,winning_result_id=? WHERE id=? AND source_sha256 IS NULL`, digest, candidate.WinningResultID, candidate.ID).Exec(ctx); err != nil {
				return nil, fmt.Errorf("upsert canonical source metadata: promote candidate: %w", err)
			}
			candidate.SourceSHA256 = append([]byte(nil), digest...)
			return &candidate, nil
		}
		candidate.SourceSHA256 = append([]byte(nil), digest...)
		if _, err := tx.NewInsert().Model(&candidate).Exec(ctx); err != nil {
			return nil, fmt.Errorf("upsert canonical source metadata: insert canonical row: %w", err)
		}
		return &candidate, nil
	}

	if candidate.ID != canonical.ID && metadataCandidateWins(candidate, *canonical) {
		if _, err := tx.NewRaw(`UPDATE media_metadata_result SET observed_tags=?,provenance=?,native_matroska=?,observed_at=?,winning_result_id=?,applied_operation_id=? WHERE id=?`,
			candidate.ObservedTags, candidate.Provenance, nullableJSON(candidate.NativeMatroska), candidate.ObservedAt, candidate.WinningResultID, candidate.AppliedOperationID, canonical.ID).Exec(ctx); err != nil {
			return nil, fmt.Errorf("upsert canonical source metadata: update winning capture: %w", err)
		}
	}
	if candidateExists && candidate.ID != canonical.ID {
		if _, err := tx.NewRaw(`UPDATE source_analysis_step SET success_metadata_result_id=? WHERE success_metadata_result_id=?`, canonical.ID, candidate.ID).Exec(ctx); err != nil {
			return nil, fmt.Errorf("upsert canonical source metadata: move selected references: %w", err)
		}
		if err := deleteUnreferencedSourceMetadataResult(ctx, tx, candidate.ID); err != nil {
			return nil, fmt.Errorf("upsert canonical source metadata: remove losing candidate: %w", err)
		}
	}
	if err := tx.NewRaw(`SELECT * FROM media_metadata_result WHERE id=?`, canonical.ID).Scan(ctx, canonical); err != nil {
		return nil, fmt.Errorf("upsert canonical source metadata: read canonical row: %w", err)
	}
	return canonical, nil
}

func metadataCandidateWins(candidate, current SourceMetadataResult) bool {
	candidateAt := candidate.ObservedAt.UTC().Truncate(time.Microsecond)
	currentAt := current.ObservedAt.UTC().Truncate(time.Microsecond)
	if candidateAt.Equal(currentAt) {
		return candidate.WinningResultID.String() > current.WinningResultID.String()
	}
	return candidateAt.After(currentAt)
}

func deleteUnreferencedSourceMetadataResult(ctx context.Context, tx bun.IDB, resultID uuid.UUID) error {
	if resultID == uuid.Nil {
		return nil
	}
	if _, err := tx.NewRaw(`DELETE FROM media_metadata_result result WHERE result.id=? AND result.source_sha256 IS NULL
		AND NOT EXISTS (SELECT 1 FROM source_analysis_step step WHERE step.success_metadata_result_id=result.id)`, resultID).Exec(ctx); err != nil {
		return fmt.Errorf("delete unreferenced source metadata result: %w", err)
	}
	return nil
}
