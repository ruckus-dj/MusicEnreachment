//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestCurrentSourceAnalysisSchemaContractWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	if !constraintExists(t, db, "operation_normalized_analysis_shape") {
		t.Fatal("normalized analysis shape constraint is missing from the current schema")
	}
	for _, obsolete := range []string{"operation_active_analysis_has_target_location", "operation_active_analysis_has_installation"} {
		if constraintExists(t, db, obsolete) {
			t.Errorf("obsolete active-analysis selector constraint %s remains", obsolete)
		}
	}

	rootID := newVariantRoot(t, ctx, db, "/srv/current-analysis-contract")
	locationID := newVariantLocation(t, ctx, db, rootID, "album/track.flac")
	_, err := db.ExecContext(ctx, `INSERT INTO operation
		(id,kind,state,stage,input_snapshot,attempt,created_at,updated_at,target_source_root_id,target_source_location_id,source_analysis_mode)
		VALUES (?, 'analyze_source','queued','queued','{}',1,now(),now(),?,?,'batch')`,
		uuid.New(), rootID, locationID)
	if err == nil || !strings.Contains(err.Error(), "operation_normalized_analysis_shape") {
		t.Fatalf("legacy multi-target analysis operation error = %v, want normalized-shape violation", err)
	}

	locationA := newVariantLocation(t, ctx, db, rootID, "album/a.flac")
	locationB := newVariantLocation(t, ctx, db, rootID, "album/b.flac")
	workA := insertGuardClosureWork(t, ctx, db, rootID, locationA)
	workB := insertGuardClosureWork(t, ctx, db, rootID, locationB)
	insertCurrentAnalysisHold(t, ctx, db, rootID, workA)
	insertCurrentAnalysisHold(t, ctx, db, rootID, workB)
	if err := tryCurrentAnalysisHold(ctx, db, rootID, workA); err == nil {
		t.Fatal("duplicate active hold for one work was accepted")
	}
}

func insertCurrentAnalysisHold(t *testing.T, ctx context.Context, db *bun.DB, rootID, workID uuid.UUID) {
	t.Helper()
	if err := tryCurrentAnalysisHold(ctx, db, rootID, workID); err != nil {
		t.Fatalf("insert active analysis hold for work %s: %v", workID, err)
	}
}

func tryCurrentAnalysisHold(ctx context.Context, db *bun.DB, rootID, workID uuid.UUID) error {
	operationID := uuid.New()
	snapshot := `{"schema_version":1,"mode":"batch","work_ids":["` + workID.String() + `"],"rerun_target":false}`
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO operation
			(id,kind,state,stage,input_snapshot,target_source_root_id,attempt,source_analysis_mode)
			VALUES (?, 'analyze_source','queued','queued',?::jsonb,?,1,'batch')`, operationID, snapshot, rootID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`INSERT INTO operation_source_work_hold(operation_id,work_id) VALUES (?,?)`, operationID, workID).Exec(ctx)
		return err
	})
}
