package service

import (
	"fmt"
	"math"
	"strconv"
)

// MatchingPolicyVersion identifies the scoring behavior used to produce a
// confidence. It is deliberately independent of persistence and provider IO.
const MatchingPolicyVersion = "matching-go-v1"

type MatchingProfile string

const (
	ReleaseMatchingProfile   MatchingProfile = "release"
	RecordingMatchingProfile MatchingProfile = "recording"
	// RecordingReleaseMatchingProfile is the approved M07 per-assignment legacy
	// combined form; release selection must use ReleaseMatchingProfile instead.
	RecordingReleaseMatchingProfile MatchingProfile = "recording_release"
)

type MatchingComponent string

const (
	MatchingReleaseArtist        MatchingComponent = "release_artist"
	MatchingRecordingArtist      MatchingComponent = "recording_artist"
	MatchingReleaseTitle         MatchingComponent = "release_title"
	MatchingRecordingTitle       MatchingComponent = "recording_title"
	MatchingReleaseDuration      MatchingComponent = "release_duration"
	MatchingRecordingDuration    MatchingComponent = "recording_duration"
	MatchingReleaseMusicBrainz   MatchingComponent = "release_musicbrainz_search"
	MatchingRecordingMusicBrainz MatchingComponent = "recording_musicbrainz_search"
	MatchingAcoustID             MatchingComponent = "acoustid"
	MatchingTrackNumber          MatchingComponent = "track_number"
	MatchingDiscNumber           MatchingComponent = "disc_number"
	MatchingTrackTotal           MatchingComponent = "track_total"
	MatchingDiscTotal            MatchingComponent = "disc_total"
)

type MatchingFactorState string

const (
	MatchingFactorAvailable     MatchingFactorState = "available"
	MatchingFactorMissing       MatchingFactorState = "missing"
	MatchingFactorNotApplicable MatchingFactorState = "not_applicable"
)

// MatchingFactor retains the evidence used by a score. Value is a normalized
// match value except for duration, whose legacy formula can be negative.
// Contribution is populated by ScoreMatchingFactors as Value*Weight.
type MatchingFactor struct {
	Component                         MatchingComponent
	State                             MatchingFactorState
	Provenance                        string
	SourceRaw                         []string
	CandidateRaw                      []string
	SourceNormalized                  string
	CandidateNormalized               string
	TransliteratedSourceNormalized    string
	TransliteratedCandidateNormalized string
	Value                             float64
	Weight                            int
	Contribution                      float64
}

// MatchingTextPair is adapter-neutral text evidence. Values retain their
// observed order; duplicate values are removed stably before comparison.
type MatchingTextPair struct {
	Source     []string
	Candidate  []string
	Provenance string
}

// MatchingScoreInput contains the evidence needed for any explicit scoring
// profile. Provider-specific result translation and I/O stay outside service.
type MatchingScoreInput struct {
	Profile MatchingProfile

	ReleaseArtist   MatchingTextPair
	ReleaseTitle    MatchingTextPair
	RecordingArtist MatchingTextPair
	RecordingTitle  MatchingTextPair

	ReleaseExpectedDurationSeconds   *float64
	ReleaseActualDurationSeconds     *float64
	RecordingExpectedDurationSeconds *float64
	RecordingActualDurationSeconds   *float64

	ReleaseMusicBrainzScore   *float64 // provider's 0..100 search score
	RecordingMusicBrainzScore *float64 // provider's 0..100 search score
	AcoustIDScore             *float64 // provider's normalized 0..1 score

	TrackNumber         *int
	CandidateTrack      *int
	TrackTotal          *int
	CandidateTrackTotal *int
	DiscNumber          *int
	CandidateDisc       *int
	DiscTotal           *int
	CandidateDiscTotal  *int

	Evidence MatchingEvidence
}

// MatchingEvidence carries IDs for lookup/audit only. IDs never contribute a
// numerical factor or force confidence to one.
type MatchingEvidence struct {
	ReleaseMBID   string
	RecordingMBID string
	TrackMBID     string
}

