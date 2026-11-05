//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func testApplySourceScanRejectsForgedRetainedFingerprintAtomically(t *testing.T, db *bun.DB) {
	ctx := context.Background()
	root := publishTestRoot(t, ctx, db, "/srv/forged-retained-analysis")
	previousScan := publishTestScan(t, ctx, db, root.ID)
	stamp := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	location, work, sha, _, selected := insertRetainedAnalysisFixture(t, ctx, db, root, previousScan, stamp)
	if err := insertRetainedFailureSteps(ctx, db, work, selected, false); err != nil {
		t.Fatal(err)
	}
	forged := publishTestFingerprint(uuid.New(), previousScan.ID, stamp.Add(time.Second))
	if _, err := db.NewInsert().Model(forged).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	finishPublishTestScan(t, ctx, db, previousScan)
	scan := publishTestScan(t, ctx, db, root.ID, false)
	prepared := &SourceScanPreparedAnalysis{
		Version: 1, HashRequested: false, SHA256State: SourcePreparedNotRequested,
		RetainedSHA256Variant: sha, ProbeState: SourcePreparedDeferred,
		FingerprintState: SourcePreparedSucceeded, FingerprintResult: forged,
		ReusedImmutableIDs: []uuid.UUID{forged.ID}, OriginalLocationID: &location.ID, OriginalMediaVariantID: location.MediaVariantID,
	}
	candidates := []SourceScanCandidateInput{{RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: prepared}}
	if err := NewSourceInventoryRepository(db).ReplaceSourceScanCandidates(ctx, scan.ID, candidates); err != nil {
		t.Fatal(err)
	}
	var generationBefore, locationGenerationBefore int64
	var candidateCountBefore, fingerprintCountBefore int
	if err := db.NewRaw(`SELECT scan_generation FROM source_root WHERE id=?`, root.ID).Scan(ctx, &generationBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT last_seen_scan_generation FROM source_location WHERE id=?`, location.ID).Scan(ctx, &locationGenerationBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT (SELECT count(*) FROM source_scan_candidate WHERE operation_id=?),(SELECT count(*) FROM media_fingerprint_result)`, scan.ID).Scan(ctx, &candidateCountBefore, &fingerprintCountBefore); err != nil {
		t.Fatal(err)
	}
	beforeApply := sourceScanPublicationSnapshot(t, ctx, db)
	apply := SourceScanApply{OperationID: scan.ID, ExpectedConfiguredPath: root.ConfiguredPath, ExpectedAttempt: scan.Attempt, ExpectedJobID: *scan.RiverJobID, SHA256Enabled: sourceAnalysisBoolPointer(false)}
	if err := NewSourceInventoryRepository(db).ApplySourceScan(ctx, apply); err == nil {
		t.Fatal("forged retained fingerprint unexpectedly applied")
	}
	var generation int64
	var locationGeneration int64
	var candidateCount, fingerprintCount int
	if err := db.NewRaw(`SELECT scan_generation FROM source_root WHERE id=?`, root.ID).Scan(ctx, &generation); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT last_seen_scan_generation FROM source_location WHERE id=?`, location.ID).Scan(ctx, &locationGeneration); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT (SELECT count(*) FROM source_scan_candidate WHERE operation_id=?),(SELECT count(*) FROM media_fingerprint_result)`, scan.ID).Scan(ctx, &candidateCount, &fingerprintCount); err != nil {
		t.Fatal(err)
	}
	if generation != generationBefore || locationGeneration != locationGenerationBefore || candidateCount != candidateCountBefore || fingerprintCount != fingerprintCountBefore {
		t.Fatalf("failed apply mutated inventory: before=%d/%d/%d/%d after=%d/%d/%d/%d", generationBefore, locationGenerationBefore, candidateCountBefore, fingerprintCountBefore, generation, locationGeneration, candidateCount, fingerprintCount)
	}
	if afterApply := sourceScanPublicationSnapshot(t, ctx, db); afterApply != beforeApply {
		t.Fatalf("rejected forged retained fingerprint changed source tables:\nbefore %s\nafter  %s", beforeApply, afterApply)
	}
}

