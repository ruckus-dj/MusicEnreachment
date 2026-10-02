package service

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// Bounds of a technical number. A value below its field's bound is inapplicable
// or invalid and becomes null: it is never clamped, replaced with zero, or
// turned into a technical value the source did not report.
const (
	sourceTechnicalNonNegativeMin int64 = 0
	sourceTechnicalPositiveMin    int64 = 1
)

// sourceTechnicalExponentSaturation keeps the parsed exponent inside an int
// while the literal's text is scanned; no value is ever materialized from it.
// A real exponent beyond this magnitude is handled by the magnitude checks: a
// positive one overflows every field and a negative one rounds a duration to
// zero, so saturation cannot change an int64 or nearest-millisecond result.
const sourceTechnicalExponentSaturation = 1 << 30

// sourceTechnicalNumberLiteral returns the decimal text of a JSON number or of
// a numeric string, so both spellings go through the same checked parse. A
// JSON null, a non-numeric scalar and a malformed value are unknown.
func sourceTechnicalNumberLiteral(raw json.RawMessage) (string, bool) {
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
		return "", false
	}
	return number.String(), true
}

// sourceTechnicalInt64 parses a non-negative integer fact with its field's
// lower bound. The literal is read exactly, so nothing is rounded into an
// integer: a missing, inapplicable, negative, fractional, unparseable or
// out-of-range value is nil, and only an exact value inside [min, MaxInt64] is
// returned.
func sourceTechnicalInt64(raw json.RawMessage, min int64) *int64 {
	literal, ok := sourceTechnicalNumberLiteral(raw)
	if !ok {
		return nil
	}
	value, ok := sourceTechnicalParseDecimal(strings.TrimSpace(literal))
	if !ok || value.negative {
		return nil
	}
	text, ok := sourceTechnicalExactInteger(value)
	if !ok {
		return nil
	}
	number, err := strconv.ParseInt(text, 10, 64)
	if err != nil || number < min {
		return nil
	}
	return &number
}

// sourceTechnicalBitsPerSample reads the bit depth of one audio stream. ffprobe
// reports the coded depth in bits_per_sample and the raw source depth in
// bits_per_raw_sample, and a container such as FLAC commonly leaves the coded
// value at 0 while the raw value carries the real depth. A positive
// bits_per_raw_sample therefore wins; a positive bits_per_sample is the
// fallback; anything else is nil. The depth is never inferred from the codec or
// the sample format, and both reported values stay in the raw snapshot.
func sourceTechnicalBitsPerSample(stream map[string]json.RawMessage) *int64 {
	if raw := sourceTechnicalInt64(stream["bits_per_raw_sample"], sourceTechnicalPositiveMin); raw != nil {
		return raw
	}
	return sourceTechnicalInt64(stream["bits_per_sample"], sourceTechnicalPositiveMin)
}

// sourceTechnicalDurationMS parses a duration in seconds as an exact decimal
// and rounds it to the nearest millisecond with halves away from zero. A
// negative, malformed or int64-overflowing duration is nil. Multihour values
// keep their full magnitude, tiny values round to 0, and no boundary value is
// lost to float rounding. Nothing proportional to the exponent is allocated.
func sourceTechnicalDurationMS(raw json.RawMessage) *int64 {
	literal, ok := sourceTechnicalNumberLiteral(raw)
	if !ok {
		return nil
	}
	seconds, ok := sourceTechnicalParseDecimal(strings.TrimSpace(literal))
	if !ok || seconds.negative {
		return nil
	}
	text, ok := sourceTechnicalRoundedInteger(seconds)
	if !ok {
		return nil
	}
	number, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil
	}
	return &number
}

// sourceTechnicalScaled is an exact decimal split into its significant digits
// and a power of ten: value = digits × 10^scale. Digits carry no leading zero
// and a zero value is the single digit "0".
type sourceTechnicalScaled struct {
	negative bool
	digits   string
	scale    int
}

