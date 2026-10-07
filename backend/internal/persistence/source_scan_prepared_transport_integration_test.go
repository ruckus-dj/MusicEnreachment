//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestSourceScanPreparedAnalysisCandidateTransportWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	manager := persistence.NewSetupManagerRepository(database)
	root := createSourceRoot(t, ctx, manager, database, "/srv/prepared", "Prepared")
	operation := createScanOperation(t, ctx, manager, root.ID)

	policy, audioCount := 2, 0
	probeVersion := "ffprobe 1"
	prepared := &persistence.SourceScanPreparedAnalysis{
		Version: 1, HashRequested: true, SHA256State: persistence.SourcePreparedFailed,
		SHA256SafeError: "safe hash failure", ProbeState: persistence.SourcePreparedSucceeded,
		ProbeVariant: &persistence.SourceMediaVariant{
			ID: uuid.New(), AnalysisPolicyVersion: &policy, FFProbeVersion: &probeVersion,
			FFProbeJSON: json.RawMessage(`{"format":{},"streams":[]}`), ObservedTags: json.RawMessage(`{}`),
			InspectedAt: preparedTimePointer(time.Unix(20, 0).UTC()), AppliedOperationID: preparedUUIDPointer(operation.ID), AudioStreamCount: &audioCount,
		},
		FingerprintState: persistence.SourcePreparedSucceeded,
		FingerprintResult: &persistence.SourceFingerprintResult{
			ID: uuid.New(), FPCalcVersion: "1.5.1", VersionBanner: "fpcalc 1.5.1",
			AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "prepared-fingerprint",
			CalculatedAt: time.Unix(21, 0).UTC(), AppliedOperationID: operation.ID, ParserContractVersion: 1,
		},
		OriginalLocationID: preparedUUIDPointer(uuid.New()), OriginalMediaVariantID: preparedUUIDPointer(uuid.New()),
		OriginalScanOperationID: preparedUUIDPointer(uuid.New()), SuccessorLocationID: preparedUUIDPointer(uuid.New()),
	}
	if err := prepared.Validate(); err != nil {
		t.Fatalf("test prepared DTO is invalid: %v", err)
	}

	mtime := time.Unix(10, 0).UTC().Truncate(time.Microsecond)
	safeProbeError := "probe is unavailable"
	inputs := []persistence.SourceScanCandidateInput{
		{RelativePath: "album/prepared.flac", SizeBytes: 12, Mtime: mtime, ProbeStatus: "audio", PreparedAnalysis: prepared},
		{RelativePath: "album/plain.flac", SizeBytes: 13, Mtime: mtime, ProbeStatus: "probe_error", SafeError: &safeProbeError},
	}
	if err := repository.AppendSourceScanCandidates(ctx, operation.ID, inputs[:1]); err != nil {
		t.Fatalf("append prepared candidate: %v", err)
	}
	if err := repository.AppendSourceScanCandidates(ctx, operation.ID, inputs[1:]); err != nil {
		t.Fatalf("append nullable candidate: %v", err)
	}

	var got []persistence.SourceScanCandidateInput
	if err := database.NewRaw(`SELECT relative_path, size_bytes, mtime, probe_status, safe_error, source_sha256, sha256_calculated_at,
		sha256_applied_operation_id, audio_stream_count, ffprobe_version, ffprobe_json, analysis_policy_version,
		observed_tags, inspected_at, probe_applied_operation_id, prepared_analysis
		FROM source_scan_candidate WHERE operation_id = ? ORDER BY relative_path`, operation.ID).Scan(ctx, &got); err != nil {
		t.Fatalf("read durable candidate rows: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(got))
	}
	// The plain path sorts before the prepared path; compare by identity.
	if got[0].RelativePath == inputs[1].RelativePath {
		got[0], got[1] = got[1], got[0]
	}
	if got[0].RelativePath != inputs[0].RelativePath || got[1].RelativePath != inputs[1].RelativePath {
		t.Fatalf("candidate paths = %q, %q", got[0].RelativePath, got[1].RelativePath)
	}
	if got[0].PreparedAnalysis == nil {
		t.Fatal("prepared_analysis was NULL after transport")
	}
	if !reflectPreparedAnalysisEqual(t, prepared, got[0].PreparedAnalysis) {
		t.Fatal("prepared analysis changed during JSONB transport")
	}
	if got[1].PreparedAnalysis != nil {
		t.Fatalf("omitted prepared analysis = %#v, want NULL", got[1].PreparedAnalysis)
	}

	var locationCount int
	if err := database.NewSelect().Model((*persistence.SourceLocation)(nil)).ColumnExpr("count(*)").Where("source_root_id = ?", root.ID).Scan(ctx, &locationCount); err != nil {
		t.Fatalf("count public locations: %v", err)
	}
	if locationCount != 0 {
		t.Fatalf("candidate transport published %d locations before apply", locationCount)
	}
	var state, stage string
	if err := database.NewRaw(`SELECT state, stage FROM operation WHERE id = ?`, operation.ID).Scan(ctx, &state, &stage); err != nil {
		t.Fatalf("read operation after candidate transport: %v", err)
	}
	if state != "queued" || stage != "queued" {
		t.Fatalf("candidate transport changed operation state/stage to %q/%q", state, stage)
	}
	var steps int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE execution_operation_id = ?`, operation.ID).Scan(ctx, &steps); err != nil {
		t.Fatalf("count operation steps: %v", err)
	}
	if steps != 0 {
		t.Fatalf("candidate transport created %d operation steps", steps)
	}

	// Replace persists transactionally; a DTO rejected during JSON marshaling must
	// not delete the previous durable batch.
	invalid := *prepared
	invalid.FingerprintResult = nil
	invalid.FingerprintState = persistence.SourcePreparedSucceeded
	if err := repository.ReplaceSourceScanCandidates(ctx, operation.ID, []persistence.SourceScanCandidateInput{
		{RelativePath: "bad.flac", SizeBytes: 1, Mtime: mtime, ProbeStatus: "audio", PreparedAnalysis: &invalid},
	}); err == nil {
		t.Fatal("replace accepted invalid prepared analysis")
	}
	var after int
	if err := database.NewRaw(`SELECT count(*) FROM source_scan_candidate WHERE operation_id = ?`, operation.ID).Scan(ctx, &after); err != nil {
		t.Fatalf("count candidates after rejected replace: %v", err)
	}
	if after != 2 {
		t.Fatalf("failed replacement left %d candidates, want original batch of 2", after)
	}
}

func reflectPreparedAnalysisEqual(t *testing.T, want, got *persistence.SourceScanPreparedAnalysis) bool {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal expected prepared analysis: %v", err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal transported prepared analysis: %v", err)
	}
	return bytes.Equal(wantJSON, gotJSON)
}

func preparedTimePointer(value time.Time) *time.Time { return &value }
func preparedUUIDPointer(value uuid.UUID) *uuid.UUID { return &value }
