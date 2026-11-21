package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	appsettings "github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

const matchingLayoutTargetEvidenceVersion = 1

func requireMatchingLayoutEvidenceReadCommitted(ctx context.Context, tx bun.Tx) error {
	if tx.Tx == nil {
		return errors.New("matching layout evidence transaction is unavailable")
	}
	var isolation string
	if err := tx.NewRaw("SHOW transaction_isolation").Scan(ctx, &isolation); err != nil {
		return fmt.Errorf("read matching layout evidence transaction isolation: %w", err)
	}
	if isolation != "read committed" {
		return errors.New("matching layout evidence requires read committed isolation")
	}
	return nil
}

// matchingLayoutTargetEvidence freezes one automatic layout target. TagEncoding
// is copied explicitly because IncomingGroupingCapture excludes it from JSON;
// otherwise the saved tag corrections would be absent from the canonical target
// evidence. The capture list is ordered by location identity.
type matchingLayoutTargetEvidence struct {
	SchemaVersion    int                           `json:"schema_version"`
	Target           automaticArtistTargetCapture  `json:"target"`
	Captures         []matchingLayoutTargetCapture `json:"captures"`
	GroupingRevision int64                         `json:"grouping_revision"`
	PairThreshold    string                        `json:"pair_threshold"`
	PolicyVersion    string                        `json:"policy_version"`
}

type matchingLayoutTargetCapture struct {
	LocationID  uuid.UUID       `json:"location_id"`
	TagEncoding json.RawMessage `json:"tag_encoding"`
}

// CaptureMatchingLayoutTargetTx freezes one automatic group for layout
// calculation and returns its canonical evidence, ready captures, and effective
// pair threshold.
func CaptureMatchingLayoutTargetTx(ctx context.Context, tx bun.Tx, groupID uuid.UUID, policyVersion string) (json.RawMessage, []IncomingGroupingCapture, float64, error) {
	evidence, captures, err := lockAndCaptureMatchingLayoutTarget(ctx, tx, groupID, policyVersion)
	if err != nil {
		return nil, nil, 0, err
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("encode matching layout target: %w", err)
	}
	canonical, err := canonicalMatchingJSON(encoded)
	if err != nil {
		return nil, nil, 0, err
	}
	threshold, err := parseMatchingLayoutPairThreshold(evidence.PairThreshold)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("decode captured matching pair threshold: %w", err)
	}
	return canonical, captures, threshold, nil
}

// lockAndCaptureMatchingLayoutTarget captures a current automatic grouping
// target under the canonical source-mutation and settings locks, then the
// grouping, group, and authority locks. It passes only captures belonging to
// READY members to the caller while retaining every member in the evidence.
//
// The caller owns any output-admission gate required by its surrounding work;
// this helper deliberately never acquires one, so it cannot invert that gate's
// order. The shared tools gate prevents settings writers from changing the
// threshold, including inserting its first row.
func lockAndCaptureMatchingLayoutTarget(ctx context.Context, tx bun.Tx, groupID uuid.UUID, policyVersion string) (matchingLayoutTargetEvidence, []IncomingGroupingCapture, error) {
	var evidence matchingLayoutTargetEvidence
	if groupID == uuid.Nil || strings.TrimSpace(policyVersion) == "" {
		return evidence, nil, fmt.Errorf("lock matching layout target: group and policy version are required")
	}
	if err := requireMatchingLayoutEvidenceReadCommitted(ctx, tx); err != nil {
		return evidence, nil, err
	}
	if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
		return evidence, nil, fmt.Errorf("lock matching layout target: lock source mutation gate: %w", err)
	}
	if err := lockToolsMoveGateShared(ctx, tx); err != nil {
		return evidence, nil, fmt.Errorf("lock matching layout target: lock settings gate: %w", err)
	}
	pairThreshold, err := matchingLayoutTargetPairThreshold(ctx, tx)
	if err != nil {
		return evidence, nil, err
	}
	if err := lockMatchingLayoutTargetRoots(ctx, tx, groupID); err != nil {
		return evidence, nil, err
	}
	var grouping struct {
		Revision     int64 `bun:"revision"`
		NeedsRefresh bool  `bun:"needs_refresh"`
	}
	if err := tx.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true FOR SHARE`).
		Scan(ctx, &grouping.Revision, &grouping.NeedsRefresh); err != nil {
		return evidence, nil, fmt.Errorf("lock matching layout target: read grouping state: %w", err)
	}
	if grouping.NeedsRefresh {
		return evidence, nil, fmt.Errorf("lock matching layout target: grouping is not current: %w", ErrMatchingTargetNeedsRefresh)
	}
	var group struct {
		Revision string `bun:"revision"`
	}
	if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? FOR SHARE`, groupID).
		Scan(ctx, &group.Revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return evidence, nil, fmt.Errorf("lock matching layout target: group is not current: %w", ErrIncomingMatchConflict)
		}
		return evidence, nil, fmt.Errorf("lock matching layout target: read group: %w", err)
	}
	authorityRevision, err := requireAutomaticMatchingAuthority(ctx, tx, groupID)
	if err != nil {
		return evidence, nil, fmt.Errorf("lock matching layout target: require automatic authority: %w", err)
	}
	target, err := captureAutomaticArtistTarget(ctx, tx, groupID, group.Revision, authorityRevision)
	if err != nil {
		return evidence, nil, err
	}
	return buildMatchingLayoutTargetEvidence(target, grouping.Revision, pairThreshold, policyVersion)
}

