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

const firstPublicationCommitVersion = 1

type firstPublicationCommitWitness struct {
	Version                     int                                 `json:"version"`
	Kind                        string                              `json:"kind"`
	Guard                       PublicationDeliveryGuard            `json:"guard"`
	PositionID                  uuid.UUID                           `json:"position_id"`
	Epoch                       uuid.UUID                           `json:"epoch"`
	CandidateGeneration         uuid.UUID                           `json:"candidate_generation"`
	RelativePath                string                              `json:"relative_path"`
	Ownership                   PublicationCurrentOwnership         `json:"ownership"`
	Audio                       PublicationCurrentAudio             `json:"audio"`
	Metadata                    PublicationCommitMetadata           `json:"metadata"`
	Assets                      PublicationAssetOutcomes            `json:"assets"`
	InitialLyrics               *PublicationInitialLyricsProjection `json:"initial_lyrics,omitempty"`
	InitialLyricsEvidenceSHA256 string                              `json:"initial_lyrics_evidence_sha256,omitempty"`
	QualityPolicyVersion        int                                 `json:"quality_policy_version"`
	ValidatedAt                 time.Time                           `json:"validated_at"`
}

// CommitFirstPublication atomically installs the durable current projection
// and consumes the running delivery's frozen inputs. It never performs I/O.
func (repository *SetupManagerRepository) CommitFirstPublication(ctx context.Context, guard PublicationDeliveryGuard) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	committed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("commit first publication: acquire output admission: %w", err)
		}
		if err := lockPublicationSourceCleanupPolicy(ctx, tx, true); err != nil {
			return fmt.Errorf("commit first publication: lock source cleanup policy: %w", err)
		}
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "commit first publication")
		if err != nil || !live {
			return err
		}
		var current TrackPublication
		currentErr := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx)
		if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
			return fmt.Errorf("commit first publication: read current row: %w", currentErr)
		}
		if attempt.Phase == "committed" && len(attempt.FrozenInputs) == 0 {
			if claim.State != "executing" || operation.Stage != "committed" || errors.Is(currentErr, sql.ErrNoRows) || !committedWitnessMatches(attempt.MutationJournal, guard, attempt, current) {
				return nil
			}
			committed = true
			return nil
		}
		attempt, operation, _, live, err = lockAndValidatePublicationDelivery(ctx, tx, guard, "commit first publication")
		if err != nil || !live || attempt.ExpectedGeneration != nil || attempt.IntentKind != "audio" {
			return err
		}
		if attempt.Phase != "exposing" || operation.Stage != "exposing" || currentErr == nil {
			return nil
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || !input.SupportsExecution() || input.IntentKind != "audio" {
			return nil
		}
		projection, err := AssessFirstPublicationCommit(*attempt, PublicationCommitOperationSnapshot{State: operation.State, Stage: operation.Stage}, nil, guard)
		if err != nil || len(projection.MissingRequirements) != 2 || projection.MissingRequirements[0] != "namespace_revalidation" || projection.MissingRequirements[1] != "live_delivery_fence" {
			return nil
		}
		var exposure publicationExposureJournalRecord
		var installed publicationCandidateInstalledJournalRecord
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return nil
		}
		for _, raw := range entries {
			var header struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal(raw, &header) != nil {
				return nil
			}
			switch header.Kind {
			case "publication_exposure":
				if decodeStrictJSON(raw, &exposure) != nil {
					return nil
				}
			case "publication_candidate_installed":
				if decodeStrictJSON(raw, &installed) != nil {
					return nil
				}
			}
		}
		if err := validateExposureNamespace(ctx, tx, attempt, exposure.Intent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		validatedAt := time.Now().UTC().Truncate(time.Microsecond)
		ownership := PublicationCurrentOwnership{Version: 1, Identity: installed.Observation.Identity}
		audioRaw, _ := json.Marshal(projection.CurrentAudio)
		metadataRaw, _ := json.Marshal(projection.Metadata)
		assetOutcomes := projection.ActualAssets.Clone()
		assetsRaw, _ := json.Marshal(assetOutcomes)
		ownershipRaw, _ := json.Marshal(ownership)
		initialLyricsRaw, err := json.Marshal(projection.InitialLyrics)
		if err != nil {
			return fmt.Errorf("commit first publication: encode initial lyrics projection: %w", err)
		}
		inserted, err := tx.NewRaw(`INSERT INTO track_publication (position_id, epoch, generation, relative_path, ownership, actual_audio, actual_metadata, actual_assets, initial_lyrics, quality_policy_version, validated_at) VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb, ?::jsonb, ?::jsonb, ?::jsonb, ?, ?) ON CONFLICT DO NOTHING`, attempt.PositionID, attempt.Epoch, attempt.CandidateGeneration, projection.TargetRelativePath, string(ownershipRaw), string(audioRaw), string(metadataRaw), string(assetsRaw), string(initialLyricsRaw), projection.QualityPolicyVersion, validatedAt).Exec(ctx)
		if err != nil {
			return fmt.Errorf("commit first publication: insert current row: %w", err)
		}
		rows, err := inserted.RowsAffected()
		if err != nil || rows != 1 {
			return nil
		}
		witness := firstPublicationCommitWitness{Version: firstPublicationCommitVersion, Kind: "publication_commit", Guard: guard, PositionID: attempt.PositionID, Epoch: attempt.Epoch, CandidateGeneration: attempt.CandidateGeneration, RelativePath: projection.TargetRelativePath, Ownership: ownership, Audio: projection.CurrentAudio, Metadata: projection.Metadata, Assets: assetOutcomes, InitialLyrics: projection.InitialLyrics, QualityPolicyVersion: projection.QualityPolicyVersion, ValidatedAt: validatedAt}
		witness.InitialLyricsEvidenceSHA256 = committedInitialLyricsCommitDigest(entries, witness)
		journalRaw, err := appendJournalEntry(attempt.MutationJournal, witness)
		if err != nil {
			return err
		}
		updated, err := tx.NewRaw(`UPDATE track_publication_attempt SET phase='committed', frozen_inputs=NULL, mutation_journal=?::jsonb WHERE operation_id=? AND phase='exposing'`, string(journalRaw), guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("commit first publication: settle attempt: %w", err)
		}
		n, err := updated.RowsAffected()
		if err != nil || n != 1 {
			return errors.New("commit first publication: attempt changed concurrently")
		}
		stage, err := tx.NewRaw(`UPDATE operation SET stage='committed', updated_at=? WHERE id=? AND state='running' AND stage='exposing'`, validatedAt, guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("commit first publication: update operation: %w", err)
		}
		n, err = stage.RowsAffected()
		if err != nil || n != 1 {
			return errors.New("commit first publication: operation changed concurrently")
		}
		if err := repository.requestSourceCleanupTx(ctx, tx, guard.OperationID, attempt.PositionID, attempt.Epoch, attempt.CandidateGeneration); err != nil {
			return fmt.Errorf("commit first publication: request source cleanup: %w", err)
		}
		committed = true
		return nil
	})
	return committed, err
}

