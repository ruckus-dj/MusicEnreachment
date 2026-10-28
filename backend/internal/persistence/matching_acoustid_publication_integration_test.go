//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestAcoustIDPublicationRejectsChangedFingerprintContentsPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixtureWithSelectedFingerprint(t, ctx, db)
	captures, err := persistence.NewIncomingGroupingRepository(db).CurrentCaptures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inputsByWork := make(map[uuid.UUID]persistence.IncomingMatchAcoustIDSourceInputFence, len(captures))
	for _, capture := range captures {
		if capture.WorkID != fixture.workA && capture.WorkID != fixture.workB {
			continue
		}
		key, err := persistence.AcoustIDRequestCacheKeyForCapture(capture)
		if err != nil {
			t.Fatalf("derive AcoustID request key for %s: %v", capture.WorkID, err)
		}
		inputsByWork[capture.WorkID] = persistence.IncomingMatchAcoustIDSourceInputFence{Capture: capture, RequestCacheKey: key}
	}
	if len(inputsByWork) != 2 {
		t.Fatalf("selected fingerprint captures = %d, want both incoming works", len(inputsByWork))
	}

	providerCache := persistence.NewProviderCacheRepository(db)
	if _, err := providerCache.ResolveAcoustIDSource(ctx, ""); err != nil {
		t.Fatalf("resolve official AcoustID source: %v", err)
	}
	candidate := fixture.candidate(fixture.memberA, .8)
	candidate.Positions = []persistence.IncomingMatchPosition{{TrackID: fixture.trackID, Medium: 0, Position: 0}}
	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{candidate})
	evaluation.Provider.AcoustIDSources = []persistence.IncomingMatchAcoustIDSourceFence{
		{FileMemberID: fixture.memberA, CacheKey: inputsByWork[fixture.workA].RequestCacheKey, SourceInputs: []persistence.IncomingMatchAcoustIDSourceInputFence{inputsByWork[fixture.workA]}},
		{FileMemberID: fixture.memberB, CacheKey: inputsByWork[fixture.workB].RequestCacheKey, SourceInputs: []persistence.IncomingMatchAcoustIDSourceInputFence{inputsByWork[fixture.workB]}},
	}
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	if err := repository.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist baseline matching result: %v", err)
	}
	headerBefore, candidatesBefore := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	if len(candidatesBefore) != 1 {
		t.Fatalf("baseline candidates = %#v, want one", candidatesBefore)
	}
	selectedBefore := captureForIncomingWork(t, ctx, db, fixture.workA)
	if selectedBefore.FingerprintWinner == nil || selectedBefore.Fingerprint == nil {
		t.Fatalf("fixture work %s has no selected fingerprint: %+v", fixture.workA, selectedBefore)
	}

	// Change the selected row in place. Its winner identity remains stable, but
	// its contents no longer match the copied AcoustID source fence.
	if _, err := db.NewRaw(`UPDATE media_fingerprint_result SET fingerprint=? WHERE id=?`, "9876,5432", *selectedBefore.FingerprintWinner).Exec(ctx); err != nil {
		t.Fatalf("mutate selected fingerprint result contents: %v", err)
	}
	selectedAfter := captureForIncomingWork(t, ctx, db, fixture.workA)
	if selectedBefore.FingerprintWinner == nil || selectedAfter.FingerprintWinner == nil || *selectedBefore.FingerprintWinner != *selectedAfter.FingerprintWinner || selectedBefore.Fingerprint == nil || selectedAfter.Fingerprint == nil || *selectedBefore.Fingerprint == *selectedAfter.Fingerprint {
		t.Fatalf("fingerprint rerun did not mutate the selected result contents in place: before=%+v after=%+v", selectedBefore, selectedAfter)
	}
	refreshIncomingMatchFixtureRevision(t, ctx, db, fixture)
	evaluation.ExpectedGroupsRevision = fixture.groupsRevision
	evaluation.ExpectedGroupingEpoch = fixture.groupingEpoch
	evaluation.Groups[0].GroupRevision = fixture.group.Revision

	priorRevision := headerBefore.EvaluationRevision
	if err := repository.ReplaceIfCurrent(ctx, evaluation, map[uuid.UUID]*string{fixture.group.ID: &priorRevision}); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("publish stale AcoustID snapshot error = %v, want conflict", err)
	}
	headerAfter, candidatesAfter := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	if headerAfter.EvaluationRevision != headerBefore.EvaluationRevision || string(headerAfter.EvaluationEvidence) != string(headerBefore.EvaluationEvidence) || len(candidatesAfter) != len(candidatesBefore) || candidatesAfter[0].FileMemberID != candidatesBefore[0].FileMemberID || candidatesAfter[0].Score != candidatesBefore[0].Score || string(candidatesAfter[0].Evidence) != string(candidatesBefore[0].Evidence) || len(candidatesAfter[0].Positions) != len(candidatesBefore[0].Positions) || candidatesAfter[0].Positions[0] != candidatesBefore[0].Positions[0] {
		t.Fatalf("stale publication changed accepted result: before=(%+v,%+v) after=(%+v,%+v)", headerBefore, candidatesBefore, headerAfter, candidatesAfter)
	}
}

func newIncomingMatchFixtureWithSelectedFingerprint(t *testing.T, ctx context.Context, db *bun.DB) *incomingMatchFixture {
	t.Helper()
	fixture := newIncomingMatchFixture(t, ctx, db)
	applyIncomingMatchFingerprint(t, ctx, db, fixture.workA, "1234,5678")
	applyIncomingMatchFingerprint(t, ctx, db, fixture.workB, "2345,6789")
	refreshIncomingMatchFixtureRevision(t, ctx, db, fixture)
	return fixture
}

