//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestApplyCurrentMatchingEvaluationRejectsEpochOnlyInvalidationPostgreSQL(t *testing.T) {
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
	header, candidates, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read evaluation header: header=%+v err=%v", header, err)
	}
	before := matchingApplyPersistedCounts(t, ctx, db, fixture.provider)

	// The group rows deliberately stay unchanged: the epoch alone is the
	// authoritative invalidation fence for source/grouping changes.
	if _, err := db.ExecContext(ctx, `UPDATE incoming_grouping_state SET revision=revision+1 WHERE singleton=true`); err != nil {
		t.Fatalf("invalidate grouping epoch: %v", err)
	}
	err = repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision)
	if !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("apply after epoch-only invalidation error = %v, want conflict", err)
	}
	if after := matchingApplyPersistedCounts(t, ctx, db, fixture.provider); after != before {
		t.Fatalf("rejected stale apply changed persisted counts: before=%+v after=%+v", before, after)
	}
	afterHeader, afterCandidates, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || afterHeader == nil || afterHeader.EvaluationRevision != header.EvaluationRevision ||
		!reflect.DeepEqual(afterHeader.EvaluationEvidence, header.EvaluationEvidence) || !reflect.DeepEqual(afterCandidates, candidates) {
		t.Fatalf("rejected stale apply changed evaluation: header=%+v err=%v", afterHeader, err)
	}
}
