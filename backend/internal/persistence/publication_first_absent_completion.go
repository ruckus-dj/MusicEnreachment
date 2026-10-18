package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/uptrace/bun"
)

const publicationFirstAbsentCleanupVersion = 1

// PublicationFirstAbsentCleanupWitness identifies exactly the private input and
// attempt directory owned by the original delivery. Its identities are caller
// evidence, not filesystem proof: the owning service must establish them live.
type PublicationFirstAbsentCleanupWitness struct {
	PrivateDirRel string                       `json:"private_dir_rel"`
	InputRel      string                       `json:"input_rel"`
	Directory     PublicationDirectoryIdentity `json:"directory_identity"`
	Input         PublicationFileIdentity      `json:"input_identity"`
}

type PublicationFirstAbsentCleanupOutcome string

const (
	PublicationFirstAbsentCleanupRecorded        PublicationFirstAbsentCleanupOutcome = "recorded"
	PublicationFirstAbsentCleanupAlreadyRecorded PublicationFirstAbsentCleanupOutcome = "already_recorded"
	PublicationFirstAbsentCleanupStale           PublicationFirstAbsentCleanupOutcome = "stale"
)

type publicationFirstAbsentCleanupRecord struct {
	Version int                                  `json:"version"`
	Kind    string                               `json:"kind"`
	Step    string                               `json:"step"`
	Guard   PublicationDeliveryGuard             `json:"guard"`
	Witness PublicationFirstAbsentCleanupWitness `json:"witness"`
	Name    string                               `json:"name"`
	Result  string                               `json:"result,omitempty"`
}

// BeginPublicationFirstAbsentDirectoryCleanup records intent only after the
// input unlink result has been journaled.
func (repository *SetupManagerRepository) BeginPublicationFirstAbsentDirectoryCleanup(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationFirstAbsentCleanupWitness) (PublicationFirstAbsentCleanupOutcome, error) {
	return repository.recordFirstAbsentCleanupIntent(ctx, guard, witness, "directory_intent", witness.PrivateDirRel, "input_result")
}

// BeginPublicationFirstAbsentCleanup records authority for the first cleanup
// action only. It does not prove the filesystem identity or perform I/O.
func (repository *SetupManagerRepository) BeginPublicationFirstAbsentCleanup(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationFirstAbsentCleanupWitness) (PublicationFirstAbsentCleanupOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationFirstAbsentCleanupStale, err
	}
	if !validFirstAbsentCleanupWitness(witness) {
		return PublicationFirstAbsentCleanupStale, errors.New("invalid first-absent cleanup witness")
	}
	return repository.recordFirstAbsentCleanupIntent(ctx, guard, witness, "input_intent", witness.InputRel, "")
}

func (repository *SetupManagerRepository) recordFirstAbsentCleanupIntent(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationFirstAbsentCleanupWitness, step, name, requires string) (PublicationFirstAbsentCleanupOutcome, error) {
	out := PublicationFirstAbsentCleanupStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "begin first-absent cleanup")
		if err != nil || !live || !firstAbsentCommittedState(attempt, operation, claim) {
			return err
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("begin first-absent cleanup: read current publication: %w", err)
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		suffixCount := 0
		if step == "directory_intent" {
			suffixCount = 2
		} else {
			suffixCount = 0
		}
		if !currentIsCandidate(current, attempt) || !committedPrefixMatches(entries, guard, attempt, current, witness) || !exactCleanupJournal(entries, guard, witness, suffixCount) {
			return nil
		}
		if requires != "" && !cleanupResultRecorded(entries, "input", witness.InputRel) {
			return nil
		}
		if step == "input_intent" && len(entries) == 0 {
			return nil
		}
		journal, err := appendJournalEntry(attempt.MutationJournal, publicationFirstAbsentCleanupRecord{Version: publicationFirstAbsentCleanupVersion, Kind: "publication_first_absent_cleanup", Step: step, Guard: guard, Witness: witness, Name: name})
		if err != nil {
			return err
		}
		if err := updateFirstAbsentJournal(ctx, tx, guard, journal, "begin first-absent cleanup"); err != nil {
			return err
		}
		out = PublicationFirstAbsentCleanupRecorded
		return nil
	})
	return out, err
}

