//go:build integration

package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestSourceAnalysisStepAppliesAndPromotionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/step-apply")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 4096, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepFingerprint, State: "pending"},
	)
	probeTool := insertVerifiedAnalysisTool(t, ctx, database, "ffmpeg", "ffprobe", "7.1.2", "ffprobe version 7.1.2")
	fingerprintTool := insertVerifiedAnalysisTool(t, ctx, database, "fpcalc", "fpcalc", "1.5.1", "fpcalc version 1.5.1")
	tools := []persistence.SourceAnalysisToolSelection{probeTool, fingerprintTool}
	fixture := newAnalysisStepFixture(t, ctx, database, repository, client, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, false, tools)
	operation := fixture.operation
	jobID := *operation.RiverJobID

	shaAttempt := fixture.claim(persistence.SourceStepSHA256)
	digest := make([]byte, 32)
	digest[0] = 0x7a
	shaApply := persistence.SourceSHA256Apply{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: jobID, StepAttempt: shaAttempt, SHA256: digest,
		CalculatedAt: time.Now().UTC().Truncate(time.Microsecond), Algorithm: "SHA-256",
	}
	probeAttempt := fixture.claim(persistence.SourceStepProbe)
	probe := persistence.SourceProbeApply{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: jobID, StepAttempt: probeAttempt, SizeBytes: location.SizeBytes,
		AnalysisPolicy: persistence.SourceAnalysisPolicyVersion, FFProbeVersion: "7.1.2",
		FFProbeJSON:  json.RawMessage(`{"format":{"format_name":"flac"}}`),
		ObservedTags: json.RawMessage(`{"ARTIST":["fixture"]}`), InspectedAt: time.Now().UTC().Truncate(time.Microsecond), AudioStreamCount: 1,
	}
	probeResult, err := repository.ApplySourceProbe(ctx, probe)
	if err != nil {
		t.Fatalf("apply probe: %v", err)
	}
	if duplicate, err := repository.ApplySourceProbe(ctx, probe); err != nil || duplicate.ID != probeResult.ID {
		t.Fatalf("idempotent probe apply = %v, %v; want result %s", duplicate, err, probeResult.ID)
	}
	changedProbe := probe
	changedProbe.FFProbeVersion = "different"
	if _, err := repository.ApplySourceProbe(ctx, changedProbe); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("same-fence probe with changed result = %v, want stale refusal", err)
	}
	canonical, err := repository.ApplySourceSHA256(ctx, shaApply)
	if err != nil {
		t.Fatalf("apply SHA-256: %v", err)
	}
	var lookupDigest [sha256.Size]byte
	copy(lookupDigest[:], digest)
	cachedProbe, found, err := repository.LookupSourceProbe(ctx, lookupDigest, probe.FFProbeVersion, probe.AnalysisPolicy)
	if err != nil || !found || cachedProbe.ID != probeResult.ID {
		t.Fatalf("probe cache after SHA promotion = %+v, %v, %v; want original selected result %s", cachedProbe, found, err, probeResult.ID)
	}
	if canonical.FFProbeVersion == nil || *canonical.FFProbeVersion != probe.FFProbeVersion || canonical.AppliedOperationID == nil || *canonical.AppliedOperationID != operation.ID {
		t.Fatalf("returned canonical digest omitted promoted probe provenance: %+v", canonical)
	}
	if duplicate, err := repository.ApplySourceSHA256(ctx, shaApply); err != nil || duplicate.ID != canonical.ID {
		t.Fatalf("idempotent SHA apply = %v, %v; want canonical %s", duplicate, err, canonical.ID)
	}
	wrongDigest := shaApply
	wrongDigest.SHA256 = append([]byte(nil), digest...)
	wrongDigest.SHA256[0]++
	if _, err := repository.ApplySourceSHA256(ctx, wrongDigest); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("same-fence SHA apply with changed digest = %v, want stale refusal", err)
	}
	locationAfterHash, err := repository.GetSourceLocation(ctx, root.ID, location.ID)
	if err != nil || locationAfterHash.MediaVariantID == nil || *locationAfterHash.MediaVariantID != canonical.ID {
		t.Fatalf("location after hash promotion = %+v, %v; want canonical variant %s", locationAfterHash, err, canonical.ID)
	}
	storedCanonical, err := repository.GetSourceMediaVariant(ctx, canonical.ID)
	if err != nil || storedCanonical.FFProbeVersion == nil || *storedCanonical.FFProbeVersion != probe.FFProbeVersion || storedCanonical.AppliedOperationID == nil || *storedCanonical.AppliedOperationID != operation.ID {
		t.Fatalf("canonical promoted probe provenance = %+v, %v", storedCanonical, err)
	}
	// Existing cache winner is immutable, while this work keeps its independently
	// executed fingerprint selected and replay is an exact no-op.
	winner := &persistence.SourceFingerprintResult{
		ID: uuid.New(), FPCalcVersion: "1.5.1", VersionBanner: "fpcalc 1.5.1",
		AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "111,222",
		ReportedDuration: 12.5, CalculatedAt: time.Now().UTC().Truncate(time.Microsecond),
		AppliedOperationID: uuid.New(), ParserContractVersion: 1,
	}
	if _, err := database.NewInsert().Model(winner).Exec(ctx); err != nil {
		t.Fatalf("insert existing fingerprint cache winner: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO media_fingerprint_cache(source_sha256,fpcalc_version,result_id) VALUES(?,?,?)`, digest, winner.FPCalcVersion, winner.ID); err != nil {
		t.Fatalf("insert existing cache association: %v", err)
	}
	fingerprintAttempt := fixture.claim(persistence.SourceStepFingerprint)
	computed := persistence.SourceFingerprintResult{
		ID: uuid.New(), FPCalcVersion: winner.FPCalcVersion, VersionBanner: winner.VersionBanner,
		AlgorithmNamespace: winner.AlgorithmNamespace, AlgorithmID: winner.AlgorithmID,
		Fingerprint: "333,444", ReportedDuration: 12.5,
		CalculatedAt: time.Now().UTC().Truncate(time.Microsecond), ParserContractVersion: 1,
	}
	fingerprintApply := persistence.SourceFingerprintApply{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, StepAttempt: fingerprintAttempt, Result: computed}
	selected, err := repository.ApplySourceFingerprint(ctx, fingerprintApply)
	if err != nil || selected.ID != computed.ID {
		t.Fatalf("apply fingerprint = %+v, %v; selected result should remain the executed result", selected, err)
	}
	if duplicate, err := repository.ApplySourceFingerprint(ctx, fingerprintApply); err != nil || duplicate.ID != selected.ID {
		t.Fatalf("idempotent fingerprint apply = %+v, %v", duplicate, err)
	}
	var cacheResult uuid.UUID
	if err := database.NewRaw(`SELECT result_id FROM media_fingerprint_cache WHERE source_sha256=? AND fpcalc_version=?`, digest, winner.FPCalcVersion).Scan(ctx, &cacheResult); err != nil || cacheResult != winner.ID {
		t.Fatalf("cache result = %s, %v; first winner %s was replaced", cacheResult, err, winner.ID)
	}
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, operation.ID, "succeeded", "fingerprint", ""); err != nil {
		t.Fatalf("finish initial normalized operation: %v", err)
	}
	targetStep := string(persistence.SourceStepFingerprint)
	cacheReuseFixture := newAnalysisStepFixture(t, ctx, database, repository, client, root, location, work,
		persistence.SourceAnalysisModeSingleStep, &work.ID, &targetStep, true, []persistence.SourceAnalysisToolSelection{fingerprintTool})
	reuseClaim := persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: cacheReuseFixture.operation.ID, OperationAttempt: cacheReuseFixture.operation.Attempt,
		JobID: *cacheReuseFixture.operation.RiverJobID, Step: persistence.SourceStepFingerprint,
	}
	fingerprintAttempt = cacheReuseFixture.claim(persistence.SourceStepFingerprint)
	reused, err := repository.ReuseSourceFingerprint(ctx, reuseClaim, fingerprintAttempt, winner.FPCalcVersion)
	if err != nil || reused.ID != winner.ID {
		t.Fatalf("reuse cached fingerprint = %+v, %v; want first cache winner %s", reused, err, winner.ID)
	}
	if duplicate, err := repository.ReuseSourceFingerprint(ctx, reuseClaim, fingerprintAttempt, winner.FPCalcVersion); err != nil || duplicate.ID != winner.ID {
		t.Fatalf("idempotent fingerprint reuse = %+v, %v; want cache winner %s", duplicate, err, winner.ID)
	}
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, cacheReuseFixture.operation.ID, "succeeded", "fingerprint", ""); err != nil {
		t.Fatalf("finish cache-reuse operation: %v", err)
	}

	// A failed explicit rerun retains the prior successful fingerprint selection.
	failureFixture := newAnalysisStepFixture(t, ctx, database, repository, client, root, location, work,
		persistence.SourceAnalysisModeSingleStep, &work.ID, &targetStep, true, []persistence.SourceAnalysisToolSelection{fingerprintTool})
	fingerprintAttempt = failureFixture.claim(persistence.SourceStepFingerprint)
	if err := repository.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: failureFixture.operation.ID, OperationAttempt: failureFixture.operation.Attempt,
		JobID: *failureFixture.operation.RiverJobID, StepAttempt: fingerprintAttempt, Step: persistence.SourceStepFingerprint, SafeError: "fpcalc failed",
	}); err != nil {
		t.Fatalf("fail fingerprint rerun: %v", err)
	}
	var preserved uuid.UUID
	if err := database.NewRaw(`SELECT success_fingerprint_result_id FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, work.ID).Scan(ctx, &preserved); err != nil || preserved != winner.ID {
		t.Fatalf("fingerprint after failed rerun = %s, %v; want previous success %s", preserved, err, winner.ID)
	}
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, failureFixture.operation.ID, "failed", "fingerprint", "fpcalc failed"); err != nil {
		t.Fatalf("settle failed fingerprint rerun: %v", err)
	}

	// A stale location identity rejects late results without changing selections.
	if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).Set("size_bytes=size_bytes+1").Where("id=?", location.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplySourceSHA256(ctx, shaApply); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("stale SHA delivery = %v, want ErrSourceAnalysisStale", err)
	}
}

func TestSourceProbeCacheRegistersWhenSHACompletesFirstWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/probe-cache-sha-first")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 4096, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
	)
	probeTool := insertVerifiedAnalysisTool(t, ctx, database, "ffmpeg", "ffprobe", "7.1.2", "ffprobe version 7.1.2")
	fixture := newAnalysisStepFixture(t, ctx, database, repository, client, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, false, []persistence.SourceAnalysisToolSelection{probeTool})
	operation := fixture.operation
	jobID := *operation.RiverJobID

	digest := make([]byte, 32)
	digest[0] = 0x91
	if _, err := repository.ApplySourceSHA256(ctx, persistence.SourceSHA256Apply{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: jobID, StepAttempt: fixture.claim(persistence.SourceStepSHA256), SHA256: digest,
		CalculatedAt: time.Now().UTC().Truncate(time.Microsecond), Algorithm: "SHA-256",
	}); err != nil {
		t.Fatalf("apply SHA-256: %v", err)
	}
	probe := persistence.SourceProbeApply{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: jobID, StepAttempt: fixture.claim(persistence.SourceStepProbe), SizeBytes: location.SizeBytes,
		AnalysisPolicy: persistence.SourceAnalysisPolicyVersion, FFProbeVersion: "7.1.2",
		FFProbeJSON:  json.RawMessage(`{"format":{"format_name":"flac"}}`),
		ObservedTags: json.RawMessage(`{"ARTIST":["fixture"]}`), InspectedAt: time.Now().UTC().Truncate(time.Microsecond), AudioStreamCount: 1,
	}
	selected, err := repository.ApplySourceProbe(ctx, probe)
	if err != nil {
		t.Fatalf("apply probe: %v", err)
	}
	var lookupDigest [sha256.Size]byte
	copy(lookupDigest[:], digest)
	cached, found, err := repository.LookupSourceProbe(ctx, lookupDigest, probe.FFProbeVersion, probe.AnalysisPolicy)
	if err != nil || !found || cached.ID != selected.ID {
		t.Fatalf("probe cache after SHA-first apply = %+v, %v, %v; want selected result %s", cached, found, err, selected.ID)
	}
}

func TestSourceFingerprintSelectionCleanupAfterRerunsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/fingerprint-cleanup")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, false,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepFingerprint, State: "pending"},
	)
	priorSelection := &persistence.SourceFingerprintResult{
		ID: uuid.New(), FPCalcVersion: "1.5.1", VersionBanner: "fpcalc version 1.5.1",
		AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "prior-selection",
		ReportedDuration: 12.5, CalculatedAt: time.Now().UTC().Truncate(time.Microsecond),
		AppliedOperationID: uuid.New(), ParserContractVersion: 1,
	}
	if _, err := database.NewInsert().Model(priorSelection).Exec(ctx); err != nil {
		t.Fatalf("insert prior fingerprint selection: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='succeeded',success_fingerprint_result_id=?,success_reuse_origin='executed' WHERE work_id=? AND step='fingerprint'`, priorSelection.ID, work.ID); err != nil {
		t.Fatalf("seed successful fingerprint selection: %v", err)
	}
	fingerprintTool := insertVerifiedAnalysisTool(t, ctx, database, "fpcalc", "fpcalc", "1.5.1", "fpcalc version 1.5.1")
	step := string(persistence.SourceStepFingerprint)
	initial := newAnalysisStepFixture(t, ctx, database, repository, client, root, location, work,
		persistence.SourceAnalysisModeSingleStep, &work.ID, &step, true, []persistence.SourceAnalysisToolSelection{fingerprintTool})
	apply := func(fixture *analysisStepFixture, attempt int, fingerprint string) uuid.UUID {
		t.Helper()
		result := persistence.SourceFingerprintResult{
			ID: uuid.New(), FPCalcVersion: "1.5.1", VersionBanner: "fpcalc 1.5.1",
			AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: fingerprint,
			ReportedDuration: 12.5, CalculatedAt: time.Now().UTC().Truncate(time.Microsecond),
			ParserContractVersion: 1,
		}
		selected, err := repository.ApplySourceFingerprint(ctx, persistence.SourceFingerprintApply{
			WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
			JobID: *fixture.operation.RiverJobID, StepAttempt: attempt, Result: result,
		})
		if err != nil {
			t.Fatalf("apply fingerprint %q: %v", fingerprint, err)
		}
		return selected.ID
	}
	firstID := apply(initial, initial.claim(persistence.SourceStepFingerprint), "111,222")
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, initial.operation.ID, "succeeded", "fingerprint", ""); err != nil {
		t.Fatalf("settle successful explicit rerun: %v", err)
	}
	failedRerun := newAnalysisStepFixture(t, ctx, database, repository, client, root, location, work,
		persistence.SourceAnalysisModeSingleStep, &work.ID, &step, true, []persistence.SourceAnalysisToolSelection{fingerprintTool})
	failedAttempt := failedRerun.claim(persistence.SourceStepFingerprint)
	if err := repository.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: failedRerun.operation.ID, OperationAttempt: failedRerun.operation.Attempt,
		JobID: *failedRerun.operation.RiverJobID, StepAttempt: failedAttempt, Step: persistence.SourceStepFingerprint, SafeError: "fpcalc failed",
	}); err != nil {
		t.Fatalf("fail fingerprint rerun: %v", err)
	}
	var exists bool
	if err := database.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_fingerprint_result WHERE id=?)`, firstID).Scan(ctx, &exists); err != nil || !exists {
		t.Fatalf("failed rerun removed prior selection: exists=%v err=%v", exists, err)
	}
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, failedRerun.operation.ID, "failed", "fingerprint", "fpcalc failed"); err != nil {
		t.Fatalf("settle failed explicit rerun: %v", err)
	}
	retry := newAnalysisStepFixture(t, ctx, database, repository, client, root, location, work,
		persistence.SourceAnalysisModeSingleStep, &work.ID, &step, false, []persistence.SourceAnalysisToolSelection{fingerprintTool})
	secondID := apply(retry, retry.claim(persistence.SourceStepFingerprint), "333,444")
	if secondID == firstID {
		t.Fatal("successful rerun selected the previous result")
	}
	if err := database.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_fingerprint_result WHERE id=?)`, firstID).Scan(ctx, &exists); err != nil || exists {
		t.Fatalf("successful rerun retained unreferenced result: exists=%v err=%v", exists, err)
	}
	digest := make([]byte, 32)
	digest[0] = 0x5c
	shaID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES(?,?,?,now(),'SHA-256',?)`, shaID, location.SizeBytes, digest, retry.operation.ID); err != nil {
		t.Fatalf("insert SHA cache identity: %v", err)
	}
	cached := &persistence.SourceFingerprintResult{
		ID: uuid.New(), FPCalcVersion: "1.5.1", VersionBanner: "fpcalc 1.5.1",
		AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "555,666",
		ReportedDuration: 12.5, CalculatedAt: time.Now().UTC().Truncate(time.Microsecond),
		AppliedOperationID: retry.operation.ID, ParserContractVersion: 1,
	}
	if _, err := database.NewInsert().Model(cached).Exec(ctx); err != nil {
		t.Fatalf("insert cache-backed fingerprint: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO media_fingerprint_cache(source_sha256,fpcalc_version,result_id) VALUES(?,?,?)`, digest, cached.FPCalcVersion, cached.ID); err != nil {
		t.Fatalf("cache fingerprint: %v", err)
	}
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, retry.operation.ID, "succeeded", "fingerprint", ""); err != nil {
		t.Fatalf("finish normalized operation: %v", err)
	}
	if err := repository.DeleteSourceRoot(ctx, root.ID, root.ConfiguredPath, 1); err != nil {
		t.Fatalf("delete root: %v", err)
	}
	if err := database.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_fingerprint_result WHERE id=?)`, secondID).Scan(ctx, &exists); err != nil || exists {
		t.Fatalf("root deletion retained unreferenced fingerprint: exists=%v err=%v", exists, err)
	}
	if err := database.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_fingerprint_result WHERE id=?)`, cached.ID).Scan(ctx, &exists); err != nil || !exists {
		t.Fatalf("root deletion removed cache-backed fingerprint: exists=%v err=%v", exists, err)
	}
}

