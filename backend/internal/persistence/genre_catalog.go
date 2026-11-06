package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

type GenreCatalogRefreshArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (GenreCatalogRefreshArgs) Kind() string { return "genre_catalog_refresh_v1" }

type GenreCatalogJobEnqueuer interface {
	InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

type GenreCatalogState struct {
	Items                 json.RawMessage
	UpdatedAt             *time.Time
	LastAttemptAt         *time.Time
	LastError             *string
	ConfigurationIdentity string
	RefreshRequest        GenreCatalogRefreshRequest
	RefreshDueAt          *time.Time
}

// GenreCatalogRefreshRequest records the durable singleton request and its
// current delivery fence. JobID remains set after completion for idempotence.
type GenreCatalogRefreshRequest struct {
	JobID       int64     `json:"job_id,omitempty"`
	OperationID uuid.UUID `json:"operation_id,omitempty"`
	Token       string    `json:"token,omitempty"`
	Status      string    `json:"status"`
}

type GenreCatalogRepository struct {
	db         *bun.DB
	operations *SetupManagerRepository
}

func NewGenreCatalogRepository(db *bun.DB, operations ...*SetupManagerRepository) *GenreCatalogRepository {
	repository := &GenreCatalogRepository{db: db}
	if len(operations) != 0 {
		repository.operations = operations[0]
	}
	return repository
}

func (repository *GenreCatalogRepository) State(ctx context.Context) (GenreCatalogState, error) {
	var state GenreCatalogState
	var items string
	var request string
	err := repository.db.NewRaw(`SELECT items::text, updated_at, last_attempt_at, last_error, configuration_identity,
		CASE WHEN refresh_request->>'status' IN ('queued','running') AND NOT EXISTS (
			SELECT 1 FROM operation o JOIN river_job j ON j.id=o.river_job_id
			WHERE o.id=(refresh_request->>'operation_id')::uuid AND o.kind='refresh_genre_catalog_v1'
				AND o.state IN ('queued','running') AND o.attempt=1
				AND o.river_job_id=(refresh_request->>'job_id')::bigint
				AND j.state IN ('available','pending','running','retryable','scheduled')
		) THEN jsonb_set(refresh_request, '{status}', '"finished"'::jsonb)::text ELSE refresh_request::text END,
		refresh_due_at FROM genre_catalog WHERE singleton=true`).Scan(ctx,
		&items, &state.UpdatedAt, &state.LastAttemptAt, &state.LastError, &state.ConfigurationIdentity, &request, &state.RefreshDueAt)
	if err != nil {
		return state, fmt.Errorf("read genre catalog: %w", err)
	}
	state.Items = json.RawMessage(items)
	if err := json.Unmarshal([]byte(request), &state.RefreshRequest); err != nil {
		return state, fmt.Errorf("decode genre catalog refresh request: %w", err)
	}
	return state, nil
}

// GenreCatalogItems exposes the validated catalog entries used to canonicalize
// manually selected metadata genres.
func (repository *GenreCatalogRepository) GenreCatalogItems(ctx context.Context) ([]librarymetadata.GenreReference, error) {
	return readGenreCatalogItems(ctx, repository.db)
}

func readGenreCatalogItems(ctx context.Context, db bun.IDB) ([]librarymetadata.GenreReference, error) {
	var encoded string
	if err := db.NewRaw(`SELECT items::text FROM genre_catalog WHERE singleton=true`).Scan(ctx, &encoded); err != nil {
		return nil, fmt.Errorf("read metadata genre catalog: %w", err)
	}
	var items []librarymetadata.GenreReference
	if encoded == "" || encoded == "null" {
		return []librarymetadata.GenreReference{}, nil
	}
	if err := json.Unmarshal([]byte(encoded), &items); err != nil {
		return nil, fmt.Errorf("decode persisted genre catalog: %w", err)
	}
	return items, nil
}

// Replace publishes a complete catalog in one statement. Failed and stale
// refreshes never replace the last known good items.
func (repository *GenreCatalogRepository) Replace(ctx context.Context, identity string, items json.RawMessage, at time.Time) (bool, error) {
	result, err := repository.db.NewRaw(`UPDATE genre_catalog SET items=?::jsonb, updated_at=?, last_error=NULL WHERE singleton=true AND configuration_identity=?`, string(items), at, identity).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("replace genre catalog: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (repository *GenreCatalogRepository) EnqueueRefresh(ctx context.Context, client GenreCatalogJobEnqueuer) error {
	_, err := repository.AdmitRefresh(ctx, client, time.Now().UTC(), nil)
	return err
}

// AdmitRefresh coalesces callers onto the active durable request. The request
// row and River insertion share a transaction, so neither can exist alone.
func (repository *GenreCatalogRepository) AdmitRefresh(ctx context.Context, client GenreCatalogJobEnqueuer, now time.Time, dueAt *time.Time) (int64, error) {
	if client == nil {
		return 0, fmt.Errorf("enqueue genre catalog refresh: River is unavailable")
	}
	var jobID int64
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("lock output admission gate: %w", err)
		}
		var raw string
		if err := tx.NewRaw(`SELECT refresh_request::text FROM genre_catalog WHERE singleton=true FOR UPDATE`).Scan(ctx, &raw); err != nil {
			return fmt.Errorf("read genre catalog refresh request: %w", err)
		}
		var request GenreCatalogRefreshRequest
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			return fmt.Errorf("decode genre catalog refresh request: %w", err)
		}
		active, err := genreCatalogJobActive(ctx, tx, request)
		if err != nil {
			return fmt.Errorf("reconcile genre catalog refresh request: %w", err)
		}
		if active {
			jobID = request.JobID
			return nil
		}
		if _, err := repository.retireMissingRefreshOperation(ctx, tx, request); err != nil {
			return err
		}
		jobID, _, request, err = repository.createRefreshOperation(ctx, tx, client, dueAt)
		return err
	})
	if err != nil {
		return 0, err
	}
	return jobID, nil
}

