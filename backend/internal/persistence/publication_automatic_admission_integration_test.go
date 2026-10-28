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

// This exercises the automatic wrapper's real delivery fence. A plausible but
// unclaimed delivery must roll back the planner's admission and all artifacts.
func TestAdmitAutomaticFirstPublicationRejectsStaleGuardPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	initializeRiver(t, ctx, db)
	repo := NewSetupManagerRepository(db)
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'automatic admission')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'track')`, trackID); err != nil {
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
		t.Fatal(err)
	}
	now := time.Date(2025, time.January, 2, 3, 4, 5, 123456000, time.UTC)
	rootID, locationID, workID, variantID, scanID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	queries := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,?, 'Source',1,?,true,'available')`, []any{rootID, rootPath, rootPath}},
		{`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, []any{scanID}},
		{`INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?,42,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}','{}'::jsonb,?,?,1)`, []any{variantID, now, scanID}},
		{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,?,42,?,1,'audio',?)`, []any{locationID, rootID, "track.flac", now, variantID}},
		{`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,?,42,?,false,?,?)`, []any{workID, locationID, rootID, rootPath, rootPath, "track.flac", now, scanID, locationID}},
		{`INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, []any{workID, variantID}},
		{`INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'executed')`, []any{workID, variantID}},
		{`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,true,true,'manual','{}'::jsonb)`, []any{uuid.New(), trackID, variantID}},
	}
	for _, q := range queries {
		if _, err := db.ExecContext(ctx, q.query, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	installationID := uuid.New()
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	installPath := "ffmpeg/release-1"
	if runtime.GOOS == "windows" {
		installPath = `ffmpeg\release-1`
	}
	verified := time.Now().UTC()
	if err := repo.CreateInstallation(ctx, &ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "release-1", RelativePath: installPath, State: "ready", ExecutableVerificationOutput: versions, Version: "ffmpeg 7.0 + ffprobe 7.0", ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified}); err != nil {
		t.Fatal(err)
	}
	selections := []SourceAnalysisToolSelection{{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: filepath.ToSlash(installPath), Executable: "ffmpeg", Version: "ffmpeg 7.0 + ffprobe 7.0", VersionBanner: ffmpegBanner}, {PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: filepath.ToSlash(installPath), Executable: "ffprobe", Version: "ffmpeg 7.0 + ffprobe 7.0", VersionBanner: ffprobeBanner}}
	lossless, channels := true, 2
	input := PublicationInputs{Version: PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion, IntentKind: "audio", InitialLyrics: &PublicationInitialLyricsInput{ExpectedMetadataRevision: 0}, Audio: &PublicationAudioInput{WorkID: workID, RootID: rootID, LocationID: locationID, MediaVariantID: variantID, ProbeResultID: variantID, ConfiguredRootPath: rootPath, InventoryRootPath: rootPath, RelativePath: "track.flac", SizeBytes: 42, MTime: now, AnalysisPolicyVersion: 1, FFProbeVersion: "ffprobe 1", AudioFacts: PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true}}, Format: publication.AudioFormatSource, DesiredTags: map[string][]string{"TITLE": {"Fixture"}}, Tools: selections}
	input.SourceAuthorityMode = "automatic"
	var associations []PublicationSourceAssociation
	if err := db.NewRaw(`SELECT id,track_id,media_variant_id,source_metadata_result_id,selected,compatible,decision_method,policy_version,metadata,updated_at FROM track_source WHERE track_id=? ORDER BY id`, trackID).Scan(ctx, &associations); err != nil {
		t.Fatal(err)
	}
	if len(associations) != 1 {
		t.Fatalf("expected one source association, got %d", len(associations))
	}
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := PublicationAdmission{PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: epoch, CandidateGeneration: uuid.New(), IntentKind: "audio", ToolsReadRequired: true, Tools: selections, Source: &PublicationSourceSelection{RootID: rootID, LocationID: locationID, WorkID: workID, MediaVariantID: variantID, ConfiguredPath: rootPath, InventoryPath: rootPath, RelativePath: "track.flac", SizeBytes: 42, MTime: now, HoldUUID: uuid.New()}, FrozenInputs: frozen, NamespaceManifest: []PublicationNamespaceNode{{CanonicalKey: "track.flac", RelativePath: "track.flac", NodeKind: "file"}}}
	called := false
	first := FirstPublicationAdmission{PublicationAdmission: request, PreviewDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PrepareFirst: func(context.Context, FirstPublicationAdmissionSnapshot, PublicationAdmission) ([]PublicationNamespaceNode, error) {
		called = true
		return request.NamespaceManifest, nil
	}}
	first.SourceSelection = &FirstPublicationSourceSelection{AssociationID: associations[0].ID, Associations: associations}
	guard := PublicationReconciliationDeliveryGuard{OperationID: uuid.New(), ApplicationAttempt: 1, RiverJobID: 1, RiverAttempt: 1, OutputEpoch: epoch, DeliveryEpoch: 1, PositionID: positionID, ObservedGeneration: 1, ClaimToken: uuid.New()}
	_, err = repo.AdmitAutomaticFirstPublicationFromPreview(ctx, first, guard, &publicationAdmissionInserter{})
	if !errors.Is(err, ErrPublicationAdmissionConflict) {
		t.Fatalf("stale guard error = %v", err)
	}
	if !called {
		t.Fatal("stale fence did not exercise admission before acknowledgement")
	}
	assertNoAdmissionArtifacts(t, ctx, db, request, nil)
	var count int
	if err := db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE hold_uuid=?`, request.Source.HoldUUID).Scan(ctx, &count); err != nil || count != 0 {
		t.Fatalf("source holds=%d err=%v", count, err)
	}
	// A real current delivery admits and acknowledges atomically, without
	// clearing the newer request that arrived while its generation was claimed.
	reconciliationDeliveryRequest(t, repo, ctx, epoch, positionID, []string{"initial"})
	delivery := new(reconciliationDeliveryInserter)
	if n, err := repo.DispatchPendingPublicationReconciliations(ctx, delivery); err != nil || n != 1 {
		t.Fatalf("dispatch reconciliation=%d err=%v", n, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, delivery.ids[0]); err != nil {
		t.Fatal(err)
	}
	current, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, delivery.args[0].OperationID, delivery.ids[0], 1)
	if err != nil || result != PublicationReconciliationClaimed {
		t.Fatalf("claim=%s err=%v", result, err)
	}
	reconciliationDeliveryRequest(t, repo, ctx, epoch, positionID, []string{"newer"})
	if _, err := repo.AdmitAutomaticFirstPublicationFromPreview(ctx, first, current, &publicationAdmissionInserter{}); err != nil {
		t.Fatalf("admit current reconciliation delivery: %v", err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=? AND scope='newer' AND generation=2`, epoch, positionID).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("newer dirty scope=%d err=%v", count, err)
	}
	var reconciled int64
	if err := db.NewRaw(`SELECT reconciled_generation FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, epoch, positionID).Scan(ctx, &reconciled); err != nil || reconciled != 1 {
		t.Fatalf("acknowledged generation=%d err=%v", reconciled, err)
	}
}
