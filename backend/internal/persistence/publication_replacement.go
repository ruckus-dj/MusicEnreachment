package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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
		if err != nil || !live || attempt.Phase != "exposing" || operation.Stage != "exposing" || attempt.IntentKind != "rewrite" || evidence.PositionID != attempt.PositionID || evidence.Epoch != attempt.Epoch || evidence.CandidateGeneration != attempt.CandidateGeneration || !sameOptionalUUID(evidence.ExpectedGeneration, attempt.ExpectedGeneration) || attempt.ExpectedGeneration == nil || evidence.Intent.Expectation != "expected_current" || evidence.Intent.TargetRelativePath == "" {
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

func validPublicationReplacementEvidence(e PublicationReplacementEvidence) bool {
	if e.Version != 1 || e.Kind != "publication_replacement" || e.Guard.OperationID == uuid.Nil || validatePublicationDeliveryGuard(e.Guard) != nil || e.PositionID == uuid.Nil || e.Epoch == uuid.Nil || e.CandidateGeneration == uuid.Nil || e.ExpectedGeneration == nil || *e.ExpectedGeneration == uuid.Nil || e.PositionID != e.Guard.ExpectedPositionID || e.Epoch != e.Guard.Epoch || e.CandidateGeneration != e.Guard.CandidateGeneration || !validExposureIntent(e.Intent) || e.Intent.Expectation != "expected_current" || !validPublicationStageObservation(e.Staged) || !stageMatchesIntent(e.Staged, e.Intent) || e.Staged.Ownership.ExpectedSize == nil || e.Staged.Ownership.ExpectedMTime == nil || e.Identity.SizeBytes == nil || *e.Identity.SizeBytes < 0 || e.Identity.MTime == nil || !validSHA256(e.Identity.SHA256) {
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

func validReplacementSuccessor(prior, next *PublicationReplacementEvidence) bool {
	if prior == nil {
		return next.From == "exposing" && next.To == "backup_intent"
	}
	identityMatches := prior != nil && (next.To == "installed" || samePublicationExposureJSON(prior.Identity, next.Identity))
	return prior.Guard == next.Guard && prior.PositionID == next.PositionID && prior.Epoch == next.Epoch && prior.CandidateGeneration == next.CandidateGeneration && sameOptionalUUID(prior.ExpectedGeneration, next.ExpectedGeneration) && prior.Intent == next.Intent && samePublicationStageObservation(prior.Staged, next.Staged) && identityMatches && prior.BackupPath == next.BackupPath && prior.To == next.From && ((prior.To == "backup_intent" && next.To == "backup_ready") || (prior.To == "backup_ready" && next.To == "replacement_intent") || (prior.To == "replacement_intent" && next.To == "installed"))
}
