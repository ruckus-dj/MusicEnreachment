// Package tagencoding detects reversible character-encoding damage in saved
// tag values and builds sparse, versioned repair overlays.
//
// The first supported transform repairs text whose original Windows-1251 bytes
// were decoded as ISO-8859-1 ("Latin-1"), for example
// "Punk-O-Ìàíèÿ 2" -> "Punk-O-Мания 2". Detection is purely structural: it
// considers only the supplied saved value. It never inspects provider
// candidates, paths, or audio evidence.
//
// Detection is intentionally narrow. It does not repair UTF-8 mojibake, other
// legacy encodings, or values containing characters outside ASCII and
// U+00A0..U+00FF. Ambiguous candidates are never applied automatically.
//
// Raw tags are never modified. Apply clones the map and changes only the
// explicitly keyed values.
package tagencoding

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

const (
	// ReportVersion is the wire version of Report.
	ReportVersion = 1
	// OverlayVersion is the wire version of Overlay.
	OverlayVersion = 1
	// DetectorVersion identifies the detection rules that produced a value.
	DetectorVersion = 3
	// TransformLatin1Windows1251 identifies a repair of Windows-1251 bytes
	// that were decoded as ISO-8859-1.
	TransformLatin1Windows1251 = "latin1_windows_1251"
)

const (
	automaticRun = 3
	// Several shorter runs can suggest a repair but still require confirmation.
	candidateCyrillicRunes = 5
)

// Status is the detector's disposition for one saved value.
type Status string

const (
	// StatusUnchanged means the value is not recognized as repairable text.
	StatusUnchanged Status = "unchanged"
	// StatusCandidate means the value is plausibly repairable but must not be
	// applied without explicit confirmation.
	StatusCandidate Status = "candidate"
	// StatusAutomatic means the value meets the rule for an automatic overlay.
	StatusAutomatic Status = "automatic"
)

// Decision records why an overlay entry exists.
type Decision string

const (
	DecisionAutomatic          Decision = "automatic"
	DecisionCandidate          Decision = "candidate"
	DecisionConfirmedCandidate Decision = "confirmed_candidate"
)

// Evidence is deterministic, value-local detection evidence. Signature is the
// longest original run that decodes to Cyrillic.
type Evidence struct {
	HighBytes     int    `json:"high_bytes"`
	CyrillicRunes int    `json:"cyrillic_runes"`
	LongestRun    int    `json:"longest_run"`
	Signature     string `json:"signature,omitempty"`
}

// Provenance describes the fixed transform and detector version behind an
// entry. It never describes a provider, path, or inferred source identity.
type Provenance struct {
	Transform       string   `json:"transform"`
	DetectorVersion int      `json:"detector_version"`
	Decision        Decision `json:"decision"`
	Evidence        Evidence `json:"evidence"`
}

// Detection is the result of inspecting one saved value. Replacement equals
// Original when Status is StatusUnchanged.
type Detection struct {
	Status      Status     `json:"status"`
	Original    string     `json:"original"`
	Replacement string     `json:"replacement"`
	Provenance  Provenance `json:"provenance"`
}

// Entry is one sparse replacement at a stable key and array index. It
// intentionally does not replace the whole tag array.
type Entry struct {
	Key         string     `json:"key"`
	Index       int        `json:"index"`
	Original    string     `json:"original"`
	Replacement string     `json:"replacement"`
	Provenance  Provenance `json:"provenance"`
}

// Overlay is a versioned sparse set of replacements. Apply validates every
// Original before changing anything.
type Overlay struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Candidate is an ambiguous repair that is not part of the automatic overlay.
// Candidates are reported separately for explicit operator review.
type Candidate struct {
	Key         string     `json:"key"`
	Index       int        `json:"index"`
	Original    string     `json:"original"`
	Replacement string     `json:"replacement"`
	Provenance  Provenance `json:"provenance"`
}

