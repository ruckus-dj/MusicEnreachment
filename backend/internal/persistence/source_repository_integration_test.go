//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

var deleteAttachedOperationID uuid.UUID

func TestSourceScanEnqueueLifecycleWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigratedWithRiver(t)
	for _, test := range []struct {
		name string
		run  func(*testing.T, *bun.DB)
	}{
		{"TestSourceScanEnqueueRollsBackTheOrphanJobWithPostgreSQL", runSourceScanEnqueueRollsBackTheOrphanJobWithPostgreSQL},
		{"TestSourceScanEnqueueRefusesDisabledAndActiveRootsWithPostgreSQL", runSourceScanEnqueueRefusesDisabledAndActiveRootsWithPostgreSQL},
	} {
		t.Run(test.name, func(t *testing.T) { test.run(t, database) })
	}
}

func runSourceRootRepositoryWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	root := &persistence.SourceRoot{ConfiguredPath: "/srv/root-crud", DisplayName: "Root CRUD", Enabled: true}
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create source root: %v", err)
	}
	if root.ID == uuid.Nil || root.ScanGeneration != 0 || root.InventoryPath != nil || root.Status != "unknown" {
		t.Fatalf("created root = %+v, want a generated id, generation 0, no inventory and unknown status", root)
	}
	stored, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil || stored.ConfiguredPath != "/srv/root-crud" || stored.DisplayName != "Root CRUD" || stored.ProcessingMode != "in_place" {
		t.Fatalf("stored root = %+v, %v", stored, err)
	}
	duplicate := &persistence.SourceRoot{ConfiguredPath: "/srv/root-crud", DisplayName: "Duplicate", Enabled: true}
	if err := inventory.CreateSourceRoot(ctx, duplicate); err == nil {
		t.Fatal("duplicate configured path was accepted")
	}

	stored.DisplayName = "Renamed"
	stored.Enabled = false
	if err := inventory.UpdateSourceRoot(ctx, stored); err != nil {
		t.Fatalf("update source root: %v", err)
	}
	renamed, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil || renamed.DisplayName != "Renamed" || renamed.Enabled {
		t.Fatalf("updated root = %+v, %v", renamed, err)
	}
	stale := *renamed
	stale.DisplayName = "Lost update"
	stale.ScanGeneration = renamed.ScanGeneration + 1
	if err := inventory.UpdateSourceRoot(ctx, &stale); err == nil {
		t.Fatal("an edit of a root another writer already advanced was accepted")
	}

	if err := deleteInventoryRoot(ctx, inventory, root.ID); err != nil {
		t.Fatalf("delete source root: %v", err)
	}
	if _, err := inventory.GetSourceRoot(ctx, root.ID); err == nil {
		t.Fatal("deleted source root is still readable")
	}
}

func runCreateSourceRootPersistsProcessingModeWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	root := &persistence.SourceRoot{
		ConfiguredPath: "/srv/root-staged-mode",
		DisplayName:    "Staged root",
		ProcessingMode: "staged",
		Enabled:        true,
	}
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create staged source root: %v", err)
	}
	if root.ProcessingMode != "staged" {
		t.Fatalf("created root processing mode = %q, want staged", root.ProcessingMode)
	}

	stored, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read staged source root: %v", err)
	}
	if stored.ProcessingMode != "staged" {
		t.Fatalf("stored root processing mode = %q, want staged", stored.ProcessingMode)
	}
}