// RecordPublicationFirstAbsentCleanupResult appends the result of one ordered
// unlink/rmdir action. A result is journal evidence only; it is never used as
// filesystem proof by startup recovery.
func (repository *SetupManagerRepository) RecordPublicationFirstAbsentCleanupResult(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationFirstAbsentCleanupWitness, step, result string) (PublicationFirstAbsentCleanupOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationFirstAbsentCleanupStale, err
	}
	if !validFirstAbsentCleanupWitness(witness) || (step != "input" && step != "directory") || (result != "removed" && result != "already_absent") {
		return PublicationFirstAbsentCleanupStale, errors.New("invalid first-absent cleanup result")
	}
	out := PublicationFirstAbsentCleanupStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "record first-absent cleanup result")
		if err != nil || !live || !firstAbsentCommittedState(attempt, operation, claim) {
			return err
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		index, name := 1, witness.InputRel
		if step == "directory" {
			index, name = 3, witness.PrivateDirRel
		}
		if !currentIsCandidate(current, attempt) || !committedPrefixMatches(entries, guard, attempt, current, witness) || !exactCleanupJournal(entries, guard, witness, index) {
			return nil
		}
		if step == "input" && !hasCleanupRecord(entries, "input_intent", witness.InputRel) {
			return nil
		}
		if step == "directory" && !hasCleanupRecord(entries, "directory_intent", witness.PrivateDirRel) {
			return nil
		}
		if step == "directory" && !cleanupResultRecorded(entries, "input", witness.InputRel) {
			return nil
		}
		journal, err := appendJournalEntry(attempt.MutationJournal, publicationFirstAbsentCleanupRecord{Version: publicationFirstAbsentCleanupVersion, Kind: "publication_first_absent_cleanup", Step: step + "_result", Guard: guard, Witness: witness, Name: name, Result: result})
		if err != nil {
			return err
		}
		if err := updateFirstAbsentJournal(ctx, tx, guard, journal, "record first-absent cleanup result"); err != nil {
			return err
		}
		out = PublicationFirstAbsentCleanupRecorded
		return nil
	})
	return out, err
}

// SettleFirstAbsentPublication atomically releases durable pins and succeeds the
// operation. originalScopeAcknowledged is an explicit service-side assertion;
// persistence cannot reconstruct the original process-local ownership claim.
func (repository *SetupManagerRepository) SettleFirstAbsentPublication(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationFirstAbsentCleanupWitness, originalScopeAcknowledged bool) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	if !originalScopeAcknowledged || !validFirstAbsentCleanupWitness(witness) {
		return false, nil
	}
	settled := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "settle first-absent publication")
		if err != nil || !live || !firstAbsentCommittedState(attempt, operation, claim) {
			return err
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		if !currentIsCandidate(current, attempt) || !committedPrefixMatches(entries, guard, attempt, current, witness) || !exactCleanupJournal(entries, guard, witness, 4) || !cleanupResultRecorded(entries, "input", witness.InputRel) || !cleanupResultRecorded(entries, "directory", witness.PrivateDirRel) {
			return nil
		}
		var hold PublicationSourceHold
		if err := tx.NewSelect().Model(&hold).Where("operation_id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("settle first-absent publication: lock source hold: %w", err)
		}
		if hold.ApplicationAttempt != guard.ApplicationAttempt || hold.RiverJobID != guard.RiverJobID || hold.Epoch != guard.Epoch {
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
		res, err := tx.NewRaw(`UPDATE operation SET state='succeeded',stage='finished',safe_error=NULL,finished_at=now(),updated_at=now() WHERE id=? AND state='running' AND attempt=? AND river_job_id=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := res.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("settle first-absent publication: operation changed concurrently")
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM track_publication_attempt WHERE operation_id=?`, guard.OperationID).Exec(ctx); err != nil {
			return err
		}
		settled = true
		return nil
	})
	return settled, err
}

func firstAbsentCommittedState(attempt *TrackPublicationAttempt, operation *Operation, claim *PublicationExecutionClaim) bool {
	return attempt != nil && operation != nil && claim != nil && attempt.IntentKind == "audio" && attempt.ExpectedGeneration == nil && attempt.Phase == "committed" && operation.State == "running" && operation.Stage == "committed" && claim.State == "executing"
}

func validFirstAbsentCleanupWitness(w PublicationFirstAbsentCleanupWitness) bool {
	return portablePreparationPath(w.PrivateDirRel) && portablePreparationPath(w.InputRel) && strings.HasPrefix(w.InputRel, w.PrivateDirRel+"/") && validPreparationPlatformIdentity(w.Directory) && w.Input.SizeBytes != nil && *w.Input.SizeBytes >= 0 && w.Input.MTime != nil
}

func currentIsCandidate(current TrackPublication, attempt *TrackPublicationAttempt) bool {
	return attempt != nil && current.PositionID == attempt.PositionID && current.Epoch == attempt.Epoch && current.Generation == attempt.CandidateGeneration
}

