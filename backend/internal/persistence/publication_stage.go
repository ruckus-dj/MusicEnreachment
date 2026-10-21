package persistence

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationStageObservation is the source-free readback for one staged
// artifact. Paths are publication-relative, never source paths.
type PublicationStageObservation struct {
	Version              int               `json:"version"`
	RelativePath         string            `json:"relative_path"`
	Ownership            ArtifactOwnership `json:"ownership"`
	PayloadSHA256        string            `json:"payload_sha256,omitempty"`
	ActualAudio          json.RawMessage   `json:"actual_audio"`
	ActualMetadata       json.RawMessage   `json:"actual_metadata"`
	ActualAssets         json.RawMessage   `json:"actual_assets,omitempty"`
	QualityPolicyVersion int               `json:"quality_policy_version,omitempty"`
}

// Clone detaches JSON evidence and optional ownership pointers from caller-owned memory.
func (o PublicationStageObservation) Clone() PublicationStageObservation {
	o.ActualAudio = append(json.RawMessage(nil), o.ActualAudio...)
	o.ActualMetadata = append(json.RawMessage(nil), o.ActualMetadata...)
	o.ActualAssets = append(json.RawMessage(nil), o.ActualAssets...)
	if o.Ownership.ExpectedSize != nil {
		value := *o.Ownership.ExpectedSize
		o.Ownership.ExpectedSize = &value
	}
	if o.Ownership.ExpectedMTime != nil {
		value := *o.Ownership.ExpectedMTime
		o.Ownership.ExpectedMTime = &value
	}
	return o
}

// ArtifactOwnership captures the expected filesystem identity for a staged
// output. Size and mtime are optional because not every staging backend has
// both measurements at observation time.
type ArtifactOwnership struct {
	ExpectedSize  *int64 `json:"expected_size,omitempty"`
	ExpectedMTime *int64 `json:"expected_mtime,omitempty"`
}

// CheckPublicationDelivery confirms that the guarded delivery is still live.
func (repository *SetupManagerRepository) CheckPublicationDelivery(ctx context.Context, guard PublicationDeliveryGuard) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	valid := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, _, _, ok, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "check publication delivery")
		valid = ok
		return err
	})
	return valid, err
}

// AdvancePublicationStage performs only preparing->staged->validated. It
// appends a typed observation to the durable journal in the same transaction.
func (repository *SetupManagerRepository) AdvancePublicationStage(ctx context.Context, guard PublicationDeliveryGuard, expectedPhase string, observation PublicationStageObservation) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	if !validPublicationStageObservation(observation) {
		return false, errors.New("invalid publication stage observation")
	}
	nextPhase := ""
	switch expectedPhase {
	case "preparing":
		nextPhase = "staged"
	case "staged":
		nextPhase = "validated"
	default:
		return false, nil
	}
	changed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, ok, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "advance publication stage")
		if err != nil || !ok || attempt.Phase != expectedPhase || operation.Stage != expectedPhase {
			return err
		}
		if expectedPhase == "preparing" || expectedPhase == "staged" {
			input, decodeErr := DecodePublicationInputs(attempt.FrozenInputs)
			if decodeErr != nil {
				return fmt.Errorf("advance publication stage: decode frozen inputs: %w", decodeErr)
			}
			if !input.SupportsExecution() {
				return errors.New("advance publication stage: frozen inputs do not support execution")
			}
			if attempt.IntentKind == "audio" && attempt.ExpectedGeneration == nil {
				entries, journalErr := decodeJournal(attempt.MutationJournal)
				if journalErr != nil {
					return fmt.Errorf("advance publication stage: decode initial lyrics journal: %w", journalErr)
				}
				state, parseErr := parseInitialLyricsPrefix(entries, guard, input)
				if parseErr != nil || state.outcome == nil || state.stageCount != map[bool]int{true: 1, false: 0}[expectedPhase == "staged"] || !samePublicationStageObservation(state.outcome.FinalObservation, observation) {
					return errors.New("advance publication stage: initial lyrics outcome is missing or does not match observation")
				}
			}
			if expectedPhase == "staged" && (len(observation.ActualAssets) == 0 || observation.QualityPolicyVersion != input.QualityPolicyVersion) {
				return errors.New("advance publication stage: staged phase requires matching asset and quality-policy evidence")
			}
		}
		journal, err := appendPublicationStageJournal(attempt.MutationJournal, expectedPhase, nextPhase, observation)
		if err != nil {
			return fmt.Errorf("advance publication stage: decode mutation journal: %w", err)
		}
		if attempt.IntentKind == "audio" && attempt.ExpectedGeneration == nil {
			input, decodeErr := DecodePublicationInputs(attempt.FrozenInputs)
			if decodeErr != nil {
				return decodeErr
			}
			entries, decodeErr := decodeJournal(journal)
			if decodeErr != nil {
				return decodeErr
			}
			if _, parseErr := parseInitialLyricsPrefix(entries, guard, input); parseErr != nil {
				return fmt.Errorf("advance publication stage: proposed initial lyrics journal is invalid: %w", parseErr)
			}
		}
		attemptResult, err := tx.NewRaw(`UPDATE track_publication_attempt SET phase=?, mutation_journal=?::jsonb WHERE operation_id=? AND phase=?`, nextPhase, string(journal), guard.OperationID, expectedPhase).Exec(ctx)
		if err != nil {
			return fmt.Errorf("advance publication stage: update attempt: %w", err)
		}
		attemptRows, err := attemptResult.RowsAffected()
		if err != nil || attemptRows != 1 {
			return fmt.Errorf("advance publication stage: attempt changed concurrently")
		}
		operationResult, err := tx.NewRaw(`UPDATE operation SET stage=?, updated_at=now() WHERE id=? AND state='running' AND stage=?`, nextPhase, operation.ID, expectedPhase).Exec(ctx)
		if err != nil {
			return fmt.Errorf("advance publication stage: update operation: %w", err)
		}
		operationRows, err := operationResult.RowsAffected()
		if err != nil || operationRows != 1 {
			return fmt.Errorf("advance publication stage: operation changed concurrently")
		}
		changed = true
		return nil
	})
	return changed, err
}

