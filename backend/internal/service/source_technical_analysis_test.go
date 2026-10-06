package service_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func int64Pointer(value int64) *int64 { return &value }

func stringPointer(value string) *string { return &value }

// assertTechnicalEqual compares two read-model fragments by their exact machine
// values. Marshalling normalizes map key order and renders a nil pointer as
// null, so a fabricated zero or a changed string fails with a readable diff.
func assertTechnicalEqual(t *testing.T, label string, got, want any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal parsed %s: %v", label, err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal expected %s: %v", label, err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Errorf("%s = %s, want %s", label, gotJSON, wantJSON)
	}
}

// technicalFixture is one raw ffprobe response on disk with the exact read-model
// it must produce.
type technicalFixture struct {
	fixture    string
	container  service.SourceTechnicalContainer
	streams    []service.SourceTechnicalAudioStream
	tags       map[string][]string
	rawMarkers []string
}

// TestParseSourceTechnicalAnalysisNormalizesFixtures drives FLAC, MP3 and MKA
// responses through the parser and asserts every machine value: container
// names, duration in milliseconds and bit rate, all audio streams ordered by
// index, and the merged observed tags. Non-audio streams (here an attached
// picture) must stay in the raw snapshot only.
func TestParseSourceTechnicalAnalysisNormalizesFixtures(t *testing.T) {
	cases := []technicalFixture{
		{
			fixture: "flac.json",
			container: service.SourceTechnicalContainer{
				Name: stringPointer("flac"), LongName: stringPointer("raw FLAC"),
				DurationMS: int64Pointer(3600001), BitRate: int64Pointer(1411200),
			},
			streams: []service.SourceTechnicalAudioStream{
				{
					Index: int64Pointer(0), CodecName: stringPointer("flac"), Profile: stringPointer("unknown"),
					DurationMS: int64Pointer(3600000), BitRate: int64Pointer(1411200),
					SampleRateHz: int64Pointer(44100), SampleFormat: stringPointer("s32"),
					BitsPerSample: int64Pointer(24), Channels: int64Pointer(2), ChannelLayout: stringPointer("stereo"),
				},
				{
					Index: int64Pointer(2), CodecName: stringPointer("flac"), Profile: stringPointer("unknown"),
					DurationMS: int64Pointer(1234567), BitRate: nil,
					SampleRateHz: int64Pointer(96000), SampleFormat: stringPointer("s16"),
					BitsPerSample: int64Pointer(16), Channels: int64Pointer(2), ChannelLayout: stringPointer("stereo"),
				},
			},
			tags: map[string][]string{
				"ARTIST":  {"Primary Artist", "Second Artist"},
				"ALBUM":   {"Shared Album"},
				"GENRE":   {"Rock", "Jazz"},
				"COMMENT": {"dup"},
				"TITLE":   {"First / Second; Third"},
			},
			rawMarkers: []string{`"attached_pic": 1`, `"codec_name": "mjpeg"`, `"bits_per_sample": 0`, `"bits_per_raw_sample": "24"`, `"bits_per_raw_sample": 16`},
		},
		{
			fixture: "mp3.json",
			container: service.SourceTechnicalContainer{
				Name: stringPointer("mp3"), LongName: stringPointer("MP2/3 (MPEG audio layer 2/3)"),
				DurationMS: int64Pointer(187494), BitRate: int64Pointer(320121),
			},
			streams: []service.SourceTechnicalAudioStream{
				{
					Index: int64Pointer(0), CodecName: stringPointer("mp3"), Profile: stringPointer("CBR"),
					DurationMS: int64Pointer(187500), BitRate: int64Pointer(320000),
					SampleRateHz: int64Pointer(44100), SampleFormat: stringPointer("s16p"),
					BitsPerSample: nil, Channels: int64Pointer(2), ChannelLayout: stringPointer("stereo"),
				},
			},
			tags: map[string][]string{
				"TITLE":  {"Song", "song"},
				"ARTIST": {"Artist"},
				"ALBUM":  {"Album"},
			},
		},
		{
			fixture: "mka.json",
			container: service.SourceTechnicalContainer{
				Name: stringPointer("matroska,webm"), LongName: stringPointer("Matroska / WebM"),
				DurationMS: int64Pointer(2147483750), BitRate: int64Pointer(1411200),
			},
			streams: []service.SourceTechnicalAudioStream{
				{
					Index: int64Pointer(1), CodecName: stringPointer("opus"), Profile: stringPointer("unknown"),
					DurationMS: int64Pointer(7200250), BitRate: nil,
					SampleRateHz: int64Pointer(48000), SampleFormat: stringPointer("s16"),
					BitsPerSample: nil, Channels: int64Pointer(2), ChannelLayout: stringPointer("stereo"),
				},
				{
					Index: int64Pointer(2), CodecName: stringPointer("ac3"), Profile: nil,
					DurationMS: nil, BitRate: int64Pointer(192000),
					SampleRateHz: nil, SampleFormat: stringPointer("fltp"),
					BitsPerSample: nil, Channels: nil, ChannelLayout: nil,
				},
				{
					Index: int64Pointer(3), CodecName: stringPointer("aaaa_unknown_codec"), Profile: nil,
					DurationMS: int64Pointer(26000750), BitRate: nil,
					SampleRateHz: nil, SampleFormat: stringPointer("fltp"),
					BitsPerSample: nil, Channels: int64Pointer(6), ChannelLayout: stringPointer("5.1"),
				},
			},
			tags: map[string][]string{
				"ENCODER":  {"libebml"},
				"TITLE":    {"Alt", "Main"},
				"GENRE":    {"ambient/drone"},
				"COMMENT":  {"third"},
				"LANGUAGE": {"jpn"},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.fixture, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "technical", testCase.fixture))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			analysis, err := service.ParseSourceTechnicalAnalysis(raw)
			if err != nil {
				t.Fatalf("parse %s: %v", testCase.fixture, err)
			}
			if !bytes.Equal(analysis.RawJSON, raw) {
				t.Fatalf("raw snapshot of %s changed: %d bytes in, %d bytes out", testCase.fixture, len(raw), len(analysis.RawJSON))
			}
			for _, marker := range testCase.rawMarkers {
				if !bytes.Contains(analysis.RawJSON, []byte(marker)) {
					t.Errorf("raw snapshot lost %s", marker)
				}
			}
			assertTechnicalEqual(t, "container", analysis.Container, testCase.container)
			assertTechnicalEqual(t, "streams", analysis.Streams, testCase.streams)
			assertTechnicalEqual(t, "tags", analysis.Tags, testCase.tags)
		})
	}
}