// DispatchDueRefresh admits a weekly request only when its persistent deadline
// has elapsed. It does not move the deadline when a manual request is coalesced.
func (repository *GenreCatalogRepository) DispatchDueRefresh(ctx context.Context, client GenreCatalogJobEnqueuer, now time.Time, nextDue time.Time) (int64, bool, error) {
	jobID, dispatched, _, err := repository.DispatchDueRefreshWithRetired(ctx, client, now, nextDue)
	return jobID, dispatched, err
}

// DispatchDueRefreshWithRetired reports the operation retired while recovering
// a lost delivery. The caller must publish notifications only after this method
// returns successfully, which means the transaction has committed.
func (repository *GenreCatalogRepository) DispatchDueRefreshWithRetired(ctx context.Context, client GenreCatalogJobEnqueuer, now time.Time, nextDue time.Time) (int64, bool, uuid.UUID, error) {
	var jobID int64
	var retiredID uuid.UUID
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("lock output admission gate: %w", err)
		}
		var dueAt *time.Time
		if err := tx.NewRaw(`SELECT refresh_due_at FROM genre_catalog WHERE singleton=true FOR UPDATE`).Scan(ctx, &dueAt); err != nil {
			return fmt.Errorf("read genre catalog refresh deadline: %w", err)
		}
		var raw string
		if err := tx.NewRaw(`SELECT refresh_request::text FROM genre_catalog WHERE singleton=true`).Scan(ctx, &raw); err != nil {
			return fmt.Errorf("read genre catalog refresh request: %w", err)
		}
		var request GenreCatalogRefreshRequest
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			return fmt.Errorf("decode genre catalog refresh request: %w", err)
		}
		active, err := genreCatalogJobActive(ctx, tx, request)
		if err != nil {
			return fmt.Errorf("reconcile genre catalog refresh request: %w", err)
		}
		if active {
			if dueAt != nil && now.Before(*dueAt) {
				return nil
			}
			if client == nil {
				return fmt.Errorf("dispatch genre catalog refresh: River is unavailable")
			}
			jobID = request.JobID
			if _, err := tx.NewRaw(`UPDATE genre_catalog SET refresh_due_at=? WHERE singleton=true`, nextDue).Exec(ctx); err != nil {
				return fmt.Errorf("advance genre catalog refresh deadline: %w", err)
			}
			return nil
		}
		retiredID, err = repository.retireMissingRefreshOperation(ctx, tx, request)
		if err != nil {
			return err
		}
		if dueAt != nil && now.Before(*dueAt) {
			return nil
		}
		if client == nil {
			return fmt.Errorf("dispatch genre catalog refresh: River is unavailable")
		}
		jobID, _, _, err = repository.createRefreshOperation(ctx, tx, client, &nextDue)
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE genre_catalog SET refresh_due_at=? WHERE singleton=true`, nextDue).Exec(ctx); err != nil {
			return fmt.Errorf("advance genre catalog refresh deadline: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, false, uuid.Nil, err
	}
	return jobID, jobID != 0, retiredID, nil
}

func (repository *GenreCatalogRepository) retireMissingRefreshOperation(ctx context.Context, tx bun.Tx, request GenreCatalogRefreshRequest) (uuid.UUID, error) {
	if request.OperationID == uuid.Nil || (request.Status != "queued" && request.Status != "running") {
		return uuid.Nil, nil
	}
	message := "Genre catalog refresh delivery was lost"
	result, err := tx.NewRaw(`UPDATE operation SET state='failed', stage='failed', safe_error=?, finished_at=now(), updated_at=now() WHERE id=? AND kind='refresh_genre_catalog_v1' AND state IN ('queued','running') AND attempt=1 AND river_job_id=?`, message, request.OperationID, request.JobID).Exec(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("retire missing genre catalog operation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.NewRaw(`UPDATE genre_catalog SET refresh_request=jsonb_set(refresh_request, '{status}', '"finished"'::jsonb), last_attempt_at=COALESCE(last_attempt_at, now()), last_error=? WHERE singleton=true AND refresh_request->>'operation_id'=? AND refresh_request->>'job_id'=? AND refresh_request->>'status' IN ('queued','running')`, message, request.OperationID, fmt.Sprint(request.JobID)).Exec(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("finish missing genre catalog request: %w", err)
	}
	if count == 1 {
		return request.OperationID, nil
	}
	return uuid.Nil, nil
}

// createRefreshOperation persists the operation, River delivery, and durable
// singleton request together. The caller holds the singleton row lock.
func (repository *GenreCatalogRepository) createRefreshOperation(ctx context.Context, tx bun.Tx, client GenreCatalogJobEnqueuer, dueAt *time.Time) (int64, uuid.UUID, GenreCatalogRefreshRequest, error) {
	if repository.operations == nil {
		return 0, uuid.Nil, GenreCatalogRefreshRequest{}, fmt.Errorf("genre catalog operation repository is unavailable")
	}
	operation := &Operation{ID: uuid.New(), Kind: "refresh_genre_catalog_v1", State: "queued", Stage: "queued", InputSnapshot: json.RawMessage(`{}`), Attempt: 1}
	if err := repository.operations.CreateOperationWith(ctx, tx, operation); err != nil {
		return 0, uuid.Nil, GenreCatalogRefreshRequest{}, err
	}
	result, err := client.InsertTx(ctx, tx.Tx, GenreCatalogRefreshArgs{OperationID: operation.ID}, nil)
	if err != nil {
		return 0, uuid.Nil, GenreCatalogRefreshRequest{}, fmt.Errorf("enqueue genre catalog refresh: %w", err)
	}
	jobID := result.Job.ID
	if _, err := tx.NewRaw(`UPDATE operation SET river_job_id=? WHERE id=?`, jobID, operation.ID).Exec(ctx); err != nil {
		return 0, uuid.Nil, GenreCatalogRefreshRequest{}, fmt.Errorf("bind genre catalog operation delivery: %w", err)
	}
	request := GenreCatalogRefreshRequest{JobID: jobID, OperationID: operation.ID, Token: uuid.NewString(), Status: "queued"}
	encoded, err := json.Marshal(request)
	if err != nil {
		return 0, uuid.Nil, GenreCatalogRefreshRequest{}, fmt.Errorf("encode genre catalog refresh request: %w", err)
	}
	if _, err := tx.NewRaw(`UPDATE genre_catalog SET refresh_request=?::jsonb, refresh_due_at=COALESCE(refresh_due_at, ?) WHERE singleton=true`, string(encoded), dueAt).Exec(ctx); err != nil {
		return 0, uuid.Nil, GenreCatalogRefreshRequest{}, fmt.Errorf("persist genre catalog refresh request: %w", err)
	}
	return jobID, operation.ID, request, nil
}

func genreCatalogJobActive(ctx context.Context, tx bun.Tx, request GenreCatalogRefreshRequest) (bool, error) {
	if request.Status != "queued" && request.Status != "running" || request.JobID <= 0 {
		return false, nil
	}
	var active bool
	err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM operation o JOIN river_job j ON j.id=o.river_job_id
		WHERE o.id=? AND o.kind='refresh_genre_catalog_v1' AND o.state IN ('queued','running')
			AND o.attempt=1 AND o.river_job_id=? AND j.state IN ('available','pending','running','retryable','scheduled')
	)`, request.OperationID, request.JobID).Scan(ctx, &active)
	if err != nil {
		return false, fmt.Errorf("read River genre catalog job: %w", err)
	}
	return active, nil
}

