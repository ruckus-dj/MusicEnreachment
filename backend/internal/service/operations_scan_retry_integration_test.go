//go:build integration

package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// TestScanRetryThroughTheOperationsServiceWithPostgreSQL drives the retry the
// API reaches: the production Operations service is built over the repository
// that owns the operation table and a real River client, so the scan retry has
// to be discovered there, drop the candidates of the failed attempt and be
// delivered under the scan job kind instead of the generic operation kind.
func TestScanRetryThroughTheOperationsServiceWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	testpostgres.ResetAndMigrate(t, database)
	ctx := context.Background()
	repository := persistence.NewSetupManagerRepository(database)
	inventory := persistence.NewSourceInventoryRepository(database)
	operations := service.NewOperationsWithRiver(repository, openSourceScanRiver(t, database))

	root := &persistence.SourceRoot{ConfiguredPath: "/srv/service-retry", DisplayName: "ServiceRetry", Enabled: true}
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatalf("create the source root: %v", err)
	}
	failed := failedScanRetryOperation(root, service.SourceScanStageTraversing)
	if err := repository.CreateOperation(ctx, failed); err != nil {
		t.Fatalf("create the failed scan operation: %v", err)
	}
	if err := inventory.ReplaceSourceScanCandidates(ctx, failed.ID, []persistence.SourceScanCandidateInput{{
		RelativePath: "album/stale.flac", SizeBytes: 1024, Mtime: time.Now().UTC().Truncate(time.Microsecond), ProbeStatus: "audio",
	}}); err != nil {
		t.Fatalf("store the candidates of the failed attempt: %v", err)
	}

	retried, err := operations.Retry(ctx, failed.ID)
	if err != nil {
		t.Fatalf("retry the failed scan through the operations service: %v", err)
	}
	if retried.ID != failed.ID || retried.State != "queued" || retried.Attempt != 2 || retried.RiverJobID == nil {
		t.Fatalf("retried operation = %+v, want the same scan queued as attempt 2 with a River job", retried)
	}
	job := readScanJobRow(t, ctx, database, *retried.RiverJobID)
	if job.Kind != service.SourceScanJobKind {
		t.Fatalf("retry River job kind = %q, want %q", job.Kind, service.SourceScanJobKind)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(job.Args), &args); err != nil {
		t.Fatalf("decode the retry River args %s: %v", job.Args, err)
	}
	if len(args) != 1 || args["operation_id"] != failed.ID.String() {
		t.Fatalf("retry River args = %v, want the operation id alone", args)
	}
	if generic := countScanStartRows(t, ctx, database, "SELECT count(*) FROM river_job WHERE kind = 'operation_v1'"); generic != 0 {
		t.Fatalf("River jobs under the generic operation kind = %d, want the scan kind", generic)
	}
	if candidates := countScanStartRows(t, ctx, database,
		"SELECT count(*) FROM source_scan_candidate WHERE operation_id = ?", failed.ID); candidates != 0 {
		t.Fatalf("candidates of the retried scan = %d, want the failed attempt's candidates dropped", candidates)
	}
	if locations := countScanStartRows(t, ctx, database,
		"SELECT count(*) FROM source_location WHERE source_root_id = ?", root.ID); locations != 0 {
		t.Fatalf("locations after the retry = %d, want the retry to write no inventory", locations)
	}
}
