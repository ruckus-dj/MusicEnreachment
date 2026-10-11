//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// Exercise application against the persisted evaluation header, not a mocked
// decision: the revision is the fence binding the automatic apply to its input.
func TestApplyCurrentMatchingEvaluationUsesPersistedUniqueDecisionPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, .95), fixture.candidate(fixture.memberB, .95),
	})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := repository.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist unique matching evaluation: %v", err)
	}
	header, _, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read persisted evaluation: header=%+v err=%v", header, err)
	}
	before := matchingApplyPersistedCounts(t, ctx, db, fixture.provider)
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision); err != nil {
		t.Fatalf("apply persisted unique evaluation: %v", err)
	}
	after := matchingApplyPersistedCounts(t, ctx, db, fixture.provider)
	if after.ProviderReleases == before.ProviderReleases || after.TrackSources == before.TrackSources {
		t.Fatalf("apply did not materialize provider release and source associations: before=%+v after=%+v", before, after)
	}
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, "stale-revision"); err == nil {
		t.Fatal("apply accepted a stale evaluation revision")
	}
	replayed := matchingApplyPersistedCounts(t, ctx, db, fixture.provider)
	if replayed != after {
		t.Fatalf("stale replay changed materialized rows: before=%+v after=%+v", after, replayed)
	}
}
