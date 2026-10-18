package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const PublicationCandidateRebuildVersion = 1

// PublicationCandidateRebuildRecord journals a live-only native reconstruction.
// It is evidence of an attempted mutation, never authority to replay it.
type PublicationCandidateRebuildRecord struct {
	Version           int                          `json:"version"`
	Kind              string                       `json:"kind"`
	Step              string                       `json:"step"`
	Guard             PublicationDeliveryGuard     `json:"guard"`
	OwnerToken        uuid.UUID                    `json:"owner_token"`
	PrivateDirRel     string                       `json:"private_dir_rel"`
	InputRel          string                       `json:"input_rel"`
	OutputRel         string                       `json:"output_rel"`
	DirectoryIdentity PublicationDirectoryIdentity `json:"directory_identity"`
	Candidate         *PublicationFileIdentity     `json:"candidate,omitempty"`
	Input             *PublicationFileIdentity     `json:"input,omitempty"`
}

// RecordPublicationCandidateRebuild appends intent/ack under the live preparing
// owner guard. Callers must not interpret AlreadyRecorded as mutation authority.
func (repository *SetupManagerRepository) RecordPublicationCandidateRebuild(ctx context.Context, guard PublicationDeliveryGuard, record PublicationCandidateRebuildRecord) error {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return err
	}
	if !validCandidateRebuildRecord(record, guard) {
		return errors.New("invalid publication candidate rebuild record")
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "record publication candidate rebuild")
		if err != nil {
			return err
		}
		if !live {
			return errors.New("publication candidate rebuild claim was lost")
		}
		if attempt.Phase != "preparing" || operation.Stage != "preparing" {
			return errors.New("publication candidate rebuild owner is not preparing")
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		var intent *PublicationCandidateRebuildRecord
		complete := false
		for _, entry := range entries {
			var header struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal(entry, &header) != nil {
				return errors.New("publication candidate rebuild journal is malformed")
			}
			if header.Kind == "publication_candidate_rebuild" {
				var prior PublicationCandidateRebuildRecord
				if decodeStrictJSON(entry, &prior) != nil || !validCandidateRebuildRecord(prior, guard) {
					return errors.New("publication candidate rebuild journal is malformed")
				}
				if prior.Step == "intent" {
					if intent != nil {
						return errors.New("publication candidate rebuild journal has duplicate intent")
					}
					intent = &prior
				} else {
					if complete || intent == nil || !sameCandidateRebuildPlan(*intent, prior) {
						return errors.New("publication candidate rebuild journal has invalid completion")
					}
					complete = true
				}
			}
		}
		if record.Step == "intent" && intent != nil || record.Step == "complete" && (intent == nil || complete || !sameCandidateRebuildPlan(*intent, record)) {
			return errors.New("publication candidate rebuild journal already contains a rebuild")
		}
		updated, err := appendJournalEntry(attempt.MutationJournal, record)
		if err != nil {
			return fmt.Errorf("append publication candidate rebuild journal: %w", err)
		}
		result, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='preparing'`, string(updated), guard.OperationID).Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("publication candidate rebuild owner changed concurrently")
		}
		return nil
	})
}

func sameCandidateRebuildPlan(left, right PublicationCandidateRebuildRecord) bool {
	return left.Version == right.Version && left.Kind == right.Kind && left.Guard == right.Guard && left.OwnerToken == right.OwnerToken && left.PrivateDirRel == right.PrivateDirRel && left.InputRel == right.InputRel && left.OutputRel == right.OutputRel && samePublicationExposureJSON(left.DirectoryIdentity, right.DirectoryIdentity) && left.Input != nil && right.Input != nil && samePublicationExposureJSON(left.Input, right.Input)
}

func validCandidateRebuildRecord(record PublicationCandidateRebuildRecord, guard PublicationDeliveryGuard) bool {
	if record.Version != PublicationCandidateRebuildVersion || record.Kind != "publication_candidate_rebuild" || record.Guard != guard || record.OwnerToken == uuid.Nil || !portablePreparationPath(record.PrivateDirRel) || !portablePreparationPath(record.InputRel) || !portablePreparationPath(record.OutputRel) || !validPreparationPlatformIdentity(record.DirectoryIdentity) {
		return false
	}
	if record.InputRel == record.OutputRel || !strings.HasPrefix(record.InputRel, record.PrivateDirRel+"/") || !strings.HasPrefix(record.OutputRel, record.PrivateDirRel+"/") {
		return false
	}
	switch record.Step {
	case "intent":
		return record.Candidate != nil && record.Input != nil
	case "complete":
		return record.Candidate != nil && record.Input != nil && validCopiedInputIdentity(record.Candidate) && validCopiedInputIdentity(record.Input)
	default:
		return false
	}
}