// sourceTechnicalParseDecimal reads a plain decimal literal (optionally signed,
// optionally with an exponent) into its exact scaled form. It rejects every
// other spelling, including a fraction slash or a hexadecimal prefix, and it
// preserves any number of leading or trailing zeros. The working set is the
// literal itself, never a number proportional to the exponent.
func sourceTechnicalParseDecimal(text string) (sourceTechnicalScaled, bool) {
	index := 0
	negative := false
	if index < len(text) && (text[index] == '+' || text[index] == '-') {
		negative = text[index] == '-'
		index++
	}
	digits := make([]byte, 0, len(text))
	fractionDigits := 0
	for index < len(text) && text[index] >= '0' && text[index] <= '9' {
		digits = append(digits, text[index])
		index++
	}
	if index < len(text) && text[index] == '.' {
		index++
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			digits = append(digits, text[index])
			fractionDigits++
			index++
		}
	}
	if len(digits) == 0 {
		return sourceTechnicalScaled{}, false
	}
	exponent := 0
	if index < len(text) && (text[index] == 'e' || text[index] == 'E') {
		index++
		exponentNegative := false
		if index < len(text) && (text[index] == '+' || text[index] == '-') {
			exponentNegative = text[index] == '-'
			index++
		}
		exponentDigits := 0
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			if exponent <= sourceTechnicalExponentSaturation {
				exponent = exponent*10 + int(text[index]-'0')
			}
			exponentDigits++
			index++
		}
		if exponentDigits == 0 {
			return sourceTechnicalScaled{}, false
		}
		if exponent > sourceTechnicalExponentSaturation {
			exponent = sourceTechnicalExponentSaturation
		}
		if exponentNegative {
			exponent = -exponent
		}
	}
	if index != len(text) {
		return sourceTechnicalScaled{}, false
	}
	significant := strings.TrimLeft(string(digits), "0")
	if significant == "" {
		return sourceTechnicalScaled{digits: "0"}, true
	}
	return sourceTechnicalScaled{negative: negative, digits: significant, scale: exponent - fractionDigits}, true
}

// sourceTechnicalExactInteger returns the exact integer a scaled decimal
// represents, or reports that it is not an integer or cannot fit an int64
// (MaxInt64 has 19 digits). It builds a string no longer than 19 digits, so a
// huge exponent or a long fraction costs no memory.
func sourceTechnicalExactInteger(value sourceTechnicalScaled) (string, bool) {
	if value.digits == "0" {
		return "0", true
	}
	if value.scale >= 0 {
		if len(value.digits)+value.scale > 19 {
			return "", false
		}
		return value.digits + strings.Repeat("0", value.scale), true
	}
	drop := -value.scale
	if drop > len(value.digits) {
		return "", false
	}
	for _, digit := range value.digits[len(value.digits)-drop:] {
		if digit != '0' {
			return "", false
		}
	}
	result := value.digits[:len(value.digits)-drop]
	if result == "" {
		return "", false
	}
	return result, true
}

// sourceTechnicalRoundedInteger returns round(value × 1000) as an exact decimal
// integer, halves away from zero, or reports that it cannot fit an int64. The
// rounding decision uses only the first dropped digit, so a long fraction and a
// huge exponent are handled without a proportional allocation; the value is
// non-negative, so halves round up.
func sourceTechnicalRoundedInteger(value sourceTechnicalScaled) (string, bool) {
	if value.digits == "0" {
		return "0", true
	}
	scale := value.scale + 3 // seconds to milliseconds
	if scale >= 0 {
		if len(value.digits)+scale > 19 {
			return "", false
		}
		return value.digits + strings.Repeat("0", scale), true
	}
	drop := -scale
	if drop > len(value.digits) {
		// The most significant fraction digit is a zero, so the value is below
		// one half and rounds to zero.
		return "0", true
	}
	kept := value.digits[:len(value.digits)-drop]
	if len(kept) > 19 {
		return "", false
	}
	if value.digits[len(value.digits)-drop] < '5' {
		if kept == "" {
			return "0", true
		}
		return kept, true
	}
	if kept == "" {
		return incrementDecimalString("0"), true
	}
	return incrementDecimalString(kept), true
}

// incrementDecimalString adds one to a non-negative decimal string, growing it
// by at most one digit.
func incrementDecimalString(value string) string {
	digits := []byte(value)
	for index := len(digits) - 1; index >= 0; index-- {
		if digits[index] != '9' {
			digits[index]++
			return string(digits)
		}
		digits[index] = '0'
	}
	return "1" + string(digits)
}
