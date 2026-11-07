//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestDeleteSourceRootCleansAssociationsForUnlocatedVariantsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	collections := persistence.NewCollectionRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/root-association-cleanup")

	shaVariant := insertAssociationCleanupVariant(t, ctx, database, true)
	digestlessVariant := insertAssociationCleanupVariant(t, ctx, database, false)
	insertAssociationCleanupLocation(t, ctx, database, root.ID, "sha.flac", shaVariant)
	insertAssociationCleanupLocation(t, ctx, database, root.ID, "digestless.flac", digestlessVariant)

	automaticPolicy := "association-cleanup-test"
	for _, association := range []struct {
		variant  uuid.UUID
		method   string
		selectIt bool
		policy   *string
	}{
		{variant: shaVariant, method: "manual", selectIt: true},
		{variant: digestlessVariant, method: "automatic", policy: &automaticPolicy},
	} {
		trackID := uuid.New()
		if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'Root deletion association')`, trackID); err != nil {
			t.Fatalf("insert track: %v", err)
		}
		if err := collections.UpsertTrackSourceDecision(ctx, persistence.TrackSourceDecision{
			TrackID: trackID, MediaVariantID: association.variant, Method: association.method,
			Selected: association.selectIt, Compatible: true, PolicyVersion: association.policy, Metadata: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("insert %s track association: %v", association.method, err)
		}
	}
	assertRootAssociationCounts(t, ctx, database, shaVariant, 1, digestlessVariant, 1)

	if err := deleteInventoryRoot(ctx, inventory, root.ID); err != nil {
		t.Fatalf("delete source root: %v", err)
	}
	assertRootAssociationCounts(t, ctx, database, shaVariant, 0, digestlessVariant, 0)
	for _, variant := range []struct {
		id        uuid.UUID
		wantCount int
	}{{shaVariant, 1}, {digestlessVariant, 0}} {
		var count int
		if err := database.NewRaw(`SELECT count(*) FROM media_variant WHERE id=?`, variant.id).Scan(ctx, &count); err != nil {
			t.Fatalf("count retained media variant %s: %v", variant.id, err)
		}
		if count != variant.wantCount {
			t.Errorf("media variant %s count = %d, want %d", variant.id, count, variant.wantCount)
		}
	}
}

func TestDeleteSourceRootPreservesSharedVariantAssociationsUntilLastLocationWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	collections := persistence.NewCollectionRepository(database)
	firstRoot := createInventoryRoot(t, ctx, inventory, "/srv/shared-association-first")
	secondRoot := createInventoryRoot(t, ctx, inventory, "/srv/shared-association-second")
	variant := insertAssociationCleanupVariant(t, ctx, database, true)
	insertAssociationCleanupLocation(t, ctx, database, firstRoot.ID, "one.flac", variant)
	insertAssociationCleanupLocation(t, ctx, database, secondRoot.ID, "two.flac", variant)
	trackID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'Shared root association')`, trackID); err != nil {
		t.Fatalf("insert track: %v", err)
	}
	if err := collections.UpsertTrackSourceDecision(ctx, persistence.TrackSourceDecision{
		TrackID: trackID, MediaVariantID: variant, Method: "manual", Selected: true,
		Compatible: true, Metadata: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("insert shared track association: %v", err)
	}

	if err := deleteInventoryRoot(ctx, inventory, firstRoot.ID); err != nil {
		t.Fatalf("delete first source root: %v", err)
	}
	assertTrackSourceCount(t, ctx, database, variant, 1)
	var selected bool
	if err := database.NewRaw(`SELECT selected FROM track_source WHERE track_id=? AND media_variant_id=?`, trackID, variant).Scan(ctx, &selected); err != nil || !selected {
		t.Fatalf("shared association selected after first deletion = %t, %v; want true", selected, err)
	}

	if err := deleteInventoryRoot(ctx, inventory, secondRoot.ID); err != nil {
		t.Fatalf("delete last source root: %v", err)
	}
	assertTrackSourceCount(t, ctx, database, variant, 0)
	var variantCount int
	if err := database.NewRaw(`SELECT count(*) FROM media_variant WHERE id=?`, variant).Scan(ctx, &variantCount); err != nil {
		t.Fatalf("count shared SHA variant: %v", err)
	}
	if variantCount != 1 {
		t.Fatalf("shared SHA variant count after last root deletion = %d, want retained cache row", variantCount)
	}
}