func committedWitnessMatches(raw json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, current TrackPublication) bool {
	entries, err := decodeJournal(raw)
	if err != nil {
		return false
	}
	commitIndex := -1
	for index, entry := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(entry, &header) != nil {
			return false
		}
		if header.Kind == "publication_commit" {
			if commitIndex >= 0 {
				return false
			}
			commitIndex = index
		}
	}
	if commitIndex < 0 {
		return false
	}
	if commitIndex != len(entries)-1 {
		var cleanup publicationFirstAbsentCleanupRecord
		if decodeStrictJSON(entries[commitIndex+1], &cleanup) != nil || !exactCleanupJournal(entries, guard, cleanup.Witness, len(entries)-commitIndex-1) {
			return false
		}
		entries = entries[:commitIndex+1]
	}
	count := 0
	for index, entry := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(entry, &header) != nil {
			return false
		}
		if header.Kind != "publication_commit" {
			continue
		}
		count++
		var witness firstPublicationCommitWitness
		if index != len(entries)-1 || decodeStrictJSON(entry, &witness) != nil || witness.Version != firstPublicationCommitVersion || witness.Kind != "publication_commit" || witness.Guard != guard || witness.PositionID != attempt.PositionID || witness.PositionID != current.PositionID || witness.Epoch != attempt.Epoch || witness.CandidateGeneration != attempt.CandidateGeneration || current.Generation != witness.CandidateGeneration || current.Epoch != witness.Epoch || current.RelativePath != witness.RelativePath || !current.ValidatedAt.Equal(witness.ValidatedAt) || !currentPublicationMatchesWitness(current, witness) {
			return false
		}
	}
	if count != 1 {
		return false
	}
	initialLyricsWitnesses := 0
	initialLyricsEvidence := false
	initialLyricsProjectionPresent := false
	for _, entry := range entries {
		var witness firstPublicationCommitWitness
		if decodeStrictJSON(entry, &witness) == nil && witness.Kind == "publication_commit" {
			initialLyricsWitnesses++
			if witness.InitialLyrics == nil {
				if len(current.InitialLyrics) != 0 && string(current.InitialLyrics) != "null" {
					return false
				}
				continue
			}
			initialLyricsProjectionPresent = true
			acquisition, outcome, ok := committedInitialLyricsRecords(entries)
			if !ok {
				return false
			}
			projection, projectionErr := initialLyricsProjection(acquisition, outcome, current.Generation)
			digest := committedInitialLyricsCommitDigest(entries, witness)
			if projectionErr != nil || !sameInitialLyricsProjectionJSON(encodePublicationCommitJSON(witness.InitialLyrics), projection) || !validSHA256(witness.InitialLyricsEvidenceSHA256) || digest != witness.InitialLyricsEvidenceSHA256 || witness.InitialLyrics.Generation != current.Generation {
				return false
			}
		}
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(entry, &header) != nil {
			return false
		}
		if header.Kind == PublicationInitialLyricsAcquisitionKind || header.Kind == PublicationInitialLyricsOutcomeKind {
			initialLyricsEvidence = true
		}
	}
	return initialLyricsWitnesses == 1 && (!initialLyricsEvidence || initialLyricsProjectionPresent && committedInitialLyricsEvidencePresent(entries))
}

func committedInitialLyricsEvidencePresent(entries []json.RawMessage) bool {
	_, _, ok := committedInitialLyricsRecords(entries)
	return ok
}

func currentPublicationMatchesWitness(current TrackPublication, witness firstPublicationCommitWitness) bool {
	return current.PositionID == witness.PositionID && current.Epoch == witness.Epoch && current.Generation == witness.CandidateGeneration && current.RelativePath == witness.RelativePath && current.ValidatedAt.Equal(witness.ValidatedAt) &&
		sameJSONValue(current.Ownership, encodePublicationCommitJSON(witness.Ownership)) &&
		sameJSONValue(current.ActualAudio, encodePublicationCommitJSON(witness.Audio)) &&
		sameJSONValue(current.ActualMetadata, encodePublicationCommitJSON(witness.Metadata)) &&
		sameJSONValue(current.ActualAssets, encodePublicationCommitJSON(witness.Assets)) &&
		sameInitialLyricsProjectionJSON(current.InitialLyrics, witness.InitialLyrics) &&
		current.QualityPolicyVersion == witness.QualityPolicyVersion
}

func encodePublicationCommitJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}
