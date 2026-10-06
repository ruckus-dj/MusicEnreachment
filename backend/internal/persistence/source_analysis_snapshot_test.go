package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestDecodeSourceAnalysisOperationSnapshotRequiresExactTargetStep(t *testing.T) {
	snapshot := validSingleStepSnapshot()
	snapshot.TargetStep = nil
	if _, err := decodeSnapshotForTest(t, snapshot); err == nil {
		t.Fatal("snapshot without an exact target step was accepted")
	}
}

func TestDecodeSourceAnalysisSnapshotRequiresExplicitCacheOnlyForUnpinnedProbe(t *testing.T) {
	snapshot := validSingleStepSnapshot()
	if _, err := decodeSnapshotForTest(t, snapshot); err == nil {
		t.Fatal("probe without a pinned tool or cache-only intent was accepted")
	}
	cacheOnly := true
	snapshot.CacheOnlyReuse = &cacheOnly
	snapshot.CacheOnlyFFProbeVersion = "ffprobe-7.1"
	if _, err := decodeSnapshotForTest(t, snapshot); err != nil {
		t.Fatalf("explicit cache-only probe selection: %v", err)
	}
}

func TestDecodeSourceAnalysisBatchSelectedSteps(t *testing.T) {
	workA, workB := uuid.New(), uuid.New()
	sha, rerun, cacheOnly := true, false, false
	base := SourceAnalysisOperationSnapshot{
		SchemaVersion: SourceAnalysisOperationSnapshotVersion, Mode: SourceAnalysisModeBatch,
		WorkIDs: []uuid.UUID{workA, workB}, SHA256Enabled: &sha, RerunTarget: &rerun, CacheOnlyReuse: &cacheOnly,
	}
	base.SelectedSteps = []SourceAnalysisStepSelection{{WorkID: workA, Step: SourceStepSHA256}, {WorkID: workB, Step: SourceStepProbe}}
	if _, err := decodeSnapshotForTest(t, base); err != nil {
		t.Fatalf("valid exact selection rejected: %v", err)
	}
	tests := []struct {
		name string
		edit func(*SourceAnalysisOperationSnapshot)
	}{
		{"duplicate tuple", func(s *SourceAnalysisOperationSnapshot) {
			s.SelectedSteps = append(s.SelectedSteps, s.SelectedSteps[0])
		}},
		{"invalid step", func(s *SourceAnalysisOperationSnapshot) { s.SelectedSteps[0].Step = "other" }},
		{"missing work", func(s *SourceAnalysisOperationSnapshot) { s.SelectedSteps = s.SelectedSteps[:1] }},
		{"unselected pinned work", func(s *SourceAnalysisOperationSnapshot) { s.SelectedSteps[0].WorkID = uuid.New() }},
		{"disabled sha", func(s *SourceAnalysisOperationSnapshot) { disabled := false; s.SHA256Enabled = &disabled }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := base
			snapshot.SelectedSteps = append([]SourceAnalysisStepSelection(nil), base.SelectedSteps...)
			test.edit(&snapshot)
			if _, err := decodeSnapshotForTest(t, snapshot); err == nil {
				t.Fatal("invalid selected_steps accepted")
			}
		})
	}
	var rawFields map[string]json.RawMessage
	encoded := marshalSnapshotForTest(t, base)
	if err := json.Unmarshal(encoded, &rawFields); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []string{"null", "[]"} {
		rawFields["selected_steps"] = json.RawMessage(selection)
		raw, err := json.Marshal(rawFields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeSourceAnalysisOperationSnapshot(raw); err == nil {
			t.Fatalf("explicit %s selected_steps accepted", selection)
		}
	}
	single := validSingleStepSnapshotWithTool()
	single.SelectedSteps = []SourceAnalysisStepSelection{{WorkID: single.WorkIDs[0], Step: SourceStepProbe}}
	if _, err := decodeSnapshotForTest(t, single); err == nil {
		t.Fatal("single-step operation accepted selected_steps")
	}
}

