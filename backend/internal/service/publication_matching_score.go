package service

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func ScorePublicationMatchingCapture(capture persistence.IncomingGroupingCapture, provider persistence.PublicationProviderTagContext) (*float64, error) {
	if capture.MediaVariantID == nil {
		return nil, nil
	}
	score, scored, err := ScorePublicationMatchingProviderContext(
		[]persistence.IncomingGroupingCapture{capture}, provider, capture.WorkID, *capture.MediaVariantID, PublicationMatchingEvidence{},
	)
	if err != nil || !scored || score.TotalWeight == 0 {
		return nil, err
	}
	return &score.Confidence, nil
}

// ScorePublicationMatchingLayoutCapture evaluates one exact Local capture
// against the selected provider position. It returns trackEvidence=false when
// the pair has a confidence but no audio-to-track proof, matching the gate used
// by automatic layout selection.
func ScorePublicationMatchingLayoutCapture(capture persistence.IncomingGroupingCapture, provider persistence.PublicationProviderTagContext) (*float64, bool, error) {
	if capture.MediaVariantID == nil {
		return nil, false, nil
	}
	score, scored, err := ScorePublicationMatchingProviderContext(
		[]persistence.IncomingGroupingCapture{capture}, provider, capture.WorkID, *capture.MediaVariantID, PublicationMatchingEvidence{},
	)
	if err != nil || !scored || score.TotalWeight == 0 {
		return nil, false, err
	}
	return &score.Confidence, matchingLayoutHasTrackEvidence(score), nil
}

// PublicationMatchingPair identifies the exact saved source and accepted
// provider position whose assignment confidence publication records. WorkID and
// MediaVariantID select the frozen publication source; Medium and Track are the
// persisted provider coordinates (zero-based, as in MatchingInput).
type PublicationMatchingPair struct {
	WorkID         uuid.UUID
	MediaVariantID uuid.UUID
	ReleaseID      string
	RecordingID    string
	Medium         int
	Track          int
}

// PublicationMatchingEvidence carries the same optional provider evidence used
// by assignment scoring. A missing entry stays missing; it is never replaced
// with a value from another source file, recording, or search response.
type PublicationMatchingEvidence struct {
	SearchKeys       map[uuid.UUID]MatchingSearchKeys
	AcoustIDScores   map[MatchingAcoustIDKey]float64
	RecordingArtists map[string][]string
}

// PublicationMatchingPairForSource binds one frozen publication source to the
// accepted provider release-position it was assigned. It copies the persisted
// zero-based coordinates without conversion.
func PublicationMatchingPairForSource(
	workID, mediaVariantID uuid.UUID,
	release persistence.MatchingRelease,
	position persistence.MatchingReleaseTrack,
) PublicationMatchingPair {
	return PublicationMatchingPair{
		WorkID: workID, MediaVariantID: mediaVariantID,
		ReleaseID: release.ProviderKey, RecordingID: position.RecordingProviderKey,
		Medium: position.Medium, Track: position.Position,
	}
}

// PublicationMatchingProviderSnapshot narrows an accepted provider tag context
// to the release graph used for scoring. The complete track graph is preserved
// because release confidence depends on its totals; the exact position is
// selected separately in the pair. ok is false when the context does not carry
// one unambiguous release-position.
func PublicationMatchingProviderSnapshot(provider persistence.PublicationProviderTagContext) (persistence.ProviderMatchingSnapshot, bool) {
	if provider.Release.ProviderKey == "" || provider.Position.ID == uuid.Nil ||
		provider.Position.RecordingProviderKey == "" {
		return persistence.ProviderMatchingSnapshot{}, false
	}
	positionFound := false
	for _, track := range provider.Release.Tracks {
		if track.ID != provider.Position.ID || track.Medium != provider.Position.Medium ||
			track.Position != provider.Position.Position ||
			track.RecordingProviderKey != provider.Position.RecordingProviderKey {
			continue
		}
		positionFound = true
		break
	}
	if !positionFound {
		return persistence.ProviderMatchingSnapshot{}, false
	}
	return persistence.ProviderMatchingSnapshot{
		Source:   provider.Source,
		Releases: []persistence.MatchingRelease{provider.Release},
	}, true
}

