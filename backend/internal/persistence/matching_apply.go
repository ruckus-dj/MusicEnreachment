package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
	"github.com/uptrace/bun"
)

var ErrMatchingManualProtection = errors.New("matching apply blocked by manual protection")

// ApplyCurrentMatchingEvaluation materializes only the persisted, unique
// evaluation decision for groupID. Evaluation evidence and all current source
// and provider fences are re-read and locked in the same transaction as writes.
func (repository *IncomingMatchCandidateRepository) ApplyCurrentMatchingEvaluation(ctx context.Context, groupID uuid.UUID, expectedEvaluationRevision string) error {
	if groupID == uuid.Nil || expectedEvaluationRevision == "" {
		return fmt.Errorf("apply matching evaluation: group and evaluation revision are required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// Publication reconciliation intents are written by this transaction, so
		// the output admission gate precedes every domain lock.
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("apply matching evaluation: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("apply matching evaluation: lock source mutation gate: %w", err)
		}
		return repository.applyCurrentMatchingEvaluationTx(ctx, tx, groupID, expectedEvaluationRevision)
	})
}

func (repository *IncomingMatchCandidateRepository) applyCurrentMatchingEvaluationTx(ctx context.Context, tx bun.Tx, groupID uuid.UUID, expectedEvaluationRevision string) error {
	var header IncomingMatchEvaluationHeader
	if err := tx.NewRaw(`SELECT group_id, evaluation_revision, evaluation_evidence, group_revision, groups_revision, grouping_epoch,
			policy_version, provider_id, provider_source_id, provider_configuration_identity, provider_namespace, provider_endpoint,
			cache_key, cache_revision, provider_snapshot_revision, provider_fences, evaluated_at,
			matching_authority_mode, matching_authority_revision, fence_version, target_run_id, target_capture, target_capture_digest
			FROM incoming_match_evaluation WHERE group_id=?`, groupID).Scan(ctx, &header); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIncomingMatchConflict
		}
		return fmt.Errorf("apply matching evaluation: read evaluation: %w", err)
	}
	if header.EvaluationRevision != expectedEvaluationRevision {
		return ErrIncomingMatchConflict
	}
	var fence IncomingMatchProviderFence
	if err := json.Unmarshal(header.ProviderFences, &fence); err != nil {
		return fmt.Errorf("apply matching evaluation: decode provider fences: %w", err)
	}
	if fence.ProviderID != header.ProviderID || fence.ProviderSourceID != header.ProviderSourceID || fence.ConfigurationIdentity != header.ProviderConfigurationIdentity || fence.Namespace != header.ProviderNamespace || fence.Endpoint != header.ProviderEndpoint || fence.CacheKey != header.CacheKey || !equalOptionalString(fence.CacheRevision, header.CacheRevision) || fence.AcceptedProjectionRevision != header.ProviderSnapshotRevision {
		return ErrIncomingMatchConflict
	}
	group := IncomingMatchGroupEvaluation{GroupID: groupID, GroupRevision: header.GroupRevision, Evidence: header.EvaluationEvidence}
	if err := tx.NewRaw(`SELECT file_member_id, work_id, source_sha256 FROM incoming_match_evaluation_member WHERE group_id=? ORDER BY file_member_id`, groupID).Scan(ctx, &group.Members); err != nil {
		return fmt.Errorf("apply matching evaluation: read evaluated members: %w", err)
	}
	if len(group.Members) == 0 {
		return ErrIncomingMatchConflict
	}
	evaluation := IncomingMatchEvaluation{
		ExpectedGroupsRevision: header.GroupsRevision, ExpectedGroupingEpoch: header.GroupingEpoch,
		PolicyVersion: header.PolicyVersion, Provider: fence, Groups: []IncomingMatchGroupEvaluation{group},
	}
	if header.FenceVersion == 2 {
		if header.TargetRunID == uuid.Nil || len(header.TargetCapture) == 0 || len(header.TargetCaptureDigest) != 32 {
			return ErrIncomingMatchConflict
		}
		evaluation.TargetFenceVersion = 2
		evaluation.TargetRunID = header.TargetRunID
		evaluation.TargetCapture = append(json.RawMessage(nil), header.TargetCapture...)
		evaluation.TargetCaptureDigest = hex.EncodeToString(header.TargetCaptureDigest)
	} else if header.FenceVersion != 1 || header.TargetRunID != uuid.Nil || len(header.TargetCapture) != 0 || len(header.TargetCaptureDigest) != 0 {
		return ErrIncomingMatchConflict
	}
	if err := lockAndValidateMatchingPrelude(ctx, tx, evaluation, "apply matching evaluation"); err != nil {
		return err
	}
	if header.MatchingAuthorityMode != "automatic" {
		return fmt.Errorf("apply matching evaluation: persisted evaluation authority is not automatic: %w", ErrIncomingMatchConflict)
	}
	currentAuthorityRevision, err := requireAutomaticMatchingAuthority(ctx, tx, groupID)
	if err != nil {
		return fmt.Errorf("apply matching evaluation: group authority is not automatic: %w", err)
	}
	if currentAuthorityRevision != header.MatchingAuthorityRevision {
		return ErrIncomingMatchConflict
	}
	if err := rejectManualOnlyMatchingEvaluation(header.EvaluationEvidence); err != nil {
		return err
	}
	decision, err := decodePersistedMatchingDecision(header.EvaluationEvidence)
	if err != nil {
		return err
	}
	if decision.GroupID != groupID || decision.PolicyVersion != header.PolicyVersion || decision.Outcome != "unique" || decision.SelectedRelease == nil {
		return fmt.Errorf("apply matching evaluation: persisted decision is not uniquely applicable")
	}
	if err := lockAndValidateMatchProvider(ctx, tx, evaluation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "incoming-match-evaluation:"+groupID.String()); err != nil {
		return fmt.Errorf("apply matching evaluation: lock evaluation: %w", err)
	}
	var currentRevision string
	if err := tx.NewRaw(`SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, groupID).Scan(ctx, &currentRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIncomingMatchConflict
		}
		return fmt.Errorf("apply matching evaluation: read locked revision: %w", err)
	}
	if currentRevision != expectedEvaluationRevision {
		return ErrIncomingMatchConflict
	}
	positions, err := applyPersistedMatchingDecision(ctx, tx, group, decision, fence, header.PolicyVersion)
	if err != nil {
		return err
	}
	return requestMatchingApplyPublicationReconciliation(ctx, repository.db, tx, positions)
}

// rejectManualOnlyMatchingEvaluation protects the automatic apply entry point
// from evaluations that carry explicit manual-review authority. Keep this
// independent of the solver decision: a uniquely solved diagnostic result is
// still not authorization to apply a manually selected release.
func rejectManualOnlyMatchingEvaluation(evidence json.RawMessage) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(evidence, &envelope); err != nil {
		return fmt.Errorf("apply matching evaluation: persisted evidence is invalid")
	}
	raw, exists := envelope["manual_authority"]
	if !exists {
		return nil
	}
	var authority struct {
		Version             int       `json:"version"`
		ExecutionMode       string    `json:"execution_mode"`
		SelectedReleaseMBID uuid.UUID `json:"selected_release_mbid"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&authority); err != nil || authority.Version != 1 || authority.ExecutionMode != "manual_review" || authority.SelectedReleaseMBID == uuid.Nil {
		return fmt.Errorf("apply matching evaluation: manual authority is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("apply matching evaluation: trailing manual authority data")
	}
	return fmt.Errorf("apply matching evaluation: manual-review evaluations cannot be applied automatically")
}

