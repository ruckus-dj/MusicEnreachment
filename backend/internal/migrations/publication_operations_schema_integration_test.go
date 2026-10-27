//go:build integration

package migrations_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

func TestPublicationOperationsRequireTypedSnapshotsAndOwnersPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	position, release, track := uuid.New(), uuid.New(), uuid.New()
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,'release')`, []any{release}},
		{`INSERT INTO local_track(id,title) VALUES (?,'track')`, []any{track}},
		{`INSERT INTO release_track(id,release_id,track_id,medium,position) VALUES (?,?,?,0,0)`, []any{position, release, track}},
	} {
		if _, err := db.NewRaw(seed.query, seed.args...).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	makeSnapshot := func(kind string, epoch uuid.UUID) string {
		body := map[string]any{"version": 1, "position_id": position.String(), "output_epoch": epoch.String()}
		if kind == "publish_track_v1" {
			body["intent_kind"] = "audio"
		}
		encoded, _ := json.Marshal(body)
		return string(encoded)
	}
	insertPublish := func(withAttempt bool, epoch uuid.UUID) error {
		id := uuid.New()
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,'publish_track_v1','queued','queued',?::jsonb)`, id, makeSnapshot("publish_track_v1", epoch)).Exec(ctx); err != nil {
				return err
			}
			if withAttempt {
				_, err := tx.NewRaw(`INSERT INTO track_publication_attempt(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest)
					VALUES (?,?,?,?,'audio','queued','{}'::jsonb,'{}'::jsonb)`, id, position, epoch, uuid.New()).Exec(ctx)
				return err
			}
			return nil
		})
	}
	// A malformed extra source field is rejected by the snapshot contract.
	badID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,'publish_track_v1','queued','queued',?::jsonb)`, badID,
		`{"version":1,"position_id":"`+position.String()+`","output_epoch":"`+uuid.NewString()+`","intent_kind":"audio","source_id":"x"}`).Exec(ctx); err == nil || !strings.Contains(err.Error(), "operation_publication_snapshot_check") {
		t.Fatalf("source-bearing snapshot constraint = %v", err)
	}
	if err := insertPublish(false, uuid.New()); err == nil || !strings.Contains(err.Error(), "active publication operation requires a matching attempt") {
		t.Fatalf("ownerless publication operation constraint = %v", err)
	}
	epoch := uuid.New()
	if err := insertPublish(true, epoch); err != nil {
		t.Fatalf("matching operation and attempt: %v", err)
	}
	// Settle this scenario before reusing its position. Otherwise uniqueness
	// would mask the deferred ownership checks exercised below.
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='cancelled',safe_error='test settled',finished_at=now()
			WHERE id IN (SELECT operation_id FROM track_publication_attempt WHERE position_id=?)`, position).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`DELETE FROM track_publication_attempt WHERE position_id=?`, position).Exec(ctx)
		return err
	}); err != nil {
		t.Fatalf("settle successful fixture: %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,target_installation_id)
		VALUES (?,'publish_track_v1','queued','queued',?::jsonb,?)`, uuid.New(), makeSnapshot("publish_track_v1", epoch), uuid.New()).Exec(ctx); err == nil || !strings.Contains(err.Error(), "operation_source_analysis_target_shape") {
		t.Fatalf("publication tools target constraint = %v", err)
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		id, attemptEpoch := uuid.New(), uuid.New()
		if _, err := tx.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,'publish_track_v1','queued','queued',?::jsonb)`, id, makeSnapshot("publish_track_v1", attemptEpoch)).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO track_publication_attempt(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest) VALUES (?,?,?,?,'audio','queued','{}'::jsonb,'{}'::jsonb)`, id, position, attemptEpoch, uuid.New()).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`DELETE FROM track_publication_attempt WHERE operation_id=?`, id).Exec(ctx)
		return err
	}); err == nil || !strings.Contains(err.Error(), "active publication operation requires a matching attempt") {
		t.Fatalf("queued owner deletion constraint = %v", err)
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		id, cancelEpoch := uuid.New(), uuid.New()
		if _, err := tx.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,'publish_track_v1','queued','queued',?::jsonb)`, id, makeSnapshot("publish_track_v1", cancelEpoch)).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO track_publication_attempt(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest) VALUES (?,?,?,?,'audio','queued','{}'::jsonb,'{}'::jsonb)`, id, position, cancelEpoch, uuid.New()).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='cancelled',safe_error='cancelled',finished_at=now() WHERE id=?`, id).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`DELETE FROM track_publication_attempt WHERE operation_id=?`, id).Exec(ctx)
		return err
	}); err != nil {
		t.Fatalf("cancel and delete owner in one transaction: %v", err)
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		id := uuid.New()
		wrongEpoch := uuid.New()
		if _, err := tx.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,'publish_track_v1','queued','queued',?::jsonb)`, id, makeSnapshot("publish_track_v1", epoch)).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`INSERT INTO track_publication_attempt(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest) VALUES (?,?,?,?,'audio','queued','{}'::jsonb,'{}'::jsonb)`, id, position, wrongEpoch, uuid.New()).Exec(ctx)
		return err
	}); err == nil || !strings.Contains(err.Error(), "active publication operation requires a matching attempt") {
		t.Fatalf("mismatched epoch constraint = %v", err)
	}
	if _, err := db.NewRaw(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key)
		VALUES (?,?,decode(repeat('01',32),'hex'),'key','file','file',?, 'dir')`, uuid.New(), uuid.New(), position).Exec(ctx); err == nil || !strings.Contains(err.Error(), "publication_namespace_claim_owner_scope_check") {
		t.Fatalf("file namespace owner constraint = %v", err)
	}
	otherPosition := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO release_track(id,release_id,track_id,medium,position) VALUES (?,?,?,0,1)`, otherPosition, release, track).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "valid", "wrong epoch", "wrong position", "delete owner", "cancel owner"} {
		t.Run("cleanup/"+mode, func(t *testing.T) {
			err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				id, itemID := uuid.New(), uuid.New()
				if _, err := tx.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot)
					VALUES (?,'cleanup_publication_sources_v1','queued','queued',?::jsonb)`, id, makeSnapshot("cleanup_publication_sources_v1", epoch)).Exec(ctx); err != nil {
					return err
				}
				if mode == "missing" {
					return nil
				}
				itemEpoch, itemPosition := epoch, position
				if mode == "wrong epoch" {
					itemEpoch = uuid.New()
				}
				if mode == "wrong position" {
					itemPosition = otherPosition
				}
				if _, err := tx.NewRaw(`INSERT INTO source_cleanup_item
					(id,operation_id,position_id,epoch,publication_generation,state,source_snapshot,borrower_revision)
					VALUES (?,?,?,?,?,'queued','{}',?)`, itemID, id, itemPosition, itemEpoch, uuid.New(), uuid.New()).Exec(ctx); err != nil {
					return err
				}
				if mode == "cancel owner" {
					if _, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='cancelled',safe_error='cancelled',finished_at=now() WHERE id=?`, id).Exec(ctx); err != nil {
						return err
					}
				}
				if mode == "delete owner" || mode == "cancel owner" {
					_, err := tx.NewRaw(`DELETE FROM source_cleanup_item WHERE id=?`, itemID).Exec(ctx)
					return err
				}
				return nil
			})
			if mode == "valid" || mode == "cancel owner" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "active cleanup operation requires a matching item") {
				t.Fatalf("cleanup owner constraint = %v", err)
			}
		})
	}
}

func TestPublicationOperationContractRollbackRequiresSettlementPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, migration := range loadMigrations(t).Sorted() {
		if migration.Name < "20261017000006" {
			prior.Add(migration)
		} else if migration.Name == "20261017000006" {
			target.Add(migration)
		}
	}
	for _, collection := range []*migrate.Migrations{prior, target} {
		migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
		if err := migrator.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := migrator.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertMigrationInventory(t, target, []string{"20261017000006"})
	id := uuid.New()
	summary, err := json.Marshal(map[string]any{"version": 1, "position_id": uuid.NewString(), "output_epoch": uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at)
		VALUES (?,'cleanup_publication_sources_v1','succeeded','complete',?::jsonb,now())`, id, string(summary)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	migrator := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true))
	if _, err := migrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "cannot roll back publication operation contract while publication state is live") {
		t.Fatalf("unsafe operation contract rollback = %v", err)
	}
	if _, err := db.NewRaw(`DELETE FROM operation WHERE id=?`, id).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	group, err := migrator.Rollback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertMigrationGroup(t, group, []string{"20261017000006"}, "rolled back publication operation contract")
}
