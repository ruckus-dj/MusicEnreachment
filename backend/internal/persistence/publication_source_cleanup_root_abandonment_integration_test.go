//go:build integration

package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestPublicationSourceCleanupRootAbandonmentPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	preservePublicationRemoveSourcesSetting(t, db)
	f := returnFailedRetryForReplacementWithDatabase(t, db, uuid.NewString())

	// Prove the normal writer persisted a real consumed retry with an unresolved
	// frontier before exercising root deletion.
	var incomplete int
	if err := f.fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_explicit_retry_resolution r
		JOIN source_cleanup_explicit_retry_item i ON i.retry_id=r.retry_id AND i.item_id=r.item_id
		WHERE i.original_operation_id=? AND r.completed_at IS NULL AND r.resolution_kind IS NULL`, f.originalID).Scan(ctx, &incomplete); err != nil || incomplete != 1 {
		t.Fatalf("persisted incomplete retry resolution=%d,%v; want one", incomplete, err)
	}

	// Resolution rows are immutable without their completion/abandonment authority.
	_, err := f.fixture.db.ExecContext(ctx, `DELETE FROM source_cleanup_explicit_retry_resolution WHERE retry_id IN
		(SELECT id FROM source_cleanup_explicit_retry WHERE original_operation_id=?)`, f.originalID)
	if err == nil || !strings.Contains(err.Error(), "source cleanup") {
		t.Fatalf("raw retry-resolution deletion error=%v; want authority rejection", err)
	}

	var rootID uuid.UUID
	if err := f.fixture.db.NewRaw(`SELECT source_root_id FROM source_cleanup_owner WHERE operation_id=? LIMIT 1`, f.originalID).Scan(ctx, &rootID); err != nil {
		t.Fatal(err)
	}
	var configuredPath string
	if err := f.fixture.db.NewRaw(`SELECT configured_path FROM source_root WHERE id=?`, rootID).Scan(ctx, &configuredPath); err != nil {
		t.Fatal(err)
	}
	repository := NewSourceInventoryRepository(f.fixture.db)
	rootLocations, err := repository.CountSourceLocations(ctx, rootID)
	if err != nil {
		t.Fatalf("count source locations before root deletion: %v", err)
	}
	// The fixture owns active River workers. The production failure transitions
	// deliberately retain their durable jobs, so finalize those exact deliveries
	// before root deletion without rewriting their durable kind or arguments.
	var originalJobID int64
	if err := f.fixture.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, f.originalID).Scan(ctx, &originalJobID); err != nil {
		t.Fatalf("read original cleanup River job identity: %v", err)
	}
	for _, delivery := range []struct {
		operationID uuid.UUID
		jobID       int64
	}{{f.originalID, originalJobID}, {f.newGuard.OperationID, f.newGuard.RiverJobID}} {
		result, err := f.fixture.db.NewRaw(`UPDATE river_job
			SET state='cancelled',finalized_at=now()
			WHERE id=? AND args->>'operation_id'=? AND finalized_at IS NULL`, delivery.jobID, delivery.operationID.String()).Exec(ctx)
		if err != nil {
			t.Fatalf("finalize cleanup River delivery %d: %v", delivery.jobID, err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			t.Fatalf("finalized cleanup River delivery %d rows=%d,%v; want exactly one", delivery.jobID, affected, err)
		}
	}

	publicationBefore := rootAbandonmentPublicationEvidence(t, f)
	callbackErr := errors.New("injected cleanup failure")
	if err := repository.DeleteSourceRootWithCleanup(ctx, rootID, configuredPath, rootLocations, func(context.Context, string, []SourceAnalysisArtifact) error { return callbackErr }); !errors.Is(err, callbackErr) {
		t.Fatalf("failed cleanup callback error=%v; want injected error", err)
	}
	var receipts int
	if err := f.fixture.db.NewRaw(`SELECT count(*) FROM source_cleanup_root_abandonment WHERE original_operation_id=?`, f.originalID).Scan(ctx, &receipts); err != nil || receipts != 0 {
		t.Fatalf("abandonment receipts after callback rollback=%d,%v; want zero", receipts, err)
	}

	if err := repository.DeleteSourceRootWithCleanup(ctx, rootID, configuredPath, rootLocations, nil); err != nil {
		t.Fatalf("delete abandoned failed-cleanup root: %v", err)
	}
	var preserved int
	if err := f.fixture.db.NewRaw(`SELECT
		(SELECT count(*) FROM source_cleanup_root_abandonment WHERE original_operation_id=? AND root_id=? AND evidence IS NOT NULL)
		+ (SELECT count(*) FROM source_cleanup_owner WHERE operation_id=? AND source_root_id=?)
		+ (SELECT count(*) FROM source_cleanup_explicit_retry WHERE original_operation_id=?)
		+ (SELECT count(*) FROM source_root WHERE id=?)`, f.originalID, rootID, f.originalID, rootID, f.originalID, rootID).Scan(ctx, &preserved); err != nil || preserved != 1 {
		t.Fatalf("receipt plus removed graph/root count=%d,%v; want archived receipt only", preserved, err)
	}
	publicationAfter := rootAbandonmentPublicationEvidence(t, f)
	if publicationAfter != publicationBefore {
		t.Fatalf("root abandonment changed CURRENT/library/namespace evidence:\nbefore %s\nafter  %s", publicationBefore, publicationAfter)
	}
}

func rootAbandonmentPublicationEvidence(t *testing.T, f retryResolutionFixture) string {
	t.Helper()
	var evidence string
	err := f.fixture.db.NewRaw(`SELECT jsonb_build_object(
		'current', COALESCE((SELECT to_jsonb(p) FROM track_publication p WHERE p.position_id=?), 'null'::jsonb),
		'library', COALESCE((SELECT to_jsonb(rt) FROM release_track rt WHERE rt.id=?), 'null'::jsonb),
		'namespace_claims', COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id)
			FROM publication_namespace_claim c
			WHERE c.epoch=? AND (c.owner_position_id=? OR EXISTS (
				SELECT 1 FROM publication_namespace_reservation r
				WHERE r.namespace_claim_id=c.id AND r.position_id=?
			))), '[]'::jsonb),
		'namespace_reservations', COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.namespace_claim_id,r.generation)
			FROM publication_namespace_reservation r WHERE r.position_id=?), '[]'::jsonb)
	)::text`, f.newGuard.PositionID, f.newGuard.PositionID, f.newGuard.Epoch,
		f.newGuard.PositionID, f.newGuard.PositionID, f.newGuard.PositionID).Scan(f.ctx, &evidence)
	if err != nil {
		t.Fatalf("capture CURRENT/library/namespace evidence: %v", err)
	}
	return evidence
}
