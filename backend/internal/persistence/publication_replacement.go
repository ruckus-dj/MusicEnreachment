package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationReplacementEvidence is durable journal evidence for a guarded
// rewrite. BackupReady binds the verified backup to the exact current bytes;
// Installed binds the post-replacement readback to the same delivery.
type PublicationReplacementEvidence struct {
	Version             int                         `json:"version"`
	Kind                string                      `json:"kind"`
	From                string                      `json:"from"`
	To                  string                      `json:"to"`
	Guard               PublicationDeliveryGuard    `json:"guard"`
	PositionID          uuid.UUID                   `json:"position_id"`
	Epoch               uuid.UUID                   `json:"epoch"`
	CandidateGeneration uuid.UUID                   `json:"candidate_generation"`
	ExpectedGeneration  *uuid.UUID                  `json:"expected_generation"`
	Intent              PublicationExposureIntent   `json:"intent"`
	Staged              PublicationStageObservation `json:"staged"`
	Identity            PublicationFileIdentity     `json:"identity"`
	BackupIdentity      PublicationFileIdentity     `json:"backup_identity"`
	BackupPath          string                      `json:"backup_path,omitempty"`
}

type PublicationReplacementOutcome string

const (
	PublicationReplacementRecorded PublicationReplacementOutcome = "recorded"
	PublicationReplacementStale    PublicationReplacementOutcome = "stale"
)

// RecordPublicationReplacement appends one ordered replacement transition.
// It never authorizes replay of a filesystem mutation: callers must only
// perform a mutation after receiving Recorded for its preceding transition.
func (repository *SetupManagerRepository) RecordPublicationReplacement(ctx context.Context, guard PublicationDeliveryGuard, evidence PublicationReplacementEvidence) (PublicationReplacementOutcome, error) {
	if validatePublicationDeliveryGuard(guard) != nil || !validPublicationReplacementEvidence(evidence) || evidence.Guard != guard {
		return PublicationReplacementStale, errors.New("invalid publication replacement evidence")
	}
	result := PublicationReplacementStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "record publication replacement")
		if err != nil || !live {
			return err
		}
		// V3 replacement evidence has a separate, fully bound journal grammar.
		// Do not let the generic writer recognize any part of it.
		if input, decodeErr := DecodePublicationInputs(attempt.FrozenInputs); decodeErr == nil && input.Version == PublicationInputsVersion3 {
			return nil
		}
		if attempt.Phase != "exposing" || operation.Stage != "exposing" || attempt.IntentKind != "rewrite" || evidence.PositionID != attempt.PositionID || evidence.Epoch != attempt.Epoch || evidence.CandidateGeneration != attempt.CandidateGeneration || !sameOptionalUUID(evidence.ExpectedGeneration, attempt.ExpectedGeneration) || attempt.ExpectedGeneration == nil || evidence.Intent.Expectation != "expected_current" || evidence.Intent.TargetRelativePath == "" {
			return err
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || input.Rewrite == nil || input.Rewrite.CurrentGeneration != *attempt.ExpectedGeneration || input.Rewrite.RelativePath != evidence.Intent.TargetRelativePath {
			return nil
		}
		// Namespace authority is mutable after exposure begins. Revalidate the
		// frozen manifest against current claims and this candidate's reservation
		// before acknowledging any replacement transition.
		if err := validateExposureNamespace(ctx, tx, attempt, evidence.Intent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("record publication replacement: validate namespace: %w", err)
		}
		if evidence.To != "installed" && !samePublicationExposureJSON(input.Rewrite.Ownership, evidence.Identity) {
			return nil
		}
		// The original exposure record must bind this rewrite to the current
		// publication snapshot. No replacement step can be recorded before it.
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		if !replacementJournalFrontier(entries) {
			return nil
		}
		var exposure publicationExposureJournalRecord
		for _, raw := range entries {
			var h struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal(raw, &h) == nil && h.Kind == "publication_exposure" {
				if decodeStrictJSON(raw, &exposure) != nil {
					return errors.New("publication exposure journal is malformed")
				}
			}
		}
		if exposure.Kind != "publication_exposure" || exposure.Guard != guard || exposure.Intent != evidence.Intent || exposure.ExpectedGeneration == nil || *exposure.ExpectedGeneration != *attempt.ExpectedGeneration || exposure.PositionID != attempt.PositionID || exposure.Epoch != attempt.Epoch || exposure.CandidateGeneration != attempt.CandidateGeneration || !validPublicationStageObservation(exposure.Observation) || !samePublicationStageObservation(exposure.Observation, evidence.Staged) {
			return nil
		}
		preparation, err := decodeReplacementPreparation(entries, guard, exposure)
		if err != nil {
			return err
		}
		if evidence.BackupPath != preparation[0].InputRel || !sameCopiedBackupIdentity(evidence.BackupIdentity, *preparation[2].CopiedInput, input.Rewrite.Ownership) {
			return nil
		}
		var prior *PublicationReplacementEvidence
		for _, raw := range entries {
			var h struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal(raw, &h) != nil || h.Kind != "publication_replacement" {
				continue
			}
			var existing PublicationReplacementEvidence
			if decodeStrictJSON(raw, &existing) != nil || !validPublicationReplacementEvidence(existing) {
				return errors.New("publication replacement journal is malformed")
			}
			if !validReplacementSuccessor(prior, &existing) {
				return errors.New("publication replacement journal is out of order")
			}
			copy := existing
			prior = &copy
		}
		if !validReplacementSuccessor(prior, &evidence) {
			return nil
		}
		journal, err := appendJournalEntry(attempt.MutationJournal, evidence)
		if err != nil {
			return err
		}
		updated, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='exposing'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record publication replacement: %w", err)
		}
		rows, err := updated.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("record publication replacement: attempt changed concurrently")
		}
		result = PublicationReplacementRecorded
		return nil
	})
	if err != nil {
		return PublicationReplacementStale, err
	}
	return result, nil
}

