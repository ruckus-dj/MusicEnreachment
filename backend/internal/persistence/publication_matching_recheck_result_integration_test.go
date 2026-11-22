//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func seedMatchingRecheckMove(t *testing.T, f publicationExecutionFixture, outputRoot string, samePath bool) (TrackPublication, uuid.UUID, matchingLocalRecheckSnapshotSource, PublicationResult) {
	t.Helper()
	ctx := t.Context()
	outcome := publicationResultForFixture(f)
	input := publicationInputsFixture(t, "audio")
	input.SourceAuthorityMode = "automatic"
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := f.db.NewRaw(statement, args...).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	position, track, release := uuid.New(), uuid.New(), uuid.New()
	association, variant, claimID := uuid.New(), uuid.New(), uuid.New()
	exec(`UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(mustJSON(input)), f.guard.OperationID)
	exec(`WITH local AS (INSERT INTO local_release(id,title) VALUES (?,'Old Local release') RETURNING id),
		artist AS (INSERT INTO local_artist(id,name) VALUES (?,'Local artist') RETURNING id)
		INSERT INTO release_artist(release_id,position,artist_id,name) SELECT local.id,0,artist.id,'Local artist' FROM local,artist`,
		release, uuid.New())
	exec(`INSERT INTO local_track(id,title) VALUES (?,'Old Local track')`, track)
	exec(`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, position, release, track)
	exec(`INSERT INTO media_variant(id,size_bytes) VALUES (?,10)`, variant)
	exec(`INSERT INTO track_source(id,track_id,media_variant_id,decision_method,policy_version,compatible,selected,metadata)
		VALUES (?,?,?,'automatic',?,true,true,'{}'::jsonb)`, association, track, variant, MatchingLayoutPolicyVersion)
	old := TrackPublication{
		PositionID: position, Epoch: f.guard.Epoch, Generation: uuid.New(), RelativePath: outcome.Observation.RelativePath,
		Ownership: mustJSON(outcome.InstalledOwnership), ActualAudio: outcome.Observation.ActualAudio,
		ActualMetadata: outcome.Observation.ActualMetadata, ActualAssets: outcome.Observation.ActualAssets,
		QualityPolicyVersion: outcome.Observation.QualityPolicyVersion, ValidatedAt: time.Now(),
	}
	if !samePath {
		old.RelativePath = scopePublicationPath("album/local.flac", f.namespaceScope)
		old.Ownership = mustJSON(writeMatchingRecheckOutput(t, outputRoot, old.RelativePath, "predecessor bytes"))
		outcome.InstalledOwnership = writeMatchingRecheckOutput(t, outputRoot, outcome.Observation.RelativePath, "destination bytes")
		outcome.Observation.PayloadSHA256 = outcome.InstalledOwnership.Identity.SHA256
	}
	if _, err := f.db.NewInsert().Model(&old).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(old.RelativePath))
	exec(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id)
		VALUES (?,?,?,?,?,'file',?)`, claimID, old.Epoch, digest[:], old.RelativePath, old.RelativePath, position)
	exec(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`,
		claimID, position, old.Generation)
	fileClaim := claimID
	if !samePath {
		fileClaim = uuid.New()
		path := outcome.Observation.RelativePath
		digest := sha256.Sum256([]byte(path))
		exec(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id)
			VALUES (?,?,?,?,?,'file',?)`, fileClaim, old.Epoch, digest[:], path, path, f.guard.ExpectedPositionID)
	}
	exec(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`,
		fileClaim, f.guard.ExpectedPositionID, f.guard.CandidateGeneration)
	if samePath {
		directoryID := uuid.New()
		directoryDigest := sha256.Sum256([]byte("album"))
		exec(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,directory_scope_key)
			VALUES (?,?,?,'album','album','dir','album')`, directoryID, old.Epoch, directoryDigest[:])
		exec(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?),(?,?,?)`,
			directoryID, position, old.Generation, directoryID, f.guard.ExpectedPositionID, f.guard.CandidateGeneration)
	}
	return old, claimID, matchingLocalRecheckSnapshotSource{
		ReleaseTrackID: position, LocalTrackID: track, TrackSourceID: association, TrackSourceInserted: true, MediaVariantID: variant,
	}, outcome
}

