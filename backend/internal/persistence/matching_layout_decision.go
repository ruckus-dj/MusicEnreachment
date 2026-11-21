package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// MatchingLayoutDecisionSchemaVersion is the durable schema of a whole-layout
// result. A stored decision is written once and is never reinterpreted under a
// different version.
const MatchingLayoutDecisionSchemaVersion = 1

// MatchingLayoutPolicyVersion identifies whole-layout selection, distinct from
// the underlying text scorer's version.
const MatchingLayoutPolicyVersion = "matching-layout-v1"

// MatchingLayoutDecisionStatus is the explicit result of whole-layout selection.
// It mirrors the service status without importing the service package.
type MatchingLayoutDecisionStatus string

const (
	MatchingLayoutDecisionSelected      MatchingLayoutDecisionStatus = "selected"
	MatchingLayoutDecisionNone          MatchingLayoutDecisionStatus = "none"
	MatchingLayoutDecisionIndeterminate MatchingLayoutDecisionStatus = "indeterminate"
)

// MatchingLayoutDecisionAssignment is one exact file-to-position decision.
type MatchingLayoutDecisionAssignment struct {
	FileMemberID    uuid.UUID `json:"file_member_id"`
	ProviderTrackID uuid.UUID `json:"provider_track_id"`
}

// MatchingLayoutDecision is the compact, write-once identity of one whole-layout
// result. It retains only the policy identity, the exact selected provider
// release, and ordered per-file provider track IDs. Source paths, tags,
// fingerprints, raw provider payloads, pair scores, and freshness evidence
// belong to the surrounding T05 attempt, not to this decision.
//
// Assignments are not required to have distinct provider track IDs: the solver
// may assign one position to files at distinct locations. Duplicate track IDs
// are therefore valid and are never treated as proof of an invalid decision.
type MatchingLayoutDecision struct {
	SchemaVersion     int                                `json:"schema_version"`
	PolicyVersion     string                             `json:"policy_version"`
	PairThreshold     string                             `json:"pair_threshold"`
	Status            MatchingLayoutDecisionStatus       `json:"status"`
	ProviderReleaseID *uuid.UUID                         `json:"provider_release_id,omitempty"`
	Assignments       []MatchingLayoutDecisionAssignment `json:"assignments,omitempty"`
	Reason            string                             `json:"reason,omitempty"`
}

// CanonicalMatchingLayoutThreshold returns the exact decimal spelling stored in
// a decision. The threshold itself is an independently authorized settings
// scalar; this function neither derives it from a score nor applies it.
func CanonicalMatchingLayoutThreshold(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return "", errors.New("matching layout decision pair threshold must be between 0 and 1")
	}
	// Normalize both zero signs so that the stored spelling is stable.
	if value == 0 {
		return "0", nil
	}
	return strconv.FormatFloat(value, 'f', -1, 64), nil
}

// Validate compares the decision with independently authorized inputs.
//
// expectedPolicyVersion and expectedPairThreshold must come from their own
// authority, not from the decision. readyMemberIDs are the distinct variant
// identities of the ready captures. candidateReleases is the accepted provider
// projection scope for the shortlist; only release and track identities are
// read from it.
//
// Validation checks identity, scope, and completeness. It never scores pairs,
// applies the threshold to a score, or repeats the selection rule. In
// particular, duplicate provider track IDs across assignments are allowed
// because the solver can reuse a position for files at distinct locations.
func (decision MatchingLayoutDecision) Validate(
	expectedPolicyVersion string,
	expectedPairThreshold float64,
	readyMemberIDs []uuid.UUID,
	candidateReleases []MatchingRelease,
) error {
	if err := decision.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(expectedPolicyVersion) == "" {
		return errors.New("matching layout decision expected policy version is required")
	}
	if decision.PolicyVersion != expectedPolicyVersion {
		return fmt.Errorf("matching layout decision policy version %q does not match expected %q", decision.PolicyVersion, expectedPolicyVersion)
	}
	expectedThreshold, err := CanonicalMatchingLayoutThreshold(expectedPairThreshold)
	if err != nil {
		return err
	}
	if decision.PairThreshold != expectedThreshold {
		return fmt.Errorf("matching layout decision pair threshold %q does not match expected %q", decision.PairThreshold, expectedThreshold)
	}
	ready, err := matchingLayoutDecisionReadyMembers(readyMemberIDs)
	if err != nil {
		return err
	}
	if decision.Status == MatchingLayoutDecisionIndeterminate {
		return nil
	}
	scope, err := matchingLayoutDecisionCandidateScope(candidateReleases)
	if err != nil {
		return err
	}
	if decision.Status == MatchingLayoutDecisionNone {
		return nil
	}
	if len(decision.Assignments) != len(ready) {
		return errors.New("matching layout decision must assign every ready member exactly once")
	}
	releaseID := *decision.ProviderReleaseID
	tracks, ok := scope[releaseID]
	if !ok {
		return fmt.Errorf("matching layout decision release %s is outside the accepted projection", releaseID)
	}
	for _, assignment := range decision.Assignments {
		if _, ok := ready[assignment.FileMemberID]; !ok {
			return fmt.Errorf("matching layout decision assigns member %s that is not ready", assignment.FileMemberID)
		}
		if _, ok := tracks[assignment.ProviderTrackID]; !ok {
			return fmt.Errorf("matching layout decision assigns track %s outside release %s", assignment.ProviderTrackID, releaseID)
		}
	}
	return nil
}

