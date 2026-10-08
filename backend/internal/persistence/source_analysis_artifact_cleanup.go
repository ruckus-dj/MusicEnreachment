package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

const SourceAnalysisArtifactCleanupOperationKind = "cleanup_source_analysis_artifacts"
const SourceAnalysisArtifactCleanupJobKind = "cleanup_source_analysis_artifacts_v1"

const cleanupDeliveryLockNamespace int32 = 0x434c4e31 // "CLN1", independent advisory-lock namespace.
const cleanupDeliveryLockPartition int32 = 1

// SourceAnalysisArtifactCleanupSnapshot intentionally contains only strict
// artifact identifiers. The canonical relative output path is always reloaded
// from the ownership row at claim time, never trusted from an operation input.
type SourceAnalysisArtifactCleanupSnapshot struct {
	ArtifactIDs []uuid.UUID `json:"artifact_ids"`
}

type SourceAnalysisArtifactCleanupFence struct {
	OperationID      uuid.UUID
	OperationAttempt int
	JobID            int64
}

type SourceAnalysisArtifactCleanupItem struct {
	ArtifactID       uuid.UUID
	OperationID      uuid.UUID
	OperationAttempt int
	JobID            int64
	RelativePath     string
	State            string
	SafeError        *string
}

type SourceAnalysisArtifactCleanupOutcome struct {
	Succeeded bool
	SafeError string
}

type SourceAnalysisArtifactCleanupExecutor func(context.Context, SourceAnalysisArtifact) (SourceAnalysisArtifactCleanupOutcome, error)

// OutputAdmissionSession pins the shared output gate to one PostgreSQL
// connection. Callers can run short transactions on that same connection while
// holding the gate across filesystem work performed outside those transactions.
type OutputAdmissionSession struct {
	conn *sql.Conn
}

func cleanupDeliveryLockKey(operationID uuid.UUID) int32 {
	hash := fnv.New32a()
	_, _ = hash.Write(operationID[:])
	return int32(hash.Sum32())
}

func (session *OutputAdmissionSession) lockCleanupDelivery(ctx context.Context, operationID uuid.UUID) error {
	_, err := session.conn.ExecContext(ctx, "SELECT pg_advisory_lock($1, $2)", cleanupDeliveryLockNamespace,
		cleanupDeliveryLockPartition^cleanupDeliveryLockKey(operationID))
	if err != nil {
		return fmt.Errorf("acquire cleanup delivery session lock: %w", err)
	}
	return nil
}

func (session *OutputAdmissionSession) unlockCleanupDelivery(operationID uuid.UUID) error {
	var unlocked bool
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := session.conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1, $2)", cleanupDeliveryLockNamespace,
		cleanupDeliveryLockPartition^cleanupDeliveryLockKey(operationID)).Scan(&unlocked)
	if err != nil || !unlocked {
		return fmt.Errorf("release cleanup delivery session lock: %w", errors.Join(err, errors.New("session lock was not held")))
	}
	return nil
}

func (repository *SetupManagerRepository) withCleanupDeliverySession(ctx context.Context, operationID uuid.UUID, callback func(*OutputAdmissionSession) error) error {
	return repository.WithOutputAdmissionSession(ctx, func(session *OutputAdmissionSession) (resultErr error) {
		if err := session.lockCleanupDelivery(ctx, operationID); err != nil {
			return err
		}
		defer func() {
			if err := session.unlockCleanupDelivery(operationID); err != nil {
				_ = session.conn.Raw(func(any) error { return driver.ErrBadConn })
				resultErr = errors.Join(resultErr, err)
			}
		}()
		return callback(session)
	})
}

func (session *OutputAdmissionSession) RunInTx(ctx context.Context, callback func(*sql.Tx) error) error {
	tx, err := session.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin output admission session transaction: %w", err)
	}
	if err := callback(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit output admission session transaction: %w", err)
	}
	return nil
}

