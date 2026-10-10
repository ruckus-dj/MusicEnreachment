package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
)

const matchingDecisionVersion = 1

type MatchingEvaluationDecisionOutcome string

const (
	MatchingDecisionOutcomeUnique     MatchingEvaluationDecisionOutcome = "unique"
	MatchingDecisionOutcomeTie        MatchingEvaluationDecisionOutcome = "tie"
	MatchingDecisionOutcomeInfeasible MatchingEvaluationDecisionOutcome = "infeasible"
	MatchingDecisionOutcomeReview     MatchingEvaluationDecisionOutcome = "review"
)

// MatchingEvaluationDecision is a versioned, persisted record of the solver's
// actual group decision. It is evidence only; it does not apply or confirm a
// match.
type MatchingEvaluationDecision struct {
	Version         int                               `json:"version"`
	GroupID         uuid.UUID                         `json:"group_id"`
	PolicyVersion   string                            `json:"policy_version"`
	Outcome         MatchingEvaluationDecisionOutcome `json:"outcome"`
	SelectedRelease *MatchingDecisionSelectedRelease  `json:"selected_release,omitempty"`
	Assignments     []MatchingDecisionAssignment      `json:"assignments"`
}

type MatchingDecisionSelectedRelease struct {
	ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
	ProviderReleaseKey string    `json:"provider_release_key"`
}

type MatchingDecisionAssignment struct {
	FileMemberID    uuid.UUID `json:"file_member_id"`
	ProviderTrackID uuid.UUID `json:"provider_track_id"`
	Medium          int       `json:"medium"`
	Position        int       `json:"position"`
}

// DecodeMatchingDecision strictly decodes the typed decision embedded in a
// persisted matching evidence envelope. Unknown fields and unsupported
// versions fail closed so persisted evidence is never silently reinterpreted.
func DecodeMatchingDecision(evidence json.RawMessage) (MatchingEvaluationDecision, error) {
	var envelope struct {
		EvaluationDecision json.RawMessage `json:"evaluation_decision"`
	}
	if err := json.Unmarshal(evidence, &envelope); err != nil {
		return MatchingEvaluationDecision{}, fmt.Errorf("decode matching evaluation evidence: %w", err)
	}
	if len(envelope.EvaluationDecision) == 0 {
		return MatchingEvaluationDecision{}, fmt.Errorf("decode matching evaluation evidence: decision is missing")
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.EvaluationDecision))
	decoder.DisallowUnknownFields()
	var decision MatchingEvaluationDecision
	if err := decoder.Decode(&decision); err != nil {
		return MatchingEvaluationDecision{}, fmt.Errorf("decode matching evaluation decision: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return MatchingEvaluationDecision{}, fmt.Errorf("decode matching evaluation decision: trailing data")
	}
	if err := validateMatchingEvaluationDecision(decision); err != nil {
		return MatchingEvaluationDecision{}, err
	}
	return decision, nil
}

func validateMatchingEvaluationDecision(decision MatchingEvaluationDecision) error {
	if decision.Version != matchingDecisionVersion {
		return fmt.Errorf("decode matching evaluation decision: unsupported version %d", decision.Version)
	}
	if decision.GroupID == uuid.Nil || !matchingpolicy.Supported(decision.PolicyVersion) {
		return fmt.Errorf("decode matching evaluation decision: group and policy identities are required")
	}
	switch decision.Outcome {
	case MatchingDecisionOutcomeUnique, MatchingDecisionOutcomeTie, MatchingDecisionOutcomeInfeasible, MatchingDecisionOutcomeReview:
	default:
		return fmt.Errorf("decode matching evaluation decision: invalid outcome %q", decision.Outcome)
	}
	if decision.SelectedRelease != nil && (decision.SelectedRelease.ProviderReleaseID == uuid.Nil || decision.SelectedRelease.ProviderReleaseKey == "") {
		return fmt.Errorf("decode matching evaluation decision: selected release identity is incomplete")
	}
	if decision.Outcome == MatchingDecisionOutcomeUnique && (decision.SelectedRelease == nil || len(decision.Assignments) == 0) {
		return fmt.Errorf("decode matching evaluation decision: unique outcome requires selected release assignments")
	}
	seen := make(map[uuid.UUID]struct{}, len(decision.Assignments))
	for _, assignment := range decision.Assignments {
		if assignment.FileMemberID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil || assignment.Medium <= 0 || assignment.Position <= 0 {
			return fmt.Errorf("decode matching evaluation decision: assignment coordinates are invalid")
		}
		if _, exists := seen[assignment.FileMemberID]; exists {
			return fmt.Errorf("decode matching evaluation decision: duplicate file member %s", assignment.FileMemberID)
		}
		seen[assignment.FileMemberID] = struct{}{}
	}
	if decision.Outcome != MatchingDecisionOutcomeUnique && len(decision.Assignments) != 0 {
		return fmt.Errorf("decode matching evaluation decision: non-unique outcome cannot contain assignments")
	}
	return nil
}
