package service

import (
	"context"
	"fmt"
	"math/big"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// MatchingLayoutRecheckStatus reports whether a recovered MusicBrainz layout
// has a strictly higher exact pair sum than the current automatic Local
// assignment on the same saved file set.
type MatchingLayoutRecheckStatus string

const (
	// MatchingLayoutRecheckRetained keeps the current Local assignment.
	MatchingLayoutRecheckRetained MatchingLayoutRecheckStatus = "retained"
	// MatchingLayoutRecheckMusicBrainz reports a strictly higher exact
	// MusicBrainz sum, including when it uses the same provider coordinates as
	// the current Local assignment. It is a calculation result, not publication
	// authorization.
	MatchingLayoutRecheckMusicBrainz MatchingLayoutRecheckStatus = "musicbrainz"
	// MatchingLayoutRecheckIneligible cannot compare safely and never authorizes
	// a replacement.
	MatchingLayoutRecheckIneligible MatchingLayoutRecheckStatus = "ineligible"
)

// MatchingLayoutRecheckInput freezes one Local-to-MusicBrainz recheck. Captures
// are the exact saved group members used for both providers. CurrentLocal is
// the complete Local edition behind the current automatic assignment.
// CurrentLocalAssignments names its exact per-file provider track IDs.
//
// Local pairs are not re-solved: their actual coordinates are scored even when
// a pair is now below Threshold. Manual authority is never eligible.
type MatchingLayoutRecheckInput struct {
	AuthorityMode string

	Captures []persistence.IncomingGroupingCapture

	MusicBrainzShortlist      []string
	MusicBrainzSnapshot       persistence.ProviderMatchingSnapshot
	MusicBrainzSearchKeys     map[uuid.UUID]MatchingSearchKeys
	MusicBrainzAcoustIDScores map[MatchingAcoustIDKey]float64

	CurrentLocal               persistence.MatchingRelease
	CurrentLocalAcoustIDScores map[MatchingAcoustIDKey]float64
	CurrentLocalAssignments    []MatchingLayoutAssignment

	Threshold float64
}

// MatchingLayoutRecheck is a pure decision. Score strings are canonical
// rational text for reporting only; the comparison itself already happened
// with exact arithmetic and never uses the rounded display score.
type MatchingLayoutRecheck struct {
	Status            MatchingLayoutRecheckStatus `json:"status"`
	Reason            string                      `json:"reason,omitempty"`
	MusicBrainz       MatchingLayoutStage         `json:"music_brainz"`
	CurrentLocalScore string                      `json:"current_local_score,omitempty"`
	MusicBrainzScore  string                      `json:"music_brainz_score,omitempty"`
}

type matchingLayoutRecheckEdgeKey struct {
	fileID   uuid.UUID
	medium   int
	position int
}

// RecheckMatchingLayout decides whether a newly selected MusicBrainz layout is
// strictly better than the current automatic Local assignment on the same file
// set. Both providers receive the shared BuildLayoutMatchingInputs projection
// of the exact saved captures. The current Local assignment is scored at its
// recorded provider tracks, not re-solved, so actual coordinates are never
// replaced merely to improve a score. The function performs no I/O and never
// writes links, enqueues work, or activates the result.
func RecheckMatchingLayout(ctx context.Context, input MatchingLayoutRecheckInput) (MatchingLayoutRecheck, error) {
	if err := ctx.Err(); err != nil {
		return MatchingLayoutRecheck{}, err
	}
	if !validMatchingConfidence(input.Threshold) {
		return MatchingLayoutRecheck{}, fmt.Errorf("matching layout recheck threshold must be between 0 and 1")
	}
	if input.AuthorityMode != "automatic" {
		return ineligibleMatchingLayoutRecheck("manual matching authority is never overridden"), nil
	}
	view, _, err := matchingLayoutStageFiles(input.Captures)
	if err != nil {
		return MatchingLayoutRecheck{}, err
	}
	localPairs, reason := matchingLayoutRecheckLocalPairs(view.Files, input.CurrentLocal, input.CurrentLocalAssignments)
	if reason != "" {
		return ineligibleMatchingLayoutRecheck(reason), nil
	}

	// CalculateMatchingLayoutStage validates the shortlist/snapshot scope and
	// builds exactly one assignment per saved member. A selected stage is
	// therefore already a complete, non-duplicated MusicBrainz scope.
	stage, err := CalculateMatchingLayoutStage(
		ctx, input.Captures, input.MusicBrainzShortlist, input.MusicBrainzSnapshot,
		input.MusicBrainzSearchKeys, input.MusicBrainzAcoustIDScores, input.Threshold,
	)
	if err != nil {
		return MatchingLayoutRecheck{}, err
	}
	result := MatchingLayoutRecheck{MusicBrainz: stage}
	if stage.Decision.Status != MatchingLayoutSelectionSelected {
		reason := stage.Decision.Reason
		if reason == "" {
			reason = "MusicBrainz has no complete qualifying layout"
		}
		result.Status, result.Reason = MatchingLayoutRecheckIneligible, reason
		return result, nil
	}

	if stage.Decision.exactScore == nil {
		return MatchingLayoutRecheck{}, fmt.Errorf("matching layout recheck has no exact MusicBrainz score")
	}

	localEdges, err := BuildLayoutMatchingInputs(
		input.Captures,
		persistence.ProviderMatchingSnapshot{Releases: []persistence.MatchingRelease{input.CurrentLocal}},
		nil,
		input.CurrentLocalAcoustIDScores,
	)
	if err != nil {
		return MatchingLayoutRecheck{}, err
	}
	selected := make([]MatchingInput, 0, len(localPairs))
	for _, edge := range localEdges {
		key := matchingLayoutRecheckEdgeKey{fileID: edge.FileID, medium: edge.Medium, position: edge.Track}
		if localPairs[key] {
			selected = append(selected, edge)
			delete(localPairs, key)
		}
	}
	if len(localPairs) != 0 {
		result.Status, result.Reason = MatchingLayoutRecheckIneligible, "current Local assignment has no comparable saved pair"
		return result, nil
	}
	localScore, err := matchingLayoutRecheckScore(selected)
	if err != nil {
		return MatchingLayoutRecheck{}, err
	}

	result.CurrentLocalScore = localScore.RatString()
	result.MusicBrainzScore = stage.Decision.exactScore.RatString()
	if matchingLayoutRecheckStrictlyBetter(stage.Decision.exactScore, localScore) {
		result.Status = MatchingLayoutRecheckMusicBrainz
		return result, nil
	}
	result.Status = MatchingLayoutRecheckRetained
	result.Reason = "MusicBrainz is not strictly better than the current Local layout"
	return result, nil
}

func ineligibleMatchingLayoutRecheck(reason string) MatchingLayoutRecheck {
	return MatchingLayoutRecheck{Status: MatchingLayoutRecheckIneligible, Reason: reason}
}

// matchingLayoutRecheckLocalPairs resolves recorded provider positions to the
// scoring matrix coordinates, without substituting a different assignment.
func matchingLayoutRecheckLocalPairs(
	files []IncomingFile, release persistence.MatchingRelease, assignments []MatchingLayoutAssignment,
) (map[matchingLayoutRecheckEdgeKey]bool, string) {
	if release.ID == uuid.Nil || !release.Complete || len(release.Tracks) == 0 {
		return nil, "current Local baseline is missing or incomplete"
	}
	byID := make(map[uuid.UUID]persistence.MatchingReleaseTrack, len(release.Tracks))
	coordinates := make(map[[2]int]bool, len(release.Tracks))
	for _, track := range release.Tracks {
		if track.ID == uuid.Nil {
			return nil, "current Local baseline has a missing provider track identity"
		}
		if _, duplicate := byID[track.ID]; duplicate {
			return nil, "current Local baseline repeats a provider track identity"
		}
		if track.Medium < 0 || track.Position < 0 {
			return nil, "current Local baseline has an invalid provider coordinate"
		}
		coordinate := [2]int{track.Medium, track.Position}
		if coordinates[coordinate] {
			return nil, "current Local baseline has duplicate provider coordinates"
		}
		byID[track.ID] = track
		coordinates[coordinate] = true
	}
	if len(assignments) != len(files) {
		return nil, "current Local assignment does not cover the saved file set"
	}
	fileIDs := make(map[uuid.UUID]bool, len(files))
	for _, file := range files {
		fileIDs[file.VariantID] = true
	}
	pairs := make(map[matchingLayoutRecheckEdgeKey]bool, len(assignments))
	for _, assignment := range assignments {
		if !fileIDs[assignment.FileMemberID] {
			return nil, "current Local assignment repeats a file or contains a foreign file"
		}
		delete(fileIDs, assignment.FileMemberID)
		track, exists := byID[assignment.ProviderTrackID]
		if !exists {
			return nil, "current Local assignment references a track outside the baseline edition"
		}
		key := matchingLayoutRecheckEdgeKey{
			fileID: assignment.FileMemberID, medium: track.Medium, position: track.Position,
		}
		pairs[key] = true
	}
	return pairs, ""
}

// matchingLayoutRecheckStrictlyBetter is the policy boundary: equality keeps
// the current Local assignment even when the MusicBrainz layout is otherwise
// eligible.
func matchingLayoutRecheckStrictlyBetter(candidate, baseline *big.Rat) bool {
	return candidate != nil && baseline != nil && candidate.Cmp(baseline) > 0
}

// matchingLayoutRecheckScore sums selected pair confidences as exact
// rationals. Each confidence is already a float64; SetFloat64 retains its exact
// binary value, so the strict comparison never depends on a rounded sum.
func matchingLayoutRecheckScore(edges []MatchingInput) (*big.Rat, error) {
	if len(edges) == 0 {
		return nil, fmt.Errorf("matching layout recheck requires selected pairs")
	}
	total := new(big.Rat)
	for _, edge := range edges {
		if !validMatchingConfidence(edge.AssignmentScore.Confidence) {
			return nil, fmt.Errorf("matching layout recheck pair has invalid confidence")
		}
		var value big.Rat
		total.Add(total, value.SetFloat64(edge.AssignmentScore.Confidence))
	}
	return total, nil
}
