package persistence

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

type sourceAnalysisStepSnapshot struct {
	SourceAnalysisOperationSnapshot
	AnalysisPolicyVersion int `json:"analysis_policy_version"`
}

// ProjectSourceAnalysisStepSnapshot creates the canonical durable intent for
// one work/step. Only executable and cache selections relevant to that step
// survive projection; SHA-256 never depends on managed tools.
func ProjectSourceAnalysisStepSnapshot(snapshot SourceAnalysisOperationSnapshot, workID uuid.UUID, step SourceStepName) (json.RawMessage, error) {
	if workID == uuid.Nil || !validSourceStep(step) {
		return nil, fmt.Errorf("project source analysis step snapshot: valid work and step are required")
	}
	if snapshot.RerunTarget == nil {
		return nil, fmt.Errorf("project source analysis step snapshot: explicit rerun intent is required")
	}
	if !containsSourceWork(snapshot.WorkIDs, workID) {
		return nil, fmt.Errorf("project source analysis step snapshot: work is not in the immutable selection")
	}
	rerun := snapshot.Mode == SourceAnalysisModeSingleStep && snapshot.TargetWorkID != nil && *snapshot.TargetWorkID == workID && snapshot.TargetStep != nil && *snapshot.TargetStep == string(step) && *snapshot.RerunTarget
	if rerun && step != SourceStepFingerprint {
		return nil, fmt.Errorf("project source analysis step snapshot: only fingerprint may be explicitly rerun")
	}
	projected := SourceAnalysisOperationSnapshot{
		SchemaVersion: SourceAnalysisOperationSnapshotVersion, Mode: SourceAnalysisModeSingleStep,
		WorkIDs: []uuid.UUID{workID}, TargetWorkID: &workID, TargetStep: sourceAnalysisStringPointer(string(step)),
		RerunTarget: sourceAnalysisBoolPointer(rerun),
	}
	encoded, err := json.Marshal(sourceAnalysisStepSnapshot{SourceAnalysisOperationSnapshot: projected, AnalysisPolicyVersion: SourceAnalysisPolicyVersion})
	if err != nil {
		return nil, fmt.Errorf("project source analysis step snapshot: %w", err)
	}
	return encoded, nil
}

// DecodeRetainedSourceAnalysisStepInput decodes and validates an admitted
// per-step intent against its immutable work and step identity.
func DecodeRetainedSourceAnalysisStepInput(step SourceAnalysisStep, work SourceAnalysisWork) (SourceAnalysisOperationSnapshot, error) {
	snapshot, err := DecodeSourceAnalysisOperationSnapshot(step.InputSnapshot)
	if err != nil {
		return SourceAnalysisOperationSnapshot{}, err
	}
	var input sourceAnalysisStepSnapshot
	if err := json.Unmarshal(step.InputSnapshot, &input); err != nil {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode retained source analysis step input: %w", err)
	}
	if snapshot.Mode != SourceAnalysisModeSingleStep || len(snapshot.WorkIDs) != 1 || snapshot.WorkIDs[0] != work.ID || snapshot.TargetWorkID == nil || *snapshot.TargetWorkID != work.ID || snapshot.TargetStep == nil || *snapshot.TargetStep != step.Step || input.AnalysisPolicyVersion != SourceAnalysisPolicyVersion {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode retained source analysis step input: identity or immutable policy does not match")
	}
	shaEnabled, cacheOnly := work.SHA256Enabled, false
	snapshot.SHA256Enabled = &shaEnabled
	snapshot.CacheOnlyReuse = &cacheOnly
	return snapshot, nil
}

func sourceAnalysisBoolPointer(value bool) *bool       { return &value }
func sourceAnalysisStringPointer(value string) *string { return &value }

func sameSourceAnalysisStepInput(left, right json.RawMessage, work SourceAnalysisWork, step string) bool {
	leftStep := SourceAnalysisStep{WorkID: work.ID, Step: step, InputSnapshot: left}
	rightStep := SourceAnalysisStep{WorkID: work.ID, Step: step, InputSnapshot: right}
	a, errA := DecodeRetainedSourceAnalysisStepInput(leftStep, work)
	b, errB := DecodeRetainedSourceAnalysisStepInput(rightStep, work)
	if errA != nil || errB != nil {
		return false
	}
	encodedA, errA := json.Marshal(sourceAnalysisStepSnapshot{SourceAnalysisOperationSnapshot: a, AnalysisPolicyVersion: SourceAnalysisPolicyVersion})
	encodedB, errB := json.Marshal(sourceAnalysisStepSnapshot{SourceAnalysisOperationSnapshot: b, AnalysisPolicyVersion: SourceAnalysisPolicyVersion})
	return errA == nil && errB == nil && string(encodedA) == string(encodedB)
}

func containsSourceWork(workIDs []uuid.UUID, target uuid.UUID) bool {
	for _, id := range workIDs {
		if id == target {
			return true
		}
	}
	return false
}