// ApplyMatchingOperation applies one immutable automatic group intent and
// settles the child operation with its receipt in the same transaction.
func (repository *SetupManagerRepository) ApplyMatchingOperation(ctx context.Context, operationID uuid.UUID, riverJobID int64, executionEpoch int64) (bool, error) {
	var snapshotBytes []byte
	if err := repository.db.NewRaw(`SELECT input_snapshot FROM operation WHERE id=?`, operationID).Scan(ctx, &snapshotBytes); err != nil {
		return false, fmt.Errorf("apply matching operation: read immutable snapshot: %w", err)
	}
	snapshot := json.RawMessage(snapshotBytes)
	var version struct {
		SchemaVersion int `json:"schema_version"`
		Source        struct {
			ConfigurationIdentity string `json:"configuration_identity"`
		} `json:"source"`
	}
	if err := json.Unmarshal(snapshot, &version); err != nil {
		return false, fmt.Errorf("apply matching operation: immutable snapshot is invalid")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("apply matching operation: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("apply matching operation: lock source mutations: %w", err)
		}
		currentProviderConfiguration := true
		if version.SchemaVersion == 2 {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
				return fmt.Errorf("apply matching operation: lock MusicBrainz configuration: %w", err)
			}
			var currentIdentity string
			if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
				return fmt.Errorf("apply matching operation: read current MusicBrainz configuration: %w", err)
			}
			currentProviderConfiguration = currentIdentity == version.Source.ConfigurationIdentity
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, operationID)
		if err != nil {
			return err
		}
		var lockedVersion struct {
			SchemaVersion int `json:"schema_version"`
		}
		if err := json.Unmarshal(operation.InputSnapshot, &lockedVersion); err != nil || lockedVersion.SchemaVersion != version.SchemaVersion {
			return fmt.Errorf("apply matching operation: immutable snapshot schema changed")
		}
		if operation.Kind != MatchingApplyOperationKind || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID || operation.ProviderExecutionEpoch != executionEpoch || operation.ProviderDeliveryClaimedAt == nil || operation.ProviderDeliveryClaimedAt.Before(time.Now().UTC().Add(-5*time.Minute)) || (operation.State != "queued" && operation.State != "running") {
			return nil
		}
		if version.SchemaVersion == 2 {
			var err error
			changed, err = repository.applyFrozenMatchingOperationTx(ctx, tx, operation, operationID, riverJobID, executionEpoch, currentProviderConfiguration)
			return err
		}
		var intent matchingApplyIntent
		if err := json.Unmarshal(operation.InputSnapshot, &intent); err != nil || intent.SchemaVersion != 1 || intent.ParentRunID == uuid.Nil || intent.GroupID == uuid.Nil || intent.EvaluationRevision == "" || intent.ExecutionEpoch < 1 {
			return fmt.Errorf("apply matching operation: immutable snapshot is invalid")
		}
		var receiptOutcome string
		if err := tx.NewRaw(`SELECT outcome FROM matching_apply_receipt WHERE apply_operation_id=? AND group_id=?`, operationID, intent.GroupID).Scan(ctx, &receiptOutcome); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("apply matching operation: check receipt: %w", err)
		}
		receipt := receiptOutcome != ""
		now := time.Now().UTC()
		if receipt {
			operation.State, operation.Stage = "succeeded", receiptOutcome
			if receiptOutcome == "review_required" {
				reason := "The matching decision is stale or conflicts with a manual mapping; review this group manually."
				operation.SafeError = &reason
			} else {
				operation.SafeError = nil
			}
		} else {
			var current bool
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM matching_apply_request request
				JOIN matching_run run ON run.id=request.parent_run_id
				JOIN matching_run_target target ON target.run_id=run.id AND target.group_id=request.group_id
				JOIN incoming_matching_generation generation ON generation.group_id=request.group_id
				WHERE request.group_id=? AND request.parent_run_id=? AND request.apply_operation_id=?
				AND request.evaluation_revision=? AND run.execution_epoch=?
				AND generation.current_run_id=run.id AND generation.high_water=target.target_generation
				AND COALESCE((SELECT authority.mode FROM incoming_group_matching_authority authority WHERE authority.group_id=request.group_id),'automatic')='automatic'
				AND COALESCE((SELECT authority.revision FROM incoming_group_matching_authority authority WHERE authority.group_id=request.group_id),0)=target.authority_revision
			)`, intent.GroupID, intent.ParentRunID, operationID, intent.EvaluationRevision, intent.ExecutionEpoch).Scan(ctx, &current); err != nil {
				return fmt.Errorf("apply matching operation: validate original run authority: %w", err)
			}
			if !current {
				operation.State, operation.Stage = "succeeded", "review_required"
				reason := "The original matching run is no longer current; review this group manually."
				operation.SafeError = &reason
			} else {
				candidateRepo := NewIncomingMatchCandidateRepository(repository.db)
				var headerFence struct {
					FenceVersion int        `bun:"fence_version"`
					TargetRunID  *uuid.UUID `bun:"target_run_id"`
				}
				if err := tx.NewRaw(`SELECT fence_version,target_run_id FROM incoming_match_evaluation WHERE group_id=? AND evaluation_revision=?`, intent.GroupID, intent.EvaluationRevision).Scan(ctx, &headerFence); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						operation.State, operation.Stage = "succeeded", "review_required"
						reason := "The matching decision is stale or conflicts with a manual mapping; review this group manually."
						operation.SafeError = &reason
					} else {
						return fmt.Errorf("apply matching operation: read evaluation target fence: %w", err)
					}
				} else if headerFence.FenceVersion == 2 && (headerFence.TargetRunID == nil || *headerFence.TargetRunID != intent.ParentRunID) ||
					(headerFence.FenceVersion == 1 && headerFence.TargetRunID != nil) || (headerFence.FenceVersion != 1 && headerFence.FenceVersion != 2) {
					operation.State, operation.Stage = "succeeded", "review_required"
					reason := "The matching decision is stale or conflicts with a manual mapping; review this group manually."
					operation.SafeError = &reason
				} else {
					if _, err := tx.ExecContext(ctx, `SAVEPOINT matching_apply_materialization`); err != nil {
						return fmt.Errorf("apply matching operation: create materialization savepoint: %w", err)
					}
					err := candidateRepo.applyCurrentMatchingEvaluationTx(ctx, tx, intent.GroupID, intent.EvaluationRevision)
					if errors.Is(err, ErrIncomingMatchConflict) || errors.Is(err, ErrMatchingManualProtection) {
						if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT matching_apply_materialization`); rollbackErr != nil {
							return fmt.Errorf("apply matching operation: rollback conflicted materialization: %w", rollbackErr)
						}
						if _, releaseErr := tx.ExecContext(ctx, `RELEASE SAVEPOINT matching_apply_materialization`); releaseErr != nil {
							return fmt.Errorf("apply matching operation: release materialization savepoint: %w", releaseErr)
						}
						operation.State, operation.Stage = "succeeded", "review_required"
						reason := "The matching decision is stale or conflicts with a manual mapping; review this group manually."
						operation.SafeError = &reason
					} else if err != nil {
						return err
					} else {
						if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT matching_apply_materialization`); err != nil {
							return fmt.Errorf("apply matching operation: release materialization savepoint: %w", err)
						}
						operation.State, operation.Stage = "succeeded", "applied"
						operation.SafeError = nil
					}
				}
			}
		}
		if !receipt {
			if _, err := tx.NewRaw(`INSERT INTO matching_apply_receipt(apply_operation_id,group_id,outcome) VALUES (?,?,?)`, operationID, intent.GroupID, operation.Stage).Exec(ctx); err != nil {
				return fmt.Errorf("apply matching operation: persist receipt: %w", err)
			}
		}
		operation.FinishedAt = &now
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "safe_error", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("apply matching operation: complete operation: %w", err)
		}
		changed = true
		return nil
	})
	return changed, err
}

type persistedMatchingDecision struct {
	Version         int       `json:"version"`
	GroupID         uuid.UUID `json:"group_id"`
	PolicyVersion   string    `json:"policy_version"`
	Outcome         string    `json:"outcome"`
	SelectedRelease *struct {
		ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
		ProviderReleaseKey string    `json:"provider_release_key"`
	} `json:"selected_release,omitempty"`
	Assignments []struct {
		FileMemberID    uuid.UUID `json:"file_member_id"`
		ProviderTrackID uuid.UUID `json:"provider_track_id"`
		Medium          int       `json:"medium"`
		Position        int       `json:"position"`
	} `json:"assignments"`
}

type persistedMatchingOutcome struct {
	Version         int             `json:"version"`
	GroupID         uuid.UUID       `json:"group_id"`
	PolicyVersion   string          `json:"policy_version"`
	Outcome         string          `json:"outcome"`
	SelectedRelease json.RawMessage `json:"selected_release,omitempty"`
	Assignments     json.RawMessage `json:"assignments"`
}

func decodeMatchingApplyOutcome(evidence json.RawMessage) (persistedMatchingOutcome, error) {
	var envelope struct {
		Decision json.RawMessage `json:"evaluation_decision"`
	}
	if err := json.Unmarshal(evidence, &envelope); err != nil || len(envelope.Decision) == 0 {
		return persistedMatchingOutcome{}, fmt.Errorf("apply matching evaluation: persisted decision is missing or invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Decision))
	decoder.DisallowUnknownFields()
	var outcome persistedMatchingOutcome
	if err := decoder.Decode(&outcome); err != nil {
		return persistedMatchingOutcome{}, fmt.Errorf("apply matching evaluation: decode persisted outcome: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return persistedMatchingOutcome{}, fmt.Errorf("apply matching evaluation: trailing decision data")
	}
	if outcome.Version != 1 || outcome.GroupID == uuid.Nil || !matchingpolicy.Supported(outcome.PolicyVersion) {
		return persistedMatchingOutcome{}, fmt.Errorf("apply matching evaluation: persisted outcome identity is invalid")
	}
	switch outcome.Outcome {
	case "unique", "tie", "infeasible", "review":
		return outcome, nil
	default:
		return persistedMatchingOutcome{}, fmt.Errorf("apply matching evaluation: persisted outcome is unsupported")
	}
}

func decodePersistedMatchingDecision(evidence json.RawMessage) (persistedMatchingDecision, error) {
	var envelope struct {
		Decision json.RawMessage `json:"evaluation_decision"`
	}
	if err := json.Unmarshal(evidence, &envelope); err != nil || len(envelope.Decision) == 0 {
		return persistedMatchingDecision{}, fmt.Errorf("apply matching evaluation: persisted decision is missing or invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Decision))
	decoder.DisallowUnknownFields()
	var decision persistedMatchingDecision
	if err := decoder.Decode(&decision); err != nil {
		return persistedMatchingDecision{}, fmt.Errorf("apply matching evaluation: decode persisted decision: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return persistedMatchingDecision{}, fmt.Errorf("apply matching evaluation: trailing decision data")
	}
	if decision.Version != 1 || decision.GroupID == uuid.Nil || !matchingpolicy.Supported(decision.PolicyVersion) || decision.Outcome != "unique" || decision.SelectedRelease == nil || decision.SelectedRelease.ProviderReleaseID == uuid.Nil || decision.SelectedRelease.ProviderReleaseKey == "" || len(decision.Assignments) == 0 {
		return persistedMatchingDecision{}, fmt.Errorf("apply matching evaluation: persisted decision is not a valid unique assignment")
	}
	seen := make(map[uuid.UUID]bool, len(decision.Assignments))
	for _, assignment := range decision.Assignments {
		if assignment.FileMemberID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil || assignment.Medium < 1 || assignment.Position < 1 || seen[assignment.FileMemberID] {
			return persistedMatchingDecision{}, fmt.Errorf("apply matching evaluation: persisted assignment is invalid")
		}
		seen[assignment.FileMemberID] = true
	}
	return decision, nil
}

// applyPersistedMatchingDecision materializes one persisted unique decision and
// returns the release positions whose publication inputs it changed.
func applyPersistedMatchingDecision(ctx context.Context, tx bun.Tx, group IncomingMatchGroupEvaluation, decision persistedMatchingDecision, fence IncomingMatchProviderFence, policy string) ([]uuid.UUID, error) {
	if len(decision.Assignments) != len(group.Members) {
		return nil, fmt.Errorf("apply matching evaluation: assignments do not cover every group member")
	}
	selectedReleaseEvaluated := false
	for _, key := range fence.ReleaseKeys {
		if key == decision.SelectedRelease.ProviderReleaseKey {
			selectedReleaseEvaluated = true
			break
		}
	}
	if !selectedReleaseEvaluated {
		return nil, ErrIncomingMatchConflict
	}
	var releaseTitle, releaseKey string
	var releaseFields, releaseEvidence string
	var releaseID uuid.UUID
	if err := tx.NewRaw(`SELECT e.name, r.id, r.provider_key, e.fields, e.field_evidence FROM provider_release r JOIN provider_source_entity e ON e.provider_id=r.provider_id AND e.provider_key=r.provider_key
		WHERE r.provider_id=? AND r.id=? AND e.provider_source_id=? AND e.entity_kind='release' AND e.complete=true`, fence.ProviderID, decision.SelectedRelease.ProviderReleaseID, fence.ProviderSourceID).Scan(ctx, &releaseTitle, &releaseID, &releaseKey, &releaseFields, &releaseEvidence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrIncomingMatchConflict
		}
		return nil, fmt.Errorf("apply matching evaluation: read selected release: %w", err)
	}
	if releaseID != decision.SelectedRelease.ProviderReleaseID {
		return nil, ErrIncomingMatchConflict
	}
	if releaseKey != decision.SelectedRelease.ProviderReleaseKey {
		return nil, ErrIncomingMatchConflict
	}
	localReleaseID, releaseCreated, err := ensureAutomaticLocalRelease(ctx, tx, fence.ProviderID, releaseID, releaseTitle, policy)
	if err != nil {
		return nil, err
	}
	if releaseCreated {
		if err := writeAutomaticMetadata(ctx, tx, "local_release", localReleaseID, "title", releaseKey, fence.ProviderSourceID, fence.ProviderID, fence.AcceptedProjectionRevision, json.RawMessage(releaseEvidence)); err != nil {
			return nil, err
		}
		credits, err := acceptedReleaseArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, releaseID)
		if err != nil {
			return nil, err
		}
		if err := materializeArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, localReleaseID, "release_artist", credits, policy, fence.AcceptedProjectionRevision); err != nil {
			return nil, err
		}
	}
	memberByID := make(map[uuid.UUID]IncomingMatchMemberFence, len(group.Members))
	for _, member := range group.Members {
		memberByID[member.FileMemberID] = member
	}
	localTrackIDs := make([]uuid.UUID, 0, len(decision.Assignments))
	for _, assignment := range decision.Assignments {
		member, ok := memberByID[assignment.FileMemberID]
		if !ok {
			return nil, fmt.Errorf("apply matching evaluation: assignment references a non-member")
		}
		var providerTrack, providerRecording uuid.UUID
		var medium, position int
		var recordingTitle string
		var recordingEvidence string
		if err := tx.NewRaw(`SELECT t.id, t.recording_id, st.medium, st.position, COALESCE(NULLIF(e.name,''), e.fields->>'title',''), e.field_evidence
			FROM provider_release_track t JOIN provider_source_release_track st ON st.provider_id=t.provider_id AND st.track_id=t.id
			LEFT JOIN provider_source_entity e ON e.provider_source_id=st.provider_source_id AND e.provider_id=t.provider_id AND e.entity_kind='recording' AND e.provider_key=(SELECT provider_key FROM provider_recording WHERE id=t.recording_id AND provider_id=t.provider_id)
				WHERE t.id=? AND t.provider_id=? AND st.release_id=? AND st.present=true AND st.provider_source_id=?`, assignment.ProviderTrackID, fence.ProviderID, releaseID, fence.ProviderSourceID).Scan(ctx, &providerTrack, &providerRecording, &medium, &position, &recordingTitle, &recordingEvidence); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrIncomingMatchConflict
			}
			return nil, fmt.Errorf("apply matching evaluation: read assigned track: %w", err)
		}
		if medium+1 != assignment.Medium || position+1 != assignment.Position {
			return nil, ErrIncomingMatchConflict
		}
		localTrackID, localRecordingID, recordingCreated, trackCreated, err := ensureAutomaticLocalTrack(ctx, tx, fence.ProviderID, providerTrack, providerRecording, localReleaseID, medium, position, recordingTitle, policy)
		if err != nil {
			return nil, err
		}
		localTrackIDs = append(localTrackIDs, localTrackID)
		if recordingCreated {
			credits, err := acceptedRecordingArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, providerRecording)
			if err != nil {
				return nil, err
			}
			if err := writeAutomaticMetadata(ctx, tx, "local_recording", localRecordingID, "title", recordingProviderKey(ctx, tx, fence.ProviderID, providerRecording), fence.ProviderSourceID, fence.ProviderID, fence.AcceptedProjectionRevision, json.RawMessage(recordingEvidence)); err != nil {
				return nil, err
			}
			if err := materializeArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, localRecordingID, "recording_artist", credits, policy, fence.AcceptedProjectionRevision); err != nil {
				return nil, err
			}
		}
		if trackCreated {
			recordingKey := recordingProviderKey(ctx, tx, fence.ProviderID, providerRecording)
			titleField, titleKey := "title", recordingKey
			titleEvidence := json.RawMessage(recordingEvidence)
			if title := acceptedTrackTitle(json.RawMessage(releaseFields), medium, position, recordingKey); title != "" {
				if _, err := tx.NewRaw(`UPDATE local_track SET title=? WHERE id=?`, title, localTrackID).Exec(ctx); err != nil {
					return nil, err
				}
				titleField, titleKey = "normalized_tracks", releaseKey
				titleEvidence = json.RawMessage(releaseEvidence)
			}
			if err := writeAutomaticMetadata(ctx, tx, "local_track", localTrackID, titleField, titleKey, fence.ProviderSourceID, fence.ProviderID, fence.AcceptedProjectionRevision, titleEvidence); err != nil {
				return nil, err
			}
			credits, err := acceptedTrackArtistCredits(json.RawMessage(releaseFields), json.RawMessage(releaseEvidence), medium, position, recordingKey, releaseKey)
			if credits == nil {
				credits, err = acceptedRecordingArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, providerRecording)
			}
			if err != nil {
				return nil, err
			}
			if err := materializeArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, localTrackID, "track_artist", credits, policy, fence.AcceptedProjectionRevision); err != nil {
				return nil, err
			}
		}
		var variants []uuid.UUID
		if err := tx.NewRaw(`SELECT DISTINCT media_variant_id FROM source_work_variant_identity WHERE work_id=? ORDER BY media_variant_id`, member.WorkID).Scan(ctx, &variants); err != nil {
			return nil, fmt.Errorf("apply matching evaluation: read source work identity: %w", err)
		}
		if len(variants) == 0 {
			return nil, ErrIncomingMatchConflict
		}
		for _, variantID := range variants {
			if err := attachAutomaticTrackSource(ctx, tx, localTrackID, variantID, policy); err != nil {
				return nil, err
			}
		}
	}
	positions, err := matchingApplyPublicationPositions(ctx, tx, localReleaseID, localTrackIDs)
	if err != nil {
		return nil, err
	}
	return positions, nil
}

// matchingApplyPublicationPositions lists the release positions whose
// publication inputs one automatic materialization changed: every position of
// the mapped release plus any other release position of a shared local track.
func matchingApplyPublicationPositions(ctx context.Context, tx bun.Tx, releaseID uuid.UUID, trackIDs []uuid.UUID) ([]uuid.UUID, error) {
	query := `SELECT DISTINCT affected.id
		FROM release_track affected
		WHERE affected.release_id=?`
	args := []any{releaseID}
	if len(trackIDs) > 0 {
		query += ` OR affected.track_id IN (?)`
		args = append(args, bun.List(trackIDs))
	}
	query += ` ORDER BY affected.id`
	var positions []uuid.UUID
	if err := tx.NewRaw(query, args...).Scan(ctx, &positions); err != nil {
		return nil, fmt.Errorf("apply matching evaluation: read publication positions: %w", err)
	}
	return positions, nil
}

// requestMatchingApplyPublicationReconciliation records the reconciliation
// obligation of one automatic materialization. Callers hold the output admission
// gate and invoke this inside the materialization savepoint, so a conflicted
// apply leaves no intent behind. Replayed receipts never reach materialization
// and therefore never request a new generation.
func requestMatchingApplyPublicationReconciliation(ctx context.Context, database *bun.DB, tx bun.Tx, positions []uuid.UUID) error {
	if len(positions) == 0 {
		return nil
	}
	var outputEpoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &outputEpoch); err != nil {
		return fmt.Errorf("apply matching evaluation: read output epoch: %w", err)
	}
	if err := NewSetupManagerRepository(database).RequestPublicationReconciliationWith(ctx, tx, outputEpoch, positions, []string{"automatic_matching"}); err != nil {
		return fmt.Errorf("apply matching evaluation: request publication reconciliation: %w", err)
	}
	return nil
}

func ensureAutomaticLocalRelease(ctx context.Context, tx bun.Tx, providerID, providerReleaseID uuid.UUID, title, policy string) (uuid.UUID, bool, error) {
	return ensureLocalRelease(ctx, tx, providerID, providerReleaseID, title, materializationAuthority{method: "automatic", policyVersion: &policy})
}

type materializationAuthority struct {
	method        string
	policyVersion *string
}

func ensureLocalRelease(ctx context.Context, tx bun.Tx, providerID, providerReleaseID uuid.UUID, title string, authority materializationAuthority) (uuid.UUID, bool, error) {
	var localID uuid.UUID
	var method string
	err := tx.NewRaw(`SELECT local_release_id, method FROM provider_local_release WHERE provider_id=? AND provider_release_id=? FOR UPDATE`, providerID, providerReleaseID).Scan(ctx, &localID, &method)
	if err == nil {
		if method == "automatic" && authority.method == "manual" {
			if _, err := tx.NewRaw(`UPDATE provider_local_release SET method='manual', policy_version=NULL WHERE provider_id=? AND provider_release_id=?`, providerID, providerReleaseID).Exec(ctx); err != nil {
				return uuid.Nil, false, fmt.Errorf("apply matching evaluation: promote local release mapping: %w", err)
			}
		} else if method != authority.method {
			return uuid.Nil, false, ErrMatchingManualProtection
		}
		return localID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, err
	}
	localID = uuid.New()
	if _, err := tx.NewRaw(`INSERT INTO local_release(id,title) VALUES (?,?)`, localID, title).Exec(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("apply matching evaluation: create local release: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method,policy_version) VALUES (?,?,?,?,?)`, providerID, providerReleaseID, localID, authority.method, authority.policyVersion).Exec(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("apply matching evaluation: map provider release: %w", err)
	}
	return localID, true, nil
}

