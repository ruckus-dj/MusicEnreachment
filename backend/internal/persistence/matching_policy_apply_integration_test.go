//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// TestApplyCurrentMatchingEvaluationRejectsUnsupportedPolicyBeforeWritesPostgreSQL
// persists the current unique evaluation, then rewrites BOTH the header policy
// and the persisted decision policy to the same unsupported literal. Because the
// two stay internally consistent, only the current-policy gate can reject the
// apply; a header/decision mismatch would not be the cause. The rejected apply
// must leave every local entity, provider mapping, release-track, and track
// source untouched. Restoring the current policy in both places then proves the
// same persisted evaluation is applicable, so the earlier rejection was the
// policy gate and not a malformed input.
func TestApplyCurrentMatchingEvaluationRejectsUnsupportedPolicyBeforeWritesPostgreSQL(t *testing.T) {
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
		t.Fatalf("read evaluation header: header=%+v err=%v", header, err)
	}

	// Rewrite the header policy and the persisted decision policy together so the
	// stored evaluation remains internally consistent. All other fences (group
	// revision, grouping epoch, provider fences) are deliberately left current.
	const unsupportedPolicy = "matching-go-v1"
	rewritePolicy := func(policy string) {
		t.Helper()
		if _, err := db.NewRaw(`UPDATE incoming_match_evaluation
			SET policy_version=?,
				evaluation_evidence=jsonb_set(evaluation_evidence, '{evaluation_decision,policy_version}', to_jsonb(?::text))
			WHERE group_id=?`, policy, policy, fixture.group.ID).Exec(ctx); err != nil {
			t.Fatalf("rewrite persisted evaluation policy to %q: %v", policy, err)
		}
	}
	rewritePolicy(unsupportedPolicy)

	before := matchingApplyPersistedCounts(t, ctx, db, fixture.provider)
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision); err == nil {
		t.Fatal("apply accepted an unsupported persisted policy")
	}
	if after := matchingApplyPersistedCounts(t, ctx, db, fixture.provider); after != before {
		t.Fatalf("rejected unsupported-policy apply changed persisted counts: before=%+v after=%+v", before, after)
	}

	rewritePolicy("matching-rapidfuzz-v1")
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision); err != nil {
		t.Fatalf("apply after restoring the current policy: %v", err)
	}
}