func TestDeleteSourceRootWaitsForAssociationWriterBeforeCleanupWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/root-association-writer-race")
	variant := insertAssociationCleanupVariant(t, ctx, database, true)
	insertAssociationCleanupLocation(t, ctx, database, root.ID, "writer.flac", variant)
	trackID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'Concurrent association')`, trackID); err != nil {
		t.Fatalf("insert track: %v", err)
	}

	writerTx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatalf("begin association writer transaction: %v", err)
	}
	defer func() { _ = writerTx.Rollback() }()
	if _, err := writerTx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?, ?)", int32(0x4d545256), int32(4)); err != nil {
		t.Fatalf("lock source fingerprint mutations for writer: %v", err)
	}
	var writerPID int
	if err := writerTx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&writerPID); err != nil {
		t.Fatalf("read association writer backend pid: %v", err)
	}
	if _, err := writerTx.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,false,true,'manual','{}')`, uuid.New(), trackID, variant); err != nil {
		t.Fatalf("insert association under fingerprint gate: %v", err)
	}

	deleteCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	deleteResult := make(chan error, 1)
	deleteStarted := make(chan struct{})
	go func() {
		close(deleteStarted)
		deleteResult <- deleteInventoryRoot(deleteCtx, inventory, root.ID)
	}()
	<-deleteStarted
	if err := waitForSourceRootLockWaiter(deleteCtx, database, writerPID); err != nil {
		t.Fatalf("wait for root deletion to block behind association writer: %v", err)
	}
	if err := writerTx.Commit(); err != nil {
		t.Fatalf("commit association writer: %v", err)
	}
	if err := <-deleteResult; err != nil {
		t.Fatalf("delete root after association writer committed: %v", err)
	}
	assertTrackSourceCount(t, ctx, database, variant, 0)
	var retained int
	if err := database.NewRaw(`SELECT count(*) FROM media_variant WHERE id=?`, variant).Scan(ctx, &retained); err != nil {
		t.Fatalf("count retained SHA cache variant: %v", err)
	}
	if retained != 1 {
		t.Fatalf("SHA cache variant count after contended deletion = %d, want 1", retained)
	}
}

func insertAssociationCleanupVariant(t *testing.T, ctx context.Context, database *bun.DB, withSHA bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if !withSHA {
		if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,123)`, id); err != nil {
			t.Fatalf("insert digestless variant: %v", err)
		}
		return id
	}
	digest := make([]byte, 32)
	copy(digest, id[:])
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES (?,123,?,now(),'SHA-256',?)`, id, digest, uuid.New()); err != nil {
		t.Fatalf("insert SHA variant: %v", err)
	}
	return id
}

func insertAssociationCleanupLocation(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID, path string, variantID uuid.UUID) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,?,?,?,1,'audio',?)`, uuid.New(), rootID, path, 123, time.Now(), variantID); err != nil {
		t.Fatalf("insert source location %s: %v", path, err)
	}
}

func assertRootAssociationCounts(t *testing.T, ctx context.Context, database *bun.DB, first uuid.UUID, firstWant int, second uuid.UUID, secondWant int) {
	t.Helper()
	assertTrackSourceCount(t, ctx, database, first, firstWant)
	assertTrackSourceCount(t, ctx, database, second, secondWant)
}

func assertTrackSourceCount(t *testing.T, ctx context.Context, database *bun.DB, variantID uuid.UUID, want int) {
	t.Helper()
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM track_source WHERE media_variant_id=?`, variantID).Scan(ctx, &count); err != nil {
		t.Fatalf("count track associations for variant %s: %v", variantID, err)
	}
	if count != want {
		t.Fatalf("track associations for variant %s = %d, want %d", variantID, count, want)
	}
}
