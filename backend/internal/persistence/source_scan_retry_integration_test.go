//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// sourceScanRetryInserter stands in for the River client of a retry. It counts
// the candidate rows the retry transaction holds at the moment the job is
// inserted, which is the only point where the order of the cleanup and the
// enqueue is observable, and it can fail the insert instead of delegating.
type sourceScanRetryInserter struct {
	client      persistence.RiverInserter
	operationID uuid.UUID
	candidates  int
	err         error
}

func (inserter *sourceScanRetryInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if err := tx.QueryRowContext(ctx,
		"SELECT count(*) FROM source_scan_candidate WHERE operation_id = $1::uuid",
		inserter.operationID.String(),
	).Scan(&inserter.candidates); err != nil {
		return nil, err
	}
	if inserter.err != nil {
		return nil, inserter.err
	}
	return inserter.client.InsertTx(ctx, tx, args, options)
}

type sourceScanRetryJobRow struct {
	Kind string `bun:"kind"`
	Args string `bun:"args"`
}

func readSourceScanRetryJob(t *testing.T, ctx context.Context, database *bun.DB, id int64) sourceScanRetryJobRow {
	t.Helper()
	row := sourceScanRetryJobRow{}
	if err := database.NewRaw("SELECT kind, args::text AS args FROM river_job WHERE id = ?", id).Scan(ctx, &row); err != nil {
		t.Fatalf("read River job %d: %v", id, err)
	}
	return row
}

func failedSourceScanRetryOperation(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, inventory *persistence.SourceInventoryRepository, root *persistence.SourceRoot, stage, safe string) *persistence.Operation {
	t.Helper()
	currentRoot, err := inventory.GetSourceRoot(ctx, root.ID)
	if err != nil {
		t.Fatalf("read the source root for the scan snapshot: %v", err)
	}
	root.ScanGeneration = currentRoot.ScanGeneration
	finished := time.Now().UTC()
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "scan_source", State: "failed", Stage: stage,
		InputSnapshot: []byte(`{"schema_version":3,"source_root_id":"` + root.ID.String() +
			`","configured_path":"` + root.ConfiguredPath + `","scan_generation":` + fmt.Sprint(root.ScanGeneration) +
			`,"sha256_enabled":false,"tools":[]}`),
		TargetSourceRootID: &root.ID, Attempt: 1, SafeError: stringPointer(safe), FinishedAt: &finished,
	}
	if err := repository.CreateOperation(ctx, operation); err != nil {
		t.Fatalf("create the failed scan operation: %v", err)
	}
	return operation
}

func storeSourceScanRetryCandidates(t *testing.T, ctx context.Context, inventory *persistence.SourceInventoryRepository, operationID uuid.UUID, candidates ...persistence.SourceScanCandidateInput) {
	t.Helper()
	if err := inventory.ReplaceSourceScanCandidates(ctx, operationID, candidates); err != nil {
		t.Fatalf("store the candidates of operation %s: %v", operationID, err)
	}
}

func countSourceScanRetryJobs(t *testing.T, ctx context.Context, database *bun.DB, kind string) int {
	t.Helper()
	return countScanEnqueueRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = ?", kind)
}

func retrySourceScan(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, id uuid.UUID, client persistence.RiverInserter) (*persistence.Operation, error) {
	t.Helper()
	return repository.RetrySourceScanOperationAndEnqueue(ctx, id, client, service.ScanSourceJobArgs{OperationID: id}, nil)
}

// holdSourceScanRetryRootDeletion deletes a root in a transaction that keeps the
// operation table lock until the test releases it, which is the lock a retry
// takes before it reads the operation and the root.
func holdSourceScanRetryRootDeletion(ctx context.Context, database *bun.DB, rootID uuid.UUID, locked chan struct{}, release chan struct{}) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		_ = transaction.Rollback()
		return err
	}
	result, err := transaction.NewDelete().Model((*persistence.SourceRoot)(nil)).Where("id = ?", rootID).Exec(ctx)
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		_ = transaction.Rollback()
		return errors.New("the root to delete does not exist")
	}
	close(locked)
	<-release
	return transaction.Commit()
}