func committedPrefixMatches(entries []json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, current TrackPublication, witness PublicationFirstAbsentCleanupWitness) bool {
	commitIndex := -1
	for i, raw := range entries {
		var h struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &h) != nil {
			return false
		}
		if h.Kind == "publication_commit" {
			if commitIndex >= 0 {
				return false
			}
			commitIndex = i
		}
	}
	if commitIndex < 0 {
		return false
	}
	prefix, err := json.Marshal(entries[:commitIndex+1])
	if err != nil {
		return false
	}
	return committedWitnessMatches(prefix, guard, attempt, current) && preparationWitnessMatches(entries[:commitIndex], guard, witness)
}

func preparationWitnessMatches(entries []json.RawMessage, guard PublicationDeliveryGuard, witness PublicationFirstAbsentCleanupWitness) bool {
	var found bool
	for _, raw := range entries {
		var p PublicationPreparationRecord
		if decodeStrictJSON(raw, &p) == nil && p.Kind == "publication_preparation" && p.OperationID == guard.OperationID && p.ClaimUUID == guard.ClaimUUID && p.Step == "input_ready" && p.PrivateDirRel == witness.PrivateDirRel && p.InputRel == witness.InputRel && p.DirectoryIdentity != nil && *p.DirectoryIdentity == witness.Directory && p.ParentIdentity.Platform == witness.Directory.Platform && p.CopiedInput != nil && p.CopiedInput.SizeBytes != nil && p.CopiedInput.MTime != nil && witness.Input.SizeBytes != nil && witness.Input.MTime != nil && *p.CopiedInput.SizeBytes == *witness.Input.SizeBytes && *p.CopiedInput.MTime == *witness.Input.MTime {
			found = true
		}
	}
	return found
}

func exactCleanupJournal(entries []json.RawMessage, guard PublicationDeliveryGuard, witness PublicationFirstAbsentCleanupWitness, suffixCount int) bool {
	commitIndex := -1
	for i, raw := range entries {
		var h struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &h) != nil {
			return false
		}
		if h.Kind == "publication_commit" {
			commitIndex = i
		}
	}
	if commitIndex < 0 || len(entries) != commitIndex+1+suffixCount {
		return false
	}
	expected := []struct{ step, name string }{{"input_intent", witness.InputRel}, {"input_result", witness.InputRel}, {"directory_intent", witness.PrivateDirRel}, {"directory_result", witness.PrivateDirRel}}
	for i := 0; i < suffixCount; i++ {
		var rec publicationFirstAbsentCleanupRecord
		if decodeStrictJSON(entries[commitIndex+1+i], &rec) != nil || rec.Version != publicationFirstAbsentCleanupVersion || rec.Kind != "publication_first_absent_cleanup" || rec.Guard != guard || !sameFirstAbsentCleanupWitness(rec.Witness, witness) || rec.Step != expected[i].step || rec.Name != expected[i].name || !validFirstAbsentCleanupRecordResult(rec) {
			return false
		}
	}
	return true
}

func sameFirstAbsentCleanupWitness(left, right PublicationFirstAbsentCleanupWitness) bool {
	return left.PrivateDirRel == right.PrivateDirRel && left.InputRel == right.InputRel && left.Directory == right.Directory &&
		sameOptionalInt64(left.Input.SizeBytes, right.Input.SizeBytes) && sameOptionalInt64(left.Input.MTime, right.Input.MTime) && left.Input.SHA256 == right.Input.SHA256
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validFirstAbsentCleanupRecordResult(rec publicationFirstAbsentCleanupRecord) bool {
	if strings.HasSuffix(rec.Step, "_intent") {
		return rec.Result == ""
	}
	return !strings.HasSuffix(rec.Step, "_result") || rec.Result == "removed" || rec.Result == "already_absent"
}

func hasCleanupRecord(entries []json.RawMessage, step, name string) bool {
	for _, raw := range entries {
		var rec publicationFirstAbsentCleanupRecord
		if decodeStrictJSON(raw, &rec) == nil && rec.Step == step && rec.Name == name {
			return true
		}
	}
	return false
}

func cleanupResultRecorded(entries []json.RawMessage, step, name string) bool {
	for _, raw := range entries {
		var rec publicationFirstAbsentCleanupRecord
		if decodeStrictJSON(raw, &rec) == nil && rec.Kind == "publication_first_absent_cleanup" && rec.Step == step+"_result" && rec.Name == name && validFirstAbsentCleanupRecordResult(rec) {
			return true
		}
	}
	return false
}

func updateFirstAbsentJournal(ctx context.Context, tx bun.Tx, guard PublicationDeliveryGuard, journal json.RawMessage, action string) error {
	res, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='committed'`, string(journal), guard.OperationID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	rows, err := res.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("%s: attempt changed concurrently", action)
	}
	return nil
}
