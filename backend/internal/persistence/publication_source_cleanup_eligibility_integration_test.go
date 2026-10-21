//go:build integration

package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestReadPublicationSourceCleanupAssessmentIncludesAlternativesAndSharedBorrowersPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	repo := NewSetupManagerRepository(db)
	positionID, siblingPositionID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,'cleanup assessment')`, []any{releaseID}},
		{`INSERT INTO local_track(id,title) VALUES (?,'cleanup assessment')`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, []any{positionID, releaseID, trackID}},
		{`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,1)`, []any{siblingPositionID, releaseID, trackID}},
	} {
		if _, err := db.ExecContext(ctx, query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	rootID, workID, scanID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,'/music','Music',1,'/music',true,'available')`, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, scanID); err != nil {
		t.Fatal(err)
	}
	variants := []uuid.UUID{uuid.New(), uuid.New()}
	for i, variant := range variants {
		locationID := uuid.New()
		mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?,42,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}','{}'::jsonb,?,?,1)`, variant, mtime, scanID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,?,42,?,1,'audio',?)`, locationID, rootID, fmt.Sprintf("track-%d.flac", i), mtime, variant); err != nil {
			t.Fatal(err)
		}
		candidateWork := workID
		if i > 0 {
			candidateWork = uuid.New()
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,'/music','/music',?,42,?,false,?,?)`, candidateWork, locationID, rootID, fmt.Sprintf("track-%d.flac", i), mtime, scanID, locationID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, candidateWork, variant); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?, ?,true,'manual','{}'::jsonb)`, uuid.New(), trackID, variant, i == 0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.ReadPublicationSourceCleanupAssessment(ctx, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("candidate alternatives = %d, want 2", len(got.Candidates))
	}
	for _, candidate := range got.Candidates {
		if len(candidate.BorrowerPositions) != 2 {
			t.Fatalf("borrower positions = %v, want both release positions", candidate.BorrowerPositions)
		}
		if candidate.Held {
			t.Fatal("unexpected publication hold")
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE source_location SET size_bytes=43 WHERE source_root_id=?`, rootID); err != nil {
		t.Fatal(err)
	}
	changedFacts, err := repo.ReadPublicationSourceCleanupAssessment(ctx, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if !assessmentHasCandidateBlocker(changedFacts, "source work file facts do not match current inventory location") {
		t.Fatal("changed current-location file facts were not blocked")
	}
	if _, err := db.ExecContext(ctx, `UPDATE source_root SET configured_path='/repointed' WHERE id=?`, rootID); err != nil {
		t.Fatal(err)
	}
	repointed, err := repo.ReadPublicationSourceCleanupAssessment(ctx, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if !assessmentHasCandidateBlocker(repointed, "source root inventory is stale for its configured path") {
		t.Fatal("repointed source root with stale inventory was not blocked")
	}
	if len(got.Blockers) == 0 {
		t.Fatal("missing current-publication blocker for unpublished position")
	}
	if _, err := repo.ReadPublicationSourceCleanupAssessment(ctx, uuid.New()); err == nil {
		t.Fatal("missing release position accepted")
	}
}

func TestReadPublicationSourceCleanupAssessmentKeepsTerminalPublicationHoldPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	_, rootID, variantID, operationID := pubSourceProtectionHoldWithFixture(t, f)
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id)
		SELECT work_id,media_variant_id FROM publication_source_hold WHERE operation_id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method)
		SELECT ?,track_id,?,false,true,'manual' FROM release_track WHERE id=?`, uuid.New(), variantID, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	var hold PublicationSourceHold
	if err := f.db.NewSelect().Model(&hold).Where("operation_id=?", operationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	// Location/work identities remain associated exactly as captured by the hold;
	// a terminal owner must not make those durable source identities cleanable.
	if _, err := f.db.ExecContext(ctx, `UPDATE operation SET state='failed',stage='failed',safe_error='publication failed',finished_at=now() WHERE id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	got, err := NewSetupManagerRepository(f.db).ReadPublicationSourceCleanupAssessment(ctx, f.guard.ExpectedPositionID)
	if err != nil {
		t.Fatal(err)
	}
	var held bool
	for _, candidate := range got.Candidates {
		if candidate.SourceRootID == rootID && candidate.SourceLocationID == hold.SourceLocationID && candidate.WorkID == hold.WorkID {
			held = candidate.Held
			foundBlocker := false
			for _, blocker := range candidate.Blockers {
				foundBlocker = foundBlocker || blocker == "source is protected by a publication hold"
			}
			if !foundBlocker {
				t.Fatal("held candidate lacks its publication-hold blocker")
			}
		}
	}
	if !held {
		t.Fatalf("terminal operation's publication hold was not retained: %+v", got.Candidates)
	}
	if !assessmentHasCandidateBlocker(got, "source is protected by a publication hold") {
		t.Fatalf("missing publication-hold blocker: %+v", got.Candidates)
	}
}

func assessmentHasCandidateBlocker(assessment PublicationSourceCleanupAssessment, blocker string) bool {
	for _, candidate := range assessment.Candidates {
		for _, candidateBlocker := range candidate.Blockers {
			if candidateBlocker == blocker {
				return true
			}
		}
	}
	return false
}
