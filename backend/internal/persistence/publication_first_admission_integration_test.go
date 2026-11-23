//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

type firstAdmissionFixture struct {
	ctx        context.Context
	db         *bun.DB
	repo       *SetupManagerRepository
	epoch      uuid.UUID
	rootPath   string
	rootID     uuid.UUID
	locationID uuid.UUID
	workID     uuid.UUID
	variantID  uuid.UUID
	now        time.Time
	selections []SourceAnalysisToolSelection
	audioFacts PersistedAudioFacts
}

func newFirstAdmissionFixture(t *testing.T, ctx context.Context, db *bun.DB) *firstAdmissionFixture {
	t.Helper()
	repo := NewSetupManagerRepository(db)
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
	if _, err := db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,?, 'Source',1,?,true,'available')`, rootID, rootPath, rootPath); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, scanID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?,42,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}','{}'::jsonb,?,?,1)`, variantID, now, scanID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,?,42,?,1,'audio',?)`, locationID, rootID, relative, now, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,?,42,?,false,?,?)`, workID, locationID, rootID, rootPath, rootPath, relative, now, scanID, locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, workID, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'executed')`, workID, variantID); err != nil {
		t.Fatal(err)
	}
	installationID := uuid.New()
	verified := time.Now().UTC()
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	installPath := "ffmpeg/release-1"
	if runtime.GOOS == "windows" {
		installPath = `ffmpeg\release-1`
	}
	if err := repo.CreateInstallation(ctx, &ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "release-1", RelativePath: installPath, State: "ready", ExecutableVerificationOutput: versions, Version: "ffmpeg 7.0 + ffprobe 7.0", ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified}); err != nil {
		t.Fatal(err)
	}
	frozenPath := filepath.ToSlash(installPath)
	selections := []SourceAnalysisToolSelection{{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenPath, Executable: "ffmpeg", Version: "ffmpeg 7.0 + ffprobe 7.0", VersionBanner: ffmpegBanner}, {PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenPath, Executable: "ffprobe", Version: "ffmpeg 7.0 + ffprobe 7.0", VersionBanner: ffprobeBanner}}
	lossless, channels := true, 2
	audioFacts := PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true}
	return &firstAdmissionFixture{ctx: ctx, db: db, repo: repo, epoch: epoch, rootPath: rootPath, rootID: rootID,
		locationID: locationID, workID: workID, variantID: variantID, now: now, selections: selections, audioFacts: audioFacts}
}

func seedRecordingCreditBaselineTx(ctx context.Context, db bun.IDB, positionID, trackID uuid.UUID, recordingTitle, artistName string) error {
	artistID, recordingID := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_artist(id,name) VALUES (?,?)`, artistID, artistName); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_recording(id,title) VALUES (?,?)`, recordingID, recordingTitle); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO recording_artist(recording_id,position,artist_id,name,name_join_phrase) VALUES (?,?,?,?,'')`, recordingID, 0, artistID, artistName); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `UPDATE release_track SET recording_id=? WHERE id=?`, recordingID, positionID); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO track_artist(track_id,artist_id,position,name,name_join_phrase) VALUES (?,?,0,?,'')`, trackID, artistID, artistName); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_artist(release_id,position,artist_id,name,name_join_phrase)
		SELECT release_id,0,?,?,''
		FROM release_track WHERE id=?`, artistID, artistName, positionID); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `UPDATE local_track SET metadata=jsonb_set(metadata,'{artist_credit_origin}','{"version":1,"kind":"recording_fallback"}'::jsonb,true) WHERE id=?`, trackID); err != nil {
		return err
	}
	return nil
}

var _ RiverInserter = (*publicationAdmissionInserter)(nil)
