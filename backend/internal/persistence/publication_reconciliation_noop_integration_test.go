//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// This covers the provider-free local projection no-op path only. It does not
// exercise automatic quality policy or provider reconciliation.
func TestPublicationReconciliationLocalNoopAcknowledgementPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if committed, err := repo.CommitPublicationResult(ctx, f.guard, publicationResultForFixture(f)); err != nil || !committed {
		t.Fatalf("establish current publication = %v, %v", committed, err)
	}
	position := f.guard.ExpectedPositionID
	var release uuid.UUID
	var generation uuid.UUID
	if err := f.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, position).Scan(ctx, &release); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, position).Scan(ctx, &generation); err != nil {
		t.Fatal(err)
	}
	seedLocalNoopAuthority(t, ctx, f.db, f.guard.Epoch, position, generation)
	guard, _ := startPublicationReconciliationForResult(t, ctx, repo, f.guard.Epoch, position)
	decision, err := repo.EvaluatePublicationReconciliationCurrent(ctx, release, guard, "", "")
	if err != nil || decision.Action != AllNoop || decision.Noop == nil {
		t.Fatalf("baseline local-only evaluation = action %v witness %v, err %v", decision.Action, decision.Noop != nil, err)
	}
	acked, err := repo.AcknowledgePublicationReconciliationNoop(ctx, guard, decision.Noop)
	if err != nil || !acked {
		t.Fatalf("acknowledge local-only no-op = %v, %v", acked, err)
	}
	assertNoopProjectionAndAttemptUnchanged(t, ctx, f.db, position, generation, f.guard.OperationID)
	var dirty int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, guard.OutputEpoch, position).Scan(ctx, &dirty); err != nil || dirty != 0 {
		t.Fatalf("dirty scope count after acknowledgement = %d, %v", dirty, err)
	}
}

