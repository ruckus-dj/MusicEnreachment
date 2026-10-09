//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestMetadataSingleStepAdmissionDoesNotRequireAnalysisToolsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/metadata-single-step")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	priorError := "prior metadata attempt failed"
	work := normalizedWork(t, ctx, repository, root, location, false,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepMetadata, State: "failed", SafeError: &priorError},
	)
	targetStep := string(persistence.SourceStepMetadata)
	fixture := newAnalysisStepFixture(t, ctx, database, repository, client, root, location, work,
		persistence.SourceAnalysisModeSingleStep, &work.ID, &targetStep, false, nil)

	var stored struct {
		State         string          `bun:"state"`
		InputSnapshot json.RawMessage `bun:"input_snapshot"`
	}
	if err := database.NewRaw(`SELECT state,input_snapshot FROM source_analysis_step WHERE work_id=? AND step='metadata'`, work.ID).Scan(ctx, &stored); err != nil {
		t.Fatalf("read admitted metadata step: %v", err)
	}
	if stored.State != "queued" {
		t.Fatalf("metadata step state = %q; want queued after retry admission", stored.State)
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(stored.InputSnapshot, &snapshot); err != nil {
		t.Fatalf("decode retained metadata intent: %v", err)
	}
	if _, exists := snapshot["tools"]; exists {
		t.Fatal("metadata intent unexpectedly captured managed tools")
	}
	if fixture.operation.ID == [16]byte{} {
		t.Fatal("metadata admission returned an empty operation ID")
	}
}
