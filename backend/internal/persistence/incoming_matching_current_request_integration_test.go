//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestCurrentMatchingRequestsCoalesceAndAcknowledgeObservedGenerationPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(db)
	groupID := uuid.New()
	request := func() error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return persistence.RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{groupID, groupID})
		})
	}
	if err := request(); err != nil {
		t.Fatal(err)
	}
	if err := request(); err != nil {
		t.Fatal(err)
	}
	requests, err := repository.ReadPendingCurrentRequests(ctx)
	if err != nil || len(requests) != 1 || requests[0].RequestedGeneration != 2 || requests[0].ReconciledGeneration != 0 {
		t.Fatalf("coalesced requests = %#v, %v", requests, err)
	}
	var wakes int
	if err := db.NewRaw(`SELECT count(*) FROM incoming_matching_reconcile_event WHERE reason='current' AND state='pending'`).Scan(ctx, &wakes); err != nil || wakes != 1 {
		t.Fatalf("pending current wake count = %d, %v; want 1", wakes, err)
	}
	if err := repository.AcknowledgeCurrentRequest(ctx, groupID, 1); err != nil {
		t.Fatal(err)
	}
	if err := request(); err != nil {
		t.Fatal(err)
	}
	requests, err = repository.ReadPendingCurrentRequests(ctx)
	if err != nil || len(requests) != 1 || requests[0].RequestedGeneration != 3 || requests[0].ReconciledGeneration != 1 {
		t.Fatalf("request after generation-one acknowledgement = %#v, %v", requests, err)
	}
	rolledBackID := uuid.New()
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := persistence.RequestIncomingMatchingReconciliationWith(ctx, tx, []uuid.UUID{rolledBackID}); err != nil {
			return err
		}
		return errors.New("rollback current request")
	})
	if err == nil {
		t.Fatal("transaction with rollback sentinel committed")
	}
	var rows int
	if err := db.NewRaw(`SELECT count(*) FROM incoming_matching_generation WHERE group_id=?`, rolledBackID).Scan(ctx, &rows); err != nil || rows != 0 {
		t.Fatalf("rolled-back generation row count = %d, %v", rows, err)
	}
}
