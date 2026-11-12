//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	riverdatabasesql "github.com/riverqueue/river/riverdriver/riverdatabasesql"
	rivermigrate "github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type countingLibraryDeletionOpener struct {
	delegate sourcefs.OutputOpener
	opens    atomic.Int64
}

func (opener *countingLibraryDeletionOpener) OpenRoot(ctx context.Context, path string) (sourcefs.OutputDirectory, error) {
	opener.opens.Add(1)
	return opener.delegate.OpenRoot(ctx, path)
}

func TestLibraryDeletionServiceNativePartialSettlementAndExplicitRetryPostgreSQL(t *testing.T) {
	ctx := context.Background()
	db := testpostgres.OpenMigratedWithRiver(t)
	migrator, err := rivermigrate.New(riverdatabasesql.New(db.DB), nil)
	if err != nil {
		t.Fatalf("create River migrator: %v", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}
	riverClient, err := river.NewClient(riverdatabasesql.New(db.DB), &river.Config{})
	if err != nil {
		t.Fatalf("create actual River client: %v", err)
	}
	repository := persistence.NewSetupManagerRepository(db)
	outputRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize native output root: %v", err)
	}
	registry := settings.New(persistence.NewSettingsRepository(db), nil)
	if err := registry.SetOutputDirectory(ctx, outputRoot, ""); err != nil {
		t.Fatalf("verify and configure native output root: %v", err)
	}
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	releaseID, trackOneID, trackTwoID := uuid.New(), uuid.New(), uuid.New()
	artistID, recordingOneID, recordingTwoID := uuid.New(), uuid.New(), uuid.New()
	positionOneID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	positionTwoID := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	generationOne, generationTwo := uuid.New(), uuid.New()
	outputOne, outputTwo := "first.flac", "second.flac"
	firstBytes, secondBytes := []byte("first published data"), []byte("second published!")
	facts := []struct {
		positionID, trackID, generation uuid.UUID
		path                            string
		content                         []byte
	}{{positionOneID, trackOneID, generationOne, outputOne, firstBytes}, {positionTwoID, trackTwoID, generationTwo, outputTwo, secondBytes}}
	for _, item := range facts {
		if err := os.WriteFile(filepath.Join(outputRoot, item.path), item.content, 0o600); err != nil {
			t.Fatalf("write native publication output %s: %v", item.path, err)
		}
	}
	mtime := time.Date(2025, 4, 3, 2, 1, 0, 123456000, time.UTC)
	for _, item := range facts {
		path := filepath.Join(outputRoot, item.path)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("set publication timestamp %s: %v", item.path, err)
		}
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		statements := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO local_release(id,title) VALUES (?, 'native deletion fixture')`, []any{releaseID}},
			{`INSERT INTO local_track(id,title) VALUES (?, 'first')`, []any{trackOneID}},
			{`INSERT INTO local_track(id,title) VALUES (?, 'second')`, []any{trackTwoID}},
			{`INSERT INTO local_artist(id,name) VALUES (?, 'Artist')`, []any{artistID}},
			{`INSERT INTO local_recording(id,title) VALUES (?, 'first')`, []any{recordingOneID}},
			{`INSERT INTO local_recording(id,title) VALUES (?, 'second')`, []any{recordingTwoID}},
			{`INSERT INTO release_track(id,release_id,track_id,recording_id,position) VALUES (?,?,?,?,0)`, []any{positionOneID, releaseID, trackOneID, recordingOneID}},
			{`INSERT INTO release_track(id,release_id,track_id,recording_id,position) VALUES (?,?,?,?,1)`, []any{positionTwoID, releaseID, trackTwoID, recordingTwoID}},
			{`INSERT INTO release_artist(release_id,artist_id,position,name,name_join_phrase) VALUES (?,?,0,'Artist','')`, []any{releaseID, artistID}},
			{`INSERT INTO recording_artist(recording_id,position,artist_id,name,name_join_phrase) VALUES (?,?,?,'Artist','')`, []any{recordingOneID, 0, artistID}},
			{`INSERT INTO recording_artist(recording_id,position,artist_id,name,name_join_phrase) VALUES (?,?,?,'Artist','')`, []any{recordingTwoID, 0, artistID}},
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed native deletion library rows: %v", err)
	}
	for _, item := range facts {
		path := filepath.Join(outputRoot, item.path)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat native publication output %s: %v", item.path, err)
		}
		digest := sha256.Sum256(item.content)
		size, modified := info.Size(), info.ModTime().UnixMicro()
		ownership, err := json.Marshal(persistence.PublicationCurrentOwnership{
			Version: persistence.PublicationCurrentOwnershipVersion,
			Identity: persistence.PublicationFileIdentity{
				SizeBytes: &size, MTime: &modified, SHA256: hex.EncodeToString(digest[:]),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at)
			VALUES (?,?,?,?,?::jsonb,'{}','{}','{}',1,now())`, item.positionID, epoch, item.generation, item.path, string(ownership)); err != nil {
			t.Fatalf("seed CURRENT publication %s: %v", item.path, err)
		}
		claimID := uuid.New()
		keyDigest := sha256.Sum256([]byte(item.path))
		if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id)
			VALUES (?,?,?::bytea,?,?,'file',?)`, claimID, epoch, keyDigest[:], item.path, item.path, item.positionID); err != nil {
			t.Fatalf("seed namespace claim %s: %v", item.path, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, item.positionID, item.generation); err != nil {
			t.Fatalf("seed namespace reservation %s: %v", item.path, err)
		}
	}
	opener := &countingLibraryDeletionOpener{delegate: sourcefs.NewOutputOpener()}
	deletion := NewLibraryDeletionService(repository, publication.NewCoordinator(), uuid.New(), opener)
	preview, err := deletion.Preview(ctx, "release", releaseID, uuid.Nil)
	if err != nil {
		t.Fatalf("preview native release deletion: %v", err)
	}
	if len(preview.Blockers) != 0 || len(preview.OutputFacts) != 2 || !preview.OutputFacts[0].Capturable || !preview.OutputFacts[1].Capturable {
		t.Fatalf("native deletion preview is not fully capturable: %+v", preview)
	}
	operation, err := deletion.Confirm(ctx, preview.ConfirmationDigest, riverClient, nil)
	if err != nil {
		t.Fatalf("confirm native release deletion: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("confirmed deletion has no actual River job ID")
	}
	admitted, err := repository.GetOperation(ctx, operation.ID)
	if err != nil || admitted == nil {
		t.Fatalf("read admitted deletion authority: operation=%v err=%v", admitted, err)
	}
	var admittedSnapshot persistence.LibraryDeletionOperationSnapshot
	if err := json.Unmarshal(admitted.InputSnapshot, &admittedSnapshot); err != nil {
		t.Fatalf("decode admitted deletion authority: %v", err)
	}
	var originalSecondWitness json.RawMessage
	for _, raw := range admittedSnapshot.OwnershipWitnesses {
		var witness persistence.LibraryDeletionPublicationWitness
		if err := json.Unmarshal(raw, &witness); err != nil {
			t.Fatalf("decode admitted publication witness: %v", err)
		}
		if witness.PositionID == positionTwoID {
			originalSecondWitness = append(json.RawMessage(nil), raw...)
		}
	}
	if len(originalSecondWitness) == 0 {
		t.Fatal("admitted snapshot omitted the second publication witness")
	}
	firstJobID := *operation.RiverJobID
	setJobState := func(jobID int64, state string) {
		t.Helper()
		result, err := db.NewRaw(`UPDATE river_job SET state=?,attempt=CASE WHEN ?='running' THEN 1 ELSE attempt END,
			finalized_at=CASE WHEN ?='completed' THEN now() ELSE finalized_at END
			WHERE id=? AND kind=?`, state, state, state, jobID, persistence.LibraryDeletionOperationKind).Exec(ctx)
		if err != nil {
			t.Fatalf("mark actual River job %s: %v", state, err)
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			t.Fatalf("expected one actual River job to become %s; rows=%d err=%v", state, rows, err)
		}
	}
	setJobRunning := func(jobID int64) { setJobState(jobID, "running") }
	setJobRunning(firstJobID)
	collectionID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO collection(id,title) VALUES (?,'post-confirm deletion scope')`, collectionID); err != nil {
		t.Fatalf("seed post-confirm collection: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO collection_release(collection_id,position,release_id) VALUES (?,0,?)`, collectionID, releaseID); err != nil {
		t.Fatalf("change frozen collection scope: %v", err)
	}
	opener.opens.Store(0)
	if err := deletion.ExecuteDelivery(ctx, operation.ID, firstJobID, 1); err == nil {
		t.Fatal("post-confirm scope conflict was acknowledged without an explicit-recovery failure")
	}
	if opens := opener.opens.Load(); opens != 0 {
		t.Fatalf("blocked frozen scope opened native filesystem %d times", opens)
	}
	var persisted persistence.Operation
	if err := db.NewSelect().Model(&persisted).Where("id=?", operation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if persisted.State != "failed" || persisted.Stage != "deletion_recovery_required" || persisted.SafeError == nil || strings.TrimSpace(*persisted.SafeError) == "" {
		t.Fatalf("changed frozen scope state=%s/%s safe_error=%v, want actionable failed recovery state", persisted.State, persisted.Stage, persisted.SafeError)
	}
	if !bytes.Equal(persisted.InputSnapshot, admitted.InputSnapshot) {
		t.Fatal("scope-conflict recovery changed the immutable confirmation digest or publication witnesses")
	}
	if recorded, err := deletion.DeliveryFailureWasRecorded(ctx, operation.ID, firstJobID); err != nil || !recorded {
		t.Fatalf("failed scope delivery durability check = %t, %v; want durable explicit-recovery failure", recorded, err)
	}
	for _, path := range []string{outputOne, outputTwo} {
		if _, err := os.Stat(filepath.Join(outputRoot, path)); err != nil {
			t.Fatalf("scope conflict changed publication output %q: %v", path, err)
		}
	}

	setJobState(firstJobID, "completed")
	if _, err := db.ExecContext(ctx, `DELETE FROM collection_release WHERE collection_id=? AND release_id=?`, collectionID, releaseID); err != nil {
		t.Fatalf("restore frozen collection scope: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM collection WHERE id=?`, collectionID); err != nil {
		t.Fatalf("remove post-confirm collection: %v", err)
	}
	if _, err := deletion.Retry(ctx, operation.ID, riverClient); err != nil {
		t.Fatalf("explicitly retry deletion after restoring frozen scope: %v", err)
	}
	if err := db.NewSelect().Model(&persisted).Where("id=?", operation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(persisted.InputSnapshot, admitted.InputSnapshot) {
		t.Fatal("explicit retry after scope restoration replaced the original deletion scope")
	}
	secondJobID := *persisted.RiverJobID
	setJobRunning(secondJobID)
	staleGuard := persistence.LibraryDeletionDeliveryGuard{
		OperationID: operation.ID, ApplicationAttempt: persisted.Attempt, RiverJobID: secondJobID,
		RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(),
	}
	if outcome, err := repository.ClaimLibraryDeletionDelivery(ctx, staleGuard); err != nil || outcome != persistence.LibraryDeletionDeliveryAcquired {
		t.Fatalf("establish interrupted actual delivery baseline: outcome=%q err=%v", outcome, err)
	}
	if err := deletion.ExecuteDelivery(ctx, operation.ID, secondJobID, 1); err == nil {
		t.Fatal("interrupted delivery returned success instead of requiring a durable-recovery check")
	}
	if err := db.NewSelect().Model(&persisted).Where("id=?", operation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if persisted.State != "failed" || persisted.Stage != "deletion_recovery_required" {
		t.Fatalf("interrupted delivery state=%s/%s, want failed/deletion_recovery_required", persisted.State, persisted.Stage)
	}
	if _, err := deletion.Retry(ctx, operation.ID, riverClient); err != nil {
		t.Fatalf("explicitly retry interrupted deletion: %v", err)
	}
	setJobState(secondJobID, "completed")
	if err := db.NewSelect().Model(&persisted).Where("id=?", operation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	thirdJobID := *persisted.RiverJobID
	setJobRunning(thirdJobID)
	opener.opens.Store(0)
	if err := os.WriteFile(filepath.Join(outputRoot, outputTwo), []byte("replacement data!"), 0o600); err != nil {
		t.Fatalf("replace second output after preview: %v", err)
	}
	if err := os.Chtimes(filepath.Join(outputRoot, outputTwo), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if err := deletion.ExecuteDelivery(ctx, operation.ID, thirdJobID, 1); err == nil || !strings.Contains(err.Error(), "frozen publication output hash changed") {
		t.Fatalf("native replacement failure = %v, want frozen publication hash mismatch", err)
	}
	replacement := []byte("replacement data!")
	if got, err := os.ReadFile(filepath.Join(outputRoot, outputTwo)); err != nil || string(got) != string(replacement) {
		t.Fatalf("changed output replacement was not preserved: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, outputOne)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first captured output was not removed before partial settlement: %v", err)
	}
	if err := db.NewSelect().Model(&persisted).Where("id=?", operation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var partial persistence.LibraryDeletionOperationSnapshot
	if err := json.Unmarshal(persisted.InputSnapshot, &partial); err != nil {
		t.Fatal(err)
	}
	if persisted.State != "failed" || len(partial.ResidualPublicationIDs) != 1 || partial.ResidualPublicationIDs[0] != positionTwoID {
		t.Fatalf("partial native failure state=%s residual=%v; want only second witness retained", persisted.State, partial.ResidualPublicationIDs)
	}
	var retainedSecondWitness json.RawMessage
	for _, raw := range partial.OwnershipWitnesses {
		var witness persistence.LibraryDeletionPublicationWitness
		if err := json.Unmarshal(raw, &witness); err != nil {
			t.Fatalf("decode retained publication witness: %v", err)
		}
		if witness.PositionID == positionTwoID {
			retainedSecondWitness = raw
		}
	}
	if !bytes.Equal(retainedSecondWitness, originalSecondWitness) {
		t.Fatalf("partial failure changed the original second publication witness: got %s, want %s", retainedSecondWitness, originalSecondWitness)
	}
	originalFrozen, err := decodeLibraryDeletionFrozenOutput(originalSecondWitness)
	if err != nil {
		t.Fatalf("decode original second native witness: %v", err)
	}
	var retainedCurrent persistence.TrackPublication
	if err := db.NewSelect().Model(&retainedCurrent).Where("position_id=?", positionTwoID).Scan(ctx); err != nil {
		t.Fatalf("read retained CURRENT publication: %v", err)
	}
	if retainedCurrent.Generation != originalFrozen.Generation || retainedCurrent.RelativePath != originalFrozen.RelativePath || !bytes.Equal(retainedCurrent.Ownership, originalFrozen.Ownership) {
		t.Fatalf("partial failure changed original CURRENT authority: %+v", retainedCurrent)
	}
	var currentCount int
	if err := db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id IN (?,?)`, positionOneID, positionTwoID).Scan(ctx, &currentCount); err != nil {
		t.Fatal(err)
	}
	if currentCount != 1 {
		t.Fatalf("partial native failure retained %d CURRENT rows, want replacement's one row", currentCount)
	}

	movedReplacement := filepath.Join(outputRoot, "moved-replacement.flac")
	if err := os.Rename(filepath.Join(outputRoot, outputTwo), movedReplacement); err != nil {
		t.Fatalf("move replacement away from frozen leaf: %v", err)
	}
	if _, err := deletion.Retry(ctx, operation.ID, riverClient); err != nil {
		t.Fatalf("explicitly retry partially settled deletion: %v", err)
	}
	if err := db.NewSelect().Model(&persisted).Where("id=?", operation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	fourthJobID := *persisted.RiverJobID
	setJobRunning(fourthJobID)
	opener.opens.Store(0)
	if err := deletion.ExecuteDelivery(ctx, operation.ID, fourthJobID, 1); err != nil {
		t.Fatalf("settle absent original output from retained witness: %v", err)
	}
	if opens := opener.opens.Load(); opens == 0 {
		t.Fatal("native explicit retry did not traverse the original absent output witness")
	}
	if got, err := os.ReadFile(movedReplacement); err != nil || string(got) != string(replacement) {
		t.Fatalf("moved replacement was not preserved by absent-leaf retry: %q err=%v", got, err)
	}
	if err := db.NewSelect().Model(&persisted).Where("id=?", operation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if persisted.State != "succeeded" {
		t.Fatalf("explicit native retry ended in state %q, want succeeded", persisted.State)
	}
	setJobState(fourthJobID, "completed")
	opener.opens.Store(0)
	if err := deletion.ExecuteDelivery(ctx, operation.ID, fourthJobID, 1); err != nil {
		t.Fatalf("completed delivery redelivery: %v", err)
	}
	if opens := opener.opens.Load(); opens != 0 {
		t.Fatalf("completed delivery opened native filesystem %d times", opens)
	}
	var remainingRelease, remainingCurrent int
	if err := db.NewRaw(`SELECT count(*) FROM local_release WHERE id=?`, releaseID).Scan(ctx, &remainingRelease); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id IN (?,?)`, positionOneID, positionTwoID).Scan(ctx, &remainingCurrent); err != nil {
		t.Fatal(err)
	}
	if remainingRelease != 0 || remainingCurrent != 0 {
		t.Fatalf("successful native retry retained release/CURRENT rows: %d/%d", remainingRelease, remainingCurrent)
	}
}