// TestSourceRootPathEditWithPostgreSQL drives the path change through the
// repository method rather than raw SQL, and pins that the previous inventory is
// kept while being reported stale.
func runSourceRootPathEditWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/edit-old")
	operation := newSourceScanOperation(t, ctx, database, root, "running")
	unchanged := sourceCandidate("album/track.flac", 1024, probeMtime())
	applySourceScan(t, ctx, inventory, operation, root.ConfiguredPath, unchanged)
	unchangedID := locationID(t, ctx, database, root.ID, "album/track.flac")

	// The path edit is an operator action on an idle root: a scan of the old path
	// that is still running is refused by the guard in UpdateSourceRoot, so the
	// scan has to finish first.
	setOperationState(t, ctx, database, operation.ID, "succeeded")

	read, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read root before the edit: %v", err)
	}
	read.ConfiguredPath = "/srv/edit-new"
	if err := inventory.UpdateSourceRoot(ctx, read); err != nil {
		t.Fatalf("edit the configured path: %v", err)
	}

	edited, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read root after the edit: %v", err)
	}
	if edited.ConfiguredPath != "/srv/edit-new" {
		t.Fatalf("configured path after the edit = %q, want /srv/edit-new", edited.ConfiguredPath)
	}
	if !edited.Stale() {
		t.Fatalf("root after the path edit = %+v, want stale against %s", edited, edited.ConfiguredPath)
	}
	if edited.InventoryPath == nil || *edited.InventoryPath != "/srv/edit-old" {
		t.Fatalf("inventory path after the edit = %v, want the previous path /srv/edit-old", edited.InventoryPath)
	}
	if edited.ScanGeneration != read.ScanGeneration {
		t.Fatalf("scan generation after the edit = %d, want %d untouched", edited.ScanGeneration, read.ScanGeneration)
	}
	count, err := inventory.CountSourceLocations(ctx, root.ID)
	if err != nil || count != 1 {
		t.Fatalf("locations after the path edit = %d, %v; want the previous inventory to be kept and reported stale", count, err)
	}
	if locationID(t, ctx, database, root.ID, "album/track.flac") != unchangedID {
		t.Fatal("the path edit rewrote the identity of an existing location")
	}

	// An edit whose read predates a scan apply must be refused: the apply already
	// advanced the generation, so the edit would overwrite a newer state.
	setOperationState(t, ctx, database, operation.ID, "succeeded")
	advanced := newSourceScanOperation(t, ctx, database, root, "running")
	applySourceScan(t, ctx, inventory, advanced, "/srv/edit-new",
		sourceCandidate("album/track.flac", 1024, probeMtime()))
	setOperationState(t, ctx, database, advanced.ID, "succeeded")
	if err := inventory.UpdateSourceRoot(ctx, read); err == nil {
		t.Fatal("a path edit written against a generation a scan already advanced was accepted")
	}
	if current, err := inventory.GetSourceRoot(ctx, root.ID); err != nil || current.ConfiguredPath != "/srv/edit-new" {
		t.Fatalf("configured path after the refused edit = %+v, %v; want /srv/edit-new", current, err)
	}

	// A scan still carrying the previous path must not publish its files as the
	// inventory of the new path.
	staleScan := newSourceScanOperation(t, ctx, database, root, "running")
	defer setOperationState(t, ctx, database, staleScan.ID, "succeeded")
	if err := inventory.ReplaceSourceScanCandidates(ctx, staleScan.ID, []persistence.SourceScanCandidateInput{
		sourceCandidate("album/old.flac", 1024, probeMtime()),
	}); err != nil {
		t.Fatalf("persist candidates of the stale scan: %v", err)
	}
	afterEdit := snapshotInventory(t, ctx, database, root.ID)
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: staleScan.ID, ExpectedConfiguredPath: "/srv/edit-old",
		ExpectedAttempt: staleScan.Attempt, ExpectedJobID: *staleScan.RiverJobID,
	}); err == nil {
		t.Fatal("an apply carrying the previous configured path was accepted")
	}
	if current := snapshotInventory(t, ctx, database, root.ID); current != afterEdit {
		t.Fatalf("inventory after the rejected apply = %q, want %q", current, afterEdit)
	}
	assertCandidateCount(t, ctx, database, staleScan.ID, 1)

	// Only a successful scan of the new path replaces the stale inventory.
	setOperationState(t, ctx, database, staleScan.ID, "succeeded")
	fresh := newSourceScanOperation(t, ctx, database, root, "running")
	defer setOperationState(t, ctx, database, fresh.ID, "succeeded")
	if err := inventory.ReplaceSourceScanCandidates(ctx, fresh.ID, []persistence.SourceScanCandidateInput{
		sourceCandidate("album/new.flac", 2048, probeMtime()),
	}); err != nil {
		t.Fatalf("persist candidates of the new path scan: %v", err)
	}
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: fresh.ID, ExpectedConfiguredPath: "/srv/edit-new",
		ExpectedAttempt: fresh.Attempt, ExpectedJobID: *fresh.RiverJobID,
	}); err != nil {
		t.Fatalf("apply the scan of the new path: %v", err)
	}
	renewed, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read root after the new path scan: %v", err)
	}
	if renewed.Stale() || renewed.InventoryPath == nil || *renewed.InventoryPath != "/srv/edit-new" {
		t.Fatalf("root after the new path scan = %+v, want a current inventory of /srv/edit-new", renewed)
	}
	if _, err := readLocationResult(ctx, database, root.ID, "album/track.flac"); err == nil {
		t.Fatal("a location of the previous path survived the new path inventory")
	}
}

func runSourceScanApplyWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/apply")
	first := newSourceScanOperation(t, ctx, database, root, "running")

	probeErrorText := "ffprobe rejected the file"
	unchanged := sourceCandidate("album/track.flac", 1024, probeMtime())
	inserted := sourceCandidate("album/added.wav", 2048, probeMtime())
	broken := sourceCandidate("album/broken.ape", 512, probeMtime())
	broken.ProbeStatus = "probe_error"
	broken.SafeError = &probeErrorText
	applySourceScan(t, ctx, inventory, first, root.ConfiguredPath,
		unchanged, inserted, broken)
	assertAppliedGeneration(t, ctx, database, root, 1, 3, first.ID)
	unchangedID := locationID(t, ctx, database, root.ID, "album/track.flac")

	// The candidate set handed to the second apply replaces the whole
	// inventory: the unchanged path keeps its id and status, the changed path
	// takes the new size and status, and the path that is gone loses its row.
	modified := sourceCandidate("album/track.flac", 4096, probeMtime())
	renamed := sourceCandidate("album/added.mp3", 2048, probeMtime())
	renamed.ProbeStatus = "no_audio"
	setOperationState(t, ctx, database, first.ID, "succeeded")
	second := newSourceScanOperation(t, ctx, database, root, "queued")
	setOperationState(t, ctx, database, second.ID, "running")
	applySourceScan(t, ctx, inventory, second, root.ConfiguredPath, modified, renamed)
	assertAppliedGeneration(t, ctx, database, root, 2, 2, second.ID)
	unchangedAfter := readLocation(t, ctx, database, root.ID, "album/track.flac")
	if unchangedAfter.ID != unchangedID {
		t.Fatalf("unchanged location id = %s, want %s preserved across scans", unchangedAfter.ID, unchangedID)
	}
	if unchangedAfter.SizeBytes != 4096 || unchangedAfter.Mtime.Equal(unchanged.Mtime) {
		t.Fatalf("changed location = %+v, want the new size and mtime", unchangedAfter)
	}
	if unchangedAfter.ProbeStatus != "audio" {
		t.Fatalf("confirmed probe status = %s, want audio", unchangedAfter.ProbeStatus)
	}
	if _, err := readLocationResult(ctx, database, root.ID, "album/added.wav"); err == nil {
		t.Fatal("a path absent from the applied batch kept its location")
	}
	if _, err := readLocationResult(ctx, database, root.ID, "album/broken.ape"); err == nil {
		t.Fatal("the removed probe_error location kept its row")
	}
	assertCandidateCount(t, ctx, database, first.ID, 0)
	assertCandidateCount(t, ctx, database, second.ID, 0)

	// An apply whose operation is not this root's own scan must change nothing,
	// including for an operation whose target is another root.
	other := createInventoryRoot(t, ctx, inventory, "/srv/apply-other")
	setOperationState(t, ctx, database, second.ID, "succeeded")
	foreign := newSourceScanOperation(t, ctx, database, other, "running")
	defer setOperationState(t, ctx, database, foreign.ID, "succeeded")
	if err := inventory.ReplaceSourceScanCandidates(ctx, foreign.ID, []persistence.SourceScanCandidateInput{
		sourceCandidate("album/foreign.flac", 1, probeMtime()),
	}); err != nil {
		t.Fatalf("persist candidates of the foreign root scan: %v", err)
	}
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: foreign.ID, ExpectedConfiguredPath: root.ConfiguredPath,
		ExpectedAttempt: foreign.Attempt, ExpectedJobID: *foreign.RiverJobID,
	}); err == nil {
		t.Fatal("an apply for an operation of another root was accepted")
	}
	install := newSourceScanOperation(t, ctx, database, root, "queued")
	setOperationState(t, ctx, database, install.ID, "succeeded")
	if err := persistOperationKindAndTarget(t, ctx, database, install.ID, "install", nil, []byte(`{"target_identity":"ffmpeg:fixture"}`)); err != nil {
		t.Fatalf("turn the scan operation into a non-scan one: %v", err)
	}
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: install.ID, ExpectedConfiguredPath: root.ConfiguredPath,
		ExpectedAttempt: install.Attempt, ExpectedJobID: *install.RiverJobID,
	}); err == nil {
		t.Fatal("an apply for a non-scan operation was accepted")
	}
	assertAppliedGeneration(t, ctx, database, root, 2, 2, second.ID)
}