// WithOutputAdmissionSession holds the same shared coordination gate used by
// output producers, but as a session lock so it spans all callback transactions
// and filesystem work. A connection with an uncertain unlock result is evicted.
func (repository *SetupManagerRepository) WithOutputAdmissionSession(ctx context.Context, callback func(*OutputAdmissionSession) error) (resultErr error) {
	if callback == nil {
		return fmt.Errorf("output admission session callback is required")
	}
	conn, err := repository.db.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire output admission session connection: %w", err)
	}
	locked := false
	defer func() {
		if locked {
			var unlocked bool
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			unlockErr := conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock_shared($1, $2)", toolsCoordinationNamespace, outputAdmissionGateKey).Scan(&unlocked)
			cancel()
			if unlockErr != nil || !unlocked {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
				if resultErr == nil {
					resultErr = fmt.Errorf("release output admission session gate: %w", errors.Join(unlockErr, errors.New("session gate was not held")))
				}
			}
		}
		if closeErr := conn.Close(); closeErr != nil && resultErr == nil {
			resultErr = fmt.Errorf("close output admission session connection: %w", closeErr)
		}
	}()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock_shared($1, $2)", toolsCoordinationNamespace, outputAdmissionGateKey); err != nil {
		return fmt.Errorf("acquire output admission session gate: %w", err)
	}
	locked = true
	return callback(&OutputAdmissionSession{conn: conn})
}

