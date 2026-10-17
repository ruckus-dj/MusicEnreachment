//go:build integration

package persistence

import (
	"context"
	"errors"
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

func TestOutputResetAllowsQueuedPublicationSourceHoldPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	pubSourceProtectionHoldWithFixture(t, f)
	oldRoot, newRoot := nativeTestPath(t, "/music/old"), nativeTestPath(t, "/music/new")
	if err := NewSettingsRepository(f.db).Set(ctx, "output_directory", oldRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='available' WHERE id=?`, f.guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	_, err := NewSetupManagerRepository(f.db).RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedOldRoot: oldRoot, NewRoot: newRoot,
		Prepare: func(context.Context, func(OutputResetDirectory) error) error { return nil },
		Finish:  func(context.Context, OutputResetJournal) error { return nil },
	})
	if err != nil {
		t.Fatalf("reset with queued source hold: %v", err)
	}
}

func TestOutputResetBlocksNonqueuedPublicationSourceHoldPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	pubSourceProtectionHoldWithFixture(t, f)
	oldRoot, newRoot := nativeTestPath(t, "/music/old"), nativeTestPath(t, "/music/new")
	if err := NewSettingsRepository(f.db).Set(ctx, "output_directory", oldRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE operation SET state='succeeded',finished_at=now() WHERE id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	_, err := NewSetupManagerRepository(f.db).RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedOldRoot: oldRoot, NewRoot: newRoot,
		Prepare: func(context.Context, func(OutputResetDirectory) error) error { return nil },
		Finish:  func(context.Context, OutputResetJournal) error { return nil },
	})
	if err == nil {
		t.Fatal("reset with nonqueued source hold succeeded")
	}
}

func TestPublicationSourceHoldProtectsRootButAllowsDisplayNameEditPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, rootID, _, _ := pubSourceProtectionHold(t)
	root, err := repo.GetSourceRoot(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	root.DisplayName = "Renamed"
	if err := repo.UpdateSourceRoot(ctx, root); err != nil {
		t.Fatalf("display-name-only edit: %v", err)
	}
	root.ConfiguredPath = "/elsewhere"
	if err := repo.UpdateSourceRoot(ctx, root); !errors.Is(err, ErrSourceRootActiveScan) {
		t.Fatalf("held path change error = %v, want ErrSourceRootActiveScan", err)
	}
	if err := repo.DeleteSourceRoot(ctx, rootID, "/source", 1); !errors.Is(err, ErrSourceRootActiveScan) {
		t.Fatalf("held root delete error = %v, want ErrSourceRootActiveScan", err)
	}
}

func TestPublicationSourceHoldRetainsDigestlessVariantOnCleanupPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, _, variantID, _ := pubSourceProtectionHold(t)
	// Remove the ordinary inventory reference so the durable publication hold
	// is the only reason this digestless variant survives orphan cleanup.
	if _, err := repo.db.NewRaw(`UPDATE source_location SET media_variant_id=NULL WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.CleanupSourceMediaVariants(ctx, []uuid.UUID{variantID}); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := repo.db.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_variant WHERE id=?)`, variantID).Scan(ctx, &exists); err != nil || !exists {
		t.Fatalf("held digestless variant exists=%t, err=%v", exists, err)
	}
	if _, err := repo.db.NewRaw(`DELETE FROM publication_source_hold WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.CleanupSourceMediaVariants(ctx, []uuid.UUID{variantID}); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_variant WHERE id=?)`, variantID).Scan(ctx, &exists); err != nil || exists {
		t.Fatalf("released digestless variant exists=%t, err=%v", exists, err)
	}
}
