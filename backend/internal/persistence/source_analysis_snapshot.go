package persistence

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

const (
	SourceAnalysisModeBatch                = "batch"
	SourceAnalysisModeSingleStep           = "single_step"
	SourceAnalysisOperationSnapshotVersion = 1
)

// SourceAnalysisOperationSnapshot describes durable work identity and explicit
// step intent. The non-serialized fields are transient admission/execution
// context only; callers must resolve them from current work and configuration.
type SourceAnalysisOperationSnapshot struct {
	SchemaVersion           int                           `json:"schema_version"`
	Mode                    string                        `json:"mode"`
	WorkIDs                 []uuid.UUID                   `json:"work_ids"`
	TargetWorkID            *uuid.UUID                    `json:"target_work_id,omitempty"`
	TargetStep              *string                       `json:"target_step,omitempty"`
	RerunTarget             *bool                         `json:"rerun_target"`
	SHA256Enabled           *bool                         `json:"-"`
	ToolsReadRequired       bool                          `json:"-"`
	CacheOnlyReuse          *bool                         `json:"-"`
	CacheOnlyFFProbeVersion string                        `json:"-"`
	CacheOnlyFPCalcVersion  string                        `json:"-"`
	Tools                   []SourceAnalysisToolSelection `json:"-"`
	SelectedSteps           []SourceAnalysisStepSelection `json:"selected_steps,omitempty"`
}

// SourceAnalysisStepSelection identifies one exact work/step tuple selected
// by a batch operation. Batch intent always carries these tuples explicitly.
type SourceAnalysisStepSelection struct {
	WorkID uuid.UUID      `json:"work_id"`
	Step   SourceStepName `json:"step"`
}

// SourceAnalysisToolSelection is an immutable executable selection captured
// from verified installation metadata during preparation.
type SourceAnalysisToolSelection struct {
	PackageKind    string    `json:"package_kind"`
	InstallationID uuid.UUID `json:"installation_id"`
	RelativePath   string    `json:"relative_path"`
	Executable     string    `json:"executable"`
	Version        string    `json:"version"`
	VersionBanner  string    `json:"version_banner"`
}

