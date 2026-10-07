//go:build integration

package persistence_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// TestSourceLocationDetailIsOneSnapshotAcrossAnalysisApplyWithPostgreSQL
// establishes the detail reader's snapshot before the first atomic apply commits.
// It must return the old state (no result, still-active operation), never a mixed
// view containing neither.
func TestSourceLocationDetailIsOneSnapshotAcrossAnalysisApplyWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), analysisRaceTimeout)
	defer cancel()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/detail-apply-snapshot")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, inventory, root, location, false,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"})
	tool := insertVerifiedAnalysisTool(t, ctx, database, "ffmpeg", "ffprobe", "7.1", "ffprobe version 7.1")
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, false, false,
		[]persistence.SourceAnalysisToolSelection{tool})
	admitAndRunNormalizedAnalysis(t, ctx, database, inventory, openScanEnqueueRiver(t, database), operation)
	stepAttempt, err := inventory.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: *operation.RiverJobID, Step: persistence.SourceStepProbe,
	})
	if err != nil {
		t.Fatalf("claim normalized probe: %v", err)
	}

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
	if _, err := inventory.ApplySourceProbe(ctx, persistence.SourceProbeApply{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: *operation.RiverJobID, StepAttempt: stepAttempt, SizeBytes: location.SizeBytes,
		AnalysisPolicy: persistence.SourceAnalysisPolicyVersion, FFProbeVersion: tool.VersionBanner,
		FFProbeJSON: []byte(`{"format":{"format_name":"flac"}}`), ObservedTags: []byte(`{}`),
		InspectedAt: time.Now().UTC().Truncate(time.Microsecond), AudioStreamCount: 1,
	}); err != nil {
		t.Fatalf("apply normalized probe while detail is paused: %v", err)
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
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), analysisRaceTimeout)
	defer cancel()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/detail-orphan-delete")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	variantID := insertMediaVariantRow(t, ctx, database, location.SizeBytes, uuid.New())
	linkLocationVariant(t, ctx, database, location.ID, variantID)
	location = readLocationByID(t, ctx, database, location.ID)

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
