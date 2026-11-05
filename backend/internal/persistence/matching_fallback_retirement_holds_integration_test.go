//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
	"testing"
	"time"
)

func prepareAgedTerminalFallbackRetirementFixture(t *testing.T, f uniquePublicationApplyFixture) {
	t.Helper()
	ctx, db, repository := f.Context, f.Database, f.Repository
	parentID, childID := f.ParentOperationID, f.ChildOperation.ID
	completeFallbackParentDelivery(t, ctx, db, parentID)
	parentPast := time.Now().UTC().Add(-96 * time.Hour)
	childPast := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := db.NewRaw(`UPDATE operation SET finished_at=?,updated_at=? WHERE id=?`, parentPast, parentPast, parentID).Exec(ctx); err != nil {
		t.Fatalf("age completed fallback parent: %v", err)
	}
	jobID := *f.ChildOperation.RiverJobID
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
		t.Fatalf("claim schema-2 apply job: %v", err)
	}
	worker := jobs.NewMatchingApplyWorker(repository, service.NewOperations(repository))
	job := &river.Job[persistence.MatchingApplyOperationArgs]{JobRow: &rivertype.JobRow{ID: jobID, Attempt: 1}, Args: persistence.MatchingApplyOperationArgs{OperationID: childID}}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("run schema-2 apply worker: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE operation SET finished_at=?,updated_at=? WHERE id=?`, childPast, childPast, childID).Exec(ctx); err != nil {
		t.Fatalf("age completed apply child: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=? WHERE id=?`, childPast, jobID).Exec(ctx); err != nil {
		t.Fatalf("age completed apply child job: %v", err)
	}
	ageFallbackProviderDependencies(t, ctx, db, parentID, childPast)
}

func assertIndependentFallbackRetirementHolds(t *testing.T, ctx context.Context, db *bun.DB, parentID, childID uuid.UUID, cutoff time.Time, currentHeader, generationOwner bool) {
	t.Helper()
	eligibilityFacts := fallbackRetirementEligibilityFacts(t, ctx, db, parentID, cutoff)
	var facts map[string]bool
	if err := json.Unmarshal(eligibilityFacts, &facts); err != nil {
		t.Fatalf("decode fallback retirement eligibility facts: %v", err)
	}
	want := map[string]bool{
		"parent_ready": true, "acquisition_ready": true, "run_succeeded": true,
		"current_generation_owner": generationOwner, "live_evaluation": currentHeader,
		"live_apply": false, "live_dependency": false, "live_parent_job": false,
		"unconsumed_delivery": false,
	}
	for name, expected := range want {
		if facts[name] != expected {
			t.Fatalf("eligibility fact %s=%t, want %t (all facts: %s)", name, facts[name], expected, eligibilityFacts)
		}
	}
	var terminalChildJobs int
	if err := db.NewRaw(`SELECT count(*) FROM operation child JOIN river_job job ON job.id=child.river_job_id
		WHERE child.id=? AND child.state='succeeded' AND child.finished_at < ? AND job.state='completed'`, childID, cutoff).
		Scan(ctx, &terminalChildJobs); err != nil || terminalChildJobs != 1 {
		t.Fatalf("terminal apply child/job rows=%d err=%v, want one aged succeeded child with completed job", terminalChildJobs, err)
	}
}

func assertFallbackRetirementHighWater(t *testing.T, ctx context.Context, db *bun.DB, groupID uuid.UUID, before int64) {
	t.Helper()
	var after int64
	if err := db.NewRaw(`SELECT high_water FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(ctx, &after); err != nil || after != before {
		t.Fatalf("retirement changed generation high-water: before=%d after=%d err=%v", before, after, err)
	}
}

func fallbackRetirementProviderCacheCount(t *testing.T, ctx context.Context, db *bun.DB) int {
	t.Helper()
	var count int
	if err := db.NewRaw(`SELECT count(*) FROM provider_response_cache`).Scan(ctx, &count); err != nil {
		t.Fatalf("read provider response cache count: %v", err)
	}
	return count
}

func assertFallbackRetirementProviderCacheCount(t *testing.T, ctx context.Context, db *bun.DB, before int) {
	t.Helper()
	after := fallbackRetirementProviderCacheCount(t, ctx, db)
	if after != before {
		t.Fatalf("fallback retirement changed provider response cache count: before=%d after=%d", before, after)
	}
}