func refreshIncomingMatchFixtureRevision(t *testing.T, ctx context.Context, db *bun.DB, fixture *incomingMatchFixture) {
	t.Helper()
	grouping := persistence.NewIncomingGroupingRepository(db)
	if err := service.NewIncomingGroups(grouping).Refresh(ctx); err != nil {
		t.Fatalf("refresh incoming groups after selected fingerprints: %v", err)
	}
	snapshot, err := service.NewIncomingGroups(grouping).Snapshot(ctx)
	if err != nil {
		t.Fatalf("read incoming group snapshot after selected fingerprints: %v", err)
	}
	if len(snapshot.Groups) != 1 || len(snapshot.Groups[0].Members) != 2 {
		t.Fatalf("selected fingerprints changed fixture grouping: %+v", snapshot.Groups)
	}
	state, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read grouping state after selected fingerprints: %v", err)
	}
	fixture.group = persistence.IncomingGroupingGroup{ID: snapshot.Groups[0].ID, Revision: snapshot.Groups[0].Revision, Members: append([]uuid.UUID(nil), snapshot.Groups[0].Members...)}
	fixture.groupingEpoch = state.Revision
	fixture.groupsRevision, err = service.IncomingGroupsRevision(snapshot.Groups)
	if err != nil {
		t.Fatalf("calculate groups revision after selected fingerprints: %v", err)
	}
}

func captureForIncomingWork(t *testing.T, ctx context.Context, db *bun.DB, workID uuid.UUID) persistence.IncomingGroupingCapture {
	t.Helper()
	captures, err := persistence.NewIncomingGroupingRepository(db).CurrentCaptures(ctx)
	if err != nil {
		t.Fatalf("read current incoming captures: %v", err)
	}
	for _, capture := range captures {
		if capture.WorkID == workID {
			return capture
		}
	}
	t.Fatalf("current incoming capture for work %s not found", workID)
	return persistence.IncomingGroupingCapture{}
}

func applyIncomingMatchFingerprint(t *testing.T, ctx context.Context, db *bun.DB, workID uuid.UUID, fingerprint string) {
	t.Helper()
	repository := persistence.NewSourceInventoryRepository(db)
	work, location, err := repository.GetNormalizedSourceAnalysisWork(ctx, workID)
	if err != nil {
		t.Fatalf("read source work %s for fingerprint: %v", workID, err)
	}
	var root persistence.SourceRoot
	if err := db.NewSelect().Model(&root).Where("id = ?", work.SourceRootID).Scan(ctx); err != nil {
		t.Fatalf("read source root for fingerprint work %s: %v", workID, err)
	}
	// This fixture starts from completed metadata-only analysis. Explicitly add
	// fingerprint as pending work before admitting a batch that selects pending
	// steps; a single-step operation is reserved for retrying a failed step.
	if _, err := db.NewRaw(`INSERT INTO source_analysis_step (work_id, step, state) VALUES (?, 'fingerprint', 'pending')`, work.ID).Exec(ctx); err != nil {
		t.Fatalf("request fingerprint step for %s: %v", workID, err)
	}
	tool := incomingMatchFingerprintTool(t, ctx, db)
	fixture := newAnalysisStepFixture(t, ctx, db, repository, openScanEnqueueRiver(t, db), &root, *location, work,
		persistence.SourceAnalysisModeBatch, nil, nil, false,
		[]persistence.SourceAnalysisToolSelection{tool})
	attempt := fixture.claim(persistence.SourceStepFingerprint)
	jobID := *fixture.operation.RiverJobID
	if _, err := repository.ApplySourceFingerprint(ctx, persistence.SourceFingerprintApply{
		WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: jobID, StepAttempt: attempt,
		Result: persistence.SourceFingerprintResult{
			ID: uuid.New(), FPCalcVersion: tool.Version, VersionBanner: tool.VersionBanner,
			AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: fingerprint,
			ReportedDuration: 12.5, CalculatedAt: time.Now().UTC().Truncate(time.Microsecond),
			ParserContractVersion: 1,
		},
	}); err != nil {
		t.Fatalf("apply selected fingerprint for %s: %v", workID, err)
	}
	delivery := persistence.SourceAnalysisOperationDelivery{Attempt: fixture.operation.Attempt, JobID: jobID}
	if err := repository.SettleNormalizedSourceAnalysisDelivery(ctx, fixture.operation.ID, delivery, "succeeded", "complete", ""); err != nil {
		t.Fatalf("settle fingerprint operation for %s: %v", workID, err)
	}
}

func incomingMatchFingerprintTool(t *testing.T, ctx context.Context, db *bun.DB) persistence.SourceAnalysisToolSelection {
	t.Helper()
	var installation persistence.ToolInstallation
	err := db.NewSelect().Model(&installation).
		Where("package_kind = ? AND source_name = ? AND release_identity = ?", "fpcalc", "analysis-test", "fpcalc-test-release").
		OrderExpr("created_at ASC").Limit(1).Scan(ctx)
	if err != nil {
		return insertVerifiedAnalysisTool(t, ctx, db, "fpcalc", "fpcalc", "1.5.1", "fpcalc version 1.5.1")
	}
	var versions map[string]string
	if err := json.Unmarshal(installation.ExecutableVerificationOutput, &versions); err != nil {
		t.Fatalf("decode verified fpcalc installation metadata: %v", err)
	}
	return persistence.SourceAnalysisToolSelection{
		PackageKind: "fpcalc", InstallationID: installation.ID, RelativePath: installation.RelativePath,
		Executable: "fpcalc", Version: "1.5.1", VersionBanner: versions["fpcalc"],
	}
}
