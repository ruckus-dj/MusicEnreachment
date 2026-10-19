//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestAdmitFirstPublicationFromPreviewPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"success", "callback error", "stale source", "enqueue rollback", "detached callback input"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := testpostgres.OpenMigrated(t)
			initializeRiver(t, ctx, db)
			repo := NewSetupManagerRepository(db)
			releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
			if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'first admission fixture')`, releaseID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'first admission fixture')`, trackID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
				t.Fatal(err)
			}
			var epoch uuid.UUID
			if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
				t.Fatal(err)
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
			if scenario == "stale source" {
				if _, err := db.ExecContext(ctx, `UPDATE source_location SET relative_path='renamed.flac' WHERE id=?`, locationID); err != nil {
					t.Fatal(err)
				}
			}
			installationID := uuid.New()
			verified := time.Now().UTC()
			versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
			installPath := "ffmpeg/release-1"
			if runtime.GOOS == "windows" {
				installPath = `ffmpeg\release-1`
			}
			if err := repo.CreateInstallation(ctx, &ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "release-1", RelativePath: installPath, State: "ready", ExecutableVersions: versions, ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified}); err != nil {
				t.Fatal(err)
			}
			frozenPath := filepath.ToSlash(installPath)
			selections := []SourceAnalysisToolSelection{{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenPath, Executable: "ffmpeg", Version: "7.0", VersionBanner: ffmpegBanner}, {PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenPath, Executable: "ffprobe", Version: "7.0", VersionBanner: ffprobeBanner}}
			lossless, channels := true, 2
			audioFacts := PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true}
			input := PublicationInputs{Version: PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion, IntentKind: "audio", Audio: &PublicationAudioInput{WorkID: workID, RootID: rootID, LocationID: locationID, MediaVariantID: variantID, ProbeResultID: variantID, ConfiguredRootPath: rootPath, InventoryRootPath: rootPath, RelativePath: relative, SizeBytes: 42, MTime: now, AnalysisPolicyVersion: 1, FFProbeVersion: "ffprobe 1", AudioFacts: audioFacts}, Format: publication.AudioFormatSource, DesiredTags: map[string][]string{"TITLE": {"Fixture"}}, Tools: selections}
			frozen, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			source := &PublicationSourceSelection{RootID: rootID, LocationID: locationID, WorkID: workID, MediaVariantID: variantID, ConfiguredPath: rootPath, InventoryPath: rootPath, RelativePath: relative, SizeBytes: 42, MTime: now, HoldUUID: uuid.New()}
			request := PublicationAdmission{PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: epoch, CandidateGeneration: uuid.New(), IntentKind: "audio", ToolsReadRequired: true, Tools: selections, Source: source, FrozenInputs: frozen, NamespaceManifest: []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "root/provisional.flac", RelativePath: "root/provisional.flac", NodeKind: "file"}}}
			callbackErr := errors.New("planner rejected preview")
			inserter := &publicationAdmissionInserter{fail: scenario == "enqueue rollback"}
			called := false
			first := FirstPublicationAdmission{PublicationAdmission: request, PreviewDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PrepareFirst: func(_ context.Context, snap FirstPublicationAdmissionSnapshot, got PublicationAdmission) ([]PublicationNamespaceNode, error) {
				called = true
				if snap.PreviewDigest != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
					t.Errorf("digest %q", snap.PreviewDigest)
				}
				if snap.Context == nil || snap.Context.Library == nil || snap.Context.Library.ActiveEpoch != epoch || len(snap.Context.Sources) != 1 || snap.Context.Sources[0].Selection.LocationID != locationID {
					t.Errorf("authoritative context %+v", snap.Context)
				}
				if len(snap.Installations) != 1 || len(snap.Settings) == 0 { /* settings are deployment-dependent */
					if len(snap.Installations) != 1 {
						t.Errorf("installations %+v", snap.Installations)
					}
				}
				if got.Source == nil || got.Source.LocationID != locationID || len(got.Tools) != 2 {
					t.Errorf("planner request %+v", got)
				}
				if scenario == "callback error" {
					return nil, callbackErr
				}
				if scenario == "detached callback input" {
					got.FrozenInputs[0] = ' '
					got.Tools[0].Version = "mutated"
				}
				return []PublicationNamespaceNode{{CanonicalKey: "final", RelativePath: "Русский", NodeKind: "dir", DirectoryScope: "final"}, {CanonicalKey: "final/Русский.flac", RelativePath: "Русский/Русский.flac", NodeKind: "file"}}, nil
			}}
			op, err := repo.AdmitFirstPublicationFromPreview(ctx, first, inserter)
			if scenario == "callback error" {
				if !errors.Is(err, callbackErr) || !called {
					t.Fatalf("error %v, called %v", err, called)
				}
				assertNoAdmissionArtifacts(t, ctx, db, request, nil)
				return
			}
			if scenario == "stale source" {
				if !errors.Is(err, ErrPublicationAdmissionConflict) {
					t.Fatalf("stale source error %v", err)
				}
				assertNoAdmissionArtifacts(t, ctx, db, request, nil)
				return
			}
			if scenario == "enqueue rollback" {
				if err == nil {
					t.Fatal("expected enqueue failure")
				}
				assertNoAdmissionArtifacts(t, ctx, db, request, nil)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "detached callback input" {
				original, err := DecodePublicationInputs(request.FrozenInputs)
				if err != nil || original.Tools[0].Version != "7.0" {
					t.Fatalf("original frozen inputs mutated: %+v %v", original, err)
				}
			}
			if inserter.args.Kind() != "publish_track_v1" || inserter.args.(publication.TrackArgs).OperationID != op {
				t.Fatalf("job args %+v", inserter.args)
			}
			var count int
			if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation r JOIN publication_namespace_claim c ON c.id=r.namespace_claim_id WHERE r.position_id=? AND r.generation=? AND c.relative_path='Русский/Русский.flac'`, positionID, request.CandidateGeneration).Scan(ctx, &count); err != nil || count != 1 {
				t.Fatalf("final manifest reservation %d (%v)", count, err)
			}
			if err := db.NewRaw(`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, op).Scan(ctx, &count); err != nil || count != 1 {
				t.Fatalf("tool holds %d (%v)", count, err)
			}
			if err := db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, op).Scan(ctx, &count); err != nil || count != 1 {
				t.Fatalf("source holds %d (%v)", count, err)
			}
		})
	}
}

var _ RiverInserter = (*publicationAdmissionInserter)(nil)
