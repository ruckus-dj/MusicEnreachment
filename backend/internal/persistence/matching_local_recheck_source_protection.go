package persistence

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// matchingLocalRecheckProtectsSource is specific to automatic unlink. It is not
// a general source hold and does not itself prevent ordinary analysis or reads.
// Unlink callers must hold the source-mutation gate through the native callback;
// Local apply takes the same gate before acquiring this protection.
func matchingLocalRecheckProtectsSource(ctx context.Context, db bun.IDB, workID, locationID uuid.UUID, unlinkTarget string) (bool, error) {
	var protected bool
	if err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM matching_local_recheck_source
		WHERE (work_id=? AND source_location_id=?) OR unlink_target=?
	)`, workID, locationID, unlinkTarget).Scan(ctx, &protected); err != nil {
		return false, fmt.Errorf("read Local recheck source protection: %w", err)
	}
	return protected, nil
}

// matchingLocalRecheckUnlinkTarget identifies the directory entry cleanup would
// unlink. Nested canonical source roots can name that entry with different
// work/location pairs. Distinct hardlink directory entries remain distinct.
func matchingLocalRecheckUnlinkTarget(rootPath, relativePath string) (string, error) {
	relativePath = filepath.FromSlash(relativePath)
	if !filepath.IsAbs(rootPath) || !filepath.IsLocal(relativePath) ||
		filepath.Clean(relativePath) == "." {
		return "", errors.New("local recheck unlink target is invalid")
	}
	return filepath.Join(rootPath, relativePath), nil
}

func matchingLocalRecheckCleanupOwnerExists(ctx context.Context, tx bun.Tx, workID, locationID uuid.UUID, unlinkTarget string) (bool, error) {
	var owners []struct {
		WorkID       uuid.UUID `bun:"work_id"`
		LocationID   uuid.UUID `bun:"source_location_id"`
		RootPath     string    `bun:"configured_path"`
		RelativePath string    `bun:"relative_path"`
	}
	if err := tx.NewRaw(`SELECT owner.work_id,owner.source_location_id,work.configured_path,work.relative_path
		FROM source_cleanup_owner owner JOIN source_analysis_work work ON work.id=owner.work_id`).Scan(ctx, &owners); err != nil {
		return false, fmt.Errorf("read source cleanup owners for local recheck: %w", err)
	}
	for _, owner := range owners {
		if owner.WorkID == workID || owner.LocationID == locationID {
			return true, nil
		}
		target, err := matchingLocalRecheckUnlinkTarget(owner.RootPath, owner.RelativePath)
		if err != nil {
			return false, err
		}
		if target == unlinkTarget {
			return true, nil
		}
	}
	return false, nil
}

// retireMatchingLocalRechecksForRemovedRoot is called under the source-mutation
// and exclusive output gates before removal of the confirmed root. A waiting
// layout has no publication authority and loses its entire exact source scope
// when one root is removed. A transition already handed to publication must
// settle or be explicitly abandoned by publication instead.
func retireMatchingLocalRechecksForRemovedRoot(ctx context.Context, tx bun.Tx, rootID uuid.UUID) error {
	var publishing bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM matching_local_recheck recheck
		JOIN matching_local_recheck_source source ON source.recheck_id=recheck.id
		WHERE source.source_root_id=? AND recheck.state='publishing'
	)`, rootID).Scan(ctx, &publishing); err != nil {
		return fmt.Errorf("read Local rechecks publishing from the root: %w", err)
	}
	if publishing {
		return fmt.Errorf("matching republication still owns sources in the root: %w", ErrSourceRootActiveScan)
	}
	if _, err := tx.NewRaw(`DELETE FROM matching_local_recheck recheck
		WHERE recheck.state='waiting' AND EXISTS (
			SELECT 1 FROM matching_local_recheck_source source
			WHERE source.recheck_id=recheck.id AND source.source_root_id=?
		)`, rootID).Exec(ctx); err != nil {
		return fmt.Errorf("retire waiting Local rechecks for removed root: %w", err)
	}
	return nil
}