// AdmitSourceAnalysisArtifactCleanupWithArgsFactory creates the operation ID
// before constructing its River arguments, keeping both durable records bound
// to the same operation identity.
func (repository *SetupManagerRepository) AdmitSourceAnalysisArtifactCleanupWithArgsFactory(
	ctx context.Context,
	artifactIDs []uuid.UUID,
	client RiverInserter,
	argsFactory func(uuid.UUID) river.JobArgs,
	options *river.InsertOpts,
) (*Operation, error) {
	ids, err := strictArtifactIDs(artifactIDs)
	if err != nil {
		return nil, fmt.Errorf("admit source analysis artifact cleanup: %w", err)
	}
	if client == nil {
		return nil, fmt.Errorf("admit source analysis artifact cleanup: River client is required")
	}
	if argsFactory == nil {
		return nil, fmt.Errorf("admit source analysis artifact cleanup: cleanup River job arguments are required")
	}
	operation := &Operation{ID: uuid.New(), Kind: SourceAnalysisArtifactCleanupOperationKind, State: "queued", Stage: "queued", Attempt: 1}
	snapshot, err := json.Marshal(SourceAnalysisArtifactCleanupSnapshot{ArtifactIDs: ids})
	if err != nil {
		return nil, fmt.Errorf("marshal cleanup snapshot: %w", err)
	}
	operation.InputSnapshot = snapshot
	insertOptions := &river.InsertOpts{MaxAttempts: 1}
	if options != nil {
		copyOptions := *options
		copyOptions.MaxAttempts = 1
		insertOptions = &copyOptions
	}
	err = repository.WithOutputAdmissionSession(ctx, func(session *OutputAdmissionSession) error {
		return session.RunInTx(ctx, func(tx *sql.Tx) error {
			eligible, err := eligibleCleanupArtifactIDs(ctx, tx, ids, true)
			if err != nil {
				return err
			}
			if len(eligible) != len(ids) {
				return fmt.Errorf("selected cleanup artifact set is no longer fully eligible")
			}
			args := argsFactory(operation.ID)
			if args == nil || args.Kind() != SourceAnalysisArtifactCleanupJobKind {
				return fmt.Errorf("admit source analysis artifact cleanup: cleanup River job arguments are required")
			}
			if err := validateCleanupJobArgs(args, operation.ID); err != nil {
				return fmt.Errorf("admit source analysis artifact cleanup: %w", err)
			}
			result, err := client.InsertTx(ctx, tx, args, insertOptions)
			if err != nil {
				return fmt.Errorf("insert cleanup River job: %w", err)
			}
			operation.RiverJobID = &result.Job.ID
			_, err = tx.ExecContext(ctx, `INSERT INTO operation
				(id,kind,state,stage,input_snapshot,attempt,river_job_id)
				VALUES ($1,$2,'queued','queued',$3::jsonb,1,$4)`,
				operation.ID, operation.Kind, string(snapshot), result.Job.ID)
			if err != nil {
				return fmt.Errorf("insert cleanup operation: %w", err)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}

func validateCleanupJobArgs(args river.JobArgs, operationID uuid.UUID) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("marshal cleanup River job arguments: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 1 {
		return fmt.Errorf("cleanup River job arguments must contain only operation_id")
	}
	value, ok := fields["operation_id"]
	if !ok {
		return fmt.Errorf("cleanup River job arguments must contain only operation_id")
	}
	var encoded string
	if err := json.Unmarshal(value, &encoded); err != nil {
		return fmt.Errorf("cleanup River job arguments must contain a valid operation_id")
	}
	id, err := uuid.Parse(encoded)
	if err != nil || id == uuid.Nil || id != operationID {
		return fmt.Errorf("cleanup River job arguments operation_id must match the generated operation")
	}
	return nil
}

// RunSourceAnalysisArtifactCleanupDelivery claims and finalizes every selected
// item on the pinned gate connection. The executor owns filesystem access and
// is called only after the claim transaction commits. A redelivery with an
// existing exact claim is rejected; startup recovery never unlinks an artifact.
func (repository *SetupManagerRepository) RunSourceAnalysisArtifactCleanupDelivery(
	ctx context.Context,
	fence SourceAnalysisArtifactCleanupFence,
	executor SourceAnalysisArtifactCleanupExecutor,
) error {
	if !validSourceAnalysisArtifactCleanupFence(fence) || executor == nil {
		return fmt.Errorf("run source analysis artifact cleanup: valid delivery fence and executor are required")
	}
	return repository.withCleanupDeliverySession(ctx, fence.OperationID, func(session *OutputAdmissionSession) error {
		var ids []uuid.UUID
		if err := session.RunInTx(ctx, func(tx *sql.Tx) error {
			var snapshot []byte
			if err := tx.QueryRowContext(ctx, `SELECT input_snapshot FROM operation
				WHERE id=$1 AND kind=$2 AND state='running' AND attempt=$3 AND river_job_id=$4 FOR UPDATE`,
				fence.OperationID, SourceAnalysisArtifactCleanupOperationKind, fence.OperationAttempt, fence.JobID).Scan(&snapshot); err != nil {
				return fmt.Errorf("read fenced cleanup operation: %w", err)
			}
			var operationSnapshot SourceAnalysisArtifactCleanupSnapshot
			decoder := json.NewDecoder(bytes.NewReader(snapshot))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&operationSnapshot); err != nil {
				return fmt.Errorf("decode cleanup operation snapshot: %w", err)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return fmt.Errorf("cleanup operation snapshot contains trailing JSON")
			}
			var err error
			ids, err = strictArtifactIDs(operationSnapshot.ArtifactIDs)
			if err != nil || len(ids) != len(operationSnapshot.ArtifactIDs) {
				return fmt.Errorf("cleanup operation snapshot contains invalid artifact identifiers")
			}
			return nil
		}); err != nil {
			return err
		}
		for _, artifactID := range ids {
			artifact, err := claimCleanupArtifact(ctx, session, fence, artifactID)
			if err != nil {
				return err
			}
			outcome, executionErr := executor(ctx, *artifact)
			if executionErr != nil {
				outcome = SourceAnalysisArtifactCleanupOutcome{SafeError: "The staged analysis artifact could not be cleaned up."}
			}
			if !outcome.Succeeded {
				outcome.SafeError = strings.TrimSpace(outcome.SafeError)
				if outcome.SafeError == "" {
					outcome.SafeError = "The staged analysis artifact could not be cleaned up."
				}
			}
			if err := finishCleanupArtifact(ctx, session, fence, artifactID, outcome); err != nil {
				return err
			}
		}
		return finishCleanupOperation(ctx, session, fence)
	})
}

func finishCleanupOperation(ctx context.Context, session *OutputAdmissionSession, fence SourceAnalysisArtifactCleanupFence) error {
	return session.RunInTx(ctx, func(tx *sql.Tx) error {
		var total, claimed, failures int
		if err := tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER (WHERE state='claimed'),count(*) FILTER (WHERE state='failed')
			FROM source_analysis_artifact_cleanup_item WHERE operation_id=$1 AND operation_attempt=$2 AND job_id=$3`,
			fence.OperationID, fence.OperationAttempt, fence.JobID).Scan(&total, &claimed, &failures); err != nil {
			return fmt.Errorf("read cleanup delivery outcomes: %w", err)
		}
		var snapshot []byte
		if err := tx.QueryRowContext(ctx, `SELECT input_snapshot FROM operation
			WHERE id=$1 AND kind=$2 AND state='running' AND attempt=$3 AND river_job_id=$4 FOR UPDATE`,
			fence.OperationID, SourceAnalysisArtifactCleanupOperationKind, fence.OperationAttempt, fence.JobID).Scan(&snapshot); err != nil {
			return fmt.Errorf("lock cleanup operation to finish: %w", err)
		}
		var value SourceAnalysisArtifactCleanupSnapshot
		if err := json.Unmarshal(snapshot, &value); err != nil || len(value.ArtifactIDs) != total || claimed != 0 {
			return fmt.Errorf("cleanup delivery does not have a complete durable outcome set")
		}
		state, safe := "succeeded", ""
		if failures > 0 {
			state, safe = "failed", "One or more staged analysis artifacts could not be cleaned up."
		}
		result, err := tx.ExecContext(ctx, `UPDATE operation SET state=$5,stage='finished',safe_error=NULLIF($6,''),finished_at=now(),updated_at=now()
			WHERE id=$1 AND kind=$2 AND state='running' AND attempt=$3 AND river_job_id=$4`,
			fence.OperationID, SourceAnalysisArtifactCleanupOperationKind, fence.OperationAttempt, fence.JobID, state, safe)
		if err != nil {
			return fmt.Errorf("finish cleanup operation: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return fmt.Errorf("finish cleanup operation: delivery fence is stale")
		}
		return nil
	})
}

// ListSourceAnalysisArtifactCleanupCandidates returns a stable, bounded selection
// for an admission attempt. AdmitSourceAnalysisArtifactCleanup rechecks the
// complete selected set under the output gate before committing it.
func (repository *SetupManagerRepository) ListSourceAnalysisArtifactCleanupCandidates(ctx context.Context, limit int) ([]*SourceAnalysisArtifact, error) {
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("list cleanup candidates: limit must be between 1 and 1000")
	}
	var artifacts []*SourceAnalysisArtifact
	err := repository.db.NewRaw(`SELECT artifact.* FROM source_analysis_artifact artifact
		WHERE artifact.state IN ('cleanup_eligible','cleanup_failed')
		AND NOT EXISTS (SELECT 1 FROM source_analysis_work_artifact_binding binding WHERE binding.artifact_id=artifact.id)
		AND NOT EXISTS (
			SELECT 1 FROM operation_source_work_hold hold JOIN operation creator ON creator.id=hold.operation_id
			WHERE hold.work_id=artifact.work_id AND creator.kind='analyze_source' AND creator.state IN ('queued','running')
			AND EXISTS (SELECT 1 FROM river_job job WHERE job.id=creator.river_job_id AND job.state IN ('available','pending','running','retryable','scheduled'))
		)
		AND NOT EXISTS (SELECT 1 FROM source_analysis_artifact_cleanup_item claim WHERE claim.artifact_id=artifact.id AND claim.state='claimed')
		ORDER BY artifact.id LIMIT ?`, limit).Scan(ctx, &artifacts)
	if err != nil {
		return nil, fmt.Errorf("list cleanup candidates: %w", err)
	}
	return artifacts, nil
}

func claimCleanupArtifact(ctx context.Context, session *OutputAdmissionSession, fence SourceAnalysisArtifactCleanupFence, artifactID uuid.UUID) (*SourceAnalysisArtifact, error) {
	artifact := new(SourceAnalysisArtifact)
	err := session.RunInTx(ctx, func(tx *sql.Tx) error {
		var cleanupOperationID uuid.UUID
		if err := tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE id=$1 AND kind=$2 AND state='running'
			AND attempt=$3 AND river_job_id=$4 FOR UPDATE`, fence.OperationID, SourceAnalysisArtifactCleanupOperationKind, fence.OperationAttempt, fence.JobID).Scan(&cleanupOperationID); err != nil {
			return fmt.Errorf("lock cleanup operation before claiming artifact: %w", err)
		}
		if err := verifyCleanupSnapshotMembership(ctx, tx, fence, artifactID); err != nil {
			return err
		}
		var workID, creatorOperationID uuid.UUID
		if err := tx.QueryRowContext(ctx, `SELECT work_id,owner_operation_id FROM source_analysis_artifact
			WHERE id=$1 AND state IN ('cleanup_eligible','cleanup_failed')`, artifactID).Scan(&workID, &creatorOperationID); err != nil {
			return fmt.Errorf("read cleanup artifact identity %s: %w", artifactID, err)
		}
		work := new(SourceAnalysisWork)
		if err := tx.QueryRowContext(ctx, `SELECT id FROM source_analysis_work WHERE id=$1 FOR UPDATE`, workID).Scan(&work.ID); err != nil {
			return fmt.Errorf("lock cleanup artifact work %s: %w", workID, err)
		}
		var lockedCreator uuid.UUID
		if err := tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE id=$1 FOR UPDATE`, creatorOperationID).Scan(&lockedCreator); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("lock cleanup artifact creator operation: %w", err)
		}
		var liveCreator bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM operation creator
			JOIN river_job job ON job.id=creator.river_job_id
			JOIN operation_source_work_hold hold ON hold.operation_id=creator.id AND hold.work_id=$2
			WHERE creator.id=$1 AND creator.kind='analyze_source' AND creator.state IN ('queued','running')
			AND job.state IN ('available','pending','running','retryable','scheduled')
		)`, creatorOperationID, workID).Scan(&liveCreator); err != nil {
			return fmt.Errorf("check cleanup artifact creator liveness: %w", err)
		}
		if liveCreator {
			return fmt.Errorf("claim cleanup artifact: its creator still holds live work")
		}
		var bound bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM source_analysis_work_artifact_binding WHERE artifact_id=$1)", artifactID).Scan(&bound); err != nil {
			return err
		}
		if bound {
			return fmt.Errorf("claim cleanup artifact: artifact still has a binding")
		}
		if err := tx.QueryRowContext(ctx, `SELECT id,work_id,relative_output_path,source_size_bytes,source_mtime,
			owner_operation_id,owner_operation_attempt,owner_job_id,state,cleanup_error,cleanup_at,created_at,updated_at
			FROM source_analysis_artifact WHERE id=$1 AND state IN ('cleanup_eligible','cleanup_failed') FOR UPDATE`, artifactID).Scan(
			&artifact.ID, &artifact.WorkID, &artifact.RelativeOutputPath, &artifact.SourceSizeBytes, &artifact.SourceMtime,
			&artifact.OwnerOperationID, &artifact.OwnerOperationAttempt, &artifact.OwnerJobID, &artifact.State,
			&artifact.CleanupError, &artifact.CleanupAt, &artifact.CreatedAt, &artifact.UpdatedAt); err != nil {
			return fmt.Errorf("claim cleanup artifact %s: %w", artifactID, err)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO source_analysis_artifact_cleanup_item
			(artifact_id,operation_id,operation_attempt,job_id,relative_output_path,state)
			VALUES ($1,$2,$3,$4,$5,'claimed')`, artifactID, fence.OperationID, fence.OperationAttempt, fence.JobID, artifact.RelativeOutputPath)
		if err != nil {
			return fmt.Errorf("record cleanup claim for artifact %s: %w", artifactID, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return artifact, nil
}

// RecoverInterruptedSourceAnalysisArtifactCleanup resolves an orphaned cleanup
// delivery without touching the filesystem. It is only safe after startup has
// established that the exact River job is no longer live; the repository also
// checks that fact under the pinned output gate before changing durable state.
func (repository *SetupManagerRepository) RecoverInterruptedSourceAnalysisArtifactCleanup(
	ctx context.Context,
	operationID uuid.UUID,
	attempt int,
	jobID int64,
) error {
	return repository.recoverInterruptedSourceAnalysisArtifactCleanup(ctx, operationID, attempt, jobID, false)
}

// RecoverInterruptedSourceAnalysisArtifactCleanupAtStartup is for reconciliation
// performed before River workers start. Unlike online recovery, a persisted
// running River job is considered interrupted; the delivery lock still fences a
// worker from changing the same operation while recovery runs.
func (repository *SetupManagerRepository) RecoverInterruptedSourceAnalysisArtifactCleanupAtStartup(
	ctx context.Context,
	operationID uuid.UUID,
	attempt int,
	jobID int64,
) error {
	return repository.recoverInterruptedSourceAnalysisArtifactCleanup(ctx, operationID, attempt, jobID, true)
}

func (repository *SetupManagerRepository) recoverInterruptedSourceAnalysisArtifactCleanup(
	ctx context.Context,
	operationID uuid.UUID,
	attempt int,
	jobID int64,
	allowPersistedRunning bool,
) error {
	fence := SourceAnalysisArtifactCleanupFence{OperationID: operationID, OperationAttempt: attempt, JobID: jobID}
	if !validSourceAnalysisArtifactCleanupFence(fence) {
		return fmt.Errorf("recover source analysis artifact cleanup: exact delivery fence is required")
	}
	return repository.withCleanupDeliverySession(ctx, operationID, func(session *OutputAdmissionSession) error {
		return session.RunInTx(ctx, func(tx *sql.Tx) error {
			var live bool
			liveJobStates := "'available','pending','running','retryable','scheduled'"
			if allowPersistedRunning {
				liveJobStates = "'available','pending','retryable','scheduled'"
			}
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
					SELECT 1 FROM operation operation JOIN river_job job ON job.id=operation.river_job_id
					WHERE operation.id=$1 AND operation.kind=$2 AND operation.state IN ('queued','running')
					AND operation.attempt=$3 AND operation.river_job_id=$4
					AND job.state IN (`+liveJobStates+`)
				)`, operationID, SourceAnalysisArtifactCleanupOperationKind, attempt, jobID).Scan(&live); err != nil {
				return fmt.Errorf("check interrupted cleanup job liveness: %w", err)
			}
			if live {
				return fmt.Errorf("recover source analysis artifact cleanup: River delivery is still live")
			}
			var lockedID uuid.UUID
			if err := tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE id=$1 AND kind=$2 AND state IN ('queued','running')
					AND attempt=$3 AND river_job_id=$4 FOR UPDATE`, operationID, SourceAnalysisArtifactCleanupOperationKind, attempt, jobID).Scan(&lockedID); err != nil {
				return fmt.Errorf("lock interrupted cleanup operation: %w", err)
			}
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
					SELECT 1 FROM operation operation JOIN river_job job ON job.id=operation.river_job_id
					WHERE operation.id=$1 AND operation.kind=$2 AND operation.state IN ('queued','running')
					AND operation.attempt=$3 AND operation.river_job_id=$4 AND job.id=$4
					AND job.state IN (`+liveJobStates+`)
				)`, operationID, SourceAnalysisArtifactCleanupOperationKind, attempt, jobID).Scan(&live); err != nil {
				return fmt.Errorf("recheck interrupted cleanup job liveness: %w", err)
			}
			if live {
				return fmt.Errorf("recover source analysis artifact cleanup: River delivery became live")
			}
			const safeError = "The artifact cleanup was interrupted. Retry the operation."
			rows, err := tx.QueryContext(ctx, `SELECT artifact_id FROM source_analysis_artifact_cleanup_item
				WHERE operation_id=$1 AND operation_attempt=$2 AND job_id=$3 AND state='claimed' ORDER BY artifact_id FOR UPDATE`, operationID, attempt, jobID)
			if err != nil {
				return fmt.Errorf("read interrupted cleanup claims: %w", err)
			}
			var artifactIDs []uuid.UUID
			for rows.Next() {
				var artifactID uuid.UUID
				if err := rows.Scan(&artifactID); err != nil {
					_ = rows.Close()
					return fmt.Errorf("read interrupted cleanup claim: %w", err)
				}
				artifactIDs = append(artifactIDs, artifactID)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return fmt.Errorf("read interrupted cleanup claims: %w", err)
			}
			if err := rows.Close(); err != nil {
				return fmt.Errorf("close interrupted cleanup claims: %w", err)
			}
			for _, artifactID := range artifactIDs {
				updated, err := tx.ExecContext(ctx, `UPDATE source_analysis_artifact SET state='cleanup_failed',
					cleanup_error=$2,cleanup_at=now(),updated_at=now() WHERE id=$1 AND state IN ('cleanup_eligible','cleanup_failed')`, artifactID, safeError)
				if err != nil {
					return fmt.Errorf("mark interrupted artifact cleanup failed: %w", err)
				}
				if err := requireOneRow(updated, "mark interrupted artifact cleanup failed"); err != nil {
					return err
				}
				finalized, err := tx.ExecContext(ctx, `UPDATE source_analysis_artifact_cleanup_item SET state='failed',safe_error=$5,finished_at=now()
					WHERE artifact_id=$1 AND operation_id=$2 AND operation_attempt=$3 AND job_id=$4 AND state='claimed'`, artifactID, operationID, attempt, jobID, safeError)
				if err != nil {
					return fmt.Errorf("finalize interrupted artifact cleanup claim: %w", err)
				}
				if err := requireOneRow(finalized, "finalize interrupted artifact cleanup claim"); err != nil {
					return err
				}
			}
			updated, err := tx.ExecContext(ctx, `UPDATE operation SET state='failed',stage='finished',safe_error=$5,finished_at=now(),updated_at=now()
					WHERE id=$1 AND kind=$2 AND state IN ('queued','running') AND attempt=$3 AND river_job_id=$4`, operationID, SourceAnalysisArtifactCleanupOperationKind, attempt, jobID, safeError)
			if err != nil {
				return fmt.Errorf("fail interrupted artifact cleanup operation: %w", err)
			}
			return requireOneRow(updated, "fail interrupted artifact cleanup operation")
		})
	})
}