// TestSourceScanApplyEmptyStoredCandidatesWithPostgreSQL pins that a traversal
// which persisted nothing applies an empty generation.
func runSourceScanApplyEmptyStoredCandidatesWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/empty")
	seed := newSourceScanOperation(t, ctx, database, root, "running")
	applySourceScan(t, ctx, inventory, seed, root.ConfiguredPath,
		sourceCandidate("album/keep.flac", 1024, probeMtime()))
	setOperationState(t, ctx, database, seed.ID, "succeeded")

	empty := newSourceScanOperation(t, ctx, database, root, "running")
	defer setOperationState(t, ctx, database, empty.ID, "succeeded")
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: empty.ID, ExpectedConfiguredPath: root.ConfiguredPath,
		ExpectedAttempt: empty.Attempt, ExpectedJobID: *empty.RiverJobID,
	}); err != nil {
		t.Fatalf("apply an empty scan: %v", err)
	}
	assertAppliedGeneration(t, ctx, database, root, 2, 0, empty.ID)
	count, err := inventory.CountSourceLocations(ctx, root.ID)
	if err != nil || count != 0 {
		t.Fatalf("locations after the empty scan = %d, %v; want an empty successful inventory", count, err)
	}
}

// TestSourceScanApplyKeepsCandidatesOnRollbackWithPostgreSQL pins that a failed
// apply leaves the stored candidates durable and the previous inventory intact.
func runSourceScanApplyKeepsCandidatesOnRollbackWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/rollback")
	first := newSourceScanOperation(t, ctx, database, root, "queued")
	setOperationState(t, ctx, database, first.ID, "running")
	defer setOperationState(t, ctx, database, first.ID, "succeeded")
	applySourceScan(t, ctx, inventory, first, root.ConfiguredPath,
		sourceCandidate("album/keep.flac", 1024, probeMtime()),
		sourceCandidate("album/drop.flac", 1024, probeMtime()))
	baseline := snapshotInventory(t, ctx, database, root.ID)

	setOperationState(t, ctx, database, first.ID, "succeeded")
	second := newSourceScanOperation(t, ctx, database, root, "queued")
	setOperationState(t, ctx, database, second.ID, "running")
	defer setOperationState(t, ctx, database, second.ID, "succeeded")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{
		sourceCandidate("album/new.flac", 256, probeMtime()),
		sourceCandidate("album/other.flac", 512, probeMtime()),
	}); err != nil {
		t.Fatalf("persist candidates of the failing scan: %v", err)
	}
	// The operation loses its root target before the apply, which is a failure
	// only the apply can observe: the candidates are already durable and valid.
	setOperationState(t, ctx, database, second.ID, "succeeded")
	if err := persistOperationKindAndTarget(t, ctx, database, second.ID, "install", nil, []byte(`{"target_identity":"ffmpeg:rollback"}`)); err != nil {
		t.Fatalf("retarget the failing scan operation: %v", err)
	}
	err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: second.ID, ExpectedConfiguredPath: root.ConfiguredPath,
		ExpectedAttempt: second.Attempt, ExpectedJobID: *second.RiverJobID,
	})
	if err == nil {
		t.Fatal("an apply for an operation without a source root target was accepted")
	}
	if !strings.Contains(err.Error(), "apply source scan") {
		t.Fatalf("apply error = %v, want the apply context", err)
	}
	if current := snapshotInventory(t, ctx, database, root.ID); current != baseline {
		t.Fatalf("inventory after the failed apply = %q, want the previous snapshot %q", current, baseline)
	}
	assertAppliedGeneration(t, ctx, database, root, 1, 2, first.ID)
	assertCandidateCount(t, ctx, database, second.ID, 2)
}