func TestConcurrentSourceSHA256ApplyUsesOneCanonicalVariantWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	digest := make([]byte, 32)
	digest[0] = 0x31
	inputs := make([]persistence.SourceSHA256Apply, 2)
	for index := range inputs {
		path := "/srv/concurrent-hash-" + string(rune('a'+index))
		root := createInventoryRoot(t, ctx, repository, path)
		location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 512, probeMtime())
		establishInventory(t, ctx, database, root)
		work := normalizedWork(t, ctx, repository, root, location, true,
			persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		)
		operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil)
		admitAndRunNormalizedAnalysis(t, ctx, database, repository, client, operation)
		jobID := *operation.RiverJobID
		stepAttempt, err := repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, Step: persistence.SourceStepSHA256})
		if err != nil {
			t.Fatal(err)
		}
		inputs[index] = persistence.SourceSHA256Apply{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, StepAttempt: stepAttempt, SHA256: append([]byte(nil), digest...), CalculatedAt: time.Now().UTC().Truncate(time.Microsecond), Algorithm: "SHA-256"}
	}
	type outcome struct {
		id  uuid.UUID
		err error
	}
	outcomes := make(chan outcome, len(inputs))
	var wait sync.WaitGroup
	for _, input := range inputs {
		wait.Add(1)
		go func(apply persistence.SourceSHA256Apply) {
			defer wait.Done()
			variant, err := repository.ApplySourceSHA256(ctx, apply)
			if err != nil {
				outcomes <- outcome{err: err}
				return
			}
			outcomes <- outcome{id: variant.ID}
		}(input)
	}
	wait.Wait()
	close(outcomes)
	var canonical uuid.UUID
	for got := range outcomes {
		if got.err != nil {
			t.Fatalf("concurrent SHA apply: %v", got.err)
		}
		if canonical == uuid.Nil {
			canonical = got.id
		} else if got.id != canonical {
			t.Fatalf("canonical IDs differ: %s / %s", canonical, got.id)
		}
	}
	var count int
	if err := database.NewRaw(`SELECT count(*) FROM media_variant WHERE source_sha256=?`, digest).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("canonical rows = %d, %v; want exactly one", count, err)
	}
}

