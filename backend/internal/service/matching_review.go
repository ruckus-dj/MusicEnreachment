package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingReviewReader interface {
	GetReviewSnapshot(context.Context, uuid.UUID) (*persistence.IncomingMatchReviewSnapshot, error)
}

// MatchingReviewService projects only the exact persisted evaluation snapshot.
type MatchingReviewService struct{ reader matchingReviewReader }

func NewMatchingReviewService(reader matchingReviewReader) *MatchingReviewService {
	return &MatchingReviewService{reader: reader}
}

type MatchingReview struct {
	Evaluated                         bool                      `json:"evaluated"`
	ProposalAvailable                 bool                      `json:"proposal_available"`
	EvaluationRevision                string                    `json:"evaluation_revision,omitempty"`
	GroupID                           uuid.UUID                 `json:"group_id"`
	Proposal                          json.RawMessage           `json:"proposal,omitempty"`
	Releases                          []MatchingReviewRelease   `json:"releases"`
	Candidates                        []MatchingReviewCandidate `json:"candidates"`
	Members                           []uuid.UUID               `json:"members"`
	AuthorityMode                     string                    `json:"authority_mode"`
	AuthorityRevision                 int64                     `json:"authority_revision"`
	CurrentLinkedManualMediaSourceIDs []uuid.UUID               `json:"current_linked_manual_media_source_ids"`
}

type MatchingReviewRelease struct {
	ProviderReleaseID uuid.UUID                      `json:"provider_release_id"`
	ReleaseID         string                         `json:"release_id"`
	Title             string                         `json:"title"`
	TracklistComplete bool                           `json:"tracklist_complete"`
	Positions         []MatchingReviewLayoutPosition `json:"positions"`
}

type MatchingReviewLayoutPosition struct {
	TrackID         uuid.UUID `json:"track_id"`
	Medium          int       `json:"medium"`
	Position        int       `json:"position"`
	DisplayedNumber string    `json:"displayed_number"`
	RecordingID     string    `json:"recording_id"`
	Title           string    `json:"title"`
}

type MatchingReviewCandidate struct {
	FileMemberID      uuid.UUID                `json:"file_member_id"`
	ReleaseID         uuid.UUID                `json:"release_id"`
	ReleaseConfidence float64                  `json:"release_confidence"`
	Positions         []MatchingReviewPosition `json:"positions"`
	Edges             []json.RawMessage        `json:"edges"`
}

type MatchingReviewPosition struct {
	TrackID              uuid.UUID `json:"track_id"`
	Medium               int       `json:"medium"`
	Position             int       `json:"position"`
	DisplayedNumber      string    `json:"displayed_number,omitempty"`
	AssignmentConfidence *float64  `json:"assignment_confidence,omitempty"`
}

type reviewProposal struct {
	Version         int                     `json:"version"`
	GroupID         uuid.UUID               `json:"group_id"`
	SelectedRelease *reviewProposalRelease  `json:"selected_release"`
	Releases        []reviewProposalRelease `json:"releases"`
	Files           []struct {
		FileMemberID uuid.UUID                    `json:"file_member_id"`
		RawTags      map[string][]string          `json:"raw_tags"`
		Diagnostics  []IncomingGroupingDiagnostic `json:"diagnostics"`
	} `json:"files"`
}

type reviewProposalRelease struct {
	ProviderReleaseID uuid.UUID                      `json:"provider_release_id"`
	ReleaseID         string                         `json:"release_id"`
	Title             string                         `json:"title"`
	TracklistComplete bool                           `json:"tracklist_complete"`
	Positions         []MatchingReviewLayoutPosition `json:"positions"`
}

