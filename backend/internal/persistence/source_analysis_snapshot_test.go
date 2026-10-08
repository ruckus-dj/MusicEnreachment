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

func TestDecodeSourceAnalysisSnapshotContainsOnlyDurableIntent(t *testing.T) {
	snapshot := validSingleStepSnapshotWithTool()
	raw := marshalSnapshotForTest(t, snapshot)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"tools", "tools_read_required", "sha256_enabled", "cache_only_reuse"} {
		if _, exists := fields[forbidden]; exists {
			t.Fatalf("runtime field %q was serialized: %s", forbidden, raw)
		}
	}
	if _, err := DecodeSourceAnalysisOperationSnapshot(raw); err != nil {
		t.Fatalf("minimal durable intent rejected: %v", err)
	}
	for _, forbidden := range []string{"tools", "tools_read_required", "sha256_enabled", "cache_only_reuse"} {
		legacy := append(raw[:len(raw)-1], []byte(`,"`+forbidden+`":true}`)...)
		if _, err := DecodeSourceAnalysisOperationSnapshot(legacy); err == nil {
			t.Errorf("legacy runtime field %q was accepted", forbidden)
		}
	}
}

func TestDecodeSourceAnalysisBatchRequiresExplicitWorkSelection(t *testing.T) {
	workID := uuid.New()
	rerun := false
	base := SourceAnalysisOperationSnapshot{
		SchemaVersion: SourceAnalysisOperationSnapshotVersion, Mode: SourceAnalysisModeBatch,
		WorkIDs: []uuid.UUID{workID}, RerunTarget: &rerun,
		SelectedSteps: []SourceAnalysisStepSelection{{WorkID: workID, Step: SourceStepSHA256}},
	}
	if _, err := decodeSnapshotForTest(t, base); err != nil {
		t.Fatalf("valid work selection rejected: %v", err)
	}
	base.WorkIDs = append(base.WorkIDs, uuid.New())
	if _, err := decodeSnapshotForTest(t, base); err == nil {
		t.Fatal("batch with multiple work items was accepted")
	}
	base.WorkIDs = base.WorkIDs[:1]
	base.SelectedSteps = nil
	if _, err := decodeSnapshotForTest(t, base); err == nil {
		t.Fatal("batch without explicit selected steps was accepted")
	}
}

func TestDecodeSourceAnalysisBatchRetainsSelectedStepIntent(t *testing.T) {
	workID := uuid.New()
	rerun := false
	snapshot := SourceAnalysisOperationSnapshot{
		SchemaVersion: SourceAnalysisOperationSnapshotVersion, Mode: SourceAnalysisModeBatch,
		WorkIDs: []uuid.UUID{workID}, RerunTarget: &rerun,
		SelectedSteps: []SourceAnalysisStepSelection{{WorkID: workID, Step: SourceStepProbe}},
	}
	decoded, err := decodeSnapshotForTest(t, snapshot)
	if err != nil {
		t.Fatalf("decode batch intent: %v", err)
	}
	if len(decoded.SelectedSteps) != 1 || decoded.SelectedSteps[0] != snapshot.SelectedSteps[0] {
		t.Fatalf("selected steps = %+v, want %+v", decoded.SelectedSteps, snapshot.SelectedSteps)
	}
	snapshot.SelectedSteps[0].Step = "unknown"
	if _, err := decodeSnapshotForTest(t, snapshot); err == nil {
		t.Fatal("invalid selected step was accepted")
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
			name:    "malformed JSON",
			prepare: func() json.RawMessage { return json.RawMessage(`{"schema_version":`) },
			wantErr: true,
		},
		{
			name: "unrelated future fields remain ignored",
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