type MatchingScore struct {
	Profile       MatchingProfile
	PolicyVersion string
	Confidence    float64
	TotalWeight   int
	Factors       []MatchingFactor
	Evidence      MatchingEvidence
}

// ScoreMatchingFactors calculates an available-only weighted average and
// applies the approved lower clamp. Invalid available factor values are errors;
// in particular, malformed provider values are never silently omitted.
func ScoreMatchingFactors(profile MatchingProfile, factors []MatchingFactor, evidence MatchingEvidence) (MatchingScore, error) {
	weights, err := matchingProfileWeights(profile)
	if err != nil {
		return MatchingScore{}, err
	}
	result := MatchingScore{
		Profile: profile, PolicyVersion: MatchingPolicyVersion,
		Factors: append([]MatchingFactor(nil), factors...), Evidence: evidence,
	}
	for index := range result.Factors {
		result.Factors[index].SourceRaw = append([]string(nil), result.Factors[index].SourceRaw...)
		result.Factors[index].CandidateRaw = append([]string(nil), result.Factors[index].CandidateRaw...)
	}
	seen := make(map[MatchingComponent]struct{}, len(factors))
	weightedTotal := 0.0
	for index := range result.Factors {
		factor := &result.Factors[index]
		weight, ok := weights[factor.Component]
		if !ok {
			return MatchingScore{}, fmt.Errorf("factor %q is not part of %q profile", factor.Component, profile)
		}
		if _, ok := seen[factor.Component]; ok {
			return MatchingScore{}, fmt.Errorf("duplicate matching factor %q", factor.Component)
		}
		seen[factor.Component] = struct{}{}
		if factor.Weight != 0 && factor.Weight != weight {
			return MatchingScore{}, fmt.Errorf("factor %q has weight %d, want %d", factor.Component, factor.Weight, weight)
		}
		factor.Weight = weight
		switch factor.State {
		case MatchingFactorMissing, MatchingFactorNotApplicable:
			factor.Contribution = 0
		case MatchingFactorAvailable:
			if math.IsNaN(factor.Value) || math.IsInf(factor.Value, 0) {
				return MatchingScore{}, fmt.Errorf("factor %q has a non-finite value", factor.Component)
			}
			if factor.Value > 1 || (!isDurationComponent(factor.Component) && factor.Value < 0) {
				return MatchingScore{}, fmt.Errorf("factor %q value is outside its allowed range", factor.Component)
			}
			factor.Contribution = factor.Value * float64(weight)
			weightedTotal += factor.Contribution
			result.TotalWeight += weight
		default:
			return MatchingScore{}, fmt.Errorf("factor %q has invalid state %q", factor.Component, factor.State)
		}
	}
	if result.TotalWeight > 0 {
		result.Confidence = math.Max(0, weightedTotal/float64(result.TotalWeight))
	}
	if math.IsNaN(result.Confidence) || math.IsInf(result.Confidence, 0) || result.Confidence < 0 || result.Confidence > 1 {
		return MatchingScore{}, fmt.Errorf("computed confidence is outside [0, 1]")
	}
	return result, nil
}

