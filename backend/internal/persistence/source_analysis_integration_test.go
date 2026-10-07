//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// TestSourceAnalysisApplyWithPostgreSQL proves the apply contract of one
// successful analysis: the variant is written, the location points at it, the
// operation becomes succeeded with both read holds cleared, a duplicate delivery
// is a no-op, and every disagreement with the immutable snapshot is refused with
// nothing written.
func TestSourceAnalysisApplyWithPostgreSQL(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-apply")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "apply")
	operation := insertRunningAnalysisOperation(t, ctx, database, root, location, installationID, nil)

	committed, err := inventory.ApplyAnalysisResult(ctx, analysisApplyFor(operation, location, "7.1"))
	if err != nil {
		t.Fatalf("apply analysis result: %v", err)
	}
	if committed == nil || committed.ID != operation.ID || committed.State != "succeeded" || committed.FinishedAt == nil {
		t.Fatalf("committed operation = %+v, want the succeeded operation %s with a finish time", committed, operation.ID)
	}
	if committed.AnalysisMediaVariantID != nil || committed.AnalysisInstallationID != nil {
		t.Fatalf("terminal operation kept a read hold: %+v", committed)
	}
	linked := readLocationByID(t, ctx, database, location.ID)
	if linked.MediaVariantID == nil {
		t.Fatal("a successful analysis left the location without a variant")
	}
	variant, err := inventory.GetMediaVariant(ctx, *linked.MediaVariantID)
	if err != nil {
		t.Fatalf("read the applied variant: %v", err)
	}
	if variant.SizeBytes != location.SizeBytes || variant.AnalysisPolicyVersion != persistence.SourceAnalysisPolicyVersion ||
		variant.FFProbeVersion != "7.1" || variant.AppliedOperationID != operation.ID {
		t.Fatalf("applied variant = %+v, want the snapshot identity and the applying operation", variant)
	}
	if countMediaVariants(t, ctx, database) != 1 {
		t.Fatalf("variants after one apply = %d, want 1", countMediaVariants(t, ctx, database))
	}

	// A duplicate delivery of an already applied operation must not probe or
	// write again: it returns the committed operation and overwrites nothing.
	duplicate := analysisApplyFor(operation, location, "9.9")
	duplicate.SizeBytes = 4096
	replayed, err := inventory.ApplyAnalysisResult(ctx, duplicate)
	if err != nil {
		t.Fatalf("replay an applied analysis: %v", err)
	}
	if replayed == nil || replayed.ID != operation.ID || replayed.State != "succeeded" {
		t.Fatalf("replayed operation = %+v, want the already succeeded %s", replayed, operation.ID)
	}
	if countMediaVariants(t, ctx, database) != 1 {
		t.Fatalf("duplicate apply wrote another variant: %d", countMediaVariants(t, ctx, database))
	}
	stored, err := inventory.GetMediaVariant(ctx, *linked.MediaVariantID)
	if err != nil {
		t.Fatalf("read the variant after the replay: %v", err)
	}
	if stored.FFProbeVersion != "7.1" {
		t.Fatalf("duplicate apply overwrote the result with version %s", stored.FFProbeVersion)
	}

	// A malformed raw result is an internal failure, never an empty success.
	if _, err := inventory.ApplyAnalysisResult(ctx, persistence.SourceAnalysisApply{
		OperationID: operation.ID, RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion, FFProbeVersion: "7.1", InspectedAt: time.Now().UTC(),
	}); err == nil {
		t.Fatal("an apply without the raw ffprobe result was accepted")
	}
}