func runSourceScanApplyPreservesNativeRelativePathIdentityAndVariantWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/native-relative-path")
	relativePath := filepath.Join("album", "track.flac")
	mtime := probeMtime()
	first := newSourceScanOperation(t, ctx, database, root, "queued")
	setOperationState(t, ctx, database, first.ID, "running")
	defer setOperationState(t, ctx, database, first.ID, "succeeded")
	applySourceScan(t, ctx, inventory, first, root.ConfiguredPath,
		sourceCandidate(relativePath, 1024, mtime))
	locationID := locationID(t, ctx, database, root.ID, relativePath)
	variantID := insertMediaVariantRow(t, ctx, database, 1024, first.ID)
	linkLocationVariant(t, ctx, database, locationID, variantID)
	setOperationState(t, ctx, database, first.ID, "succeeded")

	second := newSourceScanOperation(t, ctx, database, root, "queued")
	setOperationState(t, ctx, database, second.ID, "running")
	defer setOperationState(t, ctx, database, second.ID, "succeeded")
	applySourceScan(t, ctx, inventory, second, root.ConfiguredPath,
		sourceCandidate(relativePath, 1024, mtime))

	stored := readLocationByID(t, ctx, database, locationID)
	if stored.RelativePath != relativePath {
		t.Fatalf("stored relative path = %q, want its exact native string %q", stored.RelativePath, relativePath)
	}
	if stored.ID != locationID || stored.MediaVariantID == nil || *stored.MediaVariantID != variantID {
		t.Fatalf("location after unchanged native-path rescan = %+v, want id %s and variant %s preserved", stored, locationID, variantID)
	}
	if stored.ProbeStatus != persistence.SourceProbeStatusAudio {
		t.Fatalf("probe status after unchanged native-path rescan = %q, want audio", stored.ProbeStatus)
	}
}

func runSourceCandidatesWrapOperationWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/candidates")
	operation := newSourceScanOperation(t, ctx, database, root, "running")

	if err := inventory.AppendSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{
		sourceCandidate("album/one.flac", 1024, probeMtime()),
	}); err != nil {
		t.Fatalf("append first candidate batch: %v", err)
	}
	if err := inventory.AppendSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{
		sourceCandidate("album/two.flac", 2048, probeMtime()),
	}); err != nil {
		t.Fatalf("append second candidate batch: %v", err)
	}
	assertCandidateCount(t, ctx, database, operation.ID, 2)
	duplicate := sourceCandidate("album/one.flac", 1024, probeMtime())
	if err := inventory.AppendSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{duplicate}); err == nil {
		t.Fatal("a duplicate candidate path of one operation was accepted")
	}
	assertCandidateCount(t, ctx, database, operation.ID, 2)
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: operation.ID, ExpectedConfiguredPath: root.ConfiguredPath,
		ExpectedAttempt: operation.Attempt, ExpectedJobID: *operation.RiverJobID,
	}); err != nil {
		t.Fatalf("apply the appended batches: %v", err)
	}
	assertAppliedGeneration(t, ctx, database, root, 1, 2, operation.ID)
	assertCandidateCount(t, ctx, database, operation.ID, 0)

	// Replacing the batch is what a retry does with a new traversal.
	if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{
		sourceCandidate("album/three.flac", 4096, probeMtime()),
	}); err != nil {
		t.Fatalf("replace candidate batch: %v", err)
	}
	assertCandidateCount(t, ctx, database, operation.ID, 1)

	if err := inventory.DeleteSourceScanCandidates(ctx, operation.ID); err != nil {
		t.Fatalf("delete candidate batch: %v", err)
	}
	assertCandidateCount(t, ctx, database, operation.ID, 0)
	if err := persistOperationState(t, ctx, database, operation.ID, "succeeded"); err != nil {
		t.Fatalf("finish the operation of the candidate test: %v", err)
	}
	// Deleting the root removes only its own locations: the applied generation
	// is gone, and the candidate rows of its operation cannot outlive it either.
	if err := deleteInventoryRoot(ctx, inventory, root.ID); err != nil {
		t.Fatalf("delete root after candidate cleanup: %v", err)
	}
	if count, err := inventory.CountSourceLocations(ctx, root.ID); err != nil || count != 0 {
		t.Fatalf("locations after root deletion = %d, %v; want 0", count, err)
	}
	assertCandidateCount(t, ctx, database, operation.ID, 0)
}

func runSourceLocationPaginationWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/pagination")
	operation := newSourceScanOperation(t, ctx, database, root, "running")

	paths := []string{"album/a.flac", "album/B.flac", "album/b.flac", "album/c.mp3", "album/d.wav"}
	candidates := make([]persistence.SourceScanCandidateInput, 0, len(paths))
	for _, path := range paths {
		candidates = append(candidates, sourceCandidate(path, 1024, probeMtime()))
	}
	applySourceScan(t, ctx, inventory, operation, root.ConfiguredPath, candidates...)

	seen := make([]string, 0, len(paths))
	var cursor *persistence.SourceLocationCursor
	for page := 0; ; page++ {
		locations, next, err := inventory.ListSourceLocationsPage(ctx, root.ID, cursor, 2)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if page < 2 && len(locations) != 2 {
			t.Fatalf("page %d returned %d locations, want 2", page, len(locations))
		}
		for _, location := range locations {
			seen = append(seen, location.RelativePath)
		}
		if next == nil {
			if len(locations) != 1 {
				t.Fatalf("last page returned %d locations, want 1", len(locations))
			}
			break
		}
		cursor = next
		if page > len(paths) {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != len(paths) {
		t.Fatalf("paged locations = %q, want all %d of the applied paths", seen, len(paths))
	}
	for _, path := range paths {
		if !containsPath(seen, path) {
			t.Fatalf("paged locations = %q, want the exact path %q", seen, path)
		}
	}
	count, err := inventory.CountSourceLocations(ctx, root.ID)
	if err != nil || count != int64(len(paths)) {
		t.Fatalf("counted locations = %d, %v; want %d", count, err, len(paths))
	}
}

func runSourceRootDeletionGuardsWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/delete")
	operation := newSourceScanOperation(t, ctx, database, root, "running")
	applySourceScan(t, ctx, inventory, operation, root.ConfiguredPath,
		sourceCandidate("album/track.flac", 1024, probeMtime()))

	if err := deleteInventoryRoot(ctx, inventory, root.ID); err == nil {
		t.Fatal("a root with an active scan was deleted")
	}
	if count, err := inventory.CountSourceLocations(ctx, root.ID); err != nil || count != 1 {
		t.Fatalf("locations after the refused deletion = %d, %v; want 1", count, err)
	}
	// A terminal operation still preserves its snapshot, including the target
	// that the root deletion clears.
	if err := persistOperationState(t, ctx, database, operation.ID, "succeeded"); err != nil {
		t.Fatalf("finish the scan operation: %v", err)
	}
	if err := deleteInventoryRoot(ctx, inventory, root.ID); err != nil {
		t.Fatalf("delete the root after its scan finished: %v", err)
	}
	if count, err := inventory.CountSourceLocations(ctx, root.ID); err != nil || count != 0 {
		t.Fatalf("locations after root deletion = %d, %v; want 0", count, err)
	}
	var target *uuid.UUID
	if err := database.NewRaw("SELECT target_source_root_id FROM operation WHERE id = ?", operation.ID).Scan(ctx, &target); err != nil {
		t.Fatalf("read the terminated operation target: %v", err)
	}
	if target != nil {
		t.Fatalf("operation target after root deletion = %v, want NULL", target)
	}
}

