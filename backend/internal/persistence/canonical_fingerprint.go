package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// upsertCanonicalFingerprintResult keeps the canonical result UUID stable while
// selecting the newest candidate by (calculated_at, candidate UUID). The
// winning_result_id column stores the candidate identity separately from that
// stable canonical row identity.
func upsertCanonicalFingerprintResult(ctx context.Context, tx bun.IDB, digest []byte, candidate SourceFingerprintResult) (*SourceFingerprintResult, error) {
	if len(digest) != 32 || candidate.ID == uuid.Nil {
		return nil, fmt.Errorf("upsert canonical source fingerprint: valid digest and candidate identity are required")
	}
	// SHA application normally already owns this row lock. Taking it here too
	// serializes fingerprint promotion with other writers for the same digest.
	if err := tx.NewRaw(`SELECT id FROM media_variant WHERE source_sha256=? FOR UPDATE`, digest).Scan(ctx, new(uuid.UUID)); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("upsert canonical source fingerprint: lock source digest: %w", err)
	}

	// A fingerprint may have been persisted before its work learned the SHA.
	// Lock it before looking up/creating the canonical row so its identity and
	// provenance survive promotion when it can become canonical.
	var candidateSourceSHA []byte
	var candidateWinningID uuid.UUID
	var candidateCalculatedAt time.Time
	candidateExists := true
	if err := tx.NewRaw(`SELECT source_sha256,winning_result_id,calculated_at FROM media_fingerprint_result WHERE id=? FOR UPDATE`, candidate.ID).
		Scan(ctx, &candidateSourceSHA, &candidateWinningID, &candidateCalculatedAt); err != nil {
		if err != sql.ErrNoRows {
			return nil, fmt.Errorf("upsert canonical source fingerprint: lock candidate result: %w", err)
		}
		candidateExists = false
	}
	if candidateExists && len(candidateSourceSHA) != 0 && string(candidateSourceSHA) != string(digest) {
		return nil, fmt.Errorf("upsert canonical source fingerprint: candidate belongs to a different SHA identity")
	}
	if candidateExists {
		candidate.WinningResultID = candidateWinningID
		candidate.CalculatedAt = candidateCalculatedAt
	}
	if candidate.WinningResultID == uuid.Nil {
		candidate.WinningResultID = candidate.ID
	}

	var canonicalID uuid.UUID
	var winningID uuid.UUID
	var calculatedAt time.Time
	canonicalExists := true
	if err := tx.NewRaw(`SELECT id,winning_result_id,calculated_at FROM media_fingerprint_result WHERE source_sha256=? FOR UPDATE`, digest).
		Scan(ctx, &canonicalID, &winningID, &calculatedAt); err != nil {
		if err != sql.ErrNoRows {
			return nil, fmt.Errorf("upsert canonical source fingerprint: lock canonical result: %w", err)
		}
		canonicalExists = false
	}

	if !canonicalExists && candidateExists && candidateSourceSHA == nil {
		if _, err := tx.NewRaw(`UPDATE media_fingerprint_result SET source_sha256=? WHERE id=? AND source_sha256 IS NULL`, digest, candidate.ID).Exec(ctx); err != nil {
			return nil, fmt.Errorf("upsert canonical source fingerprint: attach digest to candidate: %w", err)
		}
		canonicalID = candidate.ID
		winningID = candidate.WinningResultID
		calculatedAt = candidate.CalculatedAt
		canonicalExists = true
	}
	if !canonicalExists {
		if _, err := tx.NewRaw(`INSERT INTO media_fingerprint_result
		(id,source_sha256,winning_result_id,fpcalc_version,version_banner,algorithm_namespace,algorithm_id,fingerprint,reported_duration,calculated_at,applied_operation_id,parser_contract_version)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT (source_sha256) DO NOTHING`,
			candidate.ID, digest, candidate.WinningResultID, candidate.FPCalcVersion, candidate.VersionBanner,
			candidate.AlgorithmNamespace, candidate.AlgorithmID, candidate.Fingerprint,
			candidate.ReportedDuration, candidate.CalculatedAt, candidate.AppliedOperationID,
			candidate.ParserContractVersion).Exec(ctx); err != nil {
			return nil, fmt.Errorf("upsert canonical source fingerprint: insert candidate: %w", err)
		}
		if err := tx.NewRaw(`SELECT id,winning_result_id,calculated_at FROM media_fingerprint_result WHERE source_sha256=? FOR UPDATE`, digest).
			Scan(ctx, &canonicalID, &winningID, &calculatedAt); err != nil {
			return nil, fmt.Errorf("upsert canonical source fingerprint: lock canonical result: %w", err)
		}
	}
	if calculatedAt.Before(candidate.CalculatedAt) ||
		calculatedAt.Equal(candidate.CalculatedAt) && winningID.String() < candidate.WinningResultID.String() {
		if _, err := tx.NewRaw(`UPDATE media_fingerprint_result SET winning_result_id=?,fpcalc_version=?,version_banner=?,algorithm_namespace=?,algorithm_id=?,fingerprint=?,reported_duration=?,calculated_at=?,applied_operation_id=?,parser_contract_version=? WHERE id=?`,
			candidate.WinningResultID, candidate.FPCalcVersion, candidate.VersionBanner, candidate.AlgorithmNamespace,
			candidate.AlgorithmID, candidate.Fingerprint, candidate.ReportedDuration,
			candidate.CalculatedAt, candidate.AppliedOperationID, candidate.ParserContractVersion,
			canonicalID).Exec(ctx); err != nil {
			return nil, fmt.Errorf("upsert canonical source fingerprint: update winner: %w", err)
		}
	}
	selected := new(SourceFingerprintResult)
	if err := tx.NewRaw(`SELECT * FROM media_fingerprint_result WHERE id=?`, canonicalID).Scan(ctx, selected); err != nil {
		return nil, fmt.Errorf("upsert canonical source fingerprint: read canonical result: %w", err)
	}
	return selected, nil
}
