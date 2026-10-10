//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestIncomingMatchRepositoryRejectsUnsupportedPolicyWithoutChangingPublishedResultPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)

	first := fixture.candidate(fixture.memberA, .75)
	first.Positions = []persistence.IncomingMatchPosition{{TrackID: fixture.trackID, Medium: 0, Position: 0}}
	if err := repository.Replace(ctx, fixture.evaluation([]persistence.IncomingMatchCandidate{first})); err != nil {
		t.Fatalf("publish current-policy result: %v", err)
	}
	beforeHeader, beforeCandidates := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)

	stale := fixture.evaluation([]persistence.IncomingMatchCandidate{fixture.candidate(fixture.memberB, .99)})
	stale.PolicyVersion = "matching-go-v1"
	err := repository.Replace(ctx, stale)
	if err == nil {
		t.Fatal("unsupported policy unexpectedly replaced the published result")
	}
	afterHeader, afterCandidates := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	if afterHeader.EvaluationRevision != beforeHeader.EvaluationRevision || afterHeader.PolicyVersion != beforeHeader.PolicyVersion {
		t.Fatalf("rejected policy changed evaluation header: before=%+v after=%+v", beforeHeader, afterHeader)
	}
	if len(afterCandidates) != len(beforeCandidates) || len(afterCandidates) != 1 || afterCandidates[0].FileMemberID != fixture.memberA || afterCandidates[0].Score != .75 || len(afterCandidates[0].Positions) != 1 || afterCandidates[0].Positions[0] != beforeCandidates[0].Positions[0] {
		t.Fatalf("rejected policy changed persisted candidates/positions: before=%+v after=%+v", beforeCandidates, afterCandidates)
	}

	// ReplaceIfCurrent must apply the same policy gate, including when the caller
	// supplies the revision it just read.
	stale = fixture.evaluation([]persistence.IncomingMatchCandidate{fixture.candidate(fixture.memberB, .99)})
	stale.PolicyVersion = "matching-go-v1"
	if err := repository.ReplaceIfCurrent(ctx, stale, map[uuid.UUID]*string{fixture.group.ID: matchingCoordinatorStringPtr(beforeHeader.EvaluationRevision)}); err == nil {
		t.Fatal("ReplaceIfCurrent accepted unsupported policy")
	}
	afterHeader, afterCandidates = mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	if afterHeader.EvaluationRevision != beforeHeader.EvaluationRevision || afterHeader.PolicyVersion != beforeHeader.PolicyVersion || len(afterCandidates) != 1 || afterCandidates[0].FileMemberID != fixture.memberA {
		t.Fatalf("rejected conditional replacement changed persisted result: header=%+v candidates=%+v", afterHeader, afterCandidates)
	}

	// Ensure the fixture's evidence really is independently JSON-serializable;
	// policy-version gates must not depend on malformed input parsing.
	var evidence map[string]json.RawMessage
	if err := json.Unmarshal(beforeHeader.EvaluationEvidence, &evidence); err != nil {
		t.Fatalf("decode persisted current-policy evidence: %v", err)
	}
}