// TestSourceAnalysisSnapshotFencesWithPostgreSQL proves the snapshot fencing of
// the apply: the result identity the caller observed, the current root and
// location rows, and the held previous variant must all still match the
// immutable input snapshot, and a mismatch writes nothing.
func TestSourceAnalysisSnapshotFencesWithPostgreSQL(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	seed := func(t *testing.T, path string) (*persistence.SourceRoot, persistence.SourceLocation, uuid.UUID, *persistence.Operation) {
		t.Helper()
		testpostgres.ResetAndMigrate(t, database)
		root := createInventoryRoot(t, ctx, inventory, path)
		location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 1024, probeMtime())
		establishInventory(t, ctx, database, root)
		// The installation identity is a managed release identity, which the
		// schema forbids from containing a path separator, so it is derived from
		// the root path without its slashes rather than using the path itself.
		identity := strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", "-")
		installationID := insertAnalysisInstallation(t, ctx, database, identity)
		operation := insertRunningAnalysisOperation(t, ctx, database, root, location, installationID, nil)
		return root, location, installationID, operation
	}
	expectStale := func(t *testing.T, apply persistence.SourceAnalysisApply, operationID uuid.UUID, variantsBefore int) {
		t.Helper()
		if _, err := inventory.ApplyAnalysisResult(ctx, apply); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
			t.Fatalf("apply = %v, want ErrSourceAnalysisStale", err)
		}
		if countMediaVariants(t, ctx, database) != variantsBefore {
			t.Fatalf("variants after the refused apply = %d, want %d", countMediaVariants(t, ctx, database), variantsBefore)
		}
		stored, err := persistence.NewSetupManagerRepository(database).GetOperation(ctx, operationID)
		if err != nil {
			t.Fatalf("read the refused operation: %v", err)
		}
		if stored.State != "running" {
			t.Fatalf("refused apply changed the operation to %s, want running", stored.State)
		}
	}

	t.Run("result_identity_disagrees_with_snapshot", func(t *testing.T) {
		_, location, _, operation := seed(t, "/srv/fence-result")
		apply := analysisApplyFor(operation, location, "7.1")
		apply.SizeBytes = location.SizeBytes + 1
		expectStale(t, apply, operation.ID, 0)
	})

	t.Run("configured_path_changed_since_snapshot", func(t *testing.T) {
		root, location, _, operation := seed(t, "/srv/fence-path")
		if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
			Set("configured_path = ?", root.ConfiguredPath+"/moved").Set("updated_at = now()").
			Where("id = ?", root.ID).Exec(ctx); err != nil {
			t.Fatalf("move the configured path behind the snapshot: %v", err)
		}
		expectStale(t, analysisApplyFor(operation, location, "7.1"), operation.ID, 0)
	})

	t.Run("location_no_longer_audio", func(t *testing.T) {
		_, location, _, operation := seed(t, "/srv/fence-audio")
		if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
			Set("probe_status = ?", persistence.SourceProbeStatusNoAudio).Set("updated_at = now()").
			Where("id = ?", location.ID).Exec(ctx); err != nil {
			t.Fatalf("downgrade the location to no_audio: %v", err)
		}
		expectStale(t, analysisApplyFor(operation, location, "7.1"), operation.ID, 0)
	})

	t.Run("previous_variant_hold_disagrees_with_snapshot", func(t *testing.T) {
		_, location, _, operation := seed(t, "/srv/fence-hold")
		previous := insertMediaVariantRow(t, ctx, database, 1024, uuid.New())
		if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
			Set("media_variant_id = ?", previous).Set("updated_at = now()").
			Where("id = ?", location.ID).Exec(ctx); err != nil {
			t.Fatalf("link a previous variant: %v", err)
		}
		// The snapshot was taken with no previous variant, so the operation hold
		// no longer agrees with it.
		if _, err := database.NewUpdate().Model((*persistence.Operation)(nil)).
			Set("analysis_media_variant_id = ?", previous).Set("updated_at = now()").
			Where("id = ?", operation.ID).Exec(ctx); err != nil {
			t.Fatalf("tamper with the operation hold: %v", err)
		}
		expectStale(t, analysisApplyFor(operation, location, "7.1"), operation.ID, 1)
	})

	t.Run("disabled_root_is_stale", func(t *testing.T) {
		root, location, _, operation := seed(t, "/srv/fence-disabled")
		if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
			Set("enabled = false").Set("updated_at = now()").Where("id = ?", root.ID).Exec(ctx); err != nil {
			t.Fatalf("disable the root: %v", err)
		}
		expectStale(t, analysisApplyFor(operation, location, "7.1"), operation.ID, 0)
	})
}

