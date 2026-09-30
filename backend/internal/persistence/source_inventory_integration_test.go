//go:build integration

package persistence_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

const sourceInventoryMigrationName = "20261003000000"

func TestSourceInventorySchemaWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	repository := persistence.NewSetupManagerRepository(database)
	ctx := context.Background()

	root := createSourceRoot(t, ctx, repository, database, "/srv/incoming", "Incoming")

	t.Run("rejects_negative_location_size", func(t *testing.T) {
		location := newSourceLocation(root.ID, "album/track.flac", 1024, 1)
		location.SizeBytes = -1
		if _, err := database.NewInsert().Model(&location).Exec(ctx); err == nil {
			t.Fatal("negative location size was accepted")
		}
	})

	t.Run("rejects_negative_candidate_size", func(t *testing.T) {
		operation := createScanOperation(t, ctx, repository, root.ID)
		candidate := newSourceScanCandidate(operation.ID, "album/track.flac", 1024)
		candidate.SizeBytes = -1
		if _, err := database.NewInsert().Model(&candidate).Exec(ctx); err == nil {
			t.Fatal("negative candidate size was accepted")
		}
	})

	t.Run("rejects_duplicate_relative_path_for_same_root", func(t *testing.T) {
		location := newSourceLocation(root.ID, "album/duplicate.flac", 1024, 1)
		insertSourceLocation(t, ctx, database, location)
		duplicate := newSourceLocation(root.ID, "album/duplicate.flac", 2048, 1)
		if _, err := database.NewInsert().Model(&duplicate).Exec(ctx); err == nil {
			t.Fatal("duplicate relative path for one root was accepted")
		}
	})

	t.Run("keeps_case_only_path_differences_separate", func(t *testing.T) {
		lower := newSourceLocation(root.ID, "album/track.mp3", 100, 1)
		insertSourceLocation(t, ctx, database, lower)
		upper := newSourceLocation(root.ID, "album/TRACK.mp3", 200, 1)
		insertSourceLocation(t, ctx, database, upper)
		// Read both stored paths and compare them verbatim. Scope the read to the
		// two rows this case inserted: an earlier subtest already left its own
		// row in the same root, and a case-insensitive lookup would still match
		// two rows if the database collapsed the case into one rewritten value.
		var stored []string
		if err := database.NewSelect().Column("relative_path").Table("source_location").
			Where("source_root_id = ?", root.ID).Where("relative_path IN (?)", bun.In([]string{"album/track.mp3", "album/TRACK.mp3"})).
			Scan(ctx, &stored); err != nil {
			t.Fatalf("read case-differing locations: %v", err)
		}
		if len(stored) != 2 {
			t.Fatalf("case-differing location count = %d, want 2", len(stored))
		}
		// Compare as a set: ORDER BY relative_path is collation-dependent, so the
		// assertion must not depend on which spelling sorts first.
		seen := map[string]bool{}
		for _, path := range stored {
			seen[path] = true
		}
		for _, want := range []string{"album/track.mp3", "album/TRACK.mp3"} {
			if !seen[want] {
				t.Fatalf("stored paths = %q, want the exact string %q among them", stored, want)
			}
		}
	})

	t.Run("keeps_same_relative_path_across_roots_separate", func(t *testing.T) {
		other := createSourceRoot(t, ctx, repository, database, "/srv/other-incoming", "Other")
		first := newSourceLocation(root.ID, "shared/track.flac", 100, 1)
		insertSourceLocation(t, ctx, database, first)
		second := newSourceLocation(other.ID, "shared/track.flac", 200, 2)
		insertSourceLocation(t, ctx, database, second)
		// Two distinct Go values always have distinct IDs, so comparing them
		// proves nothing: read each persisted row back by root and compare the
		// values the database actually stored for that root.
		readBack := func(rootID uuid.UUID) persistence.SourceLocation {
			t.Helper()
			var stored persistence.SourceLocation
			if err := database.NewSelect().Model(&stored).
				Where("source_root_id = ?", rootID).Where("relative_path = ?", "shared/track.flac").
				Scan(ctx); err != nil {
				t.Fatalf("read the location of root %s: %v", rootID, err)
			}
			return stored
		}
		storedFirst := readBack(root.ID)
		storedSecond := readBack(other.ID)
		if storedFirst.ID != first.ID || storedFirst.SourceRootID != root.ID || storedFirst.SizeBytes != 100 {
			t.Fatalf("stored location of the first root = %+v, want id %s, root %s, size 100", storedFirst, first.ID, root.ID)
		}
		if storedSecond.ID != second.ID || storedSecond.SourceRootID != other.ID || storedSecond.SizeBytes != 200 {
			t.Fatalf("stored location of the second root = %+v, want id %s, root %s, size 200", storedSecond, second.ID, other.ID)
		}
		if storedFirst.ID == storedSecond.ID {
			t.Fatalf("both roots persisted the same location row %s", storedFirst.ID)
		}
	})

	t.Run("rejects_probe_error_without_safe_error", func(t *testing.T) {
		location := newSourceLocation(root.ID, "album/broken.flac", 100, 1)
		location.ProbeStatus = "probe_error"
		if _, err := database.NewInsert().Model(&location).Exec(ctx); err == nil {
			t.Fatal("probe_error without a safe error text was accepted")
		}
	})

	t.Run("rejects_unknown_probe_status", func(t *testing.T) {
		location := newSourceLocation(root.ID, "album/unknown.flac", 100, 1)
		location.ProbeStatus = "pending"
		if _, err := database.NewInsert().Model(&location).Exec(ctx); err == nil {
			t.Fatal("unknown probe status was accepted")
		}
	})

	t.Run("deletes_locations_together_with_root", func(t *testing.T) {
		doomed := createSourceRoot(t, ctx, repository, database, "/srv/doomed", "Doomed")
		insertSourceLocation(t, ctx, database, newSourceLocation(doomed.ID, "album/track.flac", 100, 1))
		insertSourceLocation(t, ctx, database, newSourceLocation(doomed.ID, "album/disc/track.flac", 200, 1))
		if _, err := database.NewDelete().Model((*persistence.SourceRoot)(nil)).Where("id = ?", doomed.ID).Exec(ctx); err != nil {
			t.Fatalf("delete source root: %v", err)
		}
		var remaining int
		if err := database.NewRaw(`SELECT count(*) FROM source_location WHERE source_root_id = ?`, doomed.ID).Scan(ctx, &remaining); err != nil {
			t.Fatalf("count locations of the deleted root: %v", err)
		}
		if remaining != 0 {
			t.Fatalf("locations remaining after root deletion = %d, want 0", remaining)
		}
	})
}

func TestSourceScanCandidatesFollowOperationWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	repository := persistence.NewSetupManagerRepository(database)
	ctx := context.Background()
	root := createSourceRoot(t, ctx, repository, database, "/srv/candidate-incoming", "Candidates")

	operation := createScanOperation(t, ctx, repository, root.ID)
	insertSourceScanCandidate(t, ctx, database, newSourceScanCandidate(operation.ID, "album/track.flac", 1024))
	duplicate := newSourceScanCandidate(operation.ID, "album/track.flac", 1024)
	if _, err := database.NewInsert().Model(&duplicate).Exec(ctx); err == nil {
		t.Fatal("duplicate candidate path for one operation was accepted")
	}
	otherRoot := createSourceRoot(t, ctx, repository, database, "/srv/candidate-other", "CandidatesOther")
	other := createScanOperation(t, ctx, repository, otherRoot.ID)
	insertSourceScanCandidate(t, ctx, database, newSourceScanCandidate(other.ID, "album/track.flac", 1024))

	if _, err := database.NewDelete().Model((*persistence.Operation)(nil)).Where("id = ?", operation.ID).Exec(ctx); err != nil {
		t.Fatalf("delete scan operation: %v", err)
	}
	var remaining int
	if err := database.NewRaw(`SELECT count(*) FROM source_scan_candidate WHERE operation_id = ?`, operation.ID).Scan(ctx, &remaining); err != nil {
		t.Fatalf("count candidates of the deleted operation: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("candidates remaining after operation deletion = %d, want 0", remaining)
	}
	var kept int
	if err := database.NewRaw(`SELECT count(*) FROM source_scan_candidate WHERE operation_id = ?`, other.ID).Scan(ctx, &kept); err != nil {
		t.Fatalf("count candidates of the surviving operation: %v", err)
	}
	if kept != 1 {
		t.Fatalf("candidates of the surviving operation = %d, want 1", kept)
	}
}

func TestActiveSourceScanUniquenessWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	repository := persistence.NewSetupManagerRepository(database)
	ctx := context.Background()
	root := createSourceRoot(t, ctx, repository, database, "/srv/active-scan-incoming", "ActiveScan")
	other := createSourceRoot(t, ctx, repository, database, "/srv/active-scan-other", "ActiveScanOther")

	first := createScanOperation(t, ctx, repository, root.ID)
	second := &persistence.Operation{
		ID: uuid.New(), Kind: "scan_source", State: "running", Stage: "traversing",
		InputSnapshot:      scanSnapshot(root.ID, "/srv/active-scan-incoming"),
		TargetSourceRootID: &root.ID, StartedAt: timePointer(time.Now().UTC()),
	}
	if err := repository.CreateOperation(ctx, second); err == nil {
		t.Fatal("second active scan of one root was accepted")
	}

	differentRoot := &persistence.Operation{
		ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: "queued",
		InputSnapshot:      scanSnapshot(other.ID, "/srv/active-scan-other"),
		TargetSourceRootID: &other.ID,
	}
	if err := repository.CreateOperation(ctx, differentRoot); err != nil {
		t.Fatalf("active scan of a different root: %v", err)
	}

	finishOperation(t, ctx, repository, first.ID)
	if err := repository.CreateOperation(ctx, second); err != nil {
		t.Fatalf("scan after the previous scan finished: %v", err)
	}

	installation := createReadyInstallation(t, ctx, repository, "ffmpeg", "linux", "amd64", "9.9")
	install := &persistence.Operation{
		ID: uuid.New(), Kind: "install", State: "queued", Stage: "download",
		InputSnapshot: []byte(`{"target_identity":"ffmpeg:9.9"}`), TargetInstallationID: &installation.ID,
	}
	if err := repository.CreateOperation(ctx, install); err != nil {
		t.Fatalf("install operation concurrent with a scan: %v", err)
	}

	// The move interval must stay unbounded, so a move collides with an active
	// scan in both directions. Finish every active row first: the move also
	// collides with an active scan (or install) of any root by design.
	finishOperation(t, ctx, repository, second.ID)
	finishOperation(t, ctx, repository, differentRoot.ID)
	finishOperation(t, ctx, repository, install.ID)

	scanBeforeMove := &persistence.Operation{
		ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: "queued",
		InputSnapshot:      scanSnapshot(other.ID, "/srv/active-scan-other"),
		TargetSourceRootID: &other.ID,
	}
	if err := repository.CreateOperation(ctx, scanBeforeMove); err != nil {
		t.Fatalf("active scan on an idle catalog: %v", err)
	}

	move := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "preflight",
		InputSnapshot: []byte(`{"old_root":"/tools","new_root":"/new-tools"}`),
	}
	if err := repository.CreateOperation(ctx, move); err == nil {
		t.Fatal("tools root move concurrent with an active scan was accepted")
	}

	finishOperation(t, ctx, repository, scanBeforeMove.ID)
	if err := repository.CreateOperation(ctx, move); err != nil {
		t.Fatalf("tools root move on an idle catalog: %v", err)
	}

	// With the move active, an active scan of any root must collide too, so the
	// move really covers the unbounded interval and not only an empty one.
	scanAgainstMove := &persistence.Operation{
		ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: "queued",
		InputSnapshot:      scanSnapshot(other.ID, "/srv/active-scan-other"),
		TargetSourceRootID: &other.ID,
	}
	if err := repository.CreateOperation(ctx, scanAgainstMove); err == nil {
		t.Fatal("active scan concurrent with a queued tools root move was accepted")
	}
}