// CanonicalJSON returns the stable encoding used by Digest. The decision must be
// structurally valid. Assignment order is normalized by file member identity,
// and object keys are sorted, so neither is part of the logical result.
func (decision MatchingLayoutDecision) CanonicalJSON() ([]byte, error) {
	if err := decision.validate(); err != nil {
		return nil, err
	}
	canonical := decision.clone()
	sort.Slice(canonical.Assignments, func(i, j int) bool {
		return canonical.Assignments[i].FileMemberID.String() < canonical.Assignments[j].FileMemberID.String()
	})
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("encode matching layout decision: %w", err)
	}
	canonicalJSON, err := canonicalMatchingJSON(encoded)
	if err != nil {
		return nil, fmt.Errorf("canonicalize matching layout decision: %w", err)
	}
	return canonicalJSON, nil
}

// Digest returns the canonical SHA-256 identity of a validated decision. It
// covers every stored field, including the optional diagnostic reason. Compute
// it once when the decision is written; a stored decision is never edited in
// place.
func (decision MatchingLayoutDecision) Digest() (string, error) {
	canonical, err := decision.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func (decision MatchingLayoutDecision) validate() error {
	if decision.SchemaVersion != MatchingLayoutDecisionSchemaVersion {
		return fmt.Errorf("unsupported matching layout decision schema version %d", decision.SchemaVersion)
	}
	if strings.TrimSpace(decision.PolicyVersion) == "" {
		return errors.New("matching layout decision policy version is required")
	}
	if err := matchingLayoutDecisionThreshold(decision.PairThreshold); err != nil {
		return err
	}
	switch decision.Status {
	case MatchingLayoutDecisionSelected:
		if decision.ProviderReleaseID == nil || *decision.ProviderReleaseID == uuid.Nil {
			return errors.New("selected matching layout decision requires a provider release")
		}
		if len(decision.Assignments) == 0 {
			return errors.New("selected matching layout decision requires assignments")
		}
		members := make(map[uuid.UUID]struct{}, len(decision.Assignments))
		for _, assignment := range decision.Assignments {
			if assignment.FileMemberID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil {
				return errors.New("matching layout decision assignment identity is invalid")
			}
			if _, duplicate := members[assignment.FileMemberID]; duplicate {
				return fmt.Errorf("matching layout decision has duplicate assignment for member %s", assignment.FileMemberID)
			}
			members[assignment.FileMemberID] = struct{}{}
		}
	case MatchingLayoutDecisionNone, MatchingLayoutDecisionIndeterminate:
		if decision.ProviderReleaseID != nil {
			return fmt.Errorf("%s matching layout decision must not name a provider release", decision.Status)
		}
		if len(decision.Assignments) != 0 {
			return fmt.Errorf("%s matching layout decision must not contain assignments", decision.Status)
		}
	default:
		return fmt.Errorf("unsupported matching layout decision status %q", decision.Status)
	}
	return nil
}

func matchingLayoutDecisionThreshold(value string) error {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("matching layout decision pair threshold is invalid: %w", err)
	}
	canonical, err := CanonicalMatchingLayoutThreshold(parsed)
	if err != nil {
		return err
	}
	if canonical != value {
		return fmt.Errorf("matching layout decision pair threshold %q is not canonical %q", value, canonical)
	}
	return nil
}

func matchingLayoutDecisionReadyMembers(ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	if len(ids) == 0 {
		return nil, errors.New("matching layout decision requires at least one ready member")
	}
	members := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, errors.New("matching layout decision ready member identity is invalid")
		}
		if _, duplicate := members[id]; duplicate {
			return nil, fmt.Errorf("matching layout decision ready member %s is duplicated", id)
		}
		members[id] = struct{}{}
	}
	return members, nil
}

func matchingLayoutDecisionCandidateScope(releases []MatchingRelease) (map[uuid.UUID]map[uuid.UUID]struct{}, error) {
	scope := make(map[uuid.UUID]map[uuid.UUID]struct{}, len(releases))
	for _, release := range releases {
		if release.ID == uuid.Nil || !release.Complete {
			return nil, errors.New("matching layout decision requires complete candidate layouts")
		}
		if _, duplicate := scope[release.ID]; duplicate {
			return nil, fmt.Errorf("matching layout decision candidate release %s is duplicated", release.ID)
		}
		tracks := make(map[uuid.UUID]struct{}, len(release.Tracks))
		for _, track := range release.Tracks {
			if track.ID == uuid.Nil {
				return nil, fmt.Errorf("matching layout decision candidate release %s has an invalid track identity", release.ID)
			}
			if _, duplicate := tracks[track.ID]; duplicate {
				return nil, fmt.Errorf("matching layout decision candidate release %s has duplicate track %s", release.ID, track.ID)
			}
			tracks[track.ID] = struct{}{}
		}
		scope[release.ID] = tracks
	}
	return scope, nil
}

func (decision MatchingLayoutDecision) clone() MatchingLayoutDecision {
	decision.Assignments = append([]MatchingLayoutDecisionAssignment(nil), decision.Assignments...)
	if decision.ProviderReleaseID != nil {
		releaseID := *decision.ProviderReleaseID
		decision.ProviderReleaseID = &releaseID
	}
	return decision
}
