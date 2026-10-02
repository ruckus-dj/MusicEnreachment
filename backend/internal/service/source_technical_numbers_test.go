package service_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// TestParseSourceTechnicalAnalysisParsesNumbersWithCheckedRanges sweeps one
// field at a time across JSON numbers, numeric strings and every rejected
// spelling. Each rejected value must become null without failing the parse; no
// case may fabricate a zero or a clamped number.
func TestParseSourceTechnicalAnalysisParsesNumbersWithCheckedRanges(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
		want  *int64
		read  func(service.SourceTechnicalAnalysis) *int64
	}{
		{"duration_json_number", "duration", "1.5", int64Pointer(1500), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_numeric_string", "duration", `"2.25"`, int64Pointer(2250), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_rounds_up", "duration", `"1.0006"`, int64Pointer(1001), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_rounds_down", "duration", `"1.0004"`, int64Pointer(1000), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_zero_is_a_value", "duration", "0.000000", int64Pointer(0), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_multihour", "duration", `"2147483.75"`, int64Pointer(2147483750), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_not_available", "duration", `"N/A"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_not_available_lowercase", "duration", `"n/a"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_empty_string", "duration", `""`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_absent", "duration", technicalAbsent, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_json_null", "duration", "null", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_negative", "duration", "-1.5", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_unparseable", "duration", `"not-a-duration"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_seconds_overflow", "duration", "9223372036854775807", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_milliseconds_overflow", "duration", `"9223372036854776"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_boundary_half_up", "duration", `"1.0005"`, int64Pointer(1001), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_boundary_below_half", "duration", `"1.0004999"`, int64Pointer(1000), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_sub_millisecond_half_up", "duration", `"0.0005"`, int64Pointer(1), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_boundary_with_exponent", "duration", `"1.0005e0"`, int64Pointer(1001), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_tiny_negative_exponent_rounds_to_zero", "duration", `"1e-31"`, int64Pointer(0), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_zero_with_large_exponent", "duration", `"0e31"`, int64Pointer(0), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_long_zero_fraction", "duration", "0." + strings.Repeat("0", 400), int64Pointer(0), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"duration_huge_exponent_overflows", "duration", `"1e100000"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.DurationMS }},
		{"bit_rate_numeric_string", "bit_rate", `"1411200"`, int64Pointer(1411200), func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.BitRate }},
		{"bit_rate_zero", "bit_rate", "0", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.BitRate }},
		{"bit_rate_negative", "bit_rate", "-128000", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.BitRate }},
		{"bit_rate_not_available", "bit_rate", `"N/A"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Container.BitRate }},
		{"sample_rate_json_number", "sample_rate", "44100", int64Pointer(44100), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_numeric_string", "sample_rate", `"48000"`, int64Pointer(48000), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_not_available", "sample_rate", `"N/A"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_negative", "sample_rate", "-1", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_zero", "sample_rate", "0", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_fractional", "sample_rate", "44100.5", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_overflow", "sample_rate", `"99999999999999999999"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_exact_decimal_fraction_zero", "sample_rate", `"9007199254740993.0"`, int64Pointer(9007199254740993), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_exact_exponent_string", "sample_rate", `"9.007199254740993e15"`, int64Pointer(9007199254740993), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_exact_exponent_json_number", "sample_rate", `9007199254740993e0`, int64Pointer(9007199254740993), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_exact_json_number", "sample_rate", `9007199254740993`, int64Pointer(9007199254740993), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_exact_json_number_fraction_zero", "sample_rate", `9007199254740993.0`, int64Pointer(9007199254740993), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_exponent_value", "sample_rate", `"4.41e4"`, int64Pointer(44100), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_seventy_leading_zeros", "sample_rate", `"` + strings.Repeat("0", 70) + `44100"`, int64Pointer(44100), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_zero_with_large_exponent", "sample_rate", `"0e31"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_huge_exponent_overflows", "sample_rate", `"1e100000"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_fraction_above_int64_exactness", "sample_rate", `"44100.0000000000001"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"sample_rate_fraction_tiny", "sample_rate", `"44100.000000000000000001"`, nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].SampleRateHz }},
		{"channels_zero", "channels", "0", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].Channels }},
		{"channels_value", "channels", "6", int64Pointer(6), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].Channels }},
		{"bits_per_sample_zero", "bits_per_sample", "0", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].BitsPerSample }},
		{"bits_per_sample_value", "bits_per_sample", "24", int64Pointer(24), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].BitsPerSample }},
		{"index_value", "index", "3", int64Pointer(3), func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].Index }},
		{"index_negative", "index", "-1", nil, func(a service.SourceTechnicalAnalysis) *int64 { return a.Streams[0].Index }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			analysis, err := service.ParseSourceTechnicalAnalysis([]byte(technicalNumericDocument(testCase.key, testCase.value)))
			if err != nil {
				t.Fatalf("parse %s=%s: %v", testCase.key, testCase.value, err)
			}
			assertTechnicalEqual(t, testCase.key, testCase.read(analysis), testCase.want)
		})
	}
}

// TestParseSourceTechnicalAnalysisPrefersRawBitsPerSample proves the bit-depth
// fact uses the reported raw depth when it is positive and falls back to the
// coded depth only then. A real FLAC stream reports coded 0 and raw 24, so a
// parser that trusted the coded value would lose the depth; the depth is never
// inferred from the codec or the sample format.
func TestParseSourceTechnicalAnalysisPrefersRawBitsPerSample(t *testing.T) {
	cases := []struct {
		name   string
		fields string
		want   *int64
	}{
		{"raw_over_zero_coded", `,"bits_per_sample":0,"bits_per_raw_sample":"24"`, int64Pointer(24)},
		{"raw_over_other_coded", `,"bits_per_sample":16,"bits_per_raw_sample":"24"`, int64Pointer(24)},
		{"raw_as_json_number", `,"bits_per_sample":0,"bits_per_raw_sample":24`, int64Pointer(24)},
		{"raw_missing_falls_back", `,"bits_per_sample":16`, int64Pointer(16)},
		{"raw_zero_falls_back", `,"bits_per_sample":24,"bits_per_raw_sample":0`, int64Pointer(24)},
		{"raw_negative_falls_back", `,"bits_per_sample":24,"bits_per_raw_sample":-1`, int64Pointer(24)},
		{"raw_not_available_falls_back", `,"bits_per_sample":24,"bits_per_raw_sample":"N/A"`, int64Pointer(24)},
		{"both_zero_null", `,"bits_per_sample":0,"bits_per_raw_sample":0`, nil},
		{"both_missing_null", ``, nil},
		{"sample_format_is_not_a_source", `,"sample_fmt":"s16"`, nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			document := fmt.Sprintf(`{"format":{"format_name":"flac"},"streams":[{"codec_type":"audio"%s}]}`, testCase.fields)
			analysis, err := service.ParseSourceTechnicalAnalysis([]byte(document))
			if err != nil {
				t.Fatalf("parse %s: %v", document, err)
			}
			assertTechnicalEqual(t, "bits_per_sample", analysis.Streams[0].BitsPerSample, testCase.want)
		})
	}
}
