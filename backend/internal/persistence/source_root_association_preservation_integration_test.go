//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This is an end-to-end persistence contract: source-root deletion removes the
// inventory's library links, not the library's current publication projection.
func TestDeleteSourceRootPreservesCurrentPublicationAndDetachesSourcesPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	repository := NewSetupManagerRepository(f.db)
	result := publicationResultForFixture(f)
	committed, err := repository.CommitPublicationResult(ctx, f.guard, result)
	if err != nil || !committed {
		t.Fatalf("establish current publication baseline = %v, %v", committed, err)
	}

	var trackID uuid.UUID
	if err := f.db.NewRaw(`SELECT track_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &trackID); err != nil {
		t.Fatal(err)
	}
	rootID := uuid.New()
	rootPath := filepath.Clean(t.TempDir())
	root := &SourceRoot{ID: rootID, ConfiguredPath: rootPath, DisplayName: "root to delete", Enabled: true, Status: SourceRootStatusAvailable}
	if err := NewSourceInventoryRepository(f.db).CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create source root: %v", err)
	}
	alternateRootID := uuid.New()
	alternateRootPath := filepath.Clean(t.TempDir())
	alternateRoot := &SourceRoot{ID: alternateRootID, ConfiguredPath: alternateRootPath, DisplayName: "alternative location", Enabled: true, Status: SourceRootStatusAvailable}
	if err := NewSourceInventoryRepository(f.db).CreateSourceRoot(ctx, alternateRoot); err != nil {
		t.Fatalf("create alternative source root: %v", err)
	}
	variantIDs := []uuid.UUID{uuid.New(), uuid.New()}
	locationIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for i, variantID := range variantIDs {
		variant := &SourceMediaVariant{ID: variantID, SizeBytes: int64(20 + i)}
		if _, err := f.db.NewInsert().Model(variant).Exec(ctx); err != nil {
			t.Fatalf("insert source media variant %s: %v", variantID, err)
		}
		location := &SourceLocation{
			ID: locationIDs[i], SourceRootID: rootID, RelativePath: filepath.Join("album", string(rune('a'+i))+".flac"),
			SizeBytes: int64(20 + i), Mtime: time.Now().UTC(), LastSeenScanGeneration: 1,
			ProbeStatus: SourceProbeStatusAudio, MediaVariantID: &variantID,
		}
		if i == 1 {
			location.SourceRootID = alternateRootID
		}
		if _, err := f.db.NewInsert().Model(location).Exec(ctx); err != nil {
			t.Fatalf("insert source location %s: %v", location.ID, err)
		}
	}
	var operationCountBefore, riverJobCountBefore int
	if err := f.db.NewRaw(`SELECT count(*) FROM operation`).Scan(ctx, &operationCountBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &riverJobCountBefore); err != nil {
		t.Fatal(err)
	}
	// The first alternative is selected; deleting its last location must not
	// select the unselected alternative or synthesize a replacement association.
	for i, variantID := range variantIDs {
		association := &TrackSource{
			ID: uuid.New(), TrackID: trackID, MediaVariantID: variantID, Selected: i == 0,
			Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`),
		}
		if _, err := f.db.NewInsert().Model(association).Exec(ctx); err != nil {
			t.Fatalf("insert source association: %v", err)
		}
	}
	var alternateSelected bool
	if err := f.db.NewRaw(`SELECT selected FROM track_source WHERE track_id=? AND media_variant_id=?`, trackID, variantIDs[1]).Scan(ctx, &alternateSelected); err != nil || alternateSelected {
		t.Fatalf("alternate association baseline selected=%v, %v; want unselected", alternateSelected, err)
	}
	// An unrelated variant that already has no location carries a SHA identity and
	// an unselected association. Root cleanup is bounded to the variants this root
	// owned, so this association must survive and neither the SHA variant nor the
	// link may be swept globally.
	unrelatedVariantID := uuid.New()
	unrelatedDigest := []byte(strings.ReplaceAll(uuid.NewString(), "-", ""))
	unrelatedCalculatedAt := time.Now().UTC()
	unrelatedAlgorithm := "sha256"
	unrelatedOperationID := uuid.New()
	unrelatedVariant := &SourceMediaVariant{
		ID: unrelatedVariantID, SizeBytes: 40, SourceSHA256: unrelatedDigest,
		SHA256CalculatedAt: &unrelatedCalculatedAt, SHA256Algorithm: &unrelatedAlgorithm,
		SHA256AppliedOperationID: &unrelatedOperationID,
	}
	if _, err := f.db.NewInsert().Model(unrelatedVariant).Exec(ctx); err != nil {
		t.Fatalf("insert unrelated sha variant: %v", err)
	}
	unrelatedAssociation := &TrackSource{
		ID: uuid.New(), TrackID: trackID, MediaVariantID: unrelatedVariantID, Selected: false,
		Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`),
	}
	if _, err := f.db.NewInsert().Model(unrelatedAssociation).Exec(ctx); err != nil {
		t.Fatalf("insert unrelated track source: %v", err)
	}

	if err := NewSourceInventoryRepository(f.db).DeleteSourceRoot(ctx, rootID, rootPath, 1); err != nil {
		t.Fatalf("delete confirmed source root: %v", err)
	}
	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatalf("read current publication after root deletion: %v", err)
	}
	if current.Generation != f.guard.CandidateGeneration || current.Epoch != f.guard.Epoch || current.RelativePath != result.Observation.RelativePath {
		t.Fatalf("current publication changed after source deletion: %+v", current)
	}
	var associationCount, selectedCount int
	if err := f.db.NewRaw(`SELECT count(*), count(*) FILTER (WHERE selected) FROM track_source WHERE track_id=?`, trackID).Scan(ctx, &associationCount, &selectedCount); err != nil {
		t.Fatal(err)
	}
	if associationCount != 2 || selectedCount != 0 {
		t.Fatalf("source associations after deleting selected location = %d (%d selected), want retained unselected alternative and unrelated association", associationCount, selectedCount)
	}
	var alternateAssociationCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_source WHERE track_id=? AND media_variant_id=? AND selected=false`, trackID, variantIDs[1]).Scan(ctx, &alternateAssociationCount); err != nil || alternateAssociationCount != 1 {
		t.Fatalf("unselected alternative association after deletion = %d, %v; want 1 retained", alternateAssociationCount, err)
	}
	var unrelatedAssociationCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_source WHERE id=? AND media_variant_id=? AND selected=false`, unrelatedAssociation.ID, unrelatedVariantID).Scan(ctx, &unrelatedAssociationCount); err != nil || unrelatedAssociationCount != 1 {
		t.Fatalf("unrelated locationless association after deletion = %d, %v; want 1 retained", unrelatedAssociationCount, err)
	}
	var unrelatedVariantCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM media_variant WHERE id=?`, unrelatedVariantID).Scan(ctx, &unrelatedVariantCount); err != nil || unrelatedVariantCount != 1 {
		t.Fatalf("unrelated sha variant after deletion = %d, %v; want 1 retained", unrelatedVariantCount, err)
	}
	var operationCountAfter, riverJobCountAfter int
	if err := f.db.NewRaw(`SELECT count(*) FROM operation`).Scan(ctx, &operationCountAfter); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &riverJobCountAfter); err != nil {
		t.Fatal(err)
	}
	if operationCountAfter != operationCountBefore || riverJobCountAfter != riverJobCountBefore {
		t.Fatalf("source deletion created fallback work: operations %d -> %d, River jobs %d -> %d", operationCountBefore, operationCountAfter, riverJobCountBefore, riverJobCountAfter)
	}
	var removedLocationCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_location WHERE id=?`, locationIDs[0]).Scan(ctx, &removedLocationCount); err != nil || removedLocationCount != 0 {
		t.Fatalf("deleted selected location count = %d, %v; want 0", removedLocationCount, err)
	}
	var survivingLocationCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_location WHERE id=? AND source_root_id=?`, locationIDs[1], alternateRootID).Scan(ctx, &survivingLocationCount); err != nil || survivingLocationCount != 1 {
		t.Fatalf("alternative location count = %d, %v; want 1", survivingLocationCount, err)
	}
}

