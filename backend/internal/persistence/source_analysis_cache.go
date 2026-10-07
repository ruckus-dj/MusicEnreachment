package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// LookupSourceProbe returns the immutable result selected for this digest,
// ffprobe banner and analysis policy.
func (repository *SourceInventoryRepository) LookupSourceProbe(ctx context.Context, digest [sha256.Size]byte, version string, policy int) (*SourceMediaVariant, bool, error) {
	var cachedVersion string
	err := repository.db.NewRaw(`SELECT ffprobe_version FROM media_probe_cache
		WHERE source_sha256=? AND ffprobe_version_sha256=sha256(convert_to(?, 'UTF8')) AND analysis_policy_version=?`, digest[:], version, policy).Scan(ctx, &cachedVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("lookup source probe cache banner: %w", err)
	}
	if cachedVersion != version {
		return nil, false, fmt.Errorf("lookup source probe cache: ffprobe version banner digest collision")
	}

	result := new(SourceMediaVariant)
	err = repository.db.NewRaw(`SELECT result.* FROM media_probe_cache cache
		JOIN media_variant result ON result.id=cache.result_id
		WHERE cache.source_sha256=? AND cache.ffprobe_version_sha256=sha256(convert_to(?, 'UTF8')) AND cache.ffprobe_version=? AND cache.analysis_policy_version=?
		AND result.ffprobe_json IS NOT NULL AND result.inspected_at IS NOT NULL AND result.applied_operation_id IS NOT NULL`, digest[:], version, version, policy).Scan(ctx, result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("lookup source probe cache: %w", err)
	}
	return result, true, nil
}

// LookupSourceFingerprint returns the immutable result selected for this
// digest and fpcalc version.
func (repository *SourceInventoryRepository) LookupSourceFingerprint(ctx context.Context, digest [sha256.Size]byte, version string) (*SourceFingerprintResult, bool, error) {
	result := new(SourceFingerprintResult)
	err := repository.db.NewRaw(`SELECT r.* FROM media_fingerprint_cache c
		JOIN media_fingerprint_result r ON r.id=c.result_id AND r.fpcalc_version=c.fpcalc_version
		WHERE c.source_sha256=? AND c.fpcalc_version=?`, digest[:], version).Scan(ctx, result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("lookup source fingerprint cache: %w", err)
	}
	return result, true, nil
}

func registerSourceProbeCache(ctx context.Context, tx bun.IDB, digest []byte, version string, policy int, resultID uuid.UUID) error {
	if len(digest) != sha256.Size || version == "" || policy < 1 || resultID == uuid.Nil {
		return fmt.Errorf("valid digest, version, policy and result are required")
	}
	if _, err := tx.NewRaw(`INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id)
		VALUES(?,?,?,?) ON CONFLICT(source_sha256,ffprobe_version_sha256,analysis_policy_version) DO NOTHING`, digest, version, policy, resultID).Exec(ctx); err != nil {
		return fmt.Errorf("insert cache association: %w", err)
	}
	return nil
}
