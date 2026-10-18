package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// BeginManualLyricsExposure records bounded admission for the supported V3
// manual-lyrics FLAC rewrite. It performs no filesystem work and does not
// authorize resuming filesystem work after a replay.
func (repository *SetupManagerRepository) BeginManualLyricsExposure(ctx context.Context, guard PublicationDeliveryGuard, intent PublicationExposureIntent, observation PublicationStageObservation) (PublicationExposureOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationExposureStale, err
	}
	if !validExposureIntent(intent) || !validPublicationStageObservation(observation) {
		return PublicationExposureStale, errors.New("invalid manual lyrics exposure evidence")
	}
	result := PublicationExposureStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "begin manual lyrics exposure")
		if err != nil || !live {
			return err
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || !validManualLyricsFLACRewrite(input) || attempt.IntentKind != "rewrite" ||
			input.IntentKind != "rewrite" || input.Rewrite == nil || attempt.ExpectedGeneration == nil ||
			input.Rewrite.CurrentGeneration != *attempt.ExpectedGeneration || intent.Expectation != "expected_current" ||
			intent.TargetRelativePath != input.Rewrite.RelativePath ||
			intent.StagedPath == intent.TargetRelativePath {
			return nil
		}
		if attempt.Phase == "exposing" {
			if operation.Stage != "exposing" {
				return nil
			}
			if err := validateExposureNamespace(ctx, tx, attempt, intent); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return err
			}
			var current TrackPublication
			if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("begin manual lyrics exposure: read current publication: %w", err)
			}
			if !publicationRewriteMatchesCurrent(current, attempt.Epoch, *input.Rewrite) || current.Generation != *attempt.ExpectedGeneration || current.RelativePath != intent.TargetRelativePath {
				return nil
			}
			currentSnapshot, err := json.Marshal(current)
			if err != nil {
				return fmt.Errorf("begin manual lyrics exposure: encode current publication: %w", err)
			}
			out, replayed := manualLyricsExposureReplay(attempt, guard, intent, observation, input, currentSnapshot)
			if replayed {
				result = out
			}
			return nil
		}
		if attempt.Phase != "validated" || operation.Stage != "validated" ||
			!stageMatchesIntent(observation, intent) || !validManualLyricsStageJournal(attempt.MutationJournal, guard, attempt, input) {
			return nil
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return nil
		}
		baseEnd := manualLyricsStageBaseEnd(entries)
		if len(entries) != baseEnd+2 {
			return nil
		}
		var outcome publicationOptionalLyricsOutcomeJournalRecord
		if decodeStrictJSON(entries[baseEnd-1], &outcome) != nil || !samePublicationStageObservation(outcome.FinalObservation, observation) {
			return nil
		}
		if err := validateExposureNamespace(ctx, tx, attempt, intent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("begin manual lyrics exposure: read current publication: %w", err)
		}
		if !publicationRewriteMatchesCurrent(current, attempt.Epoch, *input.Rewrite) || current.Generation != *attempt.ExpectedGeneration || current.RelativePath != intent.TargetRelativePath {
			return nil
		}
		currentSnapshot, err := json.Marshal(current)
		if err != nil {
			return fmt.Errorf("begin manual lyrics exposure: encode current publication: %w", err)
		}
		if !preparationOwnsIntent(attempt.MutationJournal, guard, intent, observation) {
			return nil
		}
		record := publicationExposureJournalRecord{
			Version: PublicationExposureVersion, Kind: "publication_exposure", From: "validated", To: "exposing",
			Guard: guard, PositionID: attempt.PositionID, Epoch: attempt.Epoch,
			CandidateGeneration: attempt.CandidateGeneration, ExpectedGeneration: attempt.ExpectedGeneration,
			Intent: intent, Current: currentSnapshot, Observation: outcome.FinalObservation,
		}
		journal, err := appendPublicationExposureJournal(attempt.MutationJournal, record)
		if err != nil {
			return err
		}
		attemptResult, err := tx.NewRaw(`UPDATE track_publication_attempt SET phase='exposing', mutation_journal=?::jsonb WHERE operation_id=? AND phase='validated'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("begin manual lyrics exposure: update attempt: %w", err)
		}
		attemptRows, err := attemptResult.RowsAffected()
		if err != nil || attemptRows != 1 {
			return errors.New("begin manual lyrics exposure: attempt changed concurrently")
		}
		operationResult, err := tx.NewRaw(`UPDATE operation SET stage='exposing', updated_at=now() WHERE id=? AND state='running' AND stage='validated'`, guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("begin manual lyrics exposure: update operation: %w", err)
		}
		operationRows, err := operationResult.RowsAffected()
		if err != nil || operationRows != 1 {
			return errors.New("begin manual lyrics exposure: operation changed concurrently")
		}
		result = PublicationExposureRecorded
		return nil
	})
	return result, err
}

// manualLyricsExposureReplay accepts only the complete canonical validated
// stage prefix followed by one matching final exposure record.
func manualLyricsExposureReplay(attempt *TrackPublicationAttempt, guard PublicationDeliveryGuard, intent PublicationExposureIntent, observation PublicationStageObservation, input PublicationInputs, currentSnapshot json.RawMessage) (PublicationExposureOutcome, bool) {
	entries, err := decodeJournal(attempt.MutationJournal)
	if err != nil || (len(entries) != 7 && len(entries) != 9) {
		return PublicationExposureStale, false
	}
	baseEnd := manualLyricsStageBaseEnd(entries)
	if len(entries) != baseEnd+3 || !validManualLyricsValidatedStagePrefix(entries[:len(entries)-1], guard, attempt, input) {
		return PublicationExposureStale, false
	}
	stagePrefix, err := json.Marshal(entries[:len(entries)-1])
	if err != nil || !validManualLyricsStageJournal(stagePrefix, guard, attempt, input) {
		return PublicationExposureStale, false
	}
	var outcome publicationOptionalLyricsOutcomeJournalRecord
	if decodeStrictJSON(entries[baseEnd-1], &outcome) != nil {
		return PublicationExposureStale, false
	}
	var record publicationExposureJournalRecord
	if decodeStrictJSON(entries[len(entries)-1], &record) != nil || record.Version != PublicationExposureVersion ||
		record.Kind != "publication_exposure" || record.From != "validated" || record.To != "exposing" ||
		record.Guard != guard || record.PositionID != attempt.PositionID || record.Epoch != attempt.Epoch ||
		record.CandidateGeneration != attempt.CandidateGeneration || !sameOptionalUUID(record.ExpectedGeneration, attempt.ExpectedGeneration) ||
		record.Intent != intent || !samePublicationStageObservation(record.Observation, outcome.FinalObservation) ||
		!samePublicationStageObservation(record.Observation, observation) || !stageMatchesIntent(observation, intent) ||
		!preparationOwnsIntent(attempt.MutationJournal, guard, intent, observation) ||
		!sameJSONValue(record.Current, currentSnapshot) ||
		record.Current == nil || !validManualLyricsFLACRewrite(input) || attempt.ExpectedGeneration == nil ||
		input.Rewrite == nil || input.Rewrite.CurrentGeneration != *attempt.ExpectedGeneration || intent.Expectation != "expected_current" ||
		intent.TargetRelativePath != input.Rewrite.RelativePath {
		return PublicationExposureStale, false
	}
	return PublicationExposureAlreadyRecorded, true
}