// ClaimRefresh rotates the delivery token on every attempt; duplicate or stale
// workers therefore cannot publish or fail the current job.
func (repository *GenreCatalogRepository) ClaimRefresh(ctx context.Context, jobID int64) (string, bool, error) {
	token := uuid.NewString()
	var claimed bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var operationID uuid.UUID
		if err := tx.NewRaw(`SELECT (refresh_request->>'operation_id')::uuid FROM genre_catalog WHERE singleton=true AND refresh_request->>'job_id'=? AND refresh_request->>'status' IN ('queued','running') FOR UPDATE`, fmt.Sprint(jobID)).Scan(ctx, &operationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("read genre catalog operation: %w", err)
		}
		result, err := tx.NewRaw(`UPDATE operation SET state='running', stage='refreshing', started_at=COALESCE(started_at, now()), updated_at=now() WHERE id=? AND kind='refresh_genre_catalog_v1' AND state IN ('queued','running') AND attempt=1 AND river_job_id=?`, operationID, jobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("claim genre catalog operation: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return nil
		}
		result, err = tx.NewRaw(`UPDATE genre_catalog SET refresh_request=jsonb_set(jsonb_set(refresh_request, '{token}', to_jsonb(?::text)), '{status}', '"running"'::jsonb) WHERE singleton=true AND refresh_request->>'job_id'=? AND refresh_request->>'operation_id'=? AND refresh_request->>'status' IN ('queued','running')`, token, fmt.Sprint(jobID), operationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("claim genre catalog refresh: %w", err)
		}
		count, err = result.RowsAffected()
		claimed = count == 1
		return err
	})
	if err != nil {
		return "", false, err
	}
	return token, claimed, nil
}

