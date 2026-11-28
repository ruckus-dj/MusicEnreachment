package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/tagencoding"
)

// MatchingSearchKeys identify the exact cached search responses associated with
// one source file. Scores are never borrowed from another file or candidate.
type MatchingSearchKeys struct {
	Release   string `json:"release"`
	Recording string `json:"recording"`
}

// MatchingAcoustIDKey scopes a fingerprint result to the exact candidate
// recording that the provider matched.
type MatchingAcoustIDKey struct {
	FileID      uuid.UUID
	RecordingID string
}

// MatchingInput is one file/release/recording edge projected from saved
// evidence. It contains no selected assignment or filesystem-derived data.
type MatchingInput struct {
	FileID               uuid.UUID
	CaptureAnalysisID    uuid.UUID
	CaptureIdentity      string
	MetadataWinner       *uuid.UUID
	ProbeWinner          *uuid.UUID
	ProbeResult          []byte
	SourceSHA256         []byte
	Fingerprint          *string
	FingerprintWinner    *uuid.UUID
	Diagnostics          []IncomingGroupingDiagnostic
	SourceReleaseMBIDs   []string
	SourceRecordingMBIDs []string
	ReleaseID            string
	RecordingID          string
	Medium               int
	Track                int
	ReleaseScoreInput    MatchingScoreInput
	ReleaseScore         MatchingScore
	AssignmentInput      MatchingScoreInput
	AssignmentScore      MatchingScore
}

// BuildMatchingInputs maps immutable saved captures and a provider snapshot to
// scoring edges. Search keys are scoped by file; AcoustID scores are scoped by
// file and recording, and optional recording artist projections by recording ID.
func BuildMatchingInputs(
	captures []persistence.IncomingGroupingCapture,
	snapshot persistence.ProviderMatchingSnapshot,
	searchKeys map[uuid.UUID]MatchingSearchKeys,
	acoustIDScores map[MatchingAcoustIDKey]float64,
	recordingArtistsOptions ...map[string][]string,
) ([]MatchingInput, error) {
	if len(recordingArtistsOptions) > 1 {
		return nil, fmt.Errorf("at most one recording artist projection is allowed")
	}
	var recordingArtists map[string][]string
	if len(recordingArtistsOptions) == 1 {
		recordingArtists = recordingArtistsOptions[0]
	}
	return buildMatchingInputs(captures, snapshot, matchingScoringOptions{
		searchKeys: searchKeys, acoustIDScores: acoustIDScores, recordingArtists: recordingArtists,
	})
}

type matchingScoringOptions struct {
	searchKeys       map[uuid.UUID]MatchingSearchKeys
	acoustIDScores   map[MatchingAcoustIDKey]float64
	recordingArtists map[string][]string
	layoutPolicy     bool
}

// BuildLayoutMatchingInputs scores two-stage layout pairs from frozen inputs.
// A compilation's group credit is not a comparison with a song's performer.
func BuildLayoutMatchingInputs(
	captures []persistence.IncomingGroupingCapture,
	snapshot persistence.ProviderMatchingSnapshot,
	searchKeys map[uuid.UUID]MatchingSearchKeys,
	acoustIDScores map[MatchingAcoustIDKey]float64,
) ([]MatchingInput, error) {
	return buildMatchingInputs(captures, snapshot, matchingScoringOptions{
		searchKeys: searchKeys, acoustIDScores: acoustIDScores, layoutPolicy: true,
	})
}

