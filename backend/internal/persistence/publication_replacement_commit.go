package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const replacementPublicationCommitVersion = 1
const replacementManualLyricsCommitVersion = 2

type replacementPublicationCommitWitness struct {
	Version             int                        `json:"version"`
	Kind                string                     `json:"kind"`
	Guard               PublicationDeliveryGuard   `json:"guard"`
	PositionID          uuid.UUID                  `json:"position_id"`
	Epoch               uuid.UUID                  `json:"epoch"`
	ExpectedGeneration  uuid.UUID                  `json:"expected_generation"`
	CandidateGeneration uuid.UUID                  `json:"candidate_generation"`
	Projection          TrackPublication           `json:"projection"`
	ManualLyricsReceipt *manualLyricsCommitReceipt `json:"manual_lyrics_receipt,omitempty"`
}

type manualLyricsCommitReceipt struct {
	PositionID          uuid.UUID `json:"position_id"`
	Epoch               uuid.UUID `json:"epoch"`
	DesiredRevision     int64     `json:"desired_revision"`
	ConfirmedGeneration uuid.UUID `json:"confirmed_generation"`
	PubGeneration       uuid.UUID `json:"pub_generation"`
	DesiredState        string    `json:"desired_state"`
	DesiredSHA256       *string   `json:"desired_sha256"`
	WarningCode         *string   `json:"warning_code"`
	RecordedAt          time.Time `json:"recorded_at"`
}

