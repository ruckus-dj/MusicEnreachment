//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Add two additional eligible source graphs using the same persisted identity
// shape as the ordinary multi-file cleanup fixture. These are real source
// locations, work rows, variant identities, and borrower links, not fabricated
// cleanup rows or unbacked UUIDs.
func addCleanupRetrySourceCopies(t *testing.T, ctx context.Context, fixture publicationExecutionFixture, work SourceAnalysisWork, relatives ...string) {
	t.Helper()
	for _, relative := range relatives {
		variant, location, sourceWork := uuid.New(), uuid.New(), uuid.New()
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO media_variant(id,size_bytes) VALUES (?,0)`, []any{variant}},
			{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id)
				SELECT ?,source_root_id,?,0,mtime,last_seen_scan_generation,'audio',? FROM source_location WHERE id=?`, []any{location, relative, variant, work.LocationID}},
			{`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id)
				SELECT ?,?,source_root_id,configured_path,inventory_path,?,size_bytes,mtime,false,origin_scan_operation_id,? FROM source_analysis_work WHERE id=?`, []any{sourceWork, location, relative, location, work.ID}},
			{`INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, []any{sourceWork, variant}},
			{`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method)
				SELECT ?,track_id,?,false,true,'manual' FROM release_track WHERE id=?`, []any{uuid.New(), variant, fixture.guard.ExpectedPositionID}},
		} {
			if _, err := fixture.db.NewRaw(statement.query, statement.args...).Exec(ctx); err != nil {
				t.Fatalf("add eligible source %q: %v", relative, err)
			}
		}
	}
}

// Concurrent admissions for one returned cleanup must serialize at the durable
// admission boundary: exactly one recovery operation may own its retained
// evidence, even when both callers begin from the same observed frontier.
