package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ErrSourceTechnicalMalformed reports a raw ffprobe technical response whose
// mandatory structure is not one an analysis may store. It is a hard failure:
// the values of a response that is not an object with a format object and an
// audio-bearing streams array cannot be trusted. Individual numbers and tags
// that are absent, inapplicable or unparseable are not malformed structure;
// they become null and never fail the parse.
var ErrSourceTechnicalMalformed = errors.New("ffprobe technical response is malformed")

// SourceTechnicalAnalysis is the typed read-model of one stored ffprobe
// technical response. It carries the derived machine facts next to the exact
// bytes they were derived from: the raw snapshot is preserved byte-for-byte and
// is never replaced by this normalized shape. Non-audio streams (video,
// attached pictures) stay in RawJSON only; they are not normalized here.
type SourceTechnicalAnalysis struct {
	// RawJSON is the ffprobe response exactly as the probe returned it, with
	// every stream, namespace and conflicting value intact.
	RawJSON []byte
	// Container holds the outer format facts.
	Container SourceTechnicalContainer
	// Streams holds every audio stream, ordered by stream index ascending. An
	// audio stream without an index keeps its original relative order after the
	// indexed ones. Non-audio streams are never listed.
	Streams []SourceTechnicalAudioStream
	// Tags holds the observed source tags: format tags first, then the tags of
	// audio streams in index order. Each key is uppercase; each value is the
	// list of its distinct original strings in first-seen order. Strings are
	// never split on a separator and no provider or local tag is substituted.
	Tags map[string][]string
}

// SourceTechnicalContainer is the typed outer format of a technical response.
// Every unknown value is nil; the parser never guesses.
type SourceTechnicalContainer struct {
	// Name is format_name, unmodified and unsplit (for example "matroska,webm").
	Name *string
	// LongName is format_long_name, unmodified.
	LongName *string
	// DurationMS is the format duration rounded to the nearest millisecond.
	DurationMS *int64
	// BitRate is the format bit rate in bits per second.
	BitRate *int64
}

// SourceTechnicalAudioStream is one audio stream of a technical response. Every
// unknown value is nil. The parser neither selects one stream as the preferred
// one nor classifies a codec: an unknown codec is returned under its reported
// name like any other.
type SourceTechnicalAudioStream struct {
	// Index is the ffprobe stream index; nil when the response omitted it.
	Index *int64
	// CodecName is codec_name, unmodified.
	CodecName *string
	// Profile is profile, unmodified.
	Profile *string
	// DurationMS is the stream duration rounded to the nearest millisecond.
	DurationMS *int64
	// BitRate is the stream bit rate in bits per second.
	BitRate *int64
	// SampleRateHz is the sample rate in hertz.
	SampleRateHz *int64
	// SampleFormat is sample_fmt, unmodified (for example "s16", "fltp").
	SampleFormat *string
	// BitsPerSample is bits_per_sample.
	BitsPerSample *int64
	// Channels is the channel count.
	Channels *int64
	// ChannelLayout is channel_layout, unmodified (for example "5.1").
	ChannelLayout *string
}

