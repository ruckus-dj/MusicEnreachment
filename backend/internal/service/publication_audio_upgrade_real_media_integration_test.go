//go:build integration

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
	"go.senan.xyz/taglib"
)

// This test deliberately starts with the existing publication-source fixture,
// but replaces its test-only source and tool rows before the first admission.
// The initial publication, reconciliation, audio-upgrade admission, candidate
// validation, installation, and settlement all use the production services.
func TestAudioUpgradeReconciliationPreservesRealMultiImageCurrentPostgreSQL(t *testing.T) {
	ffmpegFromPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("native ffmpeg integration not executed: ffmpeg is unavailable: %v", err)
	}
	ffprobeFromPath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skipf("native ffmpeg integration not executed: ffprobe is unavailable: %v", err)
	}
	if err := requireRegularExecutable(ffmpegFromPath); err != nil {
		t.Skipf("native ffmpeg integration not executed: %v", err)
	}
	if err := requireRegularExecutable(ffprobeFromPath); err != nil {
		t.Skipf("native ffmpeg integration not executed: %v", err)
	}

	var fixtureReleaseID, fixturePositionID, fixtureTrackID uuid.UUID
	var installationID uuid.UUID
	var managedFFmpeg, managedFFprobe, ffprobeBanner string
	var settingsRoot, outputRoot string
	var lifecycleVersions map[string]string
	beforeAdmission := func(ctx context.Context, db *bun.DB, _ *persistence.SetupManagerRepository, releaseID uuid.UUID) {
		fixtureReleaseID = releaseID
		settingsRepo := persistence.NewSettingsRepository(db)
		registry := settings.New(settingsRepo, nil)
		runtimeSettings, err := registry.ReadRuntimeSettings(ctx)
		if err != nil {
			t.Fatalf("read publication fixture settings: %v", err)
		}
		settingsRoot, outputRoot = runtimeSettings.ToolsDirectory, runtimeSettings.OutputDirectory
		if settingsRoot == "" || outputRoot == "" {
			t.Fatal("fixture did not configure managed tool and output roots")
		}
		if err := db.NewRaw(`SELECT id,track_id FROM release_track WHERE release_id=?`, releaseID).Scan(ctx, &fixturePositionID, &fixtureTrackID); err != nil {
			t.Fatal(err)
		}
		if err := settingsRepo.SetMany(ctx, map[string]string{
			settings.PublicationFormatKey:        string(publication.AudioFormatSource),
			settings.PublicationPathTemplateKey:  "Track.{EXT}",
			settings.PublicationRemoveSourcesKey: "false",
			settings.LRCLIBEnabledKey:            "false",
		}); err != nil {
			t.Fatalf("configure real-media publication fixture: %v", err)
		}

		// The shared fixture already created a tool-installation row. Replace its
		// executable files and facts now, before PreviewFirst/AdmitFirst freezes it.
		if err := db.NewRaw(`SELECT id FROM tool_installation WHERE package_kind='ffmpeg' AND state='ready' LIMIT 1`).Scan(ctx, &installationID); err != nil {
			t.Fatalf("find fixture ffmpeg installation: %v", err)
		}
		_, version, err := verifiedBanner(ffmpegFromPath)
		if err != nil {
			t.Fatalf("query PATH ffmpeg version: %v", err)
		}
		_, ffprobeVersion, err := verifiedBanner(ffprobeFromPath)
		if err != nil {
			t.Fatalf("query PATH ffprobe version: %v", err)
		}
		if version != ffprobeVersion {
			t.Fatalf("PATH ffmpeg/ffprobe version identities differ: %q vs %q", version, ffprobeVersion)
		}
		relative, err := tools.ManagedRelativePath(tools.PackageFFmpeg, version)
		if err != nil {
			t.Fatal(err)
		}
		managedDir := filepath.Join(settingsRoot, relative)
		if err := os.MkdirAll(managedDir, 0o755); err != nil {
			t.Fatalf("create managed tool directory: %v", err)
		}
		managedFFmpeg = filepath.Join(managedDir, executableName("ffmpeg"))
		managedFFprobe = filepath.Join(managedDir, executableName("ffprobe"))
		copyRealExecutable(t, ffmpegFromPath, managedFFmpeg)
		copyRealExecutable(t, ffprobeFromPath, managedFFprobe)
		lifecycleVersions, err = tools.NewLifecycle(nil).VerifyInstallation(ctx, settingsRoot, relative, tools.PackageFFmpeg, version, runtime.GOOS)
		if err != nil {
			t.Fatalf("verify copied managed ffmpeg installation: %v", err)
		}
		canonicalVersion, err := tools.CanonicalPackageVersion(lifecycleVersions, tools.PackageFFmpeg, runtime.GOOS)
		if err != nil {
			t.Fatalf("derive canonical version from fresh managed verification: %v", err)
		}
		versionsJSON, err := json.Marshal(lifecycleVersions)
		if err != nil {
			t.Fatal(err)
		}
		verifiedAt := time.Now().UTC()
		if _, err := db.ExecContext(ctx, `UPDATE tool_installation SET release_identity=?,relative_path=?,executable_verification_output=?::jsonb,version=?,verified_at=?,artifact_identities='{}'::jsonb WHERE id=?`, version, relative, string(versionsJSON), canonicalVersion, verifiedAt, installationID); err != nil {
			t.Fatalf("record actual managed executable verification: %v", err)
		}
		if err := settingsRepo.Set(ctx, settings.ActiveFFmpegInstallationKey, installationID.String()); err != nil {
			t.Fatalf("select verified ffmpeg installation: %v", err)
		}
		if err := registry.SetLRCLIBEnabled(ctx, false); err != nil {
			t.Fatalf("disable external lyrics lookup: %v", err)
		}

		var rootPath, relativePath string
		var variantID, workID, locationID, scanOperationID uuid.UUID
		if err := db.NewRaw(`SELECT r.configured_path,l.relative_path,l.media_variant_id,w.id,w.location_id,w.origin_scan_operation_id
			FROM source_root r JOIN source_location l ON l.source_root_id=r.id
			JOIN source_analysis_work w ON w.current_location_id=l.id
			WHERE l.media_variant_id IS NOT NULL LIMIT 1`).Scan(ctx, &rootPath, &relativePath, &variantID, &workID, &locationID, &scanOperationID); err != nil {
			t.Fatalf("read source fixture identity: %v", err)
		}
		primarySource := filepath.Join(rootPath, filepath.FromSlash(relativePath))
		ffprobeBanner, _, err = verifiedBanner(managedFFprobe)
		if err != nil {
			t.Fatalf("query managed ffprobe version: %v", err)
		}
		if err := generateSineFLAC(ctx, managedFFmpeg, primarySource, 22050, 16); err != nil {
			t.Fatalf("generate actual low-quality FLAC source: %v", err)
		}
		if _, err := (metadata.Writer{}).Write(ctx, primarySource, map[string][]string{
			"TITLE": {"Published track"}, "ARTIST": {"Artist"}, "ALBUM": {"Real media reconciliation"},
		}, nil); err != nil {
			t.Fatalf("write fixture source tags: %v", err)
		}
		probe, err := probeRealFile(ctx, managedFFprobe, primarySource)
		if err != nil {
			t.Fatalf("probe actual low-quality FLAC: %v", err)
		}
		capture, err := (metadata.Reader{}).Read(ctx, primarySource, metadata.ReadRequest{CoverInspection: true})
		if err != nil {
			t.Fatalf("read actual source tags: %v", err)
		}
		info, err := os.Stat(primarySource)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE media_variant SET size_bytes=?,ffprobe_version=?,ffprobe_json=?::jsonb,observed_tags=?::jsonb,inspected_at=?,applied_operation_id=?,audio_stream_count=1 WHERE id=?`,
			info.Size(), ffprobeBanner, string(probe), mustJSONText(t, capture.Tags), info.ModTime().UTC(), scanOperationID, variantID); err != nil {
			t.Fatalf("refresh real low-quality probe facts before admission: %v", err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE source_location SET size_bytes=?,mtime=?,probe_status='audio' WHERE id=?`, info.Size(), info.ModTime().UTC(), locationID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE source_analysis_work SET size_bytes=?,mtime=? WHERE id=?`, info.Size(), info.ModTime().UTC(), workID); err != nil {
			t.Fatal(err)
		}
	}

	var upgradeSource string
	var higherVariantID uuid.UUID
	var expectedHigherProbe []byte
	var expectedArtwork metadata.ArtworkSnapshot
	var expectedTags map[string][]string
	var originalPrimary []byte
	var originalHigher []byte
	var originalCurrentAudio persistence.PersistedAudioFacts
	var committedGeneration uuid.UUID
	var binariesForDelivery toolsManagedExecutables
	scenario := publicationDeliveryScenario{
		beforeAdmission: beforeAdmission,
		run: func(ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, _ *PublicationExecutor, operationID uuid.UUID, firstJob int64, source, output, target string, _ []byte) {
			settingsRepo := persistence.NewSettingsRepository(db)
			registry := settings.New(settingsRepo, nil)
			binariesForDelivery = toolsManagedExecutables{ffmpeg: managedFFmpeg, ffprobe: managedFFprobe}
			commands, err := NewPublicationJobCommands(4<<20, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			executor := NewPublicationExecutor(repository, registry, tools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, publication.NewCoordinator(), uuid.New(), commands)
			if err := executor.ExecutePublicationDelivery(ctx, operationID, firstJob, 1); err != nil {
				t.Fatalf("execute initial publication with managed real tools: %v", err)
			}
			attempt, err := repository.ReadPublicationAttempt(ctx, operationID)
			if err != nil || attempt != nil {
				t.Fatalf("initial publication attempt did not settle: %+v, %v", attempt, err)
			}
			destination := filepath.Join(output, filepath.FromSlash(target))
			baselineContext, err := repository.ReadLibraryPublicationContext(ctx, fixtureReleaseID, fixturePositionID)
			if err != nil || baselineContext == nil || baselineContext.Current == nil {
				t.Fatalf("read initial actual CURRENT audio: %+v, %v", baselineContext, err)
			}
			originalCurrentAudio = baselineContext.Current.Audio.Facts
			originalPrimary, err = os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}

			// Establish pre-existing CURRENT-only metadata/artwork, then refresh its
			// witnessed filesystem/metadata facts before reconciliation admission.
			currentCapture, err := (metadata.Reader{}).Read(ctx, destination, metadata.ReadRequest{CoverInspection: true})
			if err != nil {
				t.Fatal(err)
			}
			currentCapture.Tags["LYRICS"] = []string{"[00:00.00]Real CURRENT lyrics\n"}
			if _, err := (metadata.Writer{}).Write(ctx, destination, currentCapture.Tags, nil); err != nil {
				t.Fatalf("write pre-existing CURRENT lyrics: %v", err)
			}
			images := []metadata.ArtworkSnapshotImage{
				{Index: 0, Type: "Front Cover", Description: "retained front", MIME: "image/jpeg", Data: audioUpgradeImage(t, "image/jpeg", colorRGBA(220, 30, 30))},
				{Index: 1, Type: "Back Cover", Description: "retained back", MIME: "image/png", Data: audioUpgradeImage(t, "image/png", colorRGBA(30, 30, 220))},
			}
			for _, image := range images {
				if err := taglib.WriteImageOptions(destination, image.Data, image.Index, image.Type, image.Description, image.MIME); err != nil {
					t.Fatalf("write pre-existing CURRENT artwork %d: %v", image.Index, err)
				}
			}
			currentCapture, err = (metadata.Reader{}).Read(ctx, destination, metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false})
			if err != nil {
				t.Fatal(err)
			}
			currentArtwork, err := metadata.ReadArtworkSnapshot(ctx, destination)
			if err != nil {
				t.Fatal(err)
			}
			if len(currentArtwork.Images) != 2 {
				t.Fatalf("pre-existing CURRENT artwork count=%d, want two", len(currentArtwork.Images))
			}
			expectedArtwork = currentArtwork
			expectedTags = currentCapture.Tags
			assets, err := ObservePublicationAssets(currentCapture)
			if err != nil {
				t.Fatalf("observe pre-existing CURRENT artwork: %v", err)
			}
			currentInfo, err := os.Stat(destination)
			if err != nil {
				t.Fatal(err)
			}
			currentBytes, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			currentHash := sha256.Sum256(currentBytes)
			size, mtime := currentInfo.Size(), currentInfo.ModTime().UnixMicro()
			currentIdentity := persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(currentHash[:])}
			// Update only the physical/metadata witnesses for this intentionally
			// prepared pre-existing CURRENT. Actual audio remains the captured output.
			if _, err := db.ExecContext(ctx, `UPDATE track_publication SET ownership=?::jsonb,actual_metadata=?::jsonb,actual_assets=?::jsonb WHERE position_id=?`,
				mustJSONText(t, persistence.PublicationCurrentOwnership{Version: persistence.PublicationCurrentOwnershipVersion, Identity: currentIdentity}),
				mustJSONText(t, persistence.PublicationCommitMetadata{Version: 1, Tags: currentCapture.Tags}), mustJSONText(t, assets), fixturePositionID); err != nil {
				t.Fatalf("seed witnessed pre-existing CURRENT metadata/artwork: %v", err)
			}

			// Add a second fully-provenanced source with actual higher-quality media.
			var rootID uuid.UUID
			var rootPath string
			if err := db.NewRaw(`SELECT id,configured_path FROM source_root LIMIT 1`).Scan(ctx, &rootID, &rootPath); err != nil {
				t.Fatal(err)
			}
			upgradeSource = filepath.Join(rootPath, "higher.flac")
			if err := generateSineFLAC(ctx, binariesForDelivery.ffmpeg, upgradeSource, 48000, 24); err != nil {
				t.Fatalf("generate actual higher-quality FLAC source: %v", err)
			}
			if _, err := (metadata.Writer{}).Write(ctx, upgradeSource, map[string][]string{
				"TITLE": {"Published track"}, "ARTIST": {"Artist"}, "ALBUM": {"Real media reconciliation"},
			}, nil); err != nil {
				t.Fatal(err)
			}
			expectedHigherProbe, err = probeRealFile(ctx, binariesForDelivery.ffprobe, upgradeSource)
			if err != nil {
				t.Fatalf("probe actual higher-quality source: %v", err)
			}
			higherCapture, err := (metadata.Reader{}).Read(ctx, upgradeSource, metadata.ReadRequest{Properties: true})
			if err != nil {
				t.Fatal(err)
			}
			higherInfo, err := os.Stat(upgradeSource)
			if err != nil {
				t.Fatal(err)
			}
			originalHigher, err = os.ReadFile(upgradeSource)
			if err != nil {
				t.Fatal(err)
			}
			var sourceRootID, scanOperation uuid.UUID
			var configured, inventory string
			if err := db.NewRaw(`SELECT id,configured_path,inventory_path FROM source_root LIMIT 1`).Scan(ctx, &sourceRootID, &configured, &inventory); err != nil {
				t.Fatal(err)
			}
			if err := db.NewRaw(`SELECT origin_scan_operation_id FROM source_analysis_work LIMIT 1`).Scan(ctx, &scanOperation); err != nil {
				t.Fatal(err)
			}
			higherVariantID = uuid.New()
			higherLocationID, higherWorkID := uuid.New(), uuid.New()
			if err := insertRealSourceFacts(ctx, db, rootID, configured, inventory, "higher.flac", higherVariantID, higherLocationID, higherWorkID, scanOperation,
				int64(higherInfo.Size()), higherInfo.ModTime().UTC(), string(expectedHigherProbe), ffprobeBanner, higherCapture.Tags); err != nil {
				t.Fatalf("insert actual higher-quality source evidence: %v", err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,metadata) VALUES (?,?,?,false,true,'automatic','policy-v1','{}'::jsonb)`, uuid.New(), fixtureTrackID, higherVariantID); err != nil {
				t.Fatalf("associate higher-quality source: %v", err)
			}

			// Produce a genuine reconciliation request/job, claim its current River
			// delivery, and let LibraryPublications build/freeze its own input witness.
			var epoch uuid.UUID
			if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
				t.Fatal(err)
			}
			positionID := fixturePositionID
			if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				return repository.RequestPublicationReconciliationWith(ctx, tx, epoch, []uuid.UUID{positionID}, []string{"audio"})
			}); err != nil {
				t.Fatalf("request real publication reconciliation: %v", err)
			}
			inserter := reconciliationIntegrationInserter{}
			repository.SetRiverInserter(inserter)
			dispatched, err := repository.DispatchPendingPublicationReconciliations(ctx, inserter)
			if err != nil || dispatched != 1 {
				t.Fatalf("dispatch real reconciliation job = %d, %v", dispatched, err)
			}
			var reconciliationOperation uuid.UUID
			var reconciliationJob int64
			if err := db.NewRaw(`SELECT operation_id,registered_job_id FROM publication_reconciliation_event WHERE output_epoch=? AND position_id=? AND state='dispatched'`, epoch, positionID).Scan(ctx, &reconciliationOperation, &reconciliationJob); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, reconciliationJob); err != nil {
				t.Fatal(err)
			}
			reconciliationGuard, result, err := repository.ClaimPublicationReconciliationDelivery(ctx, reconciliationOperation, reconciliationJob, 1)
			if err != nil || result != persistence.PublicationReconciliationClaimed {
				t.Fatalf("claim real reconciliation delivery = %s, %v", result, err)
			}
			reconciliationService := NewLibraryPublications(repository, registry, inserter, tools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}).
				WithPublicationCoordinator(publication.NewCoordinator()).WithPublicationCommands(commands)
			if err := reconciliationService.ReconcilePublication(ctx, reconciliationGuard); err != nil {
				t.Fatalf("run real publication reconciliation: %v", err)
			}
			if _, err := repository.ReleasePublicationReconciliationDelivery(ctx, reconciliationGuard); err != nil {
				t.Fatalf("release admitted reconciliation delivery: %v", err)
			}
			var upgradeAttempt persistence.TrackPublicationAttempt
			if err := db.NewSelect().Model(&upgradeAttempt).Where("position_id=?", positionID).Scan(ctx); err != nil {
				t.Fatalf("read reconciliation-admitted upgrade attempt: %v", err)
			}
			if upgradeAttempt.OperationID == uuid.Nil {
				t.Fatal("reconciliation did not admit an audio upgrade attempt")
			}
			frozen, err := persistence.DecodePublicationInputs(upgradeAttempt.FrozenInputs)
			if err != nil {
				t.Fatal(err)
			}
			if frozen.Audio == nil || frozen.Audio.MediaVariantID != higherVariantID || frozen.AudioUpgrade == nil || len(frozen.AudioUpgrade.Current.ArtworkInventory) != 2 {
				t.Fatalf("reconciliation froze unexpected source/CURRENT witness: %+v", frozen)
			}
			upgradeOperation := upgradeAttempt.OperationID
			upgradeOp, err := repository.GetOperation(ctx, upgradeOperation)
			if err != nil || upgradeOp == nil || upgradeOp.RiverJobID == nil {
				t.Fatalf("read admitted upgrade operation: %+v, %v", upgradeOp, err)
			}
			upgradeJob := *upgradeOp.RiverJobID
			if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, upgradeJob); err != nil {
				t.Fatal(err)
			}
			upgradeExecutor := NewPublicationExecutor(repository, registry, tools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, publication.NewCoordinator(), uuid.New(), commands)
			if err := upgradeExecutor.ExecutePublicationDelivery(ctx, upgradeOperation, upgradeJob, 1); err != nil {
				t.Fatalf("execute reconciled real-media audio upgrade: %v", err)
			}
			committedGeneration = upgradeAttempt.CandidateGeneration
			currentContext, err := repository.ReadLibraryPublicationContext(ctx, fixtureReleaseID, positionID)
			if err != nil || currentContext == nil || currentContext.Current == nil || currentContext.Current.Record.Generation != committedGeneration {
				t.Fatalf("read upgraded CURRENT: %+v, %v", currentContext, err)
			}
			if currentContext.Current.Audio.Facts.SampleRate == nil || *currentContext.Current.Audio.Facts.SampleRate != 48000 || currentContext.Current.Audio.Facts.BitDepth == nil || *currentContext.Current.Audio.Facts.BitDepth != 24 {
				t.Fatalf("upgraded audio facts = %+v, want actual 24-bit/48-kHz FLAC", currentContext.Current.Audio.Facts)
			}
			if publication.CompareQuality(audioUpgradeQualityCandidate(currentContext.Current.Audio.Facts), audioUpgradeQualityCandidate(originalCurrentAudio)) != publication.QualityBetter {
				t.Fatalf("actual upgraded audio is not strictly better: before=%+v after=%+v", originalCurrentAudio, currentContext.Current.Audio.Facts)
			}
			installed := filepath.Join(output, filepath.FromSlash(currentContext.Current.Record.RelativePath))
			if currentContext.Current.Record.RelativePath != target {
				t.Fatalf("audio upgrade changed target path %q to %q", target, currentContext.Current.Record.RelativePath)
			}
			finalCapture, err := (metadata.Reader{}).Read(ctx, installed, metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false})
			if err != nil || !reflect.DeepEqual(finalCapture.Tags, expectedTags) {
				t.Fatalf("upgraded publication tags/lyrics = %#v, want %#v (err=%v)", finalCapture.Tags, expectedTags, err)
			}
			finalArtwork, err := metadata.ReadArtworkSnapshot(ctx, installed)
			if err != nil || !sameArtworkSnapshot(finalArtwork, expectedArtwork) {
				t.Fatalf("upgraded CURRENT artwork changed: inventory=%+v err=%v", finalArtwork.ArtworkInventory(), err)
			}
			settledOperation, err := repository.GetOperation(ctx, upgradeOperation)
			if err != nil || settledOperation == nil || settledOperation.State != "succeeded" {
				t.Fatalf("audio-upgrade operation did not succeed: %+v, %v", settledOperation, err)
			}
			for _, table := range []string{"track_publication_attempt", "publication_execution_claim", "publication_source_hold", "operation_tool_read_hold"} {
				var count int
				if err := db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, upgradeOperation).Scan(ctx, &count); err != nil || count != 0 {
					t.Fatalf("successful upgrade retained %s=%d, err=%v", table, count, err)
				}
			}
			primaryAfter, err := os.ReadFile(source)
			if err != nil || !reflect.DeepEqual(primaryAfter, originalPrimary) {
				t.Fatalf("original source changed during upgrade: %v", err)
			}
			higherAfter, err := os.ReadFile(upgradeSource)
			if err != nil || !reflect.DeepEqual(higherAfter, originalHigher) {
				t.Fatalf("higher-quality source was not preserved: %v", err)
			}
			if !reflect.DeepEqual(expectedHigherProbe, mustProbeAgain(t, ctx, binariesForDelivery.ffprobe, upgradeSource)) {
				t.Fatal("frozen higher source probe no longer matches its file")
			}
		},
	}
	testPublicationSourceExecution(t, "Track.flac", true, false, scenario)
}

