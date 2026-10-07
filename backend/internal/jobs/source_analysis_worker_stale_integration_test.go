//go:build integration

package jobs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestSourceAnalysisWorkerRecoversCurrentStaleDeliveryPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newAnalysisDispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	operation := fixture.startScheduled(t, ctx)
	type stepInput struct {
		Step          string `bun:"step"`
		State         string `bun:"state"`
		Attempt       int    `bun:"step_attempt"`
		InputSnapshot []byte `bun:"input_snapshot"`
	}
	var before []stepInput
	if err := fixture.database.NewRaw(`SELECT step,state,step_attempt,input_snapshot FROM source_analysis_step WHERE work_id=? AND step IN ('sha256','probe') ORDER BY step`, fixture.work.ID).Scan(ctx, &before); err != nil {
		t.Fatalf("read admitted steps: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("admitted steps = %d, want two", len(before))
	}
	if _, err := fixture.database.ExecContext(ctx, "UPDATE source_root SET enabled = false WHERE id = ?", fixture.root.ID); err != nil {
		t.Fatalf("disable the root: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, "UPDATE river_job SET state='available', scheduled_at=now() WHERE id=?", *operation.RiverJobID); err != nil {
		t.Fatalf("make the admitted delivery available: %v", err)
	}
	awaitRiverCompletion(t, ctx, fixture.events, *operation.RiverJobID)

	stored := fixture.readOperation(t, ctx, operation.ID)
	if stored.State != "failed" || stored.Stage != "recovered" {
		t.Fatalf("operation = %s/%s, want failed/recovered", stored.State, stored.Stage)
	}
	if stored.SafeError == nil || *stored.SafeError != analysisSafeInterrupted {
		t.Fatalf("safe error = %v, want %q", stored.SafeError, analysisSafeInterrupted)
	}
	requireAnalysisHolds(t, ctx, fixture, operation.ID, nil, nil)
	var after []stepInput
	if err := fixture.database.NewRaw(`SELECT step,state,step_attempt,input_snapshot FROM source_analysis_step WHERE work_id=? AND step IN ('sha256','probe') ORDER BY step`, fixture.work.ID).Scan(ctx, &after); err != nil {
		t.Fatalf("read recovered steps: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("recovered steps = %d, want %d", len(after), len(before))
	}
	for i := range before {
		if after[i].Step != before[i].Step || after[i].State != "pending" || after[i].Attempt != 0 {
			t.Errorf("step %s after recovery = %s attempt %d, want pending attempt 0", after[i].Step, after[i].State, after[i].Attempt)
		}
		if len(before[i].InputSnapshot) == 0 || string(after[i].InputSnapshot) != string(before[i].InputSnapshot) {
			t.Errorf("step %s input snapshot was not retained", before[i].Step)
		}
	}
	if probeLog, err := os.ReadFile(fixture.probeLog); err == nil && len(probeLog) != 0 {
		t.Fatalf("probe ran despite disabled root: %s", probeLog)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read probe log: %v", err)
	}
	var riverState string
	if err := fixture.database.NewRaw("SELECT state FROM river_job WHERE id=?", *operation.RiverJobID).Scan(ctx, &riverState); err != nil {
		t.Fatalf("read River job state: %v", err)
	}
	if riverState != "completed" {
		t.Fatalf("River job state = %s, want completed", riverState)
	}
}
