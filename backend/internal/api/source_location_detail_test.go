package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// technicalResultFixture is a stored ffprobe response with a video stream, two
// audio streams and tags in two namespaces. It pins the exact contract the
// inspector must present: all audio streams, no non-audio stream, uppercase
// tags, deduplicated values and unknown values as null.
const technicalResultFixture = `{
  "format": {
    "format_name": "flac",
    "format_long_name": "raw FLAC",
    "duration": "1.5",
    "bit_rate": "1411200",
    "tags": {"TITLE": "Song", "artist": ["A", "B"], "ARTIST": "A"}
  },
  "streams": [
    {"index": 0, "codec_type": "video", "codec_name": "mjpeg"},
    {"index": 1, "codec_type": "audio", "codec_name": "flac", "profile": "FLAC",
     "duration": "1.5", "bit_rate": "1000000", "sample_rate": "44100", "sample_fmt": "s16",
     "bits_per_raw_sample": "16", "bits_per_sample": "0", "channels": 2,
     "channel_layout": "stereo", "tags": {"title": "Song", "album": "X"}},
    {"index": 2, "codec_type": "audio", "codec_name": "aac", "sample_rate": "-1", "channels": "0"}
  ]
}`

func analysisLocation(rootID uuid.UUID, relativePath string, mtime time.Time) persistence.SourceLocation {
	return persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: rootID, RelativePath: relativePath, SizeBytes: 2048,
		Mtime: mtime.Truncate(time.Microsecond), LastSeenScanGeneration: 1,
		ProbeStatus: persistence.SourceProbeStatusAudio,
	}
}

func decodeSourceLocationDetail(t *testing.T, response *httptest.ResponseRecorder) api.SourceLocationDetailResponse {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("detail status=%d, want 200: %s", response.Code, response.Body.String())
	}
	var detail api.SourceLocationDetailResponse
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode the location detail: %v: %s", err, response.Body.String())
	}
	return detail
}

func TestSourceLocationDetailReportsIdentityAvailabilityAndActiveOperation(t *testing.T) {
	fixture := newSourceAnalysisAPIFixture(t, supportedSourcePlatform(), true)
	root := fixture.seedAnalysisRoot(t, nil)
	mtime := time.Date(2026, time.September, 2, 8, 30, 0, 123000000, time.UTC)
	location := analysisLocation(root.ID, "disc/track.flac", mtime)
	fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}
	active := &persistence.Operation{
		ID: uuid.New(), Kind: service.SourceAnalysisOperationKind, State: "queued", Stage: service.SourceAnalysisStageQueued,
		TargetSourceRootID: &root.ID, TargetSourceLocationID: &location.ID,
	}
	if err := fixture.operations.CreateOperation(context.Background(), active); err != nil {
		t.Fatalf("store the queued analysis: %v", err)
	}

	response := sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+root.ID.String()+"/locations/"+location.ID.String(), "")
	if response.Code != http.StatusOK {
		t.Fatalf("detail status=%d: %s", response.Code, response.Body.String())
	}
	detail := decodeSourceLocationDetail(t, response)
	if detail.RootID != root.ID || detail.LocationID != location.ID || detail.RelativePath != "disc/track.flac" ||
		detail.SizeBytes != 2048 || !detail.Mtime.Equal(mtime.Truncate(time.Microsecond)) ||
		detail.ProbeStatus != persistence.SourceProbeStatusAudio {
		t.Fatalf("detail identity = %+v", detail)
	}
	if detail.Root.Status != persistence.SourceRootStatusAvailable || !detail.Root.Enabled || detail.Root.Stale ||
		detail.Root.SafeError != nil || detail.Root.InventoryPath == nil {
		t.Fatalf("detail root availability = %+v", detail.Root)
	}
	if detail.MediaVariantID != nil || detail.Result != nil || detail.AnalysisState != "not_analyzed" {
		t.Fatalf("never-analyzed detail = %+v", detail)
	}
	if detail.ActiveAnalysisOperationID == nil || *detail.ActiveAnalysisOperationID != active.ID {
		t.Fatalf("active analysis = %v, want %s", detail.ActiveAnalysisOperationID, active.ID)
	}

	foreignRoot := fixture.seedAnalysisRoot(t, nil)
	foreign := analysisLocation(foreignRoot.ID, "other.flac", mtime)
	fixture.repository.locations[foreignRoot.ID] = []persistence.SourceLocation{foreign}
	for _, path := range []string{
		"/sources/" + root.ID.String() + "/locations/" + uuid.NewString(),
		"/sources/" + root.ID.String() + "/locations/" + foreign.ID.String(),
		"/sources/" + uuid.NewString() + "/locations/" + location.ID.String(),
	} {
		missing := sourceRequest(t, fixture.handler, http.MethodGet, path, "")
		if missing.Code != http.StatusNotFound {
			t.Fatalf("GET %s status=%d, want 404: %s", path, missing.Code, missing.Body.String())
		}
	}
}