// GuardedRecordManualLyricsReplacement appends one V3 manual-lyrics
// replacement transition. It records evidence only; it never mutates files or
// publication state.
func (repository *SetupManagerRepository) GuardedRecordManualLyricsReplacement(ctx context.Context, guard PublicationDeliveryGuard, evidence PublicationReplacementEvidence) (PublicationReplacementOutcome, error) {
	evidence = clonePublicationReplacementEvidence(evidence)
	if validatePublicationDeliveryGuard(guard) != nil || !validPublicationReplacementEvidence(evidence) || evidence.Guard != guard {
		return PublicationReplacementStale, errors.New("invalid manual lyrics replacement evidence")
	}
	result := PublicationReplacementStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "record manual lyrics replacement")
		if err != nil || !live || attempt.Phase != "exposing" || operation.Stage != "exposing" || attempt.IntentKind != "rewrite" || attempt.ExpectedGeneration == nil ||
			evidence.PositionID != attempt.PositionID || evidence.Epoch != attempt.Epoch || evidence.CandidateGeneration != attempt.CandidateGeneration || !sameOptionalUUID(evidence.ExpectedGeneration, attempt.ExpectedGeneration) {
			return err
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || !validManualLyricsFLACRewrite(input) || input.Version != PublicationInputsVersion3 || input.Rewrite == nil ||
			input.Rewrite.CurrentGeneration != *attempt.ExpectedGeneration || evidence.Intent.Expectation != "expected_current" || evidence.Intent.TargetRelativePath != input.Rewrite.RelativePath {
			return nil
		}
		if err := validateExposureNamespace(ctx, tx, attempt, evidence.Intent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("record manual lyrics replacement: validate namespace: %w", err)
		}
		entries, prefixEnd, exposure, _, ok := canonicalManualLyricsExposurePrefix(attempt, guard, input)
		if !ok || len(entries) < prefixEnd || !samePublicationStageObservation(exposure.Observation, evidence.Staged) || exposure.Intent != evidence.Intent ||
			exposure.Guard != guard || exposure.PositionID != attempt.PositionID || exposure.Epoch != attempt.Epoch || exposure.CandidateGeneration != attempt.CandidateGeneration || !sameOptionalUUID(exposure.ExpectedGeneration, attempt.ExpectedGeneration) {
			return nil
		}
		preparation, err := decodeReplacementPreparation(entries[:prefixEnd], guard, exposure)
		if err != nil {
			return err
		}
		if evidence.BackupPath != preparation[0].InputRel || preparation[2].CopiedInput == nil ||
			!sameCopiedBackupIdentity(evidence.BackupIdentity, *preparation[2].CopiedInput, input.Rewrite.Ownership) {
			return nil
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("record manual lyrics replacement: read current publication: %w", err)
		}
		currentJSON, err := json.Marshal(current)
		if err != nil {
			return fmt.Errorf("record manual lyrics replacement: encode current publication: %w", err)
		}
		if current.Generation != *attempt.ExpectedGeneration || current.Epoch != attempt.Epoch || current.RelativePath != evidence.Intent.TargetRelativePath ||
			!publicationRewriteMatchesCurrent(current, attempt.Epoch, *input.Rewrite) || !sameJSONValue(currentJSON, exposure.Current) {
			return nil
		}
		prior := []PublicationReplacementEvidence{}
		for _, raw := range entries[prefixEnd:] {
			var existing PublicationReplacementEvidence
			if decodeStrictJSON(raw, &existing) != nil || !validPublicationReplacementEvidence(existing) || !manualLyricsReplacementBinding(existing, guard, attempt, exposure, input, preparation) ||
				(existing.To != "installed" && !samePublicationExposureJSON(input.Rewrite.Ownership, existing.Identity)) {
				return errors.New("manual lyrics replacement journal is malformed or foreign")
			}
			if len(prior) == 0 {
				if existing.From != "exposing" || existing.To != "backup_intent" {
					return nil
				}
			} else if !validReplacementSuccessor(&prior[len(prior)-1], &existing) {
				return nil
			}
			prior = append(prior, existing)
		}
		if len(prior) >= 4 {
			return nil
		}
		if len(prior) == 0 {
			if evidence.From != "exposing" || evidence.To != "backup_intent" {
				return nil
			}
		} else if !validReplacementSuccessor(&prior[len(prior)-1], &evidence) {
			return nil
		}
		if !manualLyricsReplacementBinding(evidence, guard, attempt, exposure, input, preparation) ||
			(evidence.To != "installed" && !samePublicationExposureJSON(input.Rewrite.Ownership, evidence.Identity)) {
			return nil
		}
		journal, err := appendJournalEntry(attempt.MutationJournal, evidence)
		if err != nil {
			return err
		}
		updated, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='exposing'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record manual lyrics replacement: %w", err)
		}
		rows, err := updated.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("record manual lyrics replacement: attempt changed concurrently")
		}
		result = PublicationReplacementRecorded
		return nil
	})
	if err != nil {
		return PublicationReplacementStale, err
	}
	return result, nil
}

