//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type matchingAdmissionGroupState struct {
	generationRows int
	highWater      int64
	requested      int64
	reconciled     int64
	currentRunID   *uuid.UUID
	runs           int
	operations     int
	deliveries     int
}

func readMatchingAdmissionGroupState(t *testing.T, ctx context.Context, db *bun.DB, groupID uuid.UUID) matchingAdmissionGroupState {
	t.Helper()
	var state matchingAdmissionGroupState
	if err := db.NewRaw(`SELECT count(*),COALESCE(max(high_water),0),COALESCE(max(requested_generation),0),
		COALESCE(max(reconciled_generation),0) FROM incoming_matching_generation WHERE group_id=?`, groupID).
		Scan(ctx, &state.generationRows, &state.highWater, &state.requested, &state.reconciled); err != nil {
		t.Fatalf("read matching admission generation state: %v", err)
	}
	if state.generationRows > 0 {
		if err := db.NewRaw(`SELECT current_run_id FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(ctx, &state.currentRunID); err != nil {
			t.Fatalf("read matching admission current run: %v", err)
		}
	}
	if err := db.NewRaw(`SELECT count(*) FROM matching_run_target WHERE group_id=?`, groupID).Scan(ctx, &state.runs); err != nil {
		t.Fatalf("count matching admission runs: %v", err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id IN (
		SELECT run_id FROM matching_run_target WHERE group_id=?
		UNION SELECT dependency.request_id FROM matching_provider_dependency dependency
		JOIN matching_run_target target ON target.run_id=dependency.parent_run_id WHERE target.group_id=?
		UNION SELECT apply_operation_id FROM matching_apply_request WHERE group_id=?
	)`, groupID, groupID, groupID).Scan(ctx, &state.operations); err != nil {
		t.Fatalf("count matching admission operations: %v", err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM matching_delivery delivery
		JOIN matching_run_target target ON target.run_id=delivery.run_id WHERE target.group_id=?`, groupID).Scan(ctx, &state.deliveries); err != nil {
		t.Fatalf("count matching admission deliveries: %v", err)
	}
	return state
}

func assertMatchingAdmissionGroupStateUnchanged(t *testing.T, ctx context.Context, db *bun.DB, groupID uuid.UUID, before matchingAdmissionGroupState) {
	t.Helper()
	after := readMatchingAdmissionGroupState(t, ctx, db, groupID)
	if !equalMatchingAdmissionGroupState(after, before) {
		t.Fatalf("failed matching admission changed group state: before=%+v after=%+v", before, after)
	}
}

func equalMatchingAdmissionGroupState(left, right matchingAdmissionGroupState) bool {
	if left.generationRows != right.generationRows || left.highWater != right.highWater ||
		left.requested != right.requested || left.reconciled != right.reconciled ||
		left.runs != right.runs || left.operations != right.operations || left.deliveries != right.deliveries {
		return false
	}
	if left.currentRunID == nil || right.currentRunID == nil {
		return left.currentRunID == nil && right.currentRunID == nil
	}
	return *left.currentRunID == *right.currentRunID
}