// ParseSourceTechnicalAnalysis turns one raw ffprobe technical response into
// its typed read-model. The mandatory structure is validated first: the response
// must be a JSON object with a format object, a streams array of stream objects
// that each carry a string codec_type, and at least one audio stream. A
// response that breaks that shape fails with ErrSourceTechnicalMalformed.
//
// Every other imperfection is local to one value: a missing field, "N/A", an
// empty string, a negative number, a fractional counter or a number outside its
// field's range becomes null. Nothing is fabricated and no zero takes the place
// of an unknown value.
func ParseSourceTechnicalAnalysis(raw []byte) (SourceTechnicalAnalysis, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return SourceTechnicalAnalysis{}, fmt.Errorf("%w: empty response", ErrSourceTechnicalMalformed)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil || response == nil {
		return SourceTechnicalAnalysis{}, fmt.Errorf("%w: response is not an object", ErrSourceTechnicalMalformed)
	}
	rawFormat, ok := response["format"]
	if !ok {
		return SourceTechnicalAnalysis{}, fmt.Errorf("%w: response has no format", ErrSourceTechnicalMalformed)
	}
	var format map[string]json.RawMessage
	if err := json.Unmarshal(rawFormat, &format); err != nil || format == nil {
		return SourceTechnicalAnalysis{}, fmt.Errorf("%w: format is not an object", ErrSourceTechnicalMalformed)
	}
	rawStreams, ok := response["streams"]
	if !ok {
		return SourceTechnicalAnalysis{}, fmt.Errorf("%w: response has no streams", ErrSourceTechnicalMalformed)
	}
	var streams []json.RawMessage
	if err := json.Unmarshal(rawStreams, &streams); err != nil || streams == nil {
		return SourceTechnicalAnalysis{}, fmt.Errorf("%w: streams are not an array", ErrSourceTechnicalMalformed)
	}

	analysis := SourceTechnicalAnalysis{
		RawJSON: raw,
		Container: SourceTechnicalContainer{
			Name:       sourceTechnicalText(format["format_name"]),
			LongName:   sourceTechnicalText(format["format_long_name"]),
			DurationMS: sourceTechnicalDurationMS(format["duration"]),
			BitRate:    sourceTechnicalInt64(format["bit_rate"], sourceTechnicalPositiveMin),
		},
		Tags: map[string][]string{},
	}

	audio := make([]sourceTechnicalParsedStream, 0, len(streams))
	for position, rawStream := range streams {
		var stream map[string]json.RawMessage
		if err := json.Unmarshal(rawStream, &stream); err != nil || stream == nil {
			return SourceTechnicalAnalysis{}, fmt.Errorf("%w: stream %d is not an object", ErrSourceTechnicalMalformed, position)
		}
		rawCodec, ok := stream["codec_type"]
		if !ok {
			return SourceTechnicalAnalysis{}, fmt.Errorf("%w: stream %d has no codec_type", ErrSourceTechnicalMalformed, position)
		}
		var codecType *string
		if err := json.Unmarshal(rawCodec, &codecType); err != nil || codecType == nil {
			return SourceTechnicalAnalysis{}, fmt.Errorf("%w: stream %d codec_type is not a string", ErrSourceTechnicalMalformed, position)
		}
		if *codecType != "audio" {
			continue
		}
		audio = append(audio, sourceTechnicalParsedStream{
			facts: SourceTechnicalAudioStream{
				Index:         sourceTechnicalInt64(stream["index"], sourceTechnicalNonNegativeMin),
				CodecName:     sourceTechnicalText(stream["codec_name"]),
				Profile:       sourceTechnicalText(stream["profile"]),
				DurationMS:    sourceTechnicalDurationMS(stream["duration"]),
				BitRate:       sourceTechnicalInt64(stream["bit_rate"], sourceTechnicalPositiveMin),
				SampleRateHz:  sourceTechnicalInt64(stream["sample_rate"], sourceTechnicalPositiveMin),
				SampleFormat:  sourceTechnicalText(stream["sample_fmt"]),
				BitsPerSample: sourceTechnicalBitsPerSample(stream),
				Channels:      sourceTechnicalInt64(stream["channels"], sourceTechnicalPositiveMin),
				ChannelLayout: sourceTechnicalText(stream["channel_layout"]),
			},
			tags:     stream["tags"],
			position: position,
		})
	}
	if len(audio) == 0 {
		return SourceTechnicalAnalysis{}, fmt.Errorf("%w: response has no audio stream", ErrSourceTechnicalMalformed)
	}
	sort.SliceStable(audio, func(left, right int) bool {
		return sourceTechnicalStreamBefore(audio[left], audio[right])
	})

	appendSourceTechnicalTags(analysis.Tags, format["tags"])
	analysis.Streams = make([]SourceTechnicalAudioStream, 0, len(audio))
	for index := range audio {
		appendSourceTechnicalTags(analysis.Tags, audio[index].tags)
		analysis.Streams = append(analysis.Streams, audio[index].facts)
	}
	return analysis, nil
}