// TestSourceAnalysisVariantHoldWithPostgreSQL proves the previous-variant hold:
// a held variant survives an unrelated orphan cleanup while its analysis is
// active, and a successful replacement clears the hold and removes it.
func TestSourceAnalysisVariantHoldWithPostgreSQL(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	t.Run("held_variant_survives_cleanup", func(t *testing.T) {
		// The subtests share one database, so each resets it before seeding to
		// keep the global variant count scoped to the subtest under test.
		testpostgres.ResetAndMigrate(t, database)
		root := createInventoryRoot(t, ctx, inventory, "/srv/hold-retain")
		location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 1024, probeMtime())
		establishInventory(t, ctx, database, root)
		installationID := insertAnalysisInstallation(t, ctx, database, "hold-retain")
		previous := insertMediaVariantRow(t, ctx, database, 1024, uuid.New())
		if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
			Set("media_variant_id = ?", previous).Set("updated_at = now()").
			Where("id = ?", location.ID).Exec(ctx); err != nil {
			t.Fatalf("link the previous variant: %v", err)
		}
		insertRunningAnalysisOperation(t, ctx, database, root, location, installationID, &previous)

		// A reconciliation of another root triggers the same orphan cleanup. The
		// held variant has no location link, yet the operation hold keeps it.
		if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
			Set("media_variant_id = NULL").Set("updated_at = now()").
			Where("id = ?", location.ID).Exec(ctx); err != nil {
			t.Fatalf("unlink the previous variant: %v", err)
		}
		otherRoot := createInventoryRoot(t, ctx, inventory, "/srv/hold-retain-other")
		triggerScanCleanup(t, ctx, database, inventory, otherRoot)
		if !mediaVariantExists(t, ctx, database, previous) {
			t.Fatal("orphan cleanup deleted a variant an active analysis holds")
		}
	})

	t.Run("terminal_apply_releases_and_cleans", func(t *testing.T) {
		testpostgres.ResetAndMigrate(t, database)
		root := createInventoryRoot(t, ctx, inventory, "/srv/hold-release")
		location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 1024, probeMtime())
		establishInventory(t, ctx, database, root)
		installationID := insertAnalysisInstallation(t, ctx, database, "hold-release")
		previous := insertMediaVariantRow(t, ctx, database, 1024, uuid.New())
		if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
			Set("media_variant_id = ?", previous).Set("updated_at = now()").
			Where("id = ?", location.ID).Exec(ctx); err != nil {
			t.Fatalf("link the previous variant: %v", err)
		}
		operation := insertRunningAnalysisOperation(t, ctx, database, root, location, installationID, &previous)

		committed, err := inventory.ApplyAnalysisResult(ctx, analysisApplyFor(operation, location, "7.2"))
		if err != nil {
			t.Fatalf("apply the replacement analysis: %v", err)
		}
		if committed.State != "succeeded" || committed.AnalysisMediaVariantID != nil {
			t.Fatalf("replacement operation = %+v, want succeeded without a hold", committed)
		}
		newLink := readLocationByID(t, ctx, database, location.ID).MediaVariantID
		if newLink == nil || *newLink == previous {
			t.Fatalf("location after the replacement = %v, want a new variant, not %s", newLink, previous)
		}
		if mediaVariantExists(t, ctx, database, previous) {
			t.Fatal("the released previous variant was not removed as an orphan")
		}
		if countMediaVariants(t, ctx, database) != 1 {
			t.Fatalf("variants after the replacement = %d, want the new one alone", countMediaVariants(t, ctx, database))
		}
	})
}

