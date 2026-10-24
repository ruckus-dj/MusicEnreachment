//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// This exercises the persisted CURRENT witness and candidate-universe query
// used by V2 admission. In particular, admission must read the real setting
// columns, not merely accept a service-built input contract.
func TestValidateAudioUpgradeAdmissionAgainstCurrentAndSourceUniversePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	repo := NewSetupManagerRepository(f.db)

	// First install a real CURRENT projection through the delivery commit path.
	oldLossless, oldChannels, oldDepth, oldRate := true, 2, 16, 44100
	oldFacts := PersistedAudioFacts{
		StreamIndex: 0, Codec: "flac", Lossless: &oldLossless, Channels: &oldChannels,
		ChannelLayout: "stereo", BitDepth: &oldDepth, SampleRate: &oldRate,
		Container: publication.AudioContainerFLAC, SupportedSourceContainer: true,
	}
	result := publicationResultForFixture(f)
	result.Observation.ActualAudio = mustJSON(PublicationCurrentAudio{Version: 1, Facts: oldFacts})
	result.Observation.ActualMetadata = mustJSON(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"TITLE": {"published title"}}})
	if committed, err := repo.CommitPublicationResult(ctx, f.guard, result); err != nil || !committed {
		t.Fatalf("install initial CURRENT: committed=%v err=%v", committed, err)
	}

	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 1, 12, 0, 0, 123456000, time.UTC)
	rootID, locationID, workID, variantID, scanID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	currentLocationID, currentWorkID, currentVariantID := uuid.New(), uuid.New(), uuid.New()
	queries := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,?, 'Upgrade source',1,?,true,'available')`, []any{rootID, rootPath, rootPath}},
		{`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, []any{scanID}},
		{`INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?,42,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"channel_layout":"stereo","bits_per_raw_sample":"24","sample_rate":"48000"}]}','{}'::jsonb,?,?,1)`, []any{variantID, now, scanID}},
		{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,?,42,?,1,'audio',?)`, []any{locationID, rootID, "track.flac", now, variantID}},
		{`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,?,42,?,false,?,?)`, []any{workID, locationID, rootID, rootPath, rootPath, "track.flac", now, scanID, locationID}},
		{`INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, []any{workID, variantID}},
		{`INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'executed')`, []any{workID, variantID}},
		{`INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?,40,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"channel_layout":"stereo","bits_per_raw_sample":"16","sample_rate":"44100"}]}','{}'::jsonb,?,?,1)`, []any{currentVariantID, now, scanID}},
		{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,?,40,?,1,'audio',?)`, []any{currentLocationID, rootID, "current.flac", now, currentVariantID}},
		{`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,?,40,?,false,?,?)`, []any{currentWorkID, currentLocationID, rootID, rootPath, rootPath, "current.flac", now, scanID, currentLocationID}},
		{`INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, []any{currentWorkID, currentVariantID}},
		{`INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'executed')`, []any{currentWorkID, currentVariantID}},
	}
	for _, query := range queries {
		if _, err := f.db.ExecContext(ctx, query.query, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	var trackID, releaseID uuid.UUID
	if err := f.db.NewRaw(`SELECT track_id,release_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &trackID, &releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,true,true,'manual','{}'::jsonb)`, uuid.New(), trackID, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,false,true,'manual','{}'::jsonb)`, uuid.New(), trackID, currentVariantID); err != nil {
		t.Fatal(err)
	}

	// CURRENT's automatic authority is bound to its committed generation; the
	// newly selected source is a strict quality improvement over its actual audio.
	authority := PositionSourceAuthority{
		PositionID: f.guard.ExpectedPositionID, Mode: "automatic", Revision: uuid.New(),
		PublicationEpoch: f.guard.Epoch, PublicationGeneration: f.guard.CandidateGeneration,
		SourceLocationID: currentLocationID, MediaVariantID: currentVariantID,
	}
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		created, err := compareAndSetPositionSourceAuthorityTx(ctx, tx, nil, authority)
		if err == nil && !created {
			return ErrPublicationAdmissionConflict
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}

	installationID := uuid.New()
	verified := time.Now().UTC()
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	installPath := filepath.Join("ffmpeg", "release-1")
	if err := repo.CreateInstallation(ctx, &ToolInstallation{
		ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH,
		SourceName: "fixture", ReleaseIdentity: "release-1", RelativePath: installPath, State: "ready",
		ExecutableVersions: versions, ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified,
	}); err != nil {
		t.Fatal(err)
	}
	installPath = filepath.ToSlash(installPath)
	selections := []SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: installPath, Executable: "ffmpeg", Version: "7.0", VersionBanner: ffmpegBanner},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: installPath, Executable: "ffprobe", Version: "7.0", VersionBanner: ffprobeBanner},
	}
	settings := NewSettingsRepository(f.db)
	for key, value := range map[string]string{"publication_format": string(publication.AudioFormatSource), "active_ffmpeg_installation_id": installationID.String()} {
		if err := settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}

	assets, err := DecodePublicationAssetOutcomes(result.Observation.ActualAssets)
	if err != nil {
		t.Fatal(err)
	}
	input := PublicationInputs{
		Version: PublicationAudioUpgradeInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "audio", SourceAuthorityMode: "automatic", Format: publication.AudioFormatSource,
		DesiredTags: map[string][]string{"TITLE": {"published title"}}, Tools: selections,
		Audio: &PublicationAudioInput{
			WorkID: workID, RootID: rootID, LocationID: locationID, MediaVariantID: variantID, ProbeResultID: variantID,
			ConfiguredRootPath: rootPath, InventoryRootPath: rootPath, RelativePath: "track.flac", SizeBytes: 42, MTime: now,
			AnalysisPolicyVersion: 1, FFProbeVersion: "ffprobe 1",
			AudioFacts: PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &oldLossless, Channels: &oldChannels,
				ChannelLayout: "stereo", BitDepth: intPointer(24), SampleRate: intPointer(48000), Container: publication.AudioContainerFLAC, SupportedSourceContainer: true},
		},
		AudioUpgrade: &PublicationAudioUpgradeInput{
			SourceAuthorityRevision: authority.Revision,
			Current: PublicationCurrentWitness{Generation: f.guard.CandidateGeneration, RelativePath: result.Observation.RelativePath,
				Ownership: result.InstalledOwnership.Identity, AudioFacts: oldFacts, Tags: map[string][]string{"TITLE": {"published title"}}, Assets: assets, ArtworkInventory: []PublicationArtworkWitness{}},
		},
	}
	request := PublicationAdmission{
		PositionID: f.guard.ExpectedPositionID, ExpectedReleaseID: releaseID, ExpectedEpoch: f.guard.Epoch,
		ExpectedGeneration: uuidPointer(f.guard.CandidateGeneration), CandidateGeneration: uuid.New(), IntentKind: "audio",
		ToolsReadRequired: true, Tools: selections, Source: &PublicationSourceSelection{RootID: rootID, LocationID: locationID, WorkID: workID,
			MediaVariantID: variantID, ConfiguredPath: rootPath, InventoryPath: rootPath, RelativePath: "track.flac", SizeBytes: 42, MTime: now, HoldUUID: uuid.New()},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("invalid V2 frozen input fixture: %v", err)
	}
	plan, err := publication.PlanAudioTransform(input.Format, input.Audio.AudioFacts.AudioSourceFacts())
	if err != nil {
		t.Fatalf("plan V2 candidate output: %v", err)
	}
	outputPath := path.Join(path.Dir(result.Observation.RelativePath), strings.TrimSuffix(path.Base(result.Observation.RelativePath), path.Ext(result.Observation.RelativePath))+"."+plan.Extension)
	if outputPath != result.Observation.RelativePath {
		t.Fatalf("same-format audio upgrade path = %q, want CURRENT path %q", outputPath, result.Observation.RelativePath)
	}
	// Reuse the exact initial namespace claim, including its canonical directory
	// scope. Audio upgrade retains the existing CURRENT path in this format.
	manifest := []PublicationNamespaceNode{
		{CanonicalKey: "album", RelativePath: "album", NodeKind: "dir", DirectoryScope: "album"},
		{CanonicalKey: result.Observation.RelativePath, RelativePath: result.Observation.RelativePath, NodeKind: "file"},
	}
	request.NamespaceManifest = manifest
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.validateAudioUpgradeAdmission(ctx, tx, request, input, f.guard.Epoch)
	}); err != nil {
		t.Fatalf("validate live audio-upgrade admission: %v", err)
	}
	stale := input
	stale.AudioUpgrade = &PublicationAudioUpgradeInput{
		SourceAuthorityRevision: uuid.New(), Current: input.AudioUpgrade.Current,
	}
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repo.validateAudioUpgradeAdmission(ctx, tx, request, stale, f.guard.Epoch)
	}); err == nil {
		t.Fatal("stale automatic source-authority revision was admitted")
	}

	// Exercise the typed admission entry point, not just its validator. The
	// original CURRENT and replacement use the same path and the real original
	// namespace reservation; this needs no obsolete-output cleanup proof.
	request.NamespaceManifest = manifest
	request.FrozenInputs, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	client := openPublicationRetryRiver(t, ctx, f.db)
	operationID, err := repo.AdmitAutomaticAudioUpgrade(ctx, request, client)
	if err != nil {
		t.Fatalf("admit automatic audio upgrade: %v", err)
	}
	var riverJobID int64
	if err := f.db.NewRaw(`SELECT river_job_id FROM operation WHERE id=?`, operationID).Scan(ctx, &riverJobID); err != nil {
		t.Fatal(err)
	}
	// River claims jobs outside admission. Mark the actual inserted row as the
	// running delivery that the repository's claim API requires.
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, riverJobID); err != nil {
		t.Fatal(err)
	}
	var applicationAttempt int
	if err := f.db.NewRaw(`SELECT attempt FROM operation WHERE id=?`, operationID).Scan(ctx, &applicationAttempt); err != nil {
		t.Fatal(err)
	}
	upgradeGuard := PublicationDeliveryGuard{
		OperationID: operationID, ApplicationAttempt: applicationAttempt, RiverJobID: riverJobID, RiverAttempt: 1,
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "audio-upgrade-test-delivery",
		Epoch: request.ExpectedEpoch, CandidateGeneration: request.CandidateGeneration,
		ExpectedPositionID: request.PositionID,
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, upgradeGuard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim admitted audio-upgrade delivery = %q, %v", outcome, err)
	}
	preparationInputs, ok, err := repo.PublicationPreparationInputs(ctx, upgradeGuard)
	if err != nil || !ok {
		t.Fatalf("read admitted audio-upgrade preparation inputs: ok=%v err=%v", ok, err)
	}
	if preparationInputs.AudioUpgrade == nil || preparationInputs.AudioUpgrade.SourceAuthorityRevision != authority.Revision {
		t.Fatal("preparation inputs did not retain the admitted automatic source authority")
	}
	if current, err := repo.CheckPublicationResultCurrent(ctx, upgradeGuard); err != nil || !current {
		t.Fatalf("check admitted audio-upgrade result current: current=%v err=%v", current, err)
	}

	// Commit a strictly better actual audio projection while retaining CURRENT's
	// exact metadata and assets. This also proves the source-authority CAS is
	// consumed as part of the successful publication commit.
	betterLossless, betterChannels, betterDepth, betterRate := true, 2, 24, 48000
	betterFacts := PersistedAudioFacts{
		StreamIndex: 0, Codec: "flac", Lossless: &betterLossless, Channels: &betterChannels,
		ChannelLayout: "stereo", BitDepth: &betterDepth, SampleRate: &betterRate,
		Container: publication.AudioContainerFLAC, SupportedSourceContainer: true,
	}
	commitResult := result
	commitResult.Observation.RelativePath = outputPath
	commitResult.Observation.ActualAudio = mustJSON(PublicationCurrentAudio{Version: 1, Facts: betterFacts})
	commitResult.Observation.ActualMetadata = mustJSON(PublicationCommitMetadata{Version: 1, Tags: input.DesiredTags})
	if committed, err := repo.CommitPublicationResult(ctx, upgradeGuard, commitResult); err != nil || !committed {
		t.Fatalf("commit automatic audio upgrade: committed=%v err=%v", committed, err)
	}
	committedAuthority, err := repo.ReadPositionSourceAuthority(ctx, request.PositionID)
	if err != nil || committedAuthority == nil || committedAuthority.SourceLocationID != locationID || committedAuthority.MediaVariantID != variantID ||
		committedAuthority.PublicationGeneration != request.CandidateGeneration {
		t.Fatalf("successful commit did not CAS source authority to the new source/current generation: authority=%#v err=%v", committedAuthority, err)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, operationID)
	if err != nil || attempt != nil {
		t.Fatalf("successful commit retained a live publication attempt: attempt=%#v err=%v", attempt, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE id=?`, riverJobID); err != nil {
		t.Fatal(err)
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, upgradeGuard); err != nil || outcome != PublicationDeliveryBlocked {
		t.Fatalf("completed delivery redelivery = %q, %v; want blocked", outcome, err)
	}
	var jobs int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE args->>'operation_id'=?`, operationID.String()).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("completed redelivery created %d River jobs; want the single admitted job", jobs)
	}

}
