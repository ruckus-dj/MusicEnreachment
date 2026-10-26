package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingManualAssignment is an explicit member-to-position choice. Medium
// and Position are one-based, as they are in the persisted review proposal.
type MatchingManualAssignment struct {
	FileMemberID    uuid.UUID `json:"file_member_id"`
	ProviderTrackID uuid.UUID `json:"provider_track_id"`
	Medium          int       `json:"medium"`
	Position        int       `json:"position"`
	TitleOverride   *string   `json:"title_override,omitempty"`
	ArtistOverrides *[]string `json:"artist_overrides,omitempty"`
}

// MatchingManualMember describes one whole-group member and any assigned
// provider position. Unassigned members remain represented in the response.
type MatchingManualMember struct {
	FileMemberID uuid.UUID                 `json:"file_member_id"`
	Locations    []string                  `json:"locations"`
	Assignment   *MatchingManualAssignment `json:"assignment,omitempty"`
}

// MatchingManualReleaseLayout is the selected release layout from the same
// persisted proposal used to validate a manual matching preview.
type MatchingManualReleaseLayout struct {
	ProviderReleaseID uuid.UUID                             `json:"provider_release_id"`
	ReleaseID         string                                `json:"release_id"`
	Title             string                                `json:"title"`
	TracklistComplete bool                                  `json:"tracklist_complete"`
	Positions         []MatchingManualReleaseLayoutPosition `json:"positions"`
}

type MatchingManualReleaseLayoutPosition struct {
	TrackID         uuid.UUID `json:"track_id"`
	Medium          int       `json:"medium"`
	Position        int       `json:"position"`
	DisplayedNumber string    `json:"displayed_number"`
	RecordingID     string    `json:"recording_id"`
	Title           string    `json:"title"`
}

// MatchingManualPreview is also the immutable response stored by confirmation.
type MatchingManualPreview struct {
	GroupID            uuid.UUID                   `json:"group_id"`
	EvaluationRevision string                      `json:"evaluation_revision"`
	ProviderReleaseID  uuid.UUID                   `json:"provider_release_id"`
	SelectedRelease    MatchingManualReleaseLayout `json:"selected_release"`
	LocalReleaseID     uuid.UUID                   `json:"local_release_id,omitempty"`
	PreviewDigest      string                      `json:"preview_digest"`
	Members            []MatchingManualMember      `json:"members"`
	OmittedMemberIDs   []uuid.UUID                 `json:"omitted_member_ids"`
}

type manualProposal struct {
	Version         int                     `json:"version"`
	GroupID         uuid.UUID               `json:"group_id"`
	SelectedRelease *manualProposalRelease  `json:"selected_release"`
	Releases        []manualProposalRelease `json:"releases"`
	Files           []manualProposalFile    `json:"files"`
}
type manualProposalFile struct {
	FileMemberID uuid.UUID       `json:"file_member_id"`
	RawTags      json.RawMessage `json:"raw_tags"`
	Diagnostics  json.RawMessage `json:"diagnostics"`
}
type manualProposalRelease struct {
	ProviderReleaseID uuid.UUID                `json:"provider_release_id"`
	ReleaseID         string                   `json:"release_id"`
	Title             string                   `json:"title"`
	TracklistComplete bool                     `json:"tracklist_complete"`
	Positions         []manualProposalPosition `json:"positions"`
}
type manualProposalPosition struct {
	TrackID         uuid.UUID `json:"track_id"`
	Medium          int       `json:"medium"`
	Position        int       `json:"position"`
	RecordingID     string    `json:"recording_id"`
	DisplayedNumber string    `json:"displayed_number"`
	Title           string    `json:"title"`
}

type manualCurrentState struct {
	LocalReleaseID *uuid.UUID          `json:"local_release_id,omitempty"`
	ReleaseMethod  string              `json:"release_method,omitempty"`
	Tracks         []manualTrackState  `json:"tracks"`
	Sources        []manualSourceState `json:"sources"`
}
type manualTrackState struct {
	ProviderTrackID  uuid.UUID       `json:"provider_track_id"`
	LocalTrackID     *uuid.UUID      `json:"local_track_id,omitempty"`
	Method           string          `json:"method,omitempty"`
	LocalRecordingID *uuid.UUID      `json:"local_recording_id,omitempty"`
	Title            *string         `json:"title,omitempty"`
	Artists          []string        `json:"artists"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
}
type manualSourceState struct {
	MemberID       uuid.UUID `json:"member_id"`
	TrackID        uuid.UUID `json:"track_id"`
	DecisionMethod string    `json:"decision_method"`
	Selected       bool      `json:"selected"`
	Compatible     bool      `json:"compatible"`
	PolicyVersion  *string   `json:"policy_version,omitempty"`
}

// PreviewMatchingManual reads the full persisted evaluation scope and current
// target state without writing any rows.
func (repository *IncomingMatchCandidateRepository) PreviewMatchingManual(ctx context.Context, groupID uuid.UUID, expectedEvaluationRevision string, providerReleaseID uuid.UUID, explicitAssignments []MatchingManualAssignment) (MatchingManualPreview, error) {
	var result MatchingManualPreview
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("preview manual matching: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("preview manual matching: lock source mutations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("preview manual matching: lock provider configuration: %w", err)
		}
		var err error
		result, _, err = repository.previewMatchingManualTx(ctx, tx, groupID, expectedEvaluationRevision, providerReleaseID, explicitAssignments, false)
		return err
	})
	return result, err
}

// ConfirmMatchingManual persists one immutable receipt and materializes only
// explicitly assigned members in one transaction.
func (repository *IncomingMatchCandidateRepository) ConfirmMatchingManual(ctx context.Context, groupID uuid.UUID, expectedEvaluationRevision string, providerReleaseID uuid.UUID, explicitAssignments []MatchingManualAssignment, previewDigest string, requestID uuid.UUID) (MatchingManualPreview, error) {
	if requestID == uuid.Nil || previewDigest == "" {
		return MatchingManualPreview{}, errors.New("manual matching confirmation identity is invalid")
	}
	var result MatchingManualPreview
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("confirm manual matching: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("confirm manual matching: lock source mutations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("confirm manual matching: lock provider configuration: %w", err)
		}
		intent, err := json.Marshal(struct {
			GroupID       uuid.UUID                  `json:"group_id"`
			Revision      string                     `json:"evaluation_revision"`
			Release       uuid.UUID                  `json:"provider_release_id"`
			Assignments   []MatchingManualAssignment `json:"assignments"`
			PreviewDigest string                     `json:"preview_digest"`
		}{groupID, expectedEvaluationRevision, providerReleaseID, canonicalManualAssignments(explicitAssignments), previewDigest})
		if err != nil {
			return err
		}
		hash := sha256.Sum256(intent)
		intentHash := hex.EncodeToString(hash[:])
		var saved struct {
			IntentHash string `bun:"intent_hash"`
			Response   []byte `bun:"response"`
		}
		err = tx.NewRaw(`SELECT intent_hash, response FROM manual_matching_receipt WHERE request_id=?`, requestID).Scan(ctx, &saved)
		if err == nil {
			if saved.IntentHash != intentHash {
				return ErrIncomingMatchConflict
			}
			return json.Unmarshal(saved.Response, &result)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("confirm manual matching: read receipt: %w", err)
		}
		var state manualMatchingTxState
		result, state, err = repository.previewMatchingManualTx(ctx, tx, groupID, expectedEvaluationRevision, providerReleaseID, explicitAssignments, true)
		if err != nil {
			return err
		}
		if result.PreviewDigest != previewDigest {
			return ErrIncomingMatchConflict
		}
		var touchedTrackIDs []uuid.UUID
		state.touchedTrackIDs = &touchedTrackIDs
		if err := materializeManualMatching(ctx, tx, state, result, explicitAssignments); err != nil {
			return err
		}
		if _, err := promoteIncomingGroupMatchingManual(ctx, tx, groupID); err != nil {
			return err
		}
		var localReleaseID uuid.UUID
		if err := tx.NewRaw(`SELECT local_release_id FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`, state.fence.ProviderID, providerReleaseID).Scan(ctx, &localReleaseID); err != nil {
			return fmt.Errorf("confirm manual matching: read local release mapping: %w", err)
		}
		result.LocalReleaseID = localReleaseID
		positions, err := manualMatchingPublicationPositions(ctx, tx, localReleaseID, touchedTrackIDs)
		if err != nil {
			return err
		}
		var outputEpoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &outputEpoch); err != nil {
			return fmt.Errorf("confirm manual matching: read output epoch: %w", err)
		}
		// Empty accepted confirmations may materialize a release without positions.
		// Preserve that matching behavior without manufacturing publication work.
		if len(positions) > 0 {
			if err := NewSetupManagerRepository(repository.db).RequestPublicationReconciliationWith(ctx, tx, outputEpoch, positions, []string{"manual_confirmation"}); err != nil {
				return fmt.Errorf("confirm manual matching: request publication reconciliation: %w", err)
			}
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("confirm manual matching: encode receipt response: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO manual_matching_receipt(request_id,group_id,local_release_id,intent_hash,preview_digest,response) VALUES (?,?,?,?,?,?::jsonb)`, requestID, groupID, result.LocalReleaseID, intentHash, previewDigest, string(encoded)).Exec(ctx); err != nil {
			return fmt.Errorf("confirm manual matching: write receipt: %w", err)
		}
		return nil
	})
	return result, err
}