func (s *MatchingReviewService) Get(ctx context.Context, groupID uuid.UUID) (MatchingReview, error) {
	if s == nil || s.reader == nil || groupID == uuid.Nil {
		return MatchingReview{}, fmt.Errorf("matching review is unavailable")
	}
	snapshot, err := s.reader.GetReviewSnapshot(ctx, groupID)
	if err != nil {
		return MatchingReview{}, err
	}
	if snapshot == nil {
		return MatchingReview{GroupID: groupID, AuthorityMode: "automatic", Releases: []MatchingReviewRelease{}, Candidates: []MatchingReviewCandidate{}, Members: []uuid.UUID{}, CurrentLinkedManualMediaSourceIDs: []uuid.UUID{}}, nil
	}
	if snapshot.Header == nil {
		mode := snapshot.AuthorityMode
		if mode == "" {
			mode = "automatic"
		}
		return MatchingReview{GroupID: groupID, AuthorityMode: mode, AuthorityRevision: snapshot.AuthorityRevision,
			CurrentLinkedManualMediaSourceIDs: snapshot.CurrentLinkedManualMediaSourceIDs, Releases: []MatchingReviewRelease{}, Candidates: []MatchingReviewCandidate{}, Members: []uuid.UUID{}}, nil
	}
	result := MatchingReview{Evaluated: true, GroupID: groupID, EvaluationRevision: snapshot.Header.EvaluationRevision,
		AuthorityMode: snapshot.AuthorityMode, AuthorityRevision: snapshot.AuthorityRevision,
		CurrentLinkedManualMediaSourceIDs: snapshot.CurrentLinkedManualMediaSourceIDs,
		Releases:                          []MatchingReviewRelease{}, Candidates: make([]MatchingReviewCandidate, 0, len(snapshot.Candidates)), Members: append([]uuid.UUID(nil), snapshot.Members...)}
	var envelope struct {
		Proposal json.RawMessage `json:"review_proposal"`
	}
	selectedReleaseID := uuid.Nil
	selectedDisplayNumbers := make(map[[2]int]string)
	if err := json.Unmarshal(snapshot.Header.EvaluationEvidence, &envelope); err != nil {
		return MatchingReview{}, fmt.Errorf("decode persisted matching review: %w", err)
	}
	if len(envelope.Proposal) != 0 && !bytes.Equal(bytes.TrimSpace(envelope.Proposal), []byte("null")) {
		var proposal reviewProposal
		decoder := json.NewDecoder(bytes.NewReader(envelope.Proposal))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&proposal); err == nil && proposal.Version == 1 && proposal.GroupID == groupID && validateReviewProposal(proposal, snapshot.Members) == nil {
			result.ProposalAvailable = true
			result.Proposal = append(json.RawMessage(nil), envelope.Proposal...)
			if proposal.SelectedRelease != nil {
				selectedReleaseID = proposal.SelectedRelease.ProviderReleaseID
				for _, position := range proposal.SelectedRelease.Positions {
					selectedDisplayNumbers[[2]int{position.Medium, position.Position}] = position.DisplayedNumber
				}
			}
			for _, release := range proposal.Releases {
				positions := append([]MatchingReviewLayoutPosition(nil), release.Positions...)
				if snapshot.Header.FrozenAuthorityVersion != nil && *snapshot.Header.FrozenAuthorityVersion == 3 {
					for index := range positions {
						positions[index].Medium--
						positions[index].Position--
					}
				}
				result.Releases = append(result.Releases, MatchingReviewRelease{
					ProviderReleaseID: release.ProviderReleaseID, ReleaseID: release.ReleaseID,
					Title: release.Title, TracklistComplete: release.TracklistComplete,
					Positions: positions,
				})
			}
		}
	}
	for _, candidate := range snapshot.Candidates {
		if candidate.FileMemberID == uuid.Nil || candidate.ProviderID == uuid.Nil || candidate.ProviderSourceID == uuid.Nil || candidate.ReleaseID == uuid.Nil || math.IsNaN(candidate.Score) || math.IsInf(candidate.Score, 0) {
			return MatchingReview{}, fmt.Errorf("persisted matching review candidate identity is invalid")
		}
		if !containsUUID(snapshot.Members, candidate.FileMemberID) {
			return MatchingReview{}, fmt.Errorf("persisted matching review candidate is outside group scope")
		}
		item := MatchingReviewCandidate{FileMemberID: candidate.FileMemberID, ReleaseID: candidate.ReleaseID, ReleaseConfidence: candidate.Score, Positions: make([]MatchingReviewPosition, 0, len(candidate.Positions)), Edges: make([]json.RawMessage, 0)}
		seenPositions := make(map[string]bool)
		for _, position := range candidate.Positions {
			key := fmt.Sprintf("%d/%d", position.Medium, position.Position)
			if position.TrackID == uuid.Nil || position.Medium < 0 || position.Position < 0 || seenPositions[key] {
				return MatchingReview{}, fmt.Errorf("persisted matching review candidate position is invalid")
			}
			seenPositions[key] = true
			displayedNumber := ""
			if candidate.ReleaseID == selectedReleaseID {
				displayedNumber = selectedDisplayNumbers[[2]int{position.Medium, position.Position}]
			}
			item.Positions = append(item.Positions, MatchingReviewPosition{TrackID: position.TrackID, Medium: position.Medium, Position: position.Position, DisplayedNumber: displayedNumber})
		}
		var evidence struct {
			Edges []json.RawMessage `json:"edge_evidence"`
		}
		if err := json.Unmarshal(candidate.Evidence, &evidence); err != nil {
			return MatchingReview{}, fmt.Errorf("decode persisted matching candidate evidence: %w", err)
		}
		for _, raw := range evidence.Edges {
			var edge struct {
				Version    int `json:"schema_version"`
				Medium     int `json:"medium"`
				Position   int `json:"position"`
				Assignment struct {
					Profile    string  `json:"profile"`
					Confidence float64 `json:"confidence"`
				} `json:"assignment_profile"`
			}
			if err := json.Unmarshal(raw, &edge); err != nil || edge.Version != 1 || edge.Medium < 0 || edge.Position < 0 || edge.Assignment.Profile == "" || math.IsNaN(edge.Assignment.Confidence) || math.IsInf(edge.Assignment.Confidence, 0) || edge.Assignment.Confidence < 0 || edge.Assignment.Confidence > 1 {
				return MatchingReview{}, fmt.Errorf("persisted matching review edge is invalid")
			}
			for i := range item.Positions {
				if item.Positions[i].Medium == edge.Medium && item.Positions[i].Position == edge.Position {
					confidence := edge.Assignment.Confidence
					item.Positions[i].AssignmentConfidence = &confidence
					break
				}
			}
		}
		item.Edges = evidence.Edges
		result.Candidates = append(result.Candidates, item)
	}
	return result, nil
}

