//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestReusePendingSourceAnalysisCacheWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/pending-cache")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 4096, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepFingerprint, State: "pending"},
	)

	digest, _ := hex.DecodeString("7a00000000000000000000000000000000000000000000000000000000000000")
	originalOperation := uuid.New()
	shaVariantID := uuid.New()
	probeVariantID := shaVariantID
	inspected := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := database.NewRaw(`INSERT INTO media_variant
		(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id)
		VALUES (?,?,?,?,'SHA-256',?)`, shaVariantID, location.SizeBytes, digest, inspected, originalOperation).Exec(ctx); err != nil {
		t.Fatalf("insert current digest variant: %v", err)
	}
	if _, err := database.NewRaw(`UPDATE source_location SET media_variant_id=? WHERE id=?`, shaVariantID, location.ID).Exec(ctx); err != nil {
		t.Fatalf("select canonical digest variant: %v", err)
	}
	if _, err := database.NewRaw(`UPDATE source_analysis_step SET state='succeeded',success_sha_variant_id=?,success_reuse_origin='executed' WHERE work_id=? AND step='sha256'`, shaVariantID, work.ID).Exec(ctx); err != nil {
		t.Fatalf("select current SHA step result: %v", err)
	}
	if _, err := database.NewRaw(`UPDATE source_location SET probe_status='not_analyzed' WHERE id=?`, location.ID).Exec(ctx); err != nil {
		t.Fatalf("reset probe status before cache reuse: %v", err)
	}
	probeVersion := "ffprobe 8.0"
	analysisPolicy, audioStreams := persistence.SourceAnalysisPolicyVersion, 1
	probeVariant := &persistence.SourceMediaVariant{
		ID: shaVariantID, AnalysisPolicyVersion: &analysisPolicy, FFProbeVersion: &probeVersion,
		FFProbeJSON: json.RawMessage(`{"format":{"format_name":"flac"}}`), ObservedTags: json.RawMessage(`{}`),
		InspectedAt: &inspected, AppliedOperationID: &originalOperation, AudioStreamCount: &audioStreams,
	}
	if _, err := database.NewUpdate().Model(probeVariant).
		Column("analysis_policy_version", "ffprobe_version", "ffprobe_json", "observed_tags", "inspected_at", "applied_operation_id", "audio_stream_count").
		WherePK().Exec(ctx); err != nil {
		t.Fatalf("promote canonical SHA identity with cached probe metadata: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO media_probe_cache(source_sha256,ffprobe_version,analysis_policy_version,result_id) VALUES(?,?,?,?)`, digest, probeVersion, analysisPolicy, probeVariantID).Exec(ctx); err != nil {
		t.Fatalf("register promoted canonical probe cache result: %v", err)
	}
	move := &persistence.Operation{
		ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued",
		InputSnapshot: json.RawMessage(`{"schema_version":1,"old_root":"/srv/tools-old","new_root":"/srv/tools-new"}`),
	}
	if err := persistence.NewSetupManagerRepository(database).CreateOperation(ctx, move); err != nil {
		t.Fatalf("create active tools-root move: %v", err)
	}

	for i := 0; i < 2; i++ {
		reused, err := repository.ReusePendingSourceAnalysisCache(ctx, root.ID, work.ID, persistence.SourceStepProbe, probeVariantID, "ffprobe 8.0")
		if err != nil {
			t.Fatalf("reuse cached probe (attempt %d): %v", i, err)
		}
		if reused != (i == 0) {
			t.Fatalf("reuse cached probe (attempt %d) = %t; want %t", i, reused, i == 0)
		}
	}
	var probeState, fpState, origin string
	var selected uuid.UUID
	var lastOperation *uuid.UUID
	if err := database.NewRaw(`SELECT state,success_probe_variant_id,success_reuse_origin,last_operation_id FROM source_analysis_step WHERE work_id=? AND step='probe'`, work.ID).
		Scan(ctx, &probeState, &selected, &origin, &lastOperation); err != nil {
		t.Fatalf("read selected probe: %v", err)
	}
	if probeState != "succeeded" || selected != probeVariantID || origin != "sha256" || lastOperation != nil {
		t.Fatalf("probe selection = state %q, id %s, origin %q, last operation %v", probeState, selected, origin, lastOperation)
	}
	var probeStatus string
	if err := database.NewRaw(`SELECT probe_status FROM source_location WHERE id=?`, location.ID).Scan(ctx, &probeStatus); err != nil || probeStatus != "audio" {
		t.Fatalf("probe status after cache reuse = %q, %v; want audio", probeStatus, err)
	}
	var retainedProvenance uuid.UUID
	var retainedInspection time.Time
	if err := database.NewRaw(`SELECT applied_operation_id,inspected_at FROM media_variant WHERE id=?`, probeVariantID).Scan(ctx, &retainedProvenance, &retainedInspection); err != nil || retainedProvenance != originalOperation || !retainedInspection.Equal(inspected) {
		t.Fatalf("cached result provenance = %s inspected=%s, %v; want original operation %s and inspection %s", retainedProvenance, retainedInspection, err, originalOperation, inspected)
	}
	var retainedSHA []byte
	var retainedSHACalculated time.Time
	var retainedSHAAlgorithm string
	var retainedSHAOperation uuid.UUID
	if err := database.NewRaw(`SELECT source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id FROM media_variant WHERE id=?`, shaVariantID).
		Scan(ctx, &retainedSHA, &retainedSHACalculated, &retainedSHAAlgorithm, &retainedSHAOperation); err != nil {
		t.Fatalf("read immutable canonical SHA provenance: %v", err)
	}
	if !bytes.Equal(retainedSHA, digest) || !retainedSHACalculated.Equal(inspected) || retainedSHAAlgorithm != "SHA-256" || retainedSHAOperation != originalOperation {
		t.Fatalf("canonical SHA identity changed during probe cache fixture: digest=%x calculated=%s algorithm=%q operation=%s", retainedSHA, retainedSHACalculated, retainedSHAAlgorithm, retainedSHAOperation)
	}
	if err := database.NewRaw(`SELECT state FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, work.ID).Scan(ctx, &fpState); err != nil {
		t.Fatalf("read independent fingerprint step: %v", err)
	}
	if fpState != "pending" {
		t.Fatalf("fingerprint state = %q; want pending", fpState)
	}
	if reused, err := repository.ReusePendingSourceAnalysisCache(ctx, root.ID, work.ID, persistence.SourceStepFingerprint, uuid.New(), "1.5.1"); err != nil || reused {
		t.Fatalf("reuse missing fingerprint cache = %t, %v; want false without error", reused, err)
	}
	if _, err := database.NewRaw(`UPDATE source_location SET size_bytes=size_bytes+1 WHERE id=?`, location.ID).Exec(ctx); err != nil {
		t.Fatalf("change current location stat: %v", err)
	}
	if reused, err := repository.ReusePendingSourceAnalysisCache(ctx, root.ID, work.ID, persistence.SourceStepFingerprint, uuid.New(), "1.5.1"); err != nil || reused {
		t.Fatalf("reuse after current stat changed = %t, %v; want false without error", reused, err)
	}
	if _, err := database.NewRaw(`UPDATE source_location SET size_bytes=? WHERE id=?`, location.SizeBytes, location.ID).Exec(ctx); err != nil {
		t.Fatalf("restore current location stat: %v", err)
	}
	var canonicalID uuid.UUID
	if err := database.NewRaw(`SELECT media_variant_id FROM source_location WHERE id=?`, location.ID).Scan(ctx, &canonicalID); err != nil || canonicalID != shaVariantID {
		t.Fatalf("canonical location SHA identity = %s, %v; want %s", canonicalID, err, shaVariantID)
	}
	var jobs, workHolds, toolHolds, executions int
	if err := database.NewRaw(`SELECT
		(SELECT count(*) FROM river_job),
		(SELECT count(*) FROM operation_source_work_hold),
		(SELECT count(*) FROM operation_tool_read_hold),
		(SELECT count(*) FROM source_analysis_step WHERE work_id=? AND execution_operation_id IS NOT NULL)`, work.ID).
		Scan(ctx, &jobs, &workHolds, &toolHolds, &executions); err != nil {
		t.Fatalf("check cache reuse side effects: %v", err)
	}
	if jobs != 0 || workHolds != 0 || toolHolds != 0 || executions != 0 {
		t.Fatalf("cache reuse created jobs, holds or execution attribution: %d, %d, %d, %d", jobs, workHolds, toolHolds, executions)
	}

	// A failed target is not an implicit retry target, even when its cache entry
	// and digest remain valid.
	if _, err := database.NewRaw(`UPDATE source_analysis_step SET state='failed',safe_error='prior failure' WHERE work_id=? AND step='probe'`, work.ID).Exec(ctx); err != nil {
		t.Fatalf("mark probe failed: %v", err)
	}
	if reused, err := repository.ReusePendingSourceAnalysisCache(ctx, root.ID, work.ID, persistence.SourceStepProbe, probeVariantID, "ffprobe 8.0"); err != nil || reused {
		t.Fatalf("reuse failed probe = %t, %v; want false without error", reused, err)
	}
}
