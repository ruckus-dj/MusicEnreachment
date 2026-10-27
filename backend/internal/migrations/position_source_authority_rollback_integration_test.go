//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

func TestPositionSourceAuthorityRollbackRefusesLiveStatePostgreSQL(t *testing.T) {
	for _, scenario := range []string{"current authority", "retained authority attempt"} {
		t.Run(scenario, func(t *testing.T) {
			db := testpostgres.Open(t)
			testpostgres.Reset(t, db)
			ctx := context.Background()
			collection, err := migrations.Collection()
			if err != nil {
				t.Fatal(err)
			}
			migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
			if err := migrator.Init(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := migrator.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
			if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'authority rollback')`, releaseID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'authority rollback')`, trackID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "current authority":
				if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'Track.flac','{}','{}','{}','{}',1,now())`, positionID, uuid.New(), uuid.New()); err != nil {
					t.Fatal(err)
				}
				var epoch, generation uuid.UUID
				if err := db.NewRaw(`SELECT epoch,generation FROM track_publication WHERE position_id=?`, positionID).Scan(ctx, &epoch, &generation); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO position_source_authority(position_id,mode,revision,publication_epoch,publication_generation,source_location_id,media_variant_id) VALUES (?,'manual',?,?,?,?,?)`, positionID, uuid.New(), epoch, generation, uuid.New(), uuid.New()); err != nil {
					t.Fatal(err)
				}
			case "retained authority attempt":
				operationID := uuid.New()
				epoch := uuid.New()
				if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
					if _, err := tx.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,'publish_track_v1','queued','queued',jsonb_build_object('version',1,'position_id',?::text,'output_epoch',?::text,'intent_kind','audio'))`, operationID, positionID, epoch); err != nil {
						return err
					}
					_, err := tx.ExecContext(ctx, `INSERT INTO track_publication_attempt(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest) VALUES (?,?,?,?,'audio','queued','{"source_authority_mode":"manual"}','{}')`, operationID, positionID, epoch, uuid.New())
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := migrator.Rollback(ctx); err == nil {
				t.Fatal("rollback discarded live source authority")
			}
			var retained bool
			if err := db.NewRaw(`SELECT to_regclass('public.position_source_authority') IS NOT NULL`).Scan(ctx, &retained); err != nil || !retained {
				t.Fatalf("authority table after refused rollback = %t, %v", retained, err)
			}
		})
	}
}