func (repository *GenreCatalogRepository) BeginClaimedRefresh(ctx context.Context, jobID int64, token, identity string, at time.Time) (bool, error) {
	result, err := repository.db.NewRaw(`UPDATE genre_catalog SET configuration_identity=?, last_attempt_at=? WHERE singleton=true AND refresh_request->>'job_id'=? AND refresh_request->>'token'=? AND refresh_request->>'status'='running'`, identity, at, fmt.Sprint(jobID), token).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("begin genre catalog refresh: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// ReplaceFenced atomically publishes only the currently claimed delivery and
// matching provider configuration. Completion is retained for duplicate jobs.
func (repository *GenreCatalogRepository) ReplaceFenced(ctx context.Context, jobID int64, token, identity string, items json.RawMessage, at time.Time) (bool, error) {
	var replaced bool
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("replace genre catalog: lock configuration: %w", err)
		}
		var currentIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
			return fmt.Errorf("replace genre catalog: read current configuration: %w", err)
		}
		if currentIdentity != identity {
			return nil
		}
		var operationID uuid.UUID
		if err := tx.NewRaw(`SELECT (refresh_request->>'operation_id')::uuid FROM genre_catalog WHERE singleton=true AND refresh_request->>'job_id'=? AND refresh_request->>'token'=? AND refresh_request->>'status'='running' FOR UPDATE`, fmt.Sprint(jobID), token).Scan(ctx, &operationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("read genre catalog operation: %w", err)
		}
		result, err := tx.NewRaw(`UPDATE operation SET state='succeeded', stage='finished', finished_at=?, updated_at=?, safe_error=NULL WHERE id=? AND kind='refresh_genre_catalog_v1' AND state='running' AND attempt=1 AND river_job_id=?`, at, at, operationID, jobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("complete genre catalog operation: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return nil
		}
		result, err = tx.NewRaw(`UPDATE genre_catalog SET items=?::jsonb, updated_at=?, last_error=NULL, refresh_request=jsonb_set(refresh_request, '{status}', '"finished"'::jsonb) WHERE singleton=true AND refresh_request->>'job_id'=? AND refresh_request->>'token'=? AND refresh_request->>'status'='running' AND refresh_request->>'operation_id'=? AND configuration_identity=?`, string(items), at, fmt.Sprint(jobID), token, operationID, identity).Exec(ctx)
		if err != nil {
			return fmt.Errorf("replace genre catalog: %w", err)
		}
		count, err = result.RowsAffected()
		replaced = count == 1
		return err
	})
	return replaced, err
}