func TestDeleteSourceRootRollsBackAssociationsWhenGroupingInvalidationFailsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	// Prove the real committed-CURRENT baseline on an independent fixture before
	// exercising a failure after the root and association mutations have begun.
	baseline := preparePublicationResultFixture(t)
	committed, err := NewSetupManagerRepository(baseline.db).CommitPublicationResult(ctx, baseline.guard, publicationResultForFixture(baseline))
	if err != nil || !committed {
		t.Fatalf("successful publication baseline = %v, %v", committed, err)
	}

	var trackID uuid.UUID
	if err := f.db.NewRaw(`SELECT track_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &trackID); err != nil {
		t.Fatal(err)
	}
	rootID, variantID := uuid.New(), uuid.New()
	rootPath := filepath.Clean(t.TempDir())
	root := &SourceRoot{ID: rootID, ConfiguredPath: rootPath, DisplayName: "rollback", Enabled: true, Status: SourceRootStatusAvailable}
	if err := NewSourceInventoryRepository(f.db).CreateSourceRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	variant := &SourceMediaVariant{ID: variantID, SizeBytes: 5}
	if _, err := f.db.NewInsert().Model(variant).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	location := &SourceLocation{ID: uuid.New(), SourceRootID: rootID, RelativePath: "rollback.flac", SizeBytes: 5, Mtime: time.Now().UTC(), LastSeenScanGeneration: 1, ProbeStatus: SourceProbeStatusAudio, MediaVariantID: &variantID}
	if _, err := f.db.NewInsert().Model(location).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	association := &TrackSource{ID: uuid.New(), TrackID: trackID, MediaVariantID: variantID, Selected: true, Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`)}
	if _, err := f.db.NewInsert().Model(association).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var selectedBefore bool
	if err := f.db.NewRaw(`SELECT selected FROM track_source WHERE id=?`, association.ID).Scan(ctx, &selectedBefore); err != nil || !selectedBefore {
		t.Fatalf("association baseline selected=%v, %v; want selected", selectedBefore, err)
	}

	triggerSuffix := uuid.NewString()
	functionName := "test_reject_grouping_invalidation_" + triggerSuffix[:8]
	triggerName := "test_reject_grouping_invalidation_trigger_" + triggerSuffix[:8]
	// The association literal is this fixture's server-generated UUID, never
	// request or operator input. The trigger proves the deletion transaction has
	// already removed the association before grouping invalidation runs: if the
	// cleanup were skipped or reordered, the diagnostic below fires instead of the
	// injected failure and the rollback assertions cannot pass.
	associationLiteral := association.ID.String()
	functionStatement := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF EXISTS (SELECT 1 FROM track_source WHERE id = '%s'::uuid) THEN
		RAISE EXCEPTION 'test association cleanup missing before grouping invalidation';
	END IF;
	RAISE EXCEPTION 'test grouping invalidation failure';
