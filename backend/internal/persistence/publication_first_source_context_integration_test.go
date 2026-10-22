//go:build integration

package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestReadLibraryFirstPublicationContextUsesCurrentCompatibleAssociatedSourcePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	repo := NewSetupManagerRepository(db)

	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'source context fixture')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'source context fixture')`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
		t.Fatal(err)
	}

	const configuredPath = "/library/source"
	const relativePath = "track.flac"
	const sizeBytes int64 = 42
	mtime := time.Date(2025, time.January, 2, 3, 4, 5, 123456000, time.UTC)
	rootID, locationID, workID, variantID, probeResultID, scanOperationID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,?, 'Source',1,?,true,'available')`, rootID, configuredPath, configuredPath); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, scanOperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,?)`, variantID, sizeBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?, ?,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}'::jsonb,'{}'::jsonb,?,?,1)`, probeResultID, sizeBytes, mtime, scanOperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?, ?,?,?,1,'audio',?)`, locationID, rootID, relativePath, sizeBytes, mtime, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,?,?, ?,false,?,?)`, workID, locationID, rootID, configuredPath, configuredPath, relativePath, sizeBytes, mtime, scanOperationID, locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, workID, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'sha256')`, workID, probeResultID); err != nil {
		t.Fatal(err)
	}
	associationID := uuid.New()
	secondAssociationID, secondVariantID := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,?)`, secondVariantID, sizeBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,metadata) VALUES (?,?,?,false,true,'automatic','policy-v1','{}'::jsonb),(?,?,?,false,true,'automatic','policy-v1','{}'::jsonb)`, associationID, trackID, variantID, secondAssociationID, trackID, secondVariantID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		t.Fatalf("read first publication context: %v", err)
	}
	if len(got.Sources) != 1 || len(got.Blockers) != 0 {
		t.Fatalf("source context = sources %d, blockers %v", len(got.Sources), got.Blockers)
	}
	if len(got.SourceAssociations) != 2 || got.SourceAssociations[0].ID.String() >= got.SourceAssociations[1].ID.String() {
		t.Fatalf("source associations were not fully captured in deterministic order: %+v", got.SourceAssociations)
	}
	associationIDs := map[uuid.UUID]struct{}{associationID: {}, secondAssociationID: {}}
	for _, association := range got.SourceAssociations {
		if _, exists := associationIDs[association.ID]; !exists {
			t.Fatalf("unexpected association in witness: %+v", association)
		}
		if association.TrackID != trackID || association.Selected || !association.Compatible || association.DecisionMethod != "automatic" ||
			association.PolicyVersion == nil || *association.PolicyVersion != "policy-v1" || string(association.Metadata) != `{}` || association.UpdatedAt.IsZero() {
			t.Fatalf("source association witness = %+v", association)
		}
	}
	if len(got.Sources) != 1 {
		t.Fatalf("eligible sources = %+v", got.Sources)
	}
	source := got.Sources[0]
	if source.AssociationID != associationID || source.Selected {
		t.Fatalf("candidate association = %s selected=%v", source.AssociationID, source.Selected)
	}
	if source.Selection.RootID != rootID || source.Selection.LocationID != locationID || source.Selection.WorkID != workID || source.Selection.MediaVariantID != variantID ||
		source.Selection.ConfiguredPath != configuredPath || source.Selection.InventoryPath != configuredPath || source.Selection.RelativePath != relativePath ||
		source.Selection.SizeBytes != sizeBytes || !source.Selection.MTime.Equal(mtime) {
		t.Fatalf("selected source = %+v", source.Selection)
	}
	if source.Input.ProbeResultID != probeResultID || source.Input.MediaVariantID != variantID || source.Quality.StreamIndex != 0 {
		t.Fatalf("probe and quality facts = input %+v quality %+v", source.Input, source.Quality)
	}

	if _, err := db.ExecContext(ctx, `UPDATE track_source SET selected=true WHERE id=?`, associationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE source_location SET relative_path='renamed.flac' WHERE id=?`, locationID); err != nil {
		t.Fatal(err)
	}
	got, err = repo.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		t.Fatalf("read context after inventory location changed: %v", err)
	}
	if len(got.Sources) != 0 || len(got.DefaultSourceBlockers) != 1 || got.DefaultSourceBlockers[0] != "one or more selected source inventory locations are stale" {
		t.Fatalf("changed location context = sources %+v, default source blockers %v", got.Sources, got.DefaultSourceBlockers)
	}
}

