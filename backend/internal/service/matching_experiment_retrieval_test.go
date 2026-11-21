package service

import (
	"encoding/json"
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
