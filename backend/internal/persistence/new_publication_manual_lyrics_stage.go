package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// ManualLyricsStageOutcome reports whether a guarded private V3 stage advanced,
// was an exact idempotent replay, or no longer belongs to this delivery.
type ManualLyricsStageOutcome string

const (
	ManualLyricsStageAdvanced        ManualLyricsStageOutcome = "advanced"
	ManualLyricsStageAlreadyRecorded ManualLyricsStageOutcome = "already_recorded"
	ManualLyricsStageStale           ManualLyricsStageOutcome = "stale"
)

// GuardedAdvanceManualLyricsStage records only the private V3 rewrite stage.
// It does not expose or commit the artifact.
func (repository *SetupManagerRepository) GuardedAdvanceManualLyricsStage(ctx context.Context, guard PublicationDeliveryGuard, from string, observation PublicationStageObservation) (ManualLyricsStageOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return ManualLyricsStageStale, err
	}
	if from != "preparing" && from != "staged" || !validPublicationStageObservation(observation) {
		return ManualLyricsStageStale, errors.New("invalid manual lyrics stage transition")
	}
	to := "staged"
	if from == "staged" {
		to = "validated"
	}
	outcome := ManualLyricsStageStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "advance manual lyrics stage")
		if err != nil || !live {
			return err
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("advance manual lyrics stage: decode frozen inputs: %w", err)
		}
		if err := input.Validate(); err != nil || !validManualLyricsFLACRewrite(input) {
			return errors.New("advance manual lyrics stage: frozen inputs are not a valid manual lyrics FLAC rewrite")
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("advance manual lyrics stage: read current publication: %w", err)
		}
		if !publicationRewriteMatchesCurrent(current, attempt.Epoch, *input.Rewrite) {
			return nil
		}
		if err := validatePublicationRewriteNamespace(ctx, tx, attempt, input.Rewrite.RelativePath); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("advance manual lyrics stage: validate namespace: %w", err)
		}
		if !validManualLyricsStageJournal(attempt.MutationJournal, guard, attempt, input) {
			return errors.New("advance manual lyrics stage: invalid V3 journal prefix")
		}
		entries, _ := decodeJournal(attempt.MutationJournal)
		baseEnd := manualLyricsStageBaseEnd(entries)
		wantCount := 1
		if to == "validated" {
			wantCount = 2
		}
		if attempt.Phase == "staged" && operation.Stage == "staged" && from == "preparing" || attempt.Phase == "validated" && operation.Stage == "validated" && from == "staged" {
			if len(entries) != baseEnd+wantCount || !manualLyricsStageRecordMatches(entries[len(entries)-1], from, to, observation) {
				return nil
			}
			outcome = ManualLyricsStageAlreadyRecorded
			return nil
		}
		if attempt.Phase != from || operation.Stage != from {
			return nil
		}
		// Caller-supplied observations are claims. The outcome journal is the
		// durable authority for the actual final private-file readback.
		base := entries[:baseEnd]
		var recorded publicationOptionalLyricsOutcomeJournalRecord
		if len(base) == 0 || decodeStrictJSON(base[len(base)-1], &recorded) != nil ||
			!validOptionalLyricsOutcomeRecord(recorded, guard, input) ||
			!samePublicationStageObservation(observation, recorded.FinalObservation) {
			return nil
		}
		stored := recorded.FinalObservation.Clone()
		if from == "preparing" && len(entries) != baseEnd {
			return nil
		}
		if from == "staged" && (len(entries) != baseEnd+1 || !manualLyricsStageRecordMatches(entries[baseEnd], "preparing", "staged", stored)) {
			return nil
		}
		journal, err := appendPublicationStageJournal(attempt.MutationJournal, from, to, stored)
		if err != nil {
			return fmt.Errorf("advance manual lyrics stage: append journal: %w", err)
		}
		attemptResult, err := tx.NewRaw(`UPDATE track_publication_attempt SET phase=?, mutation_journal=?::jsonb WHERE operation_id=? AND phase=?`, to, string(journal), guard.OperationID, from).Exec(ctx)
		if err != nil {
			return fmt.Errorf("advance manual lyrics stage: update attempt: %w", err)
		}
		if n, err := attemptResult.RowsAffected(); err != nil || n != 1 {
			return errors.New("advance manual lyrics stage: attempt changed concurrently")
		}
		operationResult, err := tx.NewRaw(`UPDATE operation SET stage=?, updated_at=now() WHERE id=? AND state='running' AND stage=?`, to, operation.ID, from).Exec(ctx)
		if err != nil {
			return fmt.Errorf("advance manual lyrics stage: update operation: %w", err)
		}
		if n, err := operationResult.RowsAffected(); err != nil || n != 1 {
			return errors.New("advance manual lyrics stage: operation changed concurrently")
		}
		outcome = ManualLyricsStageAdvanced
		return nil
	})
	return outcome, err
}

