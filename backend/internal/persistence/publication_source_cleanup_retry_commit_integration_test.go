//go:build integration

package persistence

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAdmitPublicationSourceCleanupExplicitRetryRollsBackCommitFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, _, originalOperationID := failedPublicationSourceCleanupForRetry(t)
	before := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID)
	var originalJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, originalOperationID).Scan(ctx, &originalJobID); err != nil {
		t.Fatal(err)
	}
	var priorJobs int
	if err := fixture.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='retry_publication_sources_v1'`).Scan(ctx, &priorJobs); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, `
		CREATE FUNCTION reject_retry_commit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.kind='retry_publication_sources_v1' THEN RAISE EXCEPTION 'injected retry commit failure'; END IF;
			RETURN NEW;
		END $$;
		CREATE CONSTRAINT TRIGGER reject_retry_commit AFTER INSERT ON operation
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_retry_commit()`); err != nil {
		t.Fatal(err)
	}
	inserter := new(publicationSourceCleanupTestInserter)
	result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, inserter)
	if err == nil || !strings.Contains(err.Error(), "injected retry commit failure") || result.OperationID != uuid.Nil || len(result.Blockers) != 0 || inserter.args == nil {
		t.Fatalf("deferred commit failure result=%+v err=%v inserted args=%#v", result, err, inserter.args)
	}
	if got := cleanupRetryRetainedEvidence(t, ctx, fixture, originalOperationID); got != before {
		t.Fatalf("commit failure changed retained evidence:\nbefore %s\nafter  %s", before, got)
	}
	var jobs, operations, grants, children int
	for query, target := range map[string]*int{
		`SELECT count(*) FROM river_job WHERE kind='retry_publication_sources_v1'`: &jobs,
		`SELECT count(*) FROM operation WHERE kind='retry_publication_sources_v1'`: &operations,
		`SELECT count(*) FROM source_cleanup_explicit_retry`:                       &grants,
		`SELECT count(*) FROM source_cleanup_explicit_retry_item`:                  &children,
	} {
		if err := fixture.db.NewRaw(query).Scan(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	var currentJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, originalOperationID).Scan(ctx, &currentJobID); err != nil {
		t.Fatal(err)
	}
	if jobs != priorJobs || operations != 0 || grants != 0 || children != 0 || currentJobID != originalJobID {
		t.Fatalf("rollback residue jobs=%d/%d operations=%d grants=%d children=%d original job=%d/%d", jobs, priorJobs, operations, grants, children, currentJobID, originalJobID)
	}
}

func TestAdmitPublicationSourceCleanupExplicitRetryRetainsOldRiverIdentityAfterJobDeletionPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, _, originalOperationID := failedPublicationSourceCleanupForRetry(t)
	var oldJobID int64
	var oldAttempt int
	if err := fixture.db.NewRaw(`SELECT o.river_job_id,c.river_attempt FROM operation o JOIN publication_execution_claim c ON c.operation_id=o.id WHERE o.id=?`, originalOperationID).Scan(ctx, &oldJobID, &oldAttempt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, oldJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, `DELETE FROM river_job WHERE id=?`, oldJobID); err != nil {
		t.Fatal(err)
	}
	result, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalOperationID, new(publicationSourceCleanupTestInserter))
	if err != nil || result.OperationID == uuid.Nil || len(result.Blockers) != 0 {
		t.Fatalf("admission after old River row deletion=%+v err=%v", result, err)
	}
	var boundJobID int64
	var boundAttempt int
	var kind string
	if err := fixture.db.NewRaw(`SELECT original_river_job_id,(binding->>'original_river_attempt')::integer,binding->>'original_river_kind' FROM source_cleanup_explicit_retry WHERE recovery_operation_id=?`, result.OperationID).Scan(ctx, &boundJobID, &boundAttempt, &kind); err != nil {
		t.Fatal(err)
	}
	if boundJobID != oldJobID || boundAttempt != oldAttempt || kind != "cleanup_publication_sources_v1" {
		t.Fatalf("retained old delivery binding job=%d attempt=%d kind=%q; want %d %d cleanup_publication_sources_v1", boundJobID, boundAttempt, kind, oldJobID, oldAttempt)
	}
	var stillOriginalJobID int64
	if err := fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, originalOperationID).Scan(ctx, &stillOriginalJobID); err != nil {
		t.Fatal(err)
	}
	if stillOriginalJobID != oldJobID {
		t.Fatalf("original operation job changed from %d to %d", oldJobID, stillOriginalJobID)
	}
}
