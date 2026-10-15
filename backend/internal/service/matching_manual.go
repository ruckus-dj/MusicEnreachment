package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var ErrMatchingManualInvalid = errors.New("manual matching request is invalid")
var ErrMatchingManualConflict = errors.New("manual matching snapshot changed")

type matchingManualRepository interface {
	PreviewMatchingManual(context.Context, uuid.UUID, string, uuid.UUID, []persistence.MatchingManualAssignment) (persistence.MatchingManualPreview, error)
	ConfirmMatchingManual(context.Context, uuid.UUID, string, uuid.UUID, []persistence.MatchingManualAssignment, string, uuid.UUID) (persistence.MatchingManualPreview, error)
}

type MatchingManualAssignment struct {
	FileMemberID    uuid.UUID `json:"file_member_id"`
	ProviderTrackID uuid.UUID `json:"provider_track_id"`
	Medium          int       `json:"medium"`
	Position        int       `json:"position"`
	TitleOverride   *string   `json:"title_override,omitempty"`
	ArtistOverrides *[]string `json:"artist_overrides,omitempty"`
}

type MatchingManualMember struct {
	FileMemberID uuid.UUID                 `json:"file_member_id"`
	Locations    []string                  `json:"locations"`
	Assignment   *MatchingManualAssignment `json:"assignment,omitempty"`
}

type MatchingManualPreview struct {
	GroupID            uuid.UUID              `json:"group_id"`
	EvaluationRevision string                 `json:"evaluation_revision"`
	ProviderReleaseID  uuid.UUID              `json:"provider_release_id"`
	SelectedRelease    MatchingReviewRelease  `json:"selected_release"`
	LocalReleaseID     uuid.UUID              `json:"local_release_id,omitempty"`
	PreviewDigest      string                 `json:"preview_digest"`
	Members            []MatchingManualMember `json:"members"`
	OmittedMemberIDs   []uuid.UUID            `json:"omitted_member_ids"`
}

type MatchingManualService struct{ repository matchingManualRepository }

func NewMatchingManualService(repository matchingManualRepository) *MatchingManualService {
	return &MatchingManualService{repository: repository}
}

func (s *MatchingManualService) Preview(ctx context.Context, groupID uuid.UUID, expectedRevision string, releaseID uuid.UUID, assignments []MatchingManualAssignment) (MatchingManualPreview, error) {
	if s == nil || s.repository == nil {
		return MatchingManualPreview{}, errors.New("manual matching is unavailable")
	}
	canonical, err := validateMatchingManualRequest(groupID, expectedRevision, releaseID, assignments)
	if err != nil {
		return MatchingManualPreview{}, err
	}
	preview, err := s.repository.PreviewMatchingManual(ctx, groupID, expectedRevision, releaseID, canonical)
	if err != nil {
		return MatchingManualPreview{}, matchingManualError(err)
	}
	return matchingManualResult(preview), nil
}

func (s *MatchingManualService) Confirm(ctx context.Context, groupID uuid.UUID, expectedRevision string, releaseID uuid.UUID, assignments []MatchingManualAssignment, previewDigest string, requestID uuid.UUID) (MatchingManualPreview, error) {
	if s == nil || s.repository == nil {
		return MatchingManualPreview{}, errors.New("manual matching is unavailable")
	}
	canonical, err := validateMatchingManualRequest(groupID, expectedRevision, releaseID, assignments)
	if err != nil {
		return MatchingManualPreview{}, err
	}
	if previewDigest == "" || requestID == uuid.Nil {
		return MatchingManualPreview{}, fmt.Errorf("%w: confirmation identity is required", ErrMatchingManualInvalid)
	}
	preview, err := s.repository.ConfirmMatchingManual(ctx, groupID, expectedRevision, releaseID, canonical, previewDigest, requestID)
	if err != nil {
		return MatchingManualPreview{}, matchingManualError(err)
	}
	return matchingManualResult(preview), nil
}