END
$$`, functionName, associationLiteral)
	if _, err := f.db.ExecContext(ctx, functionStatement); err != nil {
		t.Fatalf("create test-only failure function: %v", err)
	}
	if _, err := f.db.ExecContext(ctx, `CREATE TRIGGER `+triggerName+` BEFORE UPDATE ON incoming_grouping_state FOR EACH ROW EXECUTE FUNCTION `+functionName+`()`); err != nil {
		_, _ = f.db.ExecContext(ctx, `DROP FUNCTION `+functionName+`() CASCADE`)
		t.Fatalf("create test-only failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS `+triggerName+` ON incoming_grouping_state`)
		_, _ = f.db.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS `+functionName+`() CASCADE`)
	})

	err = NewSourceInventoryRepository(f.db).DeleteSourceRoot(ctx, rootID, rootPath, 1)
	if err == nil {
		t.Fatal("DeleteSourceRoot succeeded despite the downstream grouping invalidation failure")
	}
	if !strings.Contains(err.Error(), "test grouping invalidation failure") {
		t.Fatalf("deletion error = %q; want the injected grouping invalidation failure", err)
	}
	var count int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_root WHERE id=?`, rootID).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("root after rolled-back deletion = %d, %v; want retained", count, err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_location WHERE id=?`, location.ID).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("location after rolled-back deletion = %d, %v; want retained", count, err)
	}
	var selectedAfter bool
	if err := f.db.NewRaw(`SELECT selected FROM track_source WHERE id=?`, association.ID).Scan(ctx, &selectedAfter); err != nil || !selectedAfter {
		t.Fatalf("association after rolled-back deletion selected=%v, %v; want selected and retained", selectedAfter, err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM media_variant WHERE id=?`, variantID).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("variant after rolled-back deletion = %d, %v; want retained", count, err)
	}
}

