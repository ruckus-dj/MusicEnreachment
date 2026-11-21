// Package tagencoding detects reversible character-encoding damage in saved
// tag values and builds sparse, versioned repair overlays.
//
// The first supported transform repairs text whose original Windows-1251 bytes
// were decoded as ISO-8859-1 ("Latin-1"), for example
// "Punk-O-Ìàíèÿ 2" -> "Punk-O-Мания 2". Detection is purely structural: it
// considers only the supplied saved value and, for AnalyzeGroup, other supplied
// saved values. It never inspects provider candidates, paths, or audio
// evidence.
//
// Detection is intentionally narrow. It does not repair UTF-8 mojibake, other
// legacy encodings, or values containing characters outside ASCII and
// U+00A0..U+00FF. A candidate is never applied automatically, even when group
// corroborated.
//
// Raw tags are never modified. Apply clones the map and changes only the
// explicitly keyed values; Undo reverses an applied overlay.
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
	// ReportVersion is the wire version of Report and GroupReport.
	ReportVersion = 1
	// OverlayVersion is the wire version of Overlay.
	OverlayVersion = 1
	// DetectorVersion identifies the detection rules that produced a value.
	DetectorVersion = 1
	// TransformLatin1Windows1251 identifies a repair of Windows-1251 bytes
	// that were decoded as ISO-8859-1.
	TransformLatin1Windows1251 = "latin1_windows_1251"
)

const (
	// strongRun is the shortest contiguous run of Windows-1251 Cyrillic
	// letters that is safely automatic. A run this long in Latin-1 text is
	// not present in ordinary Western names and words, while shorter runs
	// remain candidates because they can be short words.
	strongRun = 5
	// candidateRun is the shortest run that is reported for explicit
	// confirmation. It is deliberately below strongRun because short values
	// need evidence before they may be applied.
	candidateRun = 3
)

// Status is the detector's disposition for one saved value.
type Status string

const (
	// StatusUnchanged means the value is not recognized as repairable text.
	StatusUnchanged Status = "unchanged"
	// StatusCandidate means the value is plausibly repairable but must not be
	// applied without explicit confirmation.
	StatusCandidate Status = "candidate"
	// StatusAutomatic means the value has a strong, unique signature and may
	// be placed in an automatic overlay.
	StatusAutomatic Status = "automatic"
)

// Decision records why an overlay entry exists.
type Decision string

const (
	DecisionAutomatic          Decision = "automatic"
	DecisionCandidate          Decision = "candidate"
	DecisionConfirmedCandidate Decision = "confirmed_candidate"
	DecisionUndo               Decision = "undo"
)

