package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestRetainedRecoverySingletonAndExplicitRerunStaySingleStep(t *testing.T) {
	owner, workID, rootID := uuid.New(), uuid.New(), uuid.New()
	shaEnabled, cacheOnly := false, false
	ordinary := retainedRecoveryIntent(
		persistence.SourceAnalysisPendingWork{Work: persistence.SourceAnalysisWork{ID: workID}, Root: persistence.SourceRoot{ID: rootID}},
		persistence.SourceStepFingerprint, owner, shaEnabled, false, cacheOnly,
		persistence.SourceAnalysisToolSelection{PackageKind: "fpcalc", InstallationID: uuid.New(), RelativePath: "fpcalc/original", Executable: "fpcalc", Version: "1.5", VersionBanner: "fpcalc version 1.5"},
	)
	if got := retainedRecoveryOwner(ordinary); got != owner {
		t.Fatalf("owner = %s, want %s", got, owner)
	}
	rerun := ordinary
	run := true
	rerun.Snapshot.RerunTarget = &run
	if got := retainedRecoveryOwner(rerun); got != uuid.Nil {
		t.Fatalf("explicit rerun owner = %s, want none", got)
	}
}

func retainedRecoveryIntent(work persistence.SourceAnalysisPendingWork, step persistence.SourceStepName, owner uuid.UUID, shaEnabled, rerun, cacheOnly bool, tools ...persistence.SourceAnalysisToolSelection) retainedSourceAnalysisIntent {
	return retainedSourceAnalysisIntent{
		Pending: work,
		Step:    persistence.SourceAnalysisStep{WorkID: work.Work.ID, Step: string(step), State: "pending", LastOperationID: &owner},
		Snapshot: persistence.SourceAnalysisOperationSnapshot{
			SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
			Mode:          persistence.SourceAnalysisModeSingleStep,
			WorkIDs:       []uuid.UUID{work.Work.ID}, TargetWorkID: &work.Work.ID,
			TargetStep: stringPointer(string(step)), RerunTarget: &rerun,
			SHA256Enabled: &shaEnabled, CacheOnlyReuse: &cacheOnly,
			ToolsReadRequired: len(tools) > 0, Tools: tools,
		},
	}
}

func stringPointer(value string) *string { return &value }
