//go:build integration

package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

func createSourceRoot(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, database *bun.DB, path, name string) *persistence.SourceRoot {
	t.Helper()
	root := &persistence.SourceRoot{
		ID: uuid.New(), ConfiguredPath: path, DisplayName: name, Enabled: true, Status: "unknown",
	}
	if _, err := database.NewInsert().Model(root).Exec(ctx); err != nil {
		t.Fatalf("create source root %s: %v", path, err)
	}
	return root
}

func createScanOperation(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, rootID uuid.UUID) *persistence.Operation {
	t.Helper()
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: "queued",
		InputSnapshot: []byte(`{"source_root_id":"` + rootID.String() + `"}`), TargetSourceRootID: &rootID,
	}
	if err := repository.CreateOperation(ctx, operation); err != nil {
		t.Fatalf("create scan operation: %v", err)
	}
	return operation
}
func scanSnapshot(rootID uuid.UUID, path string) []byte {
	return []byte(`{"source_root_id":"` + rootID.String() + `","configured_path":"` + path + `"}`)
}

func newSourceLocation(rootID uuid.UUID, relativePath string, sizeBytes, generation int64) persistence.SourceLocation {
	return persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: rootID, RelativePath: relativePath, SizeBytes: sizeBytes,
		Mtime: time.Now().UTC().Truncate(time.Microsecond), LastSeenScanGeneration: generation,
		ProbeStatus: "audio", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
}
func insertSourceLocation(t *testing.T, ctx context.Context, database *bun.DB, location persistence.SourceLocation) {
	t.Helper()
	if _, err := database.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatalf("insert source location %s: %v", location.RelativePath, err)
	}
}

func newSourceScanCandidate(operationID uuid.UUID, relativePath string, sizeBytes int64) persistence.SourceScanCandidate {
	return persistence.SourceScanCandidate{
		ID: uuid.New(), OperationID: operationID, RelativePath: relativePath, SizeBytes: sizeBytes,
		Mtime: time.Now().UTC().Truncate(time.Microsecond), ProbeStatus: "audio", CreatedAt: time.Now().UTC(),
	}
}

func insertSourceScanCandidate(t *testing.T, ctx context.Context, database *bun.DB, candidate persistence.SourceScanCandidate) {
	t.Helper()
	if _, err := database.NewInsert().Model(&candidate).Exec(ctx); err != nil {
		t.Fatalf("insert scan candidate %s: %v", candidate.RelativePath, err)
	}
}

func relationName(t *testing.T, database *bun.DB, name string) *string {
	t.Helper()
	var relation *string
	if err := database.NewRaw(`SELECT to_regclass(?)`, "public."+name).Scan(context.Background(), &relation); err != nil {
		t.Fatalf("look up relation %s: %v", name, err)
	}
	return relation
}

// assertRelationNotCreated proves a relation is absent before the migration that
// would create it. A never-created table has no row in information_schema.columns
// at all, so only to_regclass can tell absence, not a column count.
func assertRelationNotCreated(t *testing.T, database *bun.DB, name string) {
	t.Helper()
	if relation := relationName(t, database, name); relation != nil {
		t.Fatalf("relation %s already exists before the source inventory migration: %s", name, *relation)
	}
}

func assertRelationAbsent(t *testing.T, database *bun.DB, name string) {
	t.Helper()
	if relation := relationName(t, database, name); relation != nil {
		t.Fatalf("relation %s remains after rollback: %s", name, *relation)
	}
}

func assertColumnAbsent(t *testing.T, database *bun.DB, table, column string) {
	t.Helper()
	var tableCount int
	if err := database.NewRaw(
		`SELECT count(*) FROM information_schema.tables WHERE table_name = ?`, table,
	).Scan(context.Background(), &tableCount); err != nil {
		t.Fatalf("look up table %s: %v", table, err)
	}
	if tableCount == 0 {
		t.Fatalf("table %s is missing after rollback", table)
	}
	var count int
	if err := database.NewRaw(
		`SELECT count(*) FROM information_schema.columns WHERE table_name = ? AND column_name = ?`,
		table, column,
	).Scan(context.Background(), &count); err != nil {
		t.Fatalf("look up column %s.%s: %v", table, column, err)
	}
	if count != 0 {
		t.Fatalf("column %s.%s remains after rollback", table, column)
	}
}

func assertIndexAbsent(t *testing.T, database *bun.DB, name string) {
	t.Helper()
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM pg_indexes WHERE indexname = ?`, name).Scan(context.Background(), &count); err != nil {
		t.Fatalf("look up index %s: %v", name, err)
	}
	if count != 0 {
		t.Fatalf("index %s remains after rollback", name)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
