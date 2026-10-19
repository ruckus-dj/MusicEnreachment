//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	riverdatabasesql "github.com/riverqueue/river/riverdriver/riverdatabasesql"
	rivermigrate "github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	integrationtools "github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// This PostgreSQL test exercises the preparation journal through the real
// repository. It intentionally stops at preparation: cleanup authorization is
// covered separately by the publication cleanup flow tests.
func TestPublicationStagePreparationPersistsObservedInputFactsPostgreSQL(t *testing.T) {
	t.Parallel()
	testPublicationSourceExecution(t, "")
}

func TestPublicationExecutorAudioDeliveryPostgreSQL(t *testing.T) {
	for _, target := range []string{"Track.flac", "Artist/Album/Track.flac"} {
		t.Run(target, func(t *testing.T) { testPublicationSourceExecution(t, target) })
	}
}

func TestFirstPublicationPreviewConfirmationDeliveryPostgreSQL(t *testing.T) {
	for _, target := range []string{"Track.flac", "Artist/Album/Track.flac"} {
		t.Run(target, func(t *testing.T) {
			testPublicationSourceExecution(t, "Track.flac", true, target == "Artist/Album/Track.flac")
		})
	}
}

func testPublicationSourceExecution(t *testing.T, target string, confirmation ...bool) {
	t.Helper()
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
	sourceBytes := []byte("integration audio bytes")
	if target != "" {
		sourceBytes, err = os.ReadFile("../integrations/metadata/testdata/eg.flac")
		if err != nil {
			t.Fatal(err)
		}
		sourceBytes, err = flacFixtureWithoutPictures(sourceBytes)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(sourceFile, sourceBytes, 0o600); err != nil {
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
	inputs := persistence.PublicationInputs{Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion, IntentKind: "audio", Format: "source", DesiredTags: map[string][]string{}, Tools: tools, Audio: &persistence.PublicationAudioInput{
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
	if target != "" {
		output, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		toolsRoot, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(toolsRoot, "ffmpeg", "1")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte("controlled command transport"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		verified := time.Now().UTC()
		versions := map[string]string{}
		for _, name := range integrationtools.ExpectedExecutables(integrationtools.PackageFFmpeg, runtime.GOOS) {
			versions[name] = strings.TrimSuffix(name, ".exe") + " version 1"
		}
		executableVersions, err := json.Marshal(versions)
		if err != nil {
			t.Fatal(err)
		}
		installation := &persistence.ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "1", RelativePath: filepath.Join("ffmpeg", "1"), State: "ready", VerifiedAt: &verified, ArtifactIdentities: json.RawMessage(`{}`), ExecutableVersions: executableVersions}
		registry := settings.New(persistence.NewSettingsRepository(db), nil)
		if err := registry.SetToolsDirectory(ctx, toolsRoot, ""); err != nil {
			t.Fatal(err)
		}
		if err := registry.SetOutputDirectory(ctx, output, ""); err != nil {
			t.Fatal(err)
		}
		if err := manager.CreateInstallation(ctx, installation); err != nil {
			t.Fatal(err)
		}
		inputs.DesiredTags = map[string][]string{"TITLE": {"Published track"}, "ARTIST": {"Artist"}}
		for index := range inputs.Tools {
			inputs.Tools[index].RelativePath = "ffmpeg/1"
		}
		frozen, err := json.Marshal(inputs)
		if err != nil {
			t.Fatal(err)
		}
		nodes := []persistence.PublicationNamespaceNode{{CanonicalKey: strings.ToLower(target), RelativePath: target, NodeKind: "file"}}
		if strings.Contains(target, "/") {
			nodes = append([]persistence.PublicationNamespaceNode{{CanonicalKey: "artist", RelativePath: "Artist", NodeKind: "dir", DirectoryScope: "artist"}, {CanonicalKey: "artist/album", RelativePath: "Artist/Album", NodeKind: "dir", DirectoryScope: "album"}}, nodes...)
		}
		manifest, err := json.Marshal(map[string]any{"version": 1, "nodes": nodes})
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb, namespace_manifest=?::jsonb WHERE operation_id=?`, string(frozen), string(manifest), operationID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operation SET tools_read_required=true WHERE id=?`, operationID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?)`, operationID, installationID); err != nil {
			t.Fatal(err)
		}
		for _, node := range nodes {
			id := uuid.New()
			digest := sha256.Sum256([]byte(node.CanonicalKey))
			var owner, scope any
			if node.NodeKind == "file" {
				owner = positionID
			} else {
				scope = node.DirectoryScope
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key) VALUES (?,?,?,?,?,?,?,?)`, id, epoch, digest[:], node.CanonicalKey, node.RelativePath, node.NodeKind, owner, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, id, positionID, candidateID); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if len(confirmation) > 0 && confirmation[0] {
			// Remove only this fixture's synthetic admission. The source graph and
			// verified installation remain the exact inputs consumed by PreviewFirst.
			cleanup, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cleanup.Rollback() }()
			for _, table := range []string{"publication_source_hold", "operation_tool_read_hold", "publication_execution_claim", "track_publication_attempt"} {
				if _, err := cleanup.ExecContext(ctx, "DELETE FROM "+table+" WHERE operation_id=?", operationID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := cleanup.ExecContext(ctx, `DELETE FROM operation WHERE id=?`, operationID); err != nil {
				t.Fatal(err)
			}
			if _, err := cleanup.ExecContext(ctx, `DELETE FROM river_job WHERE id=?`, riverJobID); err != nil {
				t.Fatal(err)
			}
			if err := cleanup.Commit(); err != nil {
				t.Fatal(err)
			}
			// The synthetic admission reserved the same candidate namespace. Remove
			// only its generation reservations before exercising real admission.
			if _, err := db.ExecContext(ctx, `DELETE FROM publication_namespace_reservation WHERE position_id=? AND generation=?`, positionID, candidateID); err != nil {
				t.Fatal(err)
			}
			if err := persistence.NewSettingsRepository(db).Set(ctx, settings.PublicationPathTemplateKey, "Track.{EXT}"); err != nil {
				t.Fatal(err)
			}
			if err := persistence.NewSettingsRepository(db).SetMany(ctx, map[string]string{
				settings.PublicationFormatKey:        "source",
				settings.ActiveFFmpegInstallationKey: installationID.String(),
			}); err != nil {
				t.Fatal(err)
			}
			artistID := uuid.New()
			if _, err := db.ExecContext(ctx, `INSERT INTO local_artist(id,name) VALUES (?,'Artist')`, artistID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO release_artist(release_id,artist_id,position,name,name_join_phrase) VALUES (?,?,0,'Artist','')`, releaseID, artistID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO track_artist(track_id,artist_id,position,name,name_join_phrase) VALUES (?,?,0,'Artist','')`, trackID, artistID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE local_track SET title='Published track' WHERE id=?`, trackID); err != nil {
				t.Fatal(err)
			}
			publications := NewLibraryPublications(manager, registry, libraryPublicationTestInserter{}, integrationtools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
			if len(confirmation) > 1 && confirmation[1] {
				if err := persistence.NewSettingsRepository(db).Set(ctx, settings.PublicationPathTemplateKey, publication.DefaultTemplate); err != nil {
					t.Fatal(err)
				}
			}
			preview, err := publications.PreviewFirst(ctx, releaseID, positionID)
			if err != nil || len(preview.Blockers) != 0 || preview.SelectedSource == nil {
				t.Fatalf("first publication preview = %+v, %v", preview, err)
			}
			if len(confirmation) > 1 && confirmation[1] {
				if len(preview.PathComponents) != 4 {
					t.Fatalf("default-layout preview components = %#v, want 4", preview.PathComponents)
				}
				target = strings.Join(preview.PathComponents, "/")
			}
			admittedID, err := publications.AdmitFirst(ctx, releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: epoch})
			if err != nil {
				t.Fatalf("confirm first publication: %v", err)
			}
			operationID = admittedID
			attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
			if err != nil || attempt == nil {
				t.Fatalf("read admitted attempt = %+v, %v", attempt, err)
			}
			candidateID = attempt.CandidateGeneration
			op, err := manager.GetOperation(ctx, operationID)
			if err != nil || op.RiverJobID == nil {
				t.Fatalf("read admitted operation = %+v, %v", op, err)
			}
			riverJobID = *op.RiverJobID
			if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1 WHERE id=?`, riverJobID); err != nil {
				t.Fatal(err)
			}
		}
		executor := NewPublicationExecutor(manager, registry, integrationtools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, publication.NewCoordinator(), uuid.New(), audioDeliveryTestCommands{})
		err = executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 1)
		// Newly created ancestors require directory sync, unsupported on Windows.
		// Root-level first-absent installation has its own native success contract.
		if runtime.GOOS == "windows" && strings.Contains(target, "/") {
			if !errors.Is(err, sourcefs.ErrUnsupported) {
				t.Fatalf("expected unsupported directory durability: %v", err)
			}
			if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(target))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected exposed target: %v", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		capture, err := (metadata.Reader{}).Read(ctx, filepath.Join(output, filepath.FromSlash(target)), metadata.ReadRequest{CoverInspection: true})
		if err != nil || len(capture.Tags["TITLE"]) != 1 || capture.Tags["TITLE"][0] != "Published track" {
			t.Fatalf("published metadata = %#v, %v", capture.Tags, err)
		}
		var current persistence.TrackPublication
		if err := db.NewSelect().Model(&current).Where("position_id=?", positionID).Scan(ctx); err != nil || current.Generation != candidateID {
			t.Fatalf("current generation: %v", err)
		}
		for _, table := range []string{"track_publication_attempt", "publication_execution_claim", "publication_source_hold", "operation_tool_read_hold"} {
			var count int
			if err := db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, operationID).Scan(ctx, &count); err != nil || count != 0 {
				t.Fatalf("retained %s = %d, %v", table, count, err)
			}
		}
		op, err := manager.GetOperation(ctx, operationID)
		if err != nil || op.State != "succeeded" {
			t.Fatalf("publication operation = %+v, %v", op, err)
		}
		actualSource, err := os.ReadFile(sourceFile)
		if err != nil || !bytes.Equal(actualSource, sourceBytes) {
			t.Fatal("source changed during publication")
		}
		return
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

// This transport exercises dispatch/settlement with real FLAC bytes, not codec
// or executable compatibility. All metadata and filesystem readback stay native.
type audioDeliveryTestCommands struct{}

func (audioDeliveryTestCommands) Run(ctx context.Context, executable string, args ...string) (PublicationCommandResult, error) {
	result := PublicationCommandResult{QuiescenceEstablished: true}
	if len(args) == 1 && args[0] == "-version" {
		name := strings.TrimSuffix(filepath.Base(executable), ".exe")
		result.Output = []byte(name + " version 1")
		return result, nil
	}
	var input string
	for i, arg := range args {
		if arg == "-i" && i+1 < len(args) {
			input = args[i+1]
		}
	}
	if input == "" || len(args) == 0 {
		return result, fmt.Errorf("unexpected audio test command: %v", args)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	contents, err := os.ReadFile(input)
	if err != nil {
		return result, err
	}
	file, err := os.OpenFile(args[len(args)-1], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return result, err
	}
	_, writeErr := file.Write(contents)
	return result, errors.Join(writeErr, file.Close())
}

func (audioDeliveryTestCommands) RunFile(_ context.Context, request integrationtools.FileCommandRequest) (PublicationCommandResult, error) {
	if request.Input == nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("missing borrowed audio input")
	}
	return PublicationCommandResult{QuiescenceEstablished: true, Output: []byte(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}`)}, nil
}