func TestSourceLocationDetailPresentsTypedStoredResult(t *testing.T) {
	fixture := newSourceAnalysisAPIFixture(t, supportedSourcePlatform(), true)
	root := fixture.seedAnalysisRoot(t, nil)
	location := analysisLocation(root.ID, "disc/track.flac", time.Now().UTC())
	inspectedAt := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	applied := uuid.New()
	variant := &persistence.MediaVariant{
		ID: uuid.New(), SizeBytes: location.SizeBytes, AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion,
		FFProbeVersion: "6.1.1", FFProbeJSON: json.RawMessage(technicalResultFixture),
		ObservedTags: json.RawMessage(`{"TITLE":["Song"]}`), InspectedAt: inspectedAt, AppliedOperationID: applied,
	}
	fixture.repository.variants[variant.ID] = variant
	location.MediaVariantID = &variant.ID
	fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}

	response := sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+root.ID.String()+"/locations/"+location.ID.String(), "")
	if response.Code != http.StatusOK {
		t.Fatalf("detail status=%d: %s", response.Code, response.Body.String())
	}
	detail := decodeSourceLocationDetail(t, response)
	if detail.AnalysisState != "analyzed" || detail.MediaVariantID == nil || *detail.MediaVariantID != variant.ID || detail.Result == nil {
		t.Fatalf("analyzed detail = %+v", detail)
	}
	result := detail.Result
	if result.FFProbeVersion != "6.1.1" || result.AnalysisPolicyVersion != persistence.SourceAnalysisPolicyVersion ||
		!result.InspectedAt.Equal(inspectedAt) || result.AppliedOperationID != applied {
		t.Fatalf("result provenance = %+v", result)
	}
	if result.Container.Name == nil || *result.Container.Name != "flac" || result.Container.LongName == nil ||
		*result.Container.LongName != "raw FLAC" || result.Container.DurationMS == nil || *result.Container.DurationMS != 1500 ||
		result.Container.BitRate == nil || *result.Container.BitRate != 1411200 {
		t.Fatalf("container = %+v", result.Container)
	}
	if len(result.Streams) != 2 {
		t.Fatalf("streams = %d, want 2 audio streams: %+v", len(result.Streams), result.Streams)
	}
	first := result.Streams[0]
	if first.Index == nil || *first.Index != 1 || first.CodecName == nil || *first.CodecName != "flac" ||
		first.Profile == nil || *first.Profile != "FLAC" || first.DurationMS == nil || *first.DurationMS != 1500 ||
		first.BitRate == nil || *first.BitRate != 1000000 || first.SampleRateHz == nil || *first.SampleRateHz != 44100 ||
		first.SampleFormat == nil || *first.SampleFormat != "s16" || first.BitsPerSample == nil || *first.BitsPerSample != 16 ||
		first.Channels == nil || *first.Channels != 2 || first.ChannelLayout == nil || *first.ChannelLayout != "stereo" {
		t.Fatalf("first audio stream = %+v", first)
	}
	second := result.Streams[1]
	if second.Index == nil || *second.Index != 2 || second.CodecName == nil || *second.CodecName != "aac" {
		t.Fatalf("second audio stream = %+v", second)
	}
	if second.Profile != nil || second.DurationMS != nil || second.BitRate != nil || second.SampleRateHz != nil ||
		second.SampleFormat != nil || second.BitsPerSample != nil || second.Channels != nil || second.ChannelLayout != nil {
		t.Fatalf("unknown stream values became non-null: %+v", second)
	}
	if len(result.Tags) != 3 || !stringSlicesEqual(result.Tags["TITLE"], []string{"Song"}) ||
		!stringSlicesEqual(result.Tags["ARTIST"], []string{"A", "B"}) || !stringSlicesEqual(result.Tags["ALBUM"], []string{"X"}) {
		t.Fatalf("observed tags = %+v", result.Tags)
	}
	streams, ok := result.RawJSON["streams"].([]any)
	if !ok || len(streams) != 3 {
		t.Fatalf("raw snapshot streams = %#v", result.RawJSON["streams"])
	}
	if body := response.Body.String(); !strings.Contains(body, `"raw_json"`) || !strings.Contains(body, `"mjpeg"`) {
		t.Fatalf("detail body lost the raw ffprobe JSON: %s", body)
	}
}

