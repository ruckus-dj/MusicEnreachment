//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// TestSourceAnalysisFailureWakesSubscriberPostgreSQL arms a real subscriber
// before the failing action and proves the worker wakes it only after the
// failure and the release of both read holds are committed: at the wake the
// re-read operation is failed with no holds and the previous variant is still
// linked. The disabled root makes the worker fail before probing, so this wake is
// the failure notification itself and not an earlier stage transition.
func TestSourceAnalysisFailureWakesSubscriberPostgreSQL(t *testing.T) {
	fixture := newAnalysisDispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	previous := fixture.insertLinkedPreviousVariant(t, ctx)
	operation := fixture.insertQueuedAnalysisWithPrevious(t, ctx, previous)
	if _, err := fixture.database.ExecContext(ctx, "UPDATE source_root SET enabled = false WHERE id = ?", fixture.root.ID); err != nil {
		t.Fatalf("disable the root: %v", err)
	}

	// The subscriber is armed before the failing delivery and never polls.
	wake, unsubscribe := fixture.operations.Subscribe(operation.ID)
	defer unsubscribe()

	awaitRiverCompletion(t, ctx, fixture.events, fixture.deliver(t, ctx, operation.ID))

	select {
	case <-wake:
	case <-ctx.Done():
		t.Fatalf("no wake after the committed failure: %v", ctx.Err())
	}
	stored := fixture.readOperation(t, ctx, operation.ID)
	if stored.State != "failed" || stored.Stage != service.SourceAnalysisStageQueued {
		t.Fatalf("operation at wake = %s/%s, want failed/queued", stored.State, stored.Stage)
	}
	requireScanDispatchSafeError(t, stored, analysisSafeDisabled)
	requireAnalysisHolds(t, stored, nil, nil)
	location, err := fixture.inventory.GetSourceLocation(ctx, fixture.root.ID, fixture.track.ID)
	if err != nil {
		t.Fatalf("read the analyzed location: %v", err)
	}
	if location.MediaVariantID == nil || *location.MediaVariantID != previous {
		t.Fatalf("previous variant link = %v, want the unchanged %s", location.MediaVariantID, previous)
	}
}

// insertLinkedPreviousVariant writes one stored variant and links the fixture's
// location to it, which is the previous result a failing analysis must keep.
func (fixture *analysisDispatchFixture) insertLinkedPreviousVariant(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	variant := &persistence.MediaVariant{
		ID: uuid.New(), SizeBytes: fixture.track.SizeBytes,
		AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion,
		FFProbeVersion:        "ffprobe version " + analysisDispatchRelease,
		FFProbeJSON:           json.RawMessage(`{"format":{},"streams":[]}`),
		ObservedTags:          json.RawMessage(`{}`),
		InspectedAt:           time.Now().UTC(), AppliedOperationID: uuid.New(),
	}
	if _, err := fixture.database.NewInsert().Model(variant).Exec(ctx); err != nil {
		t.Fatalf("insert the previous variant: %v", err)
	}
	if _, err := fixture.database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
		Set("media_variant_id = ?", variant.ID).Set("updated_at = now()").
		Where("id = ?", fixture.track.ID).Exec(ctx); err != nil {
		t.Fatalf("link the previous variant: %v", err)
	}
	fixture.track.MediaVariantID = &variant.ID
	return variant.ID
}

// insertQueuedAnalysisWithPrevious inserts one queued analysis whose snapshot and
// both read holds pin the fixture's location and the previous variant.
func (fixture *analysisDispatchFixture) insertQueuedAnalysisWithPrevious(t *testing.T, ctx context.Context, previous uuid.UUID) *persistence.Operation {
	t.Helper()
	snapshot, err := json.Marshal(persistence.SourceAnalysisSnapshot{
		SchemaVersion:          persistence.SourceAnalysisSnapshotVersion,
		SourceRootID:           fixture.root.ID,
		SourceLocationID:       fixture.track.ID,
		ConfiguredPath:         fixture.root.ConfiguredPath,
		InventoryPath:          fixture.root.ConfiguredPath,
		RelativePath:           fixture.track.RelativePath,
		SizeBytes:              fixture.track.SizeBytes,
		Mtime:                  fixture.track.Mtime,
		PreviousVariantID:      &previous,
		AnalysisPolicyVersion:  persistence.SourceAnalysisPolicyVersion,
		AnalysisInstallationID: fixture.installationID,
	})
	if err != nil {
		t.Fatalf("marshal the analysis snapshot: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: "queued", Stage: service.SourceAnalysisStageQueued, Attempt: 1,
		InputSnapshot:          snapshot,
		TargetSourceRootID:     &fixture.root.ID,
		TargetSourceLocationID: &fixture.track.ID,
		AnalysisInstallationID: &fixture.installationID,
		AnalysisMediaVariantID: &previous,
	}
	if _, err := fixture.database.NewInsert().Model(operation).Exec(ctx); err != nil {
		t.Fatalf("insert the queued analysis: %v", err)
	}
	return operation
}
