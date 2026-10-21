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

const PublicationCandidateInstalledVersion = 1

// PublicationCandidateInstalledObservation records an install observation;
// it is journal evidence only and does not authorize filesystem work.
type PublicationCandidateInstalledObservation struct {
	Version            int                     `json:"version"`
	TargetRelativePath string                  `json:"target_relative_path"`
	Identity           PublicationFileIdentity `json:"identity"`
}

type PublicationCandidateInstalledOutcome string

const (
	PublicationCandidateInstalledRecorded        PublicationCandidateInstalledOutcome = "recorded"
	PublicationCandidateInstalledAlreadyRecorded PublicationCandidateInstalledOutcome = "already_recorded"
	PublicationCandidateInstalledStale           PublicationCandidateInstalledOutcome = "stale"
)

type publicationCandidateInstalledJournalRecord struct {
	Version             int                                      `json:"version"`
	Kind                string                                   `json:"kind"`
	From                string                                   `json:"from"`
	To                  string                                   `json:"to"`
	Guard               PublicationDeliveryGuard                 `json:"guard"`
	PositionID          uuid.UUID                                `json:"position_id"`
	Epoch               uuid.UUID                                `json:"epoch"`
	CandidateGeneration uuid.UUID                                `json:"candidate_generation"`
	ExpectedGeneration  *uuid.UUID                               `json:"expected_generation"`
	Intent              PublicationExposureIntent                `json:"intent"`
	Observation         PublicationCandidateInstalledObservation `json:"observation"`
}

// RecordPublicationCandidateInstalled appends the durable observation for an
// installed candidate. It changes neither publication state nor filesystem.
func (repository *SetupManagerRepository) RecordPublicationCandidateInstalled(ctx context.Context, guard PublicationDeliveryGuard, intent PublicationExposureIntent, validatedStage PublicationStageObservation, installedObservation PublicationCandidateInstalledObservation) (PublicationCandidateInstalledOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationCandidateInstalledStale, err
	}
	if !validExposureIntent(intent) || !validPublicationStageObservation(validatedStage) || !validPublicationCandidateInstalledObservation(installedObservation) {
		return PublicationCandidateInstalledStale, errors.New("invalid installed publication candidate evidence")
	}
	if installedObservation.TargetRelativePath != intent.TargetRelativePath || validatedStage.RelativePath != intent.StagedPath || validatedStage.Ownership.ExpectedSize == nil || validatedStage.Ownership.ExpectedMTime == nil || *validatedStage.Ownership.ExpectedSize != *installedObservation.Identity.SizeBytes || *validatedStage.Ownership.ExpectedMTime != *installedObservation.Identity.MTime || validatedStage.PayloadSHA256 != installedObservation.Identity.SHA256 {
		return PublicationCandidateInstalledStale, nil
	}
	result := PublicationCandidateInstalledStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "record installed publication candidate")
		if err != nil || !live {
			return err
		}
		if attempt.Phase != "exposing" || operation.Stage != "exposing" || attempt.ExpectedGeneration != nil || intent.Expectation != "absent" {
			return nil
		}
		if attempt.IntentKind == "audio" {
			input, decodeErr := DecodePublicationInputs(attempt.FrozenInputs)
			if decodeErr != nil || input.IntentKind != "audio" {
				return nil
			}
			entries, journalErr := decodeJournal(attempt.MutationJournal)
			if journalErr != nil {
				return journalErr
			}
			state, evidenceErr := firstPublicationInitialLyricsEvidence(entries, guard, input)
			if evidenceErr != nil || !samePublicationStageObservation(state.outcome.FinalObservation, validatedStage) {
				return nil
			}
		}
		var current TrackPublication
		err = tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("record installed publication candidate: read current publication: %w", err)
		}
		exposure, count, err := matchingPublicationExposure(attempt.MutationJournal, guard, attempt, intent, validatedStage)
		if err != nil {
			return err
		}
		if count != 1 || exposure.Current == nil || string(exposure.Current) != "null" {
			return nil
		}
		if exposure.Guard != guard || exposure.PositionID != attempt.PositionID || exposure.Epoch != attempt.Epoch || exposure.CandidateGeneration != attempt.CandidateGeneration || !sameOptionalUUID(exposure.ExpectedGeneration, attempt.ExpectedGeneration) || exposure.Intent.Expectation != "absent" || exposure.ExpectedGeneration != nil || !samePublicationExposureJSON(exposure.Intent, intent) || !samePublicationStageObservation(exposure.Observation, validatedStage) {
			return nil
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		var previous *publicationCandidateInstalledJournalRecord
		for _, raw := range entries {
			var header struct {
				Kind string `json:"kind"`
			}
			_ = json.Unmarshal(raw, &header)
			if header.Kind == "publication_candidate_installed" {
				var existing publicationCandidateInstalledJournalRecord
				if decodeStrictJSON(raw, &existing) != nil || !validPublicationCandidateInstalledRecord(existing) {
					return errors.New("publication candidate-installed journal is malformed")
				}
				if existing.Guard != guard || existing.PositionID != attempt.PositionID || existing.Epoch != attempt.Epoch || existing.CandidateGeneration != attempt.CandidateGeneration || !sameOptionalUUID(existing.ExpectedGeneration, attempt.ExpectedGeneration) || existing.Intent.Expectation != "absent" || existing.ExpectedGeneration != nil || existing.Observation.TargetRelativePath != existing.Intent.TargetRelativePath {
					return errors.New("publication candidate-installed journal is inconsistent")
				}
				if previous != nil {
					return nil
				}
				copy := existing
				previous = &copy
			}
		}
		if previous != nil {
			if samePublicationExposureJSON(previous.Intent, intent) && samePublicationExposureJSON(previous.Observation, installedObservation) {
				result = PublicationCandidateInstalledAlreadyRecorded
			}
			return nil
		}
		record := publicationCandidateInstalledJournalRecord{Version: PublicationCandidateInstalledVersion, Kind: "publication_candidate_installed", From: "exposing", To: "exposing", Guard: guard, PositionID: attempt.PositionID, Epoch: attempt.Epoch, CandidateGeneration: attempt.CandidateGeneration, ExpectedGeneration: attempt.ExpectedGeneration, Intent: intent, Observation: installedObservation}
		journal, err := appendJournalEntry(attempt.MutationJournal, record)
		if err != nil {
			return err
		}
		updated, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='exposing'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record installed publication candidate: %w", err)
		}
		rows, err := updated.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("record installed publication candidate: attempt changed concurrently")
		}
		result = PublicationCandidateInstalledRecorded
		return nil
	})
	if err != nil {
		return PublicationCandidateInstalledStale, err
	}
	return result, nil
}

