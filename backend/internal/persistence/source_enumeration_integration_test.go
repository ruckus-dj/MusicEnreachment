//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestApplySourceEnumerationCleansAssociationsOnlyForVariantsWithoutLocationsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.OpenMigratedWithRiver(t)
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-association-cleanup")
	mtime := probeMtime()
	first := newSourceScanOperation(t, ctx, database, root, "running")
	paths := []string{"sha-missing.flac", "digestless-missing.flac", "shared-missing.flac", "shared-survivor.flac"}
	candidates := make([]persistence.SourceScanCandidateInput, 0, len(paths))
	for _, path := range paths {
		candidates = append(candidates, enumerationCandidate(path, 5, mtime))
	}
	if err := inventory.ReplaceSourceScanCandidates(ctx, first.ID, candidates); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(first, root, nil)); err != nil {
		t.Fatal(err)
	}
	shaVariant := insertAssociationCleanupVariant(t, ctx, database, true)
	digestlessVariant := insertAssociationCleanupVariant(t, ctx, database, false)
	sharedVariant := insertAssociationCleanupVariant(t, ctx, database, true)
	variants := map[string]uuid.UUID{
		paths[0]: shaVariant, paths[1]: digestlessVariant,
		paths[2]: sharedVariant, paths[3]: sharedVariant,
	}
	for path, variantID := range variants {
		location := readLocation(t, ctx, database, root.ID, path)
		if _, err := database.ExecContext(ctx, `UPDATE source_location SET media_variant_id=? WHERE id=?`, variantID, location.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, variantID := range []uuid.UUID{shaVariant, digestlessVariant, sharedVariant} {
		trackID := uuid.New()
		if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'Enumeration association')`, trackID); err != nil {
			t.Fatal(err)
		}
		association := &persistence.TrackSource{ID: uuid.New(), TrackID: trackID, MediaVariantID: variantID,
			Selected: true, Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`)}
		if _, err := database.NewInsert().Model(association).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	setOperationState(t, ctx, database, first.ID, "succeeded")

	foreignRoot := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-association-cleanup-foreign")
	foreignSeed := newSourceScanOperation(t, ctx, database, foreignRoot, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, foreignSeed.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate(paths[0], 5, mtime),
	}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(foreignSeed, foreignRoot, nil)); err != nil {
		t.Fatalf("apply foreign enumeration: %v", err)
	}
	setOperationState(t, ctx, database, foreignSeed.ID, "succeeded")
	foreignLocation := readLocation(t, ctx, database, foreignRoot.ID, paths[0])
	foreignLive := newSourceScanOperation(t, ctx, database, foreignRoot, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, foreignLive.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate(paths[0], 5, mtime),
	}); err != nil {
		t.Fatal(err)
	}
	survivor := readLocation(t, ctx, database, root.ID, paths[3])

	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate(paths[3], 5, mtime),
	}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, nil)); err != nil {
		t.Fatal(err)
	}
	assertTrackSourceCount(t, ctx, database, shaVariant, 0)
	assertTrackSourceCount(t, ctx, database, digestlessVariant, 0)
	assertTrackSourceCount(t, ctx, database, sharedVariant, 1)
	if _, err := readLocationResult(ctx, database, root.ID, paths[0]); err == nil {
		t.Fatal("genuinely missing location was retained")
	}
	if _, err := readLocationResult(ctx, database, root.ID, paths[1]); err == nil {
		t.Fatal("genuinely missing digestless location was retained")
	}
	if foreignAfter := readLocation(t, ctx, database, foreignRoot.ID, paths[0]); foreignAfter.ID != foreignLocation.ID {
		t.Fatalf("foreign location %q identity changed: %s -> %s", paths[0], foreignLocation.ID, foreignAfter.ID)
	}
	if survivorAfter := readLocation(t, ctx, database, root.ID, paths[3]); survivorAfter.ID != survivor.ID {
		t.Fatalf("surviving location %q identity changed: %s -> %s", paths[3], survivor.ID, survivorAfter.ID)
	}
	assertCandidateCount(t, ctx, database, foreignLive.ID, 1)
}

func TestApplySourceEnumerationChangedFileDoesNotInheritVariantOrCleanItsAssociationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.OpenMigratedWithRiver(t)
	inventory := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/enumeration-changed-association")
	mtime := probeMtime()
	first := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, first.ID, []persistence.SourceScanCandidateInput{enumerationCandidate("changed.flac", 5, mtime)}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(first, root, nil)); err != nil {
		t.Fatal(err)
	}
	location := readLocation(t, ctx, database, root.ID, "changed.flac")
	variantID := insertAssociationCleanupVariant(t, ctx, database, true)
	if _, err := database.ExecContext(ctx, `UPDATE source_location SET media_variant_id=? WHERE id=?`, variantID, location.ID); err != nil {
		t.Fatal(err)
	}
	trackID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'Changed source association')`, trackID); err != nil {
		t.Fatal(err)
	}
	association := &persistence.TrackSource{ID: uuid.New(), TrackID: trackID, MediaVariantID: variantID,
		Selected: true, Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`)}
	if _, err := database.NewInsert().Model(association).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	setOperationState(t, ctx, database, first.ID, "succeeded")

	second := newSourceScanOperation(t, ctx, database, root, "running")
	if err := inventory.ReplaceSourceScanCandidates(ctx, second.ID, []persistence.SourceScanCandidateInput{enumerationCandidate("changed.flac", 6, mtime.Add(time.Second))}); err != nil {
		t.Fatal(err)
	}
	if err := inventory.ApplySourceEnumeration(ctx, enumerationApply(second, root, nil)); err != nil {
		t.Fatal(err)
	}
	changed := readLocation(t, ctx, database, root.ID, "changed.flac")
	if changed.ID != location.ID || changed.MediaVariantID != nil || changed.ProbeStatus != persistence.SourceProbeStatusNotAnalyzed {
		t.Fatalf("changed file retained old identity or analysis: %+v", changed)
	}
	assertTrackSourceCount(t, ctx, database, variantID, 1)
}

func enumerationCandidate(path string, size int64, mtime time.Time) persistence.SourceScanCandidateInput {
	return persistence.SourceScanCandidateInput{RelativePath: path, SizeBytes: size, Mtime: mtime, ProbeStatus: persistence.SourceProbeStatusNotAnalyzed}
}

func enumerationApply(operation *persistence.Operation, root *persistence.SourceRoot, scopes []persistence.SourceEnumerationScope) persistence.SourceEnumerationApply {
	return persistence.SourceEnumerationApply{
		OperationID: operation.ID, ExpectedConfiguredPath: root.ConfiguredPath, ExpectedAttempt: operation.Attempt,
		ExpectedJobID: *operation.RiverJobID, SHA256Enabled: true, Scopes: scopes,
		FailureSafeError: "The source directory could not be read completely. The previous inventory is unchanged.",
	}
}
