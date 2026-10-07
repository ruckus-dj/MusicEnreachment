package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestValidateSourceAnalysisOperationContractPinsSelectors(t *testing.T) {
	workID := uuid.New()
	step := string(SourceStepProbe)
	sha256Enabled, rerunTarget, cacheOnly := false, false, false
	toolID := uuid.New()
	snapshot := SourceAnalysisOperationSnapshot{
		SchemaVersion:     SourceAnalysisOperationSnapshotVersion,
		Mode:              SourceAnalysisModeSingleStep,
		WorkIDs:           []uuid.UUID{workID},
		TargetWorkID:      &workID,
		TargetStep:        &step,
		SHA256Enabled:     &sha256Enabled,
		RerunTarget:       &rerunTarget,
		CacheOnlyReuse:    &cacheOnly,
		ToolsReadRequired: true,
		Tools: []SourceAnalysisToolSelection{{
			PackageKind: "ffmpeg", InstallationID: toolID, RelativePath: "ffmpeg/r1",
			Executable: "ffprobe", Version: "7.1", VersionBanner: "ffprobe version 7.1",
		}},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	operation := &Operation{
		Kind:               analysisSourceOperationKind,
		SourceAnalysisMode: SourceAnalysisModeSingleStep,
		TargetWorkID:       &workID,
		TargetStep:         &step,
		ToolsReadRequired:  true,
		InputSnapshot:      raw,
	}
	if _, err := ValidateSourceAnalysisOperationContract(operation); err != nil {
		t.Fatalf("valid operation contract: %v", err)
	}
	wrongStep := string(SourceStepFingerprint)
	operation.TargetStep = &wrongStep
	if _, err := ValidateSourceAnalysisOperationContract(operation); err == nil {
		t.Fatal("operation with selectors different from its snapshot was accepted")
	}
}

func TestSelectsSourceAnalysisStepUsesExactOptionalBatchMembership(t *testing.T) {
	workA, workB := uuid.New(), uuid.New()
	if !selectsSourceAnalysisStep(nil, workA, SourceStepProbe) {
		t.Fatal("initial batch without selected_steps must include pending steps")
	}
	selected := []SourceAnalysisStepSelection{
		{WorkID: workA, Step: SourceStepProbe},
		{WorkID: workB, Step: SourceStepFingerprint},
	}
	for _, test := range []struct {
		work uuid.UUID
		step SourceStepName
		want bool
	}{
		{workA, SourceStepProbe, true},
		{workA, SourceStepSHA256, false},
		{workB, SourceStepProbe, false},
		{workB, SourceStepFingerprint, true},
	} {
		if got := selectsSourceAnalysisStep(selected, test.work, test.step); got != test.want {
			t.Errorf("selectsSourceAnalysisStep(%v, %s) = %t, want %t", test.work, test.step, got, test.want)
		}
	}
}