// TestSourceAnalysisLateFailureRollsBackWithPostgreSQL proves the apply is one
// transaction: when the terminal succeeded update fails after the new variant is
// already inserted and the location already relinked, the whole transaction
// rolls back. The inserted variant disappears, the previous variant and its link
// and result survive, the operation stays running, and both read holds remain.
func TestSourceAnalysisLateFailureRollsBackWithPostgreSQL(t *testing.T) {
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-late-failure")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	installationID := insertAnalysisInstallation(t, ctx, database, "late-failure")
	previous := insertMediaVariantRow(t, ctx, database, 1024, uuid.New())
	linkLocationVariant(t, ctx, database, location.ID, previous)
	operation := insertRunningAnalysisOperation(t, ctx, database, root, location, installationID, &previous)

	// The trigger raises the terminal succeeded update after the variant insert
	// and the location relink have already run in the same transaction, so the
	// transaction must undo both writes and leave the previous result, the link
	// and both holds exactly as they were.
	installFailingSucceededTrigger(t, ctx, database)
	if _, err := inventory.ApplyAnalysisResult(ctx, analysisApplyFor(operation, location, "7.3")); err == nil || !strings.Contains(err.Error(), "injected terminal failure") {
		t.Fatalf("apply failure = %v, want the injected terminal-update failure", err)
	}

	if countMediaVariants(t, ctx, database) != 1 {
		t.Fatalf("variants after the rolled back apply = %d, want only the previous one", countMediaVariants(t, ctx, database))
	}
	if !mediaVariantExists(t, ctx, database, previous) {
		t.Fatal("the rolled back apply deleted the previous variant")
	}
	linked := readLocationByID(t, ctx, database, location.ID)
	if linked.MediaVariantID == nil || *linked.MediaVariantID != previous {
		t.Fatalf("location link after the rolled back apply = %v, want the previous %s", linked.MediaVariantID, previous)
	}
	stored, err := persistence.NewSetupManagerRepository(database).GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the operation after the rolled back apply: %v", err)
	}
	if stored.State != "running" {
		t.Fatalf("operation after the rolled back apply = %s, want running", stored.State)
	}
	if stored.AnalysisMediaVariantID == nil || *stored.AnalysisMediaVariantID != previous {
		t.Fatalf("previous-variant hold after the rolled back apply = %v, want %s", stored.AnalysisMediaVariantID, previous)
	}
	if stored.AnalysisInstallationID == nil || *stored.AnalysisInstallationID != installationID {
		t.Fatalf("installation hold after the rolled back apply = %v, want %s", stored.AnalysisInstallationID, installationID)
	}
}