func validateReviewProposal(proposal reviewProposal, members []uuid.UUID) error {
	seenMembers := make(map[uuid.UUID]bool, len(proposal.Files))
	for _, file := range proposal.Files {
		if file.FileMemberID == uuid.Nil || seenMembers[file.FileMemberID] || !containsUUID(members, file.FileMemberID) {
			return fmt.Errorf("persisted matching review file scope is invalid")
		}
		seenMembers[file.FileMemberID] = true
	}
	if len(seenMembers) != len(members) {
		return fmt.Errorf("persisted matching review omits group members")
	}
	seenReleases := make(map[string]bool, len(proposal.Releases))
	for _, release := range proposal.Releases {
		if err := validateReviewProposalRelease(release, seenReleases, false); err != nil {
			return err
		}
		seenReleases[release.ReleaseID] = true
	}
	if selected := proposal.SelectedRelease; selected != nil {
		if err := validateReviewProposalRelease(*selected, nil, true); err != nil {
			return err
		}
	}
	return nil
}

func validateReviewProposalRelease(release reviewProposalRelease, seenReleases map[string]bool, requireComplete bool) error {
	if release.ProviderReleaseID == uuid.Nil || release.ReleaseID == "" || seenReleases != nil && seenReleases[release.ReleaseID] {
		return fmt.Errorf("persisted matching review release is invalid")
	}
	seen := make(map[string]bool, len(release.Positions))
	for _, position := range release.Positions {
		key := fmt.Sprintf("%d/%d", position.Medium, position.Position)
		if position.TrackID == uuid.Nil || position.Medium < 0 || position.Position < 0 || position.RecordingID == "" || seen[key] {
			return fmt.Errorf("persisted matching review release position is invalid")
		}
		seen[key] = true
	}
	if requireComplete && !release.TracklistComplete {
		return fmt.Errorf("persisted matching review release layout is incomplete")
	}
	return nil
}

func containsUUID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}