func TestSourceRootDeletionSetsOperationTargetToNullWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	repository := persistence.NewSetupManagerRepository(database)
	ctx := context.Background()
	root := createSourceRoot(t, ctx, repository, database, "/srv/terminal-incoming", "Terminal")
	operation := createScanOperation(t, ctx, repository, root.ID)
	finishOperation(t, ctx, repository, operation.ID)

	if _, err := database.NewDelete().Model((*persistence.SourceRoot)(nil)).Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("delete source root with a terminal scan: %v", err)
	}
	preserved, err := repository.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read terminal scan operation: %v", err)
	}
	if preserved.TargetSourceRootID != nil {
		t.Fatalf("terminal operation target = %v, want NULL after root deletion", preserved.TargetSourceRootID)
	}
	if preserved.State != "succeeded" || preserved.FinishedAt == nil {
		t.Fatalf("terminal operation snapshot = %s/%v, want succeeded with a finish time", preserved.State, preserved.FinishedAt)
	}
}

func TestSourceInventoryRollbackWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	// The baseline is every migration that precedes the fixed source inventory
	// migration by name, never "the last element of the collection": a later
	// migration must not silently change what this rollback test exercises.
	withoutInventory := migrate.NewMigrations()
	sourceInventoryFound := false
	for _, migration := range collection.Sorted() {
		if migration.Name == sourceInventoryMigrationName {
			sourceInventoryFound = true
			continue
		}
		if migration.Name > sourceInventoryMigrationName {
			continue
		}
		withoutInventory.Add(migration)
	}
	if !sourceInventoryFound {
		t.Fatalf("migration %s is missing from the collection", sourceInventoryMigrationName)
	}

	database := testpostgres.Open(t)
	testpostgres.Reset(t, database)
	// Init the full migrator first so the upsert path has the unique name index;
	// RunMigration needs it, the baseline only needs the migration tables.
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true), migrate.WithUpsert(true))
	if err := migrator.Init(ctx); err != nil {
		t.Fatalf("initialize migrations: %v", err)
	}
	baseline := migrate.NewMigrator(database, withoutInventory, migrate.WithMarkAppliedOnSuccess(true))
	if _, err := baseline.Migrate(ctx); err != nil {
		t.Fatalf("apply the baseline migrations: %v", err)
	}
	assertRelationNotCreated(t, database, "source_root")
	assertColumnAbsent(t, database, "operation", "target_source_root_id")

	if err := migrator.RunMigration(ctx, sourceInventoryMigrationName); err != nil {
		t.Fatalf("apply the source inventory migration: %v", err)
	}
	if sourceRelation := relationName(t, database, "source_root"); sourceRelation == nil {
		t.Fatal("source_root is missing after the source inventory migration")
	}

	group, err := migrator.Rollback(ctx)
	if err != nil {
		t.Fatalf("roll back the source inventory migration: %v", err)
	}
	if group == nil || len(group.Migrations) != 1 {
		t.Fatalf("rolled back migration group = %#v, want exactly one migration", group)
	}
	if group.Migrations[0].Name != sourceInventoryMigrationName {
		t.Fatalf("rolled back migration = %s, want %s", group.Migrations[0].Name, sourceInventoryMigrationName)
	}
	assertRelationAbsent(t, database, "source_root")
	assertRelationAbsent(t, database, "source_location")
	assertRelationAbsent(t, database, "source_scan_candidate")
	assertColumnAbsent(t, database, "operation", "target_source_root_id")
	assertIndexAbsent(t, database, "operation_one_active_source_root_scan")
	var kindConstraint string
	if err := database.NewRaw(
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'operation_kind_check'`,
	).Scan(ctx, &kindConstraint); err != nil {
		t.Fatalf("read operation kind constraint: %v", err)
	}
	for _, kind := range []string{"install", "activate", "delete", "move_tools_root"} {
		if !strings.Contains(kindConstraint, kind) {
			t.Fatalf("operation kind constraint after rollback = %s, want kind %s", kindConstraint, kind)
		}
	}
	if strings.Contains(kindConstraint, "scan_source") {
		t.Fatalf("operation kind constraint after rollback = %s, want no scan_source kind", kindConstraint)
	}
}

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
