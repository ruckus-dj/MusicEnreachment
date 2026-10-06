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

// SourceAnalysisOperationSnapshot pins the selected work and tool identities
// for a normalized source-analysis operation. Workers must use this persisted
// selection rather than re-reading the active tool configuration.
type SourceAnalysisOperationSnapshot struct {
	SchemaVersion           int                           `json:"schema_version"`
	Mode                    string                        `json:"mode"`
	WorkIDs                 []uuid.UUID                   `json:"work_ids"`
	TargetWorkID            *uuid.UUID                    `json:"target_work_id,omitempty"`
	TargetStep              *string                       `json:"target_step,omitempty"`
	RerunTarget             *bool                         `json:"rerun_target"`
	SHA256Enabled           *bool                         `json:"sha256_enabled"`
	ToolsReadRequired       bool                          `json:"tools_read_required"`
	CacheOnlyReuse          *bool                         `json:"cache_only_reuse"`
	CacheOnlyFFProbeVersion string                        `json:"cache_only_ffprobe_version,omitempty"`
	CacheOnlyFPCalcVersion  string                        `json:"cache_only_fpcalc_version,omitempty"`
	Tools                   []SourceAnalysisToolSelection `json:"tools"`
	SelectedSteps           []SourceAnalysisStepSelection `json:"selected_steps,omitempty"`
}

// SourceAnalysisStepSelection identifies one exact work/step tuple selected
// by a batch operation. Nil means the initial batch selects every pending step.
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
	_, selectedPresent := fields["selected_steps"]
	if selectedPresent && snapshot.SelectedSteps == nil {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: selected_steps must be an array when present")
	}
	if snapshot.SchemaVersion != SourceAnalysisOperationSnapshotVersion {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: unsupported schema version %d", snapshot.SchemaVersion)
	}
	if snapshot.SHA256Enabled == nil || snapshot.CacheOnlyReuse == nil || snapshot.RerunTarget == nil {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: sha256 selection, rerun intent, and cache-only intent are required")
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
		if snapshot.TargetWorkID != nil || snapshot.TargetStep != nil {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: batch operations cannot have a single-step target")
		}
		if selectedPresent {
			if len(snapshot.SelectedSteps) == 0 {
				return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: selected_steps cannot be empty")
			}
			selectedWorks := make(map[uuid.UUID]struct{}, len(snapshot.SelectedSteps))
			selectedTuples := make(map[SourceAnalysisStepSelection]struct{}, len(snapshot.SelectedSteps))
			for _, selection := range snapshot.SelectedSteps {
				if selection.WorkID == uuid.Nil || !validSourceStep(selection.Step) {
					return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: selected work and step must be valid")
				}
				if _, exists := seen[selection.WorkID]; !exists {
					return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: selected work is not pinned")
				}
				if selection.Step == SourceStepSHA256 && !*snapshot.SHA256Enabled {
					return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: sha256 is disabled")
				}
				if _, exists := selectedTuples[selection]; exists {
					return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: duplicate selected work and step")
				}
				selectedTuples[selection] = struct{}{}
				selectedWorks[selection.WorkID] = struct{}{}
			}
			if len(selectedWorks) != len(snapshot.WorkIDs) {
				return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: selected work identities must exactly match the pinned work selection")
			}
		}
	} else {
		if selectedPresent {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: single-step operations cannot have selected_steps")
		}
		if snapshot.TargetWorkID == nil || *snapshot.TargetWorkID == uuid.Nil || snapshot.TargetStep == nil || !validSourceStep(SourceStepName(*snapshot.TargetStep)) {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: single-step work and step are required")
		}
		if _, exists := seen[*snapshot.TargetWorkID]; !exists {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: target work is not in the pinned work selection")
		}
		if *snapshot.RerunTarget && SourceStepName(*snapshot.TargetStep) != SourceStepFingerprint {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: only fingerprint may be explicitly rerun")
		}
		if SourceStepName(*snapshot.TargetStep) == SourceStepProbe && !hasSourceAnalysisTool(snapshot.Tools, "ffmpeg", "ffprobe") && !*snapshot.CacheOnlyReuse {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: probe requires a pinned ffprobe selection or explicit cache-only reuse")
		}
		if SourceStepName(*snapshot.TargetStep) == SourceStepFingerprint && !hasSourceAnalysisTool(snapshot.Tools, "fpcalc", "fpcalc") && !*snapshot.CacheOnlyReuse {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: fingerprint requires a pinned fpcalc selection or explicit cache-only reuse")
		}
	}
	if snapshot.Mode == SourceAnalysisModeBatch && *snapshot.RerunTarget {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: batch operations cannot request a targeted rerun")
	}
	if snapshot.ToolsReadRequired != (len(snapshot.Tools) > 0) {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: tool selection must match tools_read_required")
	}
	if snapshot.ToolsReadRequired && *snapshot.CacheOnlyReuse {
		return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: tool execution and cache-only reuse intent are contradictory")
	}
	if *snapshot.CacheOnlyReuse && snapshot.Mode == SourceAnalysisModeSingleStep {
		if *snapshot.TargetStep == string(SourceStepProbe) && snapshot.CacheOnlyFFProbeVersion == "" {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: cache-only probe version is required")
		}
		if *snapshot.TargetStep == string(SourceStepFingerprint) && snapshot.CacheOnlyFPCalcVersion == "" {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: cache-only fingerprint version is required")
		}
	}
	seenTools := make(map[string]struct{}, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		if tool.InstallationID == uuid.Nil || tool.RelativePath == "" || tool.Executable == "" || tool.Version == "" || tool.VersionBanner == "" {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: complete verified tool metadata is required")
		}
		if (tool.PackageKind != "ffmpeg" || (tool.Executable != "ffmpeg" && tool.Executable != "ffprobe")) &&
			(tool.PackageKind != "fpcalc" || tool.Executable != "fpcalc") {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: executable does not match its package")
		}
		key := tool.PackageKind + ":" + tool.InstallationID.String() + ":" + tool.Executable
		if _, exists := seenTools[key]; exists {
			return SourceAnalysisOperationSnapshot{}, fmt.Errorf("decode source analysis operation snapshot: duplicate tool selection %s", key)
		}
		seenTools[key] = struct{}{}
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
		(!terminal && (operation.ToolsReadRequired != snapshot.ToolsReadRequired || operation.RerunTarget != *snapshot.RerunTarget)) ||
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