// CommitReplacementPublication atomically replaces the current projection after
// an installed same-path rewrite. Filesystem cleanup remains a later operation.
func (repository *SetupManagerRepository) CommitReplacementPublication(ctx context.Context, guard PublicationDeliveryGuard) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	committed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("commit replacement publication: acquire output admission: %w", err)
		}
		if err := lockPublicationSourceCleanupPolicy(ctx, tx, true); err != nil {
			return fmt.Errorf("commit replacement publication: lock source cleanup policy: %w", err)
		}
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "commit replacement publication")
		if err != nil || !live {
			return err
		}
		var current TrackPublication
		currentErr := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx)
		if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
			return fmt.Errorf("commit replacement publication: read current row: %w", currentErr)
		}
		if attempt.Phase == "committed" {
			if attempt.FrozenInputs == nil && claim.State == "executing" && operation.Stage == "committed" && currentErr == nil {
				if entries, err := decodeJournal(attempt.MutationJournal); err == nil {
					if retryCommitReplayMatches(entries, guard, attempt, current) {
						if prefix, _, ok := canonicalAutomaticLyricsRetryCompletionPrefix(entries, guard, attempt); ok {
							err = validateExposureNamespace(ctx, tx, attempt, prefix.exposure.Intent)
							if errors.Is(err, sql.ErrNoRows) {
								err = nil
							} else if err == nil {
								committed = true
							}
						}
						return err
					}
					if prefix, valid := parseCanonicalManualLyricsCommitPrefix(entries, guard, attempt); valid {
						if !currentPublicationMatchesReplacementWitness(current, prefix.Commit.Projection) {
							return nil
						}
						if err := validateExposureNamespace(ctx, tx, attempt, prefix.Exposure.Intent); err != nil {
							if errors.Is(err, sql.ErrNoRows) {
								return nil
							}
							return err
						}
						committed, err = manualLyricsReceiptMatchesTx(ctx, tx, prefix.Commit.ManualLyricsReceipt)
						return err
					}
				}
				if prefix, ok := validateCanonicalReplacementPrefix(attempt.MutationJournal, guard, attempt); ok {
					if err := validateExposureNamespace(ctx, tx, attempt, prefix.exposure.Intent); err != nil {
						if !errors.Is(err, sql.ErrNoRows) {
							return err
						}
					} else if entries, err := decodeJournal(attempt.MutationJournal); err == nil && replacementCommitReplayMatches(entries, guard, attempt, current) {
						committed = true
					}
				}
			}
			return nil
		}
		attempt, operation, _, live, err = lockAndValidatePublicationDelivery(ctx, tx, guard, "commit replacement publication")
		if err != nil || !live {
			return err
		}
		if attempt.Phase != "exposing" || operation.Stage != "exposing" || attempt.IntentKind != "rewrite" || attempt.ExpectedGeneration == nil || currentErr != nil {
			return nil
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err == nil && input.ManualLyrics != nil {
			committed, err = commitManualLyricsReplacementTx(ctx, tx, guard, attempt, current, input)
			if err == nil && committed {
				err = repository.requestSourceCleanupTx(ctx, tx, guard.OperationID, attempt.PositionID, attempt.Epoch, attempt.CandidateGeneration)
			}
			return err
		}
		if err == nil && input.AutomaticLyricsRetry != nil {
			committed, err = commitAutomaticLyricsRetryReplacementTx(ctx, tx, repository, guard, attempt, current, input)
			if err == nil && committed {
				err = repository.requestSourceCleanupTx(ctx, tx, guard.OperationID, attempt.PositionID, attempt.Epoch, attempt.CandidateGeneration)
			}
			return err
		}
		if err != nil || !input.SupportsExecution() || input.IntentKind != "rewrite" || input.Rewrite == nil || input.Rewrite.CurrentGeneration != *attempt.ExpectedGeneration {
			return nil
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return nil
		}
		var exposure publicationExposureJournalRecord
		var replacement []PublicationReplacementEvidence
		var preparation [3]PublicationPreparationRecord
		var stages [2]PublicationStageObservation
		preparationCount, stageCount, exposureCount := 0, 0, 0
		replacementStarted := false
		for _, raw := range entries {
			var h struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal(raw, &h) != nil {
				return nil
			}
			switch h.Kind {
			case "publication_preparation":
				var record PublicationPreparationRecord
				if replacementStarted || exposureCount != 0 || stageCount != 0 || preparationCount >= len(preparation) || decodeStrictJSON(raw, &record) != nil || !validPreparationJournalRecord(record) {
					return nil
				}
				if record.Step != [3]string{"intent", "directory_created", "input_ready"}[preparationCount] {
					return nil
				}
				if preparationCount > 0 && (!samePreparationPlan(preparation[0], record) || record.OwnerToken != preparation[0].OwnerToken || record.ParentIdentity != preparation[0].ParentIdentity || record.PrivateDirRel != preparation[0].PrivateDirRel || record.InputRel != preparation[0].InputRel || record.OutputRel != preparation[0].OutputRel || (preparationCount == 2 && *preparation[1].DirectoryIdentity != *record.DirectoryIdentity)) {
					return nil
				}
				preparation[preparationCount] = record
				preparationCount++
			case "stage_observation":
				var record struct {
					Version     int                         `json:"version"`
					Kind        string                      `json:"kind"`
					From        string                      `json:"from"`
					To          string                      `json:"to"`
					Observation PublicationStageObservation `json:"observation"`
				}
				if replacementStarted || exposureCount != 0 || preparationCount != len(preparation) || stageCount >= len(stages) || decodeStrictJSON(raw, &record) != nil {
					return nil
				}
				stageCount++
				validTransition := stageCount == 1 && record.From == "preparing" && record.To == "staged" || stageCount == 2 && record.From == "staged" && record.To == "validated"
				if record.Version != 1 || record.Kind != "stage_observation" || !validTransition || !validPublicationStageObservation(record.Observation) {
					return nil
				}
				stages[stageCount-1] = record.Observation
			case "publication_exposure":
				exposureCount++
				if replacementStarted || exposureCount != 1 || preparationCount != len(preparation) || stageCount != len(stages) || decodeStrictJSON(raw, &exposure) != nil {
					return nil
				}
			case "publication_candidate_installed":
				// This record belongs to absent-target publication. Mixing it into a
				// rewrite journal would make the installed identity ambiguous.
				return nil
			case "publication_replacement":
				if exposureCount != 1 || len(replacement) >= 4 {
					return nil
				}
				replacementStarted = true
				var e PublicationReplacementEvidence
				if decodeStrictJSON(raw, &e) != nil || !validPublicationReplacementEvidence(e) {
					return nil
				}
				replacement = append(replacement, e)
			default:
				return nil
			}
		}
		if preparationCount != len(preparation) || preparation[0].OwnerToken != exposure.Intent.PrepOwner || preparation[2].CopiedInput == nil || !validCopiedInputIdentity(preparation[2].CopiedInput) || preparation[0].OperationID != guard.OperationID || preparation[0].ClaimUUID != guard.ClaimUUID || preparation[0].PrivateDirRel == "" || preparation[0].OutputRel != exposure.Intent.StagedPath || preparation[0].ParentIdentity != exposure.Intent.OutputRootParent || exposure.Intent.TargetParent.Platform != preparation[0].ParentIdentity.Platform || exposureCount != 1 || exposure.Version != PublicationExposureVersion || exposure.Kind != "publication_exposure" || exposure.From != "validated" || exposure.To != "exposing" || exposure.Guard != guard || exposure.PositionID != attempt.PositionID || exposure.Epoch != attempt.Epoch || exposure.CandidateGeneration != attempt.CandidateGeneration || !sameOptionalUUID(exposure.ExpectedGeneration, attempt.ExpectedGeneration) || exposure.Current == nil || !validExposureIntent(exposure.Intent) || exposure.Intent.Expectation != "expected_current" || !validPublicationStageObservation(exposure.Observation) || !stageMatchesIntent(exposure.Observation, exposure.Intent) || len(exposure.Observation.ActualAssets) == 0 || exposure.Observation.QualityPolicyVersion != input.QualityPolicyVersion || !samePublicationStageObservation(exposure.Observation, stages[1]) || len(replacement) != 4 {
			return nil
		}
		if exposure.Intent.StagedPath != stages[1].RelativePath {
			return nil
		}
		if !samePublicationExposureJSON(input.Rewrite.Ownership, replacement[0].Identity) {
			return nil
		}
		for i, e := range replacement {
			transition := [][2]string{{"exposing", "backup_intent"}, {"backup_intent", "backup_ready"}, {"backup_ready", "replacement_intent"}, {"replacement_intent", "installed"}}[i]
			from, to := transition[0], transition[1]
			if e.From != from || e.To != to || e.Guard != guard || e.PositionID != attempt.PositionID || e.Epoch != attempt.Epoch || e.CandidateGeneration != attempt.CandidateGeneration || e.ExpectedGeneration == nil || *e.ExpectedGeneration != *attempt.ExpectedGeneration || e.Intent != exposure.Intent || !samePublicationStageObservation(e.Staged, exposure.Observation) || e.BackupPath != preparation[0].InputRel {
				return nil
			}
			if i > 0 && !validReplacementSuccessor(&replacement[i-1], &replacement[i]) {
				return nil
			}
		}
		last := replacement[3]
		if last.To != "installed" || last.Identity.SizeBytes == nil || *last.Identity.SizeBytes != *last.Staged.Ownership.ExpectedSize || last.Identity.SHA256 != last.Staged.PayloadSHA256 {
			return nil
		}
		if err := validateExposureNamespace(ctx, tx, attempt, exposure.Intent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if current.Generation != *attempt.ExpectedGeneration || current.Epoch != attempt.Epoch || current.RelativePath != exposure.Intent.TargetRelativePath || !sameJSONValue(exposure.Current, encodePublicationCommitJSON(current)) || !publicationRewriteMatchesCurrent(current, attempt.Epoch, *input.Rewrite) {
			return nil
		}
		var oldAudio PublicationCurrentAudio
		if decodePublicationCurrentContract(current.ActualAudio, &oldAudio) != nil || oldAudio.Version != 1 || oldAudio.Facts.Validate() != nil {
			return nil
		}
		validatedAt := time.Now().UTC().Truncate(time.Microsecond)
		projection := current
		projection.Generation = attempt.CandidateGeneration
		projection.InitialLyrics = nil
		projection.Ownership = encodePublicationCommitJSON(PublicationCurrentOwnership{Version: 1, Identity: last.Identity})
		projection.ActualAudio = encodePublicationCommitJSON(oldAudio)
		projection.ActualMetadata = append(json.RawMessage(nil), exposure.Observation.ActualMetadata...)
		projection.ActualAssets = append(json.RawMessage(nil), exposure.Observation.ActualAssets...)
		projection.ValidatedAt = validatedAt
		if projection.QualityPolicyVersion == 0 {
			return nil
		}
		witness := replacementPublicationCommitWitness{Version: replacementPublicationCommitVersion, Kind: "publication_replacement_commit", Guard: guard, PositionID: attempt.PositionID, Epoch: attempt.Epoch, ExpectedGeneration: *attempt.ExpectedGeneration, CandidateGeneration: attempt.CandidateGeneration, Projection: projection}
		updated, err := tx.NewRaw(`UPDATE track_publication SET epoch=?, generation=?, relative_path=?, ownership=?::jsonb, actual_audio=?::jsonb, actual_metadata=?::jsonb, actual_assets=?::jsonb, initial_lyrics=NULL, quality_policy_version=?, validated_at=? WHERE position_id=? AND epoch=? AND generation=? AND relative_path=? AND ownership=?::jsonb AND actual_audio=?::jsonb AND actual_metadata=?::jsonb AND actual_assets=?::jsonb AND quality_policy_version=? AND validated_at=?`, projection.Epoch, projection.Generation, projection.RelativePath, string(projection.Ownership), string(projection.ActualAudio), string(projection.ActualMetadata), string(projection.ActualAssets), projection.QualityPolicyVersion, validatedAt, current.PositionID, current.Epoch, current.Generation, current.RelativePath, string(current.Ownership), string(current.ActualAudio), string(current.ActualMetadata), string(current.ActualAssets), current.QualityPolicyVersion, current.ValidatedAt).Exec(ctx)
		if err != nil {
			return fmt.Errorf("commit replacement publication: update current row: %w", err)
		}
		n, err := updated.RowsAffected()
		if err != nil || n != 1 {
			return nil
		}
		if err := carryPositionSourceAuthorityTx(ctx, tx, current, projection); err != nil {
			return fmt.Errorf("commit replacement publication: carry source authority: %w", err)
		}
		journal, err := appendJournalEntry(attempt.MutationJournal, witness)
		if err != nil {
			return err
		}
		res, err := tx.NewRaw(`UPDATE track_publication_attempt SET phase='committed', frozen_inputs=NULL, mutation_journal=?::jsonb WHERE operation_id=? AND phase='exposing'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("commit replacement publication: settle attempt: %w", err)
		}
		n, err = res.RowsAffected()
		if err != nil || n != 1 {
			return errors.New("commit replacement publication: attempt changed concurrently")
		}
		res, err = tx.NewRaw(`UPDATE operation SET stage='committed', updated_at=? WHERE id=? AND state='running' AND stage='exposing'`, validatedAt, guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("commit replacement publication: update operation: %w", err)
		}
		n, err = res.RowsAffected()
		if err != nil || n != 1 {
			return errors.New("commit replacement publication: operation changed concurrently")
		}
		if err := repository.requestSourceCleanupTx(ctx, tx, guard.OperationID, attempt.PositionID, attempt.Epoch, attempt.CandidateGeneration); err != nil {
			return fmt.Errorf("commit replacement publication: request source cleanup: %w", err)
		}
		committed = true
		return nil
	})
	return committed, err
}

func commitAutomaticLyricsRetryReplacementTx(ctx context.Context, tx bun.Tx, repository *SetupManagerRepository, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, current TrackPublication, input PublicationInputs) (bool, error) {
	if input.IntentKind != "rewrite" || input.Rewrite == nil || input.AutomaticLyricsRetry == nil || attempt.ExpectedGeneration == nil || input.Rewrite.CurrentGeneration != *attempt.ExpectedGeneration || current.Generation != *attempt.ExpectedGeneration {
		return false, nil
	}
	entries, err := decodeJournal(attempt.MutationJournal)
	if err != nil {
		return false, nil
	}
	state, err := parseAutomaticLyricsRetryPrefix(entries, guard, input)
	if err != nil || state.acquisition == nil || state.outcome == nil || state.stageCount != 2 || state.exposure == nil || len(state.replacement) != 4 || state.commit != nil {
		return false, nil
	}
	exposure := *state.exposure
	if exposure.PositionID != attempt.PositionID || exposure.Epoch != attempt.Epoch || exposure.CandidateGeneration != attempt.CandidateGeneration || !sameOptionalUUID(exposure.ExpectedGeneration, attempt.ExpectedGeneration) || !sameJSONValue(exposure.Current, encodePublicationCommitJSON(current)) || current.RelativePath != exposure.Intent.TargetRelativePath || !publicationRewriteMatchesCurrent(current, attempt.Epoch, *input.Rewrite) {
		return false, nil
	}
	if err := validateExposureNamespace(ctx, tx, attempt, exposure.Intent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	last := state.replacement[3]
	if last.To != "installed" || last.Identity.SizeBytes == nil || last.Staged.Ownership.ExpectedSize == nil || *last.Identity.SizeBytes != *last.Staged.Ownership.ExpectedSize || last.Identity.SHA256 != last.Staged.PayloadSHA256 {
		return false, nil
	}
	var oldAudio PublicationCurrentAudio
	if decodePublicationCurrentContract(current.ActualAudio, &oldAudio) != nil || oldAudio.Version != 1 || oldAudio.Facts.Validate() != nil || current.QualityPolicyVersion != input.QualityPolicyVersion {
		return false, nil
	}
	lyricsProjection, err := automaticLyricsRetryProjection(*state.acquisition, *state.outcome, attempt.CandidateGeneration)
	if err != nil {
		return false, err
	}
	validatedAt := time.Now().UTC().Truncate(time.Microsecond)
	projection := current
	projection.Generation = attempt.CandidateGeneration
	projection.Ownership = encodePublicationCommitJSON(PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: last.Identity})
	projection.ActualAudio = encodePublicationCommitJSON(oldAudio)
	projection.ActualMetadata = append(json.RawMessage(nil), state.outcome.FinalObservation.ActualMetadata...)
	projection.ActualAssets = append(json.RawMessage(nil), state.outcome.FinalObservation.ActualAssets...)
	projection.InitialLyrics = encodePublicationCommitJSON(lyricsProjection)
	projection.ValidatedAt = validatedAt
	witness := replacementPublicationCommitWitness{Version: replacementPublicationCommitVersion, Kind: "publication_replacement_commit", Guard: guard, PositionID: attempt.PositionID, Epoch: attempt.Epoch, ExpectedGeneration: *attempt.ExpectedGeneration, CandidateGeneration: attempt.CandidateGeneration, Projection: projection}
	if !validAutomaticLyricsRetryCommit(witness, state, guard, input) {
		return false, errors.New("commit automatic lyrics retry: invalid replacement witness")
	}
	updated, err := tx.NewRaw(`UPDATE track_publication SET generation=?, ownership=?::jsonb, actual_audio=?::jsonb, actual_metadata=?::jsonb, actual_assets=?::jsonb, initial_lyrics=?::jsonb, validated_at=? WHERE position_id=? AND epoch=? AND generation=? AND relative_path=? AND ownership=?::jsonb AND actual_audio=?::jsonb AND actual_metadata=?::jsonb AND actual_assets=?::jsonb AND quality_policy_version=? AND validated_at=?`, projection.Generation, string(projection.Ownership), string(projection.ActualAudio), string(projection.ActualMetadata), string(projection.ActualAssets), string(projection.InitialLyrics), validatedAt, current.PositionID, current.Epoch, current.Generation, current.RelativePath, string(current.Ownership), string(current.ActualAudio), string(current.ActualMetadata), string(current.ActualAssets), current.QualityPolicyVersion, current.ValidatedAt).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("commit automatic lyrics retry: update current projection: %w", err)
	}
	rows, err := updated.RowsAffected()
	if err != nil || rows != 1 {
		return false, nil
	}
	if err := carryPositionSourceAuthorityTx(ctx, tx, current, projection); err != nil {
		return false, fmt.Errorf("commit automatic lyrics retry: carry source authority: %w", err)
	}
	journal, err := appendJournalEntry(attempt.MutationJournal, witness)
	if err != nil {
		return false, err
	}
	result, err := tx.NewRaw(`UPDATE track_publication_attempt SET phase='committed', frozen_inputs=NULL, mutation_journal=?::jsonb WHERE operation_id=? AND phase='exposing'`, string(journal), guard.OperationID).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("commit automatic lyrics retry: settle attempt: %w", err)
	}
	rows, err = result.RowsAffected()
	if err != nil || rows != 1 {
		return false, errors.New("commit automatic lyrics retry: attempt changed concurrently")
	}
	result, err = tx.NewRaw(`UPDATE operation SET stage='committed', updated_at=? WHERE id=? AND state='running' AND stage='exposing'`, validatedAt, guard.OperationID).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("commit automatic lyrics retry: update operation: %w", err)
	}
	rows, err = result.RowsAffected()
	if err != nil || rows != 1 {
		return false, errors.New("commit automatic lyrics retry: operation changed concurrently")
	}
	return true, nil
}

func retryCommitReplayMatches(entries []json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, current TrackPublication) bool {
	prefix, boundary, ok := canonicalAutomaticLyricsRetryCompletionPrefix(entries, guard, attempt)
	if !ok || boundary > len(entries) || !currentIsCandidate(current, attempt) || !currentPublicationMatchesReplacementWitness(current, prefix.commit.Projection) {
		return false
	}
	if boundary == len(entries) {
		return true
	}
	prep, installed := prefix.preparation[2], prefix.replacement[3]
	if prep.DirectoryIdentity == nil {
		return false
	}
	witness := PublicationReplacementCleanupWitness{PrivateDirRel: prep.PrivateDirRel, BackupRel: prep.InputRel, Directory: *prep.DirectoryIdentity, Parent: prep.ParentIdentity, Backup: installed.BackupIdentity}
	return sameCleanupWitnessForPrefix(witness, prefix) && replacementCleanupPrefixMatches(entries, guard, attempt, current, witness, boundary)
}

func currentPublicationMatchesReplacementWitness(current, expected TrackPublication) bool {
	var expectedLyrics *PublicationInitialLyricsProjection
	if len(expected.InitialLyrics) != 0 && decodeStrictJSON(expected.InitialLyrics, &expectedLyrics) != nil {
		return false
	}
	return current.PositionID == expected.PositionID && current.Epoch == expected.Epoch && current.Generation == expected.Generation && current.RelativePath == expected.RelativePath && current.ValidatedAt.Equal(expected.ValidatedAt) && current.QualityPolicyVersion == expected.QualityPolicyVersion && sameJSONValue(current.Ownership, expected.Ownership) && sameJSONValue(current.ActualAudio, expected.ActualAudio) && sameJSONValue(current.ActualMetadata, expected.ActualMetadata) && sameJSONValue(current.ActualAssets, expected.ActualAssets) && sameInitialLyricsProjectionJSON(current.InitialLyrics, expectedLyrics)
}
