package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/uptrace/bun"
)

const initialOutputCleanupVersion = 1

// PublicationInitialOutputCleanupWitness binds cleanup to the exact prepared
// input and output observed by the original delivery after a returned
// validation error. It is retained only until cleanup is settled.
type PublicationInitialOutputCleanupWitness struct {
	Preparation PublicationPreparationRecord `json:"preparation"`
	Input       PublicationFileIdentity      `json:"input"`
	Output      PublicationFileIdentity      `json:"output"`
	OutputRel   string                       `json:"output_rel"`
}

type publicationInitialOutputCleanupRecord struct {
	Version int                                    `json:"version"`
	Kind    string                                 `json:"kind"`
	Step    string                                 `json:"step"`
	Guard   PublicationDeliveryGuard               `json:"guard"`
	Witness PublicationInitialOutputCleanupWitness `json:"witness"`
	Result  string                                 `json:"result,omitempty"`
}

type PublicationInitialOutputCleanupOutcome string

const (
	PublicationInitialOutputCleanupRecorded PublicationInitialOutputCleanupOutcome = "recorded"
	PublicationInitialOutputCleanupStale    PublicationInitialOutputCleanupOutcome = "stale"
)

func (repository *SetupManagerRepository) BeginPublicationInitialOutputCleanup(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationInitialOutputCleanupWitness) (PublicationInitialOutputCleanupOutcome, error) {
	return repository.recordPublicationInitialOutputCleanup(ctx, guard, witness, "intent", "")
}

func (repository *SetupManagerRepository) RecordPublicationInitialOutputCleanupResult(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationInitialOutputCleanupWitness) (PublicationInitialOutputCleanupOutcome, error) {
	return repository.recordPublicationInitialOutputCleanup(ctx, guard, witness, "complete", "removed")
}

func (repository *SetupManagerRepository) recordPublicationInitialOutputCleanup(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationInitialOutputCleanupWitness, step, result string) (PublicationInitialOutputCleanupOutcome, error) {
	if validatePublicationDeliveryGuard(guard) != nil || !validInitialOutputCleanupWitness(guard, witness) || (step != "intent" || result != "") && (step != "complete" || result != "removed") {
		return PublicationInitialOutputCleanupStale, errors.New("invalid initial publication output cleanup witness")
	}
	out := PublicationInitialOutputCleanupStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, claim, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "record initial publication output cleanup")
		if err != nil || !live || !validInitialOutputCleanupState(attempt, operation, claim) {
			return err
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		want := 0
		if step == "complete" {
			want = 1
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || !exactInitialOutputCleanupPrefix(entries, guard, input, witness, want) {
			return nil
		}
		journal, err := appendJournalEntry(attempt.MutationJournal, publicationInitialOutputCleanupRecord{Version: initialOutputCleanupVersion, Kind: "publication_initial_output_cleanup", Step: step, Guard: guard, Witness: witness, Result: result})
		if err != nil {
			return err
		}
		updated, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='preparing'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := updated.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("initial publication output cleanup journal changed concurrently")
		}
		out = PublicationInitialOutputCleanupRecorded
		return nil
	})
	return out, err
}

// SettlePublicationInitialOutputFailure deletes only the live original audio
// attempt after the cleanup completion ACK. The process-local assertion cannot
// be reconstructed after a crash.
func (repository *SetupManagerRepository) SettlePublicationInitialOutputFailure(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationInitialOutputCleanupWitness, originalScopeAcknowledged bool) (bool, error) {
	if validatePublicationDeliveryGuard(guard) != nil || !originalScopeAcknowledged || !validInitialOutputCleanupWitness(guard, witness) {
		return false, nil
	}
	settled := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, claim, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "settle initial publication output failure")
		if err != nil || !live || !validInitialOutputCleanupState(attempt, operation, claim) {
			return err
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		input, inputErr := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || inputErr != nil || !exactInitialOutputCleanupPrefix(entries, guard, input, witness, 2) {
			return err
		}
		var hold PublicationSourceHold
		err = tx.NewSelect().Model(&hold).Where("operation_id=?", guard.OperationID).For("UPDATE").Scan(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lock initial publication source hold: %w", err)
		}
		if err == nil && (hold.ApplicationAttempt != guard.ApplicationAttempt || hold.RiverJobID != guard.RiverJobID || hold.Epoch != guard.Epoch) {
			return nil
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_source_hold WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND epoch=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.Epoch).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=?`, guard.OperationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE operation SET tools_read_required=false WHERE id=? AND attempt=? AND river_job_id=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx); err != nil {
			return err
		}
		res, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='publication_preparation',safe_error=?,finished_at=now(),updated_at=now() WHERE id=? AND state='running' AND stage='preparing' AND attempt=? AND river_job_id=?`, "Initial publication output validation failed; its private staging files were removed.", guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := res.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("initial publication output failure settlement changed concurrently")
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_namespace_reservation r USING publication_namespace_claim c WHERE r.namespace_claim_id=c.id AND c.epoch=? AND r.position_id=? AND r.generation=?`, guard.Epoch, guard.ExpectedPositionID, guard.CandidateGeneration).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM track_publication_attempt WHERE operation_id=? AND phase='preparing'`, guard.OperationID).Exec(ctx); err != nil {
			return err
		}
		settled = true
		return nil
	})
	return settled, err
}

