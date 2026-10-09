//go:build integration

package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// TestIncomingGroupingInvalidatesOnExplicitFingerprintRerunAdmissionWithPostgreSQL
// guards the admission path: re-queueing a previously successful fingerprint
// step must advance the incoming grouping epoch so its read model is recomputed,
// while a rejected admission must not touch the grouping state at all.
func TestIncomingGroupingInvalidatesOnExplicitFingerprintRerunAdmissionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	grouping := persistence.NewIncomingGroupingRepository(database)
	client := openScanEnqueueRiver(t, database)

	root := createInventoryRoot(t, ctx, repository, "/srv/incoming-group-rerun-invalidation")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/rerun.flac", 4096, probeMtime())
	establishInventory(t, ctx, database, root)
	priorResultID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_fingerprint_result
		(id,fpcalc_version,version_banner,algorithm_namespace,algorithm_id,fingerprint,reported_duration,calculated_at,applied_operation_id,parser_contract_version,winning_result_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, priorResultID, "1.5.1", "fpcalc version 1.5.1", "chromaprint", 1, "AQAA", 180.0, time.Now().UTC(), uuid.New(), 1, priorResultID); err != nil {
		t.Fatalf("insert prior successful fingerprint result: %v", err)
	}
	work := normalizedWork(t, ctx, repository, root, location, false)
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step (work_id,step,state,success_fingerprint_result_id,success_reuse_origin) VALUES (?,'fingerprint','succeeded',?,'executed')`, work.ID, priorResultID); err != nil {
		t.Fatalf("insert prior successful fingerprint step: %v", err)
	}
	installation := insertAnalysisFPCalcFixture(t, ctx, database, "fpcalc-rerun-release", "1.5.1")
	tool := normalizedToolSelection(t, ctx, database, installation.ID, "fpcalc")
	step := string(persistence.SourceStepFingerprint)
	rerun := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &step, false, true, []persistence.SourceAnalysisToolSelection{tool})

	// A freshly migrated grouping is already stale; clear the flag so the
	// admission's own effect is observable.
	if _, err := database.ExecContext(ctx, `UPDATE incoming_grouping_state SET needs_refresh=false WHERE singleton=true`); err != nil {
		t.Fatalf("clear incoming grouping refresh flag: %v", err)
	}
	before, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read incoming grouping state before rerun: %v", err)
	}
	if before.NeedsRefresh {
		t.Fatalf("incoming grouping precondition = %+v; want needs_refresh false", before)
	}

	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, rerun, client, service.SourceAnalysisJobArgs{OperationID: rerun.ID}, nil); err != nil {
		t.Fatalf("admit explicit fingerprint rerun: %v", err)
	}
	afterRerun, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read incoming grouping state after rerun: %v", err)
	}
	if afterRerun.Revision != before.Revision+1 || !afterRerun.NeedsRefresh {
		t.Fatalf("incoming grouping after explicit fingerprint rerun = %+v; want revision %d and needs refresh", afterRerun, before.Revision+1)
	}
	var queuedState string
	if err := database.NewRaw(`SELECT state FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, work.ID).Scan(ctx, &queuedState); err != nil || queuedState != "queued" {
		t.Fatalf("fingerprint step after rerun = %q, %v; want queued", queuedState, err)
	}

	// A rejected admission must leave the grouping state untouched rather than
	// half-applying an invalidation. The successful rerun holds the work, so a
	// second admission is rejected before any step is re-queued.
	if _, err := database.ExecContext(ctx, `UPDATE incoming_grouping_state SET needs_refresh=false WHERE singleton=true`); err != nil {
		t.Fatalf("clear incoming grouping refresh flag before failed admission: %v", err)
	}
	beforeFailure, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read incoming grouping state before failed admission: %v", err)
	}
	second := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &step, false, true, []persistence.SourceAnalysisToolSelection{tool})
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, second, client, service.SourceAnalysisJobArgs{OperationID: second.ID}, nil); err == nil {
		t.Fatal("second rerun admission unexpectedly succeeded")
	}
	afterFailure, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read incoming grouping state after failed admission: %v", err)
	}
	if afterFailure != beforeFailure {
		t.Fatalf("failed admission changed incoming grouping state: before %+v, after %+v", beforeFailure, afterFailure)
	}
	if afterFailure.NeedsRefresh {
		t.Fatalf("failed admission left needs_refresh true: %+v", afterFailure)
	}
}