// Evidence is deterministic, value-local detection evidence. Signature is the
// longest original run that decodes to Cyrillic. GroupMatches counts distinct
// strong automatic signatures elsewhere in the analyzed group that contain both
// the candidate's original and repaired form.
type Evidence struct {
	HighBytes      int    `json:"high_bytes"`
	CyrillicRunes  int    `json:"cyrillic_runes"`
	LongestRun     int    `json:"longest_run"`
	Signature      string `json:"signature,omitempty"`
	GroupMatches   int    `json:"group_matches,omitempty"`
	CaptureAnchors int    `json:"capture_anchors,omitempty"`
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
// Candidates remain pending until explicitly confirmed through Report.Confirm
// or Report.ConfirmCorroborated.
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

// GroupReport contains one Report per supplied saved tag map. Candidates are
// annotated with group corroboration but are never applied automatically.
type GroupReport struct {
	Version int      `json:"version"`
	Files   []Report `json:"files"`
}

// Detect inspects one saved tag value. It recognizes only text composed of
// ASCII and Latin-1 high bytes whose reverse Windows-1251 decoding has a
// sufficiently long contiguous Cyrillic run. Valid Unicode and ordinary Latin-1
// accents are returned unchanged.
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
	strongWord := false
	var runHasVowel, runHasConsonant bool
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
			switch {
			case strings.ContainsRune("аеёиоуыэюя", unicode.ToLower(decoded)):
				runHasVowel = true
			case strings.ContainsRune("бвгджзйклмнпрстфхцчшщ", unicode.ToLower(decoded)):
				runHasConsonant = true
			}
			run++
			strongWord = strongWord || run >= strongRun && runHasVowel && runHasConsonant
			currentRun.WriteRune(original)
			if run > longestRun {
				longestRun = run
				signature = currentRun.String()
			}
		} else {
			run = 0
			runHasVowel, runHasConsonant = false, false
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
	case strongWord && !hasMixedLatinWord(value):
		status, decision = StatusAutomatic, DecisionAutomatic
	case longestRun >= candidateRun && highBytes >= candidateRun,
		longestRun >= 2 && cyrillicRunes >= strongRun:
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

// Mixed ASCII/accented words can be legitimate Western names in an otherwise
// corrupt value. Leave the whole value for explicit review instead of changing
// those names as a side effect of fixing the Cyrillic run.
func hasMixedLatinWord(value string) bool {
	var ascii, high bool
	for _, r := range value {
		if !unicode.IsLetter(r) {
			ascii, high = false, false
			continue
		}
		ascii = ascii || r < utf8.RuneSelf
		high = high || r >= utf8.RuneSelf
		if ascii && high {
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
	// A short value without mixed Latin words can be resolved when two
	// different values from the same capture already prove the byte encoding.
	anchors := make(map[string]struct{})
	for _, entry := range report.Automatic.Entries {
		anchors[entry.Original] = struct{}{}
	}
	if len(anchors) >= 2 {
		pending := report.Candidates[:0]
		for _, candidate := range report.Candidates {
			if !captureSupportsShortCyrillic(candidate) {
				pending = append(pending, candidate)
				continue
			}
			provenance := candidate.Provenance
			provenance.Decision = DecisionAutomatic
			provenance.Evidence.CaptureAnchors = len(anchors)
			report.Automatic.Entries = append(report.Automatic.Entries, Entry{
				Key: candidate.Key, Index: candidate.Index,
				Original: candidate.Original, Replacement: candidate.Replacement,
				Provenance: provenance,
			})
		}
		report.Candidates = pending
	}
	sortEntries(report.Automatic.Entries)
	sortCandidates(report.Candidates)
	return report
}

func captureSupportsShortCyrillic(candidate Candidate) bool {
	if hasMixedLatinWord(candidate.Original) {
		return false
	}
	hasCyrillicVowel := false
	for _, r := range candidate.Replacement {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Cyrillic, r) {
			return false
		}
		hasCyrillicVowel = hasCyrillicVowel || strings.ContainsRune("аеёиоуыэюя", unicode.ToLower(r))
	}
	return hasCyrillicVowel
}

// AnalyzeGroup analyzes each saved tag map and annotates candidates that are
// contained in a unique recognized strong signature from any file in the
// group. Corroborated candidates still require an explicit confirmation call.
func AnalyzeGroup(files ...map[string][]string) GroupReport {
	group := GroupReport{Version: ReportVersion, Files: make([]Report, len(files))}
	for index, tags := range files {
		group.Files[index] = Analyze(tags)
	}

	type signature struct {
		original    string
		replacement string
	}
	strong := make(map[signature]struct{})
	for _, report := range group.Files {
		for _, entry := range report.Automatic.Entries {
			strong[signature{original: entry.Original, replacement: entry.Replacement}] = struct{}{}
		}
	}

	for fileIndex := range group.Files {
		for candidateIndex := range group.Files[fileIndex].Candidates {
			candidate := &group.Files[fileIndex].Candidates[candidateIndex]
			matches := 0
			for strongSignature := range strong {
				if candidate.Original == "" || candidate.Replacement == "" {
					continue
				}
				if strings.Contains(strongSignature.original, candidate.Original) &&
					strings.Contains(strongSignature.replacement, candidate.Replacement) {
					matches++
				}
			}
			candidate.Provenance.Evidence.GroupMatches = matches
		}
	}
	return group
}

// Confirm returns an overlay containing every automatic repair plus the
// explicitly selected candidate indexes. The candidate order is the report's
// deterministic Candidates order.
func (report Report) Confirm(candidates ...int) (Overlay, error) {
	if report.Version != ReportVersion {
		return Overlay{}, fmt.Errorf("unsupported tag report version %d", report.Version)
	}
	overlay := Overlay{
		Version: OverlayVersion,
		Entries: append([]Entry(nil), report.Automatic.Entries...),
	}
	seen := make(map[int]struct{}, len(candidates))
	for _, index := range candidates {
		if index < 0 || index >= len(report.Candidates) {
			return Overlay{}, fmt.Errorf("tag candidate index %d is out of range", index)
		}
		if _, exists := seen[index]; exists {
			return Overlay{}, fmt.Errorf("tag candidate index %d is duplicated", index)
		}
		seen[index] = struct{}{}
		candidate := report.Candidates[index]
		provenance := candidate.Provenance
		provenance.Decision = DecisionConfirmedCandidate
		overlay.Entries = append(overlay.Entries, Entry{
			Key:         candidate.Key,
			Index:       candidate.Index,
			Original:    candidate.Original,
			Replacement: candidate.Replacement,
			Provenance:  provenance,
		})
	}
	sortEntries(overlay.Entries)
	return overlay, nil
}

// ConfirmCorroborated explicitly confirms every candidate that has at least
// one matching strong signature elsewhere in its analysis group. It is a
// no-op for an ungrouped Report, whose candidates have no group matches.
func (report Report) ConfirmCorroborated() (Overlay, error) {
	indexes := make([]int, 0)
	for index, candidate := range report.Candidates {
		if candidate.Provenance.Evidence.GroupMatches > 0 {
			indexes = append(indexes, index)
		}
	}
	return report.Confirm(indexes...)
}

// ConfirmCorroborated explicitly confirms corroborated candidates in every
// file. The returned overlays correspond one-to-one with group.Files.
func (group GroupReport) ConfirmCorroborated() ([]Overlay, error) {
	overlays := make([]Overlay, len(group.Files))
	for index, report := range group.Files {
		overlay, err := report.ConfirmCorroborated()
		if err != nil {
			return nil, err
		}
		overlays[index] = overlay
	}
	return overlays, nil
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

// Undo reverses an applied overlay. It validates that the current values match
// the overlay's replacements before restoring the recorded originals.
func (overlay Overlay) Undo(tags map[string][]string) (map[string][]string, error) {
	return overlay.Reverse().Apply(tags)
}

// Reverse returns the inverse overlay. The inverse keeps the same stable
// key/index locations and detector evidence, but marks each entry as an undo.
func (overlay Overlay) Reverse() Overlay {
	reversed := Overlay{
		Version: overlay.Version,
		Entries: make([]Entry, len(overlay.Entries)),
	}
	for index, entry := range overlay.Entries {
		entry.Original, entry.Replacement = entry.Replacement, entry.Original
		entry.Provenance.Decision = DecisionUndo
		reversed.Entries[index] = entry
	}
	return reversed
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