// ProgressPublicationDelivery stores monotonic progress for the live delivery.
func (repository *SetupManagerRepository) ProgressPublicationDelivery(ctx context.Context, guard PublicationDeliveryGuard, completed int64, total *int64) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	if completed < 0 || total != nil && (*total < 0 || *total > 0 && completed > *total) {
		return false, errors.New("invalid publication progress")
	}
	changed := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, operation, _, ok, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "progress publication delivery")
		if err != nil || !ok {
			return err
		}
		if completed < operation.BytesCompleted || operation.BytesTotal != nil && *operation.BytesTotal > 0 && completed > *operation.BytesTotal ||
			(operation.BytesTotal != nil && *operation.BytesTotal > 0 && total != nil && *total < *operation.BytesTotal) {
			return nil
		}
		if total != nil && *total > 0 && completed > *total {
			return nil
		}
		if _, err := tx.NewRaw(`UPDATE operation SET bytes_completed=?, bytes_total=COALESCE(?,bytes_total), updated_at=now() WHERE id=? AND state='running'`, completed, total, guard.OperationID).Exec(ctx); err != nil {
			return fmt.Errorf("progress publication delivery: update operation: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}

// Lock order is output admission gate, epoch, position, attempt, operation,
// River job, claim. This helper deliberately requires the actual River row to
// remain running at the frozen attempt and never treats quiescence as ownership.
func lockAndValidatePublicationDelivery(ctx context.Context, tx bun.Tx, guard PublicationDeliveryGuard, action string) (*TrackPublicationAttempt, *Operation, *PublicationExecutionClaim, bool, error) {
	attempt, op, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, action)
	if err != nil || !live {
		return attempt, op, claim, live, err
	}
	if claim.State != "executing" {
		return nil, nil, nil, false, nil
	}
	var currentGeneration *uuid.UUID
	err = tx.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, guard.ExpectedPositionID).Scan(ctx, &currentGeneration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, false, fmt.Errorf("%s: read current publication: %w", action, err)
	}
	if !sameOptionalUUID(currentGeneration, attempt.ExpectedGeneration) {
		return nil, nil, nil, false, nil
	}
	return attempt, op, claim, true, nil
}

