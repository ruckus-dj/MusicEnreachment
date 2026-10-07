package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestRetainedSourceAnalysisStepIntentUsesStepSnapshotAfterOperationDeletion(t *testing.T) {
	workID := uuid.New()
	step := string(persistence.SourceStepFingerprint)
	rerun, shaEnabled, cacheOnly := true, true, false
	tool := persistence.SourceAnalysisToolSelection{
		PackageKind: "fpcalc", InstallationID: uuid.New(), RelativePath: "fpcalc/1.6.1",
		Executable: "fpcalc", Version: "1.6.1", VersionBanner: "fpcalc version 1.6.1",
	}
	raw, err := persistence.ProjectSourceAnalysisStepSnapshot(persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          persistence.SourceAnalysisModeSingleStep,
		WorkIDs:       []uuid.UUID{workID}, TargetWorkID: &workID, TargetStep: &step,
		RerunTarget: &rerun, SHA256Enabled: &shaEnabled, CacheOnlyReuse: &cacheOnly,
		ToolsReadRequired: true, Tools: []persistence.SourceAnalysisToolSelection{tool},
	}, workID, persistence.SourceStepFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	pending := persistence.SourceAnalysisPendingWork{Work: persistence.SourceAnalysisWork{ID: workID, SHA256Enabled: true}}
	stepRow := persistence.SourceAnalysisStep{WorkID: workID, Step: step, State: "pending", InputSnapshot: raw}
	intent, err := retainedSourceAnalysisStepIntent(pending, stepRow)
	if err != nil || intent.Pending.Work.ID != workID || intent.Step.WorkID != workID || intent.Step.Step != step || intent.Step.State != "pending" ||
		intent.Snapshot.RerunTarget == nil || !*intent.Snapshot.RerunTarget || len(intent.Snapshot.Tools) != 1 || intent.Snapshot.Tools[0] != tool {
		t.Fatalf("intent=%+v, err=%v", intent, err)
	}
	if _, err := retainedSourceAnalysisStepIntent(pending, persistence.SourceAnalysisStep{WorkID: workID, Step: string(persistence.SourceStepProbe), InputSnapshot: raw}); err == nil {
		t.Fatal("intent accepted a mismatched retained step")
	}
}

func TestRetainedSourceAnalysisStepIntentRejectsMalformedInputWithoutFallback(t *testing.T) {
	workID := uuid.New()
	pending := persistence.SourceAnalysisPendingWork{Work: persistence.SourceAnalysisWork{ID: workID, SHA256Enabled: false}}
	step := persistence.SourceAnalysisStep{WorkID: workID, Step: string(persistence.SourceStepFingerprint), InputSnapshot: []byte(`{"schema_version":1}`)}
	if _, err := retainedSourceAnalysisStepIntent(pending, step); err == nil {
		t.Fatal("malformed retained input was accepted")
	}
}