// Report is the deterministic detection result for one saved tag map.
type Report struct {
	Version      int         `json:"version"`
	Automatic    Overlay     `json:"automatic"`
	Candidates   []Candidate `json:"candidates"`
	UserRevision string      `json:"user_revision,omitempty"`
}

// Detect inspects one saved tag value. It recognizes only text composed of
// ASCII and Latin-1 high bytes. A reversible repair with three consecutive
// Cyrillic letters can be automatic, even next to ASCII letters. A change to
// an unrelated symbol or a non-Cyrillic glyph still requires confirmation.
func Detect(value string) Detection {
	unchanged := Detection{Status: StatusUnchanged, Original: value, Replacement: value}
	if value == "" || !utf8.ValidString(value) {
		return unchanged
	}

	encoded := make([]byte, 0, len(value))
	highBytes := 0
	for _, r := range value {
		switch {
		case r < utf8.RuneSelf:
			encoded = append(encoded, byte(r))
		case r >= 0xA0 && r <= 0xFF:
			encoded = append(encoded, byte(r))
			highBytes++
		default:
			// Already proper Unicode, a control character, or a character
			// outside the reversible Latin-1 transport.
			return unchanged
		}
	}
	if highBytes == 0 {
		return unchanged
	}

	var repaired strings.Builder
	var currentRun strings.Builder
	signature := ""
	longestRun := 0
	run := 0
	cyrillicRunes := 0
	for _, original := range value {
		decoded := original
		if original >= 0xA0 {
			decoded = charmap.Windows1251.DecodeByte(byte(original))
		}
		if decoded == utf8.RuneError {
			return unchanged
		}
		repaired.WriteRune(decoded)
		if unicode.Is(unicode.Cyrillic, decoded) {
			cyrillicRunes++
			run++
			currentRun.WriteRune(original)
			if run > longestRun {
				longestRun = run
				signature = currentRun.String()
			}
		} else {
			run = 0
			currentRun.Reset()
		}
	}

	replacement := repaired.String()
	roundTrip := make([]byte, 0, len(encoded))
	for _, r := range replacement {
		b, ok := charmap.Windows1251.EncodeRune(r)
		if !ok {
			return unchanged
		}
		roundTrip = append(roundTrip, b)
	}
	if !bytes.Equal(roundTrip, encoded) {
		return unchanged
	}

	evidence := Evidence{
		HighBytes:     highBytes,
		CyrillicRunes: cyrillicRunes,
		LongestRun:    longestRun,
		Signature:     signature,
	}
	var status Status
	var decision Decision
	switch {
	case longestRun >= automaticRun && !hasAmbiguousCollateral(value):
		status, decision = StatusAutomatic, DecisionAutomatic
	case longestRun >= automaticRun, longestRun >= 2 && cyrillicRunes >= candidateCyrillicRunes:
		status, decision = StatusCandidate, DecisionCandidate
	default:
		return unchanged
	}

	return Detection{
		Status:      status,
		Original:    value,
		Replacement: replacement,
		Provenance: Provenance{
			Transform:       TransformLatin1Windows1251,
			DetectorVersion: DetectorVersion,
			Decision:        decision,
			Evidence:        evidence,
		},
	}
}