// Scan admission can mark a root unavailable without the source mutation
// gate. Lock only roots in this target before capturing their status.
func lockMatchingLayoutTargetRoots(ctx context.Context, tx bun.Tx, groupID uuid.UUID) error {
	var rootIDs []uuid.UUID
	if err := tx.NewRaw(`SELECT id FROM source_root WHERE id IN (
		SELECT source_root_id FROM incoming_group_member_location WHERE group_id=?
	) ORDER BY id FOR SHARE`, groupID).Scan(ctx, &rootIDs); err != nil {
		return fmt.Errorf("lock matching layout target: lock source roots: %w", err)
	}
	return nil
}

func buildMatchingLayoutTargetEvidence(target automaticArtistTargetCapture, groupingRevision int64, pairThreshold, policyVersion string) (matchingLayoutTargetEvidence, []IncomingGroupingCapture, error) {
	readyCaptures, err := matchingLayoutTargetReadyCaptures(target)
	if err != nil {
		return matchingLayoutTargetEvidence{}, nil, err
	}
	return matchingLayoutTargetEvidence{
		SchemaVersion:    matchingLayoutTargetEvidenceVersion,
		Target:           target,
		Captures:         matchingLayoutTargetTagEncodings(target),
		GroupingRevision: groupingRevision,
		PairThreshold:    pairThreshold,
		PolicyVersion:    policyVersion,
	}, readyCaptures, nil
}

// RecheckMatchingLayoutTargetTx verifies the frozen evidence using the current
// policy and the independently authorized group. The source and settings locks
// stay held until the caller commits or rolls back.
func RecheckMatchingLayoutTargetTx(ctx context.Context, tx bun.Tx, groupID uuid.UUID, policyVersion string, expected json.RawMessage) ([]IncomingGroupingCapture, error) {
	expectedCanonical, err := canonicalMatchingJSON(expected)
	if err != nil {
		return nil, fmt.Errorf("recheck matching layout target: canonicalize evidence: %w", err)
	}
	if _, _, err := DecodeMatchingLayoutTarget(expectedCanonical, groupID, policyVersion); err != nil {
		return nil, err
	}
	current, readyCaptures, _, err := CaptureMatchingLayoutTargetTx(ctx, tx, groupID, policyVersion)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(current, expectedCanonical) {
		return nil, fmt.Errorf("recheck matching layout target: target changed: %w", ErrIncomingMatchConflict)
	}
	return readyCaptures, nil
}

