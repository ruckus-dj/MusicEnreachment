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
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

// PublicationResult is the read-back of the artifact that was actually
// installed. It is evidence for the current projection, not a journal entry.
type PublicationResult struct {
	Observation        PublicationStageObservation
	InstalledOwnership PublicationCurrentOwnership
	InitialLyrics      *PublicationInitialLyricsProjection
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
		if err != nil || (!input.SupportsExecution() && !validManualLyricsRewrite(input)) || input.IntentKind != attempt.IntentKind {
			return nil
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
		if input.AutomaticLyricsRetry != nil {
			matches, err := validateAutomaticLyricsRetryCurrentAuthority(ctx, tx, attempt.PositionID, *input.AutomaticLyricsRetry)
			if err != nil {
				return fmt.Errorf("commit publication result: validate automatic lyrics authority: %w", err)
			}
			if !matches {
				return nil
			}
		}
		assets := observation.ActualAssets
		publication := TrackPublication{
			PositionID: attempt.PositionID, Epoch: attempt.Epoch, Generation: attempt.CandidateGeneration,
			RelativePath: observation.RelativePath, Ownership: mustPublicationJSON(result.InstalledOwnership),
			ActualAudio: observation.ActualAudio, ActualMetadata: observation.ActualMetadata, ActualAssets: assets,
			QualityPolicyVersion: observation.QualityPolicyVersion, ValidatedAt: time.Now().UTC(),
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
			res, err := tx.NewUpdate().Model(&publication).Column("epoch", "generation", "relative_path", "ownership", "actual_audio", "actual_metadata", "actual_assets", "initial_lyrics", "quality_policy_version", "validated_at").Where("position_id=? AND generation=?", attempt.PositionID, *attempt.ExpectedGeneration).Exec(ctx)
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
			if err := carryPositionSourceAuthorityTx(ctx, tx, *previous, next); err != nil {
				return err
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
		committed = true
		return nil
	})
	return committed, err
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