func seedMatchingRecheckBatch(t *testing.T, db *bun.DB, sources ...matchingLocalRecheckSnapshotSource) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	recheck, applied := uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,started_at,finished_at)
		VALUES (?,'matching_apply_v1','succeeded','succeeded','{}'::jsonb,1,now(),now())`, applied).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := matchingLocalRecheckSnapshot{Version: 1, LocalApplyOperationID: applied, PolicyVersion: MatchingLayoutPolicyVersion, Sources: sources}
	if _, err := db.NewRaw(`INSERT INTO matching_local_recheck(id,local_apply_operation_id,state,next_recheck_at,incumbent_snapshot)
		VALUES (?,?,'publishing',now(),?::jsonb)`, recheck, applied, string(mustJSON(snapshot))).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return recheck
}

func seedMatchingRecheckReceipt(t *testing.T, f publicationExecutionFixture, recheck uuid.UUID, old TrackPublication, claimID uuid.UUID, samePath bool) {
	t.Helper()
	witness, err := matchingRecheckMoveWitness(old, claimID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO matching_local_recheck_publication(operation_id,recheck_id,output_epoch,position_id,candidate_generation,
		predecessor_position_id,predecessor_generation,predecessor_claim_id,same_path,predecessor_snapshot)
		VALUES (?,?,?,?,?,?,?,?,?,?::jsonb)`, f.guard.OperationID, recheck, old.Epoch, f.guard.ExpectedPositionID,
		f.guard.CandidateGeneration, old.PositionID, old.Generation, claimID, samePath, string(witness)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCommitMatchingRecheckSamePathMovesCurrentPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := preparePublicationResultFixture(t)
	old, claimID, source, outcome := seedMatchingRecheckMove(t, f, "", true)
	recheck := seedMatchingRecheckBatch(t, f.db, source)
	seedMatchingRecheckReceipt(t, f, recheck, old, claimID, true)
	association := source.TrackSourceID
	position := old.PositionID
	repository := NewSetupManagerRepository(f.db)
	calls := 0
	install := func() (PublicationResult, error) {
		calls++
		return outcome, nil
	}
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='manual', policy_version=NULL WHERE id=?`, association).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repository.InstallMatchingRecheckSamePath(ctx, f.guard, old.RelativePath, install); !errors.Is(err, ErrPublicationAdmissionConflict) || calls != 0 {
		t.Fatalf("manual protection reached native callback: calls=%d err=%v", calls, err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic', policy_version=? WHERE id=?`, MatchingLayoutPolicyVersion, association).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	unblockInstall, releaseInstall := coordinationRelease(t)
	started := make(chan struct{})
	installed := make(chan error, 1)
	go func() {
		installed <- repository.InstallMatchingRecheckSamePath(ctx, f.guard, old.RelativePath, func() (PublicationResult, error) {
			close(started)
			<-unblockInstall
			return install()
		})
	}()
	awaitCoordinationSignal(t, started)
	contender := startCoordinationTransaction(t, f.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return err
		}
		var generation uuid.UUID
		if err := tx.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &generation); err != nil {
			return err
		}
		if generation != f.guard.CandidateGeneration {
			return errors.New("source writer acquired the gate before settlement")
		}
		return nil
	})
	assertWaitingForPostgresLock(t, f.db, awaitCoordinationPID(t, contender.pid))
	releaseInstall()
	if err := <-installed; err != nil || calls != 1 {
		t.Fatalf("commit matching MOVE calls=%d err=%v", calls, err)
	}
	if err := <-contender.done; err != nil {
		t.Fatal(err)
	}
	var correct bool
	err := f.db.NewRaw(`SELECT
		EXISTS(SELECT 1 FROM track_publication p JOIN publication_namespace_claim c ON c.owner_position_id=p.position_id
			JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id AND r.position_id=p.position_id AND r.generation=p.generation
			WHERE p.position_id=? AND p.generation=? AND p.relative_path=? AND c.id=?)
		AND NOT EXISTS(SELECT 1 FROM track_publication WHERE position_id=?)
		AND NOT EXISTS(SELECT 1 FROM publication_namespace_reservation WHERE position_id=? AND generation=?)
		AND NOT EXISTS(SELECT 1 FROM matching_local_recheck WHERE id=?)
		AND NOT EXISTS(SELECT 1 FROM track_source WHERE id=?)
		AND NOT EXISTS(SELECT 1 FROM source_cleanup_item WHERE operation_id=?)`,
		f.guard.ExpectedPositionID, f.guard.CandidateGeneration, old.RelativePath, claimID,
		position, position, old.Generation, recheck, association, f.guard.OperationID).Scan(ctx, &correct)
	if err != nil || !correct {
		t.Fatalf("same-path move did not transfer exact ownership without cleanup: %v, %v", correct, err)
	}
}

