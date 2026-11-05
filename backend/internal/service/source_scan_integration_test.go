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