func clonePublicationReplacementEvidence(e PublicationReplacementEvidence) PublicationReplacementEvidence {
	if e.ExpectedGeneration != nil {
		value := *e.ExpectedGeneration
		e.ExpectedGeneration = &value
	}
	e.Staged = e.Staged.Clone()
	e.Identity = clonePublicationFileIdentity(e.Identity)
	e.BackupIdentity = clonePublicationFileIdentity(e.BackupIdentity)
	return e
}

func manualLyricsReplacementBinding(e PublicationReplacementEvidence, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, exposure publicationExposureJournalRecord, input PublicationInputs, preparation [3]PublicationPreparationRecord) bool {
	if e.To == "installed" && (e.Identity.MTime == nil || e.Staged.Ownership.ExpectedMTime == nil || *e.Identity.MTime != *e.Staged.Ownership.ExpectedMTime) {
		return false
	}
	return input.Rewrite != nil && preparation[2].CopiedInput != nil && attempt.ExpectedGeneration != nil &&
		*attempt.ExpectedGeneration != attempt.CandidateGeneration && e.Guard == guard && e.PositionID == attempt.PositionID && e.Epoch == attempt.Epoch &&
		e.CandidateGeneration == attempt.CandidateGeneration && sameOptionalUUID(e.ExpectedGeneration, attempt.ExpectedGeneration) &&
		e.Intent == exposure.Intent && samePublicationStageObservation(e.Staged, exposure.Observation) && e.BackupPath == preparation[0].InputRel &&
		sameCopiedBackupIdentity(e.BackupIdentity, *preparation[2].CopiedInput, input.Rewrite.Ownership)
}

func validPublicationReplacementEvidence(e PublicationReplacementEvidence) bool {
	if e.Version != 1 || e.Kind != "publication_replacement" || e.Guard.OperationID == uuid.Nil || validatePublicationDeliveryGuard(e.Guard) != nil || e.PositionID == uuid.Nil || e.Epoch == uuid.Nil || e.CandidateGeneration == uuid.Nil || e.ExpectedGeneration == nil || *e.ExpectedGeneration == uuid.Nil || e.PositionID != e.Guard.ExpectedPositionID || e.Epoch != e.Guard.Epoch || e.CandidateGeneration != e.Guard.CandidateGeneration || !validExposureIntent(e.Intent) || e.Intent.Expectation != "expected_current" || !validPublicationStageObservation(e.Staged) || !stageMatchesIntent(e.Staged, e.Intent) || e.Staged.Ownership.ExpectedSize == nil || e.Staged.Ownership.ExpectedMTime == nil || e.Identity.SizeBytes == nil || *e.Identity.SizeBytes < 0 || e.Identity.MTime == nil || !validSHA256(e.Identity.SHA256) || !validCopiedInputIdentity(&e.BackupIdentity) || !validSHA256(e.BackupIdentity.SHA256) {
		return false
	}
	switch e.To {
	case "backup_intent":
		return e.From == "exposing" && portablePreparationPath(e.BackupPath)
	case "backup_ready":
		return e.From == "backup_intent" && portablePreparationPath(e.BackupPath)
	case "replacement_intent":
		return e.From == "backup_ready" && portablePreparationPath(e.BackupPath)
	case "installed":
		return e.From == "replacement_intent" && portablePreparationPath(e.BackupPath) &&
			*e.Identity.SizeBytes == *e.Staged.Ownership.ExpectedSize && e.Identity.SHA256 == e.Staged.PayloadSHA256
	default:
		return false
	}
}

