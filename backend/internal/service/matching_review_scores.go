package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func readScoredMatchingReview(ctx context.Context, reader matchingReviewInputReader, review MatchingReview, evidence json.RawMessage) ([]MatchingReviewCandidate, error) {
	result := make([]MatchingReviewCandidate, 0)
	if !review.ProposalAvailable || len(review.Releases) == 0 {
		return result, nil
	}
	var input struct {
		Files []struct {
			FileMemberID uuid.UUID `json:"file_member_id"`
			ReleaseKey   string    `json:"release_query_key"`
			RecordingKey string    `json:"recording_query_key"`
		} `json:"files"`
	}
	if err := json.Unmarshal(evidence, &input); err != nil {
		return nil, fmt.Errorf("read current matching review query scope: %w", err)
	}
	releaseKeys := make([]string, 0, len(review.Releases))
	releases := make(map[string]MatchingReviewRelease, len(review.Releases))
	for _, release := range review.Releases {
		releaseKeys = append(releaseKeys, release.ReleaseID)
		releases[release.ReleaseID] = release
	}
	searchKeys := make(map[uuid.UUID]MatchingSearchKeys)
	cacheKeys := make([]string, 0, 2*len(input.Files))
	for _, file := range input.Files {
		searchKeys[file.FileMemberID] = MatchingSearchKeys{Release: file.ReleaseKey, Recording: file.RecordingKey}
		for _, key := range []string{file.ReleaseKey, file.RecordingKey} {
			if key != "" {
				cacheKeys = append(cacheKeys, key)
			}
		}
	}
	slices.Sort(cacheKeys)
	captures, provider, err := reader.ReadMatchingReviewInputs(ctx, review.GroupID, review.EvaluationRevision, releaseKeys, slices.Compact(cacheKeys))
	if errors.Is(err, persistence.ErrIncomingMatchConflict) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	edges, err := BuildLayoutMatchingInputs(captures, provider, searchKeys, nil)
	if err != nil {
		return nil, err
	}
	byFileRelease := make(map[matchingGroupEdgeKey]int)
	for _, edge := range edges {
		release, ok := releases[edge.ReleaseID]
		if !ok || !containsUUID(review.Members, edge.FileID) {
			continue
		}
		trackID := matchingGroupTrackID(provider.Releases, edge.ReleaseID, edge.Medium, edge.Track)
		position := slices.IndexFunc(release.Positions, func(position MatchingReviewLayoutPosition) bool {
			return position.TrackID == trackID && position.Medium == edge.Medium && position.Position == edge.Track
		})
		if position < 0 {
			continue
		}
		key := matchingGroupEdgeKey{edge.FileID, edge.ReleaseID}
		index, found := byFileRelease[key]
		if !found {
			index = len(result)
			byFileRelease[key] = index
			result = append(result, MatchingReviewCandidate{
				FileMemberID: edge.FileID, ReleaseID: release.ProviderReleaseID, Positions: []MatchingReviewPosition{}, Edges: []json.RawMessage{},
			})
		}
		candidate := &result[index]
		candidate.ReleaseConfidence = max(candidate.ReleaseConfidence, edge.ReleaseScore.Confidence)
		var confidence *float64
		if edge.AssignmentScore.TotalWeight > 0 {
			value := edge.AssignmentScore.Confidence
			confidence = &value
		}
		candidate.Positions = append(candidate.Positions, MatchingReviewPosition{
			TrackID: trackID, Medium: edge.Medium, Position: edge.Track,
			DisplayedNumber: release.Positions[position].DisplayedNumber, AssignmentConfidence: confidence,
		})
		raw, err := json.Marshal(map[string]any{
			"schema_version": 1, "medium": edge.Medium, "position": edge.Track,
			"release_profile": edge.ReleaseScore, "assignment_profile": edge.AssignmentScore,
			"release_profile_input": edge.ReleaseScoreInput, "assignment_profile_input": edge.AssignmentInput,
			"mbid_diagnostics": edge.Diagnostics,
		})
		if err != nil {
			return nil, err
		}
		candidate.Edges = append(candidate.Edges, raw)
	}
	return result, nil
}