// TestParseSourceTechnicalAnalysisKeepsMultihourDurationBeyondInt32 pins a
// duration whose millisecond value no longer fits an int32, so a regression
// that narrows the read-model to a 32-bit conversion cannot pass.
func TestParseSourceTechnicalAnalysisKeepsMultihourDurationBeyondInt32(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "technical", "mka.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	analysis, err := service.ParseSourceTechnicalAnalysis(raw)
	if err != nil {
		t.Fatalf("parse mka fixture: %v", err)
	}
	if analysis.Container.DurationMS == nil {
		t.Fatal("multihour format duration became null")
	}
	if *analysis.Container.DurationMS != 2147483750 {
		t.Errorf("multihour format duration = %d, want 2147483750", *analysis.Container.DurationMS)
	}
	if *analysis.Container.DurationMS <= int64(math.MaxInt32) {
		t.Errorf("multihour format duration %d did not exceed int32; a 32-bit conversion would truncate it", *analysis.Container.DurationMS)
	}
}

// TestParseSourceTechnicalAnalysisRejectsMalformedStructure proves the parser
// refuses a response whose mandatory shape is wrong instead of guessing facts
// from it. A rejected response must not return a partially populated read-model.
func TestParseSourceTechnicalAnalysisRejectsMalformedStructure(t *testing.T) {
	cases := []struct {
		name     string
		document string
	}{
		{"empty", ""},
		{"whitespace", "  \n\t "},
		{"not_object_array", "[]"},
		{"not_object_string", `"ffprobe"`},
		{"not_object_number", "123"},
		{"null_root", "null"},
		{"no_format", `{"streams":[]}`},
		{"format_null", `{"format":null,"streams":[]}`},
		{"format_array", `{"format":[],"streams":[]}`},
		{"no_streams", `{"format":{}}`},
		{"streams_null", `{"format":{},"streams":null}`},
		{"streams_object", `{"format":{},"streams":{}}`},
		{"stream_null", `{"format":{},"streams":[null]}`},
		{"stream_number", `{"format":{},"streams":[7]}`},
		{"stream_without_codec_type", `{"format":{},"streams":[{"index":0}]}`},
		{"stream_codec_type_null", `{"format":{},"streams":[{"codec_type":null}]}`},
		{"stream_codec_type_number", `{"format":{},"streams":[{"codec_type":7}]}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			analysis, err := service.ParseSourceTechnicalAnalysis([]byte(testCase.document))
			if !errors.Is(err, service.ErrSourceTechnicalMalformed) {
				t.Fatalf("parse %q error = %v, want ErrSourceTechnicalMalformed", testCase.document, err)
			}
			if analysis.RawJSON != nil || analysis.Streams != nil || analysis.Tags != nil || analysis.Container != (service.SourceTechnicalContainer{}) {
				t.Errorf("rejected response returned a populated read-model: %+v", analysis)
			}
		})
	}
}

func TestParseSourceTechnicalAnalysisAcceptsNoAudioStreams(t *testing.T) {
	raw := []byte(`{"format":{"format_name":"wav","tags":{"artist":"Container Artist"}},"streams":[{"codec_type":"video"}]}`)
	analysis, err := service.ParseSourceTechnicalAnalysis(raw)
	if err != nil {
		t.Fatalf("parse valid no-audio response: %v", err)
	}
	if analysis.Streams == nil || len(analysis.Streams) != 0 {
		t.Fatalf("streams = %#v, want empty non-nil slice", analysis.Streams)
	}
	assertTechnicalEqual(t, "tags", analysis.Tags, map[string][]string{"ARTIST": {"Container Artist"}})
	if !bytes.Equal(analysis.RawJSON, raw) {
		t.Error("raw snapshot changed")
	}
}

// TestParseSourceTechnicalAnalysisAcceptsMinimalAudioResponse pins the nullable
// defaults: a valid response with no facts at all yields null values, an empty
// (non-nil) tag map and one stream, never a fabricated zero.
func TestParseSourceTechnicalAnalysisAcceptsMinimalAudioResponse(t *testing.T) {
	raw := []byte(`{"format":{},"streams":[{"codec_type":"audio"}]}`)
	analysis, err := service.ParseSourceTechnicalAnalysis(raw)
	if err != nil {
		t.Fatalf("parse minimal audio response: %v", err)
	}
	if len(analysis.Streams) != 1 {
		t.Fatalf("streams = %d, want 1", len(analysis.Streams))
	}
	assertTechnicalEqual(t, "container", analysis.Container, service.SourceTechnicalContainer{})
	assertTechnicalEqual(t, "stream", analysis.Streams[0], service.SourceTechnicalAudioStream{})
	if analysis.Tags == nil {
		t.Error("tags map is nil, want an empty map")
	}
	if len(analysis.Tags) != 0 {
		t.Errorf("tags = %v, want none", analysis.Tags)
	}
	if !bytes.Equal(analysis.RawJSON, raw) {
		t.Error("raw snapshot changed")
	}
}

const technicalAbsent = "\x00"

// technicalNumericDocument builds a minimal valid response whose one probed
// field carries the given JSON literal. Container facts are tested through
// "duration"/"bit_rate"; every other key goes on the single audio stream.
func technicalNumericDocument(key, value string) string {
	containerFields, streamFields := "", ""
	if value != technicalAbsent {
		if key == "duration" || key == "bit_rate" {
			containerFields = fmt.Sprintf(`,%q:%s`, key, value)
		} else {
			streamFields = fmt.Sprintf(`,%q:%s`, key, value)
		}
	}
	return fmt.Sprintf(`{"format":{"format_name":"wav"%s},"streams":[{"codec_type":"audio"%s}]}`, containerFields, streamFields)
}
