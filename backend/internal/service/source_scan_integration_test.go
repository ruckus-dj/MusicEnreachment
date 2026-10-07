//go:build integration

package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// queueSourceScan inserts the queued scan operation a worker would pick up. Its
// River job carries only the operation ID, so the row alone is what the
// traversal under test is given.
func queueSourceScan(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) *persistence.Operation {
	t.Helper()
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "scan_source", State: "queued", Stage: service.SourceScanStageQueued,
		InputSnapshot:      []byte(`{"source_root_id":"` + rootID.String() + `"}`),
		TargetSourceRootID: &rootID,
	}
	if err := persistence.NewSetupManagerRepository(database).CreateOperation(ctx, operation); err != nil {
		t.Fatalf("create the scan operation: %v", err)
	}
	return operation
}

func setSourceScanProbePaths(probe *sourceScanProbeFixture, root string, names ...string) {
	for _, name := range names {
		probe.paths[name] = filepath.Join(root, "album", name)
	}
}

func readScanCandidates(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) map[string]persistence.SourceScanCandidateInput {
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

type sourceScanLocationFacts struct {
	id          uuid.UUID
	sizeBytes   int64
	mtime       time.Time
	probeStatus string
	safeError   *string
}

func readSourceScanLocations(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) map[string]sourceScanLocationFacts {
	t.Helper()
	locations := make([]persistence.SourceLocation, 0)
	if err := database.NewSelect().Model(&locations).Where("source_root_id = ?", rootID).Order("relative_path ASC").Scan(ctx); err != nil {
		t.Fatalf("read the source locations: %v", err)
	}
	facts := make(map[string]sourceScanLocationFacts, len(locations))
	for _, location := range locations {
		facts[location.RelativePath] = sourceScanLocationFacts{
			id: location.ID, sizeBytes: location.SizeBytes, mtime: location.Mtime,
			probeStatus: location.ProbeStatus, safeError: location.SafeError,
		}
	}
	return facts
}

func requireSourceScanGeneration(t *testing.T, ctx context.Context, inventory *persistence.SourceInventoryRepository, rootID uuid.UUID, generation int64) {
	t.Helper()
	root, err := inventory.GetSourceRoot(ctx, rootID)
	if err != nil {
		t.Fatalf("read the source root: %v", err)
	}
	if root.ScanGeneration != generation {
		t.Fatalf("scan generation = %d, want %d", root.ScanGeneration, generation)
	}
}

// TestSourceScanInventoryRoundTripWithPostgreSQL drives the traversal against
// real PostgreSQL: the candidates of the operation are the only rows it writes,
// the previous inventory survives until it is applied, a second scan re-probes
// only the file whose probe failed, and a scan that fails leaves both the
// inventory and the candidate table as they were.
func TestSourceScanInventoryRoundTripWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	operations := service.NewOperations(persistence.NewSetupManagerRepository(database))

	tree := t.TempDir()
	writeSourceWalkFile(t, filepath.Join(tree, "album", "track.flac"), "audio bytes")
	writeSourceWalkFile(t, filepath.Join(tree, "album", "silent.mka"), "video only bytes")
	writeSourceWalkFile(t, filepath.Join(tree, "album", "broken.wav"), "unreadable bytes")
	writeSourceWalkFile(t, filepath.Join(tree, "album", "cover.jpg"), "not audio")
	path, err := filepath.EvalSymlinks(tree)
	if err != nil {
		t.Fatalf("resolve source root path: %v", err)
	}
	root := &persistence.SourceRoot{ID: uuid.New(), DisplayName: "music", ConfiguredPath: path, Enabled: true}
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create the source root: %v", err)
	}
	// Given a first scan of the tree whose probe confirms one file, finds no
	// audio in another and fails on the third...
	probe := newSourceScanProbeFixture()
	setSourceScanProbePaths(probe, tree, "track.flac", "silent.mka", "broken.wav")
	probe.answer("silent.mka", false)
	probe.fail("broken.wav", errors.New("ffprobe failed"))
	sourceScan := service.NewSourceScan(inventory, probe, operations)
	first := queueSourceScan(t, ctx, database, root.ID)
	if err := sourceScan.Run(ctx, service.SourceScanRequest{OperationID: first.ID, RootID: root.ID}); err != nil {
		t.Fatalf("first scan: %v", err)
	}

	// Then the operation carries the stage the traversal reached, the candidates
	// of the operation hold one row per approved file, and the inventory the
	// operator sees is still empty.
	snapshot, err := operations.Snapshot(ctx, first.ID)
	if err != nil {
		t.Fatalf("read the operation snapshot: %v", err)
	}
	if snapshot.State != "running" || snapshot.Stage != service.SourceScanStageApplying {
		t.Fatalf("operation after the scan = %s/%s, want running/%s", snapshot.State, snapshot.Stage, service.SourceScanStageApplying)
	}
	firstCandidates := readScanCandidates(t, ctx, database, first.ID)
	if len(firstCandidates) != 3 {
		t.Fatalf("stored candidates = %v, want the three approved files", firstCandidates)
	}
	requireSourceScanStatus(t, firstCandidates, "album/track.flac", persistence.SourceProbeStatusAudio)
	requireSourceScanStatus(t, firstCandidates, "album/silent.mka", persistence.SourceProbeStatusNoAudio)
	if status := firstCandidates["album/broken.wav"].ProbeStatus; status != persistence.SourceProbeStatusProbeError {
		t.Fatalf("status of the unprobeable file = %q, want %q", status, persistence.SourceProbeStatusProbeError)
	}
	if count, err := inventory.CountSourceLocations(ctx, root.ID); err != nil || count != 0 {
		t.Fatalf("locations after the traversal = %d, %v, want 0: the inventory changes only when a generation is applied", count, err)
	}

	// When the traversal of the first scan is applied as a generation...
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{OperationID: first.ID, ExpectedConfiguredPath: root.ConfiguredPath}); err != nil {
		t.Fatalf("apply the first generation: %v", err)
	}
	if err := operations.Succeed(ctx, first.ID, service.SourceScanStageApplying); err != nil {
		t.Fatalf("finish the first operation: %v", err)
	}

	// Then the inventory holds the three files and nothing else.
	firstApplied := readSourceScanLocations(t, ctx, database, root.ID)
	if len(firstApplied) != 3 {
		t.Fatalf("applied locations = %v, want the three approved files", firstApplied)
	}
	requireSourceScanGeneration(t, ctx, inventory, root.ID, 1)

	// Given the file whose probe failed is readable now...
	probe = newSourceScanProbeFixture()
	setSourceScanProbePaths(probe, tree, "track.flac", "silent.mka", "broken.wav")
	probe.answer("track.flac", false)
	probe.answer("silent.mka", true)
	probe.answer("broken.wav", true)
	sourceScan = service.NewSourceScan(inventory, probe, operations)
	second := queueSourceScan(t, ctx, database, root.ID)

	// When the tree is scanned again...
	if err := sourceScan.Run(ctx, service.SourceScanRequest{OperationID: second.ID, RootID: root.ID}); err != nil {
		t.Fatalf("second scan: %v", err)
	}

	// Then only that file is probed again, the other candidates carry the stored
	// statuses, and the applied generation is still exactly what it was before
	// the second traversal.
	if got, want := probe.probes(), []string{"broken.wav"}; !slices.Equal(got, want) {
		t.Fatalf("probed %v, want only %v", got, want)
	}
	secondCandidates := readScanCandidates(t, ctx, database, second.ID)
	requireSourceScanStatus(t, secondCandidates, "album/track.flac", persistence.SourceProbeStatusAudio)
	requireSourceScanStatus(t, secondCandidates, "album/silent.mka", persistence.SourceProbeStatusNoAudio)
	requireSourceScanStatus(t, secondCandidates, "album/broken.wav", persistence.SourceProbeStatusAudio)
	if current := readSourceScanLocations(t, ctx, database, root.ID); !reflect.DeepEqual(current, firstApplied) {
		t.Fatalf("the applied inventory changed before the second generation was applied: %v", current)
	}

	// When the second traversal is applied...
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{OperationID: second.ID, ExpectedConfiguredPath: root.ConfiguredPath}); err != nil {
		t.Fatalf("apply the second generation: %v", err)
	}

	// Then the unchanged files kept their identity and the rechecked one healed.
	secondApplied := readSourceScanLocations(t, ctx, database, root.ID)
	for _, unchanged := range []string{"album/track.flac", "album/silent.mka"} {
		if secondApplied[unchanged].id != firstApplied[unchanged].id {
			t.Fatalf("location %q id = %s, want the preserved %s", unchanged, secondApplied[unchanged].id, firstApplied[unchanged].id)
		}
	}
	if status := secondApplied["album/broken.wav"].probeStatus; status != persistence.SourceProbeStatusAudio {
		t.Fatalf("status of the rechecked file = %q, want %q", status, persistence.SourceProbeStatusAudio)
	}
	requireSourceScanGeneration(t, ctx, inventory, root.ID, 2)
	if err := operations.Succeed(ctx, second.ID, service.SourceScanStageApplying); err != nil {
		t.Fatalf("finish the second operation: %v", err)
	}

	// Given a new file that must be probed, a barrier that rewrites it while it
	// is probed, and the candidates an earlier attempt of the operation left
	// behind...
	writeSourceWalkFile(t, filepath.Join(tree, "album", "fresh.wav"), "audio bytes")
	barrier := newSourceScanProbeFixture()
	setSourceScanProbePaths(barrier, tree, "fresh.wav")
	probed := filepath.Join(tree, "album", "fresh.wav")
	barrier.onProbe = func(name, _ string) {
		if name != "fresh.wav" {
			return
		}
		if err := os.WriteFile(probed, []byte("a payload written while the probe was running"), 0o644); err != nil {
			t.Errorf("rewrite the probed file: %v", err)
		}
	}
	third := queueSourceScan(t, ctx, database, root.ID)
	if err := inventory.ReplaceSourceScanCandidates(ctx, third.ID, []persistence.SourceScanCandidateInput{{
		RelativePath: "album/leftover.flac", SizeBytes: 1, Mtime: time.Now().UTC().Truncate(time.Microsecond),
		ProbeStatus: persistence.SourceProbeStatusAudio,
	}}); err != nil {
		t.Fatalf("store the candidates of the earlier attempt: %v", err)
	}

	// When the traversal meets the barrier...
	err = service.NewSourceScan(inventory, barrier, operations).Run(ctx, service.SourceScanRequest{OperationID: third.ID, RootID: root.ID})

	// Then the whole scan failed, its candidates are gone, and the applied
	// generation is untouched.
	if err == nil {
		t.Fatal("a scan whose probed file changed was accepted")
	}
	if stored := readScanCandidates(t, ctx, database, third.ID); len(stored) != 0 {
		t.Fatalf("the failed scan left the candidates %v", stored)
	}
	if current := readSourceScanLocations(t, ctx, database, root.ID); !reflect.DeepEqual(current, secondApplied) {
		t.Fatalf("the failed scan changed the inventory: %v", current)
	}
	requireSourceScanGeneration(t, ctx, inventory, root.ID, 2)
}