// TestSourceScanReconcilesVariantLinksWithPostgreSQL proves scan reconciliation:
// an unchanged audio file keeps its link, a file whose size or mtime moved or
// that is no longer audio loses it, an unseen file takes its orphan with it, a
// configured-path change invalidates every link of the previous path, and a
// failed apply leaves both inventory and links untouched.
func TestSourceScanReconcilesVariantLinksWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	seedLinked := func(t *testing.T, path string, size int64, mtime time.Time) (*persistence.SourceRoot, persistence.SourceLocation, uuid.UUID) {
		t.Helper()
		root := createInventoryRoot(t, ctx, inventory, path)
		// The seeded location belongs to an already applied generation, so the
		// next apply advances the root past it and a missing file is pruned.
		if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
			Set("scan_generation = 1").Where("id = ?", root.ID).Exec(ctx); err != nil {
			t.Fatalf("seed the root generation: %v", err)
		}
		root.ScanGeneration = 1
		location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", size, mtime)
		variantID := insertMediaVariantRow(t, ctx, database, size, uuid.New())
		linkLocationVariant(t, ctx, database, location.ID, variantID)
		return root, location, variantID
	}
	applyScan := func(t *testing.T, root *persistence.SourceRoot, candidates ...persistence.SourceScanCandidateInput) {
		t.Helper()
		operation := newSourceScanOperation(t, ctx, database, root, "queued")
		setOperationState(t, ctx, database, operation.ID, "succeeded")
		if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, candidates); err != nil {
			t.Fatalf("store scan candidates: %v", err)
		}
		if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
			OperationID: operation.ID, ExpectedConfiguredPath: root.ConfiguredPath,
		}); err != nil {
			t.Fatalf("apply scan: %v", err)
		}
	}

	t.Run("unchanged_keeps_link", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-unchanged", 1024, mtime)
		applyScan(t, root, sourceCandidate("album/track.flac", 1024, mtime))
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link == nil || *link != variantID {
			t.Fatalf("unchanged file link = %v, want %s", link, variantID)
		}
		if !mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("unchanged scan deleted the variant")
		}
	})

	t.Run("changed_size_detaches", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-size", 1024, mtime)
		applyScan(t, root, sourceCandidate("album/track.flac", 2048, mtime))
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link != nil {
			t.Fatalf("changed size kept variant link %s", *link)
		}
		if mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("changed size left an orphan variant")
		}
	})

	t.Run("changed_mtime_detaches", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-mtime", 1024, mtime)
		applyScan(t, root, sourceCandidate("album/track.flac", 1024, mtime.Add(time.Second)))
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link != nil {
			t.Fatalf("changed mtime kept variant link %s", *link)
		}
		if mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("changed mtime left an orphan variant")
		}
	})

	t.Run("no_audio_detaches", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-no-audio", 1024, mtime)
		candidate := sourceCandidate("album/track.flac", 1024, mtime)
		candidate.ProbeStatus = persistence.SourceProbeStatusNoAudio
		applyScan(t, root, candidate)
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link != nil {
			t.Fatalf("a no_audio file kept variant link %s", *link)
		}
		if mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("a no_audio file left an orphan variant")
		}
	})

	t.Run("pruned_location_deletes_variant", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-prune", 1024, mtime)
		applyScan(t, root, sourceCandidate("album/other.flac", 512, probeMtime()))
		if _, err := inventory.GetSourceLocation(ctx, root.ID, location.ID); !errors.Is(err, persistence.ErrSourceLocationNotFound) {
			t.Fatalf("pruned location read = %v, want ErrSourceLocationNotFound", err)
		}
		if mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("pruning a location left its variant behind")
		}
	})

	t.Run("configured_path_change_invalidates_links", func(t *testing.T) {
		root := createInventoryRoot(t, ctx, inventory, "/srv/reconcile-path-a")
		seedMtime := probeMtime()
		first := newSourceScanOperation(t, ctx, database, root, "queued")
		setOperationState(t, ctx, database, first.ID, "succeeded")
		if err := inventory.ReplaceSourceScanCandidates(ctx, first.ID, []persistence.SourceScanCandidateInput{
			sourceCandidate("album/track.flac", 1024, seedMtime),
		}); err != nil {
			t.Fatalf("store the first scan: %v", err)
		}
		if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
			OperationID: first.ID, ExpectedConfiguredPath: root.ConfiguredPath,
		}); err != nil {
			t.Fatalf("apply the first scan: %v", err)
		}
		location, err := inventory.GetSourceLocation(ctx, root.ID, locationID(t, ctx, database, root.ID, "album/track.flac"))
		if err != nil {
			t.Fatalf("read the seeded location: %v", err)
		}
		variantID := insertMediaVariantRow(t, ctx, database, 1024, uuid.New())
		linkLocationVariant(t, ctx, database, location.ID, variantID)

		stored, err := inventory.GetSourceRoot(ctx, root.ID)
		if err != nil {
			t.Fatalf("read the root before the path change: %v", err)
		}
		stored.ConfiguredPath = "/srv/reconcile-path-b"
		if err := inventory.UpdateSourceRoot(ctx, stored); err != nil {
			t.Fatalf("change the configured path: %v", err)
		}
		changed, err := inventory.GetSourceRoot(ctx, root.ID)
		if err != nil || !changed.Stale() {
			t.Fatalf("root after the path change = %+v, %v; want stale", changed, err)
		}
		newRootScan := newSourceScanOperation(t, ctx, database, changed, "queued")
		setOperationState(t, ctx, database, newRootScan.ID, "succeeded")
		if err := inventory.ReplaceSourceScanCandidates(ctx, newRootScan.ID, []persistence.SourceScanCandidateInput{
			sourceCandidate("album/track.flac", 1024, seedMtime),
		}); err != nil {
			t.Fatalf("store the new path scan: %v", err)
		}
		if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
			OperationID: newRootScan.ID, ExpectedConfiguredPath: changed.ConfiguredPath,
		}); err != nil {
			t.Fatalf("apply the new path scan: %v", err)
		}
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link != nil {
			t.Fatalf("a new inventory path kept the link of the previous path: %s", *link)
		}
		if mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("a new inventory path left the previous variant behind")
		}
	})

	t.Run("failed_apply_keeps_link", func(t *testing.T) {
		mtime := probeMtime()
		root, location, variantID := seedLinked(t, "/srv/reconcile-failed", 1024, mtime)
		operation := newSourceScanOperation(t, ctx, database, root, "queued")
		setOperationState(t, ctx, database, operation.ID, "succeeded")
		if err := inventory.ReplaceSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{
			sourceCandidate("album/track.flac", 4096, probeMtime()),
		}); err != nil {
			t.Fatalf("store the failing scan candidates: %v", err)
		}
		if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
			OperationID: operation.ID, ExpectedConfiguredPath: root.ConfiguredPath + "/moved",
		}); err == nil {
			t.Fatal("a scan apply with a moved configured path was accepted")
		}
		if link := readLocationByID(t, ctx, database, location.ID).MediaVariantID; link == nil || *link != variantID {
			t.Fatalf("failed scan link = %v, want the preserved %s", link, variantID)
		}
		if !mediaVariantExists(t, ctx, database, variantID) {
			t.Fatal("a failed scan deleted the linked variant")
		}
		if candidateCount := countScanCandidates(t, ctx, database, operation.ID); candidateCount != 1 {
			t.Fatalf("candidates after the failed scan = %d, want the durable 1", candidateCount)
		}
	})
}