func (repository *GenreCatalogRepository) RecordFailureFenced(ctx context.Context, jobID int64, token, identity string, at time.Time, message string) (bool, error) {
	var recorded bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var operationID uuid.UUID
		if err := tx.NewRaw(`SELECT (refresh_request->>'operation_id')::uuid FROM genre_catalog WHERE singleton=true AND refresh_request->>'job_id'=? AND refresh_request->>'token'=? AND refresh_request->>'status'='running' FOR UPDATE`, fmt.Sprint(jobID), token).Scan(ctx, &operationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("read genre catalog operation: %w", err)
		}
		result, err := tx.NewRaw(`UPDATE operation SET state='failed', stage='failed', safe_error=?, finished_at=?, updated_at=? WHERE id=? AND kind='refresh_genre_catalog_v1' AND state='running' AND attempt=1 AND river_job_id=?`, message, at, at, operationID, jobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("fail genre catalog operation: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return nil
		}
		result, err = tx.NewRaw(`UPDATE genre_catalog SET last_attempt_at=?, last_error=?, refresh_request=jsonb_set(refresh_request, '{status}', '"finished"'::jsonb) WHERE singleton=true AND refresh_request->>'job_id'=? AND refresh_request->>'token'=? AND refresh_request->>'operation_id'=? AND refresh_request->>'status'='running'`, at, message, fmt.Sprint(jobID), token, operationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record failed genre catalog refresh: %w", err)
		}
		count, err = result.RowsAffected()
		recorded = count == 1
		return err
	})
	return recorded, err
}