func buildMatchingInputs(
	captures []persistence.IncomingGroupingCapture,
	snapshot persistence.ProviderMatchingSnapshot,
	options matchingScoringOptions,
) ([]MatchingInput, error) {
	view, err := incomingFilesFromCaptures(captures)
	if err != nil {
		return nil, err
	}
	inputs := make([]MatchingInput, 0)
	for _, file := range view.Files {
		capture, err := matchingCaptureForFile(captures, file.VariantID)
		if err != nil {
			return nil, err
		}
		tags := file.Tags
		if options.layoutPolicy {
			tags, err = matchingLayoutTags(tags, capture.TagEncoding)
			if err != nil {
				return nil, err
			}
		}
		fields := matchingSourceFields(tags)
		technical, err := ParseSourceTechnicalAnalysis(capture.ProbeResult)
		if err != nil && !isEmptyProbe(capture.ProbeResult) {
			return nil, fmt.Errorf("parse saved source technical analysis: %w", err)
		}
		var duration *float64
		if technical.Container.DurationMS != nil {
			value := float64(*technical.Container.DurationMS) / 1000
			duration = &value
		}
		releaseMBID, recordingMBID := matchingSourceMBIDs(fields["release_mbid"]), matchingSourceMBIDs(fields["recording_mbid"])
		mbidDiagnostics := matchingMBIDDiagnostics(file.VariantID, fields)
		keys := options.searchKeys[file.VariantID]
		for _, release := range snapshot.Releases {
			candidateArtists := make([]string, 0, len(release.Credits))
			for _, credit := range release.Credits {
				candidateArtists = append(candidateArtists, credit.Name)
			}
			variousArtists := options.layoutPolicy && len(candidateArtists) == 1 &&
				strings.EqualFold(strings.TrimSpace(candidateArtists[0]), "Various Artists")
			tracksPerMedium := make(map[int]int)
			mediumCount := make(map[int]struct{})
			for _, candidateTrack := range release.Tracks {
				tracksPerMedium[candidateTrack.Medium]++
				mediumCount[candidateTrack.Medium] = struct{}{}
			}
			releaseScore := matchingSnapshotSearchScore(snapshot, keys.Release, release.ProviderKey)
			for _, track := range release.Tracks {
				var releaseDuration *float64
				if track.ReleaseLengthMS != nil {
					value := float64(*track.ReleaseLengthMS) / 1000
					releaseDuration = &value
				}
				releaseInput := MatchingScoreInput{
					Profile:                        ReleaseMatchingProfile,
					ReleaseArtist:                  MatchingTextPair{Source: fields["album_artist"], Candidate: candidateArtists, Provenance: "saved metadata tags / provider release credits"},
					ReleaseTitle:                   MatchingTextPair{Source: fields["album"], Candidate: []string{release.Name}, Provenance: "saved metadata tags / provider release title"},
					ReleaseExpectedDurationSeconds: cloneFloat(duration), ReleaseActualDurationSeconds: releaseDuration, ReleaseMusicBrainzScore: releaseScore,
					TrackNumber: parseMatchingPosition(fields["track_number"]), DiscNumber: parseMatchingPosition(fields["disc_number"]),
					TrackTotal: parseMatchingPosition(fields["track_total"]), DiscTotal: parseMatchingPosition(fields["disc_total"]),
					CandidateTrack: intPointer(track.Position + 1), CandidateDisc: intPointer(track.Medium + 1),
					CandidateTrackTotal: matchingCompleteTotal(release.Complete, tracksPerMedium[track.Medium]), CandidateDiscTotal: matchingCompleteTotal(release.Complete, len(mediumCount)),
					Evidence: MatchingEvidence{ReleaseMBID: releaseMBID},
				}
				releaseResult, err := ScoreMatching(releaseInput)
				if err != nil {
					return nil, fmt.Errorf("score release candidate: %w", err)
				}
				trackScore := matchingSnapshotSearchScore(snapshot, keys.Recording, track.RecordingProviderKey)
				var acoustID *float64
				if score, ok := options.acoustIDScores[MatchingAcoustIDKey{FileID: file.VariantID, RecordingID: track.RecordingProviderKey}]; ok {
					value := score
					acoustID = &value
				}
				var recordingCreditNames []string
				if track.TrackArtists != nil {
					recordingCreditNames = *track.TrackArtists
				}
				if recordingCreditNames == nil {
					recordingCreditNames = track.RecordingArtists
				}
				if recordingCreditNames == nil {
					recordingCreditNames = options.recordingArtists[track.RecordingProviderKey]
				}
				recordingTitle := track.TrackTitle
				if recordingTitle == "" {
					recordingTitle = track.RecordingTitle
				}
				recordingLengthMS := track.TrackLengthMS
				if recordingLengthMS == nil {
					recordingLengthMS = track.RecordingLengthMS
				}
				var recordingDuration *float64
				if recordingLengthMS != nil {
					value := float64(*recordingLengthMS) / 1000
					recordingDuration = &value
				}
				assignmentInput := MatchingScoreInput{
					Profile:                        RecordingReleaseMatchingProfile,
					ReleaseArtist:                  MatchingTextPair{Source: fields["album_artist"], Candidate: candidateArtists, Provenance: "saved metadata tags / provider release credits"},
					ReleaseTitle:                   MatchingTextPair{Source: fields["album"], Candidate: []string{release.Name}, Provenance: "saved metadata tags / provider release title"},
					RecordingArtist:                MatchingTextPair{Source: fields["artist"], Candidate: recordingCreditNames, Provenance: "saved metadata tags / provider recording credits"},
					RecordingTitle:                 MatchingTextPair{Source: fields["title"], Candidate: []string{recordingTitle}, Provenance: "saved metadata tags / provider track title"},
					ReleaseExpectedDurationSeconds: cloneFloat(duration), ReleaseActualDurationSeconds: cloneFloat(releaseDuration), ReleaseMusicBrainzScore: releaseScore,
					RecordingExpectedDurationSeconds: cloneFloat(duration), RecordingActualDurationSeconds: recordingDuration,
					RecordingMusicBrainzScore: trackScore, AcoustIDScore: acoustID,
					TrackNumber: parseMatchingPosition(fields["track_number"]), DiscNumber: parseMatchingPosition(fields["disc_number"]),
					TrackTotal: parseMatchingPosition(fields["track_total"]), DiscTotal: parseMatchingPosition(fields["disc_total"]),
					CandidateTrack: intPointer(track.Position + 1), CandidateDisc: intPointer(track.Medium + 1),
					CandidateTrackTotal: matchingCompleteTotal(release.Complete, tracksPerMedium[track.Medium]), CandidateDiscTotal: matchingCompleteTotal(release.Complete, len(mediumCount)),
					Evidence: MatchingEvidence{ReleaseMBID: releaseMBID, RecordingMBID: recordingMBID, TrackMBID: track.RecordingProviderKey},
				}
				if variousArtists {
					assignmentInput.ReleaseArtist = MatchingTextPair{
						Provenance: "compilation credit is not compared with the source performer",
					}
				}
				assignmentResult, err := ScoreAssignmentCandidate(assignmentInput)
				if err != nil {
					return nil, fmt.Errorf("score assignment candidate: %w", err)
				}
				inputs = append(inputs, MatchingInput{
					FileID: file.VariantID, CaptureAnalysisID: file.CaptureAnalysisID, CaptureIdentity: file.CaptureIdentity,
					MetadataWinner: cloneUUID(capture.MetadataWinner), ProbeWinner: cloneUUID(capture.ProbeWinner),
					ProbeResult:  append([]byte(nil), capture.ProbeResult...),
					SourceSHA256: append([]byte(nil), capture.SourceSHA256...), Fingerprint: cloneString(capture.Fingerprint),
					FingerprintWinner:    cloneUUID(capture.FingerprintWinner),
					Diagnostics:          append([]IncomingGroupingDiagnostic(nil), mbidDiagnostics...),
					SourceReleaseMBIDs:   append([]string(nil), matchingSourceMBIDList(fields["release_mbid"])...),
					SourceRecordingMBIDs: append([]string(nil), matchingSourceMBIDList(fields["recording_mbid"])...),
					ReleaseID:            release.ProviderKey, RecordingID: track.RecordingProviderKey,
					Medium: track.Medium, Track: track.Position,
					ReleaseScoreInput: releaseInput, ReleaseScore: releaseResult,
					AssignmentInput: assignmentInput, AssignmentScore: assignmentResult,
				})
			}
		}
	}
	return inputs, nil
}