// TestSourceAnalysisDeletionCleanupWithPostgreSQL proves the deletion boundary:
// deleting a root removes its locations and their now orphaned variants, an
// active analysis blocks the deletion and keeps its variant, and typed reads are
// scoped to the owning root.
func TestSourceAnalysisDeletionCleanupWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)

	root := createInventoryRoot(t, ctx, inventory, "/srv/analysis-delete")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 1024, probeMtime())
	variantID := insertMediaVariantRow(t, ctx, database, 1024, uuid.New())
	linkLocationVariant(t, ctx, database, location.ID, variantID)
	if err := deleteInventoryRoot(ctx, inventory, root.ID); err != nil {
		t.Fatalf("delete a root with a linked variant: %v", err)
	}
	if mediaVariantExists(t, ctx, database, variantID) {
		t.Fatal("root deletion left the linked variant behind")
	}
	if _, err := inventory.GetSourceLocation(ctx, root.ID, location.ID); !errors.Is(err, persistence.ErrSourceLocationNotFound) {
		t.Fatalf("location read after root deletion = %v, want ErrSourceLocationNotFound", err)
	}

	activeRoot := createInventoryRoot(t, ctx, inventory, "/srv/analysis-active-delete")
	activeLocation := insertAnalysisLocation(t, ctx, database, activeRoot.ID, "album/track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, activeRoot)
	activeVariant := insertMediaVariantRow(t, ctx, database, 1024, uuid.New())
	linkLocationVariant(t, ctx, database, activeLocation.ID, activeVariant)
	installationID := insertAnalysisInstallation(t, ctx, database, "active-delete")
	insertRunningAnalysisOperation(t, ctx, database, activeRoot, activeLocation, installationID, &activeVariant)

	if err := deleteInventoryRoot(ctx, inventory, activeRoot.ID); !errors.Is(err, persistence.ErrSourceRootActiveScan) {
		t.Fatalf("root deletion during an active analysis = %v, want ErrSourceRootActiveScan", err)
	}
	if !mediaVariantExists(t, ctx, database, activeVariant) {
		t.Fatal("a refused root deletion lost the active analysis variant")
	}
	if _, err := inventory.GetSourceRoot(ctx, activeRoot.ID); err != nil {
		t.Fatalf("a refused root deletion removed the root: %v", err)
	}

	otherRoot := createInventoryRoot(t, ctx, inventory, "/srv/analysis-other-root")
	if _, err := inventory.GetSourceLocation(ctx, otherRoot.ID, activeLocation.ID); !errors.Is(err, persistence.ErrSourceLocationNotFound) {
		t.Fatalf("reading a location through another root = %v, want ErrSourceLocationNotFound", err)
	}
	if _, err := inventory.GetSourceLocation(ctx, activeRoot.ID, activeLocation.ID); err != nil {
		t.Fatalf("reading an owned location: %v", err)
	}
	if _, err := inventory.GetMediaVariant(ctx, uuid.New()); !errors.Is(err, persistence.ErrMediaVariantNotFound) {
		t.Fatalf("reading a missing variant = %v, want ErrMediaVariantNotFound", err)
	}
}

