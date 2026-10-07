//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestPublishPreparedSourceScanAnalysisTransactionally(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	root := publishTestRoot(t, ctx, db, "/srv/publish-analysis")
	op := publishTestScan(t, ctx, db, root.ID)
	stamp := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	digest := make([]byte, 32)
	digest[0] = 0x4a
	shaID1, shaID2 := uuid.New(), uuid.New()
	sha1 := publishTestSHA(shaID1, digest, op.ID, stamp)
	sha2 := publishTestSHA(shaID2, digest, op.ID, stamp.Add(time.Second))
	probeErr := SourceScanPreparedAnalysis{Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded, SHA256Variant: sha1, ProbeState: SourcePreparedFailed, ProbeSafeError: "probe unavailable", FingerprintState: SourcePreparedSucceeded, FingerprintResult: publishTestFingerprint(uuid.New(), op.ID, stamp)}
	duplicate := SourceScanPreparedAnalysis{Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded, SHA256Variant: sha2, ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedNotRequested}
	candidates := []SourceScanCandidateInput{
		{RelativePath: "a.flac", SizeBytes: 21, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: &probeErr},
		{RelativePath: "b.flac", SizeBytes: 21, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: &duplicate},
	}
	if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		if err := insertSourceLocations(ctx, tx, *root, 1, candidates); err != nil {
			return err
		}
		if err := publishPreparedSourceScanAnalysis(ctx, tx, *root, *op, true, candidates); err != nil {
			return err
		}
		return errors.New("rollback publication fixture")
	}); err == nil || err.Error() != "rollback publication fixture" {
		t.Fatalf("publication transaction error = %v, want fixture rollback", err)
	}
	assertPublishCounts(t, ctx, db, 0, 0, 0, 0)

	if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		if err := insertSourceLocations(ctx, tx, *root, 1, candidates); err != nil {
			return err
		}
		return publishPreparedSourceScanAnalysis(ctx, tx, *root, *op, true, candidates)
	}); err != nil {
		t.Fatalf("publish prepared scan analysis: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE operation SET state='succeeded',finished_at=now() WHERE id=?`, op.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertPublishCounts(t, ctx, db, 2, 1, 1, 2)

	var shaRefs int
	if err := db.NewRaw(`SELECT count(DISTINCT success_sha_variant_id) FROM source_analysis_step WHERE step='sha256' AND state='succeeded'`).Scan(ctx, &shaRefs); err != nil || shaRefs != 1 {
		t.Fatalf("canonical SHA references = %d, %v; want one", shaRefs, err)
	}
	var failedSHA, failedProbe, succeededFingerprint bool
	if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_analysis_step WHERE step='sha256' AND state='failed'), EXISTS(SELECT 1 FROM source_analysis_step WHERE step='probe' AND state='failed' AND safe_error='probe unavailable'), EXISTS(SELECT 1 FROM source_analysis_step WHERE step='fingerprint' AND state='succeeded')`).Scan(ctx, &failedSHA, &failedProbe, &succeededFingerprint); err != nil {
		t.Fatal(err)
	}
	if failedSHA || !failedProbe || !succeededFingerprint {
		t.Fatalf("independent step states: SHA failed=%v probe failed=%v fingerprint succeeded=%v", failedSHA, failedProbe, succeededFingerprint)
	}
	var cacheCount int
	if err := db.NewRaw(`SELECT count(*) FROM media_fingerprint_cache`).Scan(ctx, &cacheCount); err != nil || cacheCount != 1 {
		t.Fatalf("fingerprint cache count = %d, %v; want only SHA-associated result", cacheCount, err)
	}

	// Hashing-disabled fingerprints remain independent and do not acquire a
	// synthetic digest/cache key.
	disabledOp := publishTestScan(t, ctx, db, root.ID, false)
	noHash := SourceScanPreparedAnalysis{Version: 1, HashRequested: false, SHA256State: SourcePreparedNotRequested, ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedSucceeded, FingerprintResult: publishTestFingerprint(uuid.New(), disabledOp.ID, stamp.Add(2*time.Second))}
	noHashCandidate := []SourceScanCandidateInput{{RelativePath: "c.flac", SizeBytes: 22, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: &noHash}}
	if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		if err := insertSourceLocations(ctx, tx, *root, 2, noHashCandidate); err != nil {
			return err
		}
		return publishPreparedSourceScanAnalysis(ctx, tx, *root, *disabledOp, false, noHashCandidate)
	}); err != nil {
		t.Fatalf("publish disabled-hash fingerprint: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE operation SET state='succeeded',finished_at=now() WHERE id=?`, disabledOp.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertPublishCounts(t, ctx, db, 3, 1, 2, 2)
	if err := db.NewRaw(`SELECT count(*) FROM media_fingerprint_cache`).Scan(ctx, &cacheCount); err != nil || cacheCount != 1 {
		t.Fatalf("disabled-hash cache count = %d, %v; want existing digest cache only", cacheCount, err)
	}
	var disabledSHAState, disabledSHASkip string
	if err := db.NewRaw(`SELECT state,skip_reason FROM source_analysis_step WHERE step='sha256' AND work_id IN (SELECT id FROM source_analysis_work WHERE relative_path='c.flac')`).Scan(ctx, &disabledSHAState, &disabledSHASkip); err != nil || disabledSHAState != "skipped" || disabledSHASkip != "disabled" {
		t.Fatalf("disabled SHA step = %q/%q, %v; want skipped/disabled", disabledSHAState, disabledSHASkip, err)
	}
}