// TestSourceScanRetryReenqueuesANewTraversalWithPostgreSQL drives a retry of a
// failed scan against real PostgreSQL and a real River job table: the retry
// keeps the operation and its snapshot, drops the candidates of the failed
// attempt before it enqueues the next traversal under the scan kind, and leaves
// the previous successful inventory exactly as it was.
func TestSourceScanRetryReenqueuesANewTraversalWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	repository := persistence.NewSetupManagerRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/scan-retry")

	applied := newSourceScanOperation(t, ctx, database, root, "running")
	applySourceScan(t, ctx, inventory, applied, root.ConfiguredPath,
		sourceCandidate("album/keep.flac", 1024, probeMtime()))
	setOperationState(t, ctx, database, applied.ID, "succeeded")
	baseline := snapshotInventory(t, ctx, database, root.ID)
	keepID := locationID(t, ctx, database, root.ID, "album/keep.flac")

	failed := failedSourceScanRetryOperation(t, ctx, repository, inventory, root, "traversing", "The source directory could not be read completely.")
	storeSourceScanRetryCandidates(t, ctx, inventory, failed.ID,
		sourceCandidate("album/stale.flac", 4096, probeMtime()),
		sourceCandidate("album/keep.flac", 1024, probeMtime()))
	assertCandidateCount(t, ctx, database, failed.ID, 2)

	inserter := &sourceScanRetryInserter{client: client, operationID: failed.ID}
	retried, err := retrySourceScan(t, ctx, repository, failed.ID, inserter)
	if err != nil {
		t.Fatalf("retry the failed scan: %v", err)
	}
	if inserter.candidates != 0 {
		t.Fatalf("candidates the retry transaction held when it enqueued = %d, want the failed attempt's candidates dropped first", inserter.candidates)
	}
	if retried.ID != failed.ID || retried.State != "queued" || retried.Stage != "retry:traversing" || retried.Attempt != 2 {
		t.Fatalf("retried operation = %+v, want the same operation queued as attempt 2", retried)
	}
	if retried.SafeError != nil || retried.StartedAt != nil || retried.FinishedAt != nil || retried.BytesCompleted != 0 {
		t.Fatalf("retried operation = %+v, want the failure state cleared", retried)
	}
	if retried.TargetSourceRootID == nil || *retried.TargetSourceRootID != root.ID || retried.RiverJobID == nil {
		t.Fatalf("retried operation = %+v, want the root %s and a new River job", retried, root.ID)
	}
	job := readSourceScanRetryJob(t, ctx, database, *retried.RiverJobID)
	if job.Kind != service.SourceScanJobKind {
		t.Fatalf("retry River job kind = %q, want %q", job.Kind, service.SourceScanJobKind)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(job.Args), &args); err != nil {
		t.Fatalf("decode the retry River args %s: %v", job.Args, err)
	}
	if !reflect.DeepEqual(args, map[string]any{"operation_id": failed.ID.String()}) {
		t.Fatalf("retry River args = %v, want the operation id alone", args)
	}
	if generic := countSourceScanRetryJobs(t, ctx, database, "operation_v1"); generic != 0 {
		t.Fatalf("River jobs under the generic operation kind = %d, want the retry to carry the scan kind", generic)
	}
	assertCandidateCount(t, ctx, database, failed.ID, 0)

	stored, err := repository.GetOperation(ctx, failed.ID)
	if err != nil {
		t.Fatalf("read the retried operation: %v", err)
	}
	if stored.Attempt != 2 {
		t.Fatalf("stored retried operation = %+v, want attempt 2", stored)
	}
	var snapshot service.ScanSourceSnapshot
	if err := json.Unmarshal(stored.InputSnapshot, &snapshot); err != nil {
		t.Fatalf("decode the stored snapshot %s: %v", stored.InputSnapshot, err)
	}
	wantSnapshot := service.ScanSourceSnapshot{
		SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: root.ID,
	}
	if !reflect.DeepEqual(snapshot, wantSnapshot) {
		t.Fatalf("stored snapshot after the retry = %+v, want %+v preserved", snapshot, wantSnapshot)
	}
	after := snapshotInventory(t, ctx, database, root.ID)
	if after != baseline {
		t.Fatalf("inventory after the retry = %q, want the previous successful inventory %q", after, baseline)
	}
	if locationID(t, ctx, database, root.ID, "album/keep.flac") != keepID {
		t.Fatal("the retry rewrote the identity of a location of the previous inventory")
	}
	// The retry made the root active again, so an edit or deletion racing it is
	// refused exactly like one racing a scan start.
	if err := deleteInventoryRoot(ctx, inventory, root.ID); !errors.Is(err, persistence.ErrSourceRootActiveScan) {
		t.Fatalf("deleting the root of a retried scan = %v, want ErrSourceRootActiveScan", err)
	}
}

