//go:build integration

package migrations_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

func TestPublicationReconciliationToolReadsRollbackPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name < "20261030000006" {
			prior.Add(migration)
		} else if migration.Name == "20261030000006" {
			target.Add(migration)
		}
	}
	if len(target.Sorted()) != 1 {
		t.Fatalf("target migrations = %d, want one", len(target.Sorted()))
	}
	priorMigrator := migrate.NewMigrator(db, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := priorMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	targetMigrator := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true))
	if err := targetMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := targetMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	installID, operationID := uuid.New(), uuid.New()
	positionID, epoch, releaseID, trackID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	snapshot, err := json.Marshal(map[string]any{"version": 1, "position_id": positionID.String(), "output_epoch": epoch.String()})
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title,metadata) VALUES (?,'Release','{}'::jsonb)`, []any{releaseID}},
		{`INSERT INTO local_track(id,title,metadata) VALUES (?,'Track','{}'::jsonb)`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,track_id,medium,position,displayed_number) VALUES (?,?,?,0,0,'1')`, []any{positionID, releaseID, trackID}},
		{`INSERT INTO publication_reconciliation_request(output_epoch,position_id,requested_generation) VALUES (?,?,1)`, []any{epoch, positionID}},
	} {
		if _, err := db.ExecContext(ctx, seed.query, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	releaseIdentity := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO tool_installation
		(id,package_kind,platform_goos,platform_goarch,source_name,release_identity,relative_path,state,verified_at)
		VALUES (?,'ffmpeg','linux','amd64','test',?,?,'ready',?)`, installID, releaseIdentity, "ffmpeg/"+releaseIdentity, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	insert := func(required bool) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,tools_read_required)
				VALUES (?,'publication_reconcile_v1','running','reconciling',?::jsonb,?)`, operationID, string(snapshot), required).Exec(ctx); err != nil {
				return err
			}
			if required {
				_, err := tx.NewRaw(`INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?)`, operationID, installID).Exec(ctx)
				if err != nil {
					return err
				}
			}
			_, err := tx.NewRaw(`INSERT INTO publication_reconciliation_event(id,output_epoch,position_id,operation_id,observed_generation) VALUES (?,?,?,?,1)`, uuid.New(), epoch, positionID, operationID).Exec(ctx)
			return err
		})
	}
	if err := insert(true); err != nil {
		t.Fatalf("insert matching active tool-reader operation and hold: %v", err)
	}

	if _, err := targetMigrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "cannot roll back publication reconciliation tool reads while tool holds remain") {
		t.Fatalf("rollback with retained hold = %v, want refusal", err)
	}
	var retained bool
	if err := db.NewRaw(`SELECT o.tools_read_required AND EXISTS(SELECT 1 FROM operation_tool_read_hold h WHERE h.operation_id=o.id AND h.installation_id=?) FROM operation o WHERE o.id=?`, installID, operationID).Scan(ctx, &retained); err != nil || !retained {
		t.Fatalf("authority after refused rollback: retained=%t err=%v", retained, err)
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=? AND installation_id=?`, operationID, installID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`UPDATE operation SET tools_read_required=false WHERE id=?`, operationID).Exec(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := targetMigrator.Rollback(ctx); err != nil {
		t.Fatalf("rollback after clean hold release: %v", err)
	}
	// Once rolled back, the prior CHECK must reject the same reconcile reader
	// shape. Assert the immediate shape CHECK, not the deferred hold constraint.
	_, err = db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,tools_read_required)
		VALUES (?,'publication_reconcile_v1','running','reconciling',?::jsonb,true)`, uuid.New(), string(snapshot))
	if err == nil || !strings.Contains(err.Error(), "operation_source_analysis_target_shape") {
		t.Fatalf("post-rollback reconcile tool reader error = %v, want shape CHECK violation", err)
	}
}
