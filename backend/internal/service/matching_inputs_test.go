package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestBuildMatchingInputsMapsSavedEvidence(t *testing.T) {
	fileID, analysisID := uuid.New(), uuid.New()
	captures := []persistence.IncomingGroupingCapture{{
		VariantID: fileID, WorkID: uuid.New(), LocationID: uuid.New(), RootID: uuid.New(),
		CaptureAnalysisID: &analysisID, Ready: true, RelativePath: "disc/track.flac",
		ConfiguredPath: "/music", InventoryPath: "/music", Mtime: time.Unix(1, 0),
		Tags:        json.RawMessage(`{"ALBUM":["Album","Album"],"ARTIST":["Artist"],"TRACKNUMBER":["2/12"],"DISCNUMBER":["1"],"MUSICBRAINZ_ALBUMID":["bad","also-bad"],"RELEASECOUNTRY":["ZZ"]}`),
		ProbeResult: json.RawMessage(`{"format":{"duration":"12.5"},"streams":[{"codec_type":"audio","duration":"12.5"}]}`),
	}}
	snapshot := persistence.ProviderMatchingSnapshot{
		Releases: []persistence.MatchingRelease{{ProviderKey: "release", Name: "Album", Credits: []persistence.MatchingReleaseCredit{
			{Position: 0, Name: "Artist"}, {Position: 1, Name: "Artist"},
		}, Tracks: []persistence.MatchingReleaseTrack{{Medium: 0, Position: 0, DisplayedNumber: "A1", RecordingProviderKey: "recording", RecordingTitle: "Song", ReleaseLengthMS: int64Pointer(13500)}}}},
		SearchScores: map[string]map[string]persistence.MatchingSearchScore{
			"file-release-query":   {"release": {Value: 80, Present: true}},
			"file-recording-query": {"recording": {Value: 60, Present: true}},
		},
	}
	inputs, err := BuildMatchingInputs(captures, snapshot, map[uuid.UUID]MatchingSearchKeys{fileID: {Release: "file-release-query", Recording: "file-recording-query"}}, nil, map[string][]string{"recording": {"Recording Artist"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 {
		t.Fatalf("got %d inputs, want one", len(inputs))
	}
	input := inputs[0]
	if input.FileID != fileID || input.CaptureAnalysisID != analysisID || input.ReleaseID != "release" || input.RecordingID != "recording" {
		t.Fatalf("unexpected input identity: %+v", input)
	}
	if got := input.AssignmentInput.ReleaseTitle.Source; len(got) != 2 || got[0] != "Album" || got[1] != "Album" {
		t.Fatalf("raw source multiplicity/order was not preserved: %#v", got)
	}
	if got := input.AssignmentInput.ReleaseArtist.Source; len(got) != 1 || got[0] != "Artist" {
		t.Fatalf("album artist did not fall back to artist: %#v", got)
	}
	if got := input.AssignmentInput.ReleaseArtist.Candidate; len(got) != 2 {
		t.Fatalf("provider credits were not preserved in order/repetition: %#v", got)
	}
	if got := input.AssignmentInput.RecordingArtist.Source; len(got) != 1 || got[0] != "Artist" {
		t.Fatalf("recording artist must use source ARTIST: %#v", got)
	}
	if got := input.AssignmentInput.RecordingArtist.Candidate; len(got) != 1 || got[0] != "Recording Artist" {
		t.Fatalf("recording artist must use recording credits: %#v", got)
	}
	if input.ReleaseScoreInput.Profile != ReleaseMatchingProfile || input.ReleaseScore.Profile != ReleaseMatchingProfile {
		t.Fatalf("release score did not use release profile: %+v / %+v", input.ReleaseScoreInput, input.ReleaseScore)
	}
	if input.AssignmentInput.Profile != RecordingReleaseMatchingProfile || input.AssignmentScore.Profile != RecordingReleaseMatchingProfile {
		t.Fatalf("assignment score did not use combined profile: %+v / %+v", input.AssignmentInput, input.AssignmentScore)
	}
	if input.ReleaseScoreInput.ReleaseMusicBrainzScore == nil || *input.ReleaseScoreInput.ReleaseMusicBrainzScore != 80 || input.AssignmentInput.RecordingMusicBrainzScore == nil || *input.AssignmentInput.RecordingMusicBrainzScore != 60 {
		t.Fatalf("file-scoped provider scores missing: %+v / %+v", input.ReleaseScoreInput, input.AssignmentInput)
	}
	if input.AssignmentInput.Evidence.ReleaseMBID != "" {
		t.Fatalf("invalid/multiple source MBIDs were selected: %+v", input.AssignmentInput.Evidence)
	}
	if input.AssignmentInput.TrackNumber != nil || input.AssignmentInput.CandidateTrack == nil || *input.AssignmentInput.CandidateTrack != 1 {
		t.Fatalf("track position projection incorrect: %+v", input.AssignmentInput)
	}
	if len(input.Diagnostics) != 1 || input.Diagnostics[0].Code != "invalid_release_mbid" {
		t.Fatalf("invalid MBIDs should remain diagnostic-only: %#v", input.Diagnostics)
	}
	if input.ReleaseScoreInput.ReleaseExpectedDurationSeconds == nil || *input.ReleaseScoreInput.ReleaseExpectedDurationSeconds != 12.5 || input.AssignmentInput.RecordingExpectedDurationSeconds == nil || *input.AssignmentInput.RecordingExpectedDurationSeconds != 12.5 {
		t.Fatalf("saved ffprobe duration missing: %+v / %+v", input.ReleaseScoreInput.ReleaseExpectedDurationSeconds, input.AssignmentInput.RecordingExpectedDurationSeconds)
	}
	if input.AssignmentInput.ReleaseExpectedDurationSeconds == input.AssignmentInput.RecordingExpectedDurationSeconds {
		t.Fatal("release and recording duration inputs should be independently projected")
	}
	if input.ReleaseScoreInput.ReleaseActualDurationSeconds == nil || *input.ReleaseScoreInput.ReleaseActualDurationSeconds != 13.5 ||
		input.AssignmentInput.ReleaseActualDurationSeconds == nil || *input.AssignmentInput.ReleaseActualDurationSeconds != 13.5 {
		t.Fatalf("release actual duration missing or incorrect: %v / %v", input.ReleaseScoreInput.ReleaseActualDurationSeconds, input.AssignmentInput.ReleaseActualDurationSeconds)
	}
	if matchingFactor(input.ReleaseScore.Factors, MatchingReleaseDuration).State != MatchingFactorAvailable ||
		matchingFactor(input.AssignmentScore.Factors, MatchingReleaseDuration).State != MatchingFactorAvailable ||
		matchingFactor(input.AssignmentScore.Factors, MatchingRecordingDuration).State != MatchingFactorMissing {
		t.Fatal("release track and recording duration evidence were not scored independently")
	}
	for _, factor := range []MatchingFactor{
		matchingFactor(input.ReleaseScore.Factors, MatchingReleaseDuration),
		matchingFactor(input.AssignmentScore.Factors, MatchingReleaseDuration),
	} {
		if factor.SourceNormalized != "12" || factor.CandidateNormalized != "14" {
			t.Fatalf("release track duration did not use ties-to-even seconds: %+v", factor)
		}
	}
	acoustIDFactor := matchingFactor(input.AssignmentScore.Factors, MatchingAcoustID)
	if acoustIDFactor.State != MatchingFactorMissing {
		t.Fatal("absent AcoustID evidence should remain missing")
	}
	if acoustIDFactor.Weight != 4 || input.AssignmentScore.TotalWeight != 19 {
		t.Fatalf("missing AcoustID should not enter the score denominator: factor=%+v weight=%d", acoustIDFactor, input.AssignmentScore.TotalWeight)
	}
	if input.ReleaseScore.FactorsContainRecordingEvidence() {
		t.Fatal("release score contains recording-only evidence")
	}
	if input.AssignmentInput.ReleaseMusicBrainzScore != nil {
		*input.AssignmentInput.ReleaseMusicBrainzScore = 0
	}
	if got := snapshot.SearchScores["file-release-query"]["release"].Value; got != 80 {
		t.Fatalf("mapping mutated provider snapshot: %v", got)
	}
}

func TestBuildMatchingInputsUsesRecordingProjectionForAssignment(t *testing.T) {
	fileID, analysisID := uuid.New(), uuid.New()
	capture := persistence.IncomingGroupingCapture{
		VariantID: fileID, WorkID: uuid.New(), LocationID: uuid.New(), RootID: uuid.New(), CaptureAnalysisID: &analysisID,
		Ready: true, RelativePath: "track.flac", ConfiguredPath: "/music", InventoryPath: "/music", Mtime: time.Unix(1, 0),
		Tags: json.RawMessage(`{"ARTIST":["Recording Artist"],"TITLE":["Song"]}`), ProbeResult: json.RawMessage(`{"format":{"duration":"200"},"streams":[{"codec_type":"audio","duration":"200"}]}`),
	}
	snapshot := persistence.ProviderMatchingSnapshot{Releases: []persistence.MatchingRelease{{
		ProviderKey: "release", Name: "Album", Credits: []persistence.MatchingReleaseCredit{{Name: "Release Artist"}},
		Tracks: []persistence.MatchingReleaseTrack{{RecordingProviderKey: "recording", RecordingTitle: "Song", RecordingArtists: []string{"Recording Artist", "Recording Artist"}, RecordingLengthMS: int64Pointer(123000)}},
	}}}
	inputs, err := BuildMatchingInputs([]persistence.IncomingGroupingCapture{capture}, snapshot, nil, nil, map[string][]string{"recording": {"Stale caller artist"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 {
		t.Fatalf("got %d inputs, want one", len(inputs))
	}
	input := inputs[0].AssignmentInput
	if got := input.RecordingArtist.Candidate; len(got) != 2 || got[0] != "Recording Artist" || got[1] != "Recording Artist" {
		t.Fatalf("recording artist projection = %#v", got)
	}
	if got := input.ReleaseArtist.Candidate; len(got) != 1 || got[0] != "Release Artist" {
		t.Fatalf("release artist projection changed: %#v", got)
	}
	if input.RecordingExpectedDurationSeconds == nil || *input.RecordingExpectedDurationSeconds != 200 || input.RecordingActualDurationSeconds == nil || *input.RecordingActualDurationSeconds != 123 || input.ReleaseExpectedDurationSeconds == nil || *input.ReleaseExpectedDurationSeconds != 200 || input.ReleaseActualDurationSeconds != nil {
		t.Fatalf("release/recording durations = %v / %v", input.ReleaseExpectedDurationSeconds, input.RecordingExpectedDurationSeconds)
	}
	if matchingFactor(inputs[0].AssignmentScore.Factors, MatchingReleaseDuration).State != MatchingFactorMissing || matchingFactor(inputs[0].AssignmentScore.Factors, MatchingRecordingDuration).State != MatchingFactorAvailable {
		t.Fatal("release and recording durations were not scored from their distinct evidence")
	}
}

func int64Pointer(value int64) *int64 { return &value }

func TestBuildMatchingInputsKeepsSearchScoresFileScopedAndOptional(t *testing.T) {
	fileOne, fileTwo := uuid.New(), uuid.New()
	analysisOne, analysisTwo := uuid.New(), uuid.New()
	capture := func(file, analysis uuid.UUID, path string) persistence.IncomingGroupingCapture {
		return persistence.IncomingGroupingCapture{VariantID: file, WorkID: uuid.New(), LocationID: uuid.New(), RootID: uuid.New(), CaptureAnalysisID: &analysis, Ready: true, RelativePath: path, ConfiguredPath: "/music", InventoryPath: "/music", Mtime: time.Unix(1, 0), Tags: json.RawMessage(`{"TITLE":["Song"],"ARTIST":["Artist"]}`), ProbeResult: json.RawMessage(`{}`)}
	}
	snapshot := persistence.ProviderMatchingSnapshot{Releases: []persistence.MatchingRelease{{ProviderKey: "r", Name: "Album", Tracks: []persistence.MatchingReleaseTrack{{Position: 1, Medium: 0, RecordingProviderKey: "rec", RecordingTitle: "Song"}}}}, SearchScores: map[string]map[string]persistence.MatchingSearchScore{"query-one": {"r": {Value: 95, Present: true}}}}
	inputs, err := BuildMatchingInputs([]persistence.IncomingGroupingCapture{capture(fileOne, analysisOne, "one.flac"), capture(fileTwo, analysisTwo, "two.flac")}, snapshot, map[uuid.UUID]MatchingSearchKeys{fileOne: {Release: "query-one"}, fileTwo: {Release: "query-two"}}, map[MatchingAcoustIDKey]float64{{FileID: fileTwo, RecordingID: "rec"}: .75})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 {
		t.Fatalf("got %d inputs, want two", len(inputs))
	}
	byFile := map[uuid.UUID]MatchingScoreInput{inputs[0].FileID: inputs[0].AssignmentInput, inputs[1].FileID: inputs[1].AssignmentInput}
	if score := byFile[fileOne].ReleaseMusicBrainzScore; score == nil || *score != 95 {
		t.Fatalf("file one's query score absent: %v", score)
	}
	if score := byFile[fileTwo].ReleaseMusicBrainzScore; score != nil {
		t.Fatalf("score leaked across files: %v", *score)
	}
	if score := byFile[fileTwo].AcoustIDScore; score == nil || *score != .75 {
		t.Fatalf("optional saved AcoustID evidence lost: %v", score)
	}
	if byFile[fileOne].AcoustIDScore != nil {
		t.Fatalf("missing AcoustID evidence should remain absent")
	}
	for _, input := range inputs {
		if input.FileID == fileOne && matchingFactor(input.AssignmentScore.Factors, MatchingAcoustID).State != MatchingFactorMissing {
			t.Fatal("absent AcoustID score should not be projected as available")
		}
	}
	if len(inputs[0].AssignmentInput.RecordingArtist.Candidate) != 0 || matchingFactor(inputs[0].AssignmentScore.Factors, MatchingRecordingArtist).State != MatchingFactorMissing {
		t.Fatal("missing recording credits must not be replaced with release artists")
	}
}

func TestBuildMatchingInputsSeparatesReleaseWinnerFromAssignmentWinner(t *testing.T) {
	fileID, analysisID := uuid.New(), uuid.New()
	capture := persistence.IncomingGroupingCapture{
		VariantID: fileID, WorkID: uuid.New(), LocationID: uuid.New(), RootID: uuid.New(),
		CaptureAnalysisID: &analysisID, Ready: true, RelativePath: "song.flac", ConfiguredPath: "/music", InventoryPath: "/music",
		Mtime: time.Unix(1, 0), Tags: json.RawMessage(`{"ALBUM":["Album"],"TITLE":["Song"],"ARTIST":["Artist"],"MUSICBRAINZ_TRACKID":["11111111-1111-4111-8111-111111111111","22222222-2222-4222-8222-222222222222"]}`), ProbeResult: json.RawMessage(`{}`),
	}
	snapshot := persistence.ProviderMatchingSnapshot{
		Releases: []persistence.MatchingRelease{
			{ProviderKey: "release-exact", Name: "Album", Credits: []persistence.MatchingReleaseCredit{{Name: "Artist"}}, Tracks: []persistence.MatchingReleaseTrack{{Medium: 0, Position: 0, RecordingProviderKey: "recording-wrong", RecordingTitle: "Wrong"}}},
			{ProviderKey: "release-assignment", Name: "Elsewhere", Credits: []persistence.MatchingReleaseCredit{{Name: "Artist"}}, Tracks: []persistence.MatchingReleaseTrack{{Medium: 0, Position: 0, RecordingProviderKey: "recording-exact", RecordingTitle: "Song"}}},
		},
		SearchScores: map[string]map[string]persistence.MatchingSearchScore{
			"release-search":   {"release-exact": {Value: 0, Present: true}, "release-assignment": {Value: 100, Present: true}},
			"recording-search": {"recording-wrong": {Value: 0, Present: true}, "recording-exact": {Value: 100, Present: true}},
		},
	}
	inputs, err := BuildMatchingInputs([]persistence.IncomingGroupingCapture{capture}, snapshot, map[uuid.UUID]MatchingSearchKeys{fileID: {Release: "release-search", Recording: "recording-search"}}, map[MatchingAcoustIDKey]float64{
		{FileID: fileID, RecordingID: "recording-wrong"}: 0.1,
		{FileID: fileID, RecordingID: "recording-exact"}: 0.95,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 {
		t.Fatalf("got %d candidate edges, want two", len(inputs))
	}
	byRelease := map[string]MatchingInput{inputs[0].ReleaseID: inputs[0], inputs[1].ReleaseID: inputs[1]}
	if byRelease["release-exact"].ReleaseScore.Confidence <= byRelease["release-assignment"].ReleaseScore.Confidence {
		t.Fatalf("release profile did not prefer exact release: exact=%v other=%v", byRelease["release-exact"].ReleaseScore.Confidence, byRelease["release-assignment"].ReleaseScore.Confidence)
	}
	if byRelease["release-assignment"].AssignmentScore.Confidence <= byRelease["release-exact"].AssignmentScore.Confidence {
		t.Fatalf("combined assignment profile did not prefer track edge: exact=%v other=%v", byRelease["release-assignment"].AssignmentScore.Confidence, byRelease["release-exact"].AssignmentScore.Confidence)
	}
	if got := byRelease["release-assignment"].AssignmentInput.AcoustIDScore; got == nil || *got != 0.95 {
		t.Fatalf("AcoustID score for the matched recording was not selected: %v", got)
	}
	if got := byRelease["release-exact"].AssignmentInput.AcoustIDScore; got == nil || *got != 0.1 {
		t.Fatalf("AcoustID score leaked from a different recording: %v", got)
	}
	if got := byRelease["release-assignment"].SourceRecordingMBIDs; len(got) != 2 || got[0] != "11111111-1111-4111-8111-111111111111" || got[1] != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("valid source recording MBIDs were not retained: %#v", got)
	}
	if byRelease["release-assignment"].AssignmentInput.Evidence.RecordingMBID != "" {
		t.Fatalf("multiple source recording MBIDs should not select an arbitrary one: %+v", byRelease["release-assignment"].AssignmentInput.Evidence)
	}
}

func matchingFactor(factors []MatchingFactor, component MatchingComponent) MatchingFactor {
	for _, factor := range factors {
		if factor.Component == component {
			return factor
		}
	}
	return MatchingFactor{}
}

func (score MatchingScore) FactorsContainRecordingEvidence() bool {
	for _, factor := range score.Factors {
		switch factor.Component {
		case MatchingRecordingArtist, MatchingRecordingTitle, MatchingRecordingDuration, MatchingRecordingMusicBrainz, MatchingAcoustID:
			return true
		}
	}
	return false
}

func TestMatchingSourceFieldsBlankAlbumArtistFallbackPreservesRawValues(t *testing.T) {
	fields := matchingSourceFields(map[string][]string{"ALBUMARTIST": {" ", ""}, "ARTIST": {"Artist"}})
	if len(fields["album_artist"]) != 3 || fields["album_artist"][0] != " " || fields["album_artist"][1] != "" || fields["album_artist"][2] != "Artist" {
		t.Fatalf("fallback did not preserve raw values: %#v", fields["album_artist"])
	}
	if !matchingHasUsableText(fields["album_artist"]) {
		t.Fatal("artist fallback is not usable comparison text")
	}
}

func TestBuildMatchingInputsDoesNotInventTotalsForIncompleteTracks(t *testing.T) {
	fileID, analysisID := uuid.New(), uuid.New()
	capture := persistence.IncomingGroupingCapture{
		VariantID: fileID, WorkID: uuid.New(), LocationID: uuid.New(), RootID: uuid.New(), CaptureAnalysisID: &analysisID,
		Ready: true, RelativePath: "track.flac", ConfiguredPath: "/music", InventoryPath: "/music", Mtime: time.Unix(1, 0),
		Tags: json.RawMessage(`{"ALBUM":["Album"],"ARTIST":["Artist"]}`), ProbeResult: json.RawMessage(`{}`),
	}
	track := persistence.MatchingReleaseTrack{Medium: 0, Position: 0, RecordingProviderKey: "recording", RecordingTitle: "Song"}
	for _, complete := range []bool{false, true} {
		snapshot := persistence.ProviderMatchingSnapshot{Releases: []persistence.MatchingRelease{{ProviderKey: "release", Name: "Album", Complete: complete, Tracks: []persistence.MatchingReleaseTrack{track}}}}
		inputs, err := BuildMatchingInputs([]persistence.IncomingGroupingCapture{capture}, snapshot, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(inputs) != 1 {
			t.Fatalf("got %d inputs, want one", len(inputs))
		}
		got := inputs[0].AssignmentInput
		if complete {
			if got.CandidateTrackTotal == nil || *got.CandidateTrackTotal != 1 || got.CandidateDiscTotal == nil || *got.CandidateDiscTotal != 1 {
				t.Fatalf("complete track collection totals missing: %+v", got)
			}
		} else if got.CandidateTrackTotal != nil || got.CandidateDiscTotal != nil {
			t.Fatalf("partial track graph fabricated totals: %+v", got)
		}
	}
}
