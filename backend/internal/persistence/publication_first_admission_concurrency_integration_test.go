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

// This exercises the actual first-admission callback while its SHARE table
// guards are held. A previously absent track_source association must not be
// inserted until admission commits.
func TestFirstPublicationAdmissionGuardsPhantomTrackSourcePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	initializeRiver(t, ctx, db)
	repo := NewSetupManagerRepository(db)
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,'first admission concurrency')`, []any{releaseID}},
		{`INSERT INTO local_track(id,title) VALUES (?,'first admission concurrency')`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, []any{positionID, releaseID, trackID}},
	} {
		if _, err := db.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize fixture root: %v", err)
	}
	now := time.Date(2025, 1, 2, 3, 4, 5, 123456000, time.UTC)
	rootID, locationID, workID, variantID, scanID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,?, 'Source',1,?,true,'available')`, []any{rootID, rootPath, rootPath}},
		{`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, []any{scanID}},
		{`INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?,42,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}','{}'::jsonb,?,?,1)`, []any{variantID, now, scanID}},
		{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?, 'track.flac',42,?,1,'audio',?)`, []any{locationID, rootID, now, variantID}},
		{`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,'track.flac',42,?,false,?,?)`, []any{workID, locationID, rootID, rootPath, rootPath, now, scanID, locationID}},
		{`INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, []any{workID, variantID}},
		{`INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'executed')`, []any{workID, variantID}},
		{`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,true,true,'manual','{}'::jsonb)`, []any{uuid.New(), trackID, variantID}},
	}
	for _, row := range seed {
		if _, err := db.ExecContext(ctx, row.query, row.args...); err != nil {
			t.Fatal(err)
		}
	}
	installationID := uuid.New()
	verified := time.Now().UTC()
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	installPath := "ffmpeg/release-concurrency"
	if runtime.GOOS == "windows" {
		installPath = `ffmpeg\release-concurrency`
	}
	if err := repo.CreateInstallation(ctx, &ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "release-concurrency", RelativePath: installPath, State: "ready", ExecutableVersions: versions, ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified}); err != nil {
		t.Fatal(err)
	}
	tools := []SourceAnalysisToolSelection{{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: filepath.ToSlash(installPath), Executable: "ffmpeg", Version: "7.0", VersionBanner: ffmpegBanner}, {PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: filepath.ToSlash(installPath), Executable: "ffprobe", Version: "7.0", VersionBanner: ffprobeBanner}}
	lossless, channels := true, 2
	input := PublicationInputs{Version: PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion, IntentKind: "audio", InitialLyrics: &PublicationInitialLyricsInput{ExpectedMetadataRevision: 0}, Audio: &PublicationAudioInput{WorkID: workID, RootID: rootID, LocationID: locationID, MediaVariantID: variantID, ProbeResultID: variantID, ConfiguredRootPath: rootPath, InventoryRootPath: rootPath, RelativePath: "track.flac", SizeBytes: 42, MTime: now, AnalysisPolicyVersion: 1, FFProbeVersion: "ffprobe 1", AudioFacts: PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true}}, Format: publication.AudioFormatSource, DesiredTags: map[string][]string{"TITLE": {"Fixture"}}, Tools: tools}
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	newTrackID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'phantom association target')`, newTrackID); err != nil {
		t.Fatal(err)
	}
	request := PublicationAdmission{PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: epoch, CandidateGeneration: uuid.New(), IntentKind: "audio", ToolsReadRequired: true, Tools: tools, Source: &PublicationSourceSelection{RootID: rootID, LocationID: locationID, WorkID: workID, MediaVariantID: variantID, ConfiguredPath: rootPath, InventoryPath: rootPath, RelativePath: "track.flac", SizeBytes: 42, MTime: now, HoldUUID: uuid.New()}, FrozenInputs: frozen, NamespaceManifest: []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "root/provisional.flac", RelativePath: "root/provisional.flac", NodeKind: "file"}}}
	entered := make(chan struct{})
	writerDone := make(chan error, 1)
	writerStarted := make(chan struct{})
	finished := false
	first := FirstPublicationAdmission{PublicationAdmission: request, PreviewDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PrepareFirst: func(_ context.Context, _ FirstPublicationAdmissionSnapshot, _ PublicationAdmission) ([]PublicationNamespaceNode, error) {
		close(entered)
		go func() {
			close(writerStarted)
			_, e := db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,false,true,'manual','{}'::jsonb)`, uuid.New(), newTrackID, variantID)
			writerDone <- e
		}()
		<-writerStarted
		select {
		case e := <-writerDone:
			t.Errorf("track_source insert escaped admission SHARE guard: %v", e)
			return nil, errors.New("phantom association was not blocked")
		case <-time.After(150 * time.Millisecond):
		}
		return []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "root/final.flac", RelativePath: "root/final.flac", NodeKind: "file"}}, nil
	}}
	if _, err := repo.AdmitFirstPublicationFromPreview(ctx, first, new(publicationAdmissionInserter)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatalf("writer failed after admission committed: %v", err)
		}
		finished = true
	case <-time.After(5 * time.Second):
		t.Fatal("blocked association insert did not resume after admission commit")
	}
	if !finished {
		t.Fatal("association insert did not complete")
	}
}