func ensureAutomaticLocalTrack(ctx context.Context, tx bun.Tx, providerID, providerTrackID, providerRecordingID, releaseID uuid.UUID, medium, position int, title, policy string) (uuid.UUID, uuid.UUID, bool, bool, error) {
	return ensureLocalTrack(ctx, tx, providerID, providerTrackID, providerRecordingID, releaseID, medium, position, title, materializationAuthority{method: "automatic", policyVersion: &policy})
}

func ensureLocalTrack(ctx context.Context, tx bun.Tx, providerID, providerTrackID, providerRecordingID, releaseID uuid.UUID, medium, position int, title string, authority materializationAuthority) (uuid.UUID, uuid.UUID, bool, bool, error) {
	var localID uuid.UUID
	var method string
	err := tx.NewRaw(`SELECT local_track_id, method FROM provider_local_track WHERE provider_id=? AND provider_track_id=? FOR UPDATE`, providerID, providerTrackID).Scan(ctx, &localID, &method)
	if err == nil {
		if method == "automatic" && authority.method == "manual" {
			if _, err := tx.NewRaw(`UPDATE provider_local_track SET method='manual', policy_version=NULL WHERE provider_id=? AND provider_track_id=?`, providerID, providerTrackID).Exec(ctx); err != nil {
				return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: promote local track mapping: %w", err)
			}
		} else if method != authority.method {
			return uuid.Nil, uuid.Nil, false, false, ErrMatchingManualProtection
		}
		var recordingID uuid.UUID
		if err := ensureManualRecordingAuthority(ctx, tx, providerID, providerRecordingID, authority); err != nil {
			return uuid.Nil, uuid.Nil, false, false, err
		}
		if err := tx.NewRaw(`SELECT local_recording_id FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=?`, providerID, providerRecordingID).Scan(ctx, &recordingID); err != nil {
			return uuid.Nil, uuid.Nil, false, false, err
		}
		return localID, recordingID, false, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, uuid.Nil, false, false, err
	}
	var recordingCreated bool
	var recordingID uuid.UUID
	err = tx.NewRaw(`SELECT local_recording_id FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=? FOR UPDATE`, providerID, providerRecordingID).Scan(ctx, &recordingID)
	if errors.Is(err, sql.ErrNoRows) {
		recordingID = uuid.New()
		if _, err := tx.NewRaw(`INSERT INTO local_recording(id,title) VALUES (?,?)`, recordingID, title).Exec(ctx); err != nil {
			return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: create local recording: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method,policy_version) VALUES (?,?,?,?,?)`, providerID, providerRecordingID, recordingID, authority.method, authority.policyVersion).Exec(ctx); err != nil {
			return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: map provider recording: %w", err)
		}
		recordingCreated = true
	} else if err != nil {
		return uuid.Nil, uuid.Nil, false, false, err
	} else if authority.method == "manual" {
		if err := ensureManualRecordingAuthority(ctx, tx, providerID, providerRecordingID, authority); err != nil {
			return uuid.Nil, uuid.Nil, false, false, err
		}
	}
	localID = uuid.New()
	if _, err := tx.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, localID, title).Exec(ctx); err != nil {
		return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: create local track: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method,policy_version) VALUES (?,?,?,?,?)`, providerID, providerTrackID, localID, authority.method, authority.policyVersion).Exec(ctx); err != nil {
		return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: map provider track: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO release_track(release_id,medium,position,track_id,recording_id) VALUES (?,?,?,?,?)`, releaseID, medium, position, localID, recordingID).Exec(ctx); err != nil {
		return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: add local release track: %w", err)
	}
	return localID, recordingID, recordingCreated, true, nil
}

// Manual mapping a track also promotes its shared recording mapping, whether
// the provider track mapping already existed or is being created now.
func ensureManualRecordingAuthority(ctx context.Context, tx bun.Tx, providerID, providerRecordingID uuid.UUID, authority materializationAuthority) error {
	var localID uuid.UUID
	var method string
	err := tx.NewRaw(`SELECT local_recording_id,method FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=? FOR UPDATE`, providerID, providerRecordingID).Scan(ctx, &localID, &method)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if method == authority.method {
		return nil
	}
	if method == "automatic" && authority.method == "manual" {
		_, err = tx.NewRaw(`UPDATE provider_local_recording SET method='manual',policy_version=NULL WHERE provider_id=? AND provider_recording_id=?`, providerID, providerRecordingID).Exec(ctx)
		return err
	}
	return ErrMatchingManualProtection
}

type acceptedArtistCredit struct {
	Position    int    `bun:"position"`
	ProviderKey string `bun:"provider_key"`
	Name        string `bun:"name"`
	JoinPhrase  string `bun:"join_phrase"`
	Authority   int    `bun:"authority"`
	FetchOrder  int64  `bun:"fetch_order"`
	SourceKind  string `bun:"source_kind"`
	SourceKey   string `bun:"source_key"`
	SourceField string `bun:"source_field"`
}

func acceptedReleaseArtistCredits(ctx context.Context, tx bun.Tx, providerID, sourceID, releaseID uuid.UUID) ([]acceptedArtistCredit, error) {
	var credits []acceptedArtistCredit
	err := tx.NewRaw(`SELECT c.position, a.provider_key, c.name, c.name_join_phrase AS join_phrase, c.authority, c.fetch_order, 'release' AS source_kind, r.provider_key AS source_key, 'artist-credit' AS source_field
		FROM provider_source_release_artist_credit c
		JOIN provider_artist a ON a.provider_id=c.provider_id AND a.id=c.artist_id
		JOIN provider_release r ON r.provider_id=c.provider_id AND r.id=c.release_id
		JOIN provider_source_entity e ON e.provider_source_id=c.provider_source_id AND e.provider_id=c.provider_id AND e.entity_kind='release' AND e.provider_key=r.provider_key
		WHERE c.provider_id=? AND c.provider_source_id=? AND c.release_id=? AND c.present=true
		AND COALESCE((e.field_evidence->'__credits'->>'authority')::integer, -1)=c.authority
		AND COALESCE((e.field_evidence->'__credits'->>'fetch_order')::bigint, -1)=c.fetch_order
		ORDER BY c.position`, providerID, sourceID, releaseID).Scan(ctx, &credits)
	if err != nil {
		return nil, fmt.Errorf("apply matching evaluation: read accepted release artist credits: %w", err)
	}
	return credits, nil
}

func acceptedRecordingArtistCredits(ctx context.Context, tx bun.Tx, providerID, sourceID, recordingID uuid.UUID) ([]acceptedArtistCredit, error) {
	var fields, evidence, recordingKey string
	err := tx.NewRaw(`SELECT e.fields, e.field_evidence, r.provider_key FROM provider_recording r
		JOIN provider_source_entity e ON e.provider_id=r.provider_id AND e.provider_key=r.provider_key AND e.entity_kind='recording'
	WHERE r.provider_id=? AND r.id=? AND e.provider_source_id=?`, providerID, recordingID, sourceID).Scan(ctx, &fields, &evidence, &recordingKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("apply matching evaluation: read accepted recording artist credits: %w", err)
	}
	var fieldEvidence map[string]ProviderFieldEvidence
	if err := json.Unmarshal([]byte(evidence), &fieldEvidence); err != nil {
		return nil, fmt.Errorf("apply matching evaluation: decode recording field evidence: %w", err)
	}
	creditEvidence := fieldEvidence["artist-credit"]
	var projection map[string]json.RawMessage
	if json.Unmarshal([]byte(fields), &projection) != nil {
		return nil, fmt.Errorf("apply matching evaluation: decode accepted recording projection")
	}
	var values []json.RawMessage
	if len(projection["artist-credit"]) == 0 || string(projection["artist-credit"]) == "null" {
		return nil, nil
	}
	if err := json.Unmarshal(projection["artist-credit"], &values); err != nil {
		return nil, fmt.Errorf("apply matching evaluation: decode accepted recording artist credits: %w", err)
	}
	credits := make([]acceptedArtistCredit, 0, len(values))
	for position, value := range values {
		var credit struct {
			Name       string          `json:"name"`
			JoinPhrase string          `json:"joinphrase"`
			Artist     json.RawMessage `json:"artist"`
		}
		if err := json.Unmarshal(value, &credit); err != nil {
			return nil, fmt.Errorf("apply matching evaluation: decode accepted recording artist credit: %w", err)
		}
		var artist struct {
			ID string `json:"id"`
		}
		if len(credit.Artist) == 0 || json.Unmarshal(credit.Artist, &artist) != nil || artist.ID == "" {
			continue
		}
		credits = append(credits, acceptedArtistCredit{
			Position: position, ProviderKey: strings.ToLower(artist.ID), Name: credit.Name, JoinPhrase: credit.JoinPhrase,
			Authority: creditEvidence.Authority, FetchOrder: creditEvidence.FetchOrder,
			SourceKind: "recording", SourceKey: recordingKey, SourceField: "artist-credit",
		})
	}
	return credits, nil
}

func acceptedTrackArtistCredits(fields, evidence json.RawMessage, medium, position int, recordingKey, releaseKey string) ([]acceptedArtistCredit, error) {
	var collection []struct {
		Medium               int              `json:"medium"`
		Position             int              `json:"position"`
		RecordingProviderKey string           `json:"recording_provider_key"`
		ArtistCredit         *json.RawMessage `json:"artist-credit"`
	}
	var projection map[string]json.RawMessage
	if err := json.Unmarshal(fields, &projection); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(projection["normalized_tracks"], &collection); err != nil {
		return nil, err
	}
	for _, track := range collection {
		if track.Medium != medium || track.Position != position || !strings.EqualFold(track.RecordingProviderKey, recordingKey) {
			continue
		}
		if track.ArtistCredit == nil {
			return nil, nil
		}
		var items []struct {
			Name       string `json:"name"`
			JoinPhrase string `json:"joinphrase"`
			Artist     struct {
				ID string `json:"id"`
			} `json:"artist"`
		}
		if err := json.Unmarshal(*track.ArtistCredit, &items); err != nil {
			return nil, err
		}
		var sourceEvidence map[string]ProviderFieldEvidence
		if err := json.Unmarshal(evidence, &sourceEvidence); err != nil {
			return nil, err
		}
		selected := sourceEvidence["normalized_tracks"]
		credits := make([]acceptedArtistCredit, 0, len(items))
		for index, item := range items {
			if item.Artist.ID == "" {
				continue
			}
			credits = append(credits, acceptedArtistCredit{
				Position: index, ProviderKey: strings.ToLower(item.Artist.ID), Name: item.Name, JoinPhrase: item.JoinPhrase,
				Authority: selected.Authority, FetchOrder: selected.FetchOrder,
				SourceKind: "release", SourceKey: releaseKey, SourceField: "normalized_tracks",
			})
		}
		return credits, nil
	}
	return nil, nil
}

func acceptedTrackTitle(fields json.RawMessage, medium, position int, recordingKey string) string {
	var projection struct {
		Tracks []struct {
			Medium               int    `json:"medium"`
			Position             int    `json:"position"`
			RecordingProviderKey string `json:"recording_provider_key"`
			Title                string `json:"title"`
		} `json:"normalized_tracks"`
	}
	if json.Unmarshal(fields, &projection) != nil {
		return ""
	}
	for _, track := range projection.Tracks {
		if track.Medium == medium && track.Position == position && strings.EqualFold(track.RecordingProviderKey, recordingKey) {
			return track.Title
		}
	}
	return ""
}

func writeAutomaticMetadata(ctx context.Context, tx bun.Tx, table string, id uuid.UUID, field, entityKey string, sourceID, providerID uuid.UUID, revision string, fieldEvidence json.RawMessage) error {
	return writeMatchingMetadata(ctx, tx, table, id, field, entityKey, sourceID, providerID, revision, fieldEvidence, "automatic_matching")
}

func writeManualMetadata(ctx context.Context, tx bun.Tx, table string, id uuid.UUID, field, entityKey string, sourceID, providerID uuid.UUID, revision string, fieldEvidence json.RawMessage) error {
	return writeMatchingMetadata(ctx, tx, table, id, field, entityKey, sourceID, providerID, revision, fieldEvidence, "manual_matching")
}

func writeMatchingMetadata(ctx context.Context, tx bun.Tx, table string, id uuid.UUID, field, entityKey string, sourceID, providerID uuid.UUID, revision string, fieldEvidence json.RawMessage, provenanceKey string) error {
	if table != "local_artist" && table != "local_release" && table != "local_recording" && table != "local_track" {
		return fmt.Errorf("apply matching evaluation: invalid provenance table")
	}
	var evidence map[string]ProviderFieldEvidence
	if err := json.Unmarshal(fieldEvidence, &evidence); err != nil {
		return err
	}
	entityKind := map[string]string{"local_release": "release", "local_recording": "recording", "local_track": "release"}[table]
	if table == "local_track" && field == "title" {
		entityKind = "recording"
	}
	metadata, err := json.Marshal(map[string]any{provenanceKey: map[string]any{
		"source_id": sourceID, "provider_id": providerID, "source_entity_id": entityKey,
		"projection_revision": revision, "field_evidence": map[string]ProviderFieldEvidence{field: evidence[field]},
		"provider_fields_reference": map[string]any{
			"entity_kind":  entityKind,
			"provider_key": entityKey, "fields": []string{field},
		},
	}})
	if err != nil {
		return err
	}
	_, err = tx.NewRaw(`UPDATE `+table+` SET metadata=COALESCE(metadata,'{}'::jsonb) || jsonb_build_object(?, COALESCE(metadata->?, '{}'::jsonb) || (?::jsonb->?)), updated_at=now() WHERE id=?`, provenanceKey, provenanceKey, string(metadata), provenanceKey, id).Exec(ctx)
	return err
}

func recordingProviderKey(ctx context.Context, tx bun.Tx, providerID, recordingID uuid.UUID) string {
	var key string
	_ = tx.NewRaw(`SELECT provider_key FROM provider_recording WHERE provider_id=? AND id=?`, providerID, recordingID).Scan(ctx, &key)
	return key
}

func materializeArtistCredits(ctx context.Context, tx bun.Tx, providerID, sourceID, parentID uuid.UUID, relation string, credits []acceptedArtistCredit, policy, projectionRevision string, manualAuthority ...bool) error {
	manual := len(manualAuthority) != 0 && manualAuthority[0]
	parentColumn := map[string]string{"release_artist": "release_id", "recording_artist": "recording_id", "track_artist": "track_id"}[relation]
	if parentColumn == "" {
		return fmt.Errorf("apply matching evaluation: invalid artist relation")
	}
	for _, credit := range credits {
		var providerArtistID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM provider_artist WHERE provider_id=? AND provider_key=?`, providerID, credit.ProviderKey).Scan(ctx, &providerArtistID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return fmt.Errorf("apply matching evaluation: resolve provider artist: %w", err)
		}
		var acceptedName string
		var artistFields, artistEvidence string
		err := tx.NewRaw(`SELECT name, fields, field_evidence FROM provider_source_entity
			WHERE provider_source_id=? AND provider_id=? AND entity_kind='artist' AND provider_key=?`, sourceID, providerID, credit.ProviderKey).Scan(ctx, &acceptedName, &artistFields, &artistEvidence)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("apply matching evaluation: read accepted artist projection: %w", err)
		}
		var projection map[string]json.RawMessage
		_ = json.Unmarshal([]byte(artistFields), &projection)
		if acceptedName == "" {
			_ = json.Unmarshal(projection["name"], &acceptedName)
		}
		var fieldEvidence map[string]ProviderFieldEvidence
		_ = json.Unmarshal([]byte(artistEvidence), &fieldEvidence)
		nameEvidence, hasNameEvidence := fieldEvidence["name"]
		nameUsable := strings.TrimSpace(acceptedName) != "" && hasNameEvidence
		if !nameUsable {
			acceptedName = credit.Name
		}
		var artistID uuid.UUID
		err = tx.NewRaw(`SELECT local_artist_id FROM provider_local_artist WHERE provider_id=? AND provider_artist_id=? FOR UPDATE`, providerID, providerArtistID).Scan(ctx, &artistID)
		if errors.Is(err, sql.ErrNoRows) {
			if acceptedName == "" {
				continue
			}
			artistID = uuid.New()
			nameField := "name"
			sourceKind, sourceKey := "artist", credit.ProviderKey
			if !nameUsable {
				nameEvidence = ProviderFieldEvidence{Authority: credit.Authority, FetchOrder: credit.FetchOrder}
				nameField = credit.SourceField
				if nameField == "" {
					nameField = "artist-credit"
				}
				if credit.SourceKind != "" {
					sourceKind = credit.SourceKind
				}
				if credit.SourceKey != "" {
					sourceKey = credit.SourceKey
				}
			}
			provenanceKey, mappingMethod := "automatic_matching", "automatic"
			if manual {
				provenanceKey, mappingMethod = "manual_matching", "manual"
			}
			provenance, marshalErr := json.Marshal(map[string]any{provenanceKey: map[string]any{
				"source_id": sourceID, "provider_id": providerID, "provider_artist_id": providerArtistID,
				"projection_revision": projectionRevision, "policy_version": policy,
				"source_entity_id": sourceKey, "field_evidence": map[string]ProviderFieldEvidence{nameField: nameEvidence},
				"name_source":               nameField,
				"provider_fields_reference": map[string]any{"entity_kind": sourceKind, "provider_key": sourceKey, "fields": []string{nameField}},
			}})
			if marshalErr != nil {
				return fmt.Errorf("apply matching evaluation: encode artist provenance: %w", marshalErr)
			}
			if _, err := tx.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?,?,?::jsonb)`, artistID, acceptedName, string(provenance)).Exec(ctx); err != nil {
				return fmt.Errorf("apply matching evaluation: create local artist: %w", err)
			}
			if _, err := tx.NewRaw(`INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method,policy_version) VALUES (?,?,?,?,?)`, providerID, providerArtistID, artistID, mappingMethod, optionalPolicy(policy, manual)).Exec(ctx); err != nil {
				return fmt.Errorf("apply matching evaluation: map provider artist: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("apply matching evaluation: read local artist mapping: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO `+relation+`(`+parentColumn+`,position,artist_id,name,name_join_phrase) VALUES (?,?,?,?,?) ON CONFLICT DO NOTHING`, parentID, credit.Position, artistID, credit.Name, credit.JoinPhrase).Exec(ctx); err != nil {
			return fmt.Errorf("apply matching evaluation: add %s credit: %w", relation, err)
		}
	}
	return nil
}