func sameCopiedBackupIdentity(actual, copied, frozen PublicationFileIdentity) bool {
	if actual.SizeBytes == nil || copied.SizeBytes == nil || *actual.SizeBytes != *copied.SizeBytes || actual.MTime == nil || copied.MTime == nil || *actual.MTime != *copied.MTime || !validSHA256(actual.SHA256) || (copied.SHA256 != "" && (!validSHA256(copied.SHA256) || actual.SHA256 != copied.SHA256)) {
		return false
	}
	return frozen.SizeBytes != nil && *actual.SizeBytes == *frozen.SizeBytes && validSHA256(frozen.SHA256) && actual.SHA256 == frozen.SHA256
}

func decodeReplacementPreparation(entries []json.RawMessage, guard PublicationDeliveryGuard, exposure publicationExposureJournalRecord) ([3]PublicationPreparationRecord, error) {
	var records [3]PublicationPreparationRecord
	count := 0
	for _, raw := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &header) != nil || header.Kind != "publication_preparation" {
			continue
		}
		if count == len(records) || decodeStrictJSON(raw, &records[count]) != nil || !validPreparationJournalRecord(records[count]) {
			return records, errors.New("publication preparation journal is malformed")
		}
		count++
	}
	if count != len(records) || records[0].Step != "intent" || records[1].Step != "directory_created" || records[2].Step != "input_ready" || records[0].OperationID != guard.OperationID || records[0].ClaimUUID != guard.ClaimUUID || records[0].OwnerToken != exposure.Intent.PrepOwner || records[0].OutputRel != exposure.Intent.StagedPath || records[0].PrivateDirRel == "" || !strings.HasPrefix(records[0].InputRel, records[0].PrivateDirRel+"/") || records[0].PrivateDirRel != records[1].PrivateDirRel || records[1].PrivateDirRel != records[2].PrivateDirRel || records[0].InputRel != records[1].InputRel || records[1].InputRel != records[2].InputRel || records[0].OutputRel != records[1].OutputRel || records[1].OutputRel != records[2].OutputRel || records[2].CopiedInput == nil || !validCopiedInputIdentity(records[2].CopiedInput) {
		return records, errors.New("publication preparation does not authorize replacement backup")
	}
	return records, nil
}

// replacementJournalFrontier prevents replay from treating arbitrary prior
// journal entries as harmless while locating the replacement evidence.
func replacementJournalFrontier(entries []json.RawMessage) bool {
	if len(entries) < 6 || len(entries) > 10 {
		return false
	}
	want := []string{"publication_preparation", "publication_preparation", "publication_preparation", "stage_observation", "stage_observation", "publication_exposure"}
	for i, raw := range entries[:6] {
		var h struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &h) != nil || h.Kind != want[i] {
			return false
		}
	}
	for _, raw := range entries[6:] {
		var evidence PublicationReplacementEvidence
		if decodeStrictJSON(raw, &evidence) != nil || !validPublicationReplacementEvidence(evidence) {
			return false
		}
	}
	return true
}

func validReplacementSuccessor(prior, next *PublicationReplacementEvidence) bool {
	if prior == nil {
		return next.From == "exposing" && next.To == "backup_intent"
	}
	identityMatches := prior != nil && (next.To == "installed" || samePublicationExposureJSON(prior.Identity, next.Identity))
	return prior.Guard == next.Guard && prior.PositionID == next.PositionID && prior.Epoch == next.Epoch && prior.CandidateGeneration == next.CandidateGeneration && sameOptionalUUID(prior.ExpectedGeneration, next.ExpectedGeneration) && prior.Intent == next.Intent && samePublicationStageObservation(prior.Staged, next.Staged) && identityMatches && samePublicationExposureJSON(prior.BackupIdentity, next.BackupIdentity) && prior.BackupPath == next.BackupPath && prior.To == next.From && ((prior.To == "backup_intent" && next.To == "backup_ready") || (prior.To == "backup_ready" && next.To == "replacement_intent") || (prior.To == "replacement_intent" && next.To == "installed"))
}
