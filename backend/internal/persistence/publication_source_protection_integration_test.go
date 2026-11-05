//go:build integration

package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This fixture attaches a source snapshot to the publication attempt with valid
// inventory identities, so source ownership is tested independently of nullability.
func pubSourceProtectionHold(t *testing.T) (*SourceInventoryRepository, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	f := newPublicationExecutionFixture(t)
	return pubSourceProtectionHoldWithFixture(t, f)
}

func pubSourceProtectionHoldWithFixture(t *testing.T, f publicationExecutionFixture) (*SourceInventoryRepository, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	repo := NewSourceInventoryRepository(f.db)
	rootID, locationID, workID, variantID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	mtime := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path)
		VALUES (?,'/source','Source',1,'/inventory')`, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,0)`, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_location
		(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id)
		VALUES (?,?,'track.flac',0,?,1,'audio',?)`, locationID, rootID, mtime, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_analysis_work
		(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id)
		VALUES (?,?,?,'/source','/inventory','track.flac',0,?,false,?,?)`,
		workID, locationID, rootID, mtime, f.guard.OperationID, locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_source_hold
		(operation_id,application_attempt,river_job_id,epoch,hold_uuid,source_root_id,source_location_id,work_id,media_variant_id,configured_path,inventory_path,relative_path,size_bytes,mtime)
		VALUES (?,1,?,?,?,?,?,?,?,'/source','/inventory','track.flac',0,?)`,
		f.guard.OperationID, f.guard.RiverJobID, f.guard.Epoch, uuid.New(), rootID, locationID, workID, variantID, mtime); err != nil {
		t.Fatal(err)
	}
	return repo, rootID, variantID, f.guard.OperationID
}