func TestReadLibraryFirstPublicationContextKeepsHealthySelectedAlternativePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	repo := NewSetupManagerRepository(db)

	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'alternative fixture')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'alternative fixture')`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
		t.Fatal(err)
	}
	const rootPath = "/library/alternatives"
	const size int64 = 42
	mtime := time.Date(2025, time.January, 2, 3, 4, 5, 123456000, time.UTC)
	rootID, scanID := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,?,'Source',1,?,true,'available')`, rootID, rootPath, rootPath); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, scanID); err != nil {
		t.Fatal(err)
	}
	good := insertPublicationContextWork(t, ctx, db, rootID, scanID, trackID, rootPath, "good.flac", size, size, mtime, true, false)
	bad := insertPublicationContextWork(t, ctx, db, rootID, scanID, trackID, rootPath, "stale.flac", size+1, size, mtime, false, true)
	got, err := repo.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		t.Fatalf("read source context with healthy and stale candidates: %v", err)
	}
	if len(got.Sources) != 1 || got.Sources[0].Selection.LocationID != good.locationID || got.Sources[0].Selected {
		t.Fatalf("eligible sources = %+v, want only healthy alternate source %s", got.Sources, good.locationID)
	}
	if len(got.DefaultSourceBlockers) != 0 {
		t.Fatalf("stale selected sibling blocked healthy alternate candidate: %v", got.DefaultSourceBlockers)
	}

	if _, err := db.ExecContext(ctx, `UPDATE source_location SET relative_path='moved.flac' WHERE id=?`, good.locationID); err != nil {
		t.Fatal(err)
	}
	got, err = repo.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		t.Fatalf("read context after all candidates became stale: %v", err)
	}
	if len(got.Sources) != 0 || len(got.DefaultSourceBlockers) == 0 {
		t.Fatalf("stale and failed candidates were not excluded: sources=%+v blockers=%v bad=%+v", got.Sources, got.DefaultSourceBlockers, bad)
	}
}

type publicationContextWork struct {
	locationID uuid.UUID
}

func insertPublicationContextWork(t *testing.T, ctx context.Context, db *bun.DB, rootID, scanID, trackID uuid.UUID, rootPath, relative string, locationSize, workSize int64, mtime time.Time, successfulProbe, selected bool) publicationContextWork {
	t.Helper()
	locationID, workID, identityID, probeID, associationID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,?)`, identityID, workSize); err != nil {
		t.Fatal(err)
	}
	if successfulProbe {
		if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?, ?,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}'::jsonb,'{}'::jsonb,?,?,1)`, probeID, workSize, mtime, scanID); err != nil {
			t.Fatal(err)
		}
	}
	probeStatus, probeState := "audio", "failed"
	var successProbe any
	var reuseOrigin any
	var safeError any = "probe failed"
	if successfulProbe {
		probeState, successProbe, reuseOrigin, safeError = "succeeded", probeID, "sha256", nil
	}
	if !successfulProbe {
		probeStatus = "not_analyzed"
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?, ?,?,?,1,?,?)`, locationID, rootID, relative, locationSize, mtime, probeStatus, identityID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,?,?, ?,false,?,?)`, workID, locationID, rootID, rootPath, rootPath, relative, workSize, mtime, scanID, locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, workID, identityID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin,safe_error) VALUES (?,'probe',?,?,?,?)`, workID, probeState, successProbe, reuseOrigin, safeError); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,metadata) VALUES (?,?,?, ?,true,'automatic','policy-v1','{}'::jsonb)`, associationID, trackID, identityID, selected); err != nil {
		t.Fatal(err)
	}
	return publicationContextWork{locationID: locationID}
}