// TestSourceScanRetryIsAtomicWithPostgreSQL fails the River insert of a retry:
// the cleanup and the enqueue are one transaction, so the failed operation keeps
// its state and its candidates and no job is left behind.
func TestSourceScanRetryIsAtomicWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	repository := persistence.NewSetupManagerRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/scan-retry-rollback")

	failed := failedSourceScanRetryOperation(t, ctx, repository, inventory, root, "applying", "The verified scan could not be applied.")
	storeSourceScanRetryCandidates(t, ctx, inventory, failed.ID,
		sourceCandidate("album/partial.flac", 512, probeMtime()))

	unreachable := errors.New("River is unreachable")
	_, err := retrySourceScan(t, ctx, repository, failed.ID,
		&sourceScanRetryInserter{client: client, operationID: failed.ID, err: unreachable})
	if !errors.Is(err, unreachable) {
		t.Fatalf("retry with a failing River insert = %v, want the insert failure", err)
	}

	stored, err := repository.GetOperation(ctx, failed.ID)
	if err != nil {
		t.Fatalf("read the operation after the rolled-back retry: %v", err)
	}
	if stored.State != "failed" || stored.Attempt != 1 || stored.RiverJobID != nil || stored.SafeError == nil || stored.FinishedAt == nil {
		t.Fatalf("operation after the rolled-back retry = %+v, want it unchanged and failed", stored)
	}
	assertCandidateCount(t, ctx, database, failed.ID, 1)
	if jobs := countSourceScanRetryJobs(t, ctx, database, service.SourceScanJobKind); jobs != 0 {
		t.Fatalf("scan River jobs after the rolled-back retry = %d, want 0", jobs)
	}
}

// TestSourceScanRetryRefusalsWithPostgreSQL pins every refusal the retry decides
// under the operation table lock and the root row lock: an active scan of the
// same root, a disabled root, a deleted root, an operation that is not failed
// and an operation that is not a scan. None of them may insert a job, change the
// operation or drop the candidates of the failed attempt.
func TestSourceScanRetryRefusalsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	repository := persistence.NewSetupManagerRepository(database)
	client := openScanEnqueueRiver(t, database)

	busy := createInventoryRoot(t, ctx, inventory, "/srv/scan-retry-busy")
	busyFailed := failedSourceScanRetryOperation(t, ctx, repository, inventory, busy, "traversing", "The traversal failed.")
	storeSourceScanRetryCandidates(t, ctx, inventory, busyFailed.ID, sourceCandidate("album/busy.flac", 1, probeMtime()))
	active := newSourceScanOperation(t, ctx, database, busy, "queued")
	if _, err := retrySourceScan(t, ctx, repository, busyFailed.ID, client); !errors.Is(err, persistence.ErrSourceRootActiveScan) {
		t.Fatalf("retry while another scan of the root is active = %v, want ErrSourceRootActiveScan", err)
	}
	assertCandidateCount(t, ctx, database, busyFailed.ID, 1)
	setOperationState(t, ctx, database, active.ID, "succeeded")

	disabled := createInventoryRoot(t, ctx, inventory, "/srv/scan-retry-disabled")
	disabledFailed := failedSourceScanRetryOperation(t, ctx, repository, inventory, disabled, "queued", "The root was disabled.")
	if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
		Set("enabled = false").Where("id = ?", disabled.ID).Exec(ctx); err != nil {
		t.Fatalf("disable the source root: %v", err)
	}
	if _, err := retrySourceScan(t, ctx, repository, disabledFailed.ID, client); !errors.Is(err, persistence.ErrSourceRootDisabled) {
		t.Fatalf("retry on a disabled root = %v, want ErrSourceRootDisabled", err)
	}

	doomed := createInventoryRoot(t, ctx, inventory, "/srv/scan-retry-doomed")
	doomedFailed := failedSourceScanRetryOperation(t, ctx, repository, inventory, doomed, "traversing", "The traversal failed.")
	if err := deleteInventoryRoot(ctx, inventory, doomed.ID); err != nil {
		t.Fatalf("delete the root of the failed scan: %v", err)
	}
	if _, err := retrySourceScan(t, ctx, repository, doomedFailed.ID, client); err == nil {
		t.Fatal("retry of a scan whose root was deleted was accepted")
	}

	finished := createInventoryRoot(t, ctx, inventory, "/srv/scan-retry-finished")
	succeeded := newSourceScanOperation(t, ctx, database, finished, "queued")
	setOperationState(t, ctx, database, succeeded.ID, "succeeded")
	// The fixture stores its own River job, so the refusal must leave that stored
	// state alone instead of assuming a nil job: capture the operation and the
	// job count first and compare them after the refused retry.
	before, err := repository.GetOperation(ctx, succeeded.ID)
	if err != nil {
		t.Fatalf("read the succeeded scan before the refused retry: %v", err)
	}
	jobsBefore := countSourceScanRetryJobs(t, ctx, database, service.SourceScanJobKind)
	if _, err := retrySourceScan(t, ctx, repository, succeeded.ID, client); err == nil {
		t.Fatal("retry of a succeeded scan was accepted")
	}
	stored, err := repository.GetOperation(ctx, succeeded.ID)
	if err != nil || stored.RiverJobID == nil || !reflect.DeepEqual(before, stored) {
		t.Fatalf("succeeded scan after the refused retry = %+v, %v; want %+v untouched", stored, err, before)
	}

	finishedAt := time.Now().UTC()
	install := &persistence.Operation{
		ID: uuid.New(), Kind: "install", State: "failed", Stage: "verify",
		InputSnapshot: []byte(`{"target_identity":"ffmpeg:scan-retry-fixture"}`),
		SafeError:     stringPointer("The installation failed."), FinishedAt: &finishedAt,
	}
	if err := repository.CreateOperation(ctx, install); err != nil {
		t.Fatalf("create a failed install operation: %v", err)
	}
	if _, err := retrySourceScan(t, ctx, repository, install.ID, client); err == nil {
		t.Fatal("a failed install was accepted by the scan retry")
	}

	if jobs := countSourceScanRetryJobs(t, ctx, database, service.SourceScanJobKind); jobs != jobsBefore {
		t.Fatalf("scan River jobs after the refusals = %d, want the %d before them preserved", jobs, jobsBefore)
	}
	if jobs := countSourceScanRetryJobs(t, ctx, database, "operation_v1"); jobs != 0 {
		t.Fatalf("River jobs under the generic operation kind after the refusals = %d, want 0", jobs)
	}
}