func runSourceRootDeletionRacesScanWithPostgreSQL(t *testing.T, database *bun.DB) {
	deleteAttachedOperationID = uuid.Nil
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/race")
	deleteAttachedOperationID = newSourceScanOperation(t, ctx, database, root, "queued").ID

	// The scan side holds the operation table exclusively and confirms it before
	// the delete is allowed to start, so the delete's refusal is a property of the
	// lock, not of a scheduling race.
	locked := make(chan struct{})
	scanResult := make(chan error, 1)
	release := make(chan struct{})
	go func() {
		scanResult <- holdOperationTableLock(ctx, database, root.ID, locked, release)
	}()
	<-locked

	deleteStarted := make(chan struct{})
	deleteResult := make(chan error, 1)
	go func() {
		close(deleteStarted)
		deleteResult <- deleteInventoryRoot(ctx, inventory, root.ID)
	}()
	<-deleteStarted
	close(release)

	scan := <-scanResult
	remove := <-deleteResult
	if scan != nil {
		t.Fatalf("scan side = %v, want it to hold the operation table lock", scan)
	}
	if remove == nil {
		t.Fatal("the delete of a root with an active scan was accepted")
	}
	var remaining int
	if err := database.NewRaw("SELECT count(*) FROM source_root WHERE id = ?", root.ID).Scan(ctx, &remaining); err != nil {
		t.Fatalf("count the raced root: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("roots with the raced id = %d, want the root to survive the refused deletion", remaining)
	}
	var target *uuid.UUID
	if err := database.NewRaw("SELECT target_source_root_id FROM operation WHERE id = ?", deleteAttachedOperationID).Scan(ctx, &target); err != nil {
		t.Fatalf("read the attached operation target: %v", err)
	}
	if target == nil || *target != root.ID {
		t.Fatalf("operation target after the refused deletion = %v, want %s", target, root.ID)
	}

	runSourceRootDeletionWinsScanAdmissionWithPostgreSQL(t, ctx, database, inventory)
}

func runSourceRootDeletionWinsScanAdmissionWithPostgreSQL(t *testing.T, ctx context.Context, database *bun.DB, inventory *persistence.SourceInventoryRepository) {
	root := createInventoryRoot(t, ctx, inventory, "/srv/deletion-wins-scan-admission")
	operationID := uuid.New()
	client := openScanEnqueueRiver(t, database)
	operation := scanEnqueueOperation(root, operationID)
	operationsBefore := countScanEnqueueRows(t, ctx, database,
		"SELECT count(*) FROM operation WHERE target_source_root_id = ?", root.ID)
	jobsBefore := countScanEnqueueRows(t, ctx, database,
		"SELECT count(*) FROM river_job WHERE kind = ? AND args->>'operation_id' = ?", service.SourceScanJobKind, operationID.String())

	// Hold the same database root-row lock as deletion, then prove the real
	// persistence enqueue is waiting on that lock before deleting and committing.
	deleteTx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin deletion transaction: %v", err)
	}
	defer func() { _ = deleteTx.Rollback() }()
	if _, err := deleteTx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?, ?)", int32(0x4d545256), int32(4)); err != nil {
		t.Fatalf("lock source fingerprint mutations: %v", err)
	}
	var deleteBackendPID int
	if err := deleteTx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&deleteBackendPID); err != nil {
		t.Fatalf("read deletion transaction backend pid: %v", err)
	}
	var lockedRoot uuid.UUID
	if err := deleteTx.QueryRowContext(ctx, "SELECT id FROM source_root WHERE id = ? FOR UPDATE", root.ID).Scan(&lockedRoot); err != nil {
		t.Fatalf("lock source root for deletion: %v", err)
	}

	enqueueCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	enqueueResult := make(chan error, 1)
	enqueueStarted := make(chan struct{})
	go func() {
		close(enqueueStarted)
		enqueueResult <- inventory.CreateSourceScanOperationAndEnqueue(enqueueCtx, operation, client,
			service.ScanSourceJobArgs{OperationID: operation.ID}, nil)
	}()
	<-enqueueStarted
	if err := waitForSourceRootLockWaiter(enqueueCtx, database, deleteBackendPID); err != nil {
		t.Fatalf("wait for scan admission to block behind deletion: %v", err)
	}
	if _, err := deleteTx.ExecContext(ctx, "DELETE FROM source_root WHERE id = ?", root.ID); err != nil {
		t.Fatalf("delete locked source root: %v", err)
	}
	if err := deleteTx.Commit(); err != nil {
		t.Fatalf("commit source root deletion: %v", err)
	}
	if err := <-enqueueResult; err == nil {
		t.Fatal("scan admission succeeded after the blocking deletion committed")
	}

	if count := countScanEnqueueRows(t, ctx, database,
		"SELECT count(*) FROM source_root WHERE id = ?", root.ID); count != 0 {
		t.Fatalf("deleted source root %s count = %d, want 0", root.ID, count)
	}
	if count := countScanEnqueueRows(t, ctx, database,
		"SELECT count(*) FROM operation WHERE target_source_root_id = ?", root.ID); count != operationsBefore {
		t.Fatalf("scan operations for deleted root %s = %d, want baseline %d", root.ID, count, operationsBefore)
	}
	if count := countScanEnqueueRows(t, ctx, database,
		"SELECT count(*) FROM river_job WHERE kind = ? AND args->>'operation_id' = ?", service.SourceScanJobKind, operationID.String()); count != jobsBefore {
		t.Fatalf("River jobs for refused scan operation %s = %d, want baseline %d", operationID, count, jobsBefore)
	}
}