// establishInventory makes a freshly created root the non-stale root of an
// applied generation, which is what an analysis snapshot requires.
func establishInventory(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot) {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
		Set("scan_generation = 1").Set("inventory_path = ?", root.ConfiguredPath).
		Set("last_successful_scan_at = now()").Set("status = ?", persistence.SourceRootStatusAvailable).
		Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("establish the root inventory: %v", err)
	}
	root.ScanGeneration = 1
	inventoryPath := root.ConfiguredPath
	root.InventoryPath = &inventoryPath
}

// triggerScanCleanup applies an empty scan generation to a root, which runs the
// same orphan-variant cleanup a real reconciliation runs.
func triggerScanCleanup(t *testing.T, ctx context.Context, database *bun.DB, inventory *persistence.SourceInventoryRepository, root *persistence.SourceRoot) {
	t.Helper()
	operation := newSourceScanOperation(t, ctx, database, root, "queued")
	setOperationState(t, ctx, database, operation.ID, "succeeded")
	if err := inventory.ApplySourceScan(ctx, persistence.SourceScanApply{
		OperationID: operation.ID, ExpectedConfiguredPath: root.ConfiguredPath,
	}); err != nil {
		t.Fatalf("apply the cleanup scan: %v", err)
	}
}

// installFailingSucceededTrigger installs a BEFORE UPDATE trigger on operation
// that raises for the running -> succeeded transition of an analysis, then
// schedules its removal through test cleanup. It exists only to fail the
// apply's terminal update after the variant insert and location relink have
// already run in the same transaction.
func installFailingSucceededTrigger(t *testing.T, ctx context.Context, database *bun.DB) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `
		CREATE FUNCTION fail_analysis_succeeded() RETURNS trigger AS $$
		BEGIN
			IF OLD.state = 'running' AND NEW.state = 'succeeded' THEN
				RAISE EXCEPTION 'injected terminal failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create the failing succeeded trigger function: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		CREATE TRIGGER fail_analysis_succeeded
		BEFORE UPDATE ON operation
		FOR EACH ROW EXECUTE FUNCTION fail_analysis_succeeded()`); err != nil {
		t.Fatalf("install the failing succeeded trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := database.ExecContext(context.Background(),
			`DROP TRIGGER IF EXISTS fail_analysis_succeeded ON operation`); err != nil {
			t.Errorf("drop the failing succeeded trigger: %v", err)
		}
		if _, err := database.ExecContext(context.Background(),
			`DROP FUNCTION IF EXISTS fail_analysis_succeeded()`); err != nil {
			t.Errorf("drop the failing succeeded trigger function: %v", err)
		}
	})
}

func insertAnalysisLocation(t *testing.T, ctx context.Context, database *bun.DB, rootID uuid.UUID, relativePath string, sizeBytes int64, mtime time.Time) persistence.SourceLocation {
	t.Helper()
	location := persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: rootID, RelativePath: relativePath, SizeBytes: sizeBytes,
		Mtime: mtime.Truncate(time.Microsecond), LastSeenScanGeneration: 1, ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatalf("insert analysis location %s: %v", relativePath, err)
	}
	return location
}

func insertAnalysisInstallation(t *testing.T, ctx context.Context, database *bun.DB, identity string) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: "ffmpeg", PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		SourceName: "analysis-test", ReleaseIdentity: identity, RelativePath: "ffmpeg/" + identity,
		State: "ready", ExecutableVersions: json.RawMessage(`{}`),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &now,
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("insert analysis installation %s: %v", identity, err)
	}
	return installation.ID
}

