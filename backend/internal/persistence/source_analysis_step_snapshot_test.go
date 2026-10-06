package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestSameSourceAnalysisStepInputRequiresExactRetainedSelection(t *testing.T) {
	snapshot := validSingleStepSnapshotWithTool()
	work := SourceAnalysisWork{ID: snapshot.WorkIDs[0], SHA256Enabled: *snapshot.SHA256Enabled}
	left, err := ProjectSourceAnalysisStepSnapshot(snapshot, work.ID, SourceStepProbe)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSourceAnalysisStepInput(left, left, work, string(SourceStepProbe)) {
		t.Fatal("identical retained inputs differ")
	}
	snapshot.Tools[0].InstallationID = uuid.New()
	right, err := ProjectSourceAnalysisStepSnapshot(snapshot, work.ID, SourceStepProbe)
	if err != nil {
		t.Fatal(err)
	}
	if sameSourceAnalysisStepInput(left, right, work, string(SourceStepProbe)) {
		t.Fatal("a different executable pin was accepted as the retained input")
	}
	if sameSourceAnalysisStepInput(left, nil, work, string(SourceStepProbe)) {
		t.Fatal("missing retained input was accepted")
	}
}

func TestProjectSourceAnalysisStepSnapshotPinsOnlyRelevantInputs(t *testing.T) {
	workID := uuid.New()
	shaEnabled, rerun, cacheOnly := true, false, false
	tool := SourceAnalysisToolSelection{PackageKind: "fpcalc", InstallationID: uuid.New(), RelativePath: "fpcalc/r1", Executable: "fpcalc", Version: "1.5", VersionBanner: "fpcalc version 1.5"}
	snapshot := SourceAnalysisOperationSnapshot{
		SchemaVersion: SourceAnalysisOperationSnapshotVersion, Mode: SourceAnalysisModeBatch,
		WorkIDs: []uuid.UUID{workID}, SHA256Enabled: &shaEnabled, RerunTarget: &rerun,
		CacheOnlyReuse: &cacheOnly, ToolsReadRequired: true, Tools: []SourceAnalysisToolSelection{tool},
		CacheOnlyFFProbeVersion: "7.0", CacheOnlyFPCalcVersion: "1.5",
		SelectedSteps: []SourceAnalysisStepSelection{{WorkID: workID, Step: SourceStepFingerprint}},
	}
	projected, err := ProjectSourceAnalysisStepSnapshot(snapshot, workID, SourceStepSHA256)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSourceAnalysisOperationSnapshot(projected)
	if err != nil {
		t.Fatal(err)
	}
	var projectedInput sourceAnalysisStepSnapshot
	if err := json.Unmarshal(projected, &projectedInput); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Tools) != 0 || decoded.ToolsReadRequired || decoded.TargetStep == nil || *decoded.TargetStep != string(SourceStepSHA256) || decoded.SelectedSteps != nil || decoded.CacheOnlyFFProbeVersion != "" || decoded.CacheOnlyFPCalcVersion != "" || projectedInput.AnalysisPolicyVersion != SourceAnalysisPolicyVersion || decoded.CacheOnlyReuse == nil || *decoded.CacheOnlyReuse {
		t.Fatalf("SHA projection retained irrelevant inputs: %+v", decoded)
	}

	step := string(SourceStepFingerprint)
	fingerprint := snapshot
	fingerprint.Mode = SourceAnalysisModeSingleStep
	fingerprint.TargetWorkID = &workID
	fingerprint.TargetStep = &step
	rerun = true
	fingerprint.RerunTarget = &rerun
	otherTool := SourceAnalysisToolSelection{PackageKind: "ffmpeg", InstallationID: uuid.New(), RelativePath: "ffmpeg/r1", Executable: "ffprobe", Version: "7", VersionBanner: "ffprobe version 7"}
	fingerprint.Tools = append(append([]SourceAnalysisToolSelection(nil), snapshot.Tools...), otherTool)
	projected, err = ProjectSourceAnalysisStepSnapshot(fingerprint, workID, SourceStepFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = DecodeSourceAnalysisOperationSnapshot(projected)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Tools) != 1 || decoded.Tools[0] != tool || decoded.CacheOnlyFPCalcVersion != "1.5" || decoded.CacheOnlyFFProbeVersion != "" || decoded.CacheOnlyReuse == nil || *decoded.CacheOnlyReuse || decoded.RerunTarget == nil || !*decoded.RerunTarget {
		t.Fatalf("fingerprint projection has unrelated inputs or lost rerun intent: %+v", decoded)
	}
}

func TestDecodeRetainedSourceAnalysisStepInputStrictlyValidatesIntent(t *testing.T) {
	workID := uuid.New()
	shaEnabled, rerun, cacheOnly := true, true, false
	stepName := string(SourceStepFingerprint)
	tool := SourceAnalysisToolSelection{PackageKind: "fpcalc", InstallationID: uuid.New(), RelativePath: "fpcalc/r1", Executable: "fpcalc", Version: "1.5", VersionBanner: "fpcalc version 1.5"}
	source := SourceAnalysisOperationSnapshot{
		SchemaVersion: SourceAnalysisOperationSnapshotVersion, Mode: SourceAnalysisModeSingleStep,
		WorkIDs: []uuid.UUID{workID}, TargetWorkID: &workID, TargetStep: &stepName,
		SHA256Enabled: &shaEnabled, RerunTarget: &rerun, CacheOnlyReuse: &cacheOnly,
		ToolsReadRequired: true, Tools: []SourceAnalysisToolSelection{tool},
	}
	raw, err := ProjectSourceAnalysisStepSnapshot(source, workID, SourceStepFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	work := SourceAnalysisWork{ID: workID, SHA256Enabled: true}
	step := SourceAnalysisStep{WorkID: workID, Step: stepName, InputSnapshot: raw}
	decoded, err := DecodeRetainedSourceAnalysisStepInput(step, work)
	if err != nil {
		t.Fatalf("decode valid retained input: %v", err)
	}
	if decoded.SHA256Enabled == nil || !*decoded.SHA256Enabled || decoded.RerunTarget == nil || !*decoded.RerunTarget || decoded.CacheOnlyReuse == nil || *decoded.CacheOnlyReuse {
		t.Fatalf("explicit retained booleans changed meaning: %+v", decoded)
	}

	for name, input := range map[string]json.RawMessage{
		"nil data": nil,
		"wrong work": func() json.RawMessage {
			wrongID := uuid.New()
			wrong := source
			wrong.WorkIDs, wrong.TargetWorkID = []uuid.UUID{wrongID}, &wrongID
			encoded, marshalErr := ProjectSourceAnalysisStepSnapshot(wrong, wrongID, SourceStepFingerprint)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			return encoded
		}(),
		"wrong policy": func() json.RawMessage {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			value["analysis_policy_version"] = SourceAnalysisPolicyVersion + 1
			encoded, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			return encoded
		}(),
		"nil explicit booleans": func() json.RawMessage {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			value["rerun_target"], value["sha256_enabled"], value["cache_only_reuse"] = nil, nil, nil
			encoded, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			return encoded
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			invalid := step
			invalid.InputSnapshot = input
			if _, err := DecodeRetainedSourceAnalysisStepInput(invalid, work); err == nil {
				t.Fatal("invalid retained input was accepted")
			}
		})
	}
}