func TestFailedAnalysisStepRetryRetainsPriorSelectionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, step := range []persistence.SourceStepName{persistence.SourceStepProbe, persistence.SourceStepSHA256} {
		t.Run(string(step), func(t *testing.T) {
			database := testpostgres.OpenMigrated(t)
			ctx := context.Background()
			repository := persistence.NewSourceInventoryRepository(database)
			root := createInventoryRoot(t, ctx, repository, "/srv/failed-step-"+string(step))
			location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 1024, probeMtime())
			establishInventory(t, ctx, database, root)
			priorFailure := "prior step attempt failed"
			work := normalizedWork(t, ctx, repository, root, location, true,
				persistence.SourceAnalysisStepInput{Step: step, State: "failed", SafeError: &priorFailure},
			)

			selectedID := uuid.New()
			if step == persistence.SourceStepProbe {
				if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
					VALUES(?,?,1,'7.1.2','{"format":{"format_name":"flac"}}'::jsonb,'{}'::jsonb,now(),?,1)`, selectedID, location.SizeBytes, uuid.New()); err != nil {
					t.Fatalf("insert prior probe selection: %v", err)
				}
			} else {
				digest := make([]byte, 32)
				digest[0] = 0x37
				if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id)
					VALUES(?,?,?,now(),'SHA-256',?)`, selectedID, location.SizeBytes, digest, uuid.New()); err != nil {
					t.Fatalf("insert prior SHA selection: %v", err)
				}
			}
			column := "success_probe_variant_id"
			if step == persistence.SourceStepSHA256 {
				column = "success_sha_variant_id"
			}
			if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET `+column+`=?,success_reuse_origin='executed' WHERE work_id=? AND step=?`, selectedID, work.ID, step); err != nil {
				t.Fatalf("seed prior successful selection: %v", err)
			}

			var selectedTools []persistence.SourceAnalysisToolSelection
			if step == persistence.SourceStepProbe {
				selectedTools = []persistence.SourceAnalysisToolSelection{
					insertVerifiedAnalysisTool(t, ctx, database, "ffmpeg", "ffprobe", "7.1.2", "ffprobe version 7.1.2"),
				}
			}
			targetStep := string(step)
			fixture := newAnalysisStepFixture(t, ctx, database, repository, openScanEnqueueRiver(t, database), root, location, work,
				persistence.SourceAnalysisModeSingleStep, &work.ID, &targetStep, false, selectedTools)
			attempt := fixture.claim(step)
			if err := repository.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
				WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
				JobID: *fixture.operation.RiverJobID, StepAttempt: attempt, Step: step, SafeError: "retry failed",
			}); err != nil {
				t.Fatalf("fail exact-step retry: %v", err)
			}
			var retained uuid.UUID
			if err := database.NewRaw(`SELECT `+column+` FROM source_analysis_step WHERE work_id=? AND step=?`, work.ID, step).Scan(ctx, &retained); err != nil || retained != selectedID {
				t.Fatalf("failed retry selected %s, %v; want retained prior result %s", retained, err, selectedID)
			}
			if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, fixture.operation.ID, "failed", string(step), "retry failed"); err != nil {
				t.Fatalf("settle failed exact-step retry: %v", err)
			}
		})
	}
}

type analysisStepFixture struct {
	t          *testing.T
	repository *persistence.SourceInventoryRepository
	ctx        context.Context
	work       *persistence.SourceAnalysisWork
	operation  *persistence.Operation
}

func newAnalysisStepFixture(
	t *testing.T,
	ctx context.Context,
	database *bun.DB,
	repository *persistence.SourceInventoryRepository,
	client persistence.RiverInserter,
	root *persistence.SourceRoot,
	location persistence.SourceLocation,
	work *persistence.SourceAnalysisWork,
	mode string,
	targetWorkID *uuid.UUID,
	targetStep *string,
	rerun bool,
	selectedTools []persistence.SourceAnalysisToolSelection,
) *analysisStepFixture {
	t.Helper()
	operation := normalizedOperation(t, root, location, work, mode, targetWorkID, targetStep, work.SHA256Enabled, rerun, selectedTools)
	admitAndRunNormalizedAnalysis(t, ctx, database, repository, client, operation)
	return &analysisStepFixture{t: t, repository: repository, ctx: ctx, work: work, operation: operation}
}

func (fixture *analysisStepFixture) claim(step persistence.SourceStepName) int {
	fixture.t.Helper()
	attempt, err := fixture.repository.ClaimSourceAnalysisStep(fixture.ctx, persistence.SourceStepClaim{
		WorkID: fixture.work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, Step: step,
	})
	if err != nil {
		fixture.t.Fatalf("claim normalized test step %s: %v", step, err)
	}
	return attempt
}

func insertVerifiedAnalysisTool(t *testing.T, ctx context.Context, database *bun.DB, packageKind, executable, version, banner string) persistence.SourceAnalysisToolSelection {
	t.Helper()
	kind := tools.PackageKind(packageKind)
	releaseIdentity := packageKind + "-test-release"
	relativePath, err := tools.ManagedRelativePath(kind, releaseIdentity)
	if err != nil {
		t.Fatalf("managed %s analysis installation path: %v", packageKind, err)
	}
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: packageKind, PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		SourceName: "analysis-test", ReleaseIdentity: releaseIdentity, RelativePath: relativePath,
		State: "ready", ExecutableVersions: verifiedExecutableVersionMetadata(t, kind, "darwin", version),
		ArtifactIdentities: json.RawMessage(`{}`),
	}
	var versions map[string]string
	if err := json.Unmarshal(installation.ExecutableVersions, &versions); err != nil {
		t.Fatalf("decode verified executable metadata: %v", err)
	}
	versions[executable] = banner
	installation.ExecutableVersions, err = json.Marshal(versions)
	if err != nil {
		t.Fatalf("encode verified executable metadata: %v", err)
	}
	verifiedAt := time.Now().UTC()
	installation.VerifiedAt = &verifiedAt
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("insert verified %s installation: %v", packageKind, err)
	}
	return persistence.SourceAnalysisToolSelection{
		PackageKind: packageKind, InstallationID: installation.ID, RelativePath: installation.RelativePath,
		Executable: executable, Version: version, VersionBanner: banner,
	}
}

func admitAndRunNormalizedAnalysis(t *testing.T, ctx context.Context, database *bun.DB, repository *persistence.SourceInventoryRepository, client persistence.RiverInserter, operation *persistence.Operation) {
	t.Helper()
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("admit normalized source analysis: %v", err)
	}
	operations := service.NewOperations(persistence.NewSetupManagerRepository(database))
	if err := operations.Running(ctx, operation.ID, "probing"); err != nil {
		t.Fatalf("start normalized source analysis: %v", err)
	}
}