func insertRetainedAnalysisFixture(t *testing.T, ctx context.Context, db *bun.DB, root *SourceRoot, scan *Operation, stamp time.Time) (*SourceLocation, *SourceAnalysisWork, *SourceMediaVariant, *SourceMediaVariant, *SourceFingerprintResult) {
	t.Helper()
	const size int64 = 20
	digest := make([]byte, 32)
	digest[0] = 0x81
	shaInput := publishTestSHA(uuid.New(), digest, scan.ID, stamp)
	probeVersion, policy, audioStreams := "ffprobe-test", 1, 1
	probeInspected := stamp.Add(time.Second)
	probeInput := &SourceMediaVariant{
		ID: uuid.New(), FFProbeVersion: &probeVersion,
		AnalysisPolicyVersion: &policy,
		FFProbeJSON:           json.RawMessage(`{"format":{},"streams":[{"codec_type":"audio"}]}`),
		ObservedTags:          json.RawMessage(`{}`), InspectedAt: &probeInspected,
		AppliedOperationID: &scan.ID, AudioStreamCount: &audioStreams,
	}
	fingerprintInput := publishTestFingerprint(uuid.New(), scan.ID, stamp.Add(2*time.Second))
	prepared := &SourceScanPreparedAnalysis{
		Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded, SHA256Variant: shaInput,
		ProbeState: SourcePreparedSucceeded, ProbeVariant: probeInput,
		FingerprintState: SourcePreparedSucceeded, FingerprintResult: fingerprintInput,
	}
	candidate := []SourceScanCandidateInput{{
		RelativePath: "unchanged.flac", SizeBytes: size, Mtime: stamp,
		ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: prepared,
	}}
	publishAndApplyRetainedCandidate(t, ctx, db, root, scan, candidate, true)

	location := new(SourceLocation)
	if err := db.NewRaw(`SELECT * FROM source_location WHERE source_root_id=? AND relative_path=?`, root.ID, candidate[0].RelativePath).Scan(ctx, location); err != nil {
		t.Fatal(err)
	}
	if location.MediaVariantID == nil {
		t.Fatal("first scan did not select a canonical SHA/probe result")
	}
	work := new(SourceAnalysisWork)
	if err := db.NewRaw(`SELECT * FROM source_analysis_work WHERE location_id=?`, location.ID).Scan(ctx, work); err != nil {
		t.Fatal(err)
	}
	var selectedSHAID, selectedProbeID, selectedFingerprintID uuid.UUID
	if err := db.NewRaw(`SELECT success_sha_variant_id FROM source_analysis_step WHERE work_id=? AND step='sha256'`, work.ID).Scan(ctx, &selectedSHAID); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT success_probe_variant_id FROM source_analysis_step WHERE work_id=? AND step='probe'`, work.ID).Scan(ctx, &selectedProbeID); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT success_fingerprint_result_id FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, work.ID).Scan(ctx, &selectedFingerprintID); err != nil {
		t.Fatal(err)
	}
	if selectedSHAID != *location.MediaVariantID {
		t.Fatalf("selected SHA variant %s differs from location's canonical link %s", selectedSHAID, *location.MediaVariantID)
	}
	sha := new(SourceMediaVariant)
	if err := db.NewRaw(`SELECT * FROM media_variant WHERE id=?`, selectedSHAID).Scan(ctx, sha); err != nil {
		t.Fatal(err)
	}
	probe := new(SourceMediaVariant)
	if err := db.NewRaw(`SELECT * FROM media_variant WHERE id=?`, selectedProbeID).Scan(ctx, probe); err != nil {
		t.Fatal(err)
	}
	fingerprint := new(SourceFingerprintResult)
	if err := db.NewRaw(`SELECT * FROM media_fingerprint_result WHERE id=?`, selectedFingerprintID).Scan(ctx, fingerprint); err != nil {
		t.Fatal(err)
	}
	return location, work, sha, probe, fingerprint
}

func insertRetainedFailureSteps(ctx context.Context, db *bun.DB, work *SourceAnalysisWork, fingerprint *SourceFingerprintResult, probe bool) error {
	if probe {
		message := "previous probe failed"
		if _, err := db.NewRaw(`UPDATE source_analysis_step SET state='failed',safe_error=?,input_snapshot=?::jsonb WHERE work_id=? AND step='probe'`, message, `{"pinned":"old-probe"}`, work.ID).Exec(ctx); err != nil {
			return err
		}
	}
	message := "latest fingerprint attempt failed"
	_, err := db.NewRaw(`UPDATE source_analysis_step SET state='failed',safe_error=?,success_fingerprint_result_id=?,input_snapshot=?::jsonb WHERE work_id=? AND step='fingerprint'`, message, fingerprint.ID, `{"pinned":"previous"}`, work.ID).Exec(ctx)
	if err != nil {
		return err
	}
	return nil
}

func publishAndApplyRetainedCandidate(t *testing.T, ctx context.Context, db *bun.DB, root *SourceRoot, scan *Operation, candidates []SourceScanCandidateInput, sha256Enabled bool) {
	t.Helper()
	if err := NewSourceInventoryRepository(db).ReplaceSourceScanCandidates(ctx, scan.ID, candidates); err != nil {
		t.Fatal(err)
	}
	returnErr := NewSourceInventoryRepository(db).ApplySourceScan(ctx, SourceScanApply{OperationID: scan.ID, ExpectedConfiguredPath: root.ConfiguredPath, ExpectedAttempt: scan.Attempt, ExpectedJobID: *scan.RiverJobID, SHA256Enabled: &sha256Enabled})
	if returnErr != nil {
		t.Fatalf("apply fixture source scan: %v", returnErr)
	}
}