func writeMatchingRecheckOutput(t *testing.T, root, relativePath, contents string) PublicationCurrentOwnership {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(contents))
	return PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: PublicationFileIdentity{
		SizeBytes: publicationFixtureInt64(info.Size()), MTime: publicationFixtureInt64(info.ModTime().UnixMicro()), SHA256: hex.EncodeToString(digest[:]),
	}}
}

func TestFinalizeMatchingRecheckMovedOutputsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	first := preparePublicationResultFixture(t)
	second := preparePublicationResultFixtureWithDatabase(t, first.db, "second")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstOld, firstClaim, firstSource, firstResult := seedMatchingRecheckMove(t, first, root, false)
	secondOld, secondClaim, secondSource, secondResult := seedMatchingRecheckMove(t, second, root, false)
	recheck := seedMatchingRecheckBatch(t, first.db, firstSource, secondSource)
	seedMatchingRecheckReceipt(t, first, recheck, firstOld, firstClaim, false)
	seedMatchingRecheckReceipt(t, second, recheck, secondOld, secondClaim, false)
	repository := NewSetupManagerRepository(first.db)
	repository.SetRiverInserter(newSourceRepublishRiverClient(t, ctx, first.db))
	commit := func(f publicationExecutionFixture, result PublicationResult) {
		t.Helper()
		if err := repository.RecordMatchingRecheckMoveCandidate(ctx, f.guard, result.Observation.RelativePath, result.Observation.PayloadSHA256); err != nil {
			t.Fatal(err)
		}
		if committed, err := repository.CommitPublicationResult(ctx, f.guard, result); err != nil || !committed {
			t.Fatalf("commit moved result: %v, %v", committed, err)
		}
	}
	commit(first, firstResult)
	for _, id := range []uuid.UUID{firstOld.PositionID, first.guard.ExpectedPositionID} {
		if used, err := matchingRecheckPublicationUsesPosition(ctx, first.db, firstOld.Epoch, id); err != nil || !used {
			t.Fatalf("unsettled move endpoint %s is not protected: %v, %v", id, used, err)
		}
	}
	if blocked, err := libraryDeletionPositionBlocked(ctx, first.db, first.guard.ExpectedPositionID, firstOld.Epoch, first.guard.CandidateGeneration); err != nil || !blocked {
		t.Fatalf("unsettled destination is deletable: %v, %v", blocked, err)
	}
	var captures int
	capture := func(_ context.Context, relative string, current TrackPublication, claim uuid.UUID) (*ObsoletePublicationOutputCleanupAdmission, error) {
		captures++
		native := filepath.Join(root, filepath.FromSlash(relative))
		data, err := os.ReadFile(native)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(native)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		var ownership PublicationCurrentOwnership
		if err := decodeStrictJSON(current.Ownership, &ownership); err != nil {
			return nil, err
		}
		if ownership.Identity.SizeBytes == nil || *ownership.Identity.SizeBytes != info.Size() ||
			ownership.Identity.MTime == nil || *ownership.Identity.MTime != info.ModTime().UnixMicro() ||
			hex.EncodeToString(digest[:]) != ownership.Identity.SHA256 {
			return nil, errors.New("output identity changed")
		}
		id := PublicationDirectoryIdentity{Platform: "unix", Volume: "1", FileID: "2"}
		return &ObsoletePublicationOutputCleanupAdmission{Epoch: current.Epoch, PositionID: current.PositionID, Generation: current.Generation,
			RelativePath: relative, NamespaceClaimID: claim, NativePath: native, FileSize: info.Size(), ModifiedAt: info.ModTime(),
			SHA256: digest[:], OutputRootIdentity: id, ParentIdentity: id}, nil
	}
	finalize := func() {
		t.Helper()
		if err := repository.FinalizeMatchingRecheckMovedOutputs(ctx, 1, capture); err != nil {
			t.Fatal(err)
		}
	}
	finalize()
	if captures != 0 {
		t.Fatal("partial success reached native proof")
	}
	commit(second, secondResult)
	secondTarget := filepath.Join(root, filepath.FromSlash(secondResult.Observation.RelativePath))
	if err := os.Rename(secondTarget, secondTarget+".hidden"); err != nil {
		t.Fatal(err)
	}
	finalize()
	var predecessors int
	if err := first.db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id IN (?,?)`, firstOld.PositionID, secondOld.PositionID).Scan(ctx, &predecessors); err != nil || predecessors != 2 {
		t.Fatalf("missing destination retired predecessor: count=%d err=%v", predecessors, err)
	}
	if err := os.Rename(secondTarget+".hidden", secondTarget); err != nil {
		t.Fatal(err)
	}
	captures = 0
	finalize()
	var settled bool
	err = first.db.NewRaw(`SELECT NOT EXISTS(SELECT 1 FROM matching_local_recheck WHERE id=?)
		AND NOT EXISTS(SELECT 1 FROM track_publication WHERE position_id IN (?,?))
		AND NOT EXISTS(SELECT 1 FROM track_source WHERE id=?)
		AND EXISTS(SELECT 1 FROM track_publication WHERE position_id=? AND generation=?)
		AND EXISTS(SELECT 1 FROM obsolete_publication_output_cleanup c JOIN operation o ON o.id=c.operation_id
			JOIN river_job job ON job.id=o.river_job_id WHERE c.namespace_claim_id=? AND c.position_id=? AND c.generation=?)`,
		recheck, firstOld.PositionID, secondOld.PositionID, firstSource.TrackSourceID,
		first.guard.ExpectedPositionID, first.guard.CandidateGeneration, firstClaim, firstOld.PositionID, firstOld.Generation).Scan(ctx, &settled)
	if err != nil || !settled || captures != 4 {
		t.Fatalf("settle complete move with old/new output proofs: settled=%v captures=%d err=%v", settled, captures, err)
	}
	if used, err := matchingRecheckPublicationUsesPosition(ctx, first.db, firstOld.Epoch, first.guard.ExpectedPositionID); err != nil || used {
		t.Fatalf("settled destination protection was not released: %v, %v", used, err)
	}

	t.Run("abandon partial result with pruned delivery", func(t *testing.T) {
		done := preparePublicationResultFixtureWithDatabase(t, first.db, "partial-done")
		failed := preparePublicationResultFixtureWithDatabase(t, first.db, "partial-failed")
		doneOld, doneClaim, doneSource, doneResult := seedMatchingRecheckMove(t, done, root, false)
		failedOld, failedClaim, failedSource, failedResult := seedMatchingRecheckMove(t, failed, root, false)
		batch := seedMatchingRecheckBatch(t, first.db, doneSource, failedSource)
		seedMatchingRecheckReceipt(t, done, batch, doneOld, doneClaim, false)
		seedMatchingRecheckReceipt(t, failed, batch, failedOld, failedClaim, false)
		if err := NewSettingsRepository(first.db).Set(ctx, "output_directory", root); err != nil {
			t.Fatal(err)
		}
		exec := func(sql string, args ...any) {
			t.Helper()
			if _, err := first.db.NewRaw(sql, args...).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		exec(`UPDATE operation SET state='failed',stage='failed',safe_error='delivery failed',finished_at=now() WHERE id=?`, failed.guard.OperationID)
		exec(`UPDATE publication_execution_claim SET state='quiescent' WHERE operation_id=?`, failed.guard.OperationID)
		exec(`UPDATE river_job SET state='discarded',finalized_at=now() WHERE id=?`, failed.guard.RiverJobID)
		exec(`DELETE FROM river_job WHERE id=?`, failed.guard.RiverJobID)
		if _, err := repository.PreviewMatchingRecheckAbandonment(ctx, failed.guard.OperationID); !errors.Is(err, ErrPublicationAdmissionConflict) {
			t.Fatalf("abandonment without a committed replacement: %v", err)
		}
		commit(done, doneResult)
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(failedResult.Observation.RelativePath))); err != nil {
			t.Fatal(err)
		}
		preview, err := repository.PreviewMatchingRecheckAbandonment(ctx, failed.guard.OperationID)
		if err != nil || len(preview.Positions) != 2 {
			t.Fatalf("preview partial abandonment: %+v, %v", preview, err)
		}
		verify := func(ctx context.Context, output string, pred []MatchingRecheckAbandonmentPredecessor) error {
			if output != root || len(pred) != 1 || pred[0].Current.PositionID != failedOld.PositionID ||
				pred[0].AbsentTargetPath != failedResult.Observation.RelativePath {
				return errors.New("unexpected abandonment proof scope")
			}
			if _, err := os.Lstat(filepath.Join(output, filepath.FromSlash(pred[0].AbsentTargetPath))); !errors.Is(err, os.ErrNotExist) {
				return errors.New("abandoned destination is not absent")
			}
			_, err := capture(ctx, pred[0].Current.RelativePath, pred[0].Current, pred[0].NamespaceClaimID)
			return err
		}
		if err := repository.AbandonMatchingRecheckPublications(ctx, failed.guard.OperationID, strings.Repeat("0", 64), verify); !errors.Is(err, ErrPublicationAdmissionConflict) {
			t.Fatalf("changed abandonment revision: %v", err)
		}
		if err := repository.AbandonMatchingRecheckPublications(ctx, failed.guard.OperationID, preview.Revision, verify); err != nil {
			t.Fatal(err)
		}
		var closed bool
		if err := first.db.NewRaw(`SELECT NOT EXISTS(SELECT 1 FROM matching_local_recheck WHERE id=?)
			AND NOT EXISTS(SELECT 1 FROM operation WHERE id=?)
			AND NOT EXISTS(SELECT 1 FROM publication_namespace_claim WHERE owner_position_id=?)
			AND EXISTS(SELECT 1 FROM track_publication WHERE position_id=? AND generation=?)
			AND EXISTS(SELECT 1 FROM track_publication WHERE position_id=? AND generation=?)`,
			batch, failed.guard.OperationID, failed.guard.ExpectedPositionID,
			done.guard.ExpectedPositionID, done.guard.CandidateGeneration,
			failedOld.PositionID, failedOld.Generation).Scan(ctx, &closed); err != nil || !closed {
			t.Fatalf("partial abandonment did not preserve committed and untouched CURRENT: %v, %v", closed, err)
		}
	})
}