// ScorePublicationMatchingProviderContext scores the exact frozen publication
// source against the release-position carried by an accepted provider context.
// ok is false when the source identity or provider position is absent; another
// source or position is never substituted.
func ScorePublicationMatchingProviderContext(
	captures []persistence.IncomingGroupingCapture,
	provider persistence.PublicationProviderTagContext,
	workID, mediaVariantID uuid.UUID,
	evidence PublicationMatchingEvidence,
) (MatchingScore, bool, error) {
	if workID == uuid.Nil || mediaVariantID == uuid.Nil {
		return MatchingScore{}, false, nil
	}
	snapshot, ok := PublicationMatchingProviderSnapshot(provider)
	if !ok {
		return MatchingScore{}, false, nil
	}
	pair := PublicationMatchingPairForSource(workID, mediaVariantID, provider.Release, provider.Position)
	return ScorePublicationMatching(captures, snapshot, pair, evidence)
}

// ScorePublicationMatching returns the exact assignment confidence for one
// saved source file and accepted provider release-position. It reuses the same
// BuildMatchingInputs projection and ScoreMatching calculation as automatic and
// manual assignment. ok is false when any part of the exact pair is absent, and
// no other source, release, recording, or position is substituted.
func ScorePublicationMatching(
	captures []persistence.IncomingGroupingCapture,
	snapshot persistence.ProviderMatchingSnapshot,
	pair PublicationMatchingPair,
	evidence PublicationMatchingEvidence,
) (MatchingScore, bool, error) {
	if pair.WorkID == uuid.Nil || pair.MediaVariantID == uuid.Nil ||
		pair.ReleaseID == "" || pair.RecordingID == "" || pair.Medium < 0 || pair.Track < 0 {
		return MatchingScore{}, false, nil
	}

	selectedCaptures := make([]persistence.IncomingGroupingCapture, 0, 1)
	for _, capture := range captures {
		if capture.WorkID != pair.WorkID || capture.MediaVariantID == nil ||
			*capture.MediaVariantID != pair.MediaVariantID {
			continue
		}
		selectedCaptures = append(selectedCaptures, capture)
	}
	if len(selectedCaptures) == 0 {
		return MatchingScore{}, false, nil
	}

	selectedReleases := make([]persistence.MatchingRelease, 0, 1)
	for _, release := range snapshot.Releases {
		if release.ProviderKey != pair.ReleaseID {
			continue
		}
		if len(selectedReleases) != 0 {
			return MatchingScore{}, false, fmt.Errorf("publication matching release identity is ambiguous")
		}
		selectedReleases = append(selectedReleases, release)
	}
	if len(selectedReleases) == 0 {
		return MatchingScore{}, false, nil
	}

	inputs, err := buildMatchingInputs(
		selectedCaptures,
		persistence.ProviderMatchingSnapshot{
			Source:       snapshot.Source,
			Releases:     selectedReleases,
			SearchScores: snapshot.SearchScores,
		},
		matchingScoringOptions{searchKeys: evidence.SearchKeys, acoustIDScores: evidence.AcoustIDScores, recordingArtists: evidence.RecordingArtists, layoutPolicy: true},
	)
	if err != nil {
		return MatchingScore{}, false, err
	}

	var selected *MatchingInput
	for index := range inputs {
		input := &inputs[index]
		if input.ReleaseID != pair.ReleaseID || input.RecordingID != pair.RecordingID ||
			input.Medium != pair.Medium || input.Track != pair.Track {
			continue
		}
		if selected != nil {
			return MatchingScore{}, false, fmt.Errorf("publication matching pair is ambiguous")
		}
		selected = input
	}
	if selected == nil {
		return MatchingScore{}, false, nil
	}

	score, err := ScoreMatching(selected.AssignmentInput)
	if err != nil {
		return MatchingScore{}, false, fmt.Errorf("score publication matching pair: %w", err)
	}
	return score, true, nil
}
