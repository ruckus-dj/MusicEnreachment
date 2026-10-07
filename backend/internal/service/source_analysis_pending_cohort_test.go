package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestPendingSourceAnalysisFingerprintTracksPolicyAndRemainingSteps(t *testing.T) {
	workID := uuid.New()
	works := []persistence.SourceAnalysisPendingWork{{
		Work:  persistence.SourceAnalysisWork{ID: workID, SHA256Enabled: true},
		Steps: []persistence.SourceAnalysisStep{{Step: string(persistence.SourceStepFingerprint)}},
	}}
	initial := pendingSourceAnalysisFingerprint(works)

	works[0].Work.SHA256Enabled = false
	if fingerprint := pendingSourceAnalysisFingerprint(works); fingerprint == initial {
		t.Fatal("fingerprint did not distinguish a changed immutable SHA policy")
	}
	works[0].Work.SHA256Enabled = true
	works[0].Steps = nil
	if fingerprint := pendingSourceAnalysisFingerprint(works); fingerprint == initial {
		t.Fatal("fingerprint did not distinguish exhausted pending steps")
	}
}

func TestRetainedRecoveryGroupsExactStepsAndOriginalToolPins(t *testing.T) {
	owner, workID, rootID := uuid.New(), uuid.New(), uuid.New()
	shaEnabled, rerun, cacheOnly := true, false, false
	ffprobe := persistence.SourceAnalysisToolSelection{
		PackageKind: "ffmpeg", InstallationID: uuid.New(), RelativePath: "ffmpeg/original",
		Executable: "ffprobe", Version: "7.0", VersionBanner: "ffprobe version 7.0",
	}
	fpcalc := persistence.SourceAnalysisToolSelection{
		PackageKind: "fpcalc", InstallationID: uuid.New(), RelativePath: "fpcalc/original",
		Executable: "fpcalc", Version: "1.5", VersionBanner: "fpcalc version 1.5",
	}
	rootPath := t.TempDir()
	work := persistence.SourceAnalysisPendingWork{
		Work: persistence.SourceAnalysisWork{ID: workID, SHA256Enabled: true},
		Root: persistence.SourceRoot{ID: rootID, ConfiguredPath: rootPath},
	}
	probe := retainedRecoveryIntent(work, persistence.SourceStepProbe, owner, shaEnabled, rerun, cacheOnly, ffprobe)
	fingerprint := retainedRecoveryIntent(work, persistence.SourceStepFingerprint, owner, shaEnabled, rerun, cacheOnly, fpcalc)
	otherOwner := retainedRecoveryIntent(work, persistence.SourceStepSHA256, uuid.New(), shaEnabled, rerun, cacheOnly)
	newWork := work
	newWork.Work.ID = uuid.New()
	newNeverAdmittedSibling := persistence.SourceAnalysisPendingWork{
		Work:  persistence.SourceAnalysisWork{ID: newWork.Work.ID, SHA256Enabled: true},
		Steps: []persistence.SourceAnalysisStep{{Step: string(persistence.SourceStepFingerprint)}},
	}
	newIntent := retainedSourceAnalysisIntent{
		Pending: newNeverAdmittedSibling,
		Step:    persistence.SourceAnalysisStep{WorkID: newWork.Work.ID, Step: string(persistence.SourceStepFingerprint), State: "pending"},
	}

	group := retainedRecoveryGroup([]retainedSourceAnalysisIntent{probe, fingerprint, otherOwner, newIntent}, owner)
	snapshot, ok := retainedRecoveryBatchSnapshot(group)
	if !ok {
		t.Fatal("compatible pending steps from the same durable owner should form a batch")
	}
	if len(snapshot.WorkIDs) != 1 || snapshot.WorkIDs[0] != workID || len(snapshot.SelectedSteps) != 2 {
		t.Fatalf("batch selection = work %v steps %v; want only the two recovered tuples", snapshot.WorkIDs, snapshot.SelectedSteps)
	}
	if snapshot.SelectedSteps[0].Step == snapshot.SelectedSteps[1].Step || snapshot.SelectedSteps[0].WorkID != workID || snapshot.SelectedSteps[1].WorkID != workID {
		t.Fatalf("batch selected unexpected tuples: %+v", snapshot.SelectedSteps)
	}
	if len(snapshot.Tools) != 2 || snapshot.Tools[0] == snapshot.Tools[1] {
		t.Fatalf("batch tools = %+v, want both distinct original pins", snapshot.Tools)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := persistence.DecodeSourceAnalysisOperationSnapshot(raw)
	if err != nil {
		t.Fatalf("decode exact recovery batch snapshot: %v", err)
	}
	if len(decoded.SelectedSteps) != 2 || len(decoded.WorkIDs) != 1 || decoded.WorkIDs[0] != workID {
		t.Fatalf("decoded exact batch selection = %+v", decoded)
	}
	for _, want := range []persistence.SourceAnalysisToolSelection{ffprobe, fpcalc} {
		found := false
		for _, got := range snapshot.Tools {
			found = found || got == want
		}
		if !found {
			t.Errorf("batch omitted retained tool pin %+v", want)
		}
	}
	if len(retainedRecoveryGroup([]retainedSourceAnalysisIntent{probe, fingerprint, otherOwner}, uuid.New())) != 0 {
		t.Fatal("different durable owners were grouped")
	}
	if _, ok := retainedRecoveryBatchSnapshot([]retainedSourceAnalysisIntent{probe, otherOwner}); ok {
		t.Fatal("steps from different durable owners formed a batch")
	}
	if len(group) != 2 {
		t.Fatalf("group included an unrelated or never-admitted pending step: %+v", group)
	}
}

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
	if _, ok := retainedRecoveryBatchSnapshot([]retainedSourceAnalysisIntent{ordinary}); ok {
		t.Fatal("singleton recovery was widened to a batch")
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
