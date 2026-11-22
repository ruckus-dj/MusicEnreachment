//go:build integration

package service_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// TestObsoleteOutputCleanupRetryFailsClosedPostgreSQL pins that the generic
// operation retry never routes a failed obsolete output cleanup into the
// install/move dispatcher. The failed operation, its River delivery and its
// live cleanup authority must all stay exactly as the failed delivery left
// them: no attempt change and no new job.
func TestObsoleteOutputCleanupRetryFailsClosedPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := testpostgres.OpenMigratedWithRiver(t)
	client := openSourceScanRiver(t, database)
	repository := persistence.NewSetupManagerRepository(database)

	epoch, positionID, generation, claimID := seedObsoleteOutputCleanupAuthority(t, ctx, database)
	oldRoot := t.TempDir()
	content := []byte("obsolete output")
	digest := sha256.Sum256(content)
	var cleanup *persistence.Operation
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		cleanup, err = repository.AdmitObsoletePublicationOutputCleanupTx(ctx, tx,
			persistence.ObsoletePublicationOutputCleanupAdmission{
				Epoch: epoch, PositionID: positionID, Generation: generation,
				RelativePath: "obsolete.flac", NativePath: filepath.Join(oldRoot, "obsolete.flac"),
				NamespaceClaimID:   claimID,
				OutputRootIdentity: persistence.PublicationDirectoryIdentity{Platform: "unix", Volume: "volume", FileID: "root"},
				ParentIdentity:     persistence.PublicationDirectoryIdentity{Platform: "unix", Volume: "volume", FileID: "parent"},
				FileSize:           int64(len(content)), ModifiedAt: time.Now().UTC(), SHA256: digest[:],
			}, client, func(id uuid.UUID) river.JobArgs {
				return persistence.ObsoletePublicationOutputCleanupJobArgs{OperationID: id}
			}, nil)
		return err
	}); err != nil {
		t.Fatalf("admit obsolete output cleanup: %v", err)
	}
	if cleanup.RiverJobID == nil {
		t.Fatal("admitted cleanup has no River job")
	}
	removeErr := errors.New("injected obsolete output removal failure")
	acquired, err := repository.ExecuteObsoletePublicationOutputCleanup(ctx, cleanup.ID, cleanup.Attempt, *cleanup.RiverJobID,
		func(context.Context, persistence.ObsoletePublicationOutputCleanup) error {
			return removeErr
		})
	if !acquired || !errors.Is(err, removeErr) {
		t.Fatalf("execute failed cleanup = acquired %t, err %v; want a recorded failure", acquired, err)
	}
	if _, err := database.NewRaw(`UPDATE river_job SET state='completed', finalized_at=now() WHERE id=?`, *cleanup.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("finalize the settled cleanup delivery: %v", err)
	}
	before, err := repository.GetOperation(ctx, cleanup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.State != "failed" || before.Stage != "cleanup" || before.RiverJobID == nil ||
		*before.RiverJobID != *cleanup.RiverJobID || before.Attempt != cleanup.Attempt || before.FinishedAt == nil || before.SafeError == nil {
		t.Fatalf("failed cleanup operation = %+v; want its failed delivery", before)
	}
	beforeAuthority, err := repository.ReadObsoletePublicationOutputCleanup(ctx, cleanup.ID)
	if err != nil || beforeAuthority == nil || beforeAuthority.State != "failed" {
		t.Fatalf("failed cleanup authority = %+v, err %v", beforeAuthority, err)
	}
	var jobsBefore int
	if err := database.NewRaw(`SELECT count(*) FROM river_job WHERE args->>'operation_id' = ?`, cleanup.ID.String()).Scan(ctx, &jobsBefore); err != nil {
		t.Fatal(err)
	}

	operations := service.NewOperationsWithRiver(repository, client)
	got, err := operations.Retry(ctx, cleanup.ID)
	if err == nil || got != nil || !strings.Contains(err.Error(), "unsupported operation kind") {
		t.Fatalf("unsupported cleanup retry = %v, %v; want a fail-closed rejection", got, err)
	}

	after, err := repository.GetOperation(ctx, cleanup.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterAuthority, err := repository.ReadObsoletePublicationOutputCleanup(ctx, cleanup.ID)
	if err != nil {
		t.Fatal(err)
	}
	var jobsAfter int
	if err := database.NewRaw(`SELECT count(*) FROM river_job WHERE args->>'operation_id' = ?`, cleanup.ID.String()).Scan(ctx, &jobsAfter); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("cleanup retry changed its operation: before=%+v after=%+v", before, after)
	}
	if !reflect.DeepEqual(beforeAuthority, afterAuthority) {
		t.Fatalf("cleanup retry changed its authority: before=%+v after=%+v", beforeAuthority, afterAuthority)
	}
	if jobsBefore != 1 || jobsAfter != jobsBefore {
		t.Fatalf("cleanup retry changed its River jobs: before=%d after=%d", jobsBefore, jobsAfter)
	}
}

// seedObsoleteOutputCleanupAuthority creates the minimum live namespace
// identity an obsolete output cleanup is admitted from: one superseded file
// claim and its reservation, with no current publication consuming it.
func seedObsoleteOutputCleanupAuthority(t *testing.T, ctx context.Context, database *bun.DB) (uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	var epoch uuid.UUID
	if err := database.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatalf("read namespace epoch: %v", err)
	}
	artistID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New()
	positionID, generation, claimID := uuid.New(), uuid.New(), uuid.New()
	if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_artist(id,name) VALUES (?, 'Artist')`, artistID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?, 'release')`, releaseID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_artist(release_id,artist_id,position,name,name_join_phrase) VALUES (?,?,0,'Artist','')`, releaseID, artistID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'track')`, trackID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_track(id,release_id,position,track_id) VALUES (?,?,0,?)`, positionID, releaseID, trackID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id)
			VALUES (?,?,decode(repeat('ab',32),'hex'),'obsolete.flac','obsolete.flac','file',?)`, claimID, epoch, positionID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, positionID, generation)
		return err
	}); err != nil {
		t.Fatalf("seed obsolete output cleanup authority: %v", err)
	}
	return epoch, positionID, generation, claimID
}