// sourceTechnicalParsedStream keeps the tags of one audio stream next to its
// typed facts until the streams are ordered by index; tags follow that order.
type sourceTechnicalParsedStream struct {
	facts    SourceTechnicalAudioStream
	tags     json.RawMessage
	position int
}

// sourceTechnicalStreamBefore orders audio streams by index ascending, falls
// back to the original position for a tie or a missing index, and keeps an
// unindexed stream after the indexed ones.
func sourceTechnicalStreamBefore(left, right sourceTechnicalParsedStream) bool {
	switch {
	case left.facts.Index != nil && right.facts.Index != nil:
		if *left.facts.Index != *right.facts.Index {
			return *left.facts.Index < *right.facts.Index
		}
	case left.facts.Index != nil:
		return true
	case right.facts.Index != nil:
		return false
	}
	return left.position < right.position
}

// sourceTechnicalText reads an optional string fact. Absent, JSON null, a
// non-string and a blank string are all unknown and become nil.
func sourceTechnicalText(raw json.RawMessage) *string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil
	}
	if strings.TrimSpace(*value) == "" {
		return nil
	}
	return value
}

// sourceTechnicalTag is one key/value pair of a tags object, kept in the order

// sourceTechnicalTag is one key/value pair of a tags object, kept in the order
// the response documents it.
type sourceTechnicalTag struct {
	key   string
	value json.RawMessage
}

// sourceTechnicalTags decodes a tags object into its ordered key/value pairs.
// The order of the document is preserved so that values merged from several
// source objects keep the order they were first seen in. A value that is not a
// tags object yields no pairs.
func sourceTechnicalTags(raw json.RawMessage) []sourceTechnicalTag {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	var tags []sourceTechnicalTag
	token, err := decoder.Token()
	if err != nil {
		return nil
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil
	}
	for decoder.More() {
		rawKey, err := decoder.Token()
		if err != nil {
			return nil
		}
		key, ok := rawKey.(string)
		if !ok {
			return nil
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil
		}
		tags = append(tags, sourceTechnicalTag{key: key, value: value})
	}
	return tags
}

// sourceTechnicalTagValues renders the values of one tag as original strings.
// A scalar is one value, an array is its values in order, and a null or another
// shape has none. Strings are returned exactly as written, never split.
func sourceTechnicalTagValues(raw json.RawMessage) []string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil
		}
		values := make([]string, 0, len(items))
		for _, item := range items {
			if value, ok := sourceTechnicalTagScalar(item); ok {
				values = append(values, value)
			}
		}
		return values
	}
	if value, ok := sourceTechnicalTagScalar(trimmed); ok {
		return []string{value}
	}
	return nil
}

// sourceTechnicalTagScalar renders one tag value as a string, keeping a number
// or a boolean in the spelling the response used.
func sourceTechnicalTagScalar(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false
	}
	if trimmed[0] == '"' {
		var value *string
		if err := json.Unmarshal(trimmed, &value); err != nil || value == nil {
			return "", false
		}
		return *value, true
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		var boolean *bool
		if err := json.Unmarshal(trimmed, &boolean); err != nil || boolean == nil {
			return "", false
		}
		return strconv.FormatBool(*boolean), true
	}
	return number.String(), true
}

// appendSourceTechnicalTags merges one tags object into the observed map. The
// key is uppercased; a value is appended only when the same exact string is not
// already present for that key, so first-seen order is kept and duplicates are
// dropped without reordering survivors.
func appendSourceTechnicalTags(tags map[string][]string, raw json.RawMessage) {
	for _, tag := range sourceTechnicalTags(raw) {
		key := strings.ToUpper(tag.key)
		for _, value := range sourceTechnicalTagValues(tag.value) {
			if !slices.Contains(tags[key], value) {
				tags[key] = append(tags[key], value)
			}
		}
	}
}