func TestDeleteSourceRootWrongConfirmationLeavesLibraryAssociationsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	var trackID uuid.UUID
	if err := f.db.NewRaw(`SELECT track_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &trackID); err != nil {
		t.Fatal(err)
	}
	rootID, variantID := uuid.New(), uuid.New()
	rootPath := filepath.Clean(t.TempDir())
	root := &SourceRoot{ID: rootID, ConfiguredPath: rootPath, DisplayName: "confirmation", Enabled: true, Status: SourceRootStatusAvailable}
	if err := NewSourceInventoryRepository(f.db).CreateSourceRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	variant := &SourceMediaVariant{ID: variantID, SizeBytes: 5}
	if _, err := f.db.NewInsert().Model(variant).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	location := &SourceLocation{ID: uuid.New(), SourceRootID: rootID, RelativePath: "one.flac", SizeBytes: 5, Mtime: time.Now().UTC(), LastSeenScanGeneration: 1, ProbeStatus: SourceProbeStatusAudio, MediaVariantID: &variantID}
	if _, err := f.db.NewInsert().Model(location).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	association := &TrackSource{ID: uuid.New(), TrackID: trackID, MediaVariantID: variantID, Selected: true, Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`)}
	if _, err := f.db.NewInsert().Model(association).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	err := NewSourceInventoryRepository(f.db).DeleteSourceRoot(ctx, rootID, rootPath+"-different", 1)
	if err == nil {
		t.Fatal("delete with mismatched confirmed path succeeded")
	}
	var count int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_source WHERE id=?`, association.ID).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("association after rejected deletion = %d, %v; want retained", count, err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM source_location WHERE id=?`, location.ID).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("location after rejected deletion = %d, %v; want retained", count, err)
	}
}

func TestDeleteSourceRootKeepsAssociationWithSurvivingRootLocationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	var trackID uuid.UUID
	if err := f.db.NewRaw(`SELECT track_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &trackID); err != nil {
		t.Fatal(err)
	}
	rootIDs := []uuid.UUID{uuid.New(), uuid.New()}
	paths := []string{filepath.Clean(t.TempDir()), filepath.Clean(t.TempDir())}
	for i, rootID := range rootIDs {
		root := &SourceRoot{ID: rootID, ConfiguredPath: paths[i], DisplayName: "shared variant", Enabled: true, Status: SourceRootStatusAvailable}
		if err := NewSourceInventoryRepository(f.db).CreateSourceRoot(ctx, root); err != nil {
			t.Fatalf("create source root %d: %v", i, err)
		}
	}
	variantID := uuid.New()
	variant := &SourceMediaVariant{ID: variantID, SizeBytes: 5}
	if _, err := f.db.NewInsert().Model(variant).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for i, rootID := range rootIDs {
		location := &SourceLocation{ID: uuid.New(), SourceRootID: rootID, RelativePath: "shared.flac", SizeBytes: 5, Mtime: time.Now().UTC(), LastSeenScanGeneration: 1, ProbeStatus: SourceProbeStatusAudio, MediaVariantID: &variantID}
		if _, err := f.db.NewInsert().Model(location).Exec(ctx); err != nil {
			t.Fatalf("insert location for root %d: %v", i, err)
		}
	}
	association := &TrackSource{ID: uuid.New(), TrackID: trackID, MediaVariantID: variantID, Selected: true, Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`)}
	if _, err := f.db.NewInsert().Model(association).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := NewSourceInventoryRepository(f.db).DeleteSourceRoot(ctx, rootIDs[0], paths[0], 1); err != nil {
		t.Fatalf("delete one of two roots sharing a variant: %v", err)
	}
	var count int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_location WHERE source_root_id=? AND media_variant_id=?`, rootIDs[1], variantID).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("surviving root's variant locations = %d, %v; want 1", count, err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM track_source WHERE id=?`, association.ID).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("association after deleting only one location owner = %d, %v; want retained", count, err)
	}
}
