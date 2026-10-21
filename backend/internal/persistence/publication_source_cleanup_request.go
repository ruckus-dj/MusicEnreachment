package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

const publicationSourceCleanupRequestBatch = 32

// admitPublicationSourceCleanupTx is the transaction-scoped form of public
// admission. The caller owns output, policy, source-mutation, and request locks.
func (repository *SetupManagerRepository) admitPublicationSourceCleanupTx(ctx context.Context, tx bun.Tx, request PublicationSourceCleanupAdmission, client RiverInserter) (PublicationSourceCleanupAdmissionResult, error) {
	result := PublicationSourceCleanupAdmissionResult{}
	if request.PositionID == uuid.Nil || request.ExpectedEpoch == uuid.Nil || request.ExpectedGeneration == uuid.Nil || client == nil {
		return result, fmt.Errorf("admit publication source cleanup: position, publication witness, and River client are required")
	}
	var setting string
	err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(ctx, &setting)
	if err == sql.ErrNoRows {
		result.Blockers = []string{"publication source removal is disabled"}
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("admit publication source cleanup: read publication_remove_sources: %w", err)
	}
	enabled, err := strconv.ParseBool(setting)
	if err != nil {
		return result, fmt.Errorf("admit publication source cleanup: parse publication_remove_sources: %w", err)
	}
	if !enabled {
		result.Blockers = []string{"publication source removal is disabled"}
		return result, nil
	}
	assessment := PublicationSourceCleanupAssessment{PositionID: request.PositionID}
	if err := readPublicationSourceCleanupAssessmentTx(ctx, tx, request.PositionID, &assessment); err != nil {
		return result, fmt.Errorf("admit publication source cleanup: assess sources: %w", err)
	}
	if err := filterHandledSourceCleanupAssessment(ctx, tx, &assessment); err != nil {
		return result, err
	}
	if len(assessment.Candidates) == 0 {
		result.Blockers = uniquePublicationCleanupStrings(assessment.Blockers)
		return result, nil
	}
	if err := lockCleanupCandidateRows(ctx, tx, assessment.Candidates); err != nil {
		return result, fmt.Errorf("admit publication source cleanup: lock source identities: %w", err)
	}
	busy, err := hasExistingSourceAnalysisWorkHold(ctx, tx, assessment.Candidates)
	if err != nil {
		return result, fmt.Errorf("admit publication source cleanup: check source analysis work holds: %w", err)
	}
	if busy {
		result.Blockers = []string{"source has an analysis work hold"}
		return result, nil
	}
	var epoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
		return result, fmt.Errorf("admit publication source cleanup: lock output epoch: %w", err)
	}
	if epoch != request.ExpectedEpoch || assessment.Epoch != request.ExpectedEpoch || assessment.Generation != request.ExpectedGeneration {
		result.Blockers = []string{"current publication witness changed"}
		return result, nil
	}
	if err := lockCleanupBorrowerPositions(ctx, tx, assessment.Candidates); err != nil {
		return result, fmt.Errorf("admit publication source cleanup: lock borrower positions: %w", err)
	}
	assessment = PublicationSourceCleanupAssessment{PositionID: request.PositionID}
	if err := readPublicationSourceCleanupAssessmentTx(ctx, tx, request.PositionID, &assessment); err != nil {
		return result, fmt.Errorf("admit publication source cleanup: recheck sources: %w", err)
	}
	if err := filterHandledSourceCleanupAssessment(ctx, tx, &assessment); err != nil {
		return result, err
	}
	if assessment.Epoch != epoch || assessment.Generation != request.ExpectedGeneration {
		result.Blockers = []string{"current publication witness changed"}
		return result, nil
	}
	if blockers := cleanupAdmissionBlockers(assessment, epoch); len(blockers) != 0 {
		result.Blockers = blockers
		return result, nil
	}
	owned, err := hasExistingCleanupOwner(ctx, tx, assessment.Candidates)
	if err != nil {
		return result, err
	}
	if owned {
		result.Blockers = []string{"source already has a cleanup owner"}
		return result, nil
	}
	if err := verifyCleanupCandidateFacts(ctx, tx, assessment.Candidates); err != nil {
		if err == errCleanupCandidateConflict {
			result.Blockers = []string{"source cleanup candidate identities changed or conflict"}
			return result, nil
		}
		return result, err
	}
	opID := uuid.New()
	snapshot, err := json.Marshal(publicationSourceCleanupOperationSnapshot{Version: 1, PositionID: request.PositionID, OutputEpoch: epoch})
	if err != nil {
		return result, fmt.Errorf("admit publication source cleanup: encode operation snapshot: %w", err)
	}
	op := &Operation{ID: opID, Kind: "cleanup_publication_sources_v1", State: "queued", Stage: "queued", Attempt: 1, InputSnapshot: snapshot}
	if err := repository.createOperationAndEnqueueTx(ctx, tx, op, client, publicationSourceCleanupJobArgs{OperationID: opID}, nil); err != nil {
		return result, fmt.Errorf("admit publication source cleanup: enqueue operation: %w", err)
	}
	for _, candidate := range assessment.Candidates {
		if err := insertCleanupOwner(ctx, tx, op, request, epoch, candidate); err != nil {
			return result, fmt.Errorf("admit publication source cleanup: create owner: %w", err)
		}
	}
	result.OperationID = opID
	return result, nil
}

