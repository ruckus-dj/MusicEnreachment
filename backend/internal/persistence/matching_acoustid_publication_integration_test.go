//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
	"testing"
	"time"
)

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