func validInitialOutputCleanupState(attempt *TrackPublicationAttempt, operation *Operation, claim *PublicationExecutionClaim) bool {
	return attempt != nil && operation != nil && claim != nil && attempt.IntentKind == "audio" && attempt.Phase == "preparing" && attempt.ExpectedGeneration == nil &&
		operation.State == "running" && operation.Stage == "preparing" && claim.State == "executing"
}

func validInitialOutputCleanupWitness(guard PublicationDeliveryGuard, witness PublicationInitialOutputCleanupWitness) bool {
	p := witness.Preparation
	return p.Step == "input_ready" && validPreparationJournalRecord(p) && p.OperationID == guard.OperationID && p.ClaimUUID == guard.ClaimUUID &&
		p.DirectoryIdentity != nil && p.CopiedInput != nil && witness.Input.SizeBytes != nil && witness.Input.MTime != nil && validSHA256(witness.Input.SHA256) &&
		witness.Output.SizeBytes != nil && *witness.Output.SizeBytes >= 0 && witness.Output.MTime != nil && validSHA256(witness.Output.SHA256) &&
		witness.OutputRel == p.OutputRel && portablePreparationPath(witness.OutputRel) && p.PrivateDirRel != "" &&
		p.ParentIdentity.Platform == p.DirectoryIdentity.Platform && p.ParentIdentity.Volume != "" && p.ParentIdentity.FileID != "" &&
		p.DirectoryIdentity.Volume != "" && p.DirectoryIdentity.FileID != "" && p.InputRel != p.OutputRel &&
		witness.Input.SizeBytes != nil && p.CopiedInput.SizeBytes != nil && *witness.Input.SizeBytes == *p.CopiedInput.SizeBytes &&
		witness.Input.MTime != nil && p.CopiedInput.MTime != nil && *witness.Input.MTime == *p.CopiedInput.MTime &&
		(p.CopiedInput.SHA256 == "" || p.CopiedInput.SHA256 == witness.Input.SHA256)
}

func exactInitialOutputCleanupPrefix(entries []json.RawMessage, guard PublicationDeliveryGuard, input PublicationInputs, witness PublicationInitialOutputCleanupWitness, cleanupCount int) bool {
	if cleanupCount < 0 || cleanupCount > 2 || len(entries) < 4+cleanupCount {
		return false
	}
	state, err := parseInitialLyricsPrefix(entries[:len(entries)-cleanupCount], guard, input)
	if err != nil || state.preparation == nil || state.preparation.Step != "input_ready" || state.acquisition == nil || state.outcome != nil || state.stageCount != 0 {
		return false
	}
	if !samePreparationRecord(*state.preparation, witness.Preparation) {
		return false
	}
	for i := 0; i < cleanupCount; i++ {
		var record publicationInitialOutputCleanupRecord
		if decodeStrictJSON(entries[len(entries)-cleanupCount+i], &record) != nil || record.Version != initialOutputCleanupVersion || record.Kind != "publication_initial_output_cleanup" || record.Guard != guard || !reflect.DeepEqual(record.Witness, witness) {
			return false
		}
		if i == 0 && (record.Step != "intent" || record.Result != "") || i == 1 && (record.Step != "complete" || record.Result != "removed") {
			return false
		}
	}
	return true
}
