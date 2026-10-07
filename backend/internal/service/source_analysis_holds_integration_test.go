//go:build integration

package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestSourceAnalysisRetryStepRefusalsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	expectRefusal := func(t *testing.T, fixture sourceAnalysisStartIntegration, request service.SourceAnalysisStepRequest, want error) {
		t.Helper()
		if _, err := fixture.starter.RetryStep(ctx, request); !errors.Is(err, want) {
			t.Fatalf("retry step = %v, want %v", err, want)
		}
		if operations := countAnalysisStartRows(t, ctx, fixture.database, "SELECT count(*) FROM operation WHERE kind = 'analyze_source'"); operations != 0 {
			t.Fatalf("analysis operations after refusal = %d, want 0", operations)
		}
		if jobs := countAnalysisStartRows(t, ctx, fixture.database, "SELECT count(*) FROM river_job WHERE kind = ?", service.SourceAnalysisJobKind); jobs != 0 {
			t.Fatalf("analysis River jobs after refusal = %d, want 0", jobs)
		}
	}
	request := func(fixture sourceAnalysisStartIntegration) service.SourceAnalysisStepRequest {
		return service.SourceAnalysisStepRequest{
			RootID: fixture.root.ID, LocationID: fixture.location.ID, Step: persistence.SourceStepSHA256,
			ExpectedSizeBytes: fixture.location.SizeBytes, ExpectedMtime: fixture.location.Mtime,
		}
	}
	t.Run("changed_identity", func(t *testing.T) {
		fixture := newSourceAnalysisStartIntegration(t, true)
		changed := request(fixture)
		changed.ExpectedSizeBytes++
		expectRefusal(t, fixture, changed, persistence.ErrSourceAnalysisStale)
	})
	t.Run("disabled_root", func(t *testing.T) {
		fixture := newSourceAnalysisStartIntegration(t, true)
		if _, err := fixture.database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
			Set("enabled = false").Where("id = ?", fixture.root.ID).Exec(ctx); err != nil {
			t.Fatalf("disable root: %v", err)
		}
		// The current work identity is revalidated as stale before the disabled-root
		// admission check; either way, the disabled root is refused without enqueue.
		expectRefusal(t, fixture, request(fixture), persistence.ErrSourceAnalysisStale)
	})
	t.Run("setup_incomplete", func(t *testing.T) {
		fixture := newSourceAnalysisStartIntegration(t, false)
		expectRefusal(t, fixture, request(fixture), service.ErrSourceAnalysisNotReady)
	})
}

// A tools-root move only excludes work that reads managed tools. A SHA retry is
// tool-free and remains admissible; it carries no installation hold to pin.
func TestSourceAnalysisToolFreeRetryIsCompatibleWithToolsMoveWithPostgreSQL(t *testing.T) {
	t.Parallel()
	fixture := newSourceAnalysisStartIntegration(t, true)
	ctx := context.Background()
	move := &persistence.Operation{ID: uuid.New(), Kind: "move_tools_root", State: "queued", Stage: "queued", InputSnapshot: []byte(`{}`)}
	if err := fixture.setup.CreateOperation(ctx, move); err != nil {
		t.Fatalf("create queued tools move: %v", err)
	}
	operation, err := fixture.starter.RetryStep(ctx, service.SourceAnalysisStepRequest{
		RootID: fixture.root.ID, LocationID: fixture.location.ID, Step: persistence.SourceStepSHA256,
		ExpectedSizeBytes: fixture.location.SizeBytes, ExpectedMtime: fixture.location.Mtime,
	})
	if err != nil {
		t.Fatalf("admit tool-free SHA retry during tools move: %v", err)
	}
	if operation.ToolsReadRequired || operation.RiverJobID == nil {
		t.Fatalf("tool-free operation acquired a tools hold: %+v", operation)
	}
}
