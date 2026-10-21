//go:build integration

package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func TestPublicationSourceCleanupOwnerProtectsRootButAllowsDisplayNameEditPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := publicationSourceCleanupReadyFixture(t)
	request := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
	inserter := new(publicationSourceCleanupTestInserter)
	admitted, err := repo.AdmitPublicationSourceCleanup(ctx, request, inserter)
	if err != nil || admitted.OperationID == uuid.Nil || len(admitted.Blockers) != 0 {
		t.Fatalf("admission = %+v, %v", admitted, err)
	}
	var owner SourceCleanupOwner
	if err := f.db.NewSelect().Model(&owner).Where("operation_id=?", admitted.OperationID).Scan(ctx); err != nil {
		t.Fatalf("read cleanup owner: %v", err)
	}
	// A terminal owner state does not release the pin: ownership is durable, not
	// an assumption about the operation still being active.
	if _, err := f.db.ExecContext(ctx, `UPDATE operation SET state='failed',safe_error='cleanup fixture failed',finished_at=now() WHERE id=?`, admitted.OperationID); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct {
		name                 string
		root, location, work uuid.UUID
	}{
		{"root", owner.SourceRootID, uuid.Nil, uuid.Nil},
		{"location", uuid.Nil, owner.SourceLocationID, uuid.Nil},
		{"work", uuid.Nil, uuid.Nil, owner.WorkID},
	} {
		held, err := sourceHoldExists(ctx, f.db, scope.root, scope.location, scope.work)
		if err != nil || !held {
			t.Fatalf("%s-scoped cleanup ownership after failed operation = %t, %v; want true", scope.name, held, err)
		}
	}
	// Each unrelated scope is queried alone, proving a held identity does not
	// broaden through the root branch.
	for _, scope := range []struct {
		name                 string
		root, location, work uuid.UUID
	}{
		{"root", uuid.New(), uuid.Nil, uuid.Nil},
		{"location", uuid.Nil, uuid.New(), uuid.Nil},
		{"work", uuid.Nil, uuid.Nil, uuid.New()},
	} {
		held, err := sourceHoldExists(ctx, f.db, scope.root, scope.location, scope.work)
		if err != nil || held {
			t.Fatalf("unrelated %s = %t, %v; want false", scope.name, held, err)
		}
	}
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return retireSourceAnalysisWork(ctx, tx, owner.WorkID)
	}); err == nil || !strings.Contains(err.Error(), "publication hold") {
		t.Fatalf("cleanup-owned work retirement error = %v, want publication hold", err)
	}
	var active int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM operation WHERE target_source_root_id=? AND state IN ('queued','running')`, owner.SourceRootID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("fixture has %d active root operations; the guard assertions would not isolate the cleanup owner", active)
	}
	inventory := NewSourceInventoryRepository(f.db)
	root, err := inventory.GetSourceRoot(ctx, owner.SourceRootID)
	if err != nil {
		t.Fatal(err)
	}
	confirmedPath := root.ConfiguredPath
	root.DisplayName = "Renamed"
	if err := inventory.UpdateSourceRoot(ctx, root); err != nil {
		t.Fatalf("display-name-only edit: %v", err)
	}
	root.ConfiguredPath = confirmedPath + "-moved"
	if err := inventory.UpdateSourceRoot(ctx, root); !errors.Is(err, ErrSourceRootActiveScan) {
		t.Fatalf("cleanup-owned path change error = %v, want ErrSourceRootActiveScan", err)
	}
	var locations int64
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM source_location WHERE source_root_id=?`, owner.SourceRootID).Scan(&locations); err != nil {
		t.Fatal(err)
	}
	if err := inventory.DeleteSourceRoot(ctx, owner.SourceRootID, confirmedPath, locations); !errors.Is(err, ErrSourceRootActiveScan) {
		t.Fatalf("cleanup-owned root delete error = %v, want ErrSourceRootActiveScan", err)
	}
}