func manualLyricsStageBaseEnd(entries []json.RawMessage) int {
	if len(entries) >= 5 {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(entries[3], &header) == nil && header.Kind == "publication_candidate_rebuild" {
			return 6
		}
	}
	return 4
}

func validManualLyricsStageJournal(raw json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, input PublicationInputs) bool {
	entries, err := decodeJournal(raw)
	if err != nil || len(entries) < 4 || len(entries) > 8 {
		return false
	}
	baseEnd := manualLyricsStageBaseEnd(entries)
	if baseEnd > len(entries) || (baseEnd != 4 && baseEnd != 6) {
		return false
	}
	var outcome publicationOptionalLyricsOutcomeJournalRecord
	if decodeStrictJSON(entries[baseEnd-1], &outcome) != nil || !validOptionalLyricsOutcomeRecord(outcome, guard, input) {
		return false
	}
	baseEvidence := PublicationOptionalLyricsOutcomeEvidence{Kind: outcome.Outcome, ManualLyrics: outcome.ManualLyrics, Preparation: outcome.Preparation, InputIdentity: outcome.InputIdentity, OutputIdentity: outcome.OutputIdentity, FinalObservation: outcome.FinalObservation, Diagnostic: outcome.Diagnostic}
	// Existing parser deliberately sees only the exact preparation/rebuild/
	// outcome prefix; stage transitions remain this method's strict grammar.
	preparations, recorded, ok := parseOptionalLyricsOutcomeJournal(entries[:baseEnd], baseEvidence, guard)
	if !ok || !recorded || !canonicalOptionalLyricsPreparation(preparations, baseEvidence, guard) {
		return false
	}
	if baseEnd == 6 {
		var intent, complete PublicationCandidateRebuildRecord
		if decodeStrictJSON(entries[3], &intent) != nil || decodeStrictJSON(entries[4], &complete) != nil || intent.Step != "intent" || complete.Step != "complete" {
			return false
		}
	}
	transitions := entries[baseEnd:]
	if len(transitions) > 2 {
		return false
	}
	for i, rawEntry := range transitions {
		from, to := "preparing", "staged"
		if i == 1 {
			from, to = "staged", "validated"
		}
		if !manualLyricsStageRecordMatches(rawEntry, from, to, outcome.FinalObservation) {
			return false
		}
	}
	return true
}

func manualLyricsStageRecordMatches(raw json.RawMessage, from, to string, observation PublicationStageObservation) bool {
	var record struct {
		Version     int                         `json:"version"`
		Kind        string                      `json:"kind"`
		From        string                      `json:"from"`
		To          string                      `json:"to"`
		Observation PublicationStageObservation `json:"observation"`
	}
	if decodeStrictJSON(raw, &record) != nil || record.Version != 1 || record.Kind != "stage_observation" || record.From != from || record.To != to || !validPublicationStageObservation(record.Observation) {
		return false
	}
	return samePublicationStageObservation(record.Observation, observation)
}