// DecodeMatchingLayoutTarget reconstructs saved calculator inputs, including
// the separately encoded tag corrections. It does not establish write authority.
func DecodeMatchingLayoutTarget(expected json.RawMessage, groupID uuid.UUID, policyVersion string) ([]IncomingGroupingCapture, float64, error) {
	var stored matchingLayoutTargetEvidence
	if err := json.Unmarshal(expected, &stored); err != nil ||
		stored.SchemaVersion != matchingLayoutTargetEvidenceVersion || stored.Target.SchemaVersion != 2 ||
		stored.Target.GroupID == uuid.Nil || stored.Target.GroupRevision == "" ||
		stored.Target.AuthorityRevision < 0 || stored.GroupingRevision < 0 || strings.TrimSpace(stored.PolicyVersion) == "" {
		return nil, 0, errors.New("matching layout target evidence is invalid")
	}
	if stored.Target.GroupID != groupID || stored.PolicyVersion != policyVersion {
		return nil, 0, fmt.Errorf("matching layout target group or policy changed: %w", ErrIncomingMatchConflict)
	}
	threshold, err := parseMatchingLayoutPairThreshold(stored.PairThreshold)
	canonicalThreshold, canonicalErr := CanonicalMatchingLayoutThreshold(threshold)
	if err != nil || canonicalErr != nil || canonicalThreshold != stored.PairThreshold {
		return nil, 0, errors.New("matching layout target threshold is invalid")
	}
	encodings := make(map[uuid.UUID]json.RawMessage, len(stored.Captures))
	for _, encoding := range stored.Captures {
		if _, exists := encodings[encoding.LocationID]; exists || encoding.LocationID == uuid.Nil || len(encoding.TagEncoding) == 0 {
			return nil, 0, errors.New("matching layout target tag evidence is incomplete or duplicated")
		}
		encodings[encoding.LocationID] = encoding.TagEncoding
	}
	for _, member := range stored.Target.Members {
		for i := range member.Captures {
			source := &member.Captures[i]
			encoded, ok := encodings[source.LocationID]
			if !ok {
				return nil, 0, errors.New("matching layout target capture has no tag evidence")
			}
			if !bytes.Equal(encoded, []byte("null")) {
				source.TagEncoding = encoded
			}
			delete(encodings, source.LocationID)
		}
	}
	if len(encodings) != 0 {
		return nil, 0, errors.New("matching layout target tag evidence is outside the capture")
	}
	captures, err := matchingLayoutTargetReadyCaptures(stored.Target)
	return captures, threshold, err
}