func lockPublicationDeliveryIdentity(ctx context.Context, tx bun.Tx, guard PublicationDeliveryGuard, action string) (*TrackPublicationAttempt, *Operation, *PublicationExecutionClaim, bool, error) {
	fail := func(err error) (*TrackPublicationAttempt, *Operation, *PublicationExecutionClaim, bool, error) {
		return nil, nil, nil, false, err
	}
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return fail(fmt.Errorf("%s: acquire output admission: %w", action, err))
	}
	var epoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, false, nil
		}
		return fail(fmt.Errorf("%s: lock epoch: %w", action, err))
	}
	if epoch != guard.Epoch {
		return nil, nil, nil, false, nil
	}
	var position uuid.UUID
	if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, guard.ExpectedPositionID).Scan(ctx, &position); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, false, nil
		}
		return fail(fmt.Errorf("%s: lock position: %w", action, err))
	}
	attempt := new(TrackPublicationAttempt)
	if err := tx.NewSelect().Model(attempt).Where("operation_id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, false, nil
		}
		return fail(fmt.Errorf("%s: lock attempt: %w", action, err))
	}
	op := new(Operation)
	if err := tx.NewSelect().Model(op).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, false, nil
		}
		return fail(fmt.Errorf("%s: lock operation: %w", action, err))
	}
	var riverState string
	var riverAttempt int
	if err := tx.NewRaw(`SELECT state, attempt FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).Scan(ctx, &riverState, &riverAttempt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, false, nil
		}
		return fail(fmt.Errorf("%s: lock River job: %w", action, err))
	}
	claim := new(PublicationExecutionClaim)
	if err := tx.NewSelect().Model(claim).Where("operation_id=? AND application_attempt=?", guard.OperationID, guard.ApplicationAttempt).For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, false, nil
		}
		return fail(fmt.Errorf("%s: lock claim: %w", action, err))
	}
	if op.Kind != "publish_track_v1" || op.State != "running" || op.Attempt != guard.ApplicationAttempt || op.RiverJobID == nil || *op.RiverJobID != guard.RiverJobID ||
		riverState != "running" || riverAttempt != guard.RiverAttempt || attempt.PositionID != guard.ExpectedPositionID || attempt.Epoch != guard.Epoch ||
		attempt.CandidateGeneration != guard.CandidateGeneration || !publicationClaimMatches(*claim, guard) {
		return nil, nil, nil, false, nil
	}
	return attempt, op, claim, true, nil
}

func validPublicationStageObservation(o PublicationStageObservation) bool {
	if o.Version != 1 || o.RelativePath == "" || strings.ContainsAny(o.RelativePath, "\\\x00") || path.IsAbs(o.RelativePath) ||
		len(o.RelativePath) >= 2 && ((o.RelativePath[0] >= 'A' && o.RelativePath[0] <= 'Z') || (o.RelativePath[0] >= 'a' && o.RelativePath[0] <= 'z')) && o.RelativePath[1] == ':' ||
		path.Clean(o.RelativePath) != o.RelativePath {
		return false
	}
	for _, part := range strings.Split(o.RelativePath, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	if o.Ownership.ExpectedSize != nil && *o.Ownership.ExpectedSize < 0 {
		return false
	}
	if o.PayloadSHA256 != "" {
		b, err := hex.DecodeString(o.PayloadSHA256)
		if err != nil || len(b) != 32 {
			return false
		}
	}
	if !publicationJSONObject(o.ActualAudio) || !publicationJSONObject(o.ActualMetadata) {
		return false
	}
	if len(o.ActualAssets) == 0 {
		return o.QualityPolicyVersion == 0
	}
	assets, err := DecodePublicationAssetOutcomes(o.ActualAssets)
	if err != nil {
		return false
	}
	var metadata PublicationCommitMetadata
	if strictMetadata(o.ActualMetadata, &metadata) != nil || assets.ValidateAgainstMetadata(metadata.Tags, nil) != nil {
		return false
	}
	// Asset outcomes are factual read-back evidence independent of quality-policy
	// provenance. Version 1 observations may carry them without claiming a policy.
	return o.QualityPolicyVersion >= 0
}

func appendPublicationStageJournal(raw json.RawMessage, from, to string, observation PublicationStageObservation) (json.RawMessage, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
		return nil, errors.New("journal is not an array")
	}
	entry, err := json.Marshal(struct {
		Version     int                         `json:"version"`
		Kind        string                      `json:"kind"`
		From        string                      `json:"from"`
		To          string                      `json:"to"`
		Observation PublicationStageObservation `json:"observation"`
	}{1, "stage_observation", from, to, observation})
	if err != nil {
		return nil, err
	}
	entries = append(entries, entry)
	return json.Marshal(entries)
}