// TestSourceScanRetrySerializesWithRootDeletionWithPostgreSQL holds the
// operation table lock while a root is deleted, which is the lock a retry takes
// before it reads anything: the retry cannot pass the deletion and must refuse
// the scan instead of resurrecting a job for a root that is gone.
func TestSourceScanRetrySerializesWithRootDeletionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	inventory := persistence.NewSourceInventoryRepository(database)
	repository := persistence.NewSetupManagerRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, inventory, "/srv/scan-retry-deletion")

	failed := failedSourceScanRetryOperation(t, ctx, repository, inventory, root, "traversing", "The traversal failed.")
	storeSourceScanRetryCandidates(t, ctx, inventory, failed.ID, sourceCandidate("album/stale.flac", 2048, probeMtime()))

	locked, release := make(chan struct{}), make(chan struct{})
	deletion := make(chan error, 1)
	go func() {
		deletion <- holdSourceScanRetryRootDeletion(ctx, database, root.ID, locked, release)
	}()
	<-locked

	retryResult := make(chan error, 1)
	go func() {
		_, err := retrySourceScan(t, ctx, repository, failed.ID, client)
		retryResult <- err
	}()
	close(release)

	if err := <-deletion; err != nil {
		t.Fatalf("the deletion side = %v, want it to delete the root", err)
	}
	if err := <-retryResult; err == nil {
		t.Fatal("a scan retry against a deleted root was accepted")
	}
	if jobs := countSourceScanRetryJobs(t, ctx, database, service.SourceScanJobKind); jobs != 0 {
		t.Fatalf("scan River jobs after the refused retry = %d, want 0", jobs)
	}
	assertCandidateCount(t, ctx, database, failed.ID, 1)
	var target *uuid.UUID
	if err := database.NewRaw("SELECT target_source_root_id FROM operation WHERE id = ?", failed.ID).Scan(ctx, &target); err != nil {
		t.Fatalf("read the operation target after the deletion: %v", err)
	}
	if target != nil {
		t.Fatalf("operation target after the root deletion = %v, want NULL", target)
	}
}