func matchingLayoutTargetPairThreshold(ctx context.Context, tx bun.Tx) (string, error) {
	threshold := *appsettings.DefaultRuntimeSettingsExpected().MatchingPairThreshold
	var stored string
	err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name=? FOR SHARE`, appsettings.MatchingPairThresholdKey).
		Scan(ctx, &stored)
	if err == nil {
		parsed, parseErr := parseMatchingLayoutPairThreshold(strings.TrimSpace(stored))
		if parseErr != nil {
			return "", parseErr
		}
		threshold = parsed
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("lock matching layout target: read pair threshold: %w", err)
	}
	return CanonicalMatchingLayoutThreshold(threshold)
}

func parseMatchingLayoutPairThreshold(value string) (float64, error) {
	threshold, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, errors.New("matching layout pair threshold is invalid")
	}
	if _, err := CanonicalMatchingLayoutThreshold(threshold); err != nil {
		return 0, err
	}
	return threshold, nil
}

func matchingLayoutTargetReadyCaptures(target automaticArtistTargetCapture) ([]IncomingGroupingCapture, error) {
	ready := make([]IncomingGroupingCapture, 0, len(target.Members))
	seenLocations := make(map[uuid.UUID]struct{})
	for _, member := range target.Members {
		if member.VariantID == uuid.Nil {
			return nil, fmt.Errorf("lock matching layout target: member identity is invalid: %w", ErrIncomingMatchConflict)
		}
		locations := make(map[uuid.UUID]IncomingGroupingMemberLocation, len(member.Locations))
		memberReady := false
		for _, location := range member.Locations {
			if location.VariantID != member.VariantID || location.LocationID == uuid.Nil {
				return nil, fmt.Errorf("lock matching layout target: member location is invalid: %w", ErrIncomingMatchConflict)
			}
			if _, duplicate := locations[location.LocationID]; duplicate {
				return nil, fmt.Errorf("lock matching layout target: duplicate member location: %w", ErrIncomingMatchConflict)
			}
			locations[location.LocationID] = location
		}
		captures := make(map[uuid.UUID]IncomingGroupingCapture, len(member.Captures))
		for _, capture := range member.Captures {
			if capture.VariantID != member.VariantID || capture.LocationID == uuid.Nil {
				return nil, fmt.Errorf("lock matching layout target: member capture is invalid: %w", ErrIncomingMatchConflict)
			}
			if _, duplicate := captures[capture.LocationID]; duplicate {
				return nil, fmt.Errorf("lock matching layout target: duplicate member capture: %w", ErrIncomingMatchConflict)
			}
			if _, exists := locations[capture.LocationID]; !exists {
				return nil, fmt.Errorf("lock matching layout target: capture has no persisted location: %w", ErrIncomingMatchConflict)
			}
			captures[capture.LocationID] = capture
		}
		if len(captures) != len(locations) {
			return nil, fmt.Errorf("lock matching layout target: member capture does not match its locations: %w", ErrIncomingMatchConflict)
		}
		for _, location := range member.Locations {
			capture, exists := captures[location.LocationID]
			if !exists || !matchingLayoutTargetLocationMatches(location, capture) {
				return nil, fmt.Errorf("lock matching layout target: member location changed: %w", ErrIncomingMatchConflict)
			}
			if _, duplicate := seenLocations[location.LocationID]; duplicate {
				return nil, fmt.Errorf("lock matching layout target: duplicate group location: %w", ErrIncomingMatchConflict)
			}
			seenLocations[location.LocationID] = struct{}{}
			if !capture.Ready || capture.CaptureAnalysisID == nil {
				continue
			}
			if *capture.CaptureAnalysisID == uuid.Nil || !capture.RootEnabled || capture.RootStale ||
				capture.RootID == uuid.Nil || capture.WorkID == uuid.Nil || capture.RelativePath == "" ||
				capture.ConfiguredPath == "" || capture.InventoryPath != capture.ConfiguredPath {
				return nil, fmt.Errorf("lock matching layout target: ready member has no current capture: %w", ErrIncomingMatchConflict)
			}
			memberReady = true
			ready = append(ready, capture)
		}
		if memberReady != member.Ready {
			return nil, fmt.Errorf("lock matching layout target: member readiness changed: %w", ErrIncomingMatchConflict)
		}
	}
	if len(ready) == 0 {
		return nil, fmt.Errorf("lock matching layout target: group has no ready captures: %w", ErrIncomingMatchConflict)
	}
	return ready, nil
}

func matchingLayoutTargetLocationMatches(location IncomingGroupingMemberLocation, capture IncomingGroupingCapture) bool {
	return location.VariantID == capture.VariantID &&
		location.RootID == capture.RootID &&
		location.WorkID == capture.WorkID &&
		location.LocationID == capture.LocationID &&
		location.ConfiguredPath == capture.ConfiguredPath &&
		location.InventoryPath == capture.InventoryPath &&
		location.RelativePath == capture.RelativePath &&
		location.SizeBytes == capture.SizeBytes &&
		location.Mtime.Equal(capture.Mtime)
}

func matchingLayoutTargetTagEncodings(target automaticArtistTargetCapture) []matchingLayoutTargetCapture {
	encodings := make([]matchingLayoutTargetCapture, 0)
	for _, member := range target.Members {
		for _, capture := range member.Captures {
			encodings = append(encodings, matchingLayoutTargetCapture{
				LocationID:  capture.LocationID,
				TagEncoding: capture.TagEncoding,
			})
		}
	}
	sort.Slice(encodings, func(i, j int) bool {
		return encodings[i].LocationID.String() < encodings[j].LocationID.String()
	})
	return encodings
}