// ScoreMatching computes the requested profile from text and numeric evidence.
// Callers choose release, recording, or the explicitly named legacy combined
// profile. Release selection remains a separate profile calculation.
func ScoreMatching(input MatchingScoreInput) (MatchingScore, error) {
	if _, err := matchingProfileWeights(input.Profile); err != nil {
		return MatchingScore{}, err
	}
	factors := make([]MatchingFactor, 0, 13)
	appendText := func(component MatchingComponent, pair MatchingTextPair) error {
		factor, err := matchingTextFactor(component, pair)
		if err != nil {
			return err
		}
		factors = append(factors, factor)
		return nil
	}
	appendDuration := func(component MatchingComponent, expected, actual *float64) {
		factors = append(factors, matchingDurationFactor(component, expected, actual))
	}
	appendProvider := func(component MatchingComponent, value *float64, scale float64, provenance string) error {
		factor, err := matchingProviderFactor(component, value, scale, provenance)
		if err != nil {
			return err
		}
		factors = append(factors, factor)
		return nil
	}
	appendPosition := func(component MatchingComponent, source, candidate *int, provenance string) {
		factors = append(factors, matchingPositionFactor(component, source, candidate, provenance))
	}

	if input.Profile != RecordingMatchingProfile {
		if err := appendText(MatchingReleaseArtist, input.ReleaseArtist); err != nil {
			return MatchingScore{}, err
		}
		if err := appendText(MatchingReleaseTitle, input.ReleaseTitle); err != nil {
			return MatchingScore{}, err
		}
		appendDuration(MatchingReleaseDuration, input.ReleaseExpectedDurationSeconds, input.ReleaseActualDurationSeconds)
		if err := appendProvider(MatchingReleaseMusicBrainz, input.ReleaseMusicBrainzScore, 100, "MusicBrainz release search"); err != nil {
			return MatchingScore{}, err
		}
		appendPosition(MatchingTrackNumber, input.TrackNumber, input.CandidateTrack, "track number")
		appendPosition(MatchingDiscNumber, input.DiscNumber, input.CandidateDisc, "disc number")
		appendPosition(MatchingTrackTotal, input.TrackTotal, input.CandidateTrackTotal, "track total")
		appendPosition(MatchingDiscTotal, input.DiscTotal, input.CandidateDiscTotal, "disc total")
	}
	if input.Profile != ReleaseMatchingProfile {
		if err := appendText(MatchingRecordingArtist, input.RecordingArtist); err != nil {
			return MatchingScore{}, err
		}
		if err := appendText(MatchingRecordingTitle, input.RecordingTitle); err != nil {
			return MatchingScore{}, err
		}
		appendDuration(MatchingRecordingDuration, input.RecordingExpectedDurationSeconds, input.RecordingActualDurationSeconds)
		if err := appendProvider(MatchingRecordingMusicBrainz, input.RecordingMusicBrainzScore, 100, "MusicBrainz recording search"); err != nil {
			return MatchingScore{}, err
		}
		if err := appendProvider(MatchingAcoustID, input.AcoustIDScore, 1, "AcoustID fingerprint"); err != nil {
			return MatchingScore{}, err
		}
	}
	return ScoreMatchingFactors(input.Profile, factors, input.Evidence)
}

// ScoreAssignmentCandidate computes the M07-approved recording+release legacy
// score, including both duration and MusicBrainz search factors. It does not
// select a release or position and has no persistence side effects.
func ScoreAssignmentCandidate(input MatchingScoreInput) (MatchingScore, error) {
	input.Profile = RecordingReleaseMatchingProfile
	return ScoreMatching(input)
}

// DurationSimilarity implements the approved legacy duration function. Inputs
// are explicit adapter-neutral seconds; absent, non-positive, NaN, or infinite
// durations are unavailable rather than synthesized as matches.
func DurationSimilarity(expectedSeconds, actualSeconds *float64) (float64, bool) {
	if expectedSeconds == nil || actualSeconds == nil ||
		!finitePositive(*expectedSeconds) || !finitePositive(*actualSeconds) {
		return 0, false
	}
	difference := math.Abs(*expectedSeconds - *actualSeconds)
	localSimilarity := math.Pow(math.Max(0, 1-difference/20), 2.2)
	logRatio := math.Log2(math.Max(*expectedSeconds, *actualSeconds)) - math.Log2(math.Min(*expectedSeconds, *actualSeconds))
	return localSimilarity - logRatio, true
}

// LegacyDurationSeconds mirrors Python round(value) (ties to even) at the
// provider/source adapter boundary, without imposing an integer-unit schema.
func LegacyDurationSeconds(value *float64) (float64, bool) {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0, false
	}
	return math.RoundToEven(*value), true
}