func finishCleanupArtifact(ctx context.Context, session *OutputAdmissionSession, fence SourceAnalysisArtifactCleanupFence, artifactID uuid.UUID, outcome SourceAnalysisArtifactCleanupOutcome) error {
	return session.RunInTx(ctx, func(tx *sql.Tx) error {
		var path string
		if err := tx.QueryRowContext(ctx, `SELECT relative_output_path FROM source_analysis_artifact_cleanup_item
			WHERE artifact_id=$1 AND operation_id=$2 AND operation_attempt=$3 AND job_id=$4 AND state='claimed' FOR UPDATE`,
			artifactID, fence.OperationID, fence.OperationAttempt, fence.JobID).Scan(&path); err != nil {
			return fmt.Errorf("read exact cleanup claim: %w", err)
		}
		if outcome.Succeeded {
			deleted, err := tx.ExecContext(ctx, `DELETE FROM source_analysis_artifact
				WHERE id=$1 AND relative_output_path=$2 AND state IN ('cleanup_eligible','cleanup_failed')
				AND NOT EXISTS (SELECT 1 FROM source_analysis_work_artifact_binding WHERE artifact_id=$1)`, artifactID, path)
			if err != nil {
				return fmt.Errorf("delete cleaned artifact ownership row: %w", err)
			}
			if err := requireOneRow(deleted, "delete cleaned artifact ownership row"); err != nil {
				return err
			}
			finalized, err := tx.ExecContext(ctx, `UPDATE source_analysis_artifact_cleanup_item SET state='succeeded',finished_at=now()
				WHERE artifact_id=$1 AND operation_id=$2 AND operation_attempt=$3 AND job_id=$4 AND state='claimed'`, artifactID, fence.OperationID, fence.OperationAttempt, fence.JobID)
			if err != nil {
				return fmt.Errorf("finalize successful cleanup outcome: %w", err)
			}
			if err := requireOneRow(finalized, "finalize successful cleanup outcome"); err != nil {
				return err
			}
		} else {
			updatedArtifact, err := tx.ExecContext(ctx, `UPDATE source_analysis_artifact SET state='cleanup_failed',cleanup_error=$2,cleanup_at=now(),updated_at=now()
				WHERE id=$1 AND relative_output_path=$3 AND state IN ('cleanup_eligible','cleanup_failed')
				AND NOT EXISTS (SELECT 1 FROM source_analysis_work_artifact_binding WHERE artifact_id=$1)`, artifactID, outcome.SafeError, path)
			if err != nil {
				return fmt.Errorf("mark artifact cleanup failed: %w", err)
			}
			if err := requireOneRow(updatedArtifact, "mark artifact cleanup failed"); err != nil {
				return err
			}
			finalized, err := tx.ExecContext(ctx, `UPDATE source_analysis_artifact_cleanup_item SET state='failed',safe_error=$5,finished_at=now()
				WHERE artifact_id=$1 AND operation_id=$2 AND operation_attempt=$3 AND job_id=$4 AND state='claimed'`, artifactID, fence.OperationID, fence.OperationAttempt, fence.JobID, outcome.SafeError)
			if err != nil {
				return fmt.Errorf("finalize failed cleanup outcome: %w", err)
			}
			if err := requireOneRow(finalized, "finalize failed cleanup outcome"); err != nil {
				return err
			}
		}
		return nil
	})
}

