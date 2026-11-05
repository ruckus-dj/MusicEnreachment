//go:build integration

package persistence

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type retryResolutionFixture struct {
	ctx        context.Context
	fixture    publicationExecutionFixture
	repository *SetupManagerRepository
	originalID uuid.UUID
	newGuard   PublicationSourceCleanupDeliveryGuard
	itemIDs    []uuid.UUID
}

func newRetryResolutionFixture(t *testing.T) retryResolutionFixture {
	t.Helper()
	return newRetryResolutionFixtureWithDatabase(t, nil, "")
}

func newRetryResolutionFixtureWithDatabase(t *testing.T, db *bun.DB, scope string) retryResolutionFixture {
	t.Helper()
	ctx := context.Background()
	var fixture publicationExecutionFixture
	var repository *SetupManagerRepository
	var variantID uuid.UUID
	if db == nil {
		fixture, repository, variantID = publicationSourceCleanupReadyFixture(t)
	} else {
		fixture, repository, variantID = publicationSourceCleanupReadyFixtureWithDatabase(t, db, scope)
	}
	if _, err := fixture.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version='cleanup-remove-v1' WHERE media_variant_id=?`, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var work SourceAnalysisWork
	if err := fixture.db.NewSelect().Model(&work).Where("id IN (SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?)", variantID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	// Freeze a native absolute root, including a volume on Windows. The shared
	// read-only protection fixture's "/source" is not an executable Windows root.
	root := t.TempDir()
	if !filepath.IsAbs(root) {
		t.Fatalf("retry source root is not native absolute: %q", root)
	}
	if _, err := fixture.db.NewRaw(`UPDATE source_root SET configured_path=?,inventory_path=? WHERE id=?`, root, root, work.SourceRootID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE source_analysis_work SET configured_path=?,inventory_path=? WHERE source_root_id=?`, root, root, work.SourceRootID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	work.ConfiguredPath, work.InventoryPath = root, root
	addCleanupRetrySourceCopies(t, ctx, fixture, work, "retry-second.flac", "retry-third.flac")
	oldGuard, originalID := admitAndClaimSourceCleanup(t, ctx, fixture, repository)
	if started, err := repository.StartPublicationSourceCleanupExecution(ctx, oldGuard); err != nil || !started {
		t.Fatalf("start original cleanup=%t,%v", started, err)
	}
	var items []SourceCleanupItem
	if err := fixture.db.NewSelect().Model(&items).Where("operation_id=?", originalID).Order("id ASC").Scan(ctx); err != nil || len(items) != 3 {
		t.Fatalf("original cleanup items=%d,%v; want three", len(items), err)
	}
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, oldGuard, items[0].ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return nil }); err != nil || outcome != PublicationSourceCleanupRemovalCompleted {
		t.Fatalf("complete original first item=%s,%v", outcome, err)
	}
	uncertain := errors.New("callback result is uncertain")
	if outcome, err := repository.RemovePublicationSourceCleanupItem(ctx, oldGuard, items[1].ID, func(context.Context, PublicationSourceCleanupRemovalRequest) error { return uncertain }); outcome != PublicationSourceCleanupRemovalBlocked || !errors.Is(err, uncertain) {
		t.Fatalf("leave original second item uncertain=%s,%v", outcome, err)
	}
	if failed, err := repository.FailPublicationSourceCleanupDelivery(ctx, oldGuard, "source removal result is unknown"); err != nil || !failed {
		t.Fatalf("return original delivery=%t,%v", failed, err)
	}
	admitted, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalID, new(publicationSourceCleanupTestInserter))
	if err != nil || admitted.OperationID == uuid.Nil || len(admitted.Blockers) != 0 {
		t.Fatalf("admit explicit retry=%+v,%v", admitted, err)
	}
	var jobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, admitted.OperationID).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var grant SourceCleanupExplicitRetry
	if err := fixture.db.NewSelect().Model(&grant).Where("recovery_operation_id=?", admitted.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	newGuard := PublicationSourceCleanupDeliveryGuard{OperationID: admitted.OperationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1,
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "retry-resolution", Epoch: grant.Epoch, PositionID: grant.PositionID}
	if outcome, err := repository.ClaimPublicationSourceCleanupExplicitRetryDelivery(ctx, newGuard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim explicit retry=%s,%v", outcome, err)
	}
	return retryResolutionFixture{ctx: ctx, fixture: fixture, repository: repository, originalID: originalID, newGuard: newGuard,
		itemIDs: []uuid.UUID{items[0].ID, items[1].ID, items[2].ID}}
}

func TestPublicationSourceCleanupRetryResolutionLifecyclePostgreSQL(t *testing.T) {
	t.Parallel()
	runRetryResolutionScenarios(t, []retryResolutionScenario{
		{"owner-returned frontier", retryReplacementOwnerReturnedScenario},
		{"two replacement frontier", retryReplacementTwoFrontiersScenario},
		{"same revision rewrite", retryReplacementSameRevisionScenario},
		{"enqueue rollback", retryReplacementEnqueueFailureScenario},
		{"concurrent replacement", retryReplacementConcurrentScenario},
	})
}

type retryResolutionScenario struct {
	name string
	run  func(*testing.T, *bun.DB, string)
}

func runRetryResolutionScenarios(t *testing.T, scenarios []retryResolutionScenario) {
	t.Helper()
	db := testpostgres.OpenMigratedWithRiver(t)
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			scope := uuid.NewString()
			preservePublicationRemoveSourcesSetting(t, db)
			scenario.run(t, db, scope)
		})
	}
}