func validPublicationCandidateInstalledObservation(o PublicationCandidateInstalledObservation) bool {
	return o.Version == PublicationCandidateInstalledVersion && portableRelativePath(o.TargetRelativePath) && o.Identity.SizeBytes != nil && *o.Identity.SizeBytes >= 0 && o.Identity.MTime != nil && validSHA256(o.Identity.SHA256)
}

func validPublicationCandidateInstalledRecord(r publicationCandidateInstalledJournalRecord) bool {
	return r.Version == PublicationCandidateInstalledVersion && r.Kind == "publication_candidate_installed" && r.From == "exposing" && r.To == "exposing" &&
		validatePublicationDeliveryGuard(r.Guard) == nil && r.PositionID != uuid.Nil && r.Epoch != uuid.Nil && r.CandidateGeneration != uuid.Nil &&
		r.PositionID == r.Guard.ExpectedPositionID && r.Epoch == r.Guard.Epoch && r.CandidateGeneration == r.Guard.CandidateGeneration &&
		(r.ExpectedGeneration == nil || *r.ExpectedGeneration != uuid.Nil) && validExposureIntent(r.Intent) && validPublicationCandidateInstalledObservation(r.Observation)
}

func matchingPublicationExposure(raw json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, intent PublicationExposureIntent, staged PublicationStageObservation) (publicationExposureJournalRecord, int, error) {
	entries, err := decodeJournal(raw)
	if err != nil {
		return publicationExposureJournalRecord{}, 0, errors.New("publication journal is malformed")
	}
	var match publicationExposureJournalRecord
	count := 0
	for _, entry := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(entry, &header) != nil || header.Kind != "publication_exposure" {
			continue
		}
		var record publicationExposureJournalRecord
		if decodeStrictJSON(entry, &record) != nil || record.Version != PublicationExposureVersion || record.Kind != "publication_exposure" || record.From != "validated" || record.To != "exposing" ||
			validatePublicationDeliveryGuard(record.Guard) != nil || record.PositionID == uuid.Nil || record.Epoch == uuid.Nil || record.CandidateGeneration == uuid.Nil ||
			record.PositionID != record.Guard.ExpectedPositionID || record.Epoch != record.Guard.Epoch || record.CandidateGeneration != record.Guard.CandidateGeneration ||
			record.ExpectedGeneration != nil || record.Intent.Expectation != "absent" || record.Current == nil || string(record.Current) != "null" ||
			!validExposureIntent(record.Intent) || !validPublicationStageObservation(record.Observation) || !stageMatchesIntent(record.Observation, record.Intent) {
			return publicationExposureJournalRecord{}, 0, errors.New("publication exposure journal is malformed")
		}
		count++
		match = record
	}
	return match, count, nil
}

func decodeJournal(raw json.RawMessage) ([]json.RawMessage, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
		return nil, errors.New("journal is not an array")
	}
	return entries, nil
}

func appendJournalEntry(raw json.RawMessage, record any) (json.RawMessage, error) {
	entries, err := decodeJournal(raw)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	entries = append(entries, encoded)
	return json.Marshal(entries)
}
