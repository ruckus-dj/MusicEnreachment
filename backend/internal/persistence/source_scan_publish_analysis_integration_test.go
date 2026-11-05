//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestSourceScanPreparedPublicationScenarios(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
	t.Run("deferred steps", func(t *testing.T) { testPublishPreparedSourceScanAnalysisDefersMoveBlockedSteps(t, db) })
	t.Run("forged retained fingerprint", func(t *testing.T) { testApplySourceScanRejectsForgedRetainedFingerprintAtomically(t, db) })
}

func testPublishPreparedSourceScanAnalysisDefersMoveBlockedSteps(t *testing.T, db *bun.DB) {
	ctx := context.Background()
	root := publishTestRoot(t, ctx, db, "/srv/publish-deferred")
	op := publishTestScan(t, ctx, db, root.ID)
	stamp := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	sha := publishTestSHA(uuid.New(), make([]byte, 32), op.ID, stamp)
	probeVersion, policy, count := "ffprobe-test", 1, 1
	probe := &SourceMediaVariant{ID: uuid.New(), FFProbeVersion: &probeVersion, AnalysisPolicyVersion: &policy, FFProbeJSON: json.RawMessage(`{"format":{},"streams":[{"codec_type":"audio"}]}`), ObservedTags: json.RawMessage(`{}`), InspectedAt: timePointer(stamp), AppliedOperationID: &op.ID, AudioStreamCount: &count}
	prepared := &SourceScanPreparedAnalysis{Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded, SHA256Variant: sha, ProbeState: SourcePreparedSucceeded, ProbeVariant: probe, FingerprintState: SourcePreparedDeferred}
	fullyDeferred := &SourceScanPreparedAnalysis{Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded, SHA256Variant: publishTestSHA(uuid.New(), sha.SourceSHA256, op.ID, stamp), ProbeState: SourcePreparedDeferred, FingerprintState: SourcePreparedDeferred}
	waitingReason := "Source technical analysis is waiting for managed tools to become available."
	candidates := []SourceScanCandidateInput{
		{RelativePath: "deferred.flac", SizeBytes: 10, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: prepared},
		{RelativePath: "both-deferred.flac", SizeBytes: 10, Mtime: stamp, ProbeStatus: SourceProbeStatusProbeError, SafeError: &waitingReason, PreparedAnalysis: fullyDeferred},
	}
	if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		if err := insertSourceLocations(ctx, tx, *root, 1, candidates); err != nil {
			return err
		}
		return publishPreparedSourceScanAnalysis(ctx, tx, *root, *op, true, candidates)
	}); err != nil {
		t.Fatalf("publish deferred analysis: %v", err)
	}
	var shaState, probeState, fingerprintState string
	if err := db.NewRaw(`SELECT (SELECT s.state FROM source_analysis_step s JOIN source_analysis_work w ON w.id=s.work_id WHERE w.source_root_id=? AND w.relative_path='both-deferred.flac' AND s.step='sha256'), (SELECT s.state FROM source_analysis_step s JOIN source_analysis_work w ON w.id=s.work_id WHERE w.source_root_id=? AND w.relative_path='both-deferred.flac' AND s.step='probe'), (SELECT s.state FROM source_analysis_step s JOIN source_analysis_work w ON w.id=s.work_id WHERE w.source_root_id=? AND w.relative_path='both-deferred.flac' AND s.step='fingerprint')`, root.ID, root.ID, root.ID).Scan(ctx, &shaState, &probeState, &fingerprintState); err != nil {
		t.Fatal(err)
	}
	if shaState != "succeeded" || probeState != "pending" || fingerprintState != "pending" {
		t.Fatalf("published states = SHA %q, probe %q, fingerprint %q; want succeeded/pending/pending", shaState, probeState, fingerprintState)
	}

	oldLocation := SourceLocation{ID: uuid.New(), SourceRootID: root.ID, RelativePath: "old.flac", SizeBytes: 11, Mtime: stamp, LastSeenScanGeneration: 1, ProbeStatus: SourceProbeStatusAudio}
	if _, err := db.NewInsert().Model(&oldLocation).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	oldWork := &SourceAnalysisWork{ID: uuid.New(), LocationID: oldLocation.ID, SourceRootID: root.ID, ConfiguredPath: root.ConfiguredPath, InventoryPath: root.ConfiguredPath, RelativePath: oldLocation.RelativePath, SizeBytes: oldLocation.SizeBytes, Mtime: stamp, OriginScanOperationID: op.ID}
	if _, err := db.NewInsert().Model(oldWork).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	oldFingerprint := publishTestFingerprint(uuid.New(), op.ID, stamp)
	oldFingerprint.WinningResultID = oldFingerprint.ID
	if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(oldFingerprint).Exec(ctx); err != nil {
			return err
		}
		step := SourceAnalysisStep{WorkID: oldWork.ID, Step: string(SourceStepFingerprint), State: "succeeded", SuccessFingerprintResultID: &oldFingerprint.ID, SuccessReuseOrigin: stringPointer("executed")}
		_, err := tx.NewInsert().Model(&step).Exec(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	deferredAgain := &SourceScanPreparedAnalysis{Version: 1, SHA256State: SourcePreparedNotRequested, ProbeState: SourcePreparedDeferred, FingerprintState: SourcePreparedDeferred}
	oldCandidate := []SourceScanCandidateInput{{RelativePath: oldLocation.RelativePath, SizeBytes: oldLocation.SizeBytes, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: deferredAgain}}
	if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		return publishPreparedSourceScanAnalysis(ctx, tx, *root, *op, false, oldCandidate)
	}); err != nil {
		t.Fatalf("publish deferred analysis over prior success: %v", err)
	}
	var retainedState string
	if err := db.NewRaw(`SELECT state FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, oldWork.ID).Scan(ctx, &retainedState); err != nil || retainedState != "succeeded" {
		t.Fatalf("prior fingerprint state = %q, %v; want succeeded", retainedState, err)
	}
}

func publishTestRoot(t *testing.T, ctx context.Context, db *bun.DB, path string) *SourceRoot {
	t.Helper()
	path = t.TempDir()
	repository := NewSourceInventoryRepository(db)
	root := &SourceRoot{ID: uuid.New(), ConfiguredPath: path, DisplayName: "publish test", Enabled: true, Status: SourceRootStatusAvailable}
	if err := repository.CreateSourceRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewUpdate().Model((*SourceRoot)(nil)).Set("inventory_path=?", path).Set("scan_generation=?", 1).Where("id=?", root.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	root.InventoryPath = &root.ConfiguredPath
	return root
}

func publishTestScan(t *testing.T, ctx context.Context, db *bun.DB, rootID uuid.UUID, policies ...bool) *Operation {
	t.Helper()
	root, err := NewSourceInventoryRepository(db).GetSourceRoot(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(sourceScanSnapshot{
		SchemaVersion: SourceScanSnapshotVersion, SourceRootID: root.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	jobID := time.Now().UnixNano()
	op := &Operation{ID: uuid.New(), Kind: "scan_source", State: "running", Stage: "scan", InputSnapshot: snapshot, TargetSourceRootID: &rootID, Attempt: 1, RiverJobID: &jobID}
	if _, err := db.NewInsert().Model(op).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return op
}

func finishPublishTestScan(t *testing.T, ctx context.Context, db *bun.DB, scan *Operation) {
	t.Helper()
	if err := NewSourceInventoryRepository(db).FinishSourceScanDelivery(ctx, scan.ID, scan.Attempt, *scan.RiverJobID, "succeeded", "completed", ""); err != nil {
		t.Fatalf("finish fixture source scan: %v", err)
	}
}

func publishTestSHA(id uuid.UUID, digest []byte, operation uuid.UUID, calculated time.Time) *SourceMediaVariant {
	algorithm := "SHA-256"
	return &SourceMediaVariant{ID: id, SourceSHA256: digest, SHA256CalculatedAt: &calculated, SHA256Algorithm: &algorithm, SHA256AppliedOperationID: &operation}
}

func publishTestFingerprint(id, operation uuid.UUID, calculated time.Time) *SourceFingerprintResult {
	return &SourceFingerprintResult{ID: id, FPCalcVersion: "fpcalc-test", VersionBanner: "fpcalc test", AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "12345", ReportedDuration: 1, CalculatedAt: calculated, AppliedOperationID: operation, ParserContractVersion: 1}
}

func sourceScanPublicationSnapshot(t *testing.T, ctx context.Context, db *bun.DB) string {
	t.Helper()
	var snapshot string
	for _, table := range []string{
		"source_root", "source_location", "source_analysis_work", "source_analysis_step",
		"source_work_variant_identity", "media_variant", "media_fingerprint_result",
		"source_scan_candidate", "operation", "operation_tool_read_hold",
	} {
		var rows string
		query := fmt.Sprintf(`SELECT COALESCE(jsonb_agg(to_jsonb(row_data) ORDER BY to_jsonb(row_data)::text), '[]'::jsonb)::text FROM %s AS row_data`, table)
		if err := db.NewRaw(query).Scan(ctx, &rows); err != nil {
			t.Fatalf("snapshot %s rows: %v", table, err)
		}
		snapshot += table + "=" + rows + ";"
	}
	return snapshot
}
