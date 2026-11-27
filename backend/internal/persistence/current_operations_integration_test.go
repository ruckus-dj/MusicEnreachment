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

func TestListCurrentOperationsStablePagesSortingAndTargetSearchPostgreSQL(t *testing.T) {
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
	if _, err := db.NewRaw(`INSERT INTO source_root(id,configured_path,display_name,processing_mode) VALUES(?,?,?,'in_place')`,
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

	// Sorting must apply to the whole filtered set before offset/limit. The
	// named roots below interleave label order with creation order: Aardvark
	// Archive is the oldest row, so a listing that pages by creation first
	// cannot put it on the first target-ascending page, while Zephyr Vault is
	// the newest row and therefore sorts last by target label.
	insertScanRoot := func(path, displayName string, createdAt time.Time) uuid.UUID {
		t.Helper()
		namedRootID := uuid.New()
		if _, err := db.NewRaw(`INSERT INTO source_root(id,configured_path,display_name,processing_mode) VALUES(?,?,?,'in_place')`,
			namedRootID, path, displayName).Exec(ctx); err != nil {
			t.Fatalf("insert source root %q: %v", displayName, err)
		}
		namedOperationID := uuid.New()
		snapshot := fmt.Sprintf(`{"schema_version":3,"source_root_id":"%s"}`, namedRootID)
		if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,safe_error,finished_at,created_at,updated_at)
			VALUES(?,?,?,?,?::jsonb,?,?,?,?)`,
			namedOperationID, "scan_source", "failed", "scan", snapshot, "seeded scan failure",
			createdAt, createdAt, createdAt).Exec(ctx); err != nil {
			t.Fatalf("insert source root operation %q: %v", displayName, err)
		}
		return namedOperationID
	}
	aardvarkID := insertScanRoot("/aardvark-root", "Aardvark Archive", sharedCreatedAt.Add(-time.Hour))
	auroraID := insertScanRoot("/aurora-root", "Aurora Archive", sharedCreatedAt.Add(3*time.Hour))
	zephyrID := insertScanRoot("/zephyr-root", "Zephyr Vault", sharedCreatedAt.Add(4*time.Hour))

	// Default attention now holds the 53 seeds plus the failed needle scan and
	// the three named roots: 57 rows, of which only the four named roots
	// resolve a non-empty target label.
	const sortedOperationsTotal = seededOperations + 4
	wantTargetAsc := []uuid.UUID{aardvarkID, auroraID, searchID, zephyrID}
	wantTargetDesc := []uuid.UUID{zephyrID, searchID, auroraID, aardvarkID}

	targetAscPage, targetAscTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{
		Sort: "target", Direction: "asc", Locale: "en",
	})
	if err != nil {
		t.Fatalf("list target ascending page: %v", err)
	}
	if targetAscTotal != sortedOperationsTotal || len(targetAscPage) != currentOperationsPageSize {
		t.Fatalf("target ascending total/page = %d/%d; want %d/%d",
			targetAscTotal, len(targetAscPage), sortedOperationsTotal, currentOperationsPageSize)
	}
	if got := operationRowIDs(targetAscPage[:len(wantTargetAsc)]); !reflect.DeepEqual(got, wantTargetAsc) {
		t.Fatalf("target ascending named order = %v; want %v (oldest row must lead the sorted page)", got, wantTargetAsc)
	}
	for _, row := range targetAscPage[len(wantTargetAsc):] {
		if row.TargetLabel != "" {
			t.Fatalf("target ascending placed resolved row %s after unresolved labels", row.ID)
		}
	}
	targetAscRest, targetAscRestTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{
		Sort: "target", Direction: "asc", Locale: "en", Offset: currentOperationsPageSize,
	})
	if err != nil {
		t.Fatalf("list target ascending second page: %v", err)
	}
	if targetAscRestTotal != sortedOperationsTotal || len(targetAscRest) != sortedOperationsTotal-currentOperationsPageSize {
		t.Fatalf("target ascending second page total/page = %d/%d; want %d/%d",
			targetAscRestTotal, len(targetAscRest), sortedOperationsTotal, sortedOperationsTotal-currentOperationsPageSize)
	}
	for _, row := range targetAscRest {
		if row.TargetLabel != "" {
			t.Fatalf("target ascending resolved row %s appeared after unresolved labels", row.ID)
		}
	}

	targetDescPage, targetDescTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{
		Sort: "target", Direction: "desc", Locale: "en",
	})
	if err != nil {
		t.Fatalf("list target descending page: %v", err)
	}
	if targetDescTotal != sortedOperationsTotal || len(targetDescPage) != currentOperationsPageSize {
		t.Fatalf("target descending total/page = %d/%d; want %d/%d",
			targetDescTotal, len(targetDescPage), sortedOperationsTotal, currentOperationsPageSize)
	}
	if got := operationRowIDs(targetDescPage[:len(wantTargetDesc)]); !reflect.DeepEqual(got, wantTargetDesc) {
		t.Fatalf("target descending named order = %v; want %v (unresolved labels stay last)", got, wantTargetDesc)
	}
	for _, row := range targetDescPage[len(wantTargetDesc):] {
		if row.TargetLabel != "" {
			t.Fatalf("target descending resolved row %s appeared before unresolved labels", row.ID)
		}
	}

	targetRussianPage, targetRussianTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{
		Sort: "target", Direction: "asc", Locale: "ru",
	})
	if err != nil {
		t.Fatalf("list target ascending russian page: %v", err)
	}
	if targetRussianTotal != sortedOperationsTotal ||
		!reflect.DeepEqual(operationRowIDs(targetRussianPage), operationRowIDs(targetAscPage)) {
		t.Fatalf("russian target page = %d/%v; want %d/%v",
			targetRussianTotal, operationRowIDs(targetRussianPage), targetAscTotal, operationRowIDs(targetAscPage))
	}

	// Creation order stays the default ordering, newest first.
	defaultPage, defaultTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{})
	if err != nil {
		t.Fatalf("list default page after named roots: %v", err)
	}
	if defaultTotal != sortedOperationsTotal || len(defaultPage) != currentOperationsPageSize {
		t.Fatalf("default total/page = %d/%d; want %d/%d",
			defaultTotal, len(defaultPage), sortedOperationsTotal, currentOperationsPageSize)
	}
	if got := operationRowIDs(defaultPage[:3]); !reflect.DeepEqual(got, []uuid.UUID{zephyrID, auroraID, searchID}) {
		t.Fatalf("default newest first = %v; want %v", got, []uuid.UUID{zephyrID, auroraID, searchID})
	}
	for _, row := range defaultPage {
		if row.ID == aardvarkID {
			t.Fatalf("default newest-first page included the oldest operation %s", row.ID)
		}
	}
	explicitDescPage, explicitDescTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{
		Sort: "created", Direction: "desc",
	})
	if err != nil {
		t.Fatalf("list explicit created descending page: %v", err)
	}
	if explicitDescTotal != defaultTotal ||
		!reflect.DeepEqual(operationRowIDs(explicitDescPage), operationRowIDs(defaultPage)) {
		t.Fatalf("explicit created descending page = %d/%v; want default %d/%v",
			explicitDescTotal, operationRowIDs(explicitDescPage), defaultTotal, operationRowIDs(defaultPage))
	}

	createdAscPage, createdAscTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{
		Sort: "created", Direction: "asc",
	})
	if err != nil {
		t.Fatalf("list created ascending page: %v", err)
	}
	if createdAscTotal != sortedOperationsTotal || len(createdAscPage) != currentOperationsPageSize {
		t.Fatalf("created ascending total/page = %d/%d; want %d/%d",
			createdAscTotal, len(createdAscPage), sortedOperationsTotal, currentOperationsPageSize)
	}
	if createdAscPage[0].ID != aardvarkID {
		t.Fatalf("created ascending first row = %s; want oldest %s", createdAscPage[0].ID, aardvarkID)
	}
	for _, row := range createdAscPage {
		if row.ID == zephyrID || row.ID == auroraID || row.ID == searchID {
			t.Fatalf("created ascending first page included newer operation %s", row.ID)
		}
	}
	createdAscRest, createdAscRestTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{
		Sort: "created", Direction: "asc", Offset: currentOperationsPageSize,
	})
	if err != nil {
		t.Fatalf("list created ascending second page: %v", err)
	}
	if createdAscRestTotal != sortedOperationsTotal ||
		len(createdAscRest) != sortedOperationsTotal-currentOperationsPageSize {
		t.Fatalf("created ascending second page total/page = %d/%d; want %d/%d",
			createdAscRestTotal, len(createdAscRest), sortedOperationsTotal, sortedOperationsTotal-currentOperationsPageSize)
	}
	if got := operationRowIDs(createdAscRest[len(createdAscRest)-3:]); !reflect.DeepEqual(got, []uuid.UUID{searchID, auroraID, zephyrID}) {
		t.Fatalf("created ascending newest tail = %v; want %v", got, []uuid.UUID{searchID, auroraID, zephyrID})
	}

	// Search pages over the label-sorted match set, so the offset is applied
	// after sorting and the total still counts every match.
	archivePage, archiveTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{
		Query: "archive", Sort: "target", Direction: "asc",
	})
	if err != nil {
		t.Fatalf("search sorted archive page: %v", err)
	}
	if archiveTotal != 2 || !reflect.DeepEqual(operationRowIDs(archivePage), []uuid.UUID{aardvarkID, auroraID}) {
		t.Fatalf("search sorted archive page = %d/%v; want 2/%v",
			archiveTotal, operationRowIDs(archivePage), []uuid.UUID{aardvarkID, auroraID})
	}
	archiveSecondPage, archiveSecondTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{
		Query: "archive", Sort: "target", Direction: "asc", Offset: 1,
	})
	if err != nil {
		t.Fatalf("search sorted archive second page: %v", err)
	}
	if archiveSecondTotal != 2 || !reflect.DeepEqual(operationRowIDs(archiveSecondPage), []uuid.UUID{auroraID}) {
		t.Fatalf("search sorted archive offset page = %d/%v; want 2/%v",
			archiveSecondTotal, operationRowIDs(archiveSecondPage), []uuid.UUID{auroraID})
	}
	archiveBeyondPage, archiveBeyondTotal, err := repository.ListCurrentOperations(ctx, CurrentOperationsFilter{
		Query: "archive", Sort: "target", Direction: "asc", Offset: 2,
	})
	if err != nil {
		t.Fatalf("search sorted archive beyond page: %v", err)
	}
	if archiveBeyondTotal != 2 || len(archiveBeyondPage) != 0 {
		t.Fatalf("search sorted archive beyond total/page = %d/%d; want 2/0",
			archiveBeyondTotal, len(archiveBeyondPage))
	}
}

func operationRowIDs(rows []CurrentOperationRow) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}
