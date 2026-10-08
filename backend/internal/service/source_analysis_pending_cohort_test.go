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

func TestRetainedRecoveryKeepsExactIntentAndAdmitsOneStepAtATime(t *testing.T) {
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
	if len(group) != 2 {
		t.Fatalf("recovery group = %+v; want only the two owned tuples", group)
	}
	if _, ok := retainedRecoveryBatchSnapshot(group); ok {
		t.Fatal("multiple retained steps were combined into a batch; they must be separately admitted")
	}
	for _, intent := range group {
		if intent.Snapshot.TargetWorkID == nil || *intent.Snapshot.TargetWorkID != workID || intent.Snapshot.TargetStep == nil || *intent.Snapshot.TargetStep != intent.Step.Step {
			t.Errorf("retained intent lost exact tuple identity: %+v", intent)
		}
		wantExecutable := "ffprobe"
		if intent.Step.Step == string(persistence.SourceStepFingerprint) {
			wantExecutable = "fpcalc"
		}
		if !intent.Snapshot.ToolsReadRequired || len(intent.Snapshot.Tools) != 1 || intent.Snapshot.Tools[0].Executable != wantExecutable {
			t.Errorf("retained intent transient tool selection = %+v, want current %s", intent.Snapshot.Tools, wantExecutable)
		}
		raw, err := json.Marshal(intent.Snapshot)
		if err != nil {
			t.Fatalf("marshal retained intent: %v", err)
		}
		var durable map[string]json.RawMessage
		if err := json.Unmarshal(raw, &durable); err != nil {
			t.Fatalf("decode durable retained intent: %v", err)
		}
		if _, exists := durable["tools"]; exists {
			t.Errorf("retained intent serialized transient tool selections: %s", raw)
		}
	}
	if len(retainedRecoveryGroup([]retainedSourceAnalysisIntent{probe, fingerprint, otherOwner}, uuid.New())) != 0 {
		t.Fatal("different durable owners were grouped")
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
