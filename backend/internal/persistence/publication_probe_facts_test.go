package persistence

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPublicationProbeFactsTreatsZeroSampleDepthAsUnknown(t *testing.T) {
	for _, probe := range []struct {
		codec, format string
	}{
		{codec: "mp3", format: "mp3"},
		{codec: "aac", format: "mov,mp4,m4a,3gp,3g2,mj2"},
		{codec: "opus", format: "ogg"},
	} {
		t.Run(probe.codec, func(t *testing.T) {
			facts, err := publicationAudioFactsFromProbe(publicationProbeFixture(probe.codec, probe.format, "0", "0"), 0)
			if err != nil {
				t.Fatal(err)
			}
			if facts.BitDepth != nil {
				t.Fatalf("bit depth = %d, want unknown", *facts.BitDepth)
			}
			if facts.Lossless == nil || *facts.Lossless {
				t.Fatalf("lossless = %v, want false", facts.Lossless)
			}
		})
	}
}

func TestPublicationProbeFactsSampleDepthFallbackAndPreference(t *testing.T) {
	for _, test := range []struct {
		name     string
		raw, bit string
		want     int
	}{
		{name: "zero raw falls back", raw: "0", bit: "16", want: 16},
		{name: "positive raw takes precedence", raw: "24", bit: "16", want: 24},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts, err := publicationAudioFactsFromProbe(publicationProbeFixture("flac", "flac", test.raw, test.bit), 0)
			if err != nil {
				t.Fatal(err)
			}
			if facts.BitDepth == nil || *facts.BitDepth != test.want {
				t.Fatalf("bit depth = %v, want %d", facts.BitDepth, test.want)
			}
		})
	}
}

func TestPublicationProbeFactsRejectsInvalidSampleDepth(t *testing.T) {
	for _, depth := range []string{"-1", "16.5", "9223372036854775808"} {
		t.Run(depth, func(t *testing.T) {
			_, err := publicationAudioFactsFromProbe(publicationProbeFixture("flac", "flac", depth, "16"), 0)
			if err == nil {
				t.Fatal("expected invalid sample depth error")
			}
		})
	}
}

func TestPublicationProbeFactsRequireExactAACFormatName(t *testing.T) {
	for _, test := range []struct {
		format string
		valid  bool
	}{
		{format: "aac", valid: true},
		{format: "aac_latm"},
		{format: "mov,mp4,m4a,3gp,3g2,mj2", valid: true},
		{format: "matroska,webm", valid: true},
	} {
		t.Run(test.format, func(t *testing.T) {
			facts, err := publicationAudioFactsFromProbe(publicationProbeFixture("aac", test.format, "0", "0"), 0)
			if (err == nil) != test.valid {
				t.Fatalf("format %q: facts=%#v err=%v, valid=%v", test.format, facts, err, test.valid)
			}
			if test.valid && facts.BitDepth != nil {
				t.Fatalf("lossy AAC bit depth = %v, want unknown", facts.BitDepth)
			}
		})
	}
}

func publicationProbeFixture(codec, format, rawBits, bits string) SourceMediaVariant {
	policyVersion := 1
	ffprobeVersion := "ffprobe version test"
	inspectedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	operationID := uuid.New()
	audioStreamCount := 1
	return SourceMediaVariant{
		AnalysisPolicyVersion: &policyVersion,
		FFProbeVersion:        &ffprobeVersion,
		FFProbeJSON: json.RawMessage(fmt.Sprintf(
			`{"format":{"format_name":%q},"streams":[{"index":0,"codec_type":"audio","codec_name":%q,"channels":2,"bits_per_raw_sample":%s,"bits_per_sample":%s}]}`,
			format, codec, rawBits, bits,
		)),
		InspectedAt:        &inspectedAt,
		AppliedOperationID: &operationID,
		AudioStreamCount:   &audioStreamCount,
	}
}
