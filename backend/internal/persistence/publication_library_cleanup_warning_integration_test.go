//go:build integration

package persistence

import (
	"testing"

	"github.com/google/uuid"
)

func TestReadLibraryPublicationContextProjectsRetainedCleanupRetryStatePostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	var releaseID uuid.UUID
	if err := f.fixture.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, f.fixture.guard.ExpectedPositionID).Scan(f.ctx, &releaseID); err != nil {
		t.Fatal(err)
	}
	read := func() *LibraryPublicationContext {
		t.Helper()
		ctx, err := f.repository.ReadLibraryPublicationContext(f.ctx, releaseID, f.fixture.guard.ExpectedPositionID)
		if err != nil {
			t.Fatal(err)
		}
		return ctx
	}
	assertWarning := func(warnings []LibraryPublicationSourceCleanupWarning, wantRetry bool, wantActive uuid.UUID) {
		t.Helper()
		if len(warnings) != 1 {
			t.Fatalf("cleanup warnings=%+v, want exactly one retained owner", warnings)
		}
		got := warnings[0]
		if got.OriginalOperationID != f.originalID || got.SafeError == "" || got.CanRetry != wantRetry {
			t.Fatalf("cleanup warning=%+v, want original=%s retry=%t", got, f.originalID, wantRetry)
		}
		if wantActive == uuid.Nil {
			if got.ActiveRetryOperationID != nil {
				t.Fatalf("active retry=%s, want none", *got.ActiveRetryOperationID)
			}
		} else if got.ActiveRetryOperationID == nil || *got.ActiveRetryOperationID != wantActive {
			t.Fatalf("active retry=%v, want %s", got.ActiveRetryOperationID, wantActive)
		}
	}

	// This fixture has a genuinely failed, owner-returned cleanup and a claimed
	// retry; the warning must represent the persisted retry, not infer eligibility.
	active := read()
	assertWarning(active.SourceCleanupWarnings, false, f.newGuard.OperationID)

	// Failed owner return is separately proven by the delivery failure transition.
	failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, f.newGuard, "retry removal outcome is unknown")
	if err != nil || !failed {
		t.Fatalf("return retry owner=%t,%v", failed, err)
	}
	returned := read()
	assertWarning(returned.SourceCleanupWarnings, true, uuid.Nil)
	// A later retry pins CURRENT from its predecessor, not its own delivery.
	replacement, err := f.repository.ReplacePublicationSourceCleanupExplicitRetry(f.ctx, f.originalID, f.newGuard.OperationID, new(publicationSourceCleanupTestInserter))
	if err != nil || replacement.OperationID == uuid.Nil {
		t.Fatalf("replace returned recovery=%+v,%v", replacement, err)
	}
	successor, _ := claimReplacementRetryForTest(t, f, replacement)
	if failed, err := f.repository.FailPublicationSourceCleanupExplicitRetryDelivery(f.ctx, successor, "successor owner returned"); err != nil || !failed {
		t.Fatalf("return frontier-backed owner=%t,%v", failed, err)
	}
	assertWarning(read().SourceCleanupWarnings, true, uuid.Nil)
	if _, err := f.fixture.db.NewRaw(`UPDATE app_setting SET setting_value='false' WHERE setting_name='publication_remove_sources'`).Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
	assertWarning(read().SourceCleanupWarnings, false, uuid.Nil)
}

func TestReadLibraryPublicationContextClearsSettledCleanupWarningPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newRetryResolutionFixture(t)
	var releaseID uuid.UUID
	if err := f.fixture.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, f.fixture.guard.ExpectedPositionID).Scan(f.ctx, &releaseID); err != nil {
		t.Fatal(err)
	}
	before, err := f.repository.ReadLibraryPublicationContext(f.ctx, releaseID, f.fixture.guard.ExpectedPositionID)
	if err != nil || len(before.SourceCleanupWarnings) != 1 {
		t.Fatalf("active retry warnings=%+v,%v", before, err)
	}
	// Resolve the live retry, not a delivery already fenced by owner return.
	resolveAllRetryItems(t, f)
	settled, err := f.repository.SettlePublicationSourceCleanupExplicitRetry(f.ctx, f.newGuard)
	if err != nil || !settled {
		t.Fatalf("settle resolved retry=%t,%v", settled, err)
	}
	after, err := f.repository.ReadLibraryPublicationContext(f.ctx, releaseID, f.fixture.guard.ExpectedPositionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.SourceCleanupWarnings) != 0 {
		t.Fatalf("cleanup warnings after successful resolution=%+v, want none", after.SourceCleanupWarnings)
	}
}

func TestReadLibraryPublicationContextProjectsQueuedCleanupRetryAsNonRetryablePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx, fixture, repository, _, originalID := failedPublicationSourceCleanupForRetry(t)
	var releaseID uuid.UUID
	if err := fixture.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, fixture.guard.ExpectedPositionID).Scan(ctx, &releaseID); err != nil {
		t.Fatal(err)
	}
	read := func() *LibraryPublicationContext {
		t.Helper()
		result, err := repository.ReadLibraryPublicationContext(ctx, releaseID, fixture.guard.ExpectedPositionID)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	queued, err := repository.AdmitPublicationSourceCleanupExplicitRetry(ctx, originalID, new(publicationSourceCleanupTestInserter))
	if err != nil || queued.OperationID == uuid.Nil || len(queued.Blockers) != 0 {
		t.Fatalf("admit cleanup retry=%+v,%v", queued, err)
	}
	warnings := read().SourceCleanupWarnings
	if len(warnings) != 1 || warnings[0].OriginalOperationID != originalID || warnings[0].SafeError == "" || warnings[0].CanRetry || warnings[0].ActiveRetryOperationID == nil || *warnings[0].ActiveRetryOperationID != queued.OperationID {
		t.Fatalf("queued retry warning=%+v, want retained non-retryable warning with active retry %s", warnings, queued.OperationID)
	}
}
