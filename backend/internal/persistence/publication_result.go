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

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

// PublicationResult is the read-back of the artifact that was actually
// installed. It is evidence for the current projection, not a journal entry.
type PublicationResult struct {
	Observation           PublicationStageObservation
	InstalledOwnership    PublicationCurrentOwnership
	InitialLyrics         *PublicationInitialLyricsProjection
	CoverEmbedding        *PublicationCoverEmbeddingProjection
	ObsoleteOutputCleanup *ObsoletePublicationOutputCleanupAdmission
}

// CommitPublicationResult installs the actual output projection and consumes
// the live publication delivery in one database transaction. It performs no I/O.
func (repository *SetupManagerRepository) CommitPublicationResult(ctx context.Context, guard PublicationDeliveryGuard, result PublicationResult) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	if !validPublicationStageObservation(result.Observation) || !validInstalledPublicationOwnership(result.InstalledOwnership) {
		return false, errors.New("commit publication result: invalid installed projection")
	}
	committed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockPublicationSourceCleanupPolicy(ctx, tx, true); err != nil {
			return fmt.Errorf("commit publication result: lock source cleanup policy: %w", err)
		}
		attempt, _, claim, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "commit publication result")
		if err != nil || !live {
			return err
		}
		if claim.State != "executing" || attempt.Phase != "preparing" && attempt.Phase != "staged" && attempt.Phase != "validated" && attempt.Phase != "exposing" {
			return nil
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || (!input.SupportsExecution() && !validManualLyricsRewrite(input) && !validAutomaticAudioUpgrade(input)) || input.IntentKind != attempt.IntentKind {
			return nil
		}
		if attempt.ExpectedGeneration != nil {
			if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
				return fmt.Errorf("commit publication result: lock output admission gate: %w", err)
			}
		}
		observation := result.Observation.Clone()
		if observation.QualityPolicyVersion != input.QualityPolicyVersion || !publicationResultPathMatches(attempt.NamespaceManifest, observation.RelativePath) {
			return nil
		}
		var previous *TrackPublication
		if attempt.ExpectedGeneration != nil {
			var current TrackPublication
			if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("commit publication result: lock current publication: %w", err)
			}
			if current.Generation != *attempt.ExpectedGeneration {
				return nil
			}
			if validAutomaticAudioUpgrade(input) {
				var authority PositionSourceAuthority
				if err := tx.NewSelect().Model(&authority).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return nil
					}
					return fmt.Errorf("commit publication result: read current source authority: %w", err)
				}
				if !automaticAudioUpgradeCurrent(current, authority, input, attempt.Epoch) {
					return nil
				}
			}
			if input.ManualLyrics != nil && !manualLyricsResultCurrent(ctx, tx, attempt.PositionID, *input.ManualLyrics, current) {
				return nil
			}
			previous = &current
		} else {
			var existing uuid.UUID
			err := tx.NewRaw(`SELECT generation FROM track_publication WHERE position_id=? FOR UPDATE`, attempt.PositionID).Scan(ctx, &existing)
			if err == nil {
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("commit publication result: check current publication: %w", err)
			}
		}
		if err := validateObsoletePublicationOutputCleanup(result.ObsoleteOutputCleanup, previous, attempt, observation.RelativePath); err != nil {
			return err
		}
		if input.ManualCover != nil && !manualCoverResultCurrent(ctx, tx, attempt.PositionID, *input.ManualCover) {
			return nil
		}
		if input.AutomaticLyricsRetry != nil {
			matches, err := validateAutomaticLyricsRetryCurrentAuthority(ctx, tx, attempt.PositionID, *input.AutomaticLyricsRetry)
			if err != nil {
				return fmt.Errorf("commit publication result: validate automatic lyrics authority: %w", err)
			}
			if !matches {
				return nil
			}
		}
		assets, err := DecodePublicationAssetOutcomes(observation.ActualAssets)
		if err != nil {
			return fmt.Errorf("commit publication result: decode current assets: %w", err)
		}
		if validAutomaticAudioUpgrade(input) {
			var beforeMetadata, afterMetadata PublicationCommitMetadata
			if previous == nil || strictMetadata(previous.ActualMetadata, &beforeMetadata) != nil || strictMetadata(observation.ActualMetadata, &afterMetadata) != nil ||
				!sameJSONValue(mustPublicationJSON(beforeMetadata.Tags), mustPublicationJSON(input.AudioUpgrade.Current.Tags)) ||
				!sameJSONValue(mustPublicationJSON(afterMetadata.Tags), mustPublicationJSON(input.AudioUpgrade.Current.Tags)) {
				return nil
			}
			beforeAssets, err := DecodePublicationAssetOutcomes(previous.ActualAssets)
			if err != nil || !sameJSONValue(mustPublicationJSON(beforeAssets), mustPublicationJSON(input.AudioUpgrade.Current.Assets)) ||
				!sameJSONValue(mustPublicationJSON(assets), mustPublicationJSON(input.AudioUpgrade.Current.Assets)) {
				return nil
			}
			actualFacts, ok := publicationAudioFactsFromObservation(observation.ActualAudio)
			if !ok || !automaticAudioUpgradeQualityBetter(actualFacts, input.AudioUpgrade.Current.AudioFacts) {
				return nil
			}
		}
		if input.ManualCover != nil {
			if previous == nil || result.CoverEmbedding == nil {
				return errors.New("commit publication result: cover rewrite requires current publication and cover outcome")
			}
			var beforeMetadata, afterMetadata PublicationCommitMetadata
			if err := strictMetadata(previous.ActualMetadata, &beforeMetadata); err != nil {
				return err
			}
			if err := strictMetadata(observation.ActualMetadata, &afterMetadata); err != nil {
				return err
			}
			beforeAssets, err := DecodePublicationAssetOutcomes(previous.ActualAssets)
			if err != nil {
				return err
			}
			beforeTags, _ := json.Marshal(beforeMetadata.Tags)
			afterTags, _ := json.Marshal(afterMetadata.Tags)
			if string(beforeTags) != string(afterTags) || !samePublicationLyricsOutcome(beforeAssets.Lyrics, assets.Lyrics) {
				return errors.New("commit publication result: cover rewrite changed published metadata or lyrics")
			}
		}
		publication := TrackPublication{
			PositionID: attempt.PositionID, Epoch: attempt.Epoch, Generation: attempt.CandidateGeneration,
			RelativePath: observation.RelativePath, Ownership: mustPublicationJSON(result.InstalledOwnership),
			ActualAudio: observation.ActualAudio, ActualMetadata: observation.ActualMetadata, ActualAssets: observation.ActualAssets,
			QualityPolicyVersion: observation.QualityPolicyVersion, ValidatedAt: time.Now().UTC(),
		}
		if result.CoverEmbedding != nil {
			receipt, err := scanPublicationAttemptCoverInput(ctx, tx, attempt.OperationID)
			if err != nil {
				return fmt.Errorf("commit publication result: read frozen cover receipt: %w", err)
			}
			projection := *result.CoverEmbedding
			if !validPublicationCoverEmbeddingForReceipt(projection, receipt, attempt.CandidateGeneration, assets) {
				return errors.New("commit publication result: cover embedding does not match frozen receipt")
			}
			publication.CoverEmbedding = mustPublicationJSON(projection)
		} else if previous != nil && len(previous.CoverEmbedding) != 0 {
			previousProjection, err := decodePublicationCoverEmbeddingProjection(previous.CoverEmbedding)
			if err != nil {
				return fmt.Errorf("commit publication result: decode current cover embedding: %w", err)
			}
			previousAssets, err := DecodePublicationAssetOutcomes(previous.ActualAssets)
			if err != nil {
				return fmt.Errorf("commit publication result: decode previous publication assets: %w", err)
			}
			if samePublicationCoverOutcome(previousAssets.Cover, assets.Cover) {
				previousProjection.Generation = attempt.CandidateGeneration
				publication.CoverEmbedding = mustPublicationJSON(previousProjection)
			}
		}
		if result.InitialLyrics != nil {
			projection := *result.InitialLyrics
			projection.Generation = attempt.CandidateGeneration
			if !validPublicationInitialLyricsProjection(projection, attempt.CandidateGeneration) {
				return errors.New("commit publication result: invalid lyrics summary")
			}
			publication.InitialLyrics = mustPublicationJSON(projection)
		} else if previous != nil && input.ManualLyrics == nil && len(previous.InitialLyrics) != 0 {
			var projection PublicationInitialLyricsProjection
			if err := json.Unmarshal(previous.InitialLyrics, &projection); err != nil {
				return fmt.Errorf("commit publication result: decode current lyrics summary: %w", err)
			}
			projection.Generation = attempt.CandidateGeneration
			publication.InitialLyrics = mustPublicationJSON(projection)
		}
		if attempt.ExpectedGeneration == nil {
			if _, err := tx.NewInsert().Model(&publication).Exec(ctx); err != nil {
				return fmt.Errorf("commit publication result: insert current publication: %w", err)
			}
		} else {
			res, err := tx.NewUpdate().Model(&publication).Column("epoch", "generation", "relative_path", "ownership", "actual_audio", "actual_metadata", "actual_assets", "initial_lyrics", "cover_embedding", "quality_policy_version", "validated_at").Where("position_id=? AND generation=?", attempt.PositionID, *attempt.ExpectedGeneration).Exec(ctx)
			if err != nil {
				return fmt.Errorf("commit publication result: replace current publication: %w", err)
			}
			rows, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("commit publication result: count current publication update: %w", err)
			}
			if rows != 1 {
				return nil
			}
		}
		if previous != nil {
			next := publication
			if validAutomaticAudioUpgrade(input) {
				var authority PositionSourceAuthority
				if err := tx.NewSelect().Model(&authority).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
					return fmt.Errorf("commit publication result: lock source authority for audio upgrade: %w", err)
				}
				actualFacts, ok := publicationAudioFactsFromObservation(observation.ActualAudio)
				if !ok || !automaticAudioUpgradeQualityBetter(actualFacts, input.AudioUpgrade.Current.AudioFacts) {
					return nil
				}
				authority.Revision = uuid.New()
				authority.PublicationEpoch = next.Epoch
				authority.PublicationGeneration = next.Generation
				authority.SourceLocationID = input.Audio.LocationID
				authority.MediaVariantID = input.Audio.MediaVariantID
				updated, err := compareAndSetPositionSourceAuthorityTx(ctx, tx, &input.AudioUpgrade.SourceAuthorityRevision, authority)
				if err != nil {
					return err
				}
				if !updated {
					return fmt.Errorf("commit publication result: source authority changed: %w", ErrPublicationAdmissionConflict)
				}
			} else if err := carryPositionSourceAuthorityTx(ctx, tx, *previous, next); err != nil {
				return err
			}
			if (input.ManualCover != nil || validAutomaticAudioUpgrade(input)) && previous != nil {
				if err := carryManualLyricsReceiptForCoverRewriteTx(ctx, tx, attempt, *previous, publication, assets); err != nil {
					return err
				}
			}
		} else if input.SourceAuthorityMode != "" {
			if input.Audio == nil || input.SourceAuthorityMode != "manual" && input.SourceAuthorityMode != "automatic" {
				return errors.New("commit publication result: invalid source authority snapshot")
			}
			created, err := compareAndSetPositionSourceAuthorityTx(ctx, tx, nil, PositionSourceAuthority{
				PositionID: attempt.PositionID, Mode: input.SourceAuthorityMode, Revision: uuid.New(),
				PublicationEpoch: attempt.Epoch, PublicationGeneration: attempt.CandidateGeneration,
				SourceLocationID: input.Audio.LocationID, MediaVariantID: input.Audio.MediaVariantID,
			})
			if err != nil {
				return err
			}
			if !created {
				return fmt.Errorf("commit publication result: source authority changed: %w", ErrPublicationAdmissionConflict)
			}
		}
		if input.ManualLyrics != nil {
			if err := repository.recordPublicationResultLyricsTx(ctx, tx, attempt, observation, *input.ManualLyrics); err != nil {
				return err
			}
		}
		if err := repository.requestSourceCleanupTx(ctx, tx, attempt.OperationID, attempt.PositionID, attempt.Epoch, attempt.CandidateGeneration); err != nil {
			return fmt.Errorf("commit publication result: request source cleanup: %w", err)
		}
		if previous != nil && previous.RelativePath != observation.RelativePath {
			if result.ObsoleteOutputCleanup == nil {
				return errors.New("commit publication result: superseded output cleanup proof is required")
			}
			if repository.riverInserter == nil {
				return errors.New("commit publication result: River inserter is unavailable for obsolete output cleanup")
			}
			if _, err := repository.AdmitObsoletePublicationOutputCleanupTx(ctx, tx, *result.ObsoleteOutputCleanup, repository.riverInserter,
				func(id uuid.UUID) river.JobArgs { return ObsoletePublicationOutputCleanupJobArgs{OperationID: id} }, nil); err != nil {
				return fmt.Errorf("commit publication result: admit obsolete output cleanup: %w", err)
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_source_hold WHERE operation_id=?`, attempt.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("commit publication result: release source holds: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=?`, attempt.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("commit publication result: release tool holds: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE operation SET state='succeeded',stage='succeeded',finished_at=now(),updated_at=now(),tools_read_required=false WHERE id=?`, attempt.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("commit publication result: finish operation: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=? AND application_attempt=?`, guard.OperationID, guard.ApplicationAttempt).Exec(ctx); err != nil {
			return fmt.Errorf("commit publication result: delete delivery claim: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM track_publication_attempt WHERE operation_id=?`, attempt.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("commit publication result: consume attempt: %w", err)
		}
		if previous != nil {
			if _, err := tx.NewRaw(`DELETE FROM publication_namespace_reservation r
				USING publication_namespace_claim c
				WHERE c.id=r.namespace_claim_id AND c.epoch=? AND r.position_id=? AND r.generation=?
				AND NOT EXISTS (
					SELECT 1 FROM track_publication p
					WHERE p.position_id=r.position_id AND p.epoch=c.epoch AND p.generation=r.generation
				)
				AND NOT EXISTS (
					SELECT 1 FROM track_publication_attempt a
					WHERE a.position_id=r.position_id AND a.epoch=c.epoch
					AND (a.candidate_generation=r.generation OR a.expected_generation=r.generation)
				)
				AND NOT EXISTS (
					SELECT 1 FROM obsolete_publication_output_cleanup o
					WHERE o.epoch=c.epoch AND o.position_id=r.position_id AND o.generation=r.generation
					AND o.namespace_claim_id=r.namespace_claim_id
				)`, previous.Epoch, previous.PositionID, previous.Generation).Exec(ctx); err != nil {
				return fmt.Errorf("commit publication result: release unreferenced superseded namespace reservations: %w", err)
			}
		}
		committed = true
		return nil
	})
	return committed, err
}

