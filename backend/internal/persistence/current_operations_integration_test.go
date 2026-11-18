//go:build integration

package persistence

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestListCurrentOperationsStablePagesAndTargetSearchPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := NewSetupManagerRepository(db)

	sharedCreatedAt := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	const seededOperations = 53
	seededIDs := make([]uuid.UUID, 0, seededOperations)
	for index := 0; index < seededOperations; index++ {
		id := uuid.New()
		state := "running"
		var safeError any
		var finishedAt any
		if index%2 == 0 {
			state = "failed"
			safeError = "seeded failure"
			finishedAt = sharedCreatedAt.Add(time.Minute)
		}
		snapshot := fmt.Sprintf(`{"target_identity":"current operation %02d"}`, index)
		if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,safe_error,finished_at,created_at,updated_at)
			VALUES(?,?,?,?,?::jsonb,?,?,?,?)`,
			id, "refresh_genre_catalog_v1", state, "unit", snapshot,
			safeError, finishedAt, sharedCreatedAt, sharedCreatedAt).Exec(ctx); err != nil {
			t.Fatalf("insert seeded operation %d: %v", index, err)
		}
		seededIDs = append(seededIDs, id)
	}
	queuedID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,created_at,updated_at)
		VALUES(?,?,?,?,?::jsonb,?,?)`,
		queuedID, "refresh_genre_catalog_v1", "queued", "unit",
		`{"target_identity":"queued control"}`, sharedCreatedAt.Add(time.Hour), sharedCreatedAt.Add(time.Hour)).Exec(ctx); err != nil {
		t.Fatalf("insert queued control: %v", err)
	}
	completedID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at,created_at,updated_at)
		VALUES(?,?,?,?,?::jsonb,?,?,?)`,
		completedID, "refresh_genre_catalog_v1", "succeeded", "unit",
		`{"target_identity":"completed control"}`, sharedCreatedAt.Add(30*time.Minute),
		sharedCreatedAt.Add(30*time.Minute), sharedCreatedAt.Add(30*time.Minute)).Exec(ctx); err != nil {
		t.Fatalf("insert completed control: %v", err)
	}

	wantOrder := append([]uuid.UUID(nil), seededIDs...)
	sort.Slice(wantOrder, func(left, right int) bool {
		return wantOrder[left].String() > wantOrder[right].String()
	})

	firstPage, total, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{})
	if err != nil {
		t.Fatalf("list default attention page: %v", err)
	}
	if total != seededOperations || len(firstPage) != currentOperationsPageSize {
		t.Fatalf("default attention total/page = %d/%d; want %d/%d", total, len(firstPage), seededOperations, currentOperationsPageSize)
	}
	if got := operationRowIDs(firstPage); !reflect.DeepEqual(got, wantOrder[:currentOperationsPageSize]) {
		t.Fatalf("first page order = %v; want %v", got, wantOrder[:currentOperationsPageSize])
	}

	secondPage, total, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{Offset: currentOperationsPageSize})
	if err != nil {
		t.Fatalf("list second attention page: %v", err)
	}
	if total != seededOperations || len(secondPage) != seededOperations-currentOperationsPageSize {
		t.Fatalf("second page total/page = %d/%d; want %d/%d", total, len(secondPage), seededOperations, seededOperations-currentOperationsPageSize)
	}
	if got := operationRowIDs(secondPage); !reflect.DeepEqual(got, wantOrder[currentOperationsPageSize:]) {
		t.Fatalf("second page order = %v; want %v", got, wantOrder[currentOperationsPageSize:])
	}
	combined := append(operationRowIDs(firstPage), operationRowIDs(secondPage)...)
	if !reflect.DeepEqual(combined, wantOrder) {
		t.Fatalf("combined pages = %v; want %v", combined, wantOrder)
	}
	for _, row := range append(firstPage, secondPage...) {
		if row.ID == queuedID || row.ID == completedID {
			t.Fatalf("default attention included control operation %s", row.ID)
		}
	}

	repeatedFirstPage, repeatedTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{})
	if err != nil {
		t.Fatalf("repeat default attention page: %v", err)
	}
	if repeatedTotal != total || !reflect.DeepEqual(operationRowIDs(repeatedFirstPage), operationRowIDs(firstPage)) {
		t.Fatalf("repeated page = %d/%v; want %d/%v", repeatedTotal, operationRowIDs(repeatedFirstPage), total, operationRowIDs(firstPage))
	}

	allStates, allTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{Status: "all"})
	if err != nil {
		t.Fatalf("list all current states: %v", err)
	}
	if allTotal != seededOperations+1 || len(allStates) != currentOperationsPageSize {
		t.Fatalf("all current states total/page = %d/%d; want %d/%d", allTotal, len(allStates), seededOperations+1, currentOperationsPageSize)
	}
	foundQueued := false
	for _, row := range allStates {
		if row.ID == queuedID {
			foundQueued = true
		}
		if row.ID == completedID {
			t.Fatalf("all current states included completed operation %s", row.ID)
		}
	}
	if !foundQueued {
		t.Fatal("all current states did not include the queued control")
	}

	rootID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO source_root(id,configured_path,display_name) VALUES(?,?,?)`,
		rootID, "/needle-root", "Needle Source Root").Exec(ctx); err != nil {
		t.Fatalf("insert source root: %v", err)
	}
	searchID := uuid.New()
	searchSnapshot := fmt.Sprintf(`{"schema_version":3,"source_root_id":"%s"}`, rootID)
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,safe_error,finished_at,created_at,updated_at)
		VALUES(?,?,?,?,?::jsonb,?,?,?,?)`,
		searchID, "scan_source", "failed", "scan", searchSnapshot, "seeded scan failure",
		sharedCreatedAt.Add(2*time.Hour), sharedCreatedAt.Add(2*time.Hour), sharedCreatedAt.Add(2*time.Hour)).Exec(ctx); err != nil {
		t.Fatalf("insert searchable scan operation: %v", err)
	}

	found, foundTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{Query: "needle source root"})
	if err != nil {
		t.Fatalf("search current operations: %v", err)
	}
	if foundTotal != 1 || len(found) != 1 || found[0].ID != searchID {
		t.Fatalf("search result = %d/%v; want 1/%s", foundTotal, operationRowIDs(found), searchID)
	}
	if found[0].TargetLabel != "Needle Source Root" {
		t.Fatalf("search target label = %q; want %q", found[0].TargetLabel, "Needle Source Root")
	}
	if found[0].TargetRef == nil || found[0].TargetRef.Kind != "source_root" ||
		found[0].TargetRef.RootID == nil || *found[0].TargetRef.RootID != rootID {
		t.Fatalf("search target ref = %#v; want source root %s", found[0].TargetRef, rootID)
	}
}

func operationRowIDs(rows []CurrentOperationRow) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}
