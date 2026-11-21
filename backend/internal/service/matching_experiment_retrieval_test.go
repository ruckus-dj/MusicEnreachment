package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestMatchingExperimentRanksHeadersWithoutRequiringEqualCounts(t *testing.T) {
	t.Parallel()
	captured := []persistence.MatchingAcquisitionCapturedMember{
		{MemberID: uuid.New(), Tags: json.RawMessage(`{"ALBUM":["Compilation"]}`)},
		{MemberID: uuid.New(), Tags: json.RawMessage(`{}`)},
	}
	headers := []MatchingExperimentReleaseHeader{
		{
			MatchingRetrievalCandidate: MatchingRetrievalCandidate{
				ID: "00000000-0000-4000-8000-000000000001", Title: "Compilation",
			},
			PositionCount: intPointer(3),
		},
		{
			MatchingRetrievalCandidate: MatchingRetrievalCandidate{
				ID: "00000000-0000-4000-8000-000000000002", Title: "Compilation",
			},
			PositionCount: intPointer(2),
		},
	}
	ranked, err := RankMatchingExperimentCatalog(captured, headers, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 2 || ranked[0].ReleaseID != headers[1].ID || ranked[0].BaselineRank != 2 ||
		!ranked[0].Shortlisted || ranked[1].Shortlisted || *ranked[1].CountDelta != 1 ||
		ranked[0].AvailableText.AlbumComparisons != 1 || ranked[0].AvailableText.Files != 1 {
		t.Fatalf("unexpected ranking: %+v", ranked)
	}
	slices.Reverse(headers)
	rankedWide, err := RankMatchingExperimentCatalog(captured, headers, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rankedWide) != 2 || rankedWide[0].ReleaseID != ranked[0].ReleaseID || !rankedWide[1].Shortlisted {
		t.Fatalf("different-count edition should remain eligible: %+v", rankedWide)
	}
}

func TestRankMatchingReleaseShortlistUsesKnownCountBeforeStableID(t *testing.T) {
	t.Parallel()
	captured := []persistence.MatchingAcquisitionCapturedMember{
		{MemberID: uuid.New(), Tags: json.RawMessage(`{"ALBUM":["Album"]}`)},
		{MemberID: uuid.New(), Tags: json.RawMessage(`{"ALBUM":["Album"]}`)},
	}
	id := func(index int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", index) }
	header := func(index int, count *int) MatchingReleaseHeader {
		return MatchingReleaseHeader{
			MatchingRetrievalCandidate: MatchingRetrievalCandidate{ID: id(index), Title: "Album"},
			PositionCount:              count,
		}
	}
	headers := []MatchingReleaseHeader{
		header(1, intPointer(2)),  // exact known agreement
		header(100, nil),          // unknown count stays neutral
		header(10, intPointer(3)), // known distance 1
		header(11, intPointer(3)),
		header(12, intPointer(3)),
		header(0, intPointer(4)), // known distance 2, lowest release ID
		header(2, intPointer(4)),
		header(3, intPointer(4)),
		header(4, intPointer(4)),
		header(5, intPointer(4)),
		header(6, intPointer(4)),
		header(7, intPointer(4)),
		header(8, intPointer(4)),
		header(9, intPointer(4)),
	}
	expected := []string{id(1), id(100), id(10), id(11), id(12), id(0), id(2), id(3), id(4), id(5)}
	ranked, err := RankMatchingReleaseShortlist(captured, headers)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ranked, expected) {
		t.Fatalf("shortlist = %v, want %v", ranked, expected)
	}
	slices.Reverse(headers)
	repeated, err := RankMatchingReleaseShortlist(captured, headers)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repeated, expected) {
		t.Fatalf("shortlist changed with input order: %v, want %v", repeated, expected)
	}
	duplicates := []MatchingReleaseHeader{
		header(1, intPointer(2)),
		header(1, intPointer(2)),
	}
	if _, err := RankMatchingReleaseShortlist(captured, duplicates); err == nil {
		t.Fatal("duplicate canonical release IDs should be rejected")
	}
}