// DecodeSourceAnalysisOperationSnapshot validates the normalized durable
// contract. There is intentionally no version fallback or inferred target step.
func DecodeSourceAnalysisOperationSnapshot(raw json.RawMessage) (SourceAnalysisOperationSnapshot, error) {
	var snapshot SourceAnalysisOperationSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: %w", err)
	}
	for _, forbidden := range []string{"sha256_enabled", "tools_read_required", "cache_only_reuse", "cache_only_ffprobe_version", "cache_only_fpcalc_version", "tools"} {
		if _, exists := fields[forbidden]; exists {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: legacy or runtime field %q is not durable intent", forbidden)
		}
	}
	if snapshot.SchemaVersion != SourceAnalysisOperationSnapshotVersion {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: unsupported schema version %d", snapshot.SchemaVersion)
	}
	if snapshot.RerunTarget == nil {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: explicit rerun intent is required")
	}
	if snapshot.Mode != SourceAnalysisModeBatch && snapshot.Mode != SourceAnalysisModeSingleStep {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: unsupported mode %q", snapshot.Mode)
	}
	if len(snapshot.WorkIDs) == 0 {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: at least one work item is required")
	}
	seen := make(map[uuid.UUID]struct{}, len(snapshot.WorkIDs))
	for _, id := range snapshot.WorkIDs {
		if id == uuid.Nil {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: work identities must be non-empty")
		}
		if _, exists := seen[id]; exists {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: duplicate work identity %s", id)
		}
		seen[id] = struct{}{}
	}
	if snapshot.Mode == SourceAnalysisModeBatch {
		if len(snapshot.WorkIDs) != 1 {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: batch operations must select exactly one work item")
		}
		if snapshot.TargetWorkID != nil || snapshot.TargetStep != nil {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: batch operations cannot have a single-step target")
		}
		if _, selectedPresent := fields["selected_steps"]; !selectedPresent || len(snapshot.SelectedSteps) == 0 {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: selected_steps must be a non-empty array")
		}
		var selectedTuples []map[string]json.RawMessage
		if err := json.Unmarshal(fields["selected_steps"], &selectedTuples); err != nil {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: selected_steps must contain exact work/step tuples: %w", err)
		}
		for _, tuple := range selectedTuples {
			if len(tuple) != 2 || tuple["work_id"] == nil || tuple["step"] == nil {
				return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: selected_steps must contain exact work/step tuples")
			}
		}
		selected := make(map[SourceAnalysisStepSelection]struct{}, len(snapshot.SelectedSteps))
		selectedWorks := make(map[uuid.UUID]struct{}, len(snapshot.SelectedSteps))
		for _, selection := range snapshot.SelectedSteps {
			if _, exists := seen[selection.WorkID]; !exists || !validSourceStep(selection.Step) {
				return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: selected step is outside the pinned work selection")
			}
			if _, exists := selected[selection]; exists {
				return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: duplicate selected step")
			}
			selected[selection] = struct{}{}
			selectedWorks[selection.WorkID] = struct{}{}
		}
		if len(selectedWorks) != len(snapshot.WorkIDs) {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: selected work identities must exactly match the pinned work selection")
		}
	} else {
		if snapshot.TargetWorkID == nil || *snapshot.TargetWorkID == uuid.Nil || snapshot.TargetStep == nil || !validSourceStep(SourceStepName(*snapshot.TargetStep)) {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: single-step work and step are required")
		}
		if _, exists := seen[*snapshot.TargetWorkID]; !exists {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: target work is not in the pinned work selection")
		}
		if *snapshot.RerunTarget && SourceStepName(*snapshot.TargetStep) != SourceStepFingerprint {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: only fingerprint may be explicitly rerun")
		}
	}
	if snapshot.Mode == SourceAnalysisModeBatch && *snapshot.RerunTarget {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: batch operations cannot request a targeted rerun")
	}
	return snapshot, nil
}

// ValidateSourceAnalysisOperationContract ties operation selectors to their
// immutable snapshot before admission or claim. A missing mode is rejected.
func ValidateSourceAnalysisOperationContract(operation *Operation) (SourceAnalysisOperationSnapshot, error) {
	if operation == nil || operation.Kind != analysisSourceOperationKind {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("validate source analysis operation: operation must be a source analysis")
	}
	if operation.SourceAnalysisMode == "" {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("validate source analysis operation: normalized mode is required")
	}
	snapshot, err := DecodeSourceAnalysisOperationSnapshot(operation.InputSnapshot)
	if err != nil {
		return SourceAnalysisOperationSnapshot{}, err
	}
	selectorsMatch := sameOptionalUUID(operation.TargetWorkID, snapshot.TargetWorkID) && sameOptionalString(operation.TargetStep, snapshot.TargetStep)
	terminal := operation.State == "succeeded" || operation.State == "failed"
	if terminal {
		selectorsMatch = selectorsMatch || operation.TargetWorkID == nil && operation.TargetStep == nil
	}
	if operation.SourceAnalysisMode != snapshot.Mode || !selectorsMatch ||
		(!terminal && operation.RerunTarget != *snapshot.RerunTarget) ||
		(terminal && (operation.TargetSourceRootID != nil || operation.TargetSourceLocationID != nil || operation.ToolsReadRequired || operation.RerunTarget)) {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("validate source analysis operation: operation selectors do not match its snapshot")
	}
	return snapshot, nil
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func hasSourceAnalysisTool(tools []SourceAnalysisToolSelection, packageKind, executable string) bool {
	for _, tool := range tools {
		if tool.PackageKind == packageKind && tool.Executable == executable {
			return true
		}
	}
	return false
}