func manualCoverResultCurrent(ctx context.Context, tx bun.Tx, positionID uuid.UUID, frozen PublicationManualCoverInput) bool {
	var revision int64
	if err := tx.NewRaw(`SELECT metadata_revision FROM release_track WHERE id=? FOR UPDATE`, positionID).Scan(ctx, &revision); err != nil {
		return false
	}
	var mode string
	var selected *uuid.UUID
	err := tx.NewRaw(`SELECT mode,selected_generation FROM release_publication_cover WHERE release_id=? FOR UPDATE`, frozen.ReleaseID).Scan(ctx, &mode, &selected)
	return err == nil && manualCoverAuthorityMatches(mode, selected, frozen)
}

func manualCoverAuthorityMatches(mode string, selected *uuid.UUID, frozen PublicationManualCoverInput) bool {
	return mode == frozen.EffectiveAuthorityMode() && selected != nil && *selected == frozen.SelectedGeneration
}

func carryManualLyricsReceiptForCoverRewriteTx(ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, previous, candidate TrackPublication, candidateAssets PublicationAssetOutcomes) error {
	var revision int64
	if err := tx.NewRaw(`SELECT metadata_revision FROM release_track WHERE id=? FOR UPDATE`, attempt.PositionID).Scan(ctx, &revision); err != nil {
		return fmt.Errorf("commit publication result: lock manual lyrics receipt revision: %w", err)
	}
	var receipt PublicationManualLyricsReceipt
	err := tx.NewSelect().Model(&receipt).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("commit publication result: lock manual lyrics receipt: %w", err)
	}
	if !manualLyricsReceiptCanCarry(receipt, previous, attempt.Epoch, revision) {
		return nil
	}
	previousAssets, err := DecodePublicationAssetOutcomes(previous.ActualAssets)
	if err != nil {
		return fmt.Errorf("commit publication result: decode previous manual lyrics assets: %w", err)
	}
	previousLyrics, previousPresent, err := EncodeObservedManualLyricsMetadata(previous.ActualMetadata)
	if err != nil {
		return fmt.Errorf("commit publication result: observe previous manual lyrics: %w", err)
	}
	candidateLyrics, candidatePresent, err := EncodeObservedManualLyricsMetadata(candidate.ActualMetadata)
	if err != nil {
		return fmt.Errorf("commit publication result: observe candidate manual lyrics: %w", err)
	}
	oldObserved := ManualLyricsObserved{MetadataPresent: previousPresent, MetadataValue: previousLyrics, Assets: previousAssets}
	newObserved := ManualLyricsObserved{MetadataPresent: candidatePresent, MetadataValue: candidateLyrics, Assets: candidateAssets}
	if previousLyrics != candidateLyrics || previousPresent != candidatePresent || !samePublicationLyricsOutcome(previousAssets.Lyrics, candidateAssets.Lyrics) ||
		validateManualLyricsReceiptObservation(receipt.DesiredState, receipt.DesiredSHA256, receipt.WarningCode, oldObserved) != nil ||
		validateManualLyricsReceiptObservation(receipt.DesiredState, receipt.DesiredSHA256, receipt.WarningCode, newObserved) != nil {
		return nil
	}
	result, err := tx.NewRaw(`UPDATE publication_manual_lyrics_receipt SET published_generation=? WHERE position_id=? AND epoch=? AND desired_revision=? AND published_generation=?`,
		attempt.CandidateGeneration, attempt.PositionID, receipt.Epoch, receipt.DesiredRevision, previous.Generation).Exec(ctx)
	if err != nil {
		return fmt.Errorf("commit publication result: carry manual lyrics receipt: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("commit publication result: count manual lyrics receipt carry: %w", err)
	}
	if rows > 1 {
		return errors.New("commit publication result: carried multiple manual lyrics receipts")
	}
	return nil
}

