//go:build integration

package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type publicationResetFixture struct {
	db        *bun.DB
	oldRoot   string
	newRoot   string
	epoch     uuid.UUID
	position  uuid.UUID
	operation uuid.UUID
	job       int64
}

func newPublicationResetFixture(t *testing.T) publicationResetFixture {
	t.Helper()
	ctx := context.Background()
	f := publicationResetFixture{db: testpostgres.OpenMigrated(t), position: uuid.New(), operation: uuid.New()}
	initializeRiver(t, ctx, f.db)
	f.oldRoot, f.newRoot = nativeTestPath(t, "/music/old"), nativeTestPath(t, "/music/new")
	if err := NewSettingsRepository(f.db).Set(ctx, "output_directory", f.oldRoot); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton`).Scan(ctx, &f.epoch); err != nil {
		t.Fatal(err)
	}
	releaseID, trackID := uuid.New(), uuid.New()
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,'release')`, []any{releaseID}},
		{`INSERT INTO local_track(id,title) VALUES (?,'track')`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, []any{f.position, releaseID, trackID}},
		{`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'track.flac','{}','{}','{}','{}',1,now())`, []any{f.position, f.epoch, uuid.New()}},
	} {
		if _, err := f.db.NewRaw(seed.query, seed.args...).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.NewRaw(`INSERT INTO river_job(kind,args) VALUES ('publication_reset_probe','{}') RETURNING id`).Scan(ctx, &f.job); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,river_job_id,finished_at)
		VALUES (?,'matching_evaluation_v1','succeeded','complete','{}',?,now())`, f.operation, f.job).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f publicationResetFixture) reset() (uuid.UUID, error) {
	return NewSetupManagerRepository(f.db).RunOutputResetRequest(context.Background(), OutputResetRequest{
		ExpectedOldRoot: f.oldRoot, NewRoot: f.newRoot,
		Prepare: func(context.Context, func(OutputResetDirectory) error) error { return nil },
		Finish:  func(context.Context, OutputResetJournal) error { return nil },
	})
}

func (f publicationResetFixture) assertUnchanged(t *testing.T, checkEpoch bool) {
	t.Helper()
	ctx := context.Background()
	var root string
	if err := f.db.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='output_directory'`).Scan(ctx, &root); err != nil || root != f.oldRoot {
		t.Fatalf("blocked reset root = %q, %v", root, err)
	}
	var publications int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id=?`, f.position).Scan(ctx, &publications); err != nil || publications != 1 {
		t.Fatalf("blocked reset publications = %d, %v", publications, err)
	}
	if checkEpoch {
		var epoch uuid.UUID
		if err := f.db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton`).Scan(ctx, &epoch); err != nil || epoch != f.epoch {
			t.Fatalf("blocked reset epoch = %s, %v", epoch, err)
		}
	}
}

func TestOutputResetBlocksTerminalPublicationClaimsPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"executing", "recovery_required", "quiescent"} {
		t.Run(state, func(t *testing.T) {
			f := newPublicationResetFixture(t)
			if _, err := f.db.NewRaw(`INSERT INTO publication_execution_claim
				(operation_id,application_attempt,river_job_id,river_attempt,claim_uuid,boot_uuid,delivery_token,epoch,state)
				VALUES (?,1,?,1,?,?,'delivery',?,?)`, f.operation, f.job, uuid.New(), uuid.New(), f.epoch, state).Exec(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := f.reset(); err == nil {
				t.Fatal("reset ignored durable claim on completed operation")
			}
			f.assertUnchanged(t, true)
		})
	}
}

func TestOutputResetSourceCleanupEligibilityPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"claimed", "recovery_required", "queued", "retry_pending"} {
		t.Run(state, func(t *testing.T) {
			f := newPublicationResetFixture(t)
			itemID := uuid.New()
			if _, err := f.db.NewRaw(`INSERT INTO source_cleanup_item
				(id,operation_id,position_id,epoch,publication_generation,state,source_snapshot,borrower_revision)
				VALUES (?,?,?,?,?,?,'{}',?)`, itemID, f.operation, f.position, f.epoch, uuid.New(), state, uuid.New()).Exec(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, err := f.reset()
			if state == "claimed" || state == "recovery_required" {
				if err == nil {
					t.Fatal("reset ignored active cleanup")
				}
				f.assertUnchanged(t, true)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got string
			var snapshotCleared, revisionCleared bool
			if err := f.db.NewRaw(`SELECT state,source_snapshot IS NULL,borrower_revision IS NULL FROM source_cleanup_item WHERE id=?`, itemID).Scan(context.Background(), &got, &snapshotCleared, &revisionCleared); err != nil || got != "cancelled" || !snapshotCleared || !revisionCleared {
				t.Fatalf("cleanup after reset = %q %t %t, %v", got, snapshotCleared, revisionCleared, err)
			}
		})
	}
}

func TestOutputResetMissingPublicationSingletonRollsBackPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationResetFixture(t)
	if _, err := f.db.NewRaw(`DELETE FROM publication_namespace_state`).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reset(); err == nil || !strings.Contains(err.Error(), "publication namespace state is missing") {
		t.Fatalf("missing namespace reset error = %v", err)
	}
	f.assertUnchanged(t, false)
}