func (repository *SetupManagerRepository) requestSourceCleanupTx(ctx context.Context, tx bun.IDB, operationID, positionID, epoch, generation uuid.UUID) error {
	var enabledValue string
	err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(ctx, &enabledValue)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read publication source removal setting: %w", err)
	}
	enabled, err := strconv.ParseBool(enabledValue)
	if err != nil {
		return fmt.Errorf("parse publication source removal setting: %w", err)
	}
	if !enabled {
		return nil
	}
	_, err = tx.NewRaw(`INSERT INTO publication_source_cleanup_request
		(originating_operation_id,position_id,output_epoch,publication_generation)
		VALUES (?,?,?,?) ON CONFLICT (position_id,output_epoch) DO UPDATE SET
		originating_operation_id=EXCLUDED.originating_operation_id,
		publication_generation=EXCLUDED.publication_generation,requested_at=clock_timestamp(),last_attempted_at=NULL`,
		operationID, positionID, epoch, generation).Exec(ctx)
	if err != nil {
		return fmt.Errorf("insert current publication source cleanup request: %w", err)
	}
	return nil
}

// ConsumePublicationSourceCleanupRequest dispatches only one durable request;
// a blocked request remains for the bounded periodic retry.
func (repository *SetupManagerRepository) ConsumePublicationSourceCleanupRequest(ctx context.Context, originID uuid.UUID, client RiverInserter) (PublicationSourceCleanupAdmissionResult, error) {
	result := PublicationSourceCleanupAdmissionResult{}
	if originID == uuid.Nil || client == nil {
		return result, fmt.Errorf("consume publication source cleanup request: request identity and River client are required")
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("consume publication source cleanup request: lock output gate: %w", err)
		}
		if err := lockPublicationSourceCleanupPolicy(ctx, tx, true); err != nil {
			return fmt.Errorf("consume publication source cleanup request: lock policy gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("consume publication source cleanup request: lock source mutation gate: %w", err)
		}
		var request struct {
			PositionID uuid.UUID `bun:"position_id"`
			Epoch      uuid.UUID `bun:"output_epoch"`
			Generation uuid.UUID `bun:"publication_generation"`
		}
		err := tx.NewRaw(`SELECT position_id,output_epoch,publication_generation FROM publication_source_cleanup_request WHERE originating_operation_id=?`, originID).Scan(ctx, &request)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read current publication source cleanup request: %w", err)
		}
		var namespaceEpoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &namespaceEpoch); err != nil {
			return fmt.Errorf("lock current publication namespace: %w", err)
		}
		var lockedPosition uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, request.PositionID).Scan(ctx, &lockedPosition); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("lock current publication position: %w", err)
		}
		var lockedRequest struct {
			PositionID uuid.UUID `bun:"position_id"`
			Epoch      uuid.UUID `bun:"output_epoch"`
			Generation uuid.UUID `bun:"publication_generation"`
		}
		err = tx.NewRaw(`SELECT position_id,output_epoch,publication_generation FROM publication_source_cleanup_request WHERE originating_operation_id=? FOR UPDATE`, originID).Scan(ctx, &lockedRequest)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock current publication source cleanup request: %w", err)
		}
		if lockedRequest.PositionID != request.PositionID || lockedRequest.Epoch != request.Epoch || lockedRequest.Generation != request.Generation {
			return nil
		}
		var enabledValue string
		err = tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_remove_sources'`).Scan(ctx, &enabledValue)
		enabled := false
		if err != sql.ErrNoRows {
			if err != nil {
				return fmt.Errorf("read publication source removal setting: %w", err)
			}
			enabled, err = strconv.ParseBool(enabledValue)
			if err != nil {
				return fmt.Errorf("parse publication source removal setting: %w", err)
			}
		}
		var current struct {
			Epoch      uuid.UUID `bun:"epoch"`
			Generation uuid.UUID `bun:"generation"`
		}
		err = tx.NewRaw(`SELECT p.epoch,p.generation FROM track_publication p JOIN publication_namespace_state n ON n.singleton AND n.output_epoch=p.epoch WHERE p.position_id=?`, request.PositionID).Scan(ctx, &current)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("check current publication source cleanup request: %w", err)
		}
		if !enabled || err == sql.ErrNoRows || namespaceEpoch != request.Epoch || current.Epoch != request.Epoch || current.Generation != request.Generation {
			_, deleteErr := tx.NewRaw(`DELETE FROM publication_source_cleanup_request WHERE originating_operation_id=?`, originID).Exec(ctx)
			return deleteErr
		}
		// AdmitPublicationSourceCleanupTx reuses all existing source/borrower checks.
		result, err = repository.admitPublicationSourceCleanupTx(ctx, tx, PublicationSourceCleanupAdmission{PositionID: request.PositionID, ExpectedEpoch: request.Epoch, ExpectedGeneration: request.Generation}, client)
		if err != nil {
			return err
		}
		if result.OperationID != uuid.Nil {
			if _, err := tx.NewRaw(`DELETE FROM publication_source_cleanup_request WHERE originating_operation_id=?`, originID).Exec(ctx); err != nil {
				return fmt.Errorf("consume publication source cleanup request: %w", err)
			}
		} else if len(result.Blockers) == 1 && result.Blockers[0] == "no unhandled current source alternatives" {
			if _, err := tx.NewRaw(`DELETE FROM publication_source_cleanup_request WHERE originating_operation_id=?`, originID).Exec(ctx); err != nil {
				return fmt.Errorf("retire handled publication source cleanup request: %w", err)
			}
		}
		return nil
	})
	return result, err
}