type toolsManagedExecutables struct{ ffmpeg, ffprobe string }

func requireRegularExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%q is not a regular executable", path)
	}
	return nil
}

func copyRealExecutable(t *testing.T, source, target string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("PATH executable %q is not regular: %v", source, err)
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := output.ReadFrom(input); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

var ffmpegVersionIdentity = regexp.MustCompile(`(?i)\bversion\s+([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)`)

func verifiedBanner(executable string) (banner, identity string, err error) {
	output, err := exec.Command(executable, "-version").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("run %s -version: %w: %s", executable, err, strings.TrimSpace(string(output)))
	}
	banner = strings.TrimSpace(string(output))
	match := ffmpegVersionIdentity.FindStringSubmatch(banner)
	if len(match) != 2 {
		return "", "", fmt.Errorf("version banner has no semantic identity: %q", banner)
	}
	return banner, strings.TrimPrefix(match[1], "n"), nil
}

func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func generateSineFLAC(ctx context.Context, ffmpeg, path string, sampleRate, depth int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".generated.flac"
	sampleFormat := "s16"
	if depth == 24 {
		sampleFormat = "s32"
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-ac", "2", "-ar", fmt.Sprint(sampleRate), "-sample_fmt", sampleFormat, "-c:a", "flac", "-compression_level", "0", "-y", temporary}
	output, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg sine generation: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func probeRealFile(ctx context.Context, ffprobe, path string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_format", "-show_streams", "-of", "json", path).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffprobe actual media: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var document struct {
		Streams []json.RawMessage `json:"streams"`
		Format  json.RawMessage   `json:"format"`
	}
	if err := json.Unmarshal(output, &document); err != nil || len(document.Streams) == 0 || len(document.Format) == 0 {
		return nil, fmt.Errorf("ffprobe returned incomplete actual media facts: %w", err)
	}
	return output, nil
}

func mustJSONText(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func insertRealSourceFacts(ctx context.Context, db *bun.DB, rootID uuid.UUID, configured, inventory, relative string, variantID, locationID, workID, scanOperationID uuid.UUID, size int64, mtime time.Time, probe, ffprobeVersion string, tags map[string][]string) error {
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?, ?,1,?,?::jsonb,?::jsonb,?,?,1)`,
		variantID, size, ffprobeVersion, probe, mustJSONNoFatal(tags), mtime, scanOperationID); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) SELECT ?,id,?,?,?,scan_generation,'audio',? FROM source_root WHERE id=?`,
		locationID, relative, size, mtime, variantID, rootID); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,?,?, ?,false,?,?)`,
		workID, locationID, rootID, configured, inventory, relative, size, mtime, scanOperationID, locationID); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, workID, variantID); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'executed')`, workID, variantID)
	return err
}

func mustJSONNoFatal(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func colorRGBA(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 255} }

func sameArtworkSnapshot(actual, expected metadata.ArtworkSnapshot) bool {
	if len(actual.Images) != len(expected.Images) || !reflect.DeepEqual(actual.ArtworkInventory(), expected.ArtworkInventory()) {
		return false
	}
	for index := range actual.Images {
		a, e := actual.Images[index], expected.Images[index]
		if a.Index != e.Index || a.Type != e.Type || a.Description != e.Description || a.MIME != e.MIME || !reflect.DeepEqual(a.Data, e.Data) {
			return false
		}
	}
	return true
}

func mustProbeAgain(t *testing.T, ctx context.Context, ffprobe, path string) []byte {
	t.Helper()
	probe, err := probeRealFile(ctx, ffprobe, path)
	if err != nil {
		t.Fatal(err)
	}
	return probe
}
