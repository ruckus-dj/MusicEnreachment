package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestIncomingGroupingCorrectionsCASAndInvalidationWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := NewIncomingGroupingRepository(db)
	state, err := repository.State(ctx)
	if err != nil || state.Revision != 0 || !state.NeedsRefresh {
		t.Fatalf("initial state = %+v, %v", state, err)
	}
	first, second := uuid.New(), uuid.New()
	groups := []IncomingGroupingGroup{
		{ID: uuid.New(), Manual: true, Revision: "r1", Members: []uuid.UUID{first}},
		{ID: uuid.New(), Manual: true, Revision: "r2", Members: []uuid.UUID{second}},
	}
	compute := func(_ []IncomingGroupingCapture, _ []IncomingGroupingGroup) ([]IncomingGroupingGroup, error) {
		return groups, nil
	}
	if err := repository.Confirm(ctx, state.Revision, compute); err != nil {
		t.Fatalf("confirm corrections: %v", err)
	}
	if err := repository.Confirm(ctx, state.Revision, compute); !errors.Is(err, ErrIncomingGroupingConflict) {
		t.Fatalf("stale confirmation error = %v, want conflict", err)
	}
	persisted, err := repository.List(ctx)
	if err != nil || len(persisted) != 2 {
		t.Fatalf("persisted groups = %+v, %v", persisted, err)
	}
	for _, group := range persisted {
		if !group.Manual || len(group.Members) != 1 {
			t.Errorf("persisted manual correction = %+v", group)
		}
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return InvalidateIncomingGrouping(ctx, tx)
	}); err != nil {
		t.Fatalf("invalidate grouping atomically: %v", err)
	}
	state, err = repository.State(ctx)
	if err != nil || state.Revision != 2 || !state.NeedsRefresh {
		t.Fatalf("state after invalidation = %+v, %v", state, err)
	}
}
