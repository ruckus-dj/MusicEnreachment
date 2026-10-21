package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// This transaction path consumes already-recorded canonical evidence. It does
// not enable V3 preparation, stage mutation, exposure, workers, or API routes.
func commitManualLyricsReplacementTx(ctx context.Context, tx bun.Tx, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, current TrackPublication, input PublicationInputs) (bool, error) {
	if input.ManualLyrics == nil || input.Rewrite == nil || attempt.ExpectedGeneration == nil || !publicationRewriteMatchesCurrent(current, attempt.Epoch, *input.Rewrite) {
		return false, nil
	}
	entries, err := decodeJournal(attempt.MutationJournal)
	if err != nil || (len(entries) != 11 && len(entries) != 13) {
		return false, nil
	}
	outcomeIndex := 3
	if len(entries) == 13 {
		outcomeIndex = 5
	}
	var outcome publicationOptionalLyricsOutcomeJournalRecord
	var installed PublicationReplacementEvidence
	if decodeStrictJSON(entries[outcomeIndex], &outcome) != nil || decodeStrictJSON(entries[len(entries)-1], &installed) != nil || !validOptionalLyricsOutcomeRecord(outcome, guard, input) {
		return false, nil
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	projection := current
	projection.Generation = attempt.CandidateGeneration
	projection.InitialLyrics = nil
	projection.Ownership = encodePublicationCommitJSON(PublicationCurrentOwnership{Version: 1, Identity: installed.Identity})
	projection.ActualMetadata = append(json.RawMessage(nil), outcome.FinalObservation.ActualMetadata...)
	projection.ActualAssets = append(json.RawMessage(nil), outcome.FinalObservation.ActualAssets...)
	projection.ValidatedAt = now
	receipt := &manualLyricsCommitReceipt{
		PositionID: attempt.PositionID, Epoch: attempt.Epoch,
		DesiredRevision:     input.ManualLyrics.ExpectedMetadataRevision,
		ConfirmedGeneration: *attempt.ExpectedGeneration, PubGeneration: attempt.CandidateGeneration,
		DesiredState: input.ManualLyrics.State, RecordedAt: now,
	}
	if receipt.DesiredState == string(ManualLyricsValue) {
		digest := sha256.Sum256([]byte(input.ManualLyrics.Value))
		hash := hex.EncodeToString(digest[:])
		receipt.DesiredSHA256 = &hash
	}
	if outcome.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics {
		warning := manualLyricsReadbackMismatchWarning
		receipt.WarningCode = &warning
	}
	witness := replacementPublicationCommitWitness{
		Version: replacementManualLyricsCommitVersion, Kind: "publication_replacement_commit", Guard: guard,
		PositionID: attempt.PositionID, Epoch: attempt.Epoch, ExpectedGeneration: *attempt.ExpectedGeneration,
		CandidateGeneration: attempt.CandidateGeneration, Projection: projection, ManualLyricsReceipt: receipt,
	}
	journal, err := appendJournalEntry(attempt.MutationJournal, witness)
	if err != nil {
		return false, err
	}
	complete, err := decodeJournal(journal)
	if err != nil {
		return false, err
	}
	prefix, valid := parseCanonicalManualLyricsCommitPrefix(complete, guard, attempt)
	if !valid || !sameJSONValue(prefix.Exposure.Current, encodePublicationCommitJSON(current)) {
		return false, nil
	}
	if err := validateExposureNamespace(ctx, tx, attempt, prefix.Exposure.Intent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	result, err := tx.NewRaw(`UPDATE track_publication SET generation=?, ownership=?::jsonb, actual_metadata=?::jsonb, actual_assets=?::jsonb, initial_lyrics=NULL, validated_at=? WHERE position_id=? AND epoch=? AND generation=? AND relative_path=? AND ownership=?::jsonb AND actual_audio=?::jsonb AND actual_metadata=?::jsonb AND actual_assets=?::jsonb AND quality_policy_version=? AND validated_at=?`,
		projection.Generation, string(projection.Ownership), string(projection.ActualMetadata), string(projection.ActualAssets), now,
		current.PositionID, current.Epoch, current.Generation, current.RelativePath, string(current.Ownership), string(current.ActualAudio), string(current.ActualMetadata), string(current.ActualAssets), current.QualityPolicyVersion, current.ValidatedAt).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("commit manual lyrics replacement: update current: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return false, errors.New("commit manual lyrics replacement: current projection changed")
	}
	if _, err := tx.NewRaw(`INSERT INTO publication_manual_lyrics_receipt(position_id,epoch,desired_revision,confirmed_generation,published_generation,desired_state,desired_sha256,warning_code,recorded_at)
		VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(position_id) DO UPDATE SET epoch=EXCLUDED.epoch, desired_revision=EXCLUDED.desired_revision, confirmed_generation=EXCLUDED.confirmed_generation, published_generation=EXCLUDED.published_generation, desired_state=EXCLUDED.desired_state, desired_sha256=EXCLUDED.desired_sha256, warning_code=EXCLUDED.warning_code, recorded_at=EXCLUDED.recorded_at`,
		receipt.PositionID, receipt.Epoch, receipt.DesiredRevision, receipt.ConfirmedGeneration, receipt.PubGeneration, receipt.DesiredState, receipt.DesiredSHA256, receipt.WarningCode, receipt.RecordedAt).Exec(ctx); err != nil {
		return false, fmt.Errorf("commit manual lyrics replacement: write receipt: %w", err)
	}
	result, err = tx.NewRaw(`UPDATE track_publication_attempt SET phase='committed', frozen_inputs=NULL, mutation_journal=?::jsonb WHERE operation_id=? AND phase='exposing'`, string(journal), guard.OperationID).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("commit manual lyrics replacement: update attempt: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return false, errors.New("commit manual lyrics replacement: attempt changed")
	}
	result, err = tx.NewRaw(`UPDATE operation SET stage='committed', updated_at=? WHERE id=? AND state='running' AND stage='exposing'`, now, guard.OperationID).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("commit manual lyrics replacement: update operation: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return false, errors.New("commit manual lyrics replacement: operation changed")
	}
	return true, nil
}

func manualLyricsReceiptMatchesTx(ctx context.Context, tx bun.Tx, expected *manualLyricsCommitReceipt) (bool, error) {
	if expected == nil {
		return false, nil
	}
	var actual PublicationManualLyricsReceipt
	if err := tx.NewSelect().Model(&actual).Where("position_id=?", expected.PositionID).For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("replay manual lyrics replacement: read receipt: %w", err)
	}
	return actual.PositionID == expected.PositionID && actual.Epoch == expected.Epoch && actual.DesiredRevision == expected.DesiredRevision &&
		actual.ConfirmedGeneration == expected.ConfirmedGeneration && actual.PublishedGeneration == expected.PubGeneration &&
		string(actual.DesiredState) == expected.DesiredState && sameOptionalString(actual.DesiredSHA256, expected.DesiredSHA256) &&
		sameOptionalString(actual.WarningCode, expected.WarningCode) && actual.RecordedAt.Equal(expected.RecordedAt), nil
}