func TestSourceLocationDetailStaysReadableWithoutSetupOrSupportedPlatform(t *testing.T) {
	diagnostic := settings.PlatformState{
		Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}, Diagnostic: true, Reason: "instance platform mismatch",
	}
	for _, test := range []struct {
		name     string
		platform settings.PlatformState
		setup    bool
	}{
		{"incomplete setup", supportedSourcePlatform(), false},
		{"diagnostic platform", diagnostic, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSourceAnalysisAPIFixture(t, test.platform, test.setup)
			root := fixture.seedAnalysisRoot(t, nil)
			location := analysisLocation(root.ID, "disc/track.flac", time.Now().UTC())
			fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}

			response := sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+root.ID.String()+"/locations/"+location.ID.String(), "")
			if response.Code != http.StatusOK {
				t.Fatalf("read-only detail status=%d, want 200: %s", response.Code, response.Body.String())
			}
			blocked := sourceRequest(t, fixture.handler, http.MethodPost,
				"/sources/"+root.ID.String()+"/locations/"+location.ID.String()+"/analyze",
				analyzeBody(location.SizeBytes, location.Mtime.Format(time.RFC3339Nano)))
			want := http.StatusConflict
			if test.platform.Diagnostic {
				want = http.StatusServiceUnavailable
			}
			if blocked.Code != want {
				t.Fatalf("mutation status=%d, want %d: %s", blocked.Code, want, blocked.Body.String())
			}
		})
	}

	handler := api.HandlerWithDependencies(api.Dependencies{})
	path := "/sources/" + uuid.NewString() + "/locations/" + uuid.NewString()
	if response := sourceRequest(t, handler, http.MethodGet, path, ""); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("detail without dependencies status=%d, want 503: %s", response.Code, response.Body.String())
	}
	if response := sourceRequest(t, handler, http.MethodPost, path+"/analyze", `{"expected_size_bytes":1,"expected_mtime":"2026-09-02T08:30:00Z"}`); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("analysis without dependencies status=%d, want 503: %s", response.Code, response.Body.String())
	}
}

func TestListSourceLocationsCarriesOnlyResultAvailability(t *testing.T) {
	fixture := newSourceAnalysisAPIFixture(t, supportedSourcePlatform(), true)
	root := fixture.seedAnalysisRoot(t, nil)
	location := analysisLocation(root.ID, "disc/track.flac", time.Now().UTC())
	variant := &persistence.MediaVariant{
		ID: uuid.New(), SizeBytes: location.SizeBytes, AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion,
		FFProbeVersion: "6.1.1", FFProbeJSON: json.RawMessage(technicalResultFixture),
		ObservedTags: json.RawMessage(`{}`), InspectedAt: time.Now().UTC(), AppliedOperationID: uuid.New(),
	}
	fixture.repository.variants[variant.ID] = variant
	location.MediaVariantID = &variant.ID
	fixture.repository.locations[root.ID] = []persistence.SourceLocation{location}

	response := sourceRequest(t, fixture.handler, http.MethodGet, "/sources/"+root.ID.String()+"/locations", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d: %s", response.Code, response.Body.String())
	}
	page := decodeSourceLocations(t, response)
	if len(page.Locations) != 1 || !page.Locations[0].HasResult || page.Locations[0].MediaVariantID == nil ||
		*page.Locations[0].MediaVariantID != variant.ID {
		t.Fatalf("listed location = %+v", page.Locations)
	}
	body := response.Body.String()
	for _, bulky := range []string{"raw_json", "ffprobe_json", "observed_tags", "analysis_policy_version", "ffprobe_version", "container", "streams"} {
		if strings.Contains(body, bulky) {
			t.Fatalf("compact location list carries %q: %s", bulky, body)
		}
	}
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
