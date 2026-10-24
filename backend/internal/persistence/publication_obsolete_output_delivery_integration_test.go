//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/uptrace/bun"
)

type obsoleteCleanupTestInserter struct{ failAfterInsert bool }

func (i obsoleteCleanupTestInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ($1,$2::jsonb,'available',0) RETURNING id`, args.Kind(), `{"operation_id":"`+args.(ObsoletePublicationOutputCleanupJobArgs).OperationID.String()+`"}`).Scan(&id)
	if err != nil {
		return nil, err
	}
	if i.failAfterInsert {
		return nil, errors.New("injected enqueue failure")
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: id}}, nil
}

func TestAdmitObsoletePublicationOutputCleanupAtomicPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, repo := readyFirstPublicationCommitFixture(t)
	initializeRiver(t, ctx, f.db)
	position := f.guard.ExpectedPositionID
	oldGeneration := uuid.New()
	keyDigest := sha256.Sum256([]byte("obsolete.flac"))
	claim := &PublicationNamespaceClaim{ID: uuid.New(), Epoch: f.guard.Epoch, KeyDigest: keyDigest[:], CanonicalKey: "obsolete.flac", RelativePath: "obsolete.flac", NodeKind: "file", OwnerPositionID: &position}
	if _, err := f.db.NewInsert().Model(claim).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewInsert().Model(&PublicationNamespaceReservation{NamespaceClaimID: claim.ID, PositionID: position, Generation: oldGeneration}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	payloadDigest := sha256.Sum256([]byte("payload"))
	request := ObsoletePublicationOutputCleanupAdmission{
		Epoch: f.guard.Epoch, PositionID: position, Generation: oldGeneration, RelativePath: "obsolete.flac",
		NativePath: filepath.Join(root, "obsolete.flac"), OutputRootIdentity: PublicationDirectoryIdentity{Platform: runtime.GOOS, Volume: "volume", FileID: "root"},
		ParentIdentity: PublicationDirectoryIdentity{Platform: runtime.GOOS, Volume: "volume", FileID: "parent"}, NamespaceClaimID: claim.ID,
		FileSize: 10, ModifiedAt: time.Now().UTC(), SHA256: payloadDigest[:],
	}
	args := func(id uuid.UUID) river.JobArgs { return ObsoletePublicationOutputCleanupJobArgs{OperationID: id} }
	var before int
	if err := f.db.NewRaw(`SELECT count(*) FROM operation WHERE kind=?`, ObsoletePublicationOutputCleanupOperationKind).Scan(ctx, &before); err != nil {
		t.Fatal(err)
	}
	err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return err
		}
		_, err := repo.AdmitObsoletePublicationOutputCleanupTx(ctx, tx, request, obsoleteCleanupTestInserter{failAfterInsert: true}, args, nil)
		return err
	})
	if err == nil {
		t.Fatal("expected injected enqueue failure")
	}
	var after int
	if err := f.db.NewRaw(`SELECT count(*) FROM operation WHERE kind=?`, ObsoletePublicationOutputCleanupOperationKind).Scan(ctx, &after); err != nil || after != before {
		t.Fatalf("operation count after rollback = %d, %v", after, err)
	}
	var authorities, jobs int
	if err := f.db.NewRaw(`SELECT count(*) FROM obsolete_publication_output_cleanup`).Scan(ctx, &authorities); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind=? AND args->>'operation_id' IS NOT NULL`, ObsoletePublicationOutputCleanupJobKind).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if authorities != 0 || jobs != 0 {
		t.Fatalf("rollback left authority/jobs: %d/%d", authorities, jobs)
	}

	var operationID uuid.UUID
	err = f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return err
		}
		op, err := repo.AdmitObsoletePublicationOutputCleanupTx(ctx, tx, request, obsoleteCleanupTestInserter{}, args, nil)
		if err == nil {
			operationID = op.ID
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var opCount, authorityCount, jobCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM operation WHERE id=?`, operationID).Scan(ctx, &opCount); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM obsolete_publication_output_cleanup WHERE operation_id=?`, operationID).Scan(ctx, &authorityCount); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE id=(SELECT river_job_id FROM operation WHERE id=?)`, operationID).Scan(ctx, &jobCount); err != nil {
		t.Fatal(err)
	}
	if opCount != 1 || authorityCount != 1 || jobCount != 1 {
		t.Fatalf("admitted operation/authority/job = %d/%d/%d", opCount, authorityCount, jobCount)
	}
	var liveAuthority int
	if err := f.db.NewRaw(`SELECT count(*) FROM obsolete_publication_output_cleanup WHERE namespace_claim_id=?`, claim.ID).Scan(ctx, &liveAuthority); err != nil || liveAuthority != 1 {
		t.Fatalf("cleanup authority baseline for claim = %d, %v", liveAuthority, err)
	}
	// Live cleanup authority keeps the old canonical namespace unavailable to
	// replacement generations, including a replacement for the same position.
	replacement := PublicationAdmission{
		PositionID: position, ExpectedEpoch: f.guard.Epoch, CandidateGeneration: uuid.New(),
		NamespaceManifest: []PublicationNamespaceNode{{CanonicalKey: "obsolete.flac", RelativePath: "obsolete.flac", NodeKind: "file"}},
	}
	err = f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return reservePublicationNamespace(ctx, tx, replacement)
	})
	if !errors.Is(err, ErrPublicationAdmissionConflict) {
		t.Fatalf("cleanup-owned namespace reuse error = %v, want admission conflict", err)
	}
	var unchanged string
	if err := f.db.NewRaw(`SELECT relative_path FROM publication_namespace_claim WHERE id=?`, claim.ID).Scan(ctx, &unchanged); err != nil || unchanged != "obsolete.flac" {
		t.Fatalf("cleanup-owned claim changed: path=%q err=%v", unchanged, err)
	}
	var snapshot string
	if err := f.db.NewRaw(`SELECT input_snapshot::text FROM operation WHERE id=?`, operationID).Scan(ctx, &snapshot); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(snapshot), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || len(decoded["operation_id"]) == 0 {
		t.Fatalf("cleanup operation snapshot carries excess authority: %s", snapshot)
	}
	op, err := repo.GetOperation(ctx, operationID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	remove := func(context.Context, ObsoletePublicationOutputCleanup) error { calls++; return nil }
	if acquired, err := repo.ExecuteObsoletePublicationOutputCleanup(ctx, operationID, op.Attempt, *op.RiverJobID+1, remove); err != nil || acquired || calls != 0 {
		t.Fatalf("stale job invoked cleanup: acquired=%t calls=%d err=%v", acquired, calls, err)
	}
	if acquired, err := repo.ExecuteObsoletePublicationOutputCleanup(ctx, operationID, op.Attempt, *op.RiverJobID, remove); err != nil || !acquired || calls != 1 {
		t.Fatalf("live cleanup did not settle: acquired=%t calls=%d err=%v", acquired, calls, err)
	}
	if row, err := repo.ReadObsoletePublicationOutputCleanup(ctx, operationID); err != nil || row != nil {
		t.Fatalf("settled authority retained: %+v %v", row, err)
	}
	if acquired, err := repo.ExecuteObsoletePublicationOutputCleanup(ctx, operationID, op.Attempt, *op.RiverJobID, remove); err != nil || acquired || calls != 1 {
		t.Fatalf("completed redelivery invoked filesystem: acquired=%t calls=%d err=%v", acquired, calls, err)
	}
	// A later independent obsolete path may fail without reviving the successful
	// action or losing the unresolved deletion authority.
	claim.ID = uuid.New()
	claim.RelativePath, claim.CanonicalKey = "failed.flac", "failed.flac"
	keyDigest = sha256.Sum256([]byte(claim.RelativePath))
	claim.KeyDigest = keyDigest[:]
	if _, err := f.db.NewInsert().Model(claim).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewInsert().Model(&PublicationNamespaceReservation{NamespaceClaimID: claim.ID, PositionID: position, Generation: oldGeneration}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	request.NamespaceClaimID, request.RelativePath, request.NativePath = claim.ID, claim.RelativePath, filepath.Join(root, claim.RelativePath)
	err = f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var admissionErr error
		op, admissionErr = repo.AdmitObsoletePublicationOutputCleanupTx(ctx, tx, request, obsoleteCleanupTestInserter{}, args, nil)
		return admissionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("private filesystem diagnostic")
	remove = func(context.Context, ObsoletePublicationOutputCleanup) error { calls++; return failure }
	if acquired, err := repo.ExecuteObsoletePublicationOutputCleanup(ctx, op.ID, op.Attempt, *op.RiverJobID, remove); !acquired || !errors.Is(err, failure) || calls != 2 {
		t.Fatalf("failed cleanup not recorded: acquired=%t calls=%d err=%v", acquired, calls, err)
	}
	row, err := repo.ReadObsoletePublicationOutputCleanup(ctx, op.ID)
	if err != nil || row == nil || row.State != "failed" || row.SafeError == nil || *row.SafeError == "" || *row.SafeError == failure.Error() {
		t.Fatalf("failed authority or safe error lost: %+v %v", row, err)
	}
	if acquired, err := repo.ExecuteObsoletePublicationOutputCleanup(ctx, op.ID, op.Attempt, *op.RiverJobID, remove); err != nil || acquired || calls != 2 {
		t.Fatalf("failed terminal delivery repeated filesystem work: acquired=%t calls=%d err=%v", acquired, calls, err)
	}
}