func manualLyricsReceiptCanCarry(receipt PublicationManualLyricsReceipt, previous TrackPublication, epoch uuid.UUID, revision int64) bool {
	return receipt.Epoch == previous.Epoch && receipt.PublishedGeneration == previous.Generation && receipt.Epoch == epoch && receipt.DesiredRevision == revision
}

func samePublicationLyricsOutcome(a, b PublicationLyricsOutcome) bool {
	return a.State == b.State && a.SHA256 == b.SHA256 && a.Format == b.Format
}

func samePublicationCoverOutcome(a, b PublicationCoverOutcome) bool {
	return a.State == b.State && a.SHA256 == b.SHA256 && a.MIME == b.MIME && sameOptionalInt64(a.SizeBytes, b.SizeBytes)
}

func publicationResultPathMatches(raw json.RawMessage, resultPath string) bool {
	var manifest struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}
	if decodeStrictJSON(raw, &manifest) != nil || manifest.Version != 1 || len(manifest.Nodes) == 0 {
		return false
	}
	for _, node := range manifest.Nodes {
		if node.NodeKind == "file" && node.RelativePath == resultPath {
			return true
		}
	}
	return false
}

func manualLyricsResultCurrent(ctx context.Context, tx bun.Tx, positionID uuid.UUID, manual PublicationManualLyricsInput, current TrackPublication) bool {
	var revision int64
	var metadata string
	if err := tx.NewRaw(`SELECT metadata_revision,metadata::text FROM release_track WHERE id=? FOR UPDATE`, positionID).Scan(ctx, &revision, &metadata); err != nil {
		return false
	}
	if revision != manual.ExpectedMetadataRevision || manual.ConfirmedCurrentGeneration != current.Generation {
		return false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(metadata), &root) != nil {
		return false
	}
	var envelope librarymetadata.Envelope
	var err error
	if raw := root["local_metadata"]; len(raw) != 0 {
		if envelope, err = librarymetadata.DecodeEnvelope(raw); err != nil || envelope.Scope != librarymetadata.ScopePosition {
			return false
		}
	} else {
		envelope = librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{}}
	}
	field, exists := envelope.Desired["synced_lyrics"]
	if !exists || field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual || manual.State != string(field.State) {
		return false
	}
	switch field.State {
	case librarymetadata.StateValue:
		value, ok := field.Value.(string)
		return ok && value != "" && value == manual.Value
	case librarymetadata.StateExplicitEmpty:
		return manual.Value == ""
	default:
		return false
	}
}