func optionalPolicy(policy string, manual bool) any {
	if manual {
		return nil
	}
	return policy
}

func attachAutomaticTrackSource(ctx context.Context, tx bun.Tx, trackID, variantID uuid.UUID, policy string) error {
	return attachTrackSource(ctx, tx, trackID, variantID, materializationAuthority{method: "automatic", policyVersion: &policy})
}

func attachTrackSource(ctx context.Context, tx bun.Tx, trackID, variantID uuid.UUID, authority materializationAuthority) error {
	if err := rejectCleanupOwnedSourceVariant(ctx, tx, variantID); err != nil {
		return fmt.Errorf("apply matching evaluation: %w", err)
	}
	var existing []struct {
		TrackID uuid.UUID `bun:"track_id"`
		Method  string    `bun:"decision_method"`
	}
	if err := tx.NewRaw(`SELECT track_id, decision_method FROM track_source WHERE media_variant_id=? ORDER BY track_id FOR UPDATE`, variantID).Scan(ctx, &existing); err != nil {
		return fmt.Errorf("apply matching evaluation: inspect track source: %w", err)
	}
	for _, source := range existing {
		if authority.method == "automatic" && source.Method == "manual" && source.TrackID != trackID {
			return fmt.Errorf("apply matching evaluation: %w", ErrMatchingManualProtection)
		}
	}
	for _, source := range existing {
		if source.TrackID == trackID {
			if source.Method == "automatic" && authority.method == "manual" {
				if _, err := tx.NewRaw(`UPDATE track_source SET decision_method='manual', policy_version=NULL WHERE track_id=? AND media_variant_id=? AND decision_method='automatic'`, trackID, variantID).Exec(ctx); err != nil {
					return fmt.Errorf("apply matching evaluation: promote source association: %w", err)
				}
			} else if authority.method == "automatic" && source.Method != authority.method {
				return fmt.Errorf("apply matching evaluation: %w", ErrMatchingManualProtection)
			}
			return nil
		}
	}
	if _, err := tx.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version) VALUES (?,?,?,false,true,?,?)`, uuid.New(), trackID, variantID, authority.method, authority.policyVersion).Exec(ctx); err != nil {
		return fmt.Errorf("apply matching evaluation: attach source variant: %w", err)
	}
	return nil
}

// applyFrozenMatchingOperationTx applies schema-2 requests exclusively from the
// persisted unique decision and the operation's frozen provider projection.
func (repository *SetupManagerRepository) applyFrozenMatchingOperationTx(
	ctx context.Context, tx bun.Tx, operation *Operation, operationID uuid.UUID, riverJobID, executionEpoch int64, currentProviderConfiguration bool,
) (bool, error) {
	var liveDelivery bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM river_job WHERE id=? AND state='running' AND attempt=?)`, riverJobID, operation.Attempt).Scan(ctx, &liveDelivery); err != nil {
		return false, fmt.Errorf("apply matching operation: validate River delivery: %w", err)
	}
	if !liveDelivery || operation.ProviderExecutionEpoch != executionEpoch || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
		return false, nil
	}
	snapshot, err := DecodeMatchingApplyInputSnapshotV2(operation.InputSnapshot)
	if err != nil {
		return false, fmt.Errorf("apply matching operation: immutable schema-2 snapshot is invalid: %w", err)
	}
	inputDigest, err := MatchingApplyInputSnapshotV2Digest(snapshot)
	if err != nil {
		return false, fmt.Errorf("apply matching operation: digest schema-2 snapshot: %w", err)
	}
	var request struct {
		GroupID            uuid.UUID `bun:"group_id"`
		ParentRunID        uuid.UUID `bun:"parent_run_id"`
		EvaluationRevision string    `bun:"evaluation_revision"`
		SchemaVersion      int       `bun:"schema_version"`
		InputSHA           []byte    `bun:"input_sha256"`
		AuthorityVersion   int       `bun:"frozen_authority_version"`
		ParentEpoch        int64     `bun:"parent_execution_epoch"`
		WaitRevision       int64     `bun:"seal_wait_revision"`
		SealDigest         []byte    `bun:"seal_digest"`
		TargetDigest       []byte    `bun:"target_capture_sha256"`
	}
	if err := tx.NewRaw(`SELECT group_id,parent_run_id,evaluation_revision,schema_version,input_sha256,
		frozen_authority_version,parent_execution_epoch,seal_wait_revision,seal_digest,target_capture_sha256
		FROM matching_apply_request WHERE apply_operation_id=? FOR UPDATE`, operationID).Scan(ctx, &request); err != nil {
		return false, fmt.Errorf("apply matching operation: read frozen apply request: %w", err)
	}
	if request.SchemaVersion != 2 || request.GroupID != snapshot.Authority.GroupID || request.ParentRunID != snapshot.Authority.ParentRunID ||
		request.EvaluationRevision != snapshot.Authority.EvaluationRevision || request.AuthorityVersion != snapshot.Authority.AuthorityVersion ||
		request.WaitRevision != snapshot.Authority.WaitRevision || !equalDigest(request.InputSHA, mustDecodeHexDigest(inputDigest)) ||
		!equalDigest(request.SealDigest, mustDecodeHexDigest(snapshot.Authority.SealDigest)) ||
		!equalDigest(request.TargetDigest, mustDecodeHexDigest(snapshot.Authority.TargetCaptureDigest)) {
		return false, fmt.Errorf("apply matching operation: schema-2 request binding is corrupted")
	}
	var prior struct {
		Outcome  string `bun:"outcome"`
		InputSHA []byte `bun:"input_sha256"`
	}
	err = tx.NewRaw(`SELECT outcome,input_sha256 FROM matching_apply_receipt WHERE apply_operation_id=?`, operationID).Scan(ctx, &prior)
	if err == nil {
		if prior.Outcome == "" || !equalDigest(prior.InputSHA, request.InputSHA) {
			return false, fmt.Errorf("apply matching operation: schema-2 receipt binding is corrupted")
		}
		settleMatchingApplyOperation(operation, prior.Outcome)
		if err := saveMatchingApplyOperation(ctx, tx, operation); err != nil {
			return false, err
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("apply matching operation: read schema-2 receipt: %w", err)
	}
	if !currentProviderConfiguration {
		return repository.finishFrozenApplyReview(ctx, tx, operation, operationID, request.GroupID, request.InputSHA, "review_required", "The matching provider configuration changed; review this group manually.")
	}
	seal, found, err := buildMatchingFallbackSealAuthority(ctx, tx, request.ParentRunID, request.WaitRevision, true)
	if err != nil {
		return false, fmt.Errorf("apply matching operation: validate historical seal: %w", err)
	}
	if !found {
		return false, fmt.Errorf("apply matching operation: historical seal is missing")
	}
	calculation, err := matchingApplyHistoricalCalculationContext(ctx, tx, seal, request.ParentRunID, request.WaitRevision)
	if err != nil {
		return false, fmt.Errorf("apply matching operation: validate historical calculation: %w", err)
	}
	var header struct {
		Evidence     json.RawMessage `bun:"evaluation_evidence"`
		Revision     string          `bun:"evaluation_revision"`
		FenceVersion int             `bun:"fence_version"`
		TargetRunID  *uuid.UUID      `bun:"target_run_id"`
	}
	if err := tx.NewRaw(`SELECT evaluation_evidence,evaluation_revision,fence_version,target_run_id FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, request.GroupID).Scan(ctx, &header); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repository.finishFrozenApplyReview(ctx, tx, operation, operationID, request.GroupID, request.InputSHA, "review_required", "The matching decision is stale or conflicts with a manual mapping; review this group manually.")
		}
		return false, fmt.Errorf("apply matching operation: read persisted evaluation: %w", err)
	}
	decision, err := decodePersistedMatchingDecision(header.Evidence)
	if err != nil {
		return false, fmt.Errorf("apply matching operation: persisted decision is invalid: %w", err)
	}
	if header.Revision != request.EvaluationRevision || (header.FenceVersion != 2 || header.TargetRunID == nil || *header.TargetRunID != request.ParentRunID) {
		return repository.finishFrozenApplyReview(ctx, tx, operation, operationID, request.GroupID, request.InputSHA, "review_required", "The matching decision is stale or conflicts with a manual mapping; review this group manually.")
	}
	if err := ValidateMatchingApplyInputSnapshotV2(snapshot, seal, calculation, request.EvaluationRevision, decision); err != nil {
		return false, fmt.Errorf("apply matching operation: schema-2 snapshot authority is corrupted: %w", err)
	}
	var parentEpoch int64
	var parentState string
	if err := tx.NewRaw(`SELECT execution_epoch,state FROM matching_run WHERE id=?`, request.ParentRunID).Scan(ctx, &parentEpoch, &parentState); err != nil {
		return false, fmt.Errorf("apply matching operation: read original matching run: %w", err)
	}
	if parentEpoch != request.ParentEpoch || parentState != "succeeded" {
		return repository.finishFrozenApplyReview(ctx, tx, operation, operationID, request.GroupID, request.InputSHA, "review_required", "The original matching run is no longer current; review this group manually.")
	}
	var current bool
	if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_run_target target
		JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
		WHERE target.run_id=? AND target.group_id=? AND generation.current_run_id=target.run_id
		AND generation.high_water=target.target_generation
		AND COALESCE((SELECT authority.mode FROM incoming_group_matching_authority authority WHERE authority.group_id=target.group_id),'automatic')='automatic'
		AND COALESCE((SELECT authority.revision FROM incoming_group_matching_authority authority WHERE authority.group_id=target.group_id),0)=target.authority_revision)`, request.ParentRunID, request.GroupID).Scan(ctx, &current); err != nil {
		return false, fmt.Errorf("apply matching operation: validate frozen target: %w", err)
	}
	if !current {
		return repository.finishFrozenApplyReview(ctx, tx, operation, operationID, request.GroupID, request.InputSHA, "review_required", "The original matching run is no longer current; review this group manually.")
	}
	if err := validateMatchingRunFenceCurrentTarget(ctx, tx, request.ParentRunID); err != nil {
		if errors.Is(err, ErrIncomingMatchConflict) {
			return repository.finishFrozenApplyReview(ctx, tx, operation, operationID, request.GroupID, request.InputSHA, "review_required", "The original matching run is no longer current; review this group manually.")
		}
		return false, fmt.Errorf("apply matching operation: revalidate frozen target capture: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT matching_apply_materialization`); err != nil {
		return false, fmt.Errorf("apply matching operation: create schema-2 savepoint: %w", err)
	}
	materializeErr := materializeFrozenMatchingApply(ctx, tx, snapshot, decision)
	if errors.Is(materializeErr, ErrIncomingMatchConflict) || errors.Is(materializeErr, ErrMatchingManualProtection) {
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT matching_apply_materialization`); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT matching_apply_materialization`); err != nil {
			return false, err
		}
		return repository.finishFrozenApplyReview(ctx, tx, operation, operationID, request.GroupID, request.InputSHA, "review_required", "The matching decision is stale or conflicts with a manual mapping; review this group manually.")
	}
	if materializeErr != nil {
		return false, materializeErr
	}
	positions, err := frozenMatchingApplyPublicationPositions(ctx, tx, snapshot)
	if err != nil {
		return false, err
	}
	if err := requestMatchingApplyPublicationReconciliation(ctx, repository.db, tx, positions); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT matching_apply_materialization`); err != nil {
		return false, err
	}
	return repository.finishFrozenApplyReview(ctx, tx, operation, operationID, request.GroupID, request.InputSHA, "applied", "")
}

// matchingApplyHistoricalCalculationContext binds the evaluation context to the
// original wait's verified seal, then revalidates the immutable database receipt
// named by that seal. The live acquisition cursor may already have advanced.
func matchingApplyHistoricalCalculationContext(
	ctx context.Context, database bun.IDB, seal MatchingFallbackSealAuthority, runID uuid.UUID, waitRevision int64,
) (MatchingFallbackCalculationContext, error) {
	if seal.RunID != runID || seal.WaitRevision != waitRevision || seal.SelectedCalculation == nil || len(seal.FullEvaluationContext) == 0 {
		return MatchingFallbackCalculationContext{}, fmt.Errorf("historical calculation authority is missing")
	}
	ref := seal.SelectedCalculation
	if ref.AcquisitionRevision < 1 || ref.AcquisitionRevision >= waitRevision || ref.StageIndex < 0 {
		return MatchingFallbackCalculationContext{}, fmt.Errorf("historical calculation reference is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(seal.FullEvaluationContext))
	decoder.DisallowUnknownFields()
	var calculation MatchingFallbackCalculationContext
	if err := decoder.Decode(&calculation); err != nil {
		return MatchingFallbackCalculationContext{}, fmt.Errorf("decode original sealed calculation context: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return MatchingFallbackCalculationContext{}, fmt.Errorf("original sealed calculation context has trailing data")
	}
	if calculation.RunID != runID || calculation.AcquisitionRevision != ref.AcquisitionRevision || calculation.StageIndex != ref.StageIndex {
		return MatchingFallbackCalculationContext{}, fmt.Errorf("original sealed calculation context identity is invalid")
	}
	receipt, err := getMatchingFallbackCalculationReceipt(ctx, database, runID, ref.AcquisitionRevision)
	if err != nil {
		return MatchingFallbackCalculationContext{}, fmt.Errorf("validate immutable calculation receipt: %w", err)
	}
	if receipt == nil {
		return MatchingFallbackCalculationContext{}, fmt.Errorf("referenced immutable calculation receipt is missing")
	}
	if receipt.AcquisitionRevision != ref.AcquisitionRevision || receipt.StageIndex != ref.StageIndex ||
		receipt.SchemaVersion != ref.SchemaVersion || receipt.CalculatorVersion != ref.CalculatorVersion ||
		receipt.DigestVersion != ref.DigestVersion || receipt.InputDigest != ref.InputDigest ||
		!matchingJSONEqual(receipt.Context, seal.FullEvaluationContext) {
		return MatchingFallbackCalculationContext{}, fmt.Errorf("immutable calculation receipt differs from original seal")
	}
	return calculation, nil
}

func mustDecodeHexDigest(value string) []byte { decoded, _ := hex.DecodeString(value); return decoded }

func settleMatchingApplyOperation(operation *Operation, outcome string) {
	now := time.Now().UTC()
	operation.State, operation.Stage, operation.UpdatedAt, operation.FinishedAt = "succeeded", outcome, now, &now
	if outcome == "review_required" {
		reason := "The matching decision is stale or conflicts with a manual mapping; review this group manually."
		operation.SafeError = &reason
	} else {
		operation.SafeError = nil
	}
}

func saveMatchingApplyOperation(ctx context.Context, tx bun.Tx, operation *Operation) error {
	if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "safe_error", "finished_at", "updated_at").WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("apply matching operation: complete operation: %w", err)
	}
	return nil
}

func (repository *SetupManagerRepository) finishFrozenApplyReview(ctx context.Context, tx bun.Tx, operation *Operation, operationID, groupID uuid.UUID, inputSHA []byte, outcome, safeError string) (bool, error) {
	if _, err := tx.NewRaw(`INSERT INTO matching_apply_receipt(apply_operation_id,group_id,outcome,input_sha256) VALUES (?,?,?,?)`, operationID, groupID, outcome, inputSHA).Exec(ctx); err != nil {
		// The request's target group is independently sealed in the request binding.
		return false, fmt.Errorf("apply matching operation: persist schema-2 receipt: %w", err)
	}
	settleMatchingApplyOperation(operation, outcome)
	if safeError != "" {
		operation.SafeError = &safeError
	}
	if err := saveMatchingApplyOperation(ctx, tx, operation); err != nil {
		return false, err
	}
	return true, nil
}

func materializeFrozenMatchingApply(ctx context.Context, tx bun.Tx, snapshot MatchingApplyInputSnapshotV2, decision persistedMatchingDecision) error {
	frozen := snapshot.Selected.Projection
	if frozen.SchemaVersion != 2 || frozen.Release.ID != snapshot.Selected.ProviderReleaseID || frozen.Release.ProviderKey != snapshot.Selected.ProviderKey ||
		decision.SelectedRelease == nil || decision.SelectedRelease.ProviderReleaseID != frozen.Release.ID ||
		!strings.EqualFold(decision.SelectedRelease.ProviderReleaseKey, frozen.Release.ProviderKey) || len(snapshot.Assignments) != len(decision.Assignments) {
		return fmt.Errorf("apply matching operation: frozen projection and persisted decision disagree")
	}
	var endpoint, configuration string
	if err := tx.NewRaw(`SELECT endpoint,configuration_identity FROM provider_source WHERE id=? AND provider_id=? FOR UPDATE`, snapshot.Source.ID, snapshot.Source.ProviderID).Scan(ctx, &endpoint, &configuration); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIncomingMatchConflict
		}
		return fmt.Errorf("apply matching operation: validate frozen provider source configuration: %w", err)
	}
	if endpoint != snapshot.Source.Endpoint || configuration != snapshot.Source.ConfigurationIdentity {
		return ErrIncomingMatchConflict
	}
	var releaseKey string
	if err := tx.NewRaw(`SELECT provider_key FROM provider_release WHERE provider_id=? AND id=?`, snapshot.Source.ProviderID, frozen.Release.ID).Scan(ctx, &releaseKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIncomingMatchConflict
		}
		return fmt.Errorf("apply matching operation: resolve frozen provider release: %w", err)
	}
	if !strings.EqualFold(releaseKey, frozen.Release.ProviderKey) {
		return ErrIncomingMatchConflict
	}
	policy := snapshot.Authority.PolicyVersion
	tracks := make(map[uuid.UUID]FrozenRecordingTrack, len(frozen.Tracks))
	for _, track := range frozen.Tracks {
		if track.ProviderTrackID == uuid.Nil || track.ProviderReleaseID != frozen.Release.ID || tracks[track.ProviderTrackID].ProviderTrackID != uuid.Nil {
			return fmt.Errorf("apply matching operation: frozen track identity is invalid")
		}
		tracks[track.ProviderTrackID] = track
	}
	assignments := make(map[uuid.UUID]MatchingApplySnapshotAssignment, len(snapshot.Assignments))
	for _, assignment := range snapshot.Assignments {
		if assignment.FileMemberID == uuid.Nil || assignment.WorkID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil || assignments[assignment.FileMemberID].FileMemberID != uuid.Nil {
			return fmt.Errorf("apply matching operation: frozen assignment identity is invalid")
		}
		assignments[assignment.FileMemberID] = assignment
	}
	localReleaseID, releaseCreated, err := ensureAutomaticLocalRelease(ctx, tx, snapshot.Source.ProviderID, frozen.Release.ID, frozen.Release.Title, policy)
	if err != nil {
		return err
	}
	if releaseCreated {
		if err := writeFrozenMetadata(ctx, tx, "local_release", localReleaseID, "title", releaseKey, snapshot.Source, snapshot.Authority.EvaluationRevision, frozen.Release.FieldSources["title"]); err != nil {
			return err
		}
		if err := materializeFrozenArtistCredits(ctx, tx, snapshot.Source, localReleaseID, "release_artist", frozen.Release.Credits, policy, snapshot.Authority.EvaluationRevision); err != nil {
			return err
		}
	}
	for _, assignment := range decision.Assignments {
		frozenAssignment, ok := assignments[assignment.FileMemberID]
		track, trackOK := tracks[assignment.ProviderTrackID]
		if !ok || !trackOK || frozenAssignment.ProviderTrackID != assignment.ProviderTrackID || frozenAssignment.Medium+1 != assignment.Medium || frozenAssignment.Position+1 != assignment.Position || track.Medium+1 != assignment.Medium || track.Position+1 != assignment.Position {
			return ErrIncomingMatchConflict
		}
		var providerTrackID, providerRecordingID uuid.UUID
		var providerRecordingKey string
		var medium, position int
		if err := tx.NewRaw(`SELECT track.id,track.recording_id,recording.provider_key,track.medium,track.position
			FROM provider_release_track track JOIN provider_recording recording
			ON recording.provider_id=track.provider_id AND recording.id=track.recording_id
			WHERE track.provider_id=? AND track.id=? AND track.release_id=?`,
			snapshot.Source.ProviderID, track.ProviderTrackID, frozen.Release.ID).Scan(ctx, &providerTrackID, &providerRecordingID, &providerRecordingKey, &medium, &position); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIncomingMatchConflict
			}
			return fmt.Errorf("apply matching operation: resolve frozen provider track: %w", err)
		}
		if providerTrackID != track.ProviderTrackID || !strings.EqualFold(providerRecordingKey, track.RecordingProviderKey) {
			return ErrIncomingMatchConflict
		}
		medium, position = track.Medium, track.Position
		localRecordingID, recordingCreated, err := ensureAutomaticLocalRecording(ctx, tx, snapshot.Source.ProviderID, providerRecordingID, track.RecordingTitle, policy)
		if err != nil {
			return err
		}
		if recordingCreated {
			source := FrozenFieldSource{EntityKind: "recording", ProviderKey: track.RecordingProviderKey, Field: "title"}
			if track.RecordingTitleSource != nil {
				source = *track.RecordingTitleSource
			}
			if err := writeFrozenMetadata(ctx, tx, "local_recording", localRecordingID, "title", track.RecordingProviderKey, snapshot.Source, snapshot.Authority.EvaluationRevision, source); err != nil {
				return err
			}
			if err := materializeFrozenArtistCredits(ctx, tx, snapshot.Source, localRecordingID, "recording_artist", track.RecordingCredits, policy, snapshot.Authority.EvaluationRevision); err != nil {
				return err
			}
		}
		localTrackID, mappedRecordingID, _, trackCreated, err := ensureAutomaticLocalTrack(ctx, tx, snapshot.Source.ProviderID, providerTrackID, providerRecordingID, localReleaseID, medium, position, track.Title, policy)
		if err != nil {
			return err
		}
		if mappedRecordingID != localRecordingID {
			return ErrIncomingMatchConflict
		}
		if trackCreated {
			field, key, source := "title", track.RecordingProviderKey, FrozenFieldSource{EntityKind: "recording", ProviderKey: track.RecordingProviderKey, Field: "title"}
			if track.TitleSource != nil {
				field, key, source = track.TitleSource.Field, track.TitleSource.ProviderKey, *track.TitleSource
			}
			if err := writeFrozenMetadata(ctx, tx, "local_track", localTrackID, field, key, snapshot.Source, snapshot.Authority.EvaluationRevision, source); err != nil {
				return err
			}
			if err := materializeFrozenArtistCredits(ctx, tx, snapshot.Source, localTrackID, "track_artist", track.Credits, policy, snapshot.Authority.EvaluationRevision); err != nil {
				return err
			}
		}
		var variants []uuid.UUID
		if err := tx.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=? ORDER BY media_variant_id`, frozenAssignment.WorkID).Scan(ctx, &variants); err != nil {
			return fmt.Errorf("apply matching operation: read captured source work: %w", err)
		}
		if len(variants) == 0 {
			return ErrIncomingMatchConflict
		}
		for _, variantID := range variants {
			if err := attachAutomaticTrackSource(ctx, tx, localTrackID, variantID, policy); err != nil {
				return err
			}
		}
	}
	return nil
}

