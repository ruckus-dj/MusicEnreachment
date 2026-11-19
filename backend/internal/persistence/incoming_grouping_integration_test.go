//go:build integration

package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestIncomingGroupingCorrectionsCASAndInvalidationWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
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
	if count, current, err := repository.ReadVisibleCount(ctx); err != nil || !current || count != 2 {
		t.Fatalf("clean incoming count = %d, current = %v, err = %v; want two groups", count, current, err)
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
	if count, current, err := repository.ReadVisibleCount(ctx); err != nil || current || count != 0 {
		t.Fatalf("invalidated incoming count = %d, current = %v, err = %v; want no trusted count", count, current, err)
	}
}

func TestIncomingGroupingConfirmRetainsUnreadyOnlyManualGroupWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	repository := NewIncomingGroupingRepository(db)
	state, err := repository.State(ctx)
	if err != nil || state.Revision != 0 || !state.NeedsRefresh {
		t.Fatalf("initial state = %+v, %v", state, err)
	}
	readyLeft, readyRight, unready := uuid.New(), uuid.New(), uuid.New()
	leftID, rightID, priorID := uuid.New(), uuid.New(), uuid.New()
	// A split gives the ready parts two fresh identities; the members that cannot
	// be regrouped stay under the previous manual identity, exactly as the
	// service-layer retainUnreadyMembers step builds them.
	groups := []IncomingGroupingGroup{
		{ID: leftID, Manual: true, Revision: "left", Members: []uuid.UUID{readyLeft}},
		{ID: rightID, Manual: true, Revision: "right", Members: []uuid.UUID{readyRight}},
		{ID: priorID, Manual: true, Revision: "prior", UnreadyMembers: []uuid.UUID{unready}},
	}
	compute := func(computed []IncomingGroupingGroup) IncomingGroupingCompute {
		return func([]IncomingGroupingCapture, []IncomingGroupingGroup) ([]IncomingGroupingGroup, error) {
			return computed, nil
		}
	}
	if err := repository.Confirm(ctx, state.Revision, compute(groups)); err != nil {
		t.Fatalf("confirm split retaining unready manual group: %v", err)
	}
	if err := repository.Confirm(ctx, state.Revision, compute(groups)); !errors.Is(err, ErrIncomingGroupingConflict) {
		t.Fatalf("stale confirmation error = %v, want conflict", err)
	}
	state, err = repository.State(ctx)
	if err != nil || state.Revision != 1 {
		t.Fatalf("state after confirmed split = %+v, %v; want revision 1", state, err)
	}
	persisted, err := repository.List(ctx)
	if err != nil || len(persisted) != 3 {
		t.Fatalf("persisted groups = %+v, %v; want two split parts and the unready remainder", persisted, err)
	}
	byID := make(map[uuid.UUID]IncomingGroupingGroup, len(persisted))
	for _, group := range persisted {
		byID[group.ID] = group
	}
	remainder, exists := byID[priorID]
	if !exists || !remainder.Manual || len(remainder.Members) != 0 || len(remainder.UnreadyMembers) != 1 || remainder.UnreadyMembers[0] != unready {
		t.Fatalf("persisted unready remainder = %+v, exists = %v", remainder, exists)
	}
	for _, id := range []uuid.UUID{leftID, rightID} {
		if group, exists := byID[id]; !exists || len(group.Members) != 1 || len(group.UnreadyMembers) != 0 {
			t.Fatalf("persisted split part %v = %+v, exists = %v", id, group, exists)
		}
	}
	var readiness []bool
	if err := db.NewRaw(`SELECT ready FROM incoming_group_member WHERE group_id=? ORDER BY ready`, priorID).Scan(ctx, &readiness); err != nil {
		t.Fatalf("read remainder member readiness: %v", err)
	}
	if len(readiness) != 1 || readiness[0] {
		t.Fatalf("remainder member readiness = %v; want one unready member", readiness)
	}
	epoch := state.Revision
	invalid := []struct {
		name  string
		group IncomingGroupingGroup
	}{
		{name: "nil identity", group: IncomingGroupingGroup{Manual: true, Revision: "nil", Members: []uuid.UUID{uuid.New()}}},
		{name: "empty manual correction", group: IncomingGroupingGroup{ID: uuid.New(), Manual: true, Revision: "empty"}},
		{name: "automatic unready only", group: IncomingGroupingGroup{ID: uuid.New(), Revision: "automatic", UnreadyMembers: []uuid.UUID{uuid.New()}}},
	}
	for _, item := range invalid {
		computed := append(append([]IncomingGroupingGroup(nil), groups...), item.group)
		if err := repository.Confirm(ctx, epoch, compute(computed)); err == nil || !strings.Contains(err.Error(), "computed incoming group is invalid") {
			t.Fatalf("%s rejection error = %v", item.name, err)
		}
	}
	persisted, err = repository.List(ctx)
	if err != nil || len(persisted) != 3 {
		t.Fatalf("persisted groups after rejected computations = %+v, %v; want rollback to the confirmed split", persisted, err)
	}
	state, err = repository.State(ctx)
	if err != nil || state.Revision != epoch {
		t.Fatalf("state after rejected computations = %+v, %v; want revision %d", state, err, epoch)
	}
}
