package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const publicationPreparationCleanupVersion = 1

type PublicationPreparationCleanupWitness struct {
	Preparation PublicationPreparationRecord `json:"preparation"`
}

type publicationPreparationCleanupRecord struct {
	Version int                                  `json:"version"`
	Kind    string                               `json:"kind"`
	Step    string                               `json:"step"`
	Guard   PublicationDeliveryGuard             `json:"guard"`
	Witness PublicationPreparationCleanupWitness `json:"witness"`
	Result  string                               `json:"result,omitempty"`
}

type PublicationPreparationCleanupOutcome string

const (
	PublicationPreparationCleanupRecorded PublicationPreparationCleanupOutcome = "recorded"
	PublicationPreparationCleanupStale    PublicationPreparationCleanupOutcome = "stale"
)

func (repository *SetupManagerRepository) BeginPublicationPreparationCleanup(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationPreparationCleanupWitness) (PublicationPreparationCleanupOutcome, error) {
	return repository.recordPublicationPreparationCleanup(ctx, guard, witness, "intent", "")
}

func (repository *SetupManagerRepository) RecordPublicationPreparationCleanupResult(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationPreparationCleanupWitness) (PublicationPreparationCleanupOutcome, error) {
	return repository.recordPublicationPreparationCleanup(ctx, guard, witness, "result", "removed")
}

func (repository *SetupManagerRepository) recordPublicationPreparationCleanup(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationPreparationCleanupWitness, step, result string) (PublicationPreparationCleanupOutcome, error) {
	if validatePublicationDeliveryGuard(guard) != nil || !validPublicationPreparationCleanupWitness(guard, witness) || (step != "intent" || result != "") && (step != "result" || result != "removed") {
		return PublicationPreparationCleanupStale, errors.New("invalid publication preparation cleanup witness")
	}
	out := PublicationPreparationCleanupStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "record publication preparation cleanup")
		if err != nil || !live || !validPreInputCleanupState(attempt, operation, claim) {
			return err
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		wantLength := 2
		if step == "result" {
			wantLength = 3
		}
		if err != nil || !exactPreInputCleanupPrefix(entries, guard, witness, wantLength) {
			return err
		}
		journal, err := appendJournalEntry(attempt.MutationJournal, publicationPreparationCleanupRecord{Version: publicationPreparationCleanupVersion, Kind: "publication_preparation_cleanup", Step: step, Guard: guard, Witness: witness, Result: result})
		if err != nil {
			return err
		}
		res, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='preparing'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := res.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("publication preparation cleanup journal changed concurrently")
		}
		out = PublicationPreparationCleanupRecorded
		return nil
	})
	return out, err
}

// SettlePublicationPreparationFailure releases only rows owned by this exact
// preparing delivery. originalScopeAcknowledged is a process-local assertion;
// persistence cannot recreate the capability after a crash.
func (repository *SetupManagerRepository) SettlePublicationPreparationFailure(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationPreparationCleanupWitness, originalScopeAcknowledged bool) (bool, error) {
	if validatePublicationDeliveryGuard(guard) != nil || !originalScopeAcknowledged || !validPublicationPreparationCleanupWitness(guard, witness) {
		return false, nil
	}
	settled := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "settle publication preparation failure")
		if err != nil || !live || !validPreInputCleanupState(attempt, operation, claim) {
			return err
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil || !exactPreInputCleanupPrefix(entries, guard, witness, 4) {
			return err
		}
		var hold PublicationSourceHold
		err = tx.NewSelect().Model(&hold).Where("operation_id=?", guard.OperationID).For("UPDATE").Scan(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lock publication source hold: %w", err)
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
		res, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='publication_preparation',safe_error=?,finished_at=now(),updated_at=now() WHERE id=? AND state='running' AND stage='preparing' AND attempt=? AND river_job_id=?`, "Publication preparation failed before input creation; its empty private directory was removed.", guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := res.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("publication preparation operation changed concurrently")
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

func validPreInputCleanupState(a *TrackPublicationAttempt, o *Operation, c *PublicationExecutionClaim) bool {
	return a != nil && o != nil && c != nil && a.Phase == "preparing" && o.State == "running" && o.Stage == "preparing" && c.State == "executing" && (a.IntentKind == "audio" || a.IntentKind == "rewrite") && a.PositionID != uuid.Nil && a.CandidateGeneration != uuid.Nil
}

func validPublicationPreparationCleanupWitness(g PublicationDeliveryGuard, w PublicationPreparationCleanupWitness) bool {
	p := w.Preparation
	return p.Step == "directory_created" && validPreparationJournalRecord(p) && p.OperationID == g.OperationID && p.ClaimUUID == g.ClaimUUID && p.DirectoryIdentity != nil && p.ParentIdentity.Platform == p.DirectoryIdentity.Platform && portablePreparationPath(p.PrivateDirRel) && !strings.Contains(p.PrivateDirRel, "/")
}

func exactPreInputCleanupPrefix(entries []json.RawMessage, g PublicationDeliveryGuard, w PublicationPreparationCleanupWitness, expectedLength int) bool {
	if expectedLength < 2 || expectedLength > 4 || len(entries) != expectedLength {
		return false
	}
	steps := []string{"intent", "directory_created"}
	for i := 0; i < 2; i++ {
		var p PublicationPreparationRecord
		if decodeStrictJSON(entries[i], &p) != nil || !validPublicationJournalPrepMatch(p, g, w.Preparation, steps[i]) {
			return false
		}
	}
	var rec publicationPreparationCleanupRecord
	if expectedLength >= 3 {
		if decodeStrictJSON(entries[2], &rec) != nil || rec.Version != publicationPreparationCleanupVersion || rec.Kind != "publication_preparation_cleanup" || rec.Step != "intent" || rec.Guard != g || !reflect.DeepEqual(rec.Witness, w) || rec.Result != "" {
			return false
		}
	}
	if expectedLength == 4 {
		if decodeStrictJSON(entries[3], &rec) != nil || rec.Version != publicationPreparationCleanupVersion || rec.Kind != "publication_preparation_cleanup" || rec.Step != "result" || rec.Result != "removed" || rec.Guard != g || !reflect.DeepEqual(rec.Witness, w) {
			return false
		}
	}
	return true
}

func validPublicationJournalPrepMatch(p PublicationPreparationRecord, g PublicationDeliveryGuard, created PublicationPreparationRecord, step string) bool {
	return p.Step == step && validPreparationJournalRecord(p) && p.OperationID == g.OperationID && p.ClaimUUID == g.ClaimUUID && p.OwnerToken == created.OwnerToken && p.PrivateDirRel == created.PrivateDirRel && p.InputRel == created.InputRel && p.OutputRel == created.OutputRel && p.ParentIdentity == created.ParentIdentity && (step == "intent" && p.DirectoryIdentity == nil || step == "directory_created" && p.DirectoryIdentity != nil && created.DirectoryIdentity != nil && *p.DirectoryIdentity == *created.DirectoryIdentity)
}