func TestPublicationReconciliationLocalNoopRetainsNewerGenerationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if committed, err := repo.CommitPublicationResult(ctx, f.guard, publicationResultForFixture(f)); err != nil || !committed {
		t.Fatalf("establish current publication = %v, %v", committed, err)
	}
	position := f.guard.ExpectedPositionID
	var release uuid.UUID
	if err := f.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, position).Scan(ctx, &release); err != nil {
		t.Fatal(err)
	}
	var generation uuid.UUID
	if err := f.db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, position).Scan(ctx, &generation); err != nil {
		t.Fatal(err)
	}
	seedLocalNoopAuthority(t, ctx, f.db, f.guard.Epoch, position, generation)
	guard, _ := startPublicationReconciliationForResult(t, ctx, repo, f.guard.Epoch, position)
	decision, err := repo.EvaluatePublicationReconciliationCurrent(ctx, release, guard, "", "")
	if err != nil || decision.Action != AllNoop || decision.Noop == nil {
		t.Fatalf("baseline local-only evaluation = action %v witness %v, err %v", decision.Action, decision.Noop != nil, err)
	}
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.RequestPublicationReconciliationWith(ctx, tx, guard.OutputEpoch, []uuid.UUID{position}, []string{"newer_local_scope"})
	}); err != nil {
		t.Fatal(err)
	}
	acked, err := repo.AcknowledgePublicationReconciliationNoop(ctx, guard, decision.Noop)
	if err != nil || !acked {
		t.Fatalf("acknowledge prior generation = %v, %v", acked, err)
	}
	var requested int64
	var dirty, pending int
	if err := f.db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, guard.OutputEpoch, position).Scan(ctx, &requested); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, guard.OutputEpoch, position).Scan(ctx, &dirty); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_event WHERE output_epoch=? AND position_id=? AND state='pending'`, guard.OutputEpoch, position).Scan(ctx, &pending); err != nil {
		t.Fatal(err)
	}
	if requested != guard.ObservedGeneration+1 || dirty != 1 || pending != 1 {
		t.Fatalf("newer request generation/dirty/pending = %d/%d/%d", requested, dirty, pending)
	}
}

func TestPublicationReconciliationNoopIgnoresUnrelatedProviderBindingPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if committed, err := repo.CommitPublicationResult(ctx, f.guard, publicationResultForFixture(f)); err != nil || !committed {
		t.Fatalf("establish CURRENT: committed=%v err=%v", committed, err)
	}
	var release uuid.UUID
	if err := f.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &release); err != nil {
		t.Fatal(err)
	}
	seedLocalNoopAuthority(t, ctx, f.db, f.guard.Epoch, f.guard.ExpectedPositionID, f.guard.CandidateGeneration)
	providerID, providerReleaseID := uuid.New(), uuid.New()
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO provider(id,code) VALUES (?,'unrelated-provider')`, []any{providerID}},
		{`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'Unrelated')`, []any{providerReleaseID, providerID, uuid.NewString()}},
		{`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`, []any{providerID, providerReleaseID, release}},
	} {
		if _, err := f.db.ExecContext(ctx, seed.query, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	guard, _ := startPublicationReconciliationForResult(t, ctx, repo, f.guard.Epoch, f.guard.ExpectedPositionID)
	decision, err := repo.EvaluatePublicationReconciliationCurrent(ctx, release, guard, "", "")
	if err != nil || decision.Action != AllNoop || decision.Noop == nil {
		t.Fatalf("unrelated provider was treated as MusicBrainz authority: decision=%+v err=%v", decision, err)
	}
	if acknowledged, err := repo.AcknowledgePublicationReconciliationNoop(ctx, guard, decision.Noop); err != nil || !acknowledged {
		t.Fatalf("acknowledge with unrelated binding: acknowledged=%v err=%v", acknowledged, err)
	}
}

func TestPublicationReconciliationLocalNoopRejectsChangedWitnessPostgreSQL(t *testing.T) {
	for _, change := range []string{"metadata", "source_authority", "missing_authority", "format", "current", "claim"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := preparePublicationResultFixture(t)
			repo := NewSetupManagerRepository(f.db)
			if committed, err := repo.CommitPublicationResult(ctx, f.guard, publicationResultForFixture(f)); err != nil || !committed {
				t.Fatalf("establish CURRENT: committed=%v err=%v", committed, err)
			}
			position := f.guard.ExpectedPositionID
			var release uuid.UUID
			if err := f.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, position).Scan(ctx, &release); err != nil {
				t.Fatal(err)
			}
			seedLocalNoopAuthority(t, ctx, f.db, f.guard.Epoch, position, f.guard.CandidateGeneration)
			guard, _ := startPublicationReconciliationForResult(t, ctx, repo, f.guard.Epoch, position)
			decision, err := repo.EvaluatePublicationReconciliationCurrent(ctx, release, guard, "", "")
			if err != nil || decision.Action != AllNoop || decision.Noop == nil {
				t.Fatalf("valid no-op baseline: decision=%+v err=%v", decision, err)
			}
			switch change {
			case "metadata":
				_, err = f.db.ExecContext(ctx, `UPDATE local_track SET metadata='{"note":true}'::jsonb,metadata_revision=metadata_revision+1 WHERE id=(SELECT track_id FROM release_track WHERE id=?)`, position)
			case "source_authority":
				_, err = f.db.ExecContext(ctx, `UPDATE position_source_authority SET revision=? WHERE position_id=?`, uuid.New(), position)
			case "missing_authority":
				_, err = f.db.ExecContext(ctx, `DELETE FROM position_source_authority WHERE position_id=?`, position)
			case "format":
				_, err = f.db.ExecContext(ctx, `UPDATE app_setting SET setting_value='mka' WHERE setting_name='publication_format'`)
			case "current":
				_, err = f.db.ExecContext(ctx, `UPDATE track_publication SET actual_metadata=jsonb_set(actual_metadata,'{tags,TITLE}','["changed"]'::jsonb) WHERE position_id=?`, position)
			case "claim":
				guard.ClaimToken = uuid.New()
			}
			if err != nil {
				t.Fatal(err)
			}
			if acknowledged, err := repo.AcknowledgePublicationReconciliationNoop(ctx, guard, decision.Noop); acknowledged {
				t.Fatalf("changed %s acknowledged: err=%v", change, err)
			}
			var dirty int
			var eventState string
			if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, guard.OutputEpoch, position).Scan(ctx, &dirty); err != nil {
				t.Fatal(err)
			}
			if err := f.db.NewRaw(`SELECT state FROM publication_reconciliation_event WHERE operation_id=?`, guard.OperationID).Scan(ctx, &eventState); err != nil {
				t.Fatal(err)
			}
			if dirty != 1 || eventState != "dispatched" {
				t.Fatalf("stale witness lost its obligation: dirty=%d event=%s", dirty, eventState)
			}
		})
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

func assertNoopProjectionAndAttemptUnchanged(t *testing.T, ctx context.Context, db *bun.DB, position, generation, operation uuid.UUID) {
	t.Helper()
	var current, attempt, extraJobs int
	if err := db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id=? AND generation=?`, position, generation).Scan(ctx, &current); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, operation).Scan(ctx, &attempt); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='publish_track_v1'`).Scan(ctx, &extraJobs); err != nil {
		t.Fatal(err)
	}
	if current != 1 || attempt != 0 || extraJobs != 1 {
		t.Fatalf("current=%d attempt=%d publication jobs=%d", current, attempt, extraJobs)
	}
}
