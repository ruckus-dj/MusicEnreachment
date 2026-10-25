//go:build integration

package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/uptrace/bun"
)

func TestPublicationAudioUpgradeContinuesObservedScopesPostgreSQL(t *testing.T) {
	t.Parallel()
	repo, ctx, epoch, position := reconciliationDeliveryFixture(t)
	reconciliationDeliveryRequest(t, repo, ctx, epoch, position, []string{"audio", "metadata"})
	inserter := new(reconciliationDeliveryInserter)
	if n, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || n != 1 {
		t.Fatalf("dispatch=%d err=%v", n, err)
	}
	opID, jobID := inserter.args[0].OperationID, inserter.ids[0]
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	guard, outcome, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, jobID, 1)
	if err != nil || outcome != PublicationReconciliationClaimed {
		t.Fatalf("claim=%s err=%v", outcome, err)
	}
	reconciliationDeliveryRequest(t, repo, ctx, epoch, position, []string{"artwork"})
	if err := repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := repo.continuePublicationAudioReconciliationWith(ctx, tx, guard); err != nil {
			return err
		}
		acknowledged, err := repo.AcknowledgePublicationReconciliationDeliveryWith(ctx, tx, guard)
		if err != nil {
			return err
		}
		if !acknowledged {
			return ErrPublicationAdmissionConflict
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var requested int64
	if err := repo.db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &requested); err != nil {
		t.Fatal(err)
	}
	var dirty []PublicationReconciliationDirtyScope
	if err := repo.db.NewRaw(`SELECT scope,generation FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=? ORDER BY scope`, epoch, position).Scan(ctx, &dirty); err != nil {
		t.Fatal(err)
	}
	if requested != 3 || len(dirty) != 3 || dirty[0].Scope != "artwork" || dirty[0].Generation != 2 || dirty[1].Scope != "audio" || dirty[1].Generation != 3 || dirty[2].Scope != "metadata" || dirty[2].Generation != 3 {
		t.Fatalf("requested generation=%d remaining dirty scopes=%+v", requested, dirty)
	}
}

// This covers the continuation/acknowledgement helper transaction, not a full
// publication admission or River enqueue rollback.
func TestPublicationAudioUpgradeContinuationAndAcknowledgementRollbackPostgreSQL(t *testing.T) {
	t.Parallel()
	repo, ctx, epoch, position := reconciliationDeliveryFixture(t)
	reconciliationDeliveryRequest(t, repo, ctx, epoch, position, []string{"audio", "metadata"})
	inserter := new(reconciliationDeliveryInserter)
	if n, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || n != 1 {
		t.Fatalf("dispatch=%d err=%v", n, err)
	}
	opID, jobID := inserter.args[0].OperationID, inserter.ids[0]
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	guard, outcome, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, jobID, 1)
	if err != nil || outcome != PublicationReconciliationClaimed {
		t.Fatalf("claim=%s err=%v", outcome, err)
	}
	err = repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := repo.continuePublicationAudioReconciliationWith(ctx, tx, guard); err != nil {
			return err
		}
		acknowledged, err := repo.AcknowledgePublicationReconciliationDeliveryWith(ctx, tx, guard)
		if err != nil || !acknowledged {
			return errors.Join(err, ErrPublicationAdmissionConflict)
		}
		return errors.New("abort continuation and acknowledgement transaction")
	})
	if err == nil {
		t.Fatal("expected transaction failure")
	}
	var requested int64
	var dirty, jobs int
	if err := repo.db.NewRaw(`SELECT requested_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &requested); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=?`, epoch, position).Scan(ctx, &dirty); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if requested != 1 || dirty != 2 || jobs != 1 {
		t.Fatalf("rollback state requested=%d dirty=%d jobs=%d", requested, dirty, jobs)
	}
}