func validInstalledPublicationOwnership(ownership PublicationCurrentOwnership) bool {
	identity := ownership.Identity
	return ownership.Version == PublicationCurrentOwnershipVersion && identity.SizeBytes != nil && *identity.SizeBytes >= 0 &&
		identity.MTime != nil && validSHA256(identity.SHA256)
}

func mustPublicationJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func (repository *SetupManagerRepository) recordPublicationResultLyricsTx(ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, observation PublicationStageObservation, manual PublicationManualLyricsInput) error {
	assets, err := DecodePublicationAssetOutcomes(observation.ActualAssets)
	if err != nil {
		return fmt.Errorf("commit publication result: decode observed assets: %w", err)
	}
	lyrics, present, err := EncodeObservedManualLyricsMetadata(observation.ActualMetadata)
	if err != nil {
		return fmt.Errorf("commit publication result: observe manual lyrics: %w", err)
	}
	var desiredRevision int64
	if err := tx.NewRaw(`SELECT metadata_revision FROM release_track WHERE id=? FOR UPDATE`, attempt.PositionID).Scan(ctx, &desiredRevision); err != nil {
		return fmt.Errorf("commit publication result: lock manual lyrics revision: %w", err)
	}
	var digest *string
	state := ManualLyricsValue
	if manual.State == "explicit_empty" {
		state, digest = ManualLyricsExplicitEmpty, nil
	} else {
		hash := sha256.Sum256([]byte(manual.Value))
		value := hex.EncodeToString(hash[:])
		digest = &value
	}
	if err := validateManualLyricsObservation(state, digest, ManualLyricsObserved{MetadataPresent: present, MetadataValue: lyrics, Assets: assets}); err != nil {
		return fmt.Errorf("commit publication result: validate manual lyrics: %w", err)
	}
	_, err = tx.NewRaw(`INSERT INTO publication_manual_lyrics_receipt(position_id,epoch,desired_revision,confirmed_generation,published_generation,desired_state,desired_sha256,recorded_at)
		VALUES (?,?,?,?,?,?,?,now()) ON CONFLICT(position_id) DO UPDATE SET epoch=EXCLUDED.epoch,desired_revision=EXCLUDED.desired_revision,confirmed_generation=EXCLUDED.confirmed_generation,published_generation=EXCLUDED.published_generation,desired_state=EXCLUDED.desired_state,desired_sha256=EXCLUDED.desired_sha256,warning_code=NULL,recorded_at=EXCLUDED.recorded_at`,
		attempt.PositionID, attempt.Epoch, desiredRevision, manual.ConfirmedCurrentGeneration, attempt.CandidateGeneration, state, digest).Exec(ctx)
	if err != nil {
		return fmt.Errorf("commit publication result: upsert manual lyrics receipt: %w", err)
	}
	return nil
}