func insertRunningAnalysisOperation(t *testing.T, ctx context.Context, database *bun.DB, root *persistence.SourceRoot, location persistence.SourceLocation, installationID uuid.UUID, previous *uuid.UUID) *persistence.Operation {
	t.Helper()
	if root.InventoryPath == nil {
		t.Fatalf("analysis fixture root %s has no inventory path", root.ID)
	}
	snapshot, err := json.Marshal(persistence.SourceAnalysisSnapshot{
		SchemaVersion:          persistence.SourceAnalysisSnapshotVersion,
		SourceRootID:           root.ID,
		SourceLocationID:       location.ID,
		ConfiguredPath:         root.ConfiguredPath,
		InventoryPath:          *root.InventoryPath,
		RelativePath:           location.RelativePath,
		SizeBytes:              location.SizeBytes,
		Mtime:                  location.Mtime,
		PreviousVariantID:      previous,
		AnalysisPolicyVersion:  persistence.SourceAnalysisPolicyVersion,
		AnalysisInstallationID: installationID,
	})
	if err != nil {
		t.Fatalf("marshal the analysis snapshot: %v", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "analyze_source", State: "running", Stage: "probing", Attempt: 1,
		InputSnapshot:          snapshot,
		TargetSourceRootID:     &root.ID,
		TargetSourceLocationID: &location.ID,
		AnalysisInstallationID: &installationID,
		AnalysisMediaVariantID: previous,
	}
	if _, err := database.NewInsert().Model(operation).Exec(ctx); err != nil {
		t.Fatalf("insert running analysis operation: %v", err)
	}
	return operation
}

func analysisApplyFor(operation *persistence.Operation, location persistence.SourceLocation, version string) persistence.SourceAnalysisApply {
	return persistence.SourceAnalysisApply{
		OperationID: operation.ID, RelativePath: location.RelativePath,
		SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion, FFProbeVersion: version,
		FFProbeJSON:  json.RawMessage(`{"format":{"format_name":"flac"}}`),
		ObservedTags: json.RawMessage(`{"ARTIST":["Fixture"]}`),
		InspectedAt:  time.Now().UTC(),
	}
}

func insertMediaVariantRow(t *testing.T, ctx context.Context, database *bun.DB, sizeBytes int64, appliedOperationID uuid.UUID) uuid.UUID {
	t.Helper()
	variant := &persistence.MediaVariant{
		ID: uuid.New(), SizeBytes: sizeBytes, AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion, FFProbeVersion: "7.1",
		FFProbeJSON: json.RawMessage(`{}`), ObservedTags: json.RawMessage(`{}`),
		InspectedAt: time.Now().UTC(), AppliedOperationID: appliedOperationID,
	}
	if _, err := database.NewInsert().Model(variant).Exec(ctx); err != nil {
		t.Fatalf("insert media variant: %v", err)
	}
	return variant.ID
}

func linkLocationVariant(t *testing.T, ctx context.Context, database *bun.DB, locationID, variantID uuid.UUID) {
	t.Helper()
	if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
		Set("media_variant_id = ?", variantID).Set("updated_at = now()").
		Where("id = ?", locationID).Exec(ctx); err != nil {
		t.Fatalf("link location %s to variant %s: %v", locationID, variantID, err)
	}
}

func readLocationByID(t *testing.T, ctx context.Context, database *bun.DB, locationID uuid.UUID) persistence.SourceLocation {
	t.Helper()
	var location persistence.SourceLocation
	if err := database.NewSelect().Model(&location).Where("id = ?", locationID).Scan(ctx); err != nil {
		t.Fatalf("read location %s: %v", locationID, err)
	}
	return location
}

func countMediaVariants(t *testing.T, ctx context.Context, database *bun.DB) int {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM media_variant").Scan(ctx, &count); err != nil {
		t.Fatalf("count media variants: %v", err)
	}
	return count
}

func mediaVariantExists(t *testing.T, ctx context.Context, database *bun.DB, id uuid.UUID) bool {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM media_variant WHERE id = ?", id).Scan(ctx, &count); err != nil {
		t.Fatalf("read media variant %s: %v", id, err)
	}
	return count == 1
}

func countScanCandidates(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID) int {
	t.Helper()
	var count int
	if err := database.NewRaw("SELECT count(*) FROM source_scan_candidate WHERE operation_id = ?", operationID).Scan(ctx, &count); err != nil {
		t.Fatalf("count scan candidates of %s: %v", operationID, err)
	}
	return count
}
