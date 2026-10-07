package jobs

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestGroupSourceAnalysisExecutionsOrdersWorkAndSteps(t *testing.T) {
	workID := uuid.New()
	otherWorkID := uuid.New()
	executions := []persistence.SourceAnalysisExecution{
		{Work: persistence.SourceAnalysisWork{ID: workID}, Step: persistence.SourceAnalysisStep{Step: string(persistence.SourceStepFingerprint)}},
		{Work: persistence.SourceAnalysisWork{ID: workID}, Step: persistence.SourceAnalysisStep{Step: string(persistence.SourceStepProbe)}},
		{Work: persistence.SourceAnalysisWork{ID: workID}, Step: persistence.SourceAnalysisStep{Step: string(persistence.SourceStepSHA256)}},
		{Work: persistence.SourceAnalysisWork{ID: otherWorkID}, Step: persistence.SourceAnalysisStep{Step: string(persistence.SourceStepProbe)}},
	}

	groups := groupSourceAnalysisExecutions(executions)
	if len(groups) != 2 {
		t.Fatalf("got %d work groups, want 2", len(groups))
	}
	if groups[0][0].Work.ID.String() > groups[1][0].Work.ID.String() {
		t.Fatal("work groups are not ordered by UUID")
	}
	for _, group := range groups {
		if len(group) == 1 {
			continue
		}
		want := []string{string(persistence.SourceStepSHA256), string(persistence.SourceStepProbe), string(persistence.SourceStepFingerprint)}
		for index, execution := range group {
			if got := execution.Step.Step; got != want[index] {
				t.Fatalf("step %d = %q, want %q", index, got, want[index])
			}
		}
	}
}
