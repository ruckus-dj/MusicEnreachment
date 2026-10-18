//go:build integration

package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	riverdatabasesql "github.com/riverqueue/river/riverdriver/riverdatabasesql"
	rivermigrate "github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// This PostgreSQL test exercises the preparation journal through the real
// repository. It intentionally stops at preparation: cleanup authorization is
// covered separately by the publication cleanup flow tests.
func TestPublicationStagePreparationPersistsObservedInputFactsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	driver := riverdatabasesql.New(db.DB)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatalf("create River migrator: %v", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}
	manager := persistence.NewSetupManagerRepository(db)
	inventory := persistence.NewSourceInventoryRepository(db)

	sourceRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(sourceRoot, "track.flac")
	if err := os.WriteFile(sourceFile, []byte("integration audio bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldMTime := time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(sourceFile, oldMTime, oldMTime); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	oldMTime = info.ModTime().UTC().Truncate(time.Microsecond)

	positionID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'preparation fixture')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'preparation fixture')`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
		t.Fatal(err)
	}
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	rootID, locationID, workID, variantID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,?, 'Source',1,?,true,'available')`, rootID, sourceRoot, sourceRoot); err != nil {
		t.Fatal(err)
	}
	scanOperationID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, scanOperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?, ?,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}'::jsonb,'{}'::jsonb,?,?,1)`, variantID, info.Size(), oldMTime, scanOperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?, 'track.flac',?,?,1,'audio',?)`, locationID, rootID, info.Size(), oldMTime, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?, ?,?,?,'track.flac',?,?,false,?,?)`, workID, locationID, rootID, sourceRoot, sourceRoot, info.Size(), oldMTime, scanOperationID, locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, workID, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'executed')`, workID, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,true,true,'manual','{}'::jsonb)`, uuid.New(), trackID, variantID); err != nil {
		t.Fatal(err)
	}
	installationID := uuid.New()
	tools := []persistence.SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffmpeg", Executable: "ffmpeg", Version: "1", VersionBanner: "ffmpeg version 1"},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffmpeg", Executable: "ffprobe", Version: "1", VersionBanner: "ffprobe version 1"},
	}
	inputs := persistence.PublicationInputs{Version: persistence.PublicationInputsVersion, IntentKind: "audio", Format: "source", DesiredTags: map[string][]string{}, Tools: tools, Audio: &persistence.PublicationAudioInput{
		WorkID: workID, RootID: rootID, LocationID: locationID, MediaVariantID: variantID, ProbeResultID: variantID,
		ConfiguredRootPath: sourceRoot, InventoryRootPath: sourceRoot, RelativePath: "track.flac", SizeBytes: info.Size(), MTime: oldMTime,
		AnalysisPolicyVersion: 1, FFProbeVersion: "ffprobe 1", AudioFacts: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: preparationBoolPointer(true), Channels: preparationIntPointer(2), Container: "flac", SupportedSourceContainer: true},
	}}
	frozen, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	operationID, candidateID, holdID := uuid.New(), uuid.New(), uuid.New()
	var riverJobID int64
	if err := db.NewRaw(`INSERT INTO river_job(kind,args,state,attempt) VALUES ('publish_track_v1','{}','running',1) RETURNING id`).Scan(ctx, &riverJobID); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(map[string]any{"version": 1, "position_id": positionID, "output_epoch": epoch, "intent_kind": "audio"})
	if _, err := db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id,finished_at) VALUES (?,'publish_track_v1','succeeded','queued',?::jsonb,1,?,now())`, operationID, string(snapshot), riverJobID); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"version": 1, "nodes": []persistence.PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "track.flac", RelativePath: "track.flac", NodeKind: "file"}}})
	if _, err := db.ExecContext(ctx, `INSERT INTO track_publication_attempt(operation_id,position_id,epoch,expected_generation,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest,mutation_journal) VALUES (?,?,?,NULL,?,'audio','queued',?::jsonb,?::jsonb,'[]')`, operationID, positionID, epoch, candidateID, string(frozen), string(manifest)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE operation SET state='queued', finished_at=NULL WHERE id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO publication_source_hold(operation_id,application_attempt,river_job_id,epoch,hold_uuid,source_root_id,source_location_id,work_id,media_variant_id,configured_path,inventory_path,relative_path,size_bytes,mtime) VALUES (?,1,?,?,?,?,?,?,?,?,?,'track.flac',?,?)`, operationID, riverJobID, epoch, holdID, rootID, locationID, workID, variantID, sourceRoot, sourceRoot, info.Size(), oldMTime); err != nil {
		t.Fatal(err)
	}
	guard := persistence.PublicationDeliveryGuard{OperationID: operationID, ApplicationAttempt: 1, RiverJobID: riverJobID, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(), Epoch: epoch, CandidateGeneration: candidateID, ExpectedPositionID: positionID}
	if outcome, err := manager.ClaimPublicationDelivery(ctx, guard); err != nil || outcome != persistence.PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	output, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := sourcefs.NewOutputOpener().OpenRoot(ctx, output)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	preparer := PublicationStagePreparer{Repository: manager, SourceOpener: sourcefs.NewOpener(), OutputParent: parent, ParentPath: output, HoldUUID: holdID}
	stage, err := preparer.Prepare(ctx, guard, persistence.PublicationDeliveryAcquired, true)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	var journal []byte
	if err := db.NewRaw(`SELECT mutation_journal FROM track_publication_attempt WHERE operation_id=?`, operationID).Scan(ctx, &journal); err != nil {
		t.Fatal(err)
	}
	var records []persistence.PublicationPreparationRecord
	if err := json.Unmarshal(journal, &records); err != nil {
		t.Fatalf("decode persisted preparation journal: %v", err)
	}
	var inputReady *persistence.PublicationPreparationRecord
	for i := range records {
		if records[i].Step == "input_ready" {
			inputReady = &records[i]
		}
	}
	if inputReady == nil || inputReady.InputMTime == nil || *inputReady.InputMTime != oldMTime.UnixMicro() {
		t.Fatalf("input_ready source mtime = %+v, want %d", inputReady, oldMTime.UnixMicro())
	}
	privateRoot, err := sourcefs.NewOpener().OpenRoot(ctx, filepath.Dir(stage.Paths().InputPath))
	if err != nil {
		t.Fatal(err)
	}
	privateInput, err := sourcefs.OpenRegularAt(ctx, privateRoot, filepath.Base(stage.Paths().InputPath))
	if err != nil {
		_ = privateRoot.Close()
		t.Fatal(err)
	}
	privateInfo, err := privateInput.Stat(ctx)
	if err != nil {
		_ = privateInput.Close()
		_ = privateRoot.Close()
		t.Fatal(err)
	}
	if err := privateInput.Close(); err != nil {
		t.Fatal(err)
	}
	if err := privateRoot.Close(); err != nil {
		t.Fatal(err)
	}
	privateMTime := privateInfo.ModTime().UTC().Truncate(time.Microsecond).UnixMicro()
	if inputReady.CopiedInput == nil || inputReady.CopiedInput.SizeBytes == nil || inputReady.CopiedInput.MTime == nil ||
		*inputReady.CopiedInput.SizeBytes != privateInfo.Size() || *inputReady.CopiedInput.MTime != privateMTime {
		t.Fatalf("copied input facts = %+v, want handle-observed private-copy size=%d mtime=%d", inputReady.CopiedInput, privateInfo.Size(), privateMTime)
	}
	if _, err := inventory.GetSourceRoot(ctx, rootID); err != nil {
		t.Fatalf("read seeded source root through inventory repository: %v", err)
	}
}

func preparationBoolPointer(value bool) *bool { return &value }

func preparationIntPointer(value int) *int { return &value }