func TestPublishPreparedSourceScanAnalysisDefersMoveBlockedSteps(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
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
	if err := db.NewRaw(`SELECT (SELECT s.state FROM source_analysis_step s JOIN source_analysis_work w ON w.id=s.work_id WHERE w.relative_path='both-deferred.flac' AND s.step='sha256'), (SELECT s.state FROM source_analysis_step s JOIN source_analysis_work w ON w.id=s.work_id WHERE w.relative_path='both-deferred.flac' AND s.step='probe'), (SELECT s.state FROM source_analysis_step s JOIN source_analysis_work w ON w.id=s.work_id WHERE w.relative_path='both-deferred.flac' AND s.step='fingerprint')`).Scan(ctx, &shaState, &probeState, &fingerprintState); err != nil {
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
	if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		selected, err := insertScanFingerprint(ctx, tx, oldFingerprint)
		if err != nil {
			return err
		}
		step := SourceAnalysisStep{WorkID: oldWork.ID, Step: string(SourceStepFingerprint), State: "succeeded", SuccessFingerprintResultID: &selected.ID, SuccessReuseOrigin: stringPointer("executed")}
		_, err = tx.NewInsert().Model(&step).Exec(ctx)
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

func TestPublishPreparedSourceScanAnalysisDoesNotBackfillUnchangedWork(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	root := publishTestRoot(t, ctx, db, "/srv/publish-unchanged")
	op := publishTestScan(t, ctx, db, root.ID)
	stamp := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	location := SourceLocation{ID: uuid.New(), SourceRootID: root.ID, RelativePath: "existing.flac", SizeBytes: 5, Mtime: stamp, LastSeenScanGeneration: 1, ProbeStatus: SourceProbeStatusAudio}
	if _, err := db.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, 32)
	digest[0] = 0x71
	sha := publishTestSHA(uuid.New(), digest, op.ID, stamp)
	if _, err := db.NewRaw(`INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES(?,?,?,?,?,?)`, sha.ID, location.SizeBytes, sha.SourceSHA256, sha.SHA256CalculatedAt, sha.SHA256Algorithm, sha.SHA256AppliedOperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewUpdate().Model((*SourceLocation)(nil)).Set("media_variant_id=?", sha.ID).Where("id=?", location.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	work := &SourceAnalysisWork{ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID, ConfiguredPath: root.ConfiguredPath, InventoryPath: root.ConfiguredPath, RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: stamp, SHA256Enabled: true, OriginScanOperationID: op.ID}
	if _, err := db.NewInsert().Model(work).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	shaStep := SourceAnalysisStep{WorkID: work.ID, Step: string(SourceStepSHA256), State: "succeeded", SuccessSHAVariantID: &sha.ID, SuccessReuseOrigin: stringPointer("executed")}
	probeStep := SourceAnalysisStep{WorkID: work.ID, Step: string(SourceStepProbe), State: "pending"}
	fingerprintStep := SourceAnalysisStep{WorkID: work.ID, Step: string(SourceStepFingerprint), State: "pending"}
	for _, step := range []*SourceAnalysisStep{&shaStep, &probeStep, &fingerprintStep} {
		if _, err := db.NewInsert().Model(step).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	probeVersion := "ffprobe-test"
	policy, audioCount := 1, 0
	inspected := stamp.Add(time.Second)
	prepared := &SourceScanPreparedAnalysis{
		Version: 1, HashRequested: false, SHA256State: SourcePreparedNotRequested,
		ProbeState: SourcePreparedSucceeded,
		ProbeVariant: &SourceMediaVariant{
			ID: uuid.New(), FFProbeVersion: &probeVersion,
			FFProbeJSON:  json.RawMessage(`{"format":{},"streams":[]}`),
			ObservedTags: json.RawMessage(`{}`), AnalysisPolicyVersion: &policy,
			InspectedAt: &inspected, AppliedOperationID: &op.ID, AudioStreamCount: &audioCount,
		},
		FingerprintState: SourcePreparedSucceeded, FingerprintResult: publishTestFingerprint(uuid.New(), op.ID, stamp.Add(2*time.Second)),
		OriginalLocationID: &location.ID, OriginalMediaVariantID: &sha.ID,
	}
	candidate := []SourceScanCandidateInput{{RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: prepared}}
	if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		if err := insertSourceLocations(ctx, tx, *root, 2, candidate); err != nil {
			return err
		}
		return publishPreparedSourceScanAnalysis(ctx, tx, *root, *op, false, candidate)
	}); err != nil {
		t.Fatal(err)
	}
	assertPublishCounts(t, ctx, db, 1, 1, 1, 1)
	var retained string
	if err := db.NewRaw(`SELECT state FROM source_analysis_step WHERE work_id=? AND step='sha256'`, work.ID).Scan(ctx, &retained); err != nil || retained != "succeeded" {
		t.Fatalf("unchanged SHA step = %q, %v; want retained success", retained, err)
	}
	if err := db.NewRaw(`SELECT state FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, work.ID).Scan(ctx, &retained); err != nil || retained != "succeeded" {
		t.Fatalf("unchanged fingerprint step = %q, %v; want newly published success", retained, err)
	}
}

func TestPublishPreparedSourceScanAnalysisLocksSharedDigestsInOrder(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rootA := publishTestRoot(t, ctx, db, "/srv/publish-order-a")
	rootB := publishTestRoot(t, ctx, db, "/srv/publish-order-b")
	opA := publishTestScan(t, ctx, db, rootA.ID)
	opB := publishTestScan(t, ctx, db, rootB.ID)
	stamp := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	digestA, digestB := make([]byte, 32), make([]byte, 32)
	digestA[0], digestB[0] = 0xa1, 0xb2
	makeCandidates := func(op *Operation, paths []string) []SourceScanCandidateInput {
		candidates := make([]SourceScanCandidateInput, 0, len(paths))
		for index, path := range paths {
			digest := digestA
			if path == "b.flac" {
				digest = digestB
			}
			prepared := &SourceScanPreparedAnalysis{Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded, SHA256Variant: publishTestSHA(uuid.New(), digest, op.ID, stamp.Add(time.Duration(index)*time.Second)), ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedNotRequested}
			candidates = append(candidates, SourceScanCandidateInput{RelativePath: path, SizeBytes: 8, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: prepared})
		}
		return candidates
	}
	candidatesA := makeCandidates(opA, []string{"a.flac", "b.flac"})
	candidatesB := makeCandidates(opB, []string{"b.flac", "a.flac"})
	for _, rootCandidates := range []struct {
		root       *SourceRoot
		candidates []SourceScanCandidateInput
	}{{rootA, candidatesA}, {rootB, candidatesB}} {
		if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
			return insertSourceLocations(ctx, tx, *rootCandidates.root, 1, rootCandidates.candidates)
		}); err != nil {
			t.Fatal(err)
		}
	}

	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for _, input := range []struct {
		root       *SourceRoot
		operation  *Operation
		candidates []SourceScanCandidateInput
	}{{rootA, opA, candidatesA}, {rootB, opB, candidatesB}} {
		workers.Add(1)
		go func(root *SourceRoot, operation *Operation, candidates []SourceScanCandidateInput) {
			defer workers.Done()
			err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
				if err := tx.NewRaw(`SELECT id FROM source_root WHERE id=? FOR UPDATE`, root.ID).Scan(ctx, new(uuid.UUID)); err != nil {
					return err
				}
				ready <- struct{}{}
				<-start
				return publishPreparedSourceScanAnalysis(ctx, tx, *root, *operation, true, candidates)
			})
			errs <- err
		}(input.root, input.operation, input.candidates)
	}
	<-ready
	<-ready
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent publication: %v", err)
		}
	}
	var digestCount int
	if err := db.NewRaw(`SELECT count(*) FROM media_variant WHERE source_sha256 IN (?,?)`, digestA, digestB).Scan(ctx, &digestCount); err != nil || digestCount != 2 {
		t.Fatalf("shared canonical SHA identities = %d, %v; want two", digestCount, err)
	}
}

func TestPublishPreparedSourceScanAnalysisOrdersRetainedAndNewDigests(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rootA := publishTestRoot(t, ctx, db, "/srv/publish-retained-a")
	rootB := publishTestRoot(t, ctx, db, "/srv/publish-retained-b")
	opA := publishTestScan(t, ctx, db, rootA.ID)
	opB := publishTestScan(t, ctx, db, rootB.ID)
	stamp := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	digestA, digestB := make([]byte, 32), make([]byte, 32)
	digestA[0], digestB[0] = 0xc1, 0xd2
	initial := func(op *Operation) []SourceScanCandidateInput {
		return []SourceScanCandidateInput{
			{RelativePath: "a.flac", SizeBytes: 8, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: &SourceScanPreparedAnalysis{Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded, SHA256Variant: publishTestSHA(uuid.New(), digestA, op.ID, stamp), ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedNotRequested}},
			{RelativePath: "b.flac", SizeBytes: 8, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: &SourceScanPreparedAnalysis{Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded, SHA256Variant: publishTestSHA(uuid.New(), digestB, op.ID, stamp), ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedNotRequested}},
		}
	}
	initialA, initialB := initial(opA), initial(opB)
	for _, pair := range []struct {
		root *SourceRoot
		op   *Operation
		in   []SourceScanCandidateInput
	}{{rootA, opA, initialA}, {rootB, opB, initialB}} {
		if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
			if err := insertSourceLocations(ctx, tx, *pair.root, 1, pair.in); err != nil {
				return err
			}
			return publishPreparedSourceScanAnalysis(ctx, tx, *pair.root, *pair.op, true, pair.in)
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewRaw(`UPDATE operation SET state='succeeded',finished_at=now() WHERE id=?`, pair.op.ID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	canonical := func(digest []byte) *SourceMediaVariant {
		t.Helper()
		variant := new(SourceMediaVariant)
		if err := db.NewRaw(`SELECT * FROM media_variant WHERE source_sha256=?`, digest).Scan(ctx, variant); err != nil {
			t.Fatal(err)
		}
		return variant
	}
	locationID := func(rootID uuid.UUID, path string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := db.NewRaw(`SELECT id FROM source_location WHERE source_root_id=? AND relative_path=?`, rootID, path).Scan(ctx, &id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	secondA := publishTestScan(t, ctx, db, rootA.ID)
	secondB := publishTestScan(t, ctx, db, rootB.ID)
	shaA, shaB := canonical(digestA), canonical(digestB)
	locAA, locAB := locationID(rootA.ID, "a.flac"), locationID(rootA.ID, "b.flac")
	locBA, locBB := locationID(rootB.ID, "a.flac"), locationID(rootB.ID, "b.flac")
	newA := &SourceScanPreparedAnalysis{Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded, SHA256Variant: publishTestSHA(uuid.New(), digestA, secondA.ID, stamp.Add(time.Second)), ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedNotRequested, OriginalLocationID: &locAA, OriginalMediaVariantID: &shaA.ID, SuccessorLocationID: &locAA}
	newB := &SourceScanPreparedAnalysis{Version: 1, HashRequested: true, SHA256State: SourcePreparedSucceeded, SHA256Variant: publishTestSHA(uuid.New(), digestB, secondB.ID, stamp.Add(time.Second)), ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedNotRequested, OriginalLocationID: &locBB, OriginalMediaVariantID: &shaB.ID, SuccessorLocationID: &locBB}
	retainedB := &SourceScanPreparedAnalysis{Version: 1, HashRequested: false, SHA256State: SourcePreparedNotRequested, RetainedSHA256Variant: shaB, ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedNotRequested, OriginalLocationID: &locAB, OriginalMediaVariantID: &shaB.ID, SuccessorLocationID: &locAB}
	retainedA := &SourceScanPreparedAnalysis{Version: 1, HashRequested: false, SHA256State: SourcePreparedNotRequested, RetainedSHA256Variant: shaA, ProbeState: SourcePreparedNotRequested, FingerprintState: SourcePreparedNotRequested, OriginalLocationID: &locBA, OriginalMediaVariantID: &shaA.ID, SuccessorLocationID: &locBA}
	secondCandidatesA := []SourceScanCandidateInput{
		{RelativePath: "a.flac", SizeBytes: 8, Mtime: stamp.Add(time.Second), ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: newA},
		{RelativePath: "b.flac", SizeBytes: 8, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: retainedB},
	}
	secondCandidatesB := []SourceScanCandidateInput{
		{RelativePath: "b.flac", SizeBytes: 8, Mtime: stamp.Add(time.Second), ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: newB},
		{RelativePath: "a.flac", SizeBytes: 8, Mtime: stamp, ProbeStatus: SourceProbeStatusAudio, PreparedAnalysis: retainedA},
	}
	for _, pair := range []struct {
		root *SourceRoot
		in   []SourceScanCandidateInput
	}{{rootA, secondCandidatesA}, {rootB, secondCandidatesB}} {
		if err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
			return insertSourceLocations(ctx, tx, *pair.root, 2, pair.in)
		}); err != nil {
			t.Fatal(err)
		}
	}
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for _, pair := range []struct {
		root *SourceRoot
		op   *Operation
		in   []SourceScanCandidateInput
	}{{rootA, secondA, secondCandidatesA}, {rootB, secondB, secondCandidatesB}} {
		workers.Add(1)
		go func(root *SourceRoot, op *Operation, candidates []SourceScanCandidateInput) {
			defer workers.Done()
			err := db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
				if err := tx.NewRaw(`SELECT id FROM source_root WHERE id=? FOR UPDATE`, root.ID).Scan(ctx, new(uuid.UUID)); err != nil {
					return err
				}
				ready <- struct{}{}
				<-start
				return publishPreparedSourceScanAnalysis(ctx, tx, *root, *op, true, candidates)
			})
			errs <- err
		}(pair.root, pair.op, pair.in)
	}
	<-ready
	<-ready
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent retained/new publication: %v", err)
		}
	}
	var workCount, shaSuccess int
	if err := db.NewRaw(`SELECT count(*) FROM source_analysis_work`).Scan(ctx, &workCount); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE step='sha256' AND state='succeeded'`).Scan(ctx, &shaSuccess); err != nil {
		t.Fatal(err)
	}
	if workCount != 4 || shaSuccess != 4 {
		t.Fatalf("mixed retained/new publication work=%d successful SHA=%d; want 4/4", workCount, shaSuccess)
	}
}

func publishTestRoot(t *testing.T, ctx context.Context, db *bun.DB, path string) *SourceRoot {
	t.Helper()
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
	shaEnabled := true
	if len(policies) > 0 {
		shaEnabled = policies[0]
	}
	root, err := NewSourceInventoryRepository(db).GetSourceRoot(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(sourceScanSnapshot{
		SchemaVersion: SourceScanSnapshotVersion, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, ScanGeneration: root.ScanGeneration,
		SHA256Enabled: &shaEnabled, Tools: []SourceAnalysisToolSelection{},
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

func assertPublishCounts(t *testing.T, ctx context.Context, db *bun.DB, works, variants, fingerprints, wantLinked int) {
	t.Helper()
	var workCount, variantCount, fingerprintCount, linked int
	for query, dest := range map[string]*int{
		`SELECT count(*) FROM source_analysis_work`:                               &workCount,
		`SELECT count(*) FROM media_variant`:                                      &variantCount,
		`SELECT count(*) FROM media_fingerprint_result`:                           &fingerprintCount,
		`SELECT count(*) FROM source_location WHERE media_variant_id IS NOT NULL`: &linked,
	} {
		if err := db.NewRaw(query).Scan(ctx, dest); err != nil {
			t.Fatal(err)
		}
	}
	if workCount != works || variantCount != variants || fingerprintCount != fingerprints || linked != wantLinked {
		t.Fatalf("published counts work=%d variants=%d fingerprints=%d linked=%d; want %d/%d/%d/%d", workCount, variantCount, fingerprintCount, linked, works, variants, fingerprints, wantLinked)
	}
}
