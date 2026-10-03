//go:build integration

package persistence_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// TestSourceLocationDetailIsOneSnapshotAcrossAnalysisApplyWithPostgreSQL
// establishes the detail reader's snapshot before the first atomic apply commits.
// It must return the old state (no result, still-active operation), never a mixed
// view containing neither.
func TestSourceLocationDetailIsOneSnapshotAcrossAnalysisApplyWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), analysisRaceTimeout)
	defer cancel()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/detail-apply-snapshot")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "detail-apply-snapshot")
	operation := insertRunningAnalysisOperation(t, ctx, database, root, location, installationID, nil)

	barrier := newPausingQueryBarrier("source_location_detail_active")
	barrier.armed.Store(true)
	database.AddQueryHook(barrier)
	readResult := make(chan *persistence.SourceLocationDetailSnapshot, 1)
	readErr := make(chan error, 1)
	go func() {
		snapshot, err := inventory.ReadSourceLocationDetail(ctx, root.ID, location.ID)
		readResult <- snapshot
		readErr <- err
	}()
	awaitSignal(t, barrier.attempted, "the detail read to reach its active-operation query")
	if _, err := inventory.ApplyAnalysisResult(ctx, analysisApplyFor(operation, location, "7.1")); err != nil {
		t.Fatalf("apply the first analysis while detail is paused: %v", err)
	}
	close(barrier.release)
	snapshot := awaitValue(t, readResult, "the detail read")
	if err := awaitResult(t, readErr, "the detail read to finish"); err != nil {
		t.Fatalf("read source location detail: %v", err)
	}
	if snapshot.Location.MediaVariantID != nil || snapshot.Variant != nil || snapshot.ActiveOperationID == nil || *snapshot.ActiveOperationID != operation.ID {
		t.Fatalf("detail snapshot after apply = location variant %v, variant %v, active %v; want old no-result state with active operation %s",
			snapshot.Location.MediaVariantID, snapshot.Variant, snapshot.ActiveOperationID, operation.ID)
	}
}

// TestSourceLocationDetailSurvivesConcurrentOrphanVariantDeleteWithPostgreSQL
// deletes a genuinely unheld variant after the reader has established its
// snapshot and reached the variant query. The read must neither lock the row nor
// fail because the concurrent unlink/delete committed.
func TestSourceLocationDetailSurvivesConcurrentOrphanVariantDeleteWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), analysisRaceTimeout)
	defer cancel()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/detail-orphan-delete")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "detail-orphan-delete")
	operation := insertRunningAnalysisOperation(t, ctx, database, root, location, installationID, nil)
	if _, err := inventory.ApplyAnalysisResult(ctx, analysisApplyFor(operation, location, "7.1")); err != nil {
		t.Fatalf("apply initial analysis: %v", err)
	}
	location = readLocationByID(t, ctx, database, location.ID)
	variantID := *location.MediaVariantID

	barrier := newPausingQueryBarrier("source_location_detail_variant")
	barrier.armed.Store(true)
	database.AddQueryHook(barrier)
	readResult := make(chan *persistence.SourceLocationDetailSnapshot, 1)
	readErr := make(chan error, 1)
	go func() {
		snapshot, err := inventory.ReadSourceLocationDetail(ctx, root.ID, location.ID)
		readResult <- snapshot
		readErr <- err
	}()
	awaitSignal(t, barrier.attempted, "the detail read to reach its variant query")
	if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
		Set("media_variant_id = NULL").Where("id = ?", location.ID).Exec(ctx); err != nil {
		t.Fatalf("unlink the now-unheld variant: %v", err)
	}
	if _, err := database.NewDelete().Model((*persistence.MediaVariant)(nil)).Where("id = ?", variantID).Exec(ctx); err != nil {
		t.Fatalf("delete the orphan variant: %v", err)
	}
	close(barrier.release)
	snapshot := awaitValue(t, readResult, "the detail read")
	if err := awaitResult(t, readErr, "the detail read to finish"); err != nil {
		t.Fatalf("read source location detail while deleting an orphan: %v", err)
	}
	if snapshot.Location.MediaVariantID == nil || *snapshot.Location.MediaVariantID != variantID || snapshot.Variant == nil || snapshot.Variant.ID != variantID {
		t.Fatalf("detail snapshot across orphan deletion = %+v, want the pre-delete linked result", snapshot)
	}
}

type pausingQueryBarrier struct {
	needle    string
	armed     atomic.Bool
	once      sync.Once
	attempted chan struct{}
	release   chan struct{}
}

func newPausingQueryBarrier(needle string) *pausingQueryBarrier {
	return &pausingQueryBarrier{needle: needle, attempted: make(chan struct{}), release: make(chan struct{})}
}

func (b *pausingQueryBarrier) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if b.armed.Load() && strings.Contains(event.Query, b.needle) {
		b.once.Do(func() { close(b.attempted) })
		<-b.release
	}
	return ctx
}

func (*pausingQueryBarrier) AfterQuery(context.Context, *bun.QueryEvent) {}

func awaitValue[T any](t *testing.T, result <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(analysisRaceTimeout):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}
