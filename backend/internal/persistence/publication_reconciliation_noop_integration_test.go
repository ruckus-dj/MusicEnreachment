//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func TestUnpublishedPublicationReconciliationRejectsLateCurrentPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if committed, err := repo.CommitPublicationResult(ctx, f.guard, publicationResultForFixture(f)); err != nil || !committed {
		t.Fatalf("establish valid CURRENT fixture = %v, %v", committed, err)
	}
	position := f.guard.ExpectedPositionID
	var release uuid.UUID
	if err := f.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, position).Scan(ctx, &release); err != nil {
		t.Fatal(err)
	}
	guard, _ := startPublicationReconciliationForResult(t, ctx, repo, f.guard.Epoch, position)
	acknowledged, err := repo.AcknowledgePublicationReconciliationUnpublished(ctx, release, guard)
	if err != nil || acknowledged {
		t.Fatalf("late CURRENT was accepted as unpublished: acknowledged=%v err=%v", acknowledged, err)
	}
	var dirty int
	var eventState string
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, guard.OutputEpoch, position).Scan(ctx, &dirty); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT state FROM publication_reconciliation_event WHERE operation_id=?`, guard.OperationID).Scan(ctx, &eventState); err != nil {
		t.Fatal(err)
	}
	if dirty == 0 || eventState != "dispatched" {
		t.Fatalf("rejected unpublished proof lost live obligation: dirty=%d event=%s", dirty, eventState)
	}
}

func seedLocalNoopAuthority(t *testing.T, ctx context.Context, db *bun.DB, epoch, position, generation uuid.UUID) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO position_source_authority(position_id,mode,revision,publication_epoch,publication_generation,source_location_id,media_variant_id) VALUES (?,'manual',?,?,?, ?,?)`, position, uuid.New(), epoch, generation, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value) VALUES ('publication_format','source') ON CONFLICT(setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`); err != nil {
		t.Fatal(err)
	}
}