// DispatchPendingPublicationSourceCleanupRequests reads only the durable request
// queue and processes a bounded batch; it never inventories publications.
func (repository *SetupManagerRepository) DispatchPendingPublicationSourceCleanupRequests(ctx context.Context, client RiverInserter) (int, error) {
	if client == nil {
		return 0, fmt.Errorf("dispatch publication source cleanup requests: River client is required")
	}
	var ids []uuid.UUID
	if err := repository.db.NewRaw(`WITH selected AS (
		SELECT originating_operation_id
		FROM publication_source_cleanup_request
		ORDER BY last_attempted_at NULLS FIRST, originating_operation_id
		LIMIT ?
		FOR UPDATE SKIP LOCKED
	)
	UPDATE publication_source_cleanup_request AS request
	SET last_attempted_at=clock_timestamp()
	FROM selected
	WHERE request.originating_operation_id=selected.originating_operation_id
	RETURNING request.originating_operation_id`, publicationSourceCleanupRequestBatch).Scan(ctx, &ids); err != nil {
		return 0, fmt.Errorf("select publication source cleanup request batch: %w", err)
	}
	consumed := 0
	var dispatchErrors []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			dispatchErrors = append(dispatchErrors, err)
			break
		}
		result, err := repository.ConsumePublicationSourceCleanupRequest(ctx, id, client)
		if err != nil {
			dispatchErrors = append(dispatchErrors, fmt.Errorf("consume publication source cleanup request %s: %w", id, err))
			if ctx.Err() != nil {
				dispatchErrors = append(dispatchErrors, ctx.Err())
				break
			}
			continue
		}
		if result.OperationID != uuid.Nil || len(result.Blockers) == 0 {
			consumed++
		}
	}
	if err := errors.Join(dispatchErrors...); err != nil {
		return consumed, fmt.Errorf("dispatch publication source cleanup requests: %w", err)
	}
	return consumed, nil
}

var _ river.JobArgs = publicationSourceCleanupJobArgs{}