func waitForSourceRootLockWaiter(ctx context.Context, database *bun.DB, blockerPID int) error {
	for {
		var waiting bool
		err := database.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE ? = ANY(pg_blocking_pids(pid))
			  AND datname = current_database()
			  AND wait_event_type = 'Lock'
		)`, blockerPID).Scan(ctx, &waiting)
		if err != nil {
			return fmt.Errorf("inspect PostgreSQL lock wait: %w", err)
		}
		if waiting {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("timed out waiting for PostgreSQL lock wait: %w", err)
		}
		runtime.Gosched()
	}
}

func holdOperationTableLock(ctx context.Context, database *bun.DB, rootID uuid.UUID, locked chan struct{}, release chan struct{}) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		_ = tx.Rollback()
		return err
	}
	result, err := tx.NewRaw("SELECT id FROM source_root WHERE id = ? FOR UPDATE", rootID).Exec(ctx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if rows != 1 {
		_ = tx.Rollback()
		return fmt.Errorf("source root is not available for a scan")
	}
	close(locked)
	<-release
	return tx.Commit()
}

// applySourceScan stores the candidates of an operation and then applies them,
// which is the traversal contract: the apply reads the durable rows, so a test
// that skips the store step is testing an empty generation.
func applySourceScan(t *testing.T, ctx context.Context, inventory *persistence.SourceInventoryRepository, operation *persistence.Operation, path string, candidates ...persistence.SourceScanCandidateInput) {
	t.Helper()
	if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, candidates); err != nil {
		t.Fatalf("persist candidates of operation %s: %v", operation.ID, err)
	}
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: operation.ID, ExpectedConfiguredPath: path,
		ExpectedAttempt: operation.Attempt, ExpectedJobID: *operation.RiverJobID,
	}); err != nil {
		t.Fatalf("apply scan operation %s: %v", operation.ID, err)
	}
}

func createInventoryRoot(t *testing.T, ctx context.Context, inventory *persistence.SourceInventoryRepository, path string) *persistence.SourceRoot {
	t.Helper()
	root := &persistence.SourceRoot{ConfiguredPath: path, DisplayName: "Inventory", Enabled: true}
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create source root %s: %v", path, err)
	}
	return root
}

func deleteInventoryRoot(ctx context.Context, inventory *persistence.SourceInventoryRepository, id uuid.UUID) error {
	root, err := inventory.GetSourceRoot(ctx, id)
	if err != nil {
		return err
	}
	count, err := inventory.CountSourceLocations(ctx, id)
	if err != nil {
		return err
	}
	return inventory.DeleteSourceRoot(ctx, id, root.ConfiguredPath, count)
}

func newSourceScanOperation(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot, state string) *persistence.Operation {
	t.Helper()
	repository := persistence.NewSetupManagerRepository(database)
	snapshot, err := json.Marshal(service.ScanSourceSnapshot{
		SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: root.ID,
	})
	if err != nil {
		t.Fatalf("encode scan snapshot: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceScanOperationKind, State: "queued", Stage: "queued",
		InputSnapshot:      snapshot,
		TargetSourceRootID: &root.ID,
	}
	client := openScanEnqueueRiver(t, database)
	if err := repository.CreateOperationAndEnqueue(ctx, operation, client,
		service.ScanSourceJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("enqueue scan operation: %v", err)
	}
	if state == "running" {
		inventory := persistence.NewSourceInventoryRepository(database)
		if err := inventory.StartSourceScanDelivery(ctx, operation.ID, operation.Attempt, *operation.RiverJobID); err != nil {
			t.Fatalf("start scan delivery: %v", err)
		}
		operation.State = "running"
	} else if state != "queued" {
		t.Fatalf("unsupported scan fixture state %q", state)
	}
	return operation
}

func sourceCandidate(relativePath string, sizeBytes int64, mtime time.Time) persistence.SourceScanCandidateInput {
	return persistence.SourceScanCandidateInput{
		RelativePath: relativePath, SizeBytes: sizeBytes, Mtime: mtime, ProbeStatus: "audio",
	}
}

func probeMtime() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func assertAppliedGeneration(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot, generation int64, locations int, operationID uuid.UUID) {
	t.Helper()
	stored, err := persistence.NewSourceInventoryRepository(database).GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read root %s: %v", root.ID, err)
	}
	if stored.ScanGeneration != generation {
		t.Fatalf("scan generation = %d, want %d", stored.ScanGeneration, generation)
	}
	if stored.InventoryPath == nil || *stored.InventoryPath != root.ConfiguredPath {
		t.Fatalf("inventory path = %v, want %s", stored.InventoryPath, root.ConfiguredPath)
	}
	if stored.LastAppliedOperationID == nil || *stored.LastAppliedOperationID != operationID {
		t.Fatalf("last applied operation = %v, want %s", stored.LastAppliedOperationID, operationID)
	}
	if stored.LastSuccessfulScanAt == nil {
		t.Fatal("last successful scan time was not recorded")
	}
	if stored.Status != persistence.SourceRootStatusAvailable || stored.SafeError != nil {
		t.Fatalf("root status after apply = %s/%v, want available without a safe error", stored.Status, stored.SafeError)
	}
	count, err := persistence.NewSourceInventoryRepository(database).CountSourceLocations(ctx, root.ID)
	if err != nil || count != int64(locations) {
		t.Fatalf("locations of the applied generation = %d, %v; want %d", count, err, locations)
	}
}

func assertCandidateCount(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, want int) {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM source_scan_candidate WHERE operation_id = ?", operationID).Scan(ctx, &count); err != nil {
		t.Fatalf("count candidates of operation %s: %v", operationID, err)
	}
	if count != want {
		t.Fatalf("candidates of operation %s = %d, want %d", operationID, count, want)
	}
}

func locationID(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID, relativePath string) uuid.UUID {
	t.Helper()
	return readLocation(t, ctx, database, rootID, relativePath).ID
}

func readLocation(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID, relativePath string) persistence.SourceLocation {
	t.Helper()
	location, err := readLocationResult(ctx, database, rootID, relativePath)
	if err != nil {
		t.Fatalf("read location %q of root %s: %v", relativePath, rootID, err)
	}
	return location
}

func readLocationResult(ctx context.Context, database *bun.DB, rootID uuid.UUID, relativePath string) (persistence.SourceLocation, error) {
	var location persistence.SourceLocation
	err := database.NewSelect().Model(&location).
		Where("source_root_id = ?", rootID).Where("relative_path = ?", relativePath).Scan(ctx)
	return location, err
}

func snapshotInventory(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) string {
	t.Helper()
	seed, err := persistence.NewSourceInventoryRepository(database).GetSourceRoot(ctx, rootID)
	if err != nil {
		t.Fatalf("read root %s: %v", rootID, err)
	}
	return fmt.Sprintf("generation=%d inventory=%v applied=%v status=%s error=%v locations=%v",
		seed.ScanGeneration, pathValue(seed.InventoryPath), seed.LastAppliedOperationID, seed.Status, seed.SafeError, inventoryLocations(t, ctx, database, rootID))
}

func pathValue(path *string) string {
	if path == nil {
		return "<none>"
	}
	return *path
}

func inventoryLocations(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID) []string {
	t.Helper()
	var locations []string
	if err := database.NewRaw(
		"SELECT relative_path || '|' || id::text || '|' || size_bytes || '|' || probe_status || '|' || last_seen_scan_generation FROM source_location WHERE source_root_id = ? ORDER BY relative_path, id",
		rootID,
	).Scan(ctx, &locations); err != nil {
		t.Fatalf("read locations of root %s: %v", rootID, err)
	}
	return locations
}

func persistOperationKindAndTarget(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, kind string, target *uuid.UUID, snapshot []byte) error {
	t.Helper()
	update := database.NewUpdate().Model((*persistence.Operation)(nil)).
		Set("kind = ?", kind).Set("target_source_root_id = ?", target).Set("updated_at = now()").
		Where("id = ?", operationID)
	if snapshot != nil {
		update = update.Set("input_snapshot = ?::jsonb", string(snapshot))
	}
	_, err := update.Exec(ctx)
	return err
}

func setOperationState(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, state string) {
	t.Helper()
	query := database.NewUpdate().Model((*persistence.Operation)(nil)).
		Set("state = ?", state).Set("updated_at = now()").Where("id = ?", operationID)
	if state == "succeeded" || state == "failed" {
		query = query.Set("finished_at = now()")
	} else {
		query = query.Set("finished_at = NULL")
	}
	if _, err := query.Exec(ctx); err != nil {
		t.Fatalf("set operation %s to %s: %v", operationID, state, err)
	}
}

func persistOperationState(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, state string) error {
	t.Helper()
	_, err := database.NewUpdate().Model((*persistence.Operation)(nil)).
		Set("state = ?", state).Set("finished_at = now()").Set("updated_at = now()").
		Where("id = ?", operationID).Exec(ctx)
	return err
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

// TestSourceScanEnqueueRollsBackTheOrphanJobWithPostgreSQL fails the operation
// insert of an enqueue whose River job was already inserted: the transaction
// must take the job with it, so no River job is left for an operation that does
// not exist, and the root must stay usable for the next start.
func runSourceScanEnqueueRollsBackTheOrphanJobWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enqueue")

	jobsBefore := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind)
	operationsBefore := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'scan_source'")
	offset := time.Now().UTC().Truncate(time.Microsecond)
	occupied := uuid.New()
	if _, err := database.NewRaw(`INSERT INTO operation (id, kind, state, stage, input_snapshot, target_source_root_id, finished_at, updated_at)
		VALUES (?, 'scan_source', 'succeeded', 'applied', '{}', ?, ?, ?)`, occupied, root.ID, offset, offset).Exec(ctx); err != nil {
		t.Fatalf("seed the finished scan operation: %v", err)
	}
	collision := scanEnqueueOperation(root, occupied)
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, collision, client, service.ScanSourceJobArgs{OperationID: occupied}, nil); err == nil {
		t.Fatal("an enqueue that reuses the id of an existing operation was accepted")
	}

	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE id = ? AND kind = 'scan_source'", occupied); count != 1 {
		t.Fatalf("pre-existing operation %s count = %d, want exactly one", occupied, count)
	}
	if jobs := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind); jobs != jobsBefore {
		t.Fatalf("River jobs after rolled-back enqueue = %d, want baseline %d", jobs, jobsBefore)
	}
	if operations := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'scan_source'"); operations != operationsBefore+1 {
		t.Fatalf("scan operations after rolled-back enqueue = %d, want baseline %d plus the pre-existing operation", operations, operationsBefore)
	}

	fresh := scanEnqueueOperation(root, uuid.New())
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, fresh, client, service.ScanSourceJobArgs{OperationID: fresh.ID}, nil); err != nil {
		t.Fatalf("enqueue after the rolled-back transaction: %v", err)
	}
	if fresh.RiverJobID == nil {
		t.Fatalf("enqueued operation %+v has no River job", fresh)
	}
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE id = ? AND kind = ?", *fresh.RiverJobID, service.SourceScanJobKind); count != 1 {
		t.Fatalf("fresh River job %d count = %d, want exactly one", *fresh.RiverJobID, count)
	}
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE id = ? AND kind = 'scan_source'", fresh.ID); count != 1 {
		t.Fatalf("fresh scan operation %s count = %d, want exactly one", fresh.ID, count)
	}
	recovered := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind)
	t.Logf("after the recovered enqueue: operation %s river job %d, scan_source river jobs=%d", fresh.ID, *fresh.RiverJobID, recovered)
	if recovered != jobsBefore+1 {
		t.Fatalf("River jobs after the recovered enqueue = %d, want baseline %d plus one", recovered, jobsBefore)
	}
}

// TestSourceScanEnqueueRefusesDisabledAndActiveRootsWithPostgreSQL pins the two
// refusals the enqueue decides under the root lock: neither may store an
// operation and neither may insert a River job.
func runSourceScanEnqueueRefusesDisabledAndActiveRootsWithPostgreSQL(t *testing.T, database *bun.DB) {
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/refusals")

	active := newSourceScanOperation(t, ctx, database, root, "queued")
	// The active scan fixture stores one operation and its River job; the refused
	// enqueues must not add another of either, so capture the real baseline first.
	operationsBefore := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'scan_source'")
	jobsBefore := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind)
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE id = ? AND kind = 'scan_source'", active.ID); count != 1 {
		t.Fatalf("active operation %s count = %d, want exactly one", active.ID, count)
	}
	activeJobID := *active.RiverJobID
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE id = ? AND kind = ?", activeJobID, service.SourceScanJobKind); count != 1 {
		t.Fatalf("active River job %d count = %d, want exactly one", activeJobID, count)
	}
	activeRefusal := scanEnqueueOperation(root, uuid.New())
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, activeRefusal, client,
		service.ScanSourceJobArgs{OperationID: active.ID}, nil); !errors.Is(err, persistence.ErrSourceRootActiveScan) {
		t.Fatalf("enqueue on a root with a queued scan = %v, want ErrSourceRootActiveScan", err)
	}
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE id = ?", activeRefusal.ID); count != 0 {
		t.Fatalf("refused active-scan operation %s count = %d, want 0", activeRefusal.ID, count)
	}

	if err := persistOperationState(t, ctx, database, active.ID, "succeeded"); err != nil {
		t.Fatalf("finish the active scan: %v", err)
	}
	if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
		Set("enabled = false").Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("disable the source root: %v", err)
	}
	disabledRefusal := scanEnqueueOperation(root, uuid.New())
	if err := inventory.CreateSourceScanOperationAndEnqueue(ctx, disabledRefusal, client,
		service.ScanSourceJobArgs{OperationID: disabledRefusal.ID}, nil); !errors.Is(err, persistence.ErrSourceRootDisabled) {
		t.Fatalf("enqueue on a disabled root = %v, want ErrSourceRootDisabled", err)
	}
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE id = ?", disabledRefusal.ID); count != 0 {
		t.Fatalf("refused disabled-root operation %s count = %d, want 0", disabledRefusal.ID, count)
	}
	if count := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE id = ? AND kind = ?", activeJobID, service.SourceScanJobKind); count != 1 {
		t.Fatalf("active River job %d count after refusals = %d, want exactly one", activeJobID, count)
	}

	operations := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM operation WHERE kind = 'scan_source'")
	jobs := countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceScanJobKind)
	t.Logf("after the refused enqueues: scan operations=%d scan_source river jobs=%d", operations, jobs)
	if operations != operationsBefore {
		t.Fatalf("scan operations after the refusals = %d, want the initial %d preserved", operations, operationsBefore)
	}
	if jobs != jobsBefore {
		t.Fatalf("River jobs after the refusals = %d, want the initial %d preserved", jobs, jobsBefore)
	}
}

func scanEnqueueOperation(root *persistence.SourceRoot, id uuid.UUID) *persistence.Operation {
	return &persistence.Operation{
		ID: id, Kind: "scan_source", State: "queued", Stage: "queued",
		InputSnapshot:      []byte(`{"source_root_id":"` + root.ID.String() + `","configured_path":"` + root.ConfiguredPath + `"}`),
		TargetSourceRootID: &root.ID,
	}
}

func countScanEnqueueRows(t *testing.T, ctx context.Context, database *bun.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := database.NewRaw(query, args...).Scan(ctx, &count); err != nil {
		t.Fatalf("count rows (%s): %v", query, err)
	}
	return count
}

// openScanEnqueueRiver returns an insert-only client using River's real schema
// from the isolated application-and-River template.
func openScanEnqueueRiver(t *testing.T, database *bun.DB) *river.Client[*sql.Tx] {
	t.Helper()
	driver := riverdatabasesql.New(database.DB)
	client, err := river.NewClient(driver, &river.Config{})
	if err != nil {
		t.Fatalf("create insert-only River client: %v", err)
	}
	return client
}