func validateMatchingManualRequest(groupID uuid.UUID, revision string, releaseID uuid.UUID, assignments []MatchingManualAssignment) ([]persistence.MatchingManualAssignment, error) {
	if groupID == uuid.Nil || strings.TrimSpace(revision) == "" || releaseID == uuid.Nil {
		return nil, fmt.Errorf("%w: group, evaluation revision, and provider release are required", ErrMatchingManualInvalid)
	}
	canonical := make([]persistence.MatchingManualAssignment, 0, len(assignments))
	seenMembers := make(map[uuid.UUID]bool, len(assignments))
	titles := make(map[uuid.UUID]*string)
	artists := make(map[uuid.UUID]*[]string)
	for _, assignment := range assignments {
		if assignment.FileMemberID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil || assignment.Medium < 1 || assignment.Position < 1 || seenMembers[assignment.FileMemberID] {
			return nil, fmt.Errorf("%w: assignments must name unique members and valid positions", ErrMatchingManualInvalid)
		}
		seenMembers[assignment.FileMemberID] = true
		if assignment.TitleOverride != nil {
			if previous, ok := titles[assignment.ProviderTrackID]; ok && *previous != *assignment.TitleOverride {
				return nil, fmt.Errorf("%w: conflicting overrides for provider track", ErrMatchingManualInvalid)
			}
			titles[assignment.ProviderTrackID] = assignment.TitleOverride
		}
		if assignment.ArtistOverrides != nil {
			if previous, ok := artists[assignment.ProviderTrackID]; ok && !equalManualOverrideStrings(*previous, *assignment.ArtistOverrides) {
				return nil, fmt.Errorf("%w: conflicting overrides for provider track", ErrMatchingManualInvalid)
			}
			artists[assignment.ProviderTrackID] = assignment.ArtistOverrides
		}
		canonical = append(canonical, persistence.MatchingManualAssignment{FileMemberID: assignment.FileMemberID, ProviderTrackID: assignment.ProviderTrackID, Medium: assignment.Medium, Position: assignment.Position, TitleOverride: copyManualString(assignment.TitleOverride), ArtistOverrides: copyManualStrings(assignment.ArtistOverrides)})
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].FileMemberID.String() < canonical[j].FileMemberID.String() })
	return canonical, nil
}

func equalManualOverrideStrings(left, right []string) bool {
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

func matchingManualError(err error) error {
	if errors.Is(err, persistence.ErrIncomingMatchConflict) || errors.Is(err, persistence.ErrMatchingManualProtection) {
		return fmt.Errorf("%w: %v", ErrMatchingManualConflict, err)
	}
	// These are the persistence layer's direct validation failures. Wrapped errors
	// (including database and fence-read failures) intentionally remain internal errors.
	switch err.Error() {
	case "manual matching identity is invalid",
		"manual matching assignments are invalid",
		"manual matching release is not in the persisted evaluation scope",
		"manual matching selected release layout is unavailable",
		"manual matching selected release layout is invalid",
		"manual matching assignment is outside selected release layout",
		"manual matching assignment references a non-member":
		return fmt.Errorf("%w: %s", ErrMatchingManualInvalid, err)
	default:
		return err
	}
}

func matchingManualResult(value persistence.MatchingManualPreview) MatchingManualPreview {
	result := MatchingManualPreview{GroupID: value.GroupID, EvaluationRevision: value.EvaluationRevision,
		ProviderReleaseID: value.ProviderReleaseID, LocalReleaseID: value.LocalReleaseID,
		PreviewDigest: value.PreviewDigest, Members: make([]MatchingManualMember, 0, len(value.Members)),
		OmittedMemberIDs: append([]uuid.UUID{}, value.OmittedMemberIDs...)}
	result.SelectedRelease = MatchingReviewRelease{
		ProviderReleaseID: value.SelectedRelease.ProviderReleaseID,
		ReleaseID:         value.SelectedRelease.ReleaseID,
		Title:             value.SelectedRelease.Title,
		TracklistComplete: value.SelectedRelease.TracklistComplete,
		Positions:         make([]MatchingReviewLayoutPosition, 0, len(value.SelectedRelease.Positions)),
	}
	for _, position := range value.SelectedRelease.Positions {
		result.SelectedRelease.Positions = append(result.SelectedRelease.Positions, MatchingReviewLayoutPosition{
			TrackID: position.TrackID, Medium: position.Medium, Position: position.Position,
			DisplayedNumber: position.DisplayedNumber, RecordingID: position.RecordingID, Title: position.Title,
		})
	}
	for _, member := range value.Members {
		item := MatchingManualMember{FileMemberID: member.FileMemberID, Locations: append([]string{}, member.Locations...)}
		if member.Assignment != nil {
			assignment := MatchingManualAssignment{FileMemberID: member.Assignment.FileMemberID, ProviderTrackID: member.Assignment.ProviderTrackID, Medium: member.Assignment.Medium, Position: member.Assignment.Position, TitleOverride: copyManualString(member.Assignment.TitleOverride), ArtistOverrides: copyManualStrings(member.Assignment.ArtistOverrides)}
			item.Assignment = &assignment
		}
		result.Members = append(result.Members, item)
	}
	return result
}

func copyManualString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyManualStrings(value *[]string) *[]string {
	if value == nil {
		return nil
	}
	cloned := make([]string, len(*value))
	copy(cloned, *value)
	return &cloned
}