func matchingCompleteTotal(complete bool, total int) *int {
	if !complete || total < 1 {
		return nil
	}
	return intPointer(total)
}

// matchingLayoutTags uses only the saved overlay. Unlike the display path,
// automatic calculation must not silently drop a mismatched or invalid report.
func matchingLayoutTags(tags map[string][]string, saved json.RawMessage) (map[string][]string, error) {
	if len(saved) == 0 {
		return tags, nil
	}
	var report tagencoding.Report
	if err := json.Unmarshal(saved, &report); err != nil {
		return nil, fmt.Errorf("decode saved matching tag corrections: %w", err)
	}
	if report.Version != tagencoding.ReportVersion {
		return nil, fmt.Errorf("unsupported matching tag correction version %d", report.Version)
	}
	if len(report.Automatic.Entries) == 0 {
		return tags, nil
	}
	effective, err := report.Automatic.Apply(tags)
	if err != nil {
		return nil, fmt.Errorf("apply saved matching tag corrections: %w", err)
	}
	return effective, nil
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func matchingCaptureForFile(captures []persistence.IncomingGroupingCapture, id uuid.UUID) (persistence.IncomingGroupingCapture, error) {
	var selected *persistence.IncomingGroupingCapture
	fingerprintConflict := false
	for _, capture := range captures {
		if capture.VariantID != id {
			continue
		}
		if selected == nil {
			copy := capture
			selected = &copy
			continue
		}
		if !bytes.Equal(selected.ProbeResult, capture.ProbeResult) || !bytes.Equal(selected.SourceSHA256, capture.SourceSHA256) ||
			!reflect.DeepEqual(selected.MetadataWinner, capture.MetadataWinner) || !reflect.DeepEqual(selected.ProbeWinner, capture.ProbeWinner) ||
			!reflect.DeepEqual(selected.CaptureAnalysisID, capture.CaptureAnalysisID) {
			return persistence.IncomingGroupingCapture{}, fmt.Errorf("incoming variant has conflicting saved matching evidence")
		}
		if !persistence.SameAcoustIDFingerprintIdentity(*selected, capture) {
			fingerprintConflict = true
		}
	}
	if selected == nil {
		return persistence.IncomingGroupingCapture{}, fmt.Errorf("incoming variant has no saved matching capture")
	}
	if fingerprintConflict {
		selected.Fingerprint = nil
		selected.FingerprintResultID = nil
		selected.FingerprintWinner = nil
		selected.FingerprintDuration = nil
		selected.FingerprintVersion = nil
		selected.FingerprintBanner = nil
		selected.FingerprintAlgorithmNamespace = nil
		selected.FingerprintAlgorithmID = nil
		selected.FingerprintCalculatedAt = nil
		selected.FingerprintParserVersion = nil
	}
	return *selected, nil
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func isEmptyProbe(raw []byte) bool { return len(raw) == 0 || string(raw) == "{}" }

func matchingSnapshotSearchScore(snapshot persistence.ProviderMatchingSnapshot, queryKey, candidateID string) *float64 {
	if queryKey == "" || candidateID == "" {
		return nil
	}
	score, ok := snapshot.SearchScores[queryKey][candidateID]
	if !ok || !score.Present || math.IsNaN(score.Value) || math.IsInf(score.Value, 0) {
		return nil
	}
	value := score.Value
	return &value
}

// matchingSourceFields maps source metadata aliases without splitting or
// deduplicating observed arrays. Comparison owns stable duplicate handling.
func matchingSourceFields(tags map[string][]string) map[string][]string {
	fields := make(map[string][]string)
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := tags[key]
		canonical := strings.ToUpper(strings.TrimSpace(key))
		var target string
		switch canonical {
		case "ALBUM":
			target = "album"
		case "TITLE", "TRACKTITLE":
			target = "title"
		case "ARTIST":
			target = "artist"
		case "ALBUMARTIST", "ALBUM ARTIST", "ALBUM_ARTIST":
			target = "album_artist"
		case "TRACK", "TRACKNUMBER", "TRACK NUMBER", "TRACK_NUMBER":
			target = "track_number"
		case "TRACKTOTAL", "TOTALTRACKS", "TRACK TOTAL", "TRACK_TOTAL":
			target = "track_total"
		case "DISC", "DISCNUMBER", "DISC NUMBER", "DISC_NUMBER", "PARTNUMBER":
			target = "disc_number"
		case "DISCTOTAL", "TOTALDISCS", "DISC TOTAL", "DISC_TOTAL":
			target = "disc_total"
		case "MUSICBRAINZ_ALBUMID", "MUSICBRAINZ ALBUM ID", "MUSICBRAINZ_RELEASEID", "MUSICBRAINZ_RELEASE_ID", "RELEASEMBID":
			target = "release_mbid"
		case "MUSICBRAINZ_TRACKID", "MUSICBRAINZ RECORDING ID", "MUSICBRAINZ_RECORDINGID", "MUSICBRAINZ_RECORDING_ID":
			target = "recording_mbid"
		default:
			continue
		}
		fields[target] = append(fields[target], values...)
	}
	if !matchingHasUsableText(fields["album_artist"]) {
		fields["album_artist"] = append(fields["album_artist"], fields["artist"]...)
	}
	return fields
}

func matchingHasUsableText(values []string) bool {
	for _, value := range values {
		if hasNonWhitespaceRune(value) {
			return true
		}
	}
	return false
}

func matchingSourceMBIDs(values []string) string {
	valid, _ := incomingMBIDs(values)
	if len(valid) == 1 {
		return valid[0]
	}
	return ""
}

func matchingSourceMBIDList(values []string) []string {
	valid, _ := incomingMBIDs(values)
	return valid
}

func matchingMBIDDiagnostics(fileID uuid.UUID, fields map[string][]string) []IncomingGroupingDiagnostic {
	diagnostics := make([]IncomingGroupingDiagnostic, 0, 4)
	for _, field := range []struct {
		name string
		code string
	}{{"release_mbid", "release"}, {"recording_mbid", "recording"}} {
		valid, invalid := incomingMBIDs(fields[field.name])
		if len(invalid) != 0 {
			diagnostics = append(diagnostics, IncomingGroupingDiagnostic{VariantID: fileID, Code: "invalid_" + field.code + "_mbid", Values: append([]string(nil), invalid...)})
		}
		if len(valid) > 1 {
			diagnostics = append(diagnostics, IncomingGroupingDiagnostic{VariantID: fileID, Code: "multiple_" + field.code + "_mbids", Values: append([]string(nil), valid...)})
		}
	}
	return diagnostics
}

func intPointer(value int) *int { return &value }

func parseMatchingPosition(values []string) *int {
	for _, value := range values {
		// Keep the observed tag atomic. In particular, do not infer a position
		// from compound text such as "2/12" without an explicit typed mapping.
		number, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && number > 0 {
			return &number
		}
	}
	return nil
}
