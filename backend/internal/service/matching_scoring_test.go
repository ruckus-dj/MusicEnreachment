package service

import (
	"math"
	"testing"
)

func TestScoreMatchingFactorsAvailableOnlyAndMissingDuration(t *testing.T) {
	result, err := ScoreMatchingFactors(ReleaseMatchingProfile, []MatchingFactor{
		{Component: MatchingReleaseArtist, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingReleaseTitle, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingReleaseDuration, State: MatchingFactorMissing},
	}, MatchingEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Confidence != 1 || result.TotalWeight != 8 {
		t.Fatalf("score = (%v, weight %d), want (1, weight 8)", result.Confidence, result.TotalWeight)
	}
	if result.Factors[0].Contribution != 4 || result.Factors[1].Contribution != 4 ||
		result.Factors[2].Weight != 2 || result.Factors[2].Contribution != 0 {
		t.Fatalf("factor evidence = %+v, want available 4+4 and missing duration weight 2", result.Factors)
	}
}

func TestReleaseScorePositionMismatchAndRecordingPositionExclusion(t *testing.T) {
	release, err := ScoreMatchingFactors(ReleaseMatchingProfile, []MatchingFactor{
		{Component: MatchingReleaseArtist, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingReleaseTitle, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingReleaseDuration, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingTrackNumber, State: MatchingFactorAvailable, Value: 0},
		{Component: MatchingTrackTotal, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingDiscNumber, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingDiscTotal, State: MatchingFactorAvailable, Value: 1},
	}, MatchingEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	if want := 13.0 / 15; math.Abs(release.Confidence-want) > 1e-12 {
		t.Fatalf("release confidence = %.15f, want %.15f", release.Confidence, want)
	}

	if _, err := ScoreMatchingFactors(RecordingMatchingProfile, []MatchingFactor{
		{Component: MatchingTrackNumber, State: MatchingFactorAvailable, Value: 0},
	}, MatchingEvidence{}); err == nil {
		t.Fatal("recording profile accepted a position factor")
	}
}

func TestRecordingScoreIncludesAcoustIDEvidenceAndNoizeValue(t *testing.T) {
	result, err := ScoreMatchingFactors(RecordingMatchingProfile, []MatchingFactor{
		{Component: MatchingRecordingArtist, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingRecordingTitle, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingRecordingDuration, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingRecordingMusicBrainz, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingAcoustID, State: MatchingFactorAvailable, Value: 0.9997},
	}, MatchingEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	if want := (4.0 + 4 + 2 + 2 + 4*0.9997) / 16; math.Abs(result.Confidence-want) > 1e-12 {
		t.Fatalf("confidence = %.15f, want %.15f", result.Confidence, want)
	}

	// Both independently supplied provider candidates retain their shared score
	// and IDs; this exercises scoring, not AcoustID parsing or projection.
	for _, mbid := range []string{"noize-vol1-recording", "noize-vol2-recording"} {
		noize, err := ScoreMatchingFactors(RecordingMatchingProfile, []MatchingFactor{
			{Component: MatchingAcoustID, State: MatchingFactorAvailable, Value: 0.96927744},
		}, MatchingEvidence{RecordingMBID: mbid})
		if err != nil {
			t.Fatal(err)
		}
		if noize.Confidence != 0.96927744 || noize.TotalWeight != 4 || noize.Evidence.RecordingMBID != mbid {
			t.Fatalf("Noize candidate %q score = (%v, weight %d), want (0.96927744, weight 4)", mbid, noize.Confidence, noize.TotalWeight)
		}
	}
}

func TestMatchingIDsAreEvidenceOnlyAndDurationMayGoNegative(t *testing.T) {
	result, err := ScoreMatchingFactors(RecordingMatchingProfile, nil, MatchingEvidence{
		RecordingMBID: "same-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Confidence != 0 || result.TotalWeight != 0 || result.Evidence.RecordingMBID != "same-id" {
		t.Fatalf("ID changed numeric score: %+v", result)
	}

	expected, actual := 10.0, 20.0
	value, available := DurationSimilarity(&expected, &actual)
	if !available || value >= 0 {
		t.Fatalf("duration similarity = (%v, %v), want available negative value", value, available)
	}
	duration, err := ScoreMatchingFactors(RecordingMatchingProfile, []MatchingFactor{{
		Component: MatchingRecordingDuration, State: MatchingFactorAvailable, Value: value,
	}}, MatchingEvidence{RecordingMBID: "same-id"})
	if err != nil {
		t.Fatal(err)
	}
	if duration.Confidence != 0 {
		t.Fatalf("negative duration was not lower-clamped: %v", duration.Confidence)
	}
}

func TestScoreMatchingFactorsRejectsInvalidProviderValues(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), 1.01, -0.01} {
		_, err := ScoreMatchingFactors(RecordingMatchingProfile, []MatchingFactor{{
			Component: MatchingAcoustID, State: MatchingFactorAvailable, Value: value,
		}}, MatchingEvidence{})
		if err == nil {
			t.Errorf("accepted invalid provider value %v", value)
		}
	}
}

func TestCombinedLegacyProfileHasBothEvidenceSets(t *testing.T) {
	result, err := ScoreMatchingFactors(RecordingReleaseMatchingProfile, []MatchingFactor{
		{Component: MatchingReleaseArtist, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingRecordingArtist, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingReleaseTitle, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingRecordingTitle, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingReleaseDuration, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingRecordingDuration, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingReleaseMusicBrainz, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingRecordingMusicBrainz, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingAcoustID, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingTrackNumber, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingDiscNumber, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingTrackTotal, State: MatchingFactorAvailable, Value: 1},
		{Component: MatchingDiscTotal, State: MatchingFactorAvailable, Value: 1},
	}, MatchingEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Confidence != 1 || result.TotalWeight != 33 {
		t.Fatalf("combined profile = (%v, weight %d), want (1, weight 33)", result.Confidence, result.TotalWeight)
	}
}

func TestStableUniqueMatchingValuesKeepsFirstOccurrence(t *testing.T) {
	values := []string{"artist-b", "artist-a", "artist-b", "artist-c", "artist-a"}
	got := stableUniqueMatchingValues(values)
	want := []string{"artist-b", "artist-a", "artist-c"}
	if len(got) != len(want) {
		t.Fatalf("values = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("values = %#v, want %#v", got, want)
		}
	}
}

func TestMatchingRejectsDurationAboveOneEvenWhenAverageIsValid(t *testing.T) {
	_, err := ScoreMatchingFactors(RecordingMatchingProfile, []MatchingFactor{
		{Component: MatchingRecordingDuration, State: MatchingFactorAvailable, Value: 2},
		{Component: MatchingRecordingTitle, State: MatchingFactorAvailable, Value: 0},
	}, MatchingEvidence{})
	if err == nil {
		t.Fatal("duration above one must not be hidden by a valid aggregate")
	}
}

func TestGoTextCanonicalizationGoldenCases(t *testing.T) {
	if got, want := canonicalMatchingText("A_B—İ Straße"), "a b i strasse"; got != want {
		t.Fatalf("canonical text = %q, want %q", got, want)
	}
	if got, want := canonicalMatchingText("Cafe\u0301"), "café"; got != want {
		t.Fatalf("decomposed NFC form = %q, want %q", got, want)
	}
	if got, want := canonicalMatchingText("Café"), "café"; got != want {
		t.Fatalf("composed NFC form = %q, want %q", got, want)
	}
	for _, test := range []struct {
		left, right string
		want        float64
	}{
		{"Straße", "STRASSE", 1},
		{"Москва", "Moskva", 1},
		{"!!!", "???", 0},
		{"a b", "a", 0.2555555555555556},
	} {
		got, err := matchingTextSimilarity(test.left, test.right)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-test.want) > 1e-12 {
			t.Errorf("Go text similarity(%q, %q) = %.15f, want %.15f", test.left, test.right, got, test.want)
		}
	}
}

func TestMatchingTextAvailabilityAndUTF8Validation(t *testing.T) {
	blank, err := matchingTextFactor(MatchingRecordingTitle, MatchingTextPair{
		Source: []string{"\t\n\v\f\r"}, Candidate: []string{"Track"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if blank.State != MatchingFactorMissing {
		t.Fatalf("whitespace-only source state = %q, want missing", blank.State)
	}
	punctuation, err := matchingTextFactor(MatchingRecordingTitle, MatchingTextPair{
		Source: []string{"!!!"}, Candidate: []string{"???"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if punctuation.State != MatchingFactorAvailable || punctuation.Value != 0 {
		t.Fatalf("punctuation factor = (%q, %v), want available zero", punctuation.State, punctuation.Value)
	}
	mixed, err := matchingTextFactor(MatchingRecordingTitle, MatchingTextPair{
		Source: []string{" \t ", "Title", "Title"}, Candidate: []string{"Title"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mixed.State != MatchingFactorAvailable || mixed.Value != 1 || len(mixed.SourceRaw) != 3 ||
		mixed.SourceRaw[1] != "Title" || mixed.SourceRaw[2] != "Title" {
		t.Fatalf("mixed blank/nonblank values = %+v, want exact match preserving raw values", mixed)
	}
	invalid, err := matchingTextFactor(MatchingRecordingTitle, MatchingTextPair{
		Source: []string{string([]byte{0xff})}, Candidate: []string{"Track"},
	})
	if err == nil || invalid.State != "" {
		t.Fatalf("invalid UTF-8 factor = (%+v, %v), want an error", invalid, err)
	}
}

func TestHighLevelCombinedScoreUsesBothDurationAndSearchFactors(t *testing.T) {
	duration := 240.0
	search := 100.0
	acoustID := 1.0
	position := 1
	input := MatchingScoreInput{
		Profile:                        RecordingReleaseMatchingProfile,
		ReleaseArtist:                  MatchingTextPair{Source: []string{"Artist"}, Candidate: []string{"Artist"}},
		ReleaseTitle:                   MatchingTextPair{Source: []string{"Album"}, Candidate: []string{"Album"}},
		RecordingArtist:                MatchingTextPair{Source: []string{"Artist"}, Candidate: []string{"Artist"}},
		RecordingTitle:                 MatchingTextPair{Source: []string{"Track"}, Candidate: []string{"Track"}},
		ReleaseExpectedDurationSeconds: &duration, ReleaseActualDurationSeconds: &duration,
		RecordingExpectedDurationSeconds: &duration, RecordingActualDurationSeconds: &duration,
		ReleaseMusicBrainzScore: &search, RecordingMusicBrainzScore: &search,
		AcoustIDScore: &acoustID,
		TrackNumber:   &position, CandidateTrack: &position,
		TrackTotal: &position, CandidateTrackTotal: &position,
		DiscNumber: &position, CandidateDisc: &position,
		DiscTotal: &position, CandidateDiscTotal: &position,
	}
	result, err := ScoreMatching(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Confidence != 1 || result.TotalWeight != 33 {
		t.Fatalf("combined high-level score = (%v, weight %d), want (1, weight 33)", result.Confidence, result.TotalWeight)
	}

	input.AcoustIDScore = nil
	withoutAcoustID, err := ScoreMatching(input)
	if err != nil {
		t.Fatal(err)
	}
	if withoutAcoustID.Confidence != 1 || withoutAcoustID.TotalWeight != 29 {
		t.Fatalf("combined score without AcoustID = (%v, weight %d), want (1, weight 29)", withoutAcoustID.Confidence, withoutAcoustID.TotalWeight)
	}
}

func TestReleaseScoringGoldenFixtures(t *testing.T) {
	search := 100.0
	duration := 164.0
	kisKis, err := ScoreMatching(MatchingScoreInput{
		Profile:       ReleaseMatchingProfile,
		ReleaseArtist: MatchingTextPair{Source: []string{"кис-кис"}, Candidate: []string{"кис-кис"}},
		ReleaseTitle: MatchingTextPair{
			// This ASCII golden isolates the Go Levenshtein/coverage policy;
			// its expectation does not assert RapidFuzz parity.
			Source: []string{"Harakiri"}, Candidate: []string{"Harakiri tribute Egoru Letovu"},
		},
		ReleaseExpectedDurationSeconds: &duration, ReleaseActualDurationSeconds: &duration,
		ReleaseMusicBrainzScore: &search,
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(kisKis.Confidence-0.7315101070154578) > 1e-12 || kisKis.TotalWeight != 12 {
		t.Fatalf("Go-policy Kis-Kis release fixture = (%0.16f, weight %d), want (0.7315101070154578, weight 12)", kisKis.Confidence, kisKis.TotalWeight)
	}

	noizeDuration := 160.0
	track, disc := 13, 1
	trackTotal, discTotal := 30, 1
	noize, err := ScoreMatching(MatchingScoreInput{
		Profile:       ReleaseMatchingProfile,
		ReleaseArtist: MatchingTextPair{Source: []string{"Noize MC"}, Candidate: []string{"Noize MC"}},
		ReleaseTitle: MatchingTextPair{
			Source: []string{"Хипхопера: Орфей & Эвридика"}, Candidate: []string{"Хипхопера: Орфей & Эвридика"},
		},
		ReleaseExpectedDurationSeconds: &noizeDuration, ReleaseActualDurationSeconds: &noizeDuration,
		RecordingArtist: MatchingTextPair{
			Source: []string{"Олег Груз"}, Candidate: []string{"Олег Груз", "Noize MC", "Анастасия Александрина"},
		},
		RecordingTitle: MatchingTextPair{
			Source:    []string{"Подписание контракта (Аид, Орфей, Фортуна) (feat. Noize MC & Анастасия Александрина)"},
			Candidate: []string{"Подписание контракта (Аид, Орфей, Фортуна)"},
		},
		TrackNumber: &track, CandidateTrack: &track, TrackTotal: &trackTotal, CandidateTrackTotal: &trackTotal,
		DiscNumber: &disc, CandidateDisc: &disc, DiscTotal: &discTotal, CandidateDiscTotal: &discTotal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if noize.Confidence != 1 || noize.TotalWeight != 15 {
		t.Fatalf("Noize release fixture = (%v, weight %d), want (1, weight 15)", noize.Confidence, noize.TotalWeight)
	}
	contributions := make(map[MatchingComponent]float64, len(noize.Factors))
	for _, factor := range noize.Factors {
		contributions[factor.Component] = factor.Contribution
	}
	if contributions[MatchingReleaseArtist] != 4 || contributions[MatchingReleaseTitle] != 4 ||
		contributions[MatchingReleaseDuration] != 2 {
		t.Fatalf("Noize release identity contributions = %v, want 4, 4, 2", contributions)
	}
	positionContribution := contributions[MatchingTrackNumber] + contributions[MatchingDiscNumber] +
		contributions[MatchingTrackTotal] + contributions[MatchingDiscTotal]
	if positionContribution != 5 {
		t.Fatalf("Noize position contribution = %v, want 5", positionContribution)
	}
}

func TestDurationScoringGoldenFixturesAndGrossMismatchClamp(t *testing.T) {
	for _, test := range []struct {
		expected, actual, want float64
	}{
		{276, 276, 1.0},
		{278, 276, 0.783},
		{281, 276, 0.505},
		{291, 276, -0.029},
		{552, 276, -1.0},
		{1104, 276, -2.0},
		{3496, 276, -3.663},
	} {
		got, available := DurationSimilarity(&test.expected, &test.actual)
		if !available || math.Abs(got-test.want) > 0.001 {
			t.Errorf("DurationSimilarity(%v, %v) = (%v, %v), want (%v, true)", test.expected, test.actual, got, available, test.want)
		}
	}
	artist := MatchingFactor{Component: MatchingRecordingArtist, State: MatchingFactorAvailable, Value: 1}
	duration := MatchingFactor{Component: MatchingRecordingDuration, State: MatchingFactorAvailable, Value: -3.663}
	gross, err := ScoreMatchingFactors(RecordingMatchingProfile, []MatchingFactor{artist, duration}, MatchingEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	if gross.Confidence != 0 {
		t.Fatalf("gross mismatch confidence = %v, want clamped zero", gross.Confidence)
	}
}

func TestLegacyDurationRoundingMatchesPythonTiesToEven(t *testing.T) {
	for _, test := range []struct{ input, want float64 }{{2.5, 2}, {3.5, 4}, {4.49, 4}} {
		got, ok := LegacyDurationSeconds(&test.input)
		if !ok || got != test.want {
			t.Errorf("LegacyDurationSeconds(%v) = (%v, %v), want (%v, true)", test.input, got, ok, test.want)
		}
	}
}
