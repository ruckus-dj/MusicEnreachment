package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestRankMatchingCatalogUsesPerFileFullTextMeanAndIgnoresTrackTitle(t *testing.T) {
	captured := []persistence.MatchingAcquisitionCapturedMember{
		matchingRetrievalMember(`{"ALBUMARTIST":["Alpha","Beta"],"ALBUM":["Known Album"],"TITLE":["Wrong Candidate Track"]}`),
	}
	preferred := MatchingRetrievalCandidate{ID: uuid.NewString(), Title: "Known Album", ArtistCredits: []string{"Alpha Beta"}}
	trackOnly := MatchingRetrievalCandidate{ID: uuid.NewString(), Title: "Wrong Candidate Track", ArtistCredits: []string{"Unrelated Artist"}}
	candidates := []MatchingRetrievalCandidate{preferred, trackOnly}
	for range 9 {
		candidates = append(candidates, MatchingRetrievalCandidate{ID: uuid.NewString(), Title: "Known Album", ArtistCredits: []string{"Alpha Beta"}})
	}
	for range 2 {
		candidates = append(candidates, MatchingRetrievalCandidate{ID: uuid.NewString(), Title: "Unrelated Album", ArtistCredits: []string{"Unrelated Artist"}})
	}

	ranked, err := RankMatchingCatalog(captured, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !containsExactString(ranked, preferred.ID) || containsExactString(ranked, trackOnly.ID) {
		t.Fatalf("shortlist should favor full artist+album evidence and ignore track title: %v", ranked)
	}
}

func TestRankMatchingCatalogExactDateAndCatalogOnlyBreakTextTie(t *testing.T) {
	captured := []persistence.MatchingAcquisitionCapturedMember{
		matchingRetrievalMember(`{"ALBUM":["Same Album"],"DATE":["2020-01-02"],"CATALOGNUMBER":["CAT/01;02"]}`),
	}
	plain := MatchingRetrievalCandidate{ID: "ffffffff-ffff-4fff-8fff-ffffffffffff", Title: "Same Album"}
	preferred := MatchingRetrievalCandidate{ID: uuid.NewString(), Title: "Same Album", Date: "2020-01-02", CatalogNumbers: []string{"CAT/01;02"}}
	candidates := []MatchingRetrievalCandidate{plain, preferred}
	for range 9 {
		candidates = append(candidates, MatchingRetrievalCandidate{ID: uuid.NewString(), Title: "Same Album"})
	}
	// This candidate's embedded album MBID must not make it a text-score winner.
	mbid := uuid.NewString()
	candidates = append(candidates, MatchingRetrievalCandidate{ID: mbid, Title: "Unrelated Album"})
	captured[0].Tags = json.RawMessage(fmt.Sprintf(`{"ALBUM":["Same Album"],"DATE":["2020-01-02"],"CATALOGNUMBER":["CAT/01;02"],"MUSICBRAINZ_ALBUMID":[%q]}`, mbid))

	ranked, err := RankMatchingCatalog(captured, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !containsExactString(ranked, preferred.ID) || containsExactString(ranked, plain.ID) || containsExactString(ranked, mbid) {
		t.Fatalf("exact DATE/catalog should break text ties without MBID evidence: %v", ranked)
	}
}

func TestRankMatchingCatalogUsesOnlyFullDATEAndRejectsDuplicateMembers(t *testing.T) {
	member := matchingRetrievalMember(`{"ALBUM":["Album"],"DATE":["2020"],"YEAR":["2020-01-02"]}`)
	candidates := []MatchingRetrievalCandidate{{ID: "ffffffff-ffff-4fff-8fff-ffffffffffff", Title: "Album", Date: "2020-01-02"}}
	for range 10 {
		candidates = append(candidates, MatchingRetrievalCandidate{ID: uuid.NewString(), Title: "Album"})
	}
	ranked, err := RankMatchingCatalog([]persistence.MatchingAcquisitionCapturedMember{member}, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if containsExactString(ranked, candidates[0].ID) {
		t.Fatalf("YEAR must not substitute for the complete saved DATE: %v", ranked)
	}
	if _, err := RankMatchingCatalog([]persistence.MatchingAcquisitionCapturedMember{member, member}, candidates); err == nil {
		t.Fatal("duplicate captured member IDs should be rejected")
	}
}

func TestRankMatchingCatalogResultIsPermutationStable(t *testing.T) {
	captured := []persistence.MatchingAcquisitionCapturedMember{
		matchingRetrievalMember(`{"ALBUMARTIST":["Artist"],"ALBUM":["Album"]}`),
		matchingRetrievalMember(`{"ALBUM":["Album"]}`),
	}
	candidates := make([]MatchingRetrievalCandidate, 12)
	for index := range candidates {
		candidates[index] = MatchingRetrievalCandidate{ID: uuid.NewString(), Title: "Album", ArtistCredits: []string{"Artist"}}
	}
	first, err := RankMatchingCatalog(captured, candidates)
	if err != nil {
		t.Fatal(err)
	}
	for left, right := 0, len(candidates)-1; left < right; left, right = left+1, right-1 {
		candidates[left], candidates[right] = candidates[right], candidates[left]
	}
	second, err := RankMatchingCatalog(captured, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("shortlist changed with input order: first=%v second=%v", first, second)
	}
}

func TestMatchingArtistFitUsesFullValuesAndExcludesMissing(t *testing.T) {
	captured := []persistence.MatchingAcquisitionCapturedMember{
		matchingRetrievalMember(`{"ALBUMARTIST":["Alpha","Beta"]}`),
		matchingRetrievalMember(`{"TITLE":["No artist"]}`),
	}
	fit, available, err := MatchingArtistFit(captured, "Alpha Beta")
	if err != nil || !available || fit < 0.7 {
		t.Fatalf("full artist fit = %v, available=%v, err=%v", fit, available, err)
	}
	fit, available, err = MatchingArtistFit(captured, "Alpha")
	if err != nil || !available || fit >= 1 {
		t.Fatalf("artist fit must not maximize individual values: %v, available=%v, err=%v", fit, available, err)
	}
	fallback, available, err := MatchingArtistFit([]persistence.MatchingAcquisitionCapturedMember{
		matchingRetrievalMember(`{"ARTIST":["Alpha Beta"]}`),
	}, "Alpha Beta")
	if err != nil || !available || fallback != 1 {
		t.Fatalf("ARTIST fallback fit = %v, available=%v, err=%v", fallback, available, err)
	}
	if _, available, err := MatchingArtistFit([]persistence.MatchingAcquisitionCapturedMember{
		matchingRetrievalMember(`{"TITLE":["No artist"]}`),
	}, "Alpha"); err != nil || available {
		t.Fatalf("missing artist evidence should be unavailable, err=%v", err)
	}
}

func matchingRetrievalMember(tags string) persistence.MatchingAcquisitionCapturedMember {
	return persistence.MatchingAcquisitionCapturedMember{MemberID: uuid.New(), Tags: json.RawMessage(tags)}
}