func TestDecodeSourceAnalysisOperationSnapshotValidation(t *testing.T) {
	tests := []struct {
		name    string
		prepare func() json.RawMessage
		wantErr bool
	}{
		{
			name: "valid explicitly false booleans",
			prepare: func() json.RawMessage {
				return marshalSnapshotForTest(t, validSingleStepSnapshotWithTool())
			},
		},
		{
			name: "unknown schema version",
			prepare: func() json.RawMessage {
				snapshot := validSingleStepSnapshotWithTool()
				snapshot.SchemaVersion++
				return marshalSnapshotForTest(t, snapshot)
			},
			wantErr: true,
		},
		{
			name: "missing explicit boolean selections",
			prepare: func() json.RawMessage {
				snapshot := validSingleStepSnapshotWithTool()
				snapshot.RerunTarget = nil
				return marshalSnapshotForTest(t, snapshot)
			},
			wantErr: true,
		},
		{
			name: "empty work selection",
			prepare: func() json.RawMessage {
				snapshot := validSingleStepSnapshotWithTool()
				snapshot.WorkIDs = nil
				return marshalSnapshotForTest(t, snapshot)
			},
			wantErr: true,
		},
		{
			name: "nil work identity",
			prepare: func() json.RawMessage {
				snapshot := validSingleStepSnapshotWithTool()
				snapshot.WorkIDs = []uuid.UUID{uuid.Nil}
				return marshalSnapshotForTest(t, snapshot)
			},
			wantErr: true,
		},
		{
			name: "duplicate work identities",
			prepare: func() json.RawMessage {
				snapshot := validSingleStepSnapshotWithTool()
				snapshot.WorkIDs = append(snapshot.WorkIDs, snapshot.WorkIDs[0])
				return marshalSnapshotForTest(t, snapshot)
			},
			wantErr: true,
		},
		{
			name: "cache-only probe requires version",
			prepare: func() json.RawMessage {
				snapshot := validSingleStepSnapshot()
				cacheOnly := true
				snapshot.CacheOnlyReuse = &cacheOnly
				return marshalSnapshotForTest(t, snapshot)
			},
			wantErr: true,
		},
		{
			name:    "malformed JSON",
			prepare: func() json.RawMessage { return json.RawMessage(`{"schema_version":`) },
			wantErr: true,
		},
		{
			name: "unknown fields remain ignored",
			prepare: func() json.RawMessage {
				raw := marshalSnapshotForTest(t, validSingleStepSnapshotWithTool())
				return append(raw[:len(raw)-1], []byte(`,"future_field":true}`)...)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeSourceAnalysisOperationSnapshot(test.prepare())
			if (err != nil) != test.wantErr {
				t.Fatalf("DecodeSourceAnalysisOperationSnapshot() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func validSingleStepSnapshot() SourceAnalysisOperationSnapshot {
	workID := uuid.New()
	sha256Enabled, rerunTarget, cacheOnly := false, false, false
	step := string(SourceStepProbe)
	return SourceAnalysisOperationSnapshot{
		SchemaVersion: SourceAnalysisOperationSnapshotVersion,
		Mode:          SourceAnalysisModeSingleStep,
		WorkIDs:       []uuid.UUID{workID}, TargetWorkID: &workID, TargetStep: &step,
		SHA256Enabled: &sha256Enabled, RerunTarget: &rerunTarget, CacheOnlyReuse: &cacheOnly,
	}
}

func validSingleStepSnapshotWithTool() SourceAnalysisOperationSnapshot {
	snapshot := validSingleStepSnapshot()
	snapshot.ToolsReadRequired = true
	snapshot.Tools = []SourceAnalysisToolSelection{{
		PackageKind: "ffmpeg", InstallationID: uuid.New(), RelativePath: "ffmpeg/r1",
		Executable: "ffprobe", Version: "7.1", VersionBanner: "ffprobe version 7.1",
	}}
	return snapshot
}

func decodeSnapshotForTest(t *testing.T, snapshot SourceAnalysisOperationSnapshot) (SourceAnalysisOperationSnapshot, error) {
	t.Helper()
	return DecodeSourceAnalysisOperationSnapshot(marshalSnapshotForTest(t, snapshot))
}

func marshalSnapshotForTest(t *testing.T, snapshot SourceAnalysisOperationSnapshot) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
