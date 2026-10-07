//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// TestSourceAnalysisFailureWakesSubscriberPostgreSQL arms a real subscriber
// before the failing action and proves the worker wakes it only after the
// failed step and release of its work and managed-tool read holds are committed:
// at the authoritative re-read batch bookkeeping succeeded with no holds and
// the previous variant is still linked. Removing the source file makes a normal
// step failure occur after the delivery starts, so this wake is the failure
// notification itself and not an earlier stage transition.
func TestSourceAnalysisFailureWakesSubscriberPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAnalysisDispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	previous := fixture.insertLinkedPreviousVariant(t, ctx)
	operation := fixture.startScheduled(t, ctx)
	if err := os.Remove(filepath.Join(fixture.source, "album", "track.flac")); err != nil {
		t.Fatalf("remove the source file: %v", err)
	}

	// Keep a subscription armed across the delivery. Every wake is only a hint;
	// intermediate running/probing notifications require a fresh snapshot and
	// another subscription rather than being mistaken for terminal data.
	wake, unsubscribe := fixture.operations.Subscribe(operation.ID)
	defer func() { unsubscribe() }()
	if _, err := fixture.database.ExecContext(ctx, "UPDATE river_job SET state='available', scheduled_at=now() WHERE id=?", *operation.RiverJobID); err != nil {
		t.Fatalf("make the admitted delivery available: %v", err)
	}
	awaitRiverCompletion(t, ctx, fixture.events, *operation.RiverJobID)
	for {
		stored := fixture.readOperation(t, ctx, operation.ID)
		if stored.State == "succeeded" {
			if stored.Stage != service.SourceAnalysisStageApplying {
				t.Fatalf("operation at terminal snapshot = succeeded/%s, want succeeded/applying", stored.Stage)
			}
			requireAnalysisStepSafeError(t, ctx, fixture, operation.ID, persistence.SourceStepProbe, "The source file is unavailable. The previous result is unchanged.")
			requireAnalysisHolds(t, ctx, fixture, operation.ID, nil, nil)
			location, err := fixture.inventory.GetSourceLocation(ctx, fixture.root.ID, fixture.track.ID)
			if err != nil {
				t.Fatalf("read the analyzed location: %v", err)
			}
			if location.MediaVariantID == nil || *location.MediaVariantID != previous {
				t.Fatalf("previous variant link = %v, want the unchanged %s", location.MediaVariantID, previous)
			}
			return
		}
		if stored.State == "failed" {
			t.Fatalf("batch bookkeeping state = failed/%s; want succeeded with a failed probe step", stored.Stage)
		}
		select {
		case <-wake:
			previousUnsubscribe := unsubscribe
			wake, unsubscribe = fixture.operations.Subscribe(operation.ID)
			previousUnsubscribe()
		case <-ctx.Done():
			t.Fatalf("no wake before terminal snapshot; operation remains %s/%s: %v", stored.State, stored.Stage, ctx.Err())
		}
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