type manualMatchingTxState struct {
	header          IncomingMatchEvaluationHeader
	fence           IncomingMatchProviderFence
	release         MatchingRelease
	localReleaseID  *uuid.UUID
	frozen          *FrozenMaterialization
	seal            *MatchingFallbackSealAuthority
	context         *MatchingFallbackCalculationContext
	touchedTrackIDs *[]uuid.UUID
}

func (repository *IncomingMatchCandidateRepository) previewMatchingManualTx(ctx context.Context, tx bun.Tx, groupID uuid.UUID, expectedRevision string, releaseID uuid.UUID, assignments []MatchingManualAssignment, locking bool) (MatchingManualPreview, manualMatchingTxState, error) {
	var result MatchingManualPreview
	var state manualMatchingTxState
	if groupID == uuid.Nil || expectedRevision == "" || releaseID == uuid.Nil {
		return result, state, errors.New("manual matching identity is invalid")
	}
	if err := validateManualAssignments(assignments); err != nil {
		return result, state, err
	}
	var err error
	err = tx.NewRaw(`SELECT group_id,evaluation_revision,evaluation_evidence,group_revision,groups_revision,grouping_epoch,policy_version,provider_id,provider_source_id,provider_configuration_identity,provider_namespace,provider_endpoint,cache_key,cache_revision,provider_snapshot_revision,provider_fences,evaluated_at,matching_authority_mode,matching_authority_revision,fence_version,target_run_id,target_capture,target_capture_digest,frozen_authority_version,frozen_run_id,frozen_wait_revision,frozen_seal_digest FROM incoming_match_evaluation WHERE group_id=?`, groupID).Scan(ctx, &state.header)
	if errors.Is(err, sql.ErrNoRows) {
		return result, state, ErrIncomingMatchConflict
	}
	if err != nil {
		return result, state, fmt.Errorf("manual matching: read evaluation: %w", err)
	}
	if state.header.EvaluationRevision != expectedRevision {
		return result, state, ErrIncomingMatchConflict
	}
	// Version 3 evaluations are rooted in a verified historical seal. Branch
	// before decoding provider_fences: frozen headers intentionally have none of
	// the legacy mutable-cache fence semantics.
	if state.header.FrozenAuthorityVersion != nil && *state.header.FrozenAuthorityVersion == 3 {
		return repository.previewFrozenMatchingManualTx(ctx, tx, groupID, expectedRevision, releaseID, assignments, locking, state)
	}
	if err := json.Unmarshal(state.header.ProviderFences, &state.fence); err != nil {
		return result, state, fmt.Errorf("manual matching: decode provider fences: %w", err)
	}
	if state.fence.ProviderID != state.header.ProviderID || state.fence.ProviderSourceID != state.header.ProviderSourceID || state.fence.ConfigurationIdentity != state.header.ProviderConfigurationIdentity || state.fence.Namespace != state.header.ProviderNamespace || state.fence.Endpoint != state.header.ProviderEndpoint || state.fence.CacheKey != state.header.CacheKey || !equalOptionalString(state.fence.CacheRevision, state.header.CacheRevision) || state.fence.AcceptedProjectionRevision != state.header.ProviderSnapshotRevision {
		return result, state, ErrIncomingMatchConflict
	}
	group := IncomingMatchGroupEvaluation{GroupID: groupID, GroupRevision: state.header.GroupRevision, Evidence: state.header.EvaluationEvidence}
	if err := tx.NewRaw(`SELECT file_member_id,work_id,source_sha256 FROM incoming_match_evaluation_member WHERE group_id=? ORDER BY file_member_id`, groupID).Scan(ctx, &group.Members); err != nil {
		return result, state, err
	}
	if len(group.Members) == 0 {
		return result, state, ErrIncomingMatchConflict
	}
	evaluation := IncomingMatchEvaluation{ExpectedGroupsRevision: state.header.GroupsRevision, ExpectedGroupingEpoch: state.header.GroupingEpoch, PolicyVersion: state.header.PolicyVersion, Provider: state.fence, Groups: []IncomingMatchGroupEvaluation{group}}
	if state.header.FenceVersion == 2 {
		if state.header.TargetRunID == uuid.Nil || len(state.header.TargetCapture) == 0 || len(state.header.TargetCaptureDigest) != sha256.Size {
			return result, state, ErrIncomingMatchConflict
		}
		evaluation.TargetFenceVersion = 2
		evaluation.TargetRunID = state.header.TargetRunID
		evaluation.TargetCapture = append(json.RawMessage(nil), state.header.TargetCapture...)
		evaluation.TargetCaptureDigest = hex.EncodeToString(state.header.TargetCaptureDigest)
	} else if state.header.FenceVersion != 1 || state.header.TargetRunID != uuid.Nil || len(state.header.TargetCapture) != 0 || len(state.header.TargetCaptureDigest) != 0 {
		return result, state, ErrIncomingMatchConflict
	}
	manualContext := manualReviewFenceContext{
		GroupID: groupID, EvaluationRevision: expectedRevision,
		CapturedAuthorityRevision: state.header.MatchingAuthorityRevision,
	}
	if err := lockAndValidateMatchingPrelude(ctx, tx, evaluation, "manual matching", manualContext); err != nil {
		return result, state, err
	}
	authority, err := lockIncomingGroupMatchingAuthority(ctx, tx, groupID)
	if err != nil {
		return result, state, err
	}
	currentAuthority := authority.Revision == state.header.MatchingAuthorityRevision
	manualContinuation := authority.Mode == "manual" && authority.Revision == state.header.MatchingAuthorityRevision+1
	if (state.header.MatchingAuthorityMode != "automatic" && state.header.MatchingAuthorityMode != "manual_review") || (!currentAuthority && !manualContinuation) {
		return result, state, ErrIncomingMatchConflict
	}
	if err := lockAndValidateMatchProvider(ctx, tx, evaluation); err != nil {
		return result, state, err
	}
	if locking {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "incoming-match-evaluation:"+groupID.String()); err != nil {
			return result, state, err
		}
		var revision string
		if err := tx.NewRaw(`SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, groupID).Scan(ctx, &revision); err != nil || revision != expectedRevision {
			return result, state, ErrIncomingMatchConflict
		}
	}
	selectedKey := manualProviderReleaseKey(ctx, tx, state.fence.ProviderID, releaseID)
	keyIndex := sort.SearchStrings(state.fence.ReleaseKeys, selectedKey)
	selected := selectedKey != "" && keyIndex < len(state.fence.ReleaseKeys) && state.fence.ReleaseKeys[keyIndex] == selectedKey
	if !selected {
		return result, state, errors.New("manual matching release is not in the persisted evaluation scope")
	}
	var envelope struct {
		Proposal json.RawMessage `json:"review_proposal"`
	}
	if err := json.Unmarshal(state.header.EvaluationEvidence, &envelope); err != nil {
		return result, state, errors.New("manual matching review proposal is invalid")
	}
	var proposal manualProposal
	if err := strictManualDecode(envelope.Proposal, &proposal); err != nil {
		return result, state, errors.New("manual matching review proposal is invalid")
	}
	if proposal.Version != 1 || proposal.GroupID != groupID {
		return result, state, errors.New("manual matching review proposal is invalid")
	}
	var selectedProposal *manualProposalRelease
	for i := range proposal.Releases {
		if proposal.Releases[i].ProviderReleaseID == releaseID {
			selectedProposal = &proposal.Releases[i]
			break
		}
	}
	if selectedProposal == nil || !selectedProposal.TracklistComplete {
		return result, state, errors.New("manual matching selected release layout is unavailable")
	}
	positions := make(map[uuid.UUID]manualProposalPosition, len(selectedProposal.Positions))
	coordinates := make(map[string]uuid.UUID, len(selectedProposal.Positions))
	for _, p := range selectedProposal.Positions {
		if p.TrackID == uuid.Nil || p.Medium < 0 || p.Position < 0 || p.RecordingID == "" {
			return result, state, errors.New("manual matching selected release layout is invalid")
		}
		coord := fmt.Sprintf("%d/%d", p.Medium, p.Position)
		if _, ok := coordinates[coord]; ok {
			return result, state, errors.New("manual matching selected release layout is invalid")
		}
		coordinates[coord] = p.TrackID
		positions[p.TrackID] = p
	}
	for _, a := range assignments {
		p, ok := positions[a.ProviderTrackID]
		if !ok || p.Medium+1 != a.Medium || p.Position+1 != a.Position {
			return result, state, errors.New("manual matching assignment is outside selected release layout")
		}
	}
	var release MatchingRelease
	releases := []MatchingRelease{}
	if err := readMatchingReleases(ctx, tx, state.fence.ProviderSourceID, state.fence.ProviderID, []string{selectedProposal.ReleaseID}, &releases); err != nil {
		return result, state, fmt.Errorf("manual matching: read selected release: %w", err)
	}
	if len(releases) != 1 || releases[0].ID != releaseID || !releases[0].Complete {
		return result, state, ErrIncomingMatchConflict
	}
	release = releases[0]
	state.release = release
	var localID uuid.UUID
	err = tx.NewRaw(`SELECT local_release_id FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`, state.fence.ProviderID, releaseID).Scan(ctx, &localID)
	if err == nil {
		state.localReleaseID = &localID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, state, err
	}
	captures, err := loadIncomingGroupingCaptures(ctx, tx)
	if err != nil {
		return result, state, err
	}
	memberSet := make(map[uuid.UUID]bool, len(group.Members))
	for _, m := range group.Members {
		memberSet[m.FileMemberID] = true
	}
	locations := make(map[uuid.UUID][]string)
	for _, c := range captures {
		if memberSet[c.VariantID] {
			locations[c.VariantID] = append(locations[c.VariantID], c.ConfiguredPath+"/"+c.RelativePath)
		}
	}
	assignmentByMember := make(map[uuid.UUID]MatchingManualAssignment, len(assignments))
	for _, a := range assignments {
		if !memberSet[a.FileMemberID] {
			return result, state, errors.New("manual matching assignment references a non-member")
		}
		assignmentByMember[a.FileMemberID] = a
	}
	result = MatchingManualPreview{GroupID: groupID, EvaluationRevision: expectedRevision, ProviderReleaseID: releaseID, Members: make([]MatchingManualMember, 0, len(group.Members)), OmittedMemberIDs: make([]uuid.UUID, 0)}
	result.SelectedRelease = MatchingManualReleaseLayout{
		ProviderReleaseID: selectedProposal.ProviderReleaseID,
		ReleaseID:         selectedProposal.ReleaseID,
		Title:             selectedProposal.Title,
		TracklistComplete: selectedProposal.TracklistComplete,
		Positions:         make([]MatchingManualReleaseLayoutPosition, 0, len(selectedProposal.Positions)),
	}
	for _, position := range selectedProposal.Positions {
		result.SelectedRelease.Positions = append(result.SelectedRelease.Positions, MatchingManualReleaseLayoutPosition{
			TrackID: position.TrackID, Medium: position.Medium, Position: position.Position,
			DisplayedNumber: position.DisplayedNumber, RecordingID: position.RecordingID, Title: position.Title,
		})
	}
	for _, m := range group.Members {
		paths := locations[m.FileMemberID]
		if len(paths) == 0 {
			return result, state, ErrIncomingMatchConflict
		}
		sort.Strings(paths)
		member := MatchingManualMember{FileMemberID: m.FileMemberID, Locations: paths}
		if a, ok := assignmentByMember[m.FileMemberID]; ok {
			member.Assignment = &a
		} else {
			result.OmittedMemberIDs = append(result.OmittedMemberIDs, m.FileMemberID)
		}
		result.Members = append(result.Members, member)
	}
	current, err := readManualCurrentState(ctx, tx, state.fence.ProviderID, releaseID, selectedProposal.Positions, group.Members)
	if err != nil {
		return result, state, err
	}
	digestInput, err := json.Marshal(struct {
		Header    IncomingMatchEvaluationHeader  `json:"header"`
		Release   MatchingRelease                `json:"release"`
		Members   []MatchingManualMember         `json:"members"`
		Current   manualCurrentState             `json:"current"`
		Authority IncomingGroupMatchingAuthority `json:"authority"`
	}{state.header, release, result.Members, current, authority})
	if err != nil {
		return result, state, err
	}
	sum := sha256.Sum256(digestInput)
	result.PreviewDigest = hex.EncodeToString(sum[:])
	result.LocalReleaseID = state.localReleaseIDOrNil()
	return result, state, nil
}

// previewFrozenMatchingManualTx consumes the persisted frozen proposal and the
// accepted lookup projection sealed by its historical authority. It deliberately
// does not decode legacy provider fences or consult mutable cache projections.
func (repository *IncomingMatchCandidateRepository) previewFrozenMatchingManualTx(
	ctx context.Context, tx bun.Tx, groupID uuid.UUID, expectedRevision string, releaseID uuid.UUID,
	assignments []MatchingManualAssignment, locking bool, state manualMatchingTxState,
) (MatchingManualPreview, manualMatchingTxState, error) {
	var result MatchingManualPreview
	if state.header.FrozenRunID == nil || state.header.FrozenWaitRevision == nil || len(state.header.FrozenSealDigest) == 0 {
		return result, state, ErrIncomingMatchConflict
	}
	seal, found, err := readMatchingFallbackAuthoritySeal(ctx, tx, *state.header.FrozenRunID, *state.header.FrozenWaitRevision)
	if err != nil || !found || seal.SchemaVersion != 3 || seal.Digest != hex.EncodeToString(state.header.FrozenSealDigest) ||
		seal.Target.GroupID != groupID || seal.Target.GroupRevision != state.header.GroupRevision || seal.SourceID != state.header.ProviderSourceID || seal.ProviderID != state.header.ProviderID {
		return result, state, ErrIncomingMatchConflict
	}
	var proposalEnvelope struct {
		Proposal json.RawMessage `json:"review_proposal"`
	}
	if json.Unmarshal(state.header.EvaluationEvidence, &proposalEnvelope) != nil {
		return result, state, errors.New("manual matching review proposal is invalid")
	}
	var proposal manualProposal
	if err := strictManualDecode(proposalEnvelope.Proposal, &proposal); err != nil || proposal.Version != 1 || proposal.GroupID != groupID {
		return result, state, errors.New("manual matching review proposal is invalid")
	}
	var selected *manualProposalRelease
	for index := range proposal.Releases {
		if proposal.Releases[index].ProviderReleaseID == releaseID {
			selected = &proposal.Releases[index]
			break
		}
	}
	if selected == nil || len(selected.Positions) == 0 {
		return result, state, errors.New("manual matching selected release layout is unavailable")
	}
	contextSnapshot, err := matchingFallbackSealContext(seal)
	if err != nil {
		return result, state, ErrIncomingMatchConflict
	}
	var projection *ProviderMatchingSnapshot
	for _, lookup := range contextSnapshot.LookupReceipts {
		var candidate ProviderMatchingSnapshot
		if json.Unmarshal(lookup.AcceptedProjection, &candidate) != nil || len(candidate.Releases) != 1 {
			continue
		}
		if strings.EqualFold(candidate.Releases[0].ProviderKey, selected.ReleaseID) {
			if projection != nil {
				return result, state, ErrIncomingMatchConflict
			}
			projection = &candidate
		}
	}
	if projection == nil || projection.Releases[0].ID != releaseID {
		return result, state, ErrIncomingMatchConflict
	}
	positions := make(map[uuid.UUID]manualProposalPosition, len(selected.Positions))
	coordinates := make(map[string]uuid.UUID, len(selected.Positions))
	for _, p := range selected.Positions {
		if p.TrackID == uuid.Nil || p.Medium < 1 || p.Position < 1 {
			return result, state, errors.New("manual matching selected release layout is invalid")
		}
		coord := fmt.Sprintf("%d/%d", p.Medium, p.Position)
		if _, exists := coordinates[coord]; exists {
			return result, state, errors.New("manual matching selected release layout is invalid")
		}
		coordinates[coord], positions[p.TrackID] = p.TrackID, p
	}
	for _, assignment := range assignments {
		p, ok := positions[assignment.ProviderTrackID]
		if !ok || p.Medium != assignment.Medium || p.Position != assignment.Position {
			return result, state, errors.New("manual matching assignment is outside selected release layout")
		}
	}
	group := IncomingMatchGroupEvaluation{GroupID: groupID, GroupRevision: state.header.GroupRevision}
	if err := tx.NewRaw(`SELECT file_member_id,work_id,source_sha256 FROM incoming_match_evaluation_member WHERE group_id=? ORDER BY file_member_id`, groupID).Scan(ctx, &group.Members); err != nil || len(group.Members) == 0 {
		return result, state, ErrIncomingMatchConflict
	}
	var source struct{ Endpoint, Configuration string }
	if err := tx.NewRaw(`SELECT endpoint,configuration_identity FROM provider_source WHERE id=? AND provider_id=? FOR UPDATE`, seal.SourceID, seal.ProviderID).Scan(ctx, &source.Endpoint, &source.Configuration); err != nil || source.Endpoint != seal.Endpoint || source.Configuration != seal.SourceConfiguration {
		return result, state, ErrIncomingMatchConflict
	}
	var currentConfiguration string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentConfiguration); err != nil || currentConfiguration != seal.RunProviderConfiguration {
		return result, state, ErrIncomingMatchConflict
	}
	var currentGroupRevision string
	if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? FOR UPDATE`, groupID).Scan(ctx, &currentGroupRevision); err != nil || currentGroupRevision != state.header.GroupRevision {
		return result, state, ErrIncomingMatchConflict
	}
	locationIDs, err := currentManualMemberLocationIDs(ctx, tx, group.Members)
	if err != nil || len(locationIDs) == 0 {
		return result, state, ErrIncomingMatchConflict
	}
	captures, err := loadIncomingGroupingCapturesForLocations(ctx, tx, locationIDs)
	if err != nil || !frozenManualCaptureIsCurrent(seal.Target.Capture, seal.CapturedMetadata, group.Members, captures) {
		return result, state, ErrIncomingMatchConflict
	}
	var currentRunID uuid.UUID
	if err := tx.NewRaw(`SELECT current_run_id FROM incoming_matching_generation WHERE group_id=? FOR UPDATE`, groupID).Scan(ctx, &currentRunID); err != nil || currentRunID != seal.RunID {
		return result, state, ErrIncomingMatchConflict
	}
	authority, err := lockIncomingGroupMatchingAuthority(ctx, tx, groupID)
	if err != nil || authority.GroupID != groupID || (authority.Mode != "automatic" && authority.Mode != "manual") ||
		(seal.GroupAuthority.Mode == "automatic" && (authority.Mode == "automatic" && authority.Revision != seal.GroupAuthority.Revision || authority.Mode == "manual" && authority.Revision <= seal.GroupAuthority.Revision)) {
		return result, state, ErrIncomingMatchConflict
	}
	if locking {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "incoming-match-evaluation:"+groupID.String()); err != nil {
			return result, state, err
		}
		var revision string
		if err := tx.NewRaw(`SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, groupID).Scan(ctx, &revision); err != nil || revision != expectedRevision {
			return result, state, ErrIncomingMatchConflict
		}
	}
	state.fence = IncomingMatchProviderFence{ProviderID: seal.ProviderID, ProviderSourceID: seal.SourceID, ConfigurationIdentity: seal.SourceConfiguration}
	state.seal, state.context = &seal, &contextSnapshot
	materialAssignments := make([]FrozenTrackAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		p := positions[assignment.ProviderTrackID]
		track := projection.Releases[0].Tracks
		var recordingKey string
		for _, candidate := range track {
			if candidate.ID == assignment.ProviderTrackID {
				recordingKey = candidate.RecordingProviderKey
				break
			}
		}
		materialAssignments = append(materialAssignments, FrozenTrackAssignment{ProviderTrackID: assignment.ProviderTrackID, ProviderReleaseID: releaseID, ReleaseKey: selected.ReleaseID, RecordingKey: recordingKey, Medium: p.Medium - 1, Position: p.Position - 1})
	}
	frozen, err := acceptedProjectionToMaterialization(*projection, selected.ReleaseID, materialAssignments)
	if err != nil {
		return result, state, ErrIncomingMatchConflict
	}
	state.frozen = &frozen
	var localID uuid.UUID
	err = tx.NewRaw(`SELECT local_release_id FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`, seal.ProviderID, releaseID).Scan(ctx, &localID)
	if err == nil {
		state.localReleaseID = &localID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, state, err
	}
	memberSet := make(map[uuid.UUID]bool, len(group.Members))
	for _, member := range group.Members {
		memberSet[member.FileMemberID] = true
	}
	locations := map[uuid.UUID][]string{}
	for _, capture := range captures {
		if memberSet[capture.VariantID] {
			locations[capture.VariantID] = append(locations[capture.VariantID], capture.ConfiguredPath+"/"+capture.RelativePath)
		}
	}
	assignmentByMember := map[uuid.UUID]MatchingManualAssignment{}
	for _, assignment := range assignments {
		if !memberSet[assignment.FileMemberID] {
			return result, state, errors.New("manual matching assignment references a non-member")
		}
		assignmentByMember[assignment.FileMemberID] = assignment
	}
	result = MatchingManualPreview{GroupID: groupID, EvaluationRevision: expectedRevision, ProviderReleaseID: releaseID, Members: make([]MatchingManualMember, 0, len(group.Members)), OmittedMemberIDs: make([]uuid.UUID, 0)}
	result.SelectedRelease = MatchingManualReleaseLayout{ProviderReleaseID: releaseID, ReleaseID: selected.ReleaseID, Title: frozen.Release.Title, TracklistComplete: selected.TracklistComplete, Positions: make([]MatchingManualReleaseLayoutPosition, 0, len(selected.Positions))}
	for _, p := range selected.Positions {
		result.SelectedRelease.Positions = append(result.SelectedRelease.Positions, MatchingManualReleaseLayoutPosition{TrackID: p.TrackID, Medium: p.Medium, Position: p.Position, DisplayedNumber: p.DisplayedNumber, RecordingID: p.RecordingID, Title: p.Title})
	}
	for _, member := range group.Members {
		paths := locations[member.FileMemberID]
		if len(paths) == 0 {
			return result, state, ErrIncomingMatchConflict
		}
		sort.Strings(paths)
		item := MatchingManualMember{FileMemberID: member.FileMemberID, Locations: paths}
		if assignment, ok := assignmentByMember[member.FileMemberID]; ok {
			item.Assignment = &assignment
		} else {
			result.OmittedMemberIDs = append(result.OmittedMemberIDs, member.FileMemberID)
		}
		result.Members = append(result.Members, item)
	}
	current, err := readManualCurrentState(ctx, tx, seal.ProviderID, releaseID, selected.Positions, group.Members)
	if err != nil {
		return result, state, err
	}
	digestInput, err := json.Marshal(struct {
		Header          IncomingMatchEvaluationHeader
		Members         []MatchingManualMember
		Current         manualCurrentState
		Authority       IncomingGroupMatchingAuthority
		SourceID        uuid.UUID
		Configuration   string
		SealDigest      string
		Materialization FrozenMaterialization
	}{state.header, result.Members, current, authority, seal.SourceID, source.Configuration, seal.Digest, frozen})
	if err != nil {
		return result, state, err
	}
	sum := sha256.Sum256(digestInput)
	result.PreviewDigest = hex.EncodeToString(sum[:])
	result.LocalReleaseID = state.localReleaseIDOrNil()
	return result, state, nil
}

// currentManualMemberLocationIDs resolves the current inventory locations for
// all sealed member variants. Resolve locations before reading captures so
// additional captures for a member remain visible to the frozen-snapshot check.
func currentManualMemberLocationIDs(ctx context.Context, tx bun.IDB, members []IncomingMatchMemberFence) ([]uuid.UUID, error) {
	variantIDs := make([]uuid.UUID, 0, len(members))
	for _, member := range members {
		if member.FileMemberID == uuid.Nil {
			return nil, ErrIncomingMatchConflict
		}
		variantIDs = append(variantIDs, member.FileMemberID)
	}
	if len(variantIDs) == 0 {
		return nil, ErrIncomingMatchConflict
	}
	var locationIDs []uuid.UUID
	err := tx.NewRaw(`SELECT location.id
		FROM source_root root
		JOIN source_location location ON location.source_root_id=root.id
		LEFT JOIN source_analysis_work work ON work.current_location_id=location.id AND work.location_id=location.id
			AND work.configured_path=root.configured_path AND work.inventory_path=root.inventory_path
			AND work.relative_path=location.relative_path AND work.size_bytes=location.size_bytes AND work.mtime=location.mtime
	LEFT JOIN source_analysis_step sha ON sha.work_id=work.id AND sha.step='sha256' AND sha.state='succeeded'
	WHERE COALESCE(sha.success_sha_variant_id, work.id, location.id) IN (?)
	ORDER BY location.id`, bun.List(variantIDs)).Scan(ctx, &locationIDs)
	if err != nil {
		return nil, fmt.Errorf("resolve current manual matching member locations: %w", err)
	}
	return locationIDs, nil
}

func matchingFallbackSealContext(seal MatchingFallbackSealAuthority) (MatchingFallbackCalculationContext, error) {
	var context MatchingFallbackCalculationContext
	if len(seal.FullEvaluationContext) == 0 {
		return context, errors.New("sealed lookup projection is unavailable")
	}
	if err := strictManualDecode(seal.FullEvaluationContext, &context); err != nil {
		return context, err
	}
	return context, nil
}

// frozenManualCaptureIsCurrent proves that the immutable review target still
// describes the current persisted captures. The seal remains the source of
// authority; this comparison only prevents replay against changed source state.
func frozenManualCaptureIsCurrent(raw json.RawMessage, metadata []MatchingAcquisitionCapturedMember, members []IncomingMatchMemberFence, current []IncomingGroupingCapture) bool {
	var target struct {
		Members []struct {
			VariantID    uuid.UUID                        `json:"variant_id"`
			SourceSHA256 string                           `json:"source_sha256"`
			Locations    []IncomingGroupingMemberLocation `json:"locations"`
			Captures     []IncomingGroupingCapture        `json:"captures"`
		} `json:"members"`
	}
	if json.Unmarshal(raw, &target) != nil || len(target.Members) == 0 || len(target.Members) != len(members) || len(metadata) != len(members) {
		return false
	}
	memberIDs := make(map[uuid.UUID]IncomingMatchMemberFence, len(members))
	for _, member := range members {
		if member.FileMemberID == uuid.Nil || memberIDs[member.FileMemberID].FileMemberID != uuid.Nil {
			return false
		}
		memberIDs[member.FileMemberID] = member
	}
	metadataByID := make(map[uuid.UUID]MatchingAcquisitionCapturedMember, len(metadata))
	for _, item := range metadata {
		if item.MemberID == uuid.Nil || metadataByID[item.MemberID].MemberID != uuid.Nil {
			return false
		}
		metadataByID[item.MemberID] = item
	}
	// The persisted reader is global, so index only captures that belong to the
	// sealed target scope. Unrelated inventory variants must not leak into the
	// leftover check, while every capture of a target variant is retained.
	allowedVariants := make(map[uuid.UUID]struct{}, len(target.Members))
	for _, expected := range target.Members {
		allowedVariants[expected.VariantID] = struct{}{}
	}
	currentByID := make(map[uuid.UUID][]IncomingGroupingCapture, len(members))
	for _, capture := range current {
		if _, allowed := allowedVariants[capture.VariantID]; !allowed {
			continue
		}
		currentByID[capture.VariantID] = append(currentByID[capture.VariantID], capture)
	}
	for _, expected := range target.Members {
		member, exists := memberIDs[expected.VariantID]
		captured, hasMetadata := metadataByID[expected.VariantID]
		actual := currentByID[expected.VariantID]
		if !exists || !hasMetadata || len(expected.Captures) == 0 || len(actual) != len(expected.Captures) || captured.WorkID != member.WorkID {
			return false
		}
		if len(captured.Tags) > 0 && !matchingJSONEqual(captured.Tags, actual[0].Tags) {
			return false
		}
		capturedHash, hashErr := memberSourceHash(raw, expected.VariantID)
		if hashErr != nil {
			return false
		}
		if capturedHash != "" {
			for _, capture := range actual {
				if len(capture.SourceSHA256) == 0 || !strings.EqualFold(hex.EncodeToString(capture.SourceSHA256), capturedHash) {
					return false
				}
			}
		}
		if !equalFrozenCaptureJSON(expected.Captures, actual) {
			return false
		}
		actualLocations := make([]IncomingGroupingMemberLocation, 0, len(actual))
		for _, capture := range actual {
			actualLocations = append(actualLocations, IncomingGroupingMemberLocation{
				VariantID: capture.VariantID, RootID: capture.RootID, WorkID: capture.WorkID, LocationID: capture.LocationID,
				ConfiguredPath: capture.ConfiguredPath, InventoryPath: capture.InventoryPath, RelativePath: capture.RelativePath,
				SizeBytes: capture.SizeBytes, Mtime: capture.Mtime,
			})
		}
		if !equalFrozenCaptureJSON(expected.Locations, actualLocations) {
			return false
		}
		delete(memberIDs, expected.VariantID)
		delete(metadataByID, expected.VariantID)
		delete(currentByID, expected.VariantID)
	}
	return len(memberIDs) == 0 && len(metadataByID) == 0 && len(currentByID) == 0
}

func equalFrozenCaptureJSON[T any](left, right []T) bool {
	if len(left) != len(right) {
		return false
	}
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && matchingJSONEqual(leftBytes, rightBytes)
}

func (s manualMatchingTxState) localReleaseIDOrNil() uuid.UUID {
	if s.localReleaseID == nil {
		return uuid.Nil
	}
	return *s.localReleaseID
}

func canonicalManualAssignments(in []MatchingManualAssignment) []MatchingManualAssignment {
	out := append([]MatchingManualAssignment(nil), in...)
	for i := range out {
		out[i].TitleOverride = cloneManualString(out[i].TitleOverride)
		out[i].ArtistOverrides = cloneManualStrings(out[i].ArtistOverrides)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FileMemberID.String() < out[j].FileMemberID.String() })
	return out
}
func validateManualAssignments(in []MatchingManualAssignment) error {
	seen := map[uuid.UUID]bool{}
	titles := map[uuid.UUID]*string{}
	artists := map[uuid.UUID]*[]string{}
	for _, a := range in {
		if a.FileMemberID == uuid.Nil || a.ProviderTrackID == uuid.Nil || a.Medium < 1 || a.Position < 1 || seen[a.FileMemberID] {
			return errors.New("manual matching assignments are invalid")
		}
		seen[a.FileMemberID] = true
		if a.TitleOverride != nil {
			if previous, ok := titles[a.ProviderTrackID]; ok && *previous != *a.TitleOverride {
				return errors.New("manual matching assignments are invalid")
			}
			titles[a.ProviderTrackID] = a.TitleOverride
		}
		if a.ArtistOverrides != nil {
			if previous, ok := artists[a.ProviderTrackID]; ok && !equalManualStrings(*previous, *a.ArtistOverrides) {
				return errors.New("manual matching assignments are invalid")
			}
			artists[a.ProviderTrackID] = a.ArtistOverrides
		}
	}
	return nil
}

func cloneManualString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneManualStrings(value *[]string) *[]string {
	if value == nil {
		return nil
	}
	cloned := make([]string, len(*value))
	copy(cloned, *value)
	return &cloned
}

func equalManualStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func strictManualDecode(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}
func manualProviderReleaseKey(ctx context.Context, tx bun.Tx, providerID, releaseID uuid.UUID) string {
	var key string
	_ = tx.NewRaw(`SELECT provider_key FROM provider_release WHERE provider_id=? AND id=?`, providerID, releaseID).Scan(ctx, &key)
	return key
}

func readManualCurrentState(ctx context.Context, tx bun.Tx, providerID, releaseID uuid.UUID, positions []manualProposalPosition, members []IncomingMatchMemberFence) (manualCurrentState, error) {
	state := manualCurrentState{Tracks: make([]manualTrackState, 0, len(positions)), Sources: make([]manualSourceState, 0)}
	var id uuid.UUID
	var method string
	err := tx.NewRaw(`SELECT local_release_id,method FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`, providerID, releaseID).Scan(ctx, &id, &method)
	if err == nil {
		state.LocalReleaseID = &id
		state.ReleaseMethod = method
	} else if !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	orderedPositions := append([]manualProposalPosition(nil), positions...)
	sort.Slice(orderedPositions, func(i, j int) bool {
		return orderedPositions[i].TrackID.String() < orderedPositions[j].TrackID.String()
	})
	for _, p := range orderedPositions {
		row := manualTrackState{ProviderTrackID: p.TrackID, Artists: []string{}}
		var localID, recordingID uuid.UUID
		var trackMethod string
		var title string
		err := tx.NewRaw(`SELECT m.local_track_id,m.method,t.recording_id,l.title,l.metadata FROM provider_local_track m JOIN release_track t ON t.track_id=m.local_track_id JOIN local_track l ON l.id=t.track_id JOIN provider_local_release r ON r.local_release_id=t.release_id WHERE m.provider_id=? AND m.provider_track_id=? AND r.provider_release_id=? FOR UPDATE OF l`, providerID, p.TrackID, releaseID).Scan(ctx, &localID, &trackMethod, &recordingID, &title, &row.Metadata)
		if err == nil {
			row.LocalTrackID = &localID
			row.Method = trackMethod
			row.LocalRecordingID = &recordingID
			row.Title = &title
			if err := tx.NewRaw(`SELECT name FROM track_artist WHERE track_id=? ORDER BY position FOR UPDATE`, localID).Scan(ctx, &row.Artists); err != nil {
				return state, err
			}
			if row.Artists == nil {
				row.Artists = []string{}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return state, err
		}
		state.Tracks = append(state.Tracks, row)
	}
	for _, m := range members {
		var rows []manualSourceState
		if err := tx.NewRaw(`SELECT media_variant_id AS member_id,track_id,decision_method,selected,compatible,policy_version FROM track_source WHERE media_variant_id IN (SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=?) ORDER BY track_id`, m.WorkID).Scan(ctx, &rows); err != nil {
			return state, err
		}
		state.Sources = append(state.Sources, rows...)
	}
	return state, nil
}

func materializeManualMatching(ctx context.Context, tx bun.Tx, state manualMatchingTxState, preview MatchingManualPreview, assignments []MatchingManualAssignment) error {
	if state.frozen != nil {
		return materializeFrozenManualMatching(ctx, tx, state, preview, assignments)
	}
	localReleaseID, created, err := ensureLocalRelease(ctx, tx, state.fence.ProviderID, preview.ProviderReleaseID, state.release.Name, materializationAuthority{method: "manual"})
	if err != nil {
		return err
	}
	if created {
		evidence, _ := json.Marshal(state.release.FieldEvidence)
		if err := writeManualMetadata(ctx, tx, "local_release", localReleaseID, "title", state.release.ProviderKey, state.fence.ProviderSourceID, state.fence.ProviderID, state.fence.AcceptedProjectionRevision, evidence); err != nil {
			return err
		}
		credits, err := acceptedReleaseArtistCredits(ctx, tx, state.fence.ProviderID, state.fence.ProviderSourceID, preview.ProviderReleaseID)
		if err != nil {
			return err
		}
		if err := materializeArtistCredits(ctx, tx, state.fence.ProviderID, state.fence.ProviderSourceID, localReleaseID, "release_artist", credits, "", state.fence.AcceptedProjectionRevision, true); err != nil {
			return err
		}
	}
	overridesByTrack := manualOverridesByTrack(assignments)
	appliedOverrides := make(map[uuid.UUID]bool)
	for _, assignment := range assignments {
		var recordingID uuid.UUID
		var medium, position int
		var recordingTitle, recordingEvidence string
		err := tx.NewRaw(`SELECT t.recording_id,st.medium,st.position,COALESCE(NULLIF(e.name,''),e.fields->>'title',''),e.field_evidence
			FROM provider_release_track t JOIN provider_source_release_track st ON st.provider_id=t.provider_id AND st.track_id=t.id
			LEFT JOIN provider_recording r ON r.provider_id=t.provider_id AND r.id=t.recording_id
			LEFT JOIN provider_source_entity e ON e.provider_source_id=st.provider_source_id AND e.provider_id=t.provider_id AND e.entity_kind='recording' AND e.provider_key=r.provider_key
			WHERE t.id=? AND t.provider_id=? AND st.release_id=? AND st.present=true AND st.provider_source_id=?`, assignment.ProviderTrackID, state.fence.ProviderID, preview.ProviderReleaseID, state.fence.ProviderSourceID).Scan(ctx, &recordingID, &medium, &position, &recordingTitle, &recordingEvidence)
		if err != nil || medium+1 != assignment.Medium || position+1 != assignment.Position {
			return ErrIncomingMatchConflict
		}
		var recordingKey string
		if err := tx.NewRaw(`SELECT provider_key FROM provider_recording WHERE provider_id=? AND id=?`, state.fence.ProviderID, recordingID).Scan(ctx, &recordingKey); err != nil {
			return err
		}
		trackTitle := acceptedTrackTitle(state.release.Fields, medium, position, recordingKey)
		trackTitleField, trackTitleKey := "normalized_tracks", state.release.ProviderKey
		trackTitleEvidence, err := json.Marshal(state.release.FieldEvidence)
		if err != nil {
			return fmt.Errorf("manual matching: encode release title evidence: %w", err)
		}
		if trackTitle == "" {
			trackTitle = recordingTitle
			trackTitleField, trackTitleKey = "title", recordingKey
			trackTitleEvidence = json.RawMessage(recordingEvidence)
		}
		localTrackID, localRecordingID, recordingCreated, trackCreated, err := ensureLocalTrack(ctx, tx, state.fence.ProviderID, assignment.ProviderTrackID, recordingID, localReleaseID, medium, position, trackTitle, materializationAuthority{method: "manual"})
		if err != nil {
			return err
		}
		if state.touchedTrackIDs != nil {
			*state.touchedTrackIDs = append(*state.touchedTrackIDs, localTrackID)
		}
		if recordingCreated {
			credits, err := acceptedRecordingArtistCredits(ctx, tx, state.fence.ProviderID, state.fence.ProviderSourceID, recordingID)
			if err != nil {
				return err
			}
			if err := writeManualMetadata(ctx, tx, "local_recording", localRecordingID, "title", recordingKey, state.fence.ProviderSourceID, state.fence.ProviderID, state.fence.AcceptedProjectionRevision, json.RawMessage(recordingEvidence)); err != nil {
				return err
			}
			if err := materializeArtistCredits(ctx, tx, state.fence.ProviderID, state.fence.ProviderSourceID, localRecordingID, "recording_artist", credits, "", state.fence.AcceptedProjectionRevision, true); err != nil {
				return err
			}
		}
		if trackCreated {
			normalizedTrackEvidence := state.release.FieldEvidence["normalized_tracks"]
			if err := writeManualMetadata(ctx, tx, "local_track", localTrackID, trackTitleField, trackTitleKey, state.fence.ProviderSourceID, state.fence.ProviderID, state.fence.AcceptedProjectionRevision, trackTitleEvidence); err != nil {
				return err
			}
			trackCreditEvidence, err := json.Marshal(map[string]ProviderFieldEvidence{"normalized_tracks": normalizedTrackEvidence})
			if err != nil {
				return err
			}
			credits, err := acceptedTrackArtistCredits(state.release.Fields, trackCreditEvidence, medium, position, recordingKey, state.release.ProviderKey)
			if err != nil {
				return err
			}
			if credits == nil {
				credits, err = acceptedRecordingArtistCredits(ctx, tx, state.fence.ProviderID, state.fence.ProviderSourceID, recordingID)
			}
			if err != nil {
				return err
			}
			if err := materializeArtistCredits(ctx, tx, state.fence.ProviderID, state.fence.ProviderSourceID, localTrackID, "track_artist", credits, "", state.fence.AcceptedProjectionRevision, true); err != nil {
				return err
			}
		}
		if !appliedOverrides[assignment.ProviderTrackID] {
			override := overridesByTrack[assignment.ProviderTrackID]
			if err := applyManualTrackOverrides(ctx, tx, localTrackID, override.TitleOverride, override.ArtistOverrides); err != nil {
				return err
			}
			appliedOverrides[assignment.ProviderTrackID] = true
		}
		var workID uuid.UUID
		if err := tx.NewRaw(`SELECT work_id FROM incoming_match_evaluation_member WHERE group_id=? AND file_member_id=?`, preview.GroupID, assignment.FileMemberID).Scan(ctx, &workID); err != nil {
			return err
		}
		var variants []uuid.UUID
		if err := tx.NewRaw(`SELECT DISTINCT media_variant_id FROM source_work_variant_identity WHERE work_id=? ORDER BY media_variant_id`, workID).Scan(ctx, &variants); err != nil {
			return err
		}
		if len(variants) == 0 {
			return ErrIncomingMatchConflict
		}
		metadataResultID, err := sourceWorkMetadataResultID(ctx, tx, workID)
		if err != nil {
			return err
		}
		for _, variantID := range variants {
			if err := attachTrackSource(ctx, tx, localTrackID, variantID, materializationAuthority{method: "manual"}, metadataResultID); err != nil {
				return err
			}
		}
	}
	return nil
}

func materializeFrozenManualMatching(ctx context.Context, tx bun.Tx, state manualMatchingTxState, preview MatchingManualPreview, assignments []MatchingManualAssignment) error {
	if state.frozen == nil || state.seal == nil {
		return ErrIncomingMatchConflict
	}
	frozen := *state.frozen
	seal := *state.seal
	localReleaseID, releaseCreated, err := ensureLocalRelease(ctx, tx, seal.ProviderID, frozen.Release.ID, frozen.Release.Title, materializationAuthority{method: "manual"})
	if err != nil {
		return err
	}
	if releaseCreated {
		if source, ok := frozen.Release.FieldSources["title"]; ok {
			if err := writeManualMetadata(ctx, tx, "local_release", localReleaseID, "title", frozen.Release.ProviderKey, seal.SourceID, seal.ProviderID, preview.EvaluationRevision, jsonEvidence("title", source)); err != nil {
				return err
			}
		}
		if len(frozen.Release.Credits) > 0 {
			if err := materializeFrozenManualArtistCredits(ctx, tx, seal.ProviderID, seal.SourceID, localReleaseID, "release_artist", frozen.Release.Credits, preview.EvaluationRevision); err != nil {
				return err
			}
		}
	}
	tracks := make(map[uuid.UUID]FrozenRecordingTrack, len(frozen.Tracks))
	for _, track := range frozen.Tracks {
		tracks[track.ProviderTrackID] = track
	}
	overridesByTrack := manualOverridesByTrack(assignments)
	appliedOverrides := make(map[uuid.UUID]bool)
	for _, assignment := range assignments {
		track, ok := tracks[assignment.ProviderTrackID]
		if !ok || track.ProviderReleaseID != frozen.Release.ID || track.Medium+1 != assignment.Medium || track.Position+1 != assignment.Position {
			return ErrIncomingMatchConflict
		}
		var recordingID uuid.UUID
		var recordingKey string
		var medium, position int
		if err := tx.NewRaw(`SELECT track.recording_id,recording.provider_key,track.medium,track.position FROM provider_release_track track JOIN provider_recording recording ON recording.provider_id=track.provider_id AND recording.id=track.recording_id WHERE track.provider_id=? AND track.id=? AND track.release_id=?`, seal.ProviderID, track.ProviderTrackID, frozen.Release.ID).Scan(ctx, &recordingID, &recordingKey, &medium, &position); err != nil || !strings.EqualFold(recordingKey, track.RecordingProviderKey) || medium != track.Medium || position != track.Position {
			return ErrIncomingMatchConflict
		}
		localRecordingID, recordingCreated, err := ensureManualLocalRecording(ctx, tx, seal.ProviderID, recordingID, track.RecordingTitle)
		if err != nil {
			return err
		}
		if recordingCreated {
			source := track.RecordingTitleSource
			if source == nil {
				fallback := FrozenFieldSource{EntityKind: "recording", ProviderKey: track.RecordingProviderKey, Field: "title"}
				source = &fallback
			}
			if err := writeManualMetadata(ctx, tx, "local_recording", localRecordingID, "title", track.RecordingProviderKey, seal.SourceID, seal.ProviderID, preview.EvaluationRevision, jsonEvidence("title", *source)); err != nil {
				return err
			}
			if len(track.RecordingCredits) > 0 {
				if err := materializeFrozenManualArtistCredits(ctx, tx, seal.ProviderID, seal.SourceID, localRecordingID, "recording_artist", track.RecordingCredits, preview.EvaluationRevision); err != nil {
					return err
				}
			}
		}
		localTrackID, _, _, trackCreated, err := ensureLocalTrack(ctx, tx, seal.ProviderID, track.ProviderTrackID, recordingID, localReleaseID, medium, position, track.Title, materializationAuthority{method: "manual"})
		if err != nil {
			return err
		}
		if state.touchedTrackIDs != nil {
			*state.touchedTrackIDs = append(*state.touchedTrackIDs, localTrackID)
		}
		if trackCreated {
			field, key, source := "title", track.RecordingProviderKey, FrozenFieldSource{EntityKind: "recording", ProviderKey: track.RecordingProviderKey, Field: "title"}
			if track.TitleSource != nil {
				field, key, source = track.TitleSource.Field, track.TitleSource.ProviderKey, *track.TitleSource
			}
			if err := writeManualMetadata(ctx, tx, "local_track", localTrackID, field, key, seal.SourceID, seal.ProviderID, preview.EvaluationRevision, jsonEvidence(field, source)); err != nil {
				return err
			}
			if len(track.Credits) > 0 {
				if err := materializeFrozenManualArtistCredits(ctx, tx, seal.ProviderID, seal.SourceID, localTrackID, "track_artist", track.Credits, preview.EvaluationRevision); err != nil {
					return err
				}
			}
		}
		if !appliedOverrides[assignment.ProviderTrackID] {
			override := overridesByTrack[assignment.ProviderTrackID]
			if err := applyManualTrackOverrides(ctx, tx, localTrackID, override.TitleOverride, override.ArtistOverrides); err != nil {
				return err
			}
			appliedOverrides[assignment.ProviderTrackID] = true
		}
		var workID uuid.UUID
		if err := tx.NewRaw(`SELECT work_id FROM incoming_match_evaluation_member WHERE group_id=? AND file_member_id=?`, preview.GroupID, assignment.FileMemberID).Scan(ctx, &workID); err != nil {
			return err
		}
		var variants []uuid.UUID
		if err := tx.NewRaw(`SELECT DISTINCT media_variant_id FROM source_work_variant_identity WHERE work_id=? ORDER BY media_variant_id`, workID).Scan(ctx, &variants); err != nil || len(variants) == 0 {
			return ErrIncomingMatchConflict
		}
		metadataResultID, err := sourceWorkMetadataResultID(ctx, tx, workID)
		if err != nil {
			return err
		}
		for _, variant := range variants {
			if err := attachTrackSource(ctx, tx, localTrackID, variant, materializationAuthority{method: "manual"}, metadataResultID); err != nil {
				return err
			}
		}
	}
	return nil
}

func manualMatchingPublicationPositions(ctx context.Context, tx bun.Tx, releaseID uuid.UUID, touchedTrackIDs []uuid.UUID) ([]uuid.UUID, error) {
	query := `SELECT DISTINCT affected.id FROM release_track affected WHERE affected.release_id=?`
	args := []any{releaseID}
	if len(touchedTrackIDs) > 0 {
		query += ` OR affected.track_id IN (?)`
		args = append(args, bun.List(touchedTrackIDs))
	}
	query += ` ORDER BY affected.id`
	var positions []uuid.UUID
	if err := tx.NewRaw(query, args...).Scan(ctx, &positions); err != nil {
		return nil, fmt.Errorf("confirm manual matching: read publication positions: %w", err)
	}
	return positions, nil
}

func jsonEvidence(field string, source FrozenFieldSource) json.RawMessage {
	encoded, _ := json.Marshal(map[string]ProviderFieldEvidence{field: {Authority: source.Authority, FetchOrder: source.FetchOrder}})
	return encoded
}
