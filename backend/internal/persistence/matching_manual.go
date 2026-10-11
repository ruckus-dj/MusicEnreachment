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
	"path"
	"sort"

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
	ProviderTrackID  uuid.UUID  `json:"provider_track_id"`
	LocalTrackID     *uuid.UUID `json:"local_track_id,omitempty"`
	Method           string     `json:"method,omitempty"`
	LocalRecordingID *uuid.UUID `json:"local_recording_id,omitempty"`
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
		if err := materializeManualMatching(ctx, tx, state, result, explicitAssignments); err != nil {
			return err
		}
		var localReleaseID uuid.UUID
		if err := tx.NewRaw(`SELECT local_release_id FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`, state.fence.ProviderID, providerReleaseID).Scan(ctx, &localReleaseID); err != nil {
			return fmt.Errorf("confirm manual matching: read local release mapping: %w", err)
		}
		result.LocalReleaseID = localReleaseID
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
	header         IncomingMatchEvaluationHeader
	fence          IncomingMatchProviderFence
	release        MatchingRelease
	localReleaseID *uuid.UUID
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
	err = tx.NewRaw(`SELECT group_id,evaluation_revision,evaluation_evidence,group_revision,groups_revision,grouping_epoch,policy_version,provider_id,provider_source_id,provider_configuration_identity,provider_namespace,provider_endpoint,cache_key,cache_revision,provider_snapshot_revision,provider_fences,evaluated_at FROM incoming_match_evaluation WHERE group_id=?`, groupID).Scan(ctx, &state.header)
	if errors.Is(err, sql.ErrNoRows) {
		return result, state, ErrIncomingMatchConflict
	}
	if err != nil {
		return result, state, fmt.Errorf("manual matching: read evaluation: %w", err)
	}
	if state.header.EvaluationRevision != expectedRevision {
		return result, state, ErrIncomingMatchConflict
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
	if err := lockAndValidateMatchingPrelude(ctx, tx, evaluation, "manual matching"); err != nil {
		return result, state, err
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
	locationSlots := make(map[uuid.UUID][]string)
	for _, c := range captures {
		if memberSet[c.VariantID] {
			locations[c.VariantID] = append(locations[c.VariantID], c.ConfiguredPath+"/"+c.RelativePath)
			root := c.RootID.String()
			parent := path.Clean(path.Dir(c.RelativePath))
			if parent == "." {
				parent = ""
			}
			locationSlots[c.VariantID] = append(locationSlots[c.VariantID], root+"\x00"+parent)
		}
	}
	assignmentByMember := make(map[uuid.UUID]MatchingManualAssignment, len(assignments))
	for _, a := range assignments {
		if !memberSet[a.FileMemberID] {
			return result, state, errors.New("manual matching assignment references a non-member")
		}
		assignmentByMember[a.FileMemberID] = a
	}
	occupiedSlots := make(map[string]uuid.UUID)
	for _, a := range assignments {
		for _, folder := range locationSlots[a.FileMemberID] {
			slot := fmt.Sprintf("%s\x00%d\x00%d", folder, a.Medium, a.Position)
			if other, exists := occupiedSlots[slot]; exists && other != a.FileMemberID {
				return result, state, errors.New("manual matching assignments collide in a source folder")
			}
			occupiedSlots[slot] = a.FileMemberID
		}
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
		Header  IncomingMatchEvaluationHeader `json:"header"`
		Release MatchingRelease               `json:"release"`
		Members []MatchingManualMember        `json:"members"`
		Current manualCurrentState            `json:"current"`
	}{state.header, release, result.Members, current})
	if err != nil {
		return result, state, err
	}
	sum := sha256.Sum256(digestInput)
	result.PreviewDigest = hex.EncodeToString(sum[:])
	result.LocalReleaseID = state.localReleaseIDOrNil()
	return result, state, nil
}

func (s manualMatchingTxState) localReleaseIDOrNil() uuid.UUID {
	if s.localReleaseID == nil {
		return uuid.Nil
	}
	return *s.localReleaseID
}

func canonicalManualAssignments(in []MatchingManualAssignment) []MatchingManualAssignment {
	out := append([]MatchingManualAssignment(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].FileMemberID.String() < out[j].FileMemberID.String() })
	return out
}
func validateManualAssignments(in []MatchingManualAssignment) error {
	seen := map[uuid.UUID]bool{}
	for _, a := range in {
		if a.FileMemberID == uuid.Nil || a.ProviderTrackID == uuid.Nil || a.Medium < 1 || a.Position < 1 || seen[a.FileMemberID] {
			return errors.New("manual matching assignments are invalid")
		}
		seen[a.FileMemberID] = true
	}
	return nil
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
	for _, p := range positions {
		row := manualTrackState{ProviderTrackID: p.TrackID}
		var localID, recordingID uuid.UUID
		var trackMethod string
		err := tx.NewRaw(`SELECT m.local_track_id,m.method,t.recording_id FROM provider_local_track m JOIN release_track t ON t.track_id=m.local_track_id JOIN provider_local_release r ON r.local_release_id=t.release_id WHERE m.provider_id=? AND m.provider_track_id=? AND r.provider_release_id=?`, providerID, p.TrackID, releaseID).Scan(ctx, &localID, &trackMethod, &recordingID)
		if err == nil {
			row.LocalTrackID = &localID
			row.Method = trackMethod
			row.LocalRecordingID = &recordingID
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
		for _, variantID := range variants {
			if err := attachTrackSource(ctx, tx, localTrackID, variantID, materializationAuthority{method: "manual"}); err != nil {
				return err
			}
		}
	}
	return nil
}
