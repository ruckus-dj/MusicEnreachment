//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestIncomingGroupingInvalidatesOnMetadataAndRootMutationOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.OpenMigrated(t)
	repository := persistence.NewSourceInventoryRepository(database)
	grouping := persistence.NewIncomingGroupingRepository(database)
	root := createInventoryRoot(t, ctx, repository, "/srv/incoming-group-invalidation")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, false,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepMetadata, State: "pending"},
	)
	fixture := newAnalysisStepFixture(t, ctx, database, repository, openScanEnqueueRiver(t, database), root, location, work,
		persistence.SourceAnalysisModeBatch, nil, nil, false, nil)
	stepAttempt := fixture.claim(persistence.SourceStepMetadata)

	initial, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read initial incoming grouping state: %v", err)
	}
	metadata := persistence.SourceMetadataApply{
		WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: stepAttempt,
		ObservedTags: json.RawMessage(`{"ARTIST":["fixture"]}`),
		Provenance:   json.RawMessage(`{"source":"test"}`), ObservedAt: time.Now().UTC(),
	}
	if _, err := repository.ApplySourceMetadata(ctx, metadata); err != nil {
		t.Fatalf("apply metadata: %v", err)
	}
	afterMetadata, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read incoming grouping after metadata apply: %v", err)
	}
	if afterMetadata.Revision != initial.Revision+1 || !afterMetadata.NeedsRefresh {
		t.Fatalf("incoming grouping after metadata apply = %+v; want revision %d and needs refresh", afterMetadata, initial.Revision+1)
	}

	stale := metadata
	stale.OperationAttempt++
	if _, err := repository.ApplySourceMetadata(ctx, stale); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("stale metadata apply = %v; want ErrSourceAnalysisStale", err)
	}
	afterStale, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read incoming grouping after stale apply: %v", err)
	}
	if afterStale != afterMetadata {
		t.Fatalf("stale metadata apply changed incoming grouping state: before %+v, after %+v", afterMetadata, afterStale)
	}

	updated, err := repository.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read source root before edit: %v", err)
	}
	updated.DisplayName = "edited root"
	if err := repository.UpdateSourceRoot(ctx, updated); err != nil {
		t.Fatalf("update source root: %v", err)
	}
	afterRootEdit, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read incoming grouping after root edit: %v", err)
	}
	if afterRootEdit.Revision != afterStale.Revision+1 || !afterRootEdit.NeedsRefresh {
		t.Fatalf("incoming grouping after root edit = %+v; want revision %d and needs refresh", afterRootEdit, afterStale.Revision+1)
	}
}
