//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestSourceAnalysisStepAppliesAndPromotionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, repository, "/srv/step-apply")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 4096, probeMtime())
	establishInventory(t, ctx, database, root)
	installation := insertAnalysisInstallation(t, ctx, database, "step-apply")
	operation := insertRunningAnalysisOperation(t, ctx, database, root, location, installation, nil)
	jobID := int64(8181)
	if _, err := database.NewUpdate().Model((*persistence.Operation)(nil)).Set("river_job_id=?", jobID).Where("id=?", operation.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, InventoryPath: *root.InventoryPath,
		RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		SHA256Enabled: true, OriginScanOperationID: uuid.New(),
	}
	if err := repository.StoreSourceAnalysisWork(ctx, work, []persistence.SourceAnalysisStepInput{
		{Step: persistence.SourceStepSHA256, State: "pending"},
		{Step: persistence.SourceStepProbe, State: "pending"},
		{Step: persistence.SourceStepFingerprint, State: "pending"},
	}); err != nil {
		t.Fatalf("store work: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO operation_source_work_hold(operation_id,work_id) VALUES(?,?)`, operation.ID, work.ID); err != nil {
		t.Fatalf("hold work: %v", err)
	}
	claim := func(step persistence.SourceStepName, allowSuccessful bool) int {
		t.Helper()
		attempt, err := repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
			WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
			JobID: jobID, Step: step, AllowSuccessful: allowSuccessful,
		})
		if err != nil {
			t.Fatalf("claim %s: %v", step, err)
		}
		return attempt
	}

	// A hash failure cannot undo the sibling probe, and its explicit retry has a
	// new captured step attempt.
	shaAttempt := claim(persistence.SourceStepSHA256, false)
	if err := repository.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: jobID, StepAttempt: shaAttempt, Step: persistence.SourceStepSHA256, SafeError: "digest unavailable",
	}); err != nil {
		t.Fatalf("fail hash step: %v", err)
	}
	probeAttempt := claim(persistence.SourceStepProbe, false)
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
	probeAttempt = claim(persistence.SourceStepProbe, true)
	if err := repository.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: jobID, StepAttempt: probeAttempt, Step: persistence.SourceStepProbe, SafeError: "probe rerun failed",
	}); err != nil {
		t.Fatalf("fail probe rerun: %v", err)
	}

	shaAttempt = claim(persistence.SourceStepSHA256, false)
	digest := make([]byte, 32)
	digest[0] = 0x7a
	shaApply := persistence.SourceSHA256Apply{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: jobID, StepAttempt: shaAttempt, SHA256: digest,
		CalculatedAt: time.Now().UTC().Truncate(time.Microsecond), Algorithm: "SHA-256",
	}
	canonical, err := repository.ApplySourceSHA256(ctx, shaApply)
	if err != nil {
		t.Fatalf("apply SHA-256: %v", err)
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
	shaAttempt = claim(persistence.SourceStepSHA256, true)
	if err := repository.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: jobID, StepAttempt: shaAttempt, Step: persistence.SourceStepSHA256, SafeError: "hash rerun failed",
	}); err != nil {
		t.Fatalf("fail hash rerun: %v", err)
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
	fingerprintAttempt := claim(persistence.SourceStepFingerprint, false)
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
	reuseClaim := persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: jobID, Step: persistence.SourceStepFingerprint,
	}
	fingerprintAttempt = claim(persistence.SourceStepFingerprint, true)
	reused, err := repository.ReuseSourceFingerprint(ctx, reuseClaim, fingerprintAttempt, winner.FPCalcVersion)
	if err != nil || reused.ID != winner.ID {
		t.Fatalf("reuse cached fingerprint = %+v, %v; want first cache winner %s", reused, err, winner.ID)
	}
	if duplicate, err := repository.ReuseSourceFingerprint(ctx, reuseClaim, fingerprintAttempt, winner.FPCalcVersion); err != nil || duplicate.ID != winner.ID {
		t.Fatalf("idempotent fingerprint reuse = %+v, %v; want cache winner %s", duplicate, err, winner.ID)
	}

	// A failed explicit rerun retains the prior successful fingerprint selection.
	fingerprintAttempt = claim(persistence.SourceStepFingerprint, true)
	if err := repository.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: jobID, StepAttempt: fingerprintAttempt, Step: persistence.SourceStepFingerprint, SafeError: "fpcalc failed",
	}); err != nil {
		t.Fatalf("fail fingerprint rerun: %v", err)
	}
	var preserved uuid.UUID
	if err := database.NewRaw(`SELECT success_fingerprint_result_id FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, work.ID).Scan(ctx, &preserved); err != nil || preserved != winner.ID {
		t.Fatalf("fingerprint after failed rerun = %s, %v; want previous success %s", preserved, err, winner.ID)
	}

	// A stale location identity rejects late results without changing selections.
	if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).Set("size_bytes=size_bytes+1").Where("id=?", location.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplySourceSHA256(ctx, shaApply); !errors.Is(err, persistence.ErrSourceAnalysisStale) {
		t.Fatalf("stale SHA delivery = %v, want ErrSourceAnalysisStale", err)
	}
}

func TestSourceFingerprintSelectionCleanupAfterRerunsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, repository, "/srv/fingerprint-cleanup")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	installation := insertAnalysisInstallation(t, ctx, database, "fingerprint-cleanup")
	operation := insertRunningAnalysisOperation(t, ctx, database, root, location, installation, nil)
	jobID := int64(8282)
	if _, err := database.NewUpdate().Model((*persistence.Operation)(nil)).Set("river_job_id=?", jobID).Where("id=?", operation.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, InventoryPath: *root.InventoryPath,
		RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		OriginScanOperationID: uuid.New(),
	}
	if err := repository.StoreSourceAnalysisWork(ctx, work, []persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepFingerprint, State: "pending"}}); err != nil {
		t.Fatalf("store work: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO operation_source_work_hold(operation_id,work_id) VALUES(?,?)`, operation.ID, work.ID); err != nil {
		t.Fatalf("hold work: %v", err)
	}
	claim := func() int {
		t.Helper()
		attempt, err := repository.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
			WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
			JobID: jobID, Step: persistence.SourceStepFingerprint, AllowSuccessful: true,
		})
		if err != nil {
			t.Fatalf("claim fingerprint: %v", err)
		}
		return attempt
	}
	apply := func(attempt int, fingerprint string) uuid.UUID {
		t.Helper()
		result := persistence.SourceFingerprintResult{
			ID: uuid.New(), FPCalcVersion: "1.5.1", VersionBanner: "fpcalc 1.5.1",
			AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: fingerprint,
			ReportedDuration: 12.5, CalculatedAt: time.Now().UTC().Truncate(time.Microsecond),
			ParserContractVersion: 1,
		}
		selected, err := repository.ApplySourceFingerprint(ctx, persistence.SourceFingerprintApply{
			WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
			JobID: jobID, StepAttempt: attempt, Result: result,
		})
		if err != nil {
			t.Fatalf("apply fingerprint %q: %v", fingerprint, err)
		}
		return selected.ID
	}
	firstID := apply(claim(), "111,222")
	failedAttempt := claim()
	if err := repository.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: jobID, StepAttempt: failedAttempt, Step: persistence.SourceStepFingerprint, SafeError: "fpcalc failed",
	}); err != nil {
		t.Fatalf("fail fingerprint rerun: %v", err)
	}
	var exists bool
	if err := database.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_fingerprint_result WHERE id=?)`, firstID).Scan(ctx, &exists); err != nil || !exists {
		t.Fatalf("failed rerun removed prior selection: exists=%v err=%v", exists, err)
	}
	secondID := apply(claim(), "333,444")
	if secondID == firstID {
		t.Fatal("successful rerun selected the previous result")
	}
	if err := database.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_fingerprint_result WHERE id=?)`, firstID).Scan(ctx, &exists); err != nil || exists {
		t.Fatalf("successful rerun retained unreferenced result: exists=%v err=%v", exists, err)
	}
	digest := make([]byte, 32)
	digest[0] = 0x5c
	shaID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES(?,?,?,now(),'SHA-256',?)`, shaID, location.SizeBytes, digest, operation.ID); err != nil {
		t.Fatalf("insert SHA cache identity: %v", err)
	}
	cached := &persistence.SourceFingerprintResult{
		ID: uuid.New(), FPCalcVersion: "1.5.1", VersionBanner: "fpcalc 1.5.1",
		AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "555,666",
		ReportedDuration: 12.5, CalculatedAt: time.Now().UTC().Truncate(time.Microsecond),
		AppliedOperationID: operation.ID, ParserContractVersion: 1,
	}
	if _, err := database.NewInsert().Model(cached).Exec(ctx); err != nil {
		t.Fatalf("insert cache-backed fingerprint: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO media_fingerprint_cache(source_sha256,fpcalc_version,result_id) VALUES(?,?,?)`, digest, cached.FPCalcVersion, cached.ID); err != nil {
		t.Fatalf("cache fingerprint: %v", err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM operation_source_work_hold WHERE operation_id=? AND work_id=?`, operation.ID, work.ID); err != nil {
		t.Fatalf("release work hold: %v", err)
	}
	if _, err := database.NewUpdate().Model((*persistence.Operation)(nil)).
		Set("state='succeeded'").Set("finished_at=now()").Set("updated_at=now()").Set("target_source_root_id=NULL").Set("target_source_location_id=NULL").
		Set("analysis_installation_id=NULL").Set("analysis_media_variant_id=NULL").Where("id=?", operation.ID).Exec(ctx); err != nil {
		t.Fatalf("finish operation: %v", err)
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
	digest := make([]byte, 32)
	digest[0] = 0x31
	inputs := make([]persistence.SourceSHA256Apply, 2)
	for index := range inputs {
		path := "/srv/concurrent-hash-" + string(rune('a'+index))
		root := createInventoryRoot(t, ctx, repository, path)
		location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 512, probeMtime())
		establishInventory(t, ctx, database, root)
		installation := insertAnalysisInstallation(t, ctx, database, "concurrent-hash-"+string(rune('a'+index)))
		operation := insertRunningAnalysisOperation(t, ctx, database, root, location, installation, nil)
		jobID := int64(9200 + index)
		if _, err := database.NewUpdate().Model((*persistence.Operation)(nil)).Set("river_job_id=?", jobID).Where("id=?", operation.ID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		work := &persistence.SourceAnalysisWork{ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID, ConfiguredPath: root.ConfiguredPath, InventoryPath: *root.InventoryPath, RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime, SHA256Enabled: true, OriginScanOperationID: uuid.New()}
		if err := repository.StoreSourceAnalysisWork(ctx, work, []persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ExecContext(ctx, `INSERT INTO operation_source_work_hold(operation_id,work_id) VALUES(?,?)`, operation.ID, work.ID); err != nil {
			t.Fatal(err)
		}
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
