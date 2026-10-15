//go:build integration

package service_test

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
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func queueSourceScan(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) *persistence.Operation {
	t.Helper()
	snapshot, err := json.Marshal(service.ScanSourceSnapshot{SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: rootID})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "queued", Stage: service.SourceScanStageQueued,
		InputSnapshot: snapshot, TargetSourceRootID: &rootID,
	}
	client := openSourceScanRiver(t, database)
	if err := persistence.NewSourceInventoryRepository(database).CreateSourceScanOperationAndEnqueue(
		ctx, operation, client, service.ScanSourceJobArgs{OperationID: operation.ID}, nil,
	); err != nil {
		t.Fatalf("enqueue the scan operation: %v", err)
	}
	return operation
}

func readSourceScanLocations(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) map[string]sourceScanLocationFacts {
	t.Helper()
	locations := make([]persistence.SourceLocation, 0)
	if err := database.NewSelect().Model(&locations).Where("source_root_id = ?", rootID).Order("relative_path ASC").Scan(ctx); err != nil {
		t.Fatalf("read the source locations: %v", err)
	}
	facts := make(map[string]sourceScanLocationFacts, len(locations))
	for _, location := range locations {
		facts[location.RelativePath] = sourceScanLocationFacts{id: location.ID, sizeBytes: location.SizeBytes, mtime: location.Mtime}
	}
	return facts
}

type sourceScanLocationFacts struct {
	id        uuid.UUID
	sizeBytes int64
	mtime     time.Time
}

func readSourceScanCandidates(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) map[string]persistence.SourceScanCandidateInput {
	t.Helper()
	candidates := make([]persistence.SourceScanCandidateInput, 0)
	if err := database.NewRaw(`SELECT relative_path, size_bytes, mtime, probe_status, safe_error
		FROM source_scan_candidate WHERE operation_id = ? ORDER BY relative_path`, operationID).Scan(ctx, &candidates); err != nil {
		t.Fatalf("read the scan candidates: %v", err)
	}
	byPath := make(map[string]persistence.SourceScanCandidateInput, len(candidates))
	for _, candidate := range candidates {
		byPath[candidate.RelativePath] = candidate
	}
	return byPath
}

func readSourceScanWorkIDs(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) map[uuid.UUID]uuid.UUID {
	t.Helper()
	var rows []struct {
		ID                uuid.UUID `bun:"id"`
		CurrentLocationID uuid.UUID `bun:"current_location_id"`
	}
	if err := database.NewRaw(`SELECT id, current_location_id FROM source_analysis_work WHERE source_root_id = ? AND current_location_id IS NOT NULL`, rootID).Scan(ctx, &rows); err != nil {
		t.Fatalf("read source analysis work: %v", err)
	}
	workIDs := make(map[uuid.UUID]uuid.UUID, len(rows))
	for _, row := range rows {
		workIDs[row.CurrentLocationID] = row.ID
	}
	return workIDs
}

func TestSourceScanInventoryRoundTripWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	tree := t.TempDir()
	writeSourceScanFile(t, tree, "album/changed.flac", "first version")
	writeSourceScanFile(t, tree, "album/unchanged.mka", "same bytes")
	writeSourceScanFile(t, tree, "album/deleted.wav", "will be removed")
	rootPath, err := filepath.EvalSymlinks(tree)
	if err != nil {
		t.Fatal(err)
	}
	root := &persistence.SourceRoot{ID: uuid.New(), DisplayName: "music", ConfiguredPath: rootPath, Enabled: true}
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create source root: %v", err)
	}
	scan := service.NewSourceScan(inventory)
	first := queueSourceScan(t, ctx, database, root.ID)
	if err := inventory.StartSourceScanDelivery(ctx, first.ID, first.Attempt, *first.RiverJobID); err != nil {
		t.Fatalf("start first delivery: %v", err)
	}
	firstRequest := service.SourceScanRequest{OperationID: first.ID, RootID: root.ID, ExpectedConfiguredPath: rootPath, ExpectedAttempt: first.Attempt, ExpectedJobID: *first.RiverJobID}
	if scopes, err := scan.EnumerateObserved(ctx, firstRequest); err != nil || len(scopes) != 0 {
		t.Fatalf("first enumeration scopes = %v, %v", scopes, err)
	}
	firstCandidates := readSourceScanCandidates(t, ctx, database, first.ID)
	if len(firstCandidates) != 3 {
		t.Fatalf("first stat-only candidates = %v, want three", firstCandidates)
	}
	for path, candidate := range firstCandidates {
		if candidate.ProbeStatus != persistence.SourceProbeStatusNotAnalyzed || candidate.PreparedAnalysis != nil || len(candidate.SourceSHA256) != 0 {
			t.Errorf("candidate %q contains more than filesystem identity: %+v", path, candidate)
		}
	}
	if count, err := inventory.CountSourceLocations(ctx, root.ID); err != nil || count != 0 {
		t.Fatalf("locations before transactional apply = %d, %v; want empty inventory", count, err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, persistence.SourceEnumerationApply{
		OperationID: first.ID, ExpectedConfiguredPath: rootPath, ExpectedAttempt: first.Attempt, ExpectedJobID: *first.RiverJobID,
	}); err != nil {
		t.Fatalf("apply first enumeration: %v", err)
	}
	if err := inventory.FinishSourceScanDelivery(ctx, first.ID, first.Attempt, *first.RiverJobID, "succeeded", service.SourceScanStageApplying, ""); err != nil {
		t.Fatalf("finish first delivery: %v", err)
	}
	firstLocations := readSourceScanLocations(t, ctx, database, root.ID)
	firstWork := readSourceScanWorkIDs(t, ctx, database, root.ID)
	if len(firstLocations) != 3 || len(firstWork) != 3 {
		t.Fatalf("first applied locations/work = %v/%v, want three each", firstLocations, firstWork)
	}

	writeSourceScanFile(t, tree, "album/changed.flac", "changed payload with new size")
	if err := os.Remove(filepath.Join(tree, "album", "deleted.wav")); err != nil {
		t.Fatal(err)
	}
	writeSourceScanFile(t, tree, "album/new.flac", "new file")
	second := queueSourceScan(t, ctx, database, root.ID)
	if err := inventory.StartSourceScanDelivery(ctx, second.ID, second.Attempt, *second.RiverJobID); err != nil {
		t.Fatalf("start second delivery: %v", err)
	}
	secondRequest := service.SourceScanRequest{OperationID: second.ID, RootID: root.ID, ExpectedConfiguredPath: rootPath, ExpectedAttempt: second.Attempt, ExpectedJobID: *second.RiverJobID}
	if scopes, err := scan.EnumerateObserved(ctx, secondRequest); err != nil || len(scopes) != 0 {
		t.Fatalf("second enumeration scopes = %v, %v", scopes, err)
	}
	if current := readSourceScanLocations(t, ctx, database, root.ID); !sameSourceScanLocations(current, firstLocations) {
		t.Fatalf("enumeration changed the published inventory before apply: %v", current)
	}
	if err := inventory.ApplySourceEnumeration(ctx, persistence.SourceEnumerationApply{
		OperationID: second.ID, ExpectedConfiguredPath: rootPath, ExpectedAttempt: second.Attempt, ExpectedJobID: *second.RiverJobID,
	}); err != nil {
		t.Fatalf("apply second enumeration: %v", err)
	}
	if err := inventory.FinishSourceScanDelivery(ctx, second.ID, second.Attempt, *second.RiverJobID, "succeeded", service.SourceScanStageApplying, ""); err != nil {
		t.Fatalf("finish second delivery: %v", err)
	}
	secondLocations := readSourceScanLocations(t, ctx, database, root.ID)
	if len(secondLocations) != 3 || secondLocations["album/deleted.wav"].id != uuid.Nil {
		t.Fatalf("second inventory = %v, want changed/unchanged/new and no deleted file", secondLocations)
	}
	if secondLocations["album/unchanged.mka"].id != firstLocations["album/unchanged.mka"].id {
		t.Fatal("unchanged location identity was not preserved")
	}
	if secondLocations["album/changed.flac"].sizeBytes == firstLocations["album/changed.flac"].sizeBytes {
		t.Fatal("changed file stat was not published")
	}
	secondWork := readSourceScanWorkIDs(t, ctx, database, root.ID)
	if secondWork[secondLocations["album/unchanged.mka"].id] != firstWork[firstLocations["album/unchanged.mka"].id] {
		t.Fatal("unchanged source-analysis work identity was not preserved")
	}
	rootAfter, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil || rootAfter.ScanGeneration != 2 {
		t.Fatalf("scan generation = %v, %v; want 2", rootAfter, err)
	}
}

func sameSourceScanLocations(left, right map[string]sourceScanLocationFacts) bool {
	if len(left) != len(right) {
		return false
	}
	for path, expected := range right {
		actual, ok := left[path]
		if !ok || actual.id != expected.id || actual.sizeBytes != expected.sizeBytes || !actual.mtime.Equal(expected.mtime) {
			return false
		}
	}
	return true
}

func writeSourceScanFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
