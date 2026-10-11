//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestManualMatchingAcceptsWriterEvaluationEvidencePostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	groupingRepository := persistence.NewIncomingGroupingRepository(db)
	providerRepository := persistence.NewProviderCacheRepository(db)

	grouping, err := service.NewIncomingGroups(groupingRepository).Snapshot(ctx)
	if err != nil {
		t.Fatalf("read current incoming grouping: %v", err)
	}
	captures, err := groupingRepository.CurrentCaptures(ctx)
	if err != nil {
		t.Fatalf("read current incoming captures: %v", err)
	}
	queryKeys := []string{fixture.cacheKey, "recording-search:a"}
	providerSnapshot, err := providerRepository.GetMusicBrainzMatchingSnapshot(ctx,
		"https://musicbrainz.example/ws/2", "incoming-match-integration", fixture.releaseKeys[:1], queryKeys)
	if err != nil {
		t.Fatalf("read current provider matching snapshot: %v", err)
	}
	if len(providerSnapshot.Releases) != 1 {
		t.Fatalf("provider snapshot releases = %d, want one fixture release", len(providerSnapshot.Releases))
	}
	snapshot := service.MatchingEvaluationSnapshot{
		Grouping: grouping, Captures: captures, Provider: providerSnapshot,
		RequestedGroupIDs: []uuid.UUID{fixture.group.ID},
		ReleaseKeysByFile: map[uuid.UUID][]string{
			fixture.memberA: {fixture.releaseKeys[0]},
			fixture.memberB: {fixture.releaseKeys[0]},
		},
		SearchKeysByFile: map[uuid.UUID]service.MatchingSearchKeys{
			fixture.memberA: {Release: "release-search:0:1:fixture", Recording: "recording-search:a"},
			fixture.memberB: {Release: "release-search:0:1:fixture", Recording: "recording-search:a"},
		},
	}
	writerResults, err := service.EvaluateMatchingSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatalf("evaluate real matching snapshot: %v", err)
	}
	if len(writerResults) != 1 || writerResults[0].GroupID != fixture.group.ID || len(writerResults[0].Result.Groups) != 1 {
		t.Fatalf("writer results = %+v, want one evaluated fixture group", writerResults)
	}

	// Keep the fixture's known-valid persistence fences, but persist the evidence
	// produced by the exported evaluator verbatim; manual review consumes its
	// review_proposal rather than a fabricated replacement.
	evaluation := fixture.evaluation(writerResults[0].Result.Groups[0].Candidates)
	evaluation.Groups[0].Evidence = append([]byte(nil), writerResults[0].Result.Groups[0].Evidence...)
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	if err := repository.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist writer evaluation evidence: %v", err)
	}
	header, _, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read persisted evaluation header: header=%+v err=%v", header, err)
	}

	assignment := []persistence.MatchingManualAssignment{{
		FileMemberID: fixture.memberA, ProviderTrackID: fixture.trackID, Medium: 1, Position: 1,
	}}
	preview, err := repository.PreviewMatchingManual(ctx, fixture.group.ID, header.EvaluationRevision, fixture.releaseID, assignment)
	if err != nil {
		t.Fatalf("preview partial manual assignment from writer evidence: %v", err)
	}
	if len(preview.Members) != 2 || len(preview.OmittedMemberIDs) != 1 || preview.OmittedMemberIDs[0] != fixture.memberB {
		t.Fatalf("preview scope = %+v, want both members and the second omitted", preview)
	}
	if _, err := repository.ConfirmMatchingManual(ctx, fixture.group.ID, preview.EvaluationRevision, fixture.releaseID,
		assignment, preview.PreviewDigest, uuid.New()); err != nil {
		t.Fatalf("confirm partial manual assignment from writer evidence: %v", err)
	}
}
