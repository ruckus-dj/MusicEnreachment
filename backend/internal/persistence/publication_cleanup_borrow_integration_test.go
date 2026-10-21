//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestPublicationAdmissionConflictsWithCleanupOwnerPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)

	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,'cleanup borrow fixture')`, []any{releaseID}},
		{`INSERT INTO local_track(id,title) VALUES (?,'cleanup borrow fixture')`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, []any{positionID, releaseID, trackID}},
	} {
		if _, err := db.ExecContext(ctx, query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}

	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize fixture root: %v", err)
	}
	const relative = "track.flac"
	now := time.Date(2025, time.January, 2, 3, 4, 5, 123456000, time.UTC)
	rootID, locationID, workID, variantID, scanID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,?, 'Source',1,?,true,'available')`, rootID, rootPath, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, scanID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?,42,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}','{}'::jsonb,?,?,1)`, variantID, now, scanID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,?,42,?,1,'audio',?)`, locationID, rootID, relative, now, variantID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,?,42,?,false,?,?)`, workID, locationID, rootID, rootPath, rootPath, relative, now, scanID, locationID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, workID, variantID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'executed')`, workID, variantID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,true,true,'manual','{}'::jsonb)`, uuid.New(), trackID, variantID)
	if err != nil {
		t.Fatal(err)
	}

	lossless, channels := true, 2
	audio := PublicationAudioInput{
		WorkID: workID, RootID: rootID, LocationID: locationID, MediaVariantID: variantID, ProbeResultID: variantID,
		ConfiguredRootPath: rootPath, InventoryRootPath: rootPath, RelativePath: relative, SizeBytes: 42, MTime: now,
		AnalysisPolicyVersion: 1, FFProbeVersion: "ffprobe 1",
		AudioFacts: PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true},
	}
	source := PublicationSourceSelection{RootID: rootID, LocationID: locationID, WorkID: workID, MediaVariantID: variantID, ConfiguredPath: rootPath, InventoryPath: rootPath, RelativePath: relative, SizeBytes: 42, MTime: now, HoldUUID: uuid.New()}
	validate := func() error {
		return db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
			if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
				return err
			}
			if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
				return err
			}
			return validateAndLockPublicationSource(ctx, tx, positionID, source, audio)
		})
	}
	if err := validate(); err != nil {
		t.Fatalf("valid source before cleanup owner: %v", err)
	}

	cleanupOperationID, cleanupItemID, riverJobID := uuid.New(), uuid.New(), int64(7)
	cleanupSnapshot := `{"version":1,"position_id":"` + positionID.String() + `","output_epoch":"` + uuid.NewString() + `"}`
	if _, err := db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,river_job_id,attempt,safe_error,finished_at) VALUES (?,'cleanup_publication_sources_v1','failed','failed',?::jsonb,?,1,'fixture failure',now())`, cleanupOperationID, cleanupSnapshot, riverJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_cleanup_item(id,operation_id,position_id,epoch,publication_generation,state,source_snapshot,borrower_revision) VALUES (?,?,?,? ,?,'queued','{}'::jsonb,?)`, cleanupItemID, cleanupOperationID, positionID, uuid.New(), uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_cleanup_owner(item_id,operation_id,application_attempt,river_job_id,owner_uuid,source_root_id,source_location_id,work_id) VALUES (?,?,1,?,?,?,?,?)`, cleanupItemID, cleanupOperationID, riverJobID, uuid.New(), rootID, locationID, workID); err != nil {
		t.Fatal(err)
	}
	if err := validate(); !errors.Is(err, ErrPublicationAdmissionConflict) {
		t.Fatalf("source with failed terminal cleanup owner error = %v, want ErrPublicationAdmissionConflict", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM source_cleanup_owner WHERE item_id=?`, cleanupItemID); err != nil {
		t.Fatal(err)
	}
	if err := validate(); err != nil {
		t.Fatalf("valid source after cleanup owner deletion: %v", err)
	}
}