// hasAmbiguousCollateral reports whether the repair would change a character
// that is not part of a credible Cyrillic run. A Latin-1 symbol such as an
// isolated multiplication sign ("Ìàíèÿ × 2") becomes a Cyrillic letter, and a
// changed non-Cyrillic glyph is outside the intended transform; either change
// must be confirmed explicitly. Symbols adjacent to decoded Cyrillic letters
// ("×àðäûø") remain ordinary mojibake of that name.
func hasAmbiguousCollateral(value string) bool {
	originals := []rune(value)
	decoded := make([]rune, len(originals))
	cyrillic := make([]bool, len(originals))
	for index, original := range originals {
		if original < 0xA0 {
			decoded[index] = original
			continue
		}
		decoded[index] = charmap.Windows1251.DecodeByte(byte(original))
		cyrillic[index] = unicode.Is(unicode.Cyrillic, decoded[index])
	}
	for index, original := range originals {
		if original < 0xA0 || decoded[index] == original {
			continue
		}
		if !cyrillic[index] {
			return true
		}
		if unicode.IsLetter(original) {
			continue
		}
		if original == '×' && ((index > 0 && unicode.IsDigit(originals[index-1])) ||
			(index+1 < len(originals) && unicode.IsDigit(originals[index+1]))) {
			return true
		}
		joined := index > 0 && cyrillic[index-1] ||
			index+1 < len(originals) && cyrillic[index+1]
		if !joined {
			return true
		}
	}
	return false
}

// Analyze inspects every value in tags without changing the map. Automatic
// repairs are placed in the report's overlay; ambiguous repairs remain
// candidates. Keys and array order are preserved by the resulting entries.
func Analyze(tags map[string][]string) Report {
	report := Report{Version: ReportVersion, Automatic: Overlay{Version: OverlayVersion}}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for index, value := range tags[key] {
			detection := Detect(value)
			switch detection.Status {
			case StatusAutomatic:
				report.Automatic.Entries = append(report.Automatic.Entries, Entry{
					Key:         key,
					Index:       index,
					Original:    detection.Original,
					Replacement: detection.Replacement,
					Provenance:  detection.Provenance,
				})
			case StatusCandidate:
				report.Candidates = append(report.Candidates, Candidate{
					Key:         key,
					Index:       index,
					Original:    detection.Original,
					Replacement: detection.Replacement,
					Provenance:  detection.Provenance,
				})
			}
		}
	}
	sortEntries(report.Automatic.Entries)
	sortCandidates(report.Candidates)
	return report
}

// Apply returns a cloned tag map with the overlay's sparse replacements. Every
// entry must match its recorded Original exactly; a conflict returns an error
// and leaves the input untouched.
func (overlay Overlay) Apply(tags map[string][]string) (map[string][]string, error) {
	if overlay.Version != OverlayVersion {
		return nil, fmt.Errorf("unsupported tag overlay version %d", overlay.Version)
	}
	result := cloneTags(tags)
	seen := make(map[entryKey]struct{}, len(overlay.Entries))
	for _, entry := range overlay.Entries {
		values, exists := result[entry.Key]
		if !exists {
			return nil, fmt.Errorf("tag overlay key %q is not present", entry.Key)
		}
		if entry.Index < 0 || entry.Index >= len(values) {
			return nil, fmt.Errorf("tag overlay index %d for key %q is out of range", entry.Index, entry.Key)
		}
		key := entryKey{key: entry.Key, index: entry.Index}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("tag overlay entry %q[%d] is duplicated", entry.Key, entry.Index)
		}
		seen[key] = struct{}{}
		if values[entry.Index] != entry.Original {
			return nil, fmt.Errorf(
				"tag overlay conflict at %q[%d]: expected %q, found %q",
				entry.Key, entry.Index, entry.Original, values[entry.Index],
			)
		}
		values[entry.Index] = entry.Replacement
	}
	return result, nil
}

type entryKey struct {
	key   string
	index int
}

func cloneTags(tags map[string][]string) map[string][]string {
	if tags == nil {
		return nil
	}
	result := make(map[string][]string, len(tags))
	for key, values := range tags {
		if values == nil {
			result[key] = nil
			continue
		}
		result[key] = make([]string, len(values))
		copy(result[key], values)
	}
	return result
}

func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].Key != entries[right].Key {
			return entries[left].Key < entries[right].Key
		}
		return entries[left].Index < entries[right].Index
	})
}

func sortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].Key != candidates[right].Key {
			return candidates[left].Key < candidates[right].Key
		}
		return candidates[left].Index < candidates[right].Index
	})
}