func requireOneRow(result sql.Result, action string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: read affected row count: %w", action, err)
	}
	if changed != 1 {
		return fmt.Errorf("%s: ownership fence no longer matches", action)
	}
	return nil
}

func verifyCleanupSnapshotMembership(ctx context.Context, tx *sql.Tx, fence SourceAnalysisArtifactCleanupFence, artifactID uuid.UUID) error {
	var present bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM operation operation
		CROSS JOIN LATERAL jsonb_array_elements_text(operation.input_snapshot->'artifact_ids') selected(id)
		WHERE operation.id=$1 AND operation.kind=$2 AND operation.state='running'
		AND operation.attempt=$3 AND operation.river_job_id=$4 AND selected.id=$5::text
	)`, fence.OperationID, SourceAnalysisArtifactCleanupOperationKind, fence.OperationAttempt, fence.JobID, artifactID).Scan(&present)
	if err != nil {
		return fmt.Errorf("verify cleanup artifact snapshot membership: %w", err)
	}
	if !present {
		return fmt.Errorf("artifact is outside the exact cleanup delivery snapshot")
	}
	return nil
}

func (repository *SetupManagerRepository) ListSourceAnalysisArtifactCleanupItems(ctx context.Context, operationID uuid.UUID) ([]SourceAnalysisArtifactCleanupItem, error) {
	if operationID == uuid.Nil {
		return nil, fmt.Errorf("list cleanup outcomes: operation is required")
	}
	items := make([]SourceAnalysisArtifactCleanupItem, 0)
	err := repository.db.NewRaw(`SELECT artifact_id,operation_id,operation_attempt,job_id,relative_output_path,state,safe_error
		FROM source_analysis_artifact_cleanup_item WHERE operation_id=? ORDER BY artifact_id`, operationID).Scan(ctx, &items)
	if err != nil {
		return nil, fmt.Errorf("list cleanup outcomes: %w", err)
	}
	return items, nil
}

func eligibleCleanupArtifactIDs(ctx context.Context, tx *sql.Tx, ids []uuid.UUID, lock bool) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one artifact identifier is required")
	}
	query := `SELECT artifact.id FROM source_analysis_artifact artifact
		WHERE artifact.id = ANY($1::uuid[])
		AND artifact.state IN ('cleanup_eligible','cleanup_failed')
		AND NOT EXISTS (SELECT 1 FROM source_analysis_work_artifact_binding binding WHERE binding.artifact_id=artifact.id)
		AND NOT EXISTS (
			SELECT 1 FROM operation_source_work_hold hold
			JOIN operation creator ON creator.id=hold.operation_id
			WHERE hold.work_id=artifact.work_id AND creator.state IN ('queued','running')
			AND creator.kind='analyze_source'
			AND EXISTS (SELECT 1 FROM river_job job WHERE job.id=creator.river_job_id AND job.state IN ('available','pending','running','retryable','scheduled'))
		)
		AND NOT EXISTS (SELECT 1 FROM source_analysis_artifact_cleanup_item claim WHERE claim.artifact_id=artifact.id AND claim.state='claimed')`
	if lock {
		query += " FOR UPDATE OF artifact"
	}
	rows, err := tx.QueryContext(ctx, query, uuidArray(ids))
	if err != nil {
		return nil, fmt.Errorf("select eligible cleanup artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	selected := make([]uuid.UUID, 0, len(ids))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		selected = append(selected, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].String() < selected[j].String() })
	return selected, nil
}

func strictArtifactIDs(ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("artifact identifiers are required")
	}
	result := append([]uuid.UUID(nil), ids...)
	for _, id := range result {
		if id == uuid.Nil {
			return nil, fmt.Errorf("artifact identifier must be a non-zero UUID")
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, fmt.Errorf("duplicate artifact identifier %s", result[index])
		}
	}
	return result, nil
}

func validSourceAnalysisArtifactCleanupFence(fence SourceAnalysisArtifactCleanupFence) bool {
	return fence.OperationID != uuid.Nil && fence.OperationAttempt > 0 && fence.JobID > 0
}

func uuidArray(ids []uuid.UUID) string {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = id.String()
	}
	return "{" + strings.Join(values, ",") + "}"
}
