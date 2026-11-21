package service

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestMatchingLayoutRecheckPolicy(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	root := uuid.New()
	firstLocal, secondLocal := uuid.New(), uuid.New()
	firstMusicBrainz, secondMusicBrainz := uuid.New(), uuid.New()
	captures := []persistence.IncomingGroupingCapture{
		matchingLayoutRecheckCapture(first, root, "One", 1),
		matchingLayoutRecheckCapture(second, root, "Two", 2),
	}
	localAssignments := []MatchingLayoutAssignment{
		{FileMemberID: first, ProviderTrackID: firstLocal},
		{FileMemberID: second, ProviderTrackID: secondLocal},
	}
	input := MatchingLayoutRecheckInput{
		AuthorityMode:        "automatic",
		Captures:             captures,
		MusicBrainzShortlist: []string{"musicbrainz-edition"},
		MusicBrainzSnapshot: persistence.ProviderMatchingSnapshot{Releases: []persistence.MatchingRelease{
			matchingLayoutRecheckRelease("musicbrainz-edition", firstMusicBrainz, secondMusicBrainz, "One", "Two"),
		}},
		CurrentLocal:            matchingLayoutRecheckRelease("local-edition", firstLocal, secondLocal, "One", "Two"),
		CurrentLocalAssignments: localAssignments,
		Threshold:               0.70,
	}

	t.Run("equal exact sums retain Local", func(t *testing.T) {
		got, err := RecheckMatchingLayout(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != MatchingLayoutRecheckRetained {
			t.Fatalf("status = %q, want retained: %+v", got.Status, got)
		}
		if got.CurrentLocalScore == "" || got.CurrentLocalScore != got.MusicBrainzScore {
			t.Fatalf("equal layouts must report equal exact sums: %q / %q", got.CurrentLocalScore, got.MusicBrainzScore)
		}
	})

	t.Run("strict win with the same provider positions", func(t *testing.T) {
		win := input
		win.CurrentLocal = matchingLayoutRecheckRelease(
			"local-edition", firstLocal, secondLocal, "Wrong One", "Wrong Two",
		)
		got, err := RecheckMatchingLayout(t.Context(), win)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != MatchingLayoutRecheckMusicBrainz {
			t.Fatalf("status = %q, want musicbrainz: %+v", got.Status, got)
		}
		if got.CurrentLocalScore == "" || got.MusicBrainzScore == "" ||
			got.CurrentLocalScore == got.MusicBrainzScore {
			t.Fatalf("strict win must report distinct exact sums: %q / %q", got.CurrentLocalScore, got.MusicBrainzScore)
		}
		localCoordinates := matchingLayoutRecheckCoordinates(input.CurrentLocal, localAssignments)
		musicBrainzCoordinates := matchingLayoutRecheckCoordinates(
			input.MusicBrainzSnapshot.Releases[0], got.MusicBrainz.Assignments,
		)
		if !maps.Equal(localCoordinates, musicBrainzCoordinates) {
			t.Fatalf("win changed provider coordinates: local=%v musicbrainz=%v", localCoordinates, musicBrainzCoordinates)
		}
	})

	t.Run("different file set is not a win", func(t *testing.T) {
		mismatch := input
		mismatch.CurrentLocalAssignments = []MatchingLayoutAssignment{
			{FileMemberID: first, ProviderTrackID: firstLocal},
		}
		got, err := RecheckMatchingLayout(t.Context(), mismatch)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != MatchingLayoutRecheckIneligible || got.Reason == "" {
			t.Fatalf("partial baseline must be ineligible: %+v", got)
		}
	})

	t.Run("manual authority is never overridden", func(t *testing.T) {
		manual := input
		manual.AuthorityMode = "manual"
		got, err := RecheckMatchingLayout(t.Context(), manual)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != MatchingLayoutRecheckIneligible {
			t.Fatalf("manual authority status = %q, want ineligible", got.Status)
		}
	})
}

func TestMatchingLayoutRecheckExactSumIsNotRounded(t *testing.T) {
	const releaseKey = "musicbrainz"
	root := uuid.New()
	nearOne := math.Nextafter(1, 0)
	input := MatchingGroupEvaluationInput{ReleaseLayouts: []persistence.MatchingRelease{{ProviderKey: releaseKey, Complete: true}}}
	var localEdges []MatchingInput
	for pos, confidence := range []float64{1, nearOne, 0.75} {
		file := uuid.New()
		input.Members = append(input.Members, persistence.IncomingMatchMemberFence{FileMemberID: file})
		input.Files = append(input.Files, IncomingFile{
			VariantID: file,
			Locations: []IncomingLocation{{RootID: root, RelativePath: fmt.Sprintf("album/%d.flac", pos)}},
		})
		release := &input.ReleaseLayouts[0]
		release.Tracks = append(release.Tracks, persistence.MatchingReleaseTrack{Position: pos})
		edge := experimentLayoutEdge(file, releaseKey, 0, pos, 1, confidence)
		edge.AssignmentScore.Factors = []MatchingFactor{{Component: MatchingRecordingTitle, State: MatchingFactorAvailable, Value: 1}}
		input.Inputs = append(input.Inputs, edge)
		if pos == 0 {
			edge.AssignmentScore.Confidence = nearOne
		}
		localEdges = append(localEdges, edge)
	}
	localScore, err := matchingLayoutRecheckScore(localEdges)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := SelectMatchingLayout(t.Context(), input, 0.70)
	if err != nil || candidate.Status != MatchingLayoutSelectionSelected || candidate.exactScore == nil {
		t.Fatalf("qualification failed: %+v, %v", candidate, err)
	}
	if !matchingLayoutRecheckStrictlyBetter(candidate.exactScore, localScore) {
		t.Fatalf("exact sum did not prefer the strictly higher layout: %s / %s", localScore, candidate.exactScore)
	}
	localRounded, _ := localScore.Float64()
	if localRounded != candidate.Score {
		t.Fatalf("fixture must round equal: %v / %v", localRounded, candidate.Score)
	}
	localEdges[0].AssignmentScore.Confidence = 1
	equalScore, err := matchingLayoutRecheckScore(localEdges)
	if err != nil || candidate.exactScore.Cmp(equalScore) != 0 {
		t.Fatalf("rounded solver total differs from the exact equal baseline: %s / %v (%v)", candidate.exactScore, equalScore, err)
	}
}

func matchingLayoutRecheckCapture(
	fileID, root uuid.UUID, title string, position int,
) persistence.IncomingGroupingCapture {
	analysisID := uuid.New()
	return persistence.IncomingGroupingCapture{
		VariantID: fileID, WorkID: uuid.New(), LocationID: uuid.New(), RootID: root,
		CaptureAnalysisID: &analysisID, Ready: true,
		RelativePath: fmt.Sprintf("%s.flac", fileID), ConfiguredPath: "/music", InventoryPath: "/music",
		Mtime: time.Unix(1, 0),
		Tags: json.RawMessage(fmt.Sprintf(
			`{"ALBUM":["Album"],"ALBUMARTIST":["Artist"],"ARTIST":["Artist"],"TITLE":[%q],"TRACKNUMBER":["%d"]}`,
			title, position,
		)),
		ProbeResult: json.RawMessage(`{"format":{"duration":"200"},"streams":[{"codec_type":"audio","duration":"200"}]}`),
	}
}

func matchingLayoutRecheckRelease(
	key string, firstTrack, secondTrack uuid.UUID, firstTitle, secondTitle string,
) persistence.MatchingRelease {
	return persistence.MatchingRelease{
		ID: uuid.New(), ProviderKey: key, Name: "Album", Complete: true,
		Credits: []persistence.MatchingReleaseCredit{{Position: 0, Name: "Artist"}},
		Tracks: []persistence.MatchingReleaseTrack{
			{
				ID: firstTrack, Medium: 0, Position: 0, DisplayedNumber: "1",
				RecordingProviderKey: key + "-recording-1", RecordingTitle: firstTitle,
				RecordingArtists: []string{"Artist"}, RecordingLengthMS: matchingLayoutRecheckDuration(200000),
				ReleaseLengthMS: matchingLayoutRecheckDuration(200000),
			},
			{
				ID: secondTrack, Medium: 0, Position: 1, DisplayedNumber: "2",
				RecordingProviderKey: key + "-recording-2", RecordingTitle: secondTitle,
				RecordingArtists: []string{"Artist"}, RecordingLengthMS: matchingLayoutRecheckDuration(200000),
				ReleaseLengthMS: matchingLayoutRecheckDuration(200000),
			},
		},
	}
}

func matchingLayoutRecheckDuration(value int64) *int64 { return &value }

func matchingLayoutRecheckCoordinates(
	release persistence.MatchingRelease, assignments []MatchingLayoutAssignment,
) map[string][2]int {
	tracks := make(map[uuid.UUID]persistence.MatchingReleaseTrack, len(release.Tracks))
	for _, track := range release.Tracks {
		tracks[track.ID] = track
	}
	coordinates := make(map[string][2]int, len(assignments))
	for _, assignment := range assignments {
		track := tracks[assignment.ProviderTrackID]
		coordinates[assignment.FileMemberID.String()] = [2]int{track.Medium, track.Position}
	}
	return coordinates
}