func matchingDurationFactor(component MatchingComponent, expected, actual *float64) MatchingFactor {
	left, leftOK := LegacyDurationSeconds(expected)
	right, rightOK := LegacyDurationSeconds(actual)
	factor := MatchingFactor{Component: component, Provenance: "duration in seconds"}
	if expected != nil {
		factor.SourceRaw = []string{formatMatchingNumber(*expected)}
	}
	if actual != nil {
		factor.CandidateRaw = []string{formatMatchingNumber(*actual)}
	}
	if !leftOK || !rightOK || left <= 0 || right <= 0 {
		factor.State = MatchingFactorMissing
		return factor
	}
	factor.State = MatchingFactorAvailable
	factor.SourceNormalized = formatMatchingNumber(left)
	factor.CandidateNormalized = formatMatchingNumber(right)
	value, available := DurationSimilarity(&left, &right)
	if !available {
		factor.State = MatchingFactorNotApplicable
		return factor
	}
	factor.Value = value
	return factor
}

func matchingProviderFactor(component MatchingComponent, value *float64, scale float64, provenance string) (MatchingFactor, error) {
	factor := MatchingFactor{Component: component, Provenance: provenance}
	if value == nil {
		factor.State = MatchingFactorMissing
		return factor, nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > scale {
		return MatchingFactor{}, fmt.Errorf("provider factor %q is outside [0, %v]", component, scale)
	}
	factor.State = MatchingFactorAvailable
	factor.SourceRaw = []string{formatMatchingNumber(*value)}
	factor.SourceNormalized = formatMatchingNumber(*value / scale)
	factor.Value = *value / scale
	return factor, nil
}

func matchingPositionFactor(component MatchingComponent, source, candidate *int, provenance string) MatchingFactor {
	factor := MatchingFactor{Component: component, Provenance: provenance}
	if source != nil {
		factor.SourceRaw = []string{fmt.Sprint(*source)}
	}
	if candidate != nil {
		factor.CandidateRaw = []string{fmt.Sprint(*candidate)}
	}
	if source == nil || candidate == nil {
		factor.State = MatchingFactorMissing
		return factor
	}
	if *source <= 0 || *candidate <= 0 {
		factor.State = MatchingFactorNotApplicable
		return factor
	}
	factor.State = MatchingFactorAvailable
	factor.SourceNormalized = fmt.Sprint(*source)
	factor.CandidateNormalized = fmt.Sprint(*candidate)
	if *source == *candidate {
		factor.Value = 1
	}
	return factor
}

func formatMatchingNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func matchingProfileWeights(profile MatchingProfile) (map[MatchingComponent]int, error) {
	artist := map[MatchingComponent]int{}
	switch profile {
	case ReleaseMatchingProfile:
		artist[MatchingReleaseArtist] = 4
		artist[MatchingReleaseTitle] = 4
		artist[MatchingReleaseDuration] = 2
		artist[MatchingReleaseMusicBrainz] = 2
		artist[MatchingTrackNumber] = 2
		artist[MatchingDiscNumber] = 1
		artist[MatchingTrackTotal] = 1
		artist[MatchingDiscTotal] = 1
	case RecordingMatchingProfile:
		artist[MatchingRecordingArtist] = 4
		artist[MatchingRecordingTitle] = 4
		artist[MatchingRecordingDuration] = 2
		artist[MatchingRecordingMusicBrainz] = 2
		artist[MatchingAcoustID] = 4
	case RecordingReleaseMatchingProfile:
		artist[MatchingReleaseArtist] = 4
		artist[MatchingRecordingArtist] = 4
		artist[MatchingReleaseTitle] = 4
		artist[MatchingRecordingTitle] = 4
		artist[MatchingReleaseDuration] = 2
		artist[MatchingRecordingDuration] = 2
		artist[MatchingReleaseMusicBrainz] = 2
		artist[MatchingRecordingMusicBrainz] = 2
		artist[MatchingAcoustID] = 4
		artist[MatchingTrackNumber] = 2
		artist[MatchingDiscNumber] = 1
		artist[MatchingTrackTotal] = 1
		artist[MatchingDiscTotal] = 1
	default:
		return nil, fmt.Errorf("unknown matching profile %q", profile)
	}
	return artist, nil
}

func isDurationComponent(component MatchingComponent) bool {
	return component == MatchingReleaseDuration || component == MatchingRecordingDuration
}

// stableUniqueMatchingValues removes exact duplicate values while retaining
// source order. It does not normalize or rewrite the caller's observed values.
func stableUniqueMatchingValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}
