//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestIncomingGroupingReadinessUsesDurableAnalysisState(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingGroupingRepository(db)

	var bindings int
	if err := db.NewRaw(`SELECT count(*) FROM source_analysis_work_artifact_binding WHERE work_id IN (?, ?)`, fixture.workA, fixture.workB).Scan(ctx, &bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("settled metadata fixture retained %d ephemeral bindings", bindings)
	}
	assertIncomingReadiness(t, ctx, repository, fixture.workA, true)

	// A rerun can leave the previous successful metadata result in place while
	// the current durable step state is pending or failed. Neither is ready.
	for _, test := range []struct {
		name      string
		state     string
		safeError string
	}{
		{name: "pending rerun", state: "pending"},
		{name: "failed rerun", state: "failed", safeError: "analysis failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.NewRaw(`UPDATE source_analysis_step SET state=?, safe_error=? WHERE work_id=? AND step='metadata'`, test.state, nullableTestString(test.safeError), fixture.workA).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			assertIncomingReadiness(t, ctx, repository, fixture.workA, false)
		})
		if _, err := db.NewRaw(`UPDATE source_analysis_step SET state='succeeded', safe_error=NULL WHERE work_id=? AND step='metadata'`, fixture.workA).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.NewRaw(`INSERT INTO source_analysis_step(work_id,step,state) VALUES(?,'sha256','pending')`, fixture.workA).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertIncomingReadiness(t, ctx, repository, fixture.workA, false)
	if _, err := db.NewRaw(`DELETE FROM source_analysis_step WHERE work_id=? AND step='sha256'`, fixture.workA).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	// In-place operation output has the same durable readiness contract and
	// must not depend on a materialized artifact binding either.
	if _, err := db.NewRaw(`UPDATE source_root SET processing_mode='in_place' WHERE id=(SELECT source_root_id FROM source_analysis_work WHERE id=?)`, fixture.workA).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertIncomingReadiness(t, ctx, repository, fixture.workA, true)
}

func assertIncomingReadiness(t *testing.T, ctx context.Context, repository *persistence.IncomingGroupingRepository, workID uuid.UUID, want bool) {
	t.Helper()
	captures, err := repository.CurrentCaptures(ctx)
	if err != nil {
		t.Fatalf("read incoming captures: %v", err)
	}
	for _, capture := range captures {
		if capture.WorkID == workID && capture.Ready == want {
			return
		}
	}
	t.Fatalf("work %v readiness not found as %v in captures %+v", workID, want, captures)
}

func nullableTestString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
