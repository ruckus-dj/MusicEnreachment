//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// This deliberately uses a real migrated PostgreSQL database: cleanup admission
// must commit the operation, its snapshot, and its one-shot River delivery as a
// single transaction while holding the output admission gate.
type cleanupAcceptanceInserter struct {
	called  int
	args    river.JobArgs
	options *river.InsertOpts
	jobID   int64
	err     error
}

func (inserter *cleanupAcceptanceInserter) InsertTx(_ context.Context, _ *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	inserter.called++
	inserter.args, inserter.options = args, options
	if inserter.err != nil {
		return nil, inserter.err
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: inserter.jobID}}, nil
}

type cleanupAcceptanceArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

var cleanupAcceptanceJobID atomic.Int64

func (cleanupAcceptanceArgs) Kind() string { return SourceAnalysisArtifactCleanupJobKind }

func TestCleanupAcceptanceAdmissionAndAtomicRollback(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repo := NewSetupManagerRepository(db)
	artifactID := insertCleanupAcceptanceArtifact(t, ctx, db, SourceAnalysisArtifactCleanupEligible)
	client := &cleanupAcceptanceInserter{jobID: 981234}
	var factoryID uuid.UUID
	op, err := repo.AdmitSourceAnalysisArtifactCleanupWithArgsFactory(ctx, []uuid.UUID{artifactID}, client, func(id uuid.UUID) river.JobArgs {
		factoryID = id
		return cleanupAcceptanceArgs{OperationID: id}
	}, &river.InsertOpts{MaxAttempts: 7})
	if err != nil {
		t.Fatalf("admit cleanup: %v", err)
	}
	if factoryID == uuid.Nil || op.ID != factoryID || op.Attempt != 1 || op.RiverJobID == nil || *op.RiverJobID != client.jobID {
		t.Fatalf("operation/factory identity mismatch: op=%+v factory=%s", op, factoryID)
	}
	if client.called != 1 || client.options == nil || client.options.MaxAttempts != 1 {
		t.Fatalf("River insert calls/options = %d/%+v, want once and MaxAttempts=1", client.called, client.options)
	}
	jobArgs, ok := client.args.(cleanupAcceptanceArgs)
	if !ok || jobArgs.OperationID != op.ID {
		t.Fatalf("job args = %#v, want operation ID %s", client.args, op.ID)
	}
	var persistedSnapshot []byte
	if err := db.NewRaw(`SELECT input_snapshot FROM operation WHERE id = ?`, op.ID).Scan(ctx, &persistedSnapshot); err != nil {
		t.Fatalf("read operation snapshot: %v", err)
	}
	var snapshot SourceAnalysisArtifactCleanupSnapshot
	if err := json.Unmarshal(persistedSnapshot, &snapshot); err != nil || len(snapshot.ArtifactIDs) != 1 || snapshot.ArtifactIDs[0] != artifactID {
		t.Fatalf("snapshot = %s, %v", persistedSnapshot, err)
	}

	rollbackID := insertCleanupAcceptanceArtifact(t, ctx, db, SourceAnalysisArtifactCleanupEligible)
	failedClient := &cleanupAcceptanceInserter{jobID: 981235, err: errors.New("insert deliberately failed")}
	if _, err := repo.AdmitSourceAnalysisArtifactCleanupWithArgsFactory(ctx, []uuid.UUID{rollbackID}, failedClient, func(id uuid.UUID) river.JobArgs { return cleanupAcceptanceArgs{OperationID: id} }, nil); err == nil {
		t.Fatal("admission succeeded despite River insert failure")
	}
	var operations, jobs int
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE kind = ? AND input_snapshot @> jsonb_build_object('artifact_ids', jsonb_build_array(?::text))`, SourceAnalysisArtifactCleanupOperationKind, rollbackID).Scan(ctx, &operations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE id = ?`, failedClient.jobID).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if operations != 0 || jobs != 0 {
		t.Fatalf("failed admission left operation/job rows: %d/%d", operations, jobs)
	}
}

func initializeRiver(t *testing.T, ctx context.Context, db *bun.DB) {
	t.Helper()
	driver := riverdatabasesql.New(db.DB)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
}

func insertCleanupAcceptanceArtifact(t *testing.T, ctx context.Context, db *bun.DB, state string) uuid.UUID {
	t.Helper()
	initializeRiver(t, ctx, db)
	rootID, locationID, workID, creatorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	jobID := cleanupAcceptanceJobID.Add(1) + 700000
	rootPath := "/cleanup-acceptance/" + uuid.NewString()
	artifactID := uuid.New()
	relativeOutputPath := "analysis/staging/" + rootID.String() + "/" + workID.String() + "/" + artifactID.String()
	for _, queryArgs := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO source_root(id,configured_path,display_name) VALUES(?,?,?)`, []any{rootID, rootPath, "cleanup acceptance"}},
		{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status) VALUES(?,?,'music/song.flac',5,now(),1,'audio')`, []any{locationID, rootID}},
		{`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id) VALUES(?,?,?,?,?,'music/song.flac',5,now(),true,?)`, []any{workID, locationID, rootID, rootPath, rootPath, uuid.New()}},
		{`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id,created_at,updated_at,finished_at) VALUES(?,'analyze_source','succeeded','complete','{}',1,?,now(),now(),now())`, []any{creatorID, jobID}},
		{`INSERT INTO source_analysis_work_execution(work_id,operation_id,operation_attempt,job_id,processing_mode) VALUES(?,?,1,?,'staged')`, []any{workID, creatorID, jobID}},
	} {
		if _, err := db.ExecContext(ctx, queryArgs.query, queryArgs.args...); err != nil {
			t.Fatalf("insert cleanup fixture: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_artifact(id,work_id,relative_output_path,source_size_bytes,source_mtime,owner_operation_id,owner_operation_attempt,owner_job_id,state) VALUES(?,?,?,5,now(),?,1,?,?)`, artifactID, workID, relativeOutputPath, creatorID, jobID, state); err != nil {
		t.Fatalf("insert artifact: %v", err)
	}
	return artifactID
}
