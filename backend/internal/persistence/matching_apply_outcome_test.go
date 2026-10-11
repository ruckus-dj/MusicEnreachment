package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
)

func TestDecodeMatchingApplyOutcomeAllowsManualReviewDecisions(t *testing.T) {
	for _, outcome := range []string{"review", "tie", "infeasible"} {
		t.Run(outcome, func(t *testing.T) {
			groupID := uuid.New()
			evidence, err := json.Marshal(map[string]any{"evaluation_decision": map[string]any{
				"version": 1, "group_id": groupID, "policy_version": matchingpolicy.CurrentVersion,
				"outcome": outcome, "assignments": []any{},
			}})
			if err != nil {
				t.Fatal(err)
			}
			decision, err := decodeMatchingApplyOutcome(evidence)
			if err != nil {
				t.Fatalf("decode review outcome: %v", err)
			}
			if decision.GroupID != groupID || decision.Outcome != outcome {
				t.Fatalf("decoded outcome = %#v", decision)
			}
			if _, err := decodePersistedMatchingDecision(evidence); err == nil {
				t.Fatal("manual decision accepted by automatic apply decoder")
			}
		})
	}
}