// frozenMatchingApplyPublicationPositions resolves the local release and tracks
// that materializeFrozenMatchingApply mapped from the immutable snapshot.
func frozenMatchingApplyPublicationPositions(ctx context.Context, tx bun.Tx, snapshot MatchingApplyInputSnapshotV2) ([]uuid.UUID, error) {
	var localReleaseID uuid.UUID
	if err := tx.NewRaw(`SELECT local_release_id FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`,
		snapshot.Source.ProviderID, snapshot.Selected.ProviderReleaseID).Scan(ctx, &localReleaseID); err != nil {
		return nil, fmt.Errorf("apply matching operation: read materialized local release: %w", err)
	}
	trackIDs := make([]uuid.UUID, 0, len(snapshot.Assignments))
	for _, assignment := range snapshot.Assignments {
		var localTrackID uuid.UUID
		if err := tx.NewRaw(`SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id=?`,
			snapshot.Source.ProviderID, assignment.ProviderTrackID).Scan(ctx, &localTrackID); err != nil {
			return nil, fmt.Errorf("apply matching operation: read materialized local track: %w", err)
		}
		trackIDs = append(trackIDs, localTrackID)
	}
	return matchingApplyPublicationPositions(ctx, tx, localReleaseID, trackIDs)
}

func ensureManualLocalRecording(ctx context.Context, tx bun.Tx, providerID, providerRecordingID uuid.UUID, title string) (uuid.UUID, bool, error) {
	var localID uuid.UUID
	err := tx.NewRaw(`SELECT local_recording_id FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=? FOR UPDATE`, providerID, providerRecordingID).Scan(ctx, &localID)
	if err == nil {
		if err := ensureManualRecordingAuthority(ctx, tx, providerID, providerRecordingID, materializationAuthority{method: "manual"}); err != nil {
			return uuid.Nil, false, err
		}
		return localID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, err
	}
	localID = uuid.New()
	if _, err := tx.NewRaw(`INSERT INTO local_recording(id,title) VALUES (?,?)`, localID, title).Exec(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("apply matching evaluation: create local recording: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method,policy_version) VALUES (?,?,?,'manual',NULL)`, providerID, providerRecordingID, localID).Exec(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("apply matching evaluation: map provider recording: %w", err)
	}
	return localID, true, nil
}

// ensureAutomaticLocalRecording keeps the provider recording's title separate
// from any release-track title override. The caller holds the stable provider
// recording row lock before entering this helper.
func ensureAutomaticLocalRecording(ctx context.Context, tx bun.Tx, providerID, providerRecordingID uuid.UUID, title, policy string) (uuid.UUID, bool, error) {
	var localID uuid.UUID
	var method string
	err := tx.NewRaw(`SELECT local_recording_id,method FROM provider_local_recording
		WHERE provider_id=? AND provider_recording_id=? FOR UPDATE`, providerID, providerRecordingID).Scan(ctx, &localID, &method)
	if err == nil {
		if method != "automatic" {
			return uuid.Nil, false, ErrMatchingManualProtection
		}
		return localID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, fmt.Errorf("apply matching operation: read local recording mapping: %w", err)
	}
	localID = uuid.New()
	if _, err := tx.NewRaw(`INSERT INTO local_recording(id,title) VALUES (?,?)`, localID, title).Exec(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("apply matching operation: create local recording: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method,policy_version) VALUES (?,?,?,?,?)`, providerID, providerRecordingID, localID, "automatic", policy).Exec(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("apply matching operation: map provider recording: %w", err)
	}
	return localID, true, nil
}

func writeFrozenMetadata(ctx context.Context, tx bun.Tx, table string, id uuid.UUID, field, entityKey string, source MatchingApplySnapshotSourceV2, revision string, fieldSource FrozenFieldSource) error {
	evidence, err := json.Marshal(map[string]ProviderFieldEvidence{field: {Authority: fieldSource.Authority, FetchOrder: fieldSource.FetchOrder}})
	if err != nil {
		return err
	}
	return writeAutomaticMetadata(ctx, tx, table, id, field, entityKey, source.ID, source.ProviderID, revision, evidence)
}

func materializeFrozenArtistCredits(ctx context.Context, tx bun.Tx, source MatchingApplySnapshotSourceV2, parentID uuid.UUID, relation string, credits []FrozenArtistCredit, policy, revision string) error {
	parentColumn := map[string]string{"release_artist": "release_id", "recording_artist": "recording_id", "track_artist": "track_id"}[relation]
	if parentColumn == "" {
		return fmt.Errorf("apply matching operation: invalid frozen artist relation")
	}
	providerArtistIDs := make(map[string]uuid.UUID, len(credits))
	for _, credit := range credits {
		key := strings.ToLower(strings.TrimSpace(credit.ArtistProviderKey))
		if key == "" {
			return ErrIncomingMatchConflict
		}
		if _, exists := providerArtistIDs[key]; exists {
			continue
		}
		var providerArtistID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM provider_artist WHERE provider_id=? AND provider_key=?`, source.ProviderID, key).Scan(ctx, &providerArtistID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIncomingMatchConflict
			}
			return fmt.Errorf("apply matching operation: resolve frozen artist identity: %w", err)
		}
		providerArtistIDs[key] = providerArtistID
	}
	orderedIDs := make([]uuid.UUID, 0, len(providerArtistIDs))
	for _, id := range providerArtistIDs {
		orderedIDs = append(orderedIDs, id)
	}
	sort.Slice(orderedIDs, func(i, j int) bool { return orderedIDs[i].String() < orderedIDs[j].String() })
	for _, id := range orderedIDs {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "frozen-apply-artist:"+source.ProviderID.String()+":"+id.String()); err != nil {
			return fmt.Errorf("apply matching operation: lock frozen artist identity: %w", err)
		}
	}
	for _, credit := range credits {
		providerArtistID := providerArtistIDs[strings.ToLower(credit.ArtistProviderKey)]
		var artistID uuid.UUID
		var method string
		err := tx.NewRaw(`SELECT local_artist_id,method FROM provider_local_artist WHERE provider_id=? AND provider_artist_id=? FOR UPDATE`, source.ProviderID, providerArtistID).Scan(ctx, &artistID, &method)
		if errors.Is(err, sql.ErrNoRows) {
			name := credit.ArtistName
			if strings.TrimSpace(name) == "" {
				name = credit.Name
			}
			if strings.TrimSpace(name) == "" {
				return ErrIncomingMatchConflict
			}
			artistID = uuid.New()
			sourceKind, sourceKey, field := "artist", credit.ArtistProviderKey, "artist-credit"
			ev := ProviderFieldEvidence{}
			if credit.Source != nil {
				sourceKind, sourceKey, field = credit.Source.EntityKind, credit.Source.ProviderKey, credit.Source.Field
				ev = ProviderFieldEvidence{Authority: credit.Source.Authority, FetchOrder: credit.Source.FetchOrder}
			}
			if credit.ArtistNameSource != nil {
				sourceKind, sourceKey, field = credit.ArtistNameSource.EntityKind, credit.ArtistNameSource.ProviderKey, credit.ArtistNameSource.Field
				ev = ProviderFieldEvidence{Authority: credit.ArtistNameSource.Authority, FetchOrder: credit.ArtistNameSource.FetchOrder}
			}
			provenance, marshalErr := json.Marshal(map[string]any{"automatic_matching": map[string]any{"source_id": source.ID, "provider_id": source.ProviderID, "provider_artist_id": providerArtistID, "projection_revision": revision, "policy_version": policy, "source_entity_id": sourceKey, "field_evidence": map[string]ProviderFieldEvidence{field: ev}, "name_source": field, "provider_fields_reference": map[string]any{"entity_kind": sourceKind, "provider_key": sourceKey, "fields": []string{field}}}})
			if marshalErr != nil {
				return marshalErr
			}
			if _, err := tx.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?,?,?::jsonb)`, artistID, name, string(provenance)).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, source.ProviderID, providerArtistID, artistID, policy).Exec(ctx); err != nil {
				return err
			}
		} else if err != nil {
			return fmt.Errorf("apply matching operation: read frozen artist mapping: %w", err)
		} else if method != "automatic" && method != "manual" {
			return ErrMatchingManualProtection
		}
		if _, err := tx.NewRaw(`INSERT INTO `+relation+`(`+parentColumn+`,position,artist_id,name,name_join_phrase) VALUES (?,?,?,?,?) ON CONFLICT DO NOTHING`, parentID, credit.Position, artistID, credit.Name, credit.JoinPhrase).Exec(ctx); err != nil {
			return fmt.Errorf("apply matching operation: add frozen artist credit: %w", err)
		}
	}
	return nil
}
