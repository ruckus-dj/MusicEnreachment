//go:build integration

package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// TestSourceAnalysisHTTPAgainstPostgreSQL drives the inspector and the analysis
// start through the real repository. Root ownership of a location, the stored
// variant read and the active-analysis discovery are PostgreSQL behaviour, not
// properties of a fake.
func TestSourceAnalysisHTTPAgainstPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	store := persistence.NewSettingsRepository(database)
	registry := settings.New(store, nil)
	platform, err := registry.InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	toolsRoot, outputRoot := t.TempDir(), t.TempDir()
	if err := registry.SetToolsDirectory(ctx, toolsRoot, outputRoot); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetOutputDirectory(ctx, outputRoot, toolsRoot); err != nil {
		t.Fatal(err)
	}
	if err := registry.CompleteSetup(ctx); err != nil {
		t.Fatal(err)
	}

	setupManager := persistence.NewSetupManagerRepository(database)
	inventory := persistence.NewSourceInventoryRepository(database)
	driver := riverdatabasesql.New(database.DB)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatalf("create River migrator: %v", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}
	riverClient, err := river.NewClient(driver, &river.Config{})
	if err != nil {
		t.Fatalf("create insert-only River client: %v", err)
	}
	handler := api.HandlerWithDependencies(api.Dependencies{
		Setup:                 service.NewSetup(store, registry, platform, setupManager, nil),
		SourceRoots:           service.NewSourceRoots(inventory, registry),
		SourceLocations:       service.NewSourceLocations(inventory),
		SourceLocationDetails: service.NewSourceLocationDetails(inventory),
		SourceAnalysis:        service.NewSourceAnalysisOperations(persistence.NewSourceAnalysisStartStore(database), registry, registry, platform, riverClient),
		Operations:            service.NewOperations(setupManager),
	})

	root := decodeSourceRoot(t, sourceRequest(t, handler, http.MethodPost, "/sources",
		fmt.Sprintf(`{"display_name":"Music","configured_path":%q,"processing_mode":"in_place"}`, t.TempDir())))
	location := persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: root.ID, RelativePath: "disc/track.flac", SizeBytes: 2048,
		Mtime: time.Now().UTC().Truncate(time.Microsecond), LastSeenScanGeneration: 1,
		ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatalf("store a source location: %v", err)
	}
	variant := &persistence.MediaVariant{
		ID: uuid.New(), SizeBytes: location.SizeBytes, AnalysisPolicyVersion: persistence.SourceAnalysisPolicyVersion,
		FFProbeVersion: "6.1.1", FFProbeJSON: json.RawMessage(technicalResultFixture),
		ObservedTags: json.RawMessage(`{"TITLE":["Song"]}`), InspectedAt: time.Now().UTC(), AppliedOperationID: uuid.New(),
	}
	if _, err := database.NewInsert().Model(variant).Exec(ctx); err != nil {
		t.Fatalf("store a media variant: %v", err)
	}
	if _, err := database.NewUpdate().Model((*persistence.SourceLocation)(nil)).
		Set("media_variant_id = ?", variant.ID).Where("id = ?", location.ID).Exec(ctx); err != nil {
		t.Fatalf("link the stored variant: %v", err)
	}

	detailPath := "/sources/" + root.ID.String() + "/locations/" + location.ID.String()
	detail := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet, detailPath, ""))
	if detail.AnalysisState != "analyzed" || detail.Result == nil || len(detail.Result.Streams) != 2 ||
		detail.ActiveAnalysisOperationID != nil {
		t.Fatalf("stored detail = %+v", detail)
	}

	if _, err := database.NewUpdate().Model((*persistence.SourceRoot)(nil)).
		Set("scan_generation = 1").Set("inventory_path = configured_path").
		Set("last_successful_scan_at = now()").Set("status = ?", persistence.SourceRootStatusAvailable).
		Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("establish the source root inventory: %v", err)
	}
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, InventoryPath: root.ConfiguredPath,
		RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		SHA256Enabled: true, OriginScanOperationID: uuid.New(),
	}
	failed := "previous digest failure"
	if err := inventory.StoreSourceAnalysisWork(ctx, work, []persistence.SourceAnalysisStepInput{{
		Step: persistence.SourceStepSHA256, State: "failed", SafeError: &failed,
	}}); err != nil {
		t.Fatalf("store current failed SHA-256 work: %v", err)
	}
	retryPath := detailPath + "/retry"
	rerunPath := detailPath + "/fingerprint/rerun"
	for _, test := range []struct {
		name, path, body string
	}{
		{name: "stale identity", path: retryPath, body: `{"step":"sha256","expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z"}`},
		{name: "step is not failed", path: retryPath, body: fmt.Sprintf(`{"step":"probe","expected_size_bytes":%d,"expected_mtime":%q}`, location.SizeBytes, location.Mtime.Format(time.RFC3339Nano))},
		{name: "rerun requires prior success", path: rerunPath, body: fmt.Sprintf(`{"expected_size_bytes":%d,"expected_mtime":%q}`, location.SizeBytes, location.Mtime.Format(time.RFC3339Nano))},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := sourceRequest(t, handler, http.MethodPost, test.path, test.body)
			if response.Code != http.StatusConflict {
				t.Fatalf("status=%d, want 409: %s", response.Code, response.Body.String())
			}
		})
	}
	retryResponse := sourceRequest(t, handler, http.MethodPost, detailPath+"/retry",
		fmt.Sprintf(`{"step":"sha256","expected_size_bytes":%d,"expected_mtime":%q}`, location.SizeBytes, location.Mtime.Format(time.RFC3339Nano)))
	if retryResponse.Code != http.StatusAccepted {
		t.Fatalf("admit a retry of the failed SHA-256 step: status=%d: %s", retryResponse.Code, retryResponse.Body.String())
	}
	active := decodeOperation(t, retryResponse.Body.Bytes())
	discovered := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet, detailPath, ""))
	if discovered.ActiveAnalysisOperationID == nil || *discovered.ActiveAnalysisOperationID != active.ID {
		t.Fatalf("active analysis = %v, want %s", discovered.ActiveAnalysisOperationID, active.ID)
	}
	operation := decodeOperation(t, sourceRequest(t, handler, http.MethodGet, "/operations/"+active.ID.String(), "").Body.Bytes())
	if operation.State != "queued" || operation.TargetSourceLocationID == nil || *operation.TargetSourceLocationID != location.ID {
		t.Fatalf("HTTP operation = %+v", operation)
	}
	persisted, err := setupManager.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatalf("read the persisted operation: %v", err)
	}
	if persisted.TargetWorkID == nil || *persisted.TargetWorkID != work.ID ||
		persisted.TargetStep == nil || *persisted.TargetStep != string(persistence.SourceStepSHA256) || persisted.RiverJobID == nil {
		t.Fatalf("persisted operation targets = %+v", persisted)
	}
	var queuedStepCount int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step
		WHERE work_id = ? AND step = 'sha256' AND state = 'queued' AND execution_operation_id = ?`,
		work.ID, active.ID).Scan(ctx, &queuedStepCount); err != nil {
		t.Fatalf("read the admitted SHA-256 step: %v", err)
	}
	if queuedStepCount != 1 {
		t.Fatalf("queued SHA-256 steps = %d, want 1", queuedStepCount)
	}
	var riverJobCount int
	if err := database.NewRaw("SELECT count(*) FROM river_job WHERE id = ?", *persisted.RiverJobID).Scan(ctx, &riverJobCount); err != nil {
		t.Fatalf("read the admitted River job: %v", err)
	}
	if riverJobCount != 1 {
		t.Fatalf("admitted River jobs = %d, want 1", riverJobCount)
	}

	// Complete the admitted hash retry through the same persistence boundary the
	// worker uses, but synthesize an execution failure rather than starting tools.
	analysis := persistence.NewSourceInventoryRepository(database)
	operations := service.NewOperations(setupManager)
	if err := operations.Running(ctx, active.ID, "hashing"); err != nil {
		t.Fatalf("mark hash retry running: %v", err)
	}
	shaAttempt, err := analysis.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: active.ID, OperationAttempt: persisted.Attempt,
		JobID: *persisted.RiverJobID, Step: persistence.SourceStepSHA256,
	})
	if err != nil {
		t.Fatalf("claim SHA-256 retry: %v", err)
	}
	if err := analysis.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: active.ID, OperationAttempt: persisted.Attempt,
		JobID: *persisted.RiverJobID, StepAttempt: shaAttempt, Step: persistence.SourceStepSHA256,
		SafeError: "synthetic hash execution failure",
	}); err != nil {
		t.Fatalf("persist SHA-256 execution failure: %v", err)
	}
	if err := analysis.SettleNormalizedSourceAnalysisOperation(ctx, active.ID, "failed", "sha256", "synthetic hash execution failure"); err != nil {
		t.Fatalf("settle failed SHA-256 operation: %v", err)
	}
	failedReadback := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet, detailPath, ""))
	if failedReadback.ActiveAnalysisOperationID != nil {
		t.Fatalf("failed SHA-256 operation remains active: %s", *failedReadback.ActiveAnalysisOperationID)
	}

	// Seed an already successful fingerprint and two verified fpcalc versions.
	// Rerun must pin the current active installation; failing that run must retain
	// the prior fingerprint and its provenance in the HTTP inspector projection.
	priorFingerprint := &persistence.SourceFingerprintResult{
		ID: uuid.New(), FPCalcVersion: "1.5.0", VersionBanner: "fpcalc version 1.5.0",
		AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "prior,compressed,fingerprint",
		ReportedDuration: 12.5, CalculatedAt: time.Now().UTC().Truncate(time.Microsecond),
		AppliedOperationID: uuid.New(), ParserContractVersion: 1,
	}
	if _, err := database.NewInsert().Model(priorFingerprint).Exec(ctx); err != nil {
		t.Fatalf("seed prior fingerprint result: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_fingerprint_result_id,success_reuse_origin)
		VALUES(?, 'fingerprint', 'succeeded', ?, 'executed')`, work.ID, priorFingerprint.ID); err != nil {
		t.Fatalf("seed successful fingerprint step: %v", err)
	}
	installFP := func(version string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		release := "fpcalc-test-" + version
		relativePath, err := tools.ManagedRelativePath(tools.PackageFPCalc, release)
		if err != nil {
			t.Fatalf("managed fpcalc path: %v", err)
		}
		verifiedAt := time.Now().UTC()
		installation := &persistence.ToolInstallation{
			ID: id, PackageKind: string(tools.PackageFPCalc), PlatformGOOS: platform.Platform.GOOS,
			PlatformGOARCH: platform.Platform.GOARCH, SourceName: "api-test", ReleaseIdentity: release,
			RelativePath: relativePath, State: "ready", ExecutableVersions: json.RawMessage(fmt.Sprintf(`{"fpcalc":%q}`, "fpcalc version "+version)),
			ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verifiedAt,
		}
		if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
			t.Fatalf("insert fpcalc %s: %v", version, err)
		}
		return id
	}
	_ = installFP("1.5.0")
	activeFP := installFP("1.6.0")
	if err := store.Set(ctx, settings.ActiveFPCalcInstallationKey, activeFP.String()); err != nil {
		t.Fatalf("activate fpcalc installation: %v", err)
	}
	rerunResponse := sourceRequest(t, handler, http.MethodPost, detailPath+"/fingerprint/rerun",
		fmt.Sprintf(`{"expected_size_bytes":%d,"expected_mtime":%q}`, location.SizeBytes, location.Mtime.Format(time.RFC3339Nano)))
	if rerunResponse.Code != http.StatusAccepted {
		t.Fatalf("rerun fingerprint status=%d: %s", rerunResponse.Code, rerunResponse.Body.String())
	}
	rerun := decodeOperation(t, rerunResponse.Body.Bytes())
	rerunRow, err := setupManager.GetOperation(ctx, rerun.ID)
	if err != nil {
		t.Fatalf("read fingerprint rerun operation: %v", err)
	}
	var rerunSnapshot persistence.SourceAnalysisOperationSnapshot
	if err := json.Unmarshal(rerunRow.InputSnapshot, &rerunSnapshot); err != nil {
		t.Fatalf("decode fingerprint rerun snapshot: %v", err)
	}
	if rerunSnapshot.TargetStep == nil || *rerunSnapshot.TargetStep != string(persistence.SourceStepFingerprint) || len(rerunSnapshot.Tools) != 0 || rerunSnapshot.RerunTarget == nil || !*rerunSnapshot.RerunTarget {
		t.Fatalf("fingerprint rerun snapshot did not preserve minimal explicit intent: %+v", rerunSnapshot)
	}
	if err := operations.Running(ctx, rerun.ID, "fingerprinting"); err != nil {
		t.Fatalf("mark fingerprint rerun running: %v", err)
	}
	fingerprintAttempt, err := analysis.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: rerun.ID, OperationAttempt: rerunRow.Attempt,
		JobID: *rerunRow.RiverJobID, Step: persistence.SourceStepFingerprint,
	})
	if err != nil {
		t.Fatalf("claim fingerprint rerun: %v", err)
	}
	if err := analysis.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: rerun.ID, OperationAttempt: rerunRow.Attempt,
		JobID: *rerunRow.RiverJobID, StepAttempt: fingerprintAttempt, Step: persistence.SourceStepFingerprint,
		SafeError: "synthetic fpcalc execution failure",
	}); err != nil {
		t.Fatalf("persist fingerprint rerun failure: %v", err)
	}
	if err := analysis.SettleNormalizedSourceAnalysisOperation(ctx, rerun.ID, "failed", "fingerprint", "synthetic fpcalc execution failure"); err != nil {
		t.Fatalf("settle failed fingerprint rerun: %v", err)
	}
	retained := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet, detailPath, ""))
	fingerprintStepFound := false
	for _, step := range retained.Steps {
		if step.Name == string(persistence.SourceStepFingerprint) {
			fingerprintStepFound = true
			if step.State != "failed" || step.Fingerprint == nil || step.Fingerprint.Value != priorFingerprint.Fingerprint || step.Fingerprint.Version != priorFingerprint.FPCalcVersion {
				t.Fatalf("failed rerun inspector step = %+v; prior success should remain readable", step)
			}
		}
	}
	if !fingerprintStepFound {
		t.Fatalf("inspector omitted fingerprint step after failed rerun: %+v", retained.Steps)
	}
	retryFingerprintResponse := sourceRequest(t, handler, http.MethodPost, detailPath+"/retry",
		fmt.Sprintf(`{"step":"fingerprint","expected_size_bytes":%d,"expected_mtime":%q}`, location.SizeBytes, location.Mtime.Format(time.RFC3339Nano)))
	if retryFingerprintResponse.Code != http.StatusAccepted {
		t.Fatalf("retry failed fingerprint step status=%d: %s", retryFingerprintResponse.Code, retryFingerprintResponse.Body.String())
	}
	retryFingerprint := decodeOperation(t, retryFingerprintResponse.Body.Bytes())
	retryFingerprintRow, err := setupManager.GetOperation(ctx, retryFingerprint.ID)
	if err != nil {
		t.Fatalf("read exact fingerprint retry operation: %v", err)
	}
	var retryFingerprintSnapshot persistence.SourceAnalysisOperationSnapshot
	if err := json.Unmarshal(retryFingerprintRow.InputSnapshot, &retryFingerprintSnapshot); err != nil {
		t.Fatalf("decode exact fingerprint retry snapshot: %v", err)
	}
	if retryFingerprintSnapshot.TargetStep == nil || *retryFingerprintSnapshot.TargetStep != string(persistence.SourceStepFingerprint) || len(retryFingerprintSnapshot.Tools) != 0 || retryFingerprintSnapshot.RerunTarget == nil || *retryFingerprintSnapshot.RerunTarget {
		t.Fatalf("fingerprint retry did not preserve exact failed-step intent without tool pins: %+v", retryFingerprintSnapshot)
	}
	assertSettled := func(id uuid.UUID) {
		t.Helper()
		readback := decodeOperation(t, sourceRequest(t, handler, http.MethodGet, "/operations/"+id.String(), "").Body.Bytes())
		if readback.State != "succeeded" {
			t.Fatalf("completed HTTP operation = %+v", readback)
		}
		var holds int
		if err := database.NewRaw(`SELECT
			(SELECT count(*) FROM operation_source_work_hold WHERE operation_id=?) +
			(SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?)`, id, id).Scan(ctx, &holds); err != nil {
			t.Fatal(err)
		}
		if holds != 0 {
			t.Fatalf("completed operation retains %d holds", holds)
		}
	}
	completeFingerprint := func(row *persistence.Operation, version, value string) {
		t.Helper()
		if err := operations.Running(ctx, row.ID, "fingerprinting"); err != nil {
			t.Fatal(err)
		}
		attempt, err := analysis.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
			WorkID: work.ID, OperationID: row.ID, OperationAttempt: row.Attempt,
			JobID: *row.RiverJobID, Step: persistence.SourceStepFingerprint,
		})
		if err != nil {
			t.Fatal(err)
		}
		result := *priorFingerprint
		result.ID, result.FPCalcVersion, result.VersionBanner = uuid.New(), version, "fpcalc version "+version
		result.Fingerprint, result.CalculatedAt = value, time.Now().UTC().Truncate(time.Microsecond)
		if _, err := analysis.ApplySourceFingerprint(ctx, persistence.SourceFingerprintApply{
			WorkID: work.ID, OperationID: row.ID, OperationAttempt: row.Attempt,
			JobID: *row.RiverJobID, StepAttempt: attempt, Result: result,
		}); err != nil {
			t.Fatal(err)
		}
		if err := analysis.SettleNormalizedSourceAnalysisOperation(ctx, row.ID, "succeeded", "fingerprint", ""); err != nil {
			t.Fatal(err)
		}
		readback := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet, detailPath, ""))
		if readback.ActiveAnalysisOperationID != nil {
			t.Fatal("completed fingerprint operation remains active")
		}
		found := false
		for _, step := range readback.Steps {
			if step.Name == "fingerprint" {
				found = true
				if step.State != "succeeded" || step.Fingerprint == nil || step.Fingerprint.Value != value || step.Fingerprint.Version != version || step.Fingerprint.AppliedOperationID != row.ID {
					t.Fatalf("completed fingerprint readback = %+v", step)
				}
			}
			if step.Name == "sha256" && (step.State != "failed" || step.SafeError == nil || *step.SafeError != "synthetic hash execution failure") {
				t.Fatalf("fingerprint execution changed SHA sibling: %+v", step)
			}
		}
		if !found {
			t.Fatal("completed fingerprint missing from inspector")
		}
		assertSettled(row.ID)
	}
	completeFingerprint(retryFingerprintRow, "1.6.0", "synthetic-successful-retry")
	newActiveFP := installFP("1.7.0")
	if err := store.Set(ctx, settings.ActiveFPCalcInstallationKey, newActiveFP.String()); err != nil {
		t.Fatal(err)
	}
	successfulRerunResponse := sourceRequest(t, handler, http.MethodPost, rerunPath,
		fmt.Sprintf(`{"expected_size_bytes":%d,"expected_mtime":%q}`, location.SizeBytes, location.Mtime.Format(time.RFC3339Nano)))
	if successfulRerunResponse.Code != http.StatusAccepted {
		t.Fatalf("successful rerun admission: %d %s", successfulRerunResponse.Code, successfulRerunResponse.Body.String())
	}
	successfulRerun := decodeOperation(t, successfulRerunResponse.Body.Bytes())
	successfulRerunRow, err := setupManager.GetOperation(ctx, successfulRerun.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(successfulRerunRow.InputSnapshot)
	if err != nil || len(snapshot.Tools) != 0 || !successfulRerunRow.RerunTarget || snapshot.TargetStep == nil || *snapshot.TargetStep != "fingerprint" {
		t.Fatalf("successful rerun minimal intent = %+v, %v", snapshot, err)
	}
	completeFingerprint(successfulRerunRow, "1.7.0", "synthetic-successful-rerun")

	// A probe retry admitted over HTTP also completes without touching the hash
	// failure or the newly selected fingerprint. No external process is simulated
	// by the endpoint: only the worker's typed apply boundary is driven here.
	ffmpegID := uuid.New()
	ffmpegPath, err := tools.ManagedRelativePath(tools.PackageFFmpeg, "ffmpeg-api-test")
	if err != nil {
		t.Fatal(err)
	}
	verifiedAt := time.Now().UTC()
	ffmpeg := &persistence.ToolInstallation{
		ID: ffmpegID, PackageKind: "ffmpeg", PlatformGOOS: platform.Platform.GOOS, PlatformGOARCH: platform.Platform.GOARCH,
		SourceName: "api-test", ReleaseIdentity: "ffmpeg-api-test", RelativePath: ffmpegPath, State: "ready",
		ExecutableVersions: json.RawMessage(`{"ffmpeg":"ffmpeg version 6.1.1","ffprobe":"ffprobe version 6.1.1"}`),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verifiedAt,
	}
	if _, err := database.NewInsert().Model(ffmpeg).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, settings.ActiveFFmpegInstallationKey, ffmpegID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,safe_error)
		VALUES(?,'probe','failed','synthetic prior probe failure')`, work.ID); err != nil {
		t.Fatal(err)
	}
	probeResponse := sourceRequest(t, handler, http.MethodPost, retryPath,
		fmt.Sprintf(`{"step":"probe","expected_size_bytes":%d,"expected_mtime":%q}`, location.SizeBytes, location.Mtime.Format(time.RFC3339Nano)))
	if probeResponse.Code != http.StatusAccepted {
		t.Fatalf("probe retry admission: %d %s", probeResponse.Code, probeResponse.Body.String())
	}
	probeOperation := decodeOperation(t, probeResponse.Body.Bytes())
	probeRow, err := setupManager.GetOperation(ctx, probeOperation.ID)
	if err != nil {
		t.Fatal(err)
	}
	probeSnapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(probeRow.InputSnapshot)
	if err != nil || len(probeSnapshot.Tools) != 0 || probeSnapshot.TargetStep == nil || *probeSnapshot.TargetStep != "probe" {
		t.Fatalf("probe-only retry inputs = %+v, %v", probeSnapshot, err)
	}
	if err := operations.Running(ctx, probeRow.ID, "probing"); err != nil {
		t.Fatal(err)
	}
	failedProbeAttempt, err := analysis.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: probeRow.ID, OperationAttempt: probeRow.Attempt, JobID: *probeRow.RiverJobID, Step: persistence.SourceStepProbe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := analysis.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: probeRow.ID, OperationAttempt: probeRow.Attempt, JobID: *probeRow.RiverJobID,
		StepAttempt: failedProbeAttempt, Step: persistence.SourceStepProbe, SafeError: "synthetic probe retry failure",
	}); err != nil {
		t.Fatal(err)
	}
	if err := analysis.SettleNormalizedSourceAnalysisOperation(ctx, probeRow.ID, "failed", "probe", "synthetic probe retry failure"); err != nil {
		t.Fatal(err)
	}
	failedProbeReadback := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet, detailPath, ""))
	for _, step := range failedProbeReadback.Steps {
		if step.Name == "probe" && (step.State != "failed" || step.SafeError == nil || *step.SafeError != "synthetic probe retry failure") {
			t.Fatalf("failed probe retry readback = %+v", step)
		}
		if step.Name == "fingerprint" && (step.Fingerprint == nil || step.Fingerprint.Value != "synthetic-successful-rerun") {
			t.Fatalf("failed probe retry lost fingerprint sibling: %+v", step)
		}
	}
	probeResponse = sourceRequest(t, handler, http.MethodPost, retryPath,
		fmt.Sprintf(`{"step":"probe","expected_size_bytes":%d,"expected_mtime":%q}`, location.SizeBytes, location.Mtime.Format(time.RFC3339Nano)))
	if probeResponse.Code != http.StatusAccepted {
		t.Fatalf("probe retry after failure: %d %s", probeResponse.Code, probeResponse.Body.String())
	}
	probeOperation = decodeOperation(t, probeResponse.Body.Bytes())
	probeRow, err = setupManager.GetOperation(ctx, probeOperation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := operations.Running(ctx, probeRow.ID, "probing"); err != nil {
		t.Fatal(err)
	}
	probeAttempt, err := analysis.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: probeRow.ID, OperationAttempt: probeRow.Attempt, JobID: *probeRow.RiverJobID, Step: persistence.SourceStepProbe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := analysis.ApplySourceProbe(ctx, persistence.SourceProbeApply{
		WorkID: work.ID, OperationID: probeRow.ID, OperationAttempt: probeRow.Attempt, JobID: *probeRow.RiverJobID,
		StepAttempt: probeAttempt, SizeBytes: location.SizeBytes, AnalysisPolicy: persistence.SourceAnalysisPolicyVersion,
		FFProbeVersion: "ffprobe version 6.1.1", FFProbeJSON: json.RawMessage(technicalResultFixture),
		ObservedTags: json.RawMessage(`{"TITLE":["Song"]}`), InspectedAt: time.Now().UTC(), AudioStreamCount: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := analysis.SettleNormalizedSourceAnalysisOperation(ctx, probeRow.ID, "succeeded", "probe", ""); err != nil {
		t.Fatal(err)
	}
	probeReadback := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet, detailPath, ""))
	if probeReadback.ActiveAnalysisOperationID != nil || probeReadback.Result == nil || probeReadback.Result.AppliedOperationID != probeRow.ID {
		t.Fatalf("completed probe readback = %+v", probeReadback)
	}
	for _, step := range probeReadback.Steps {
		if step.Name == "fingerprint" && (step.Fingerprint == nil || step.Fingerprint.Value != "synthetic-successful-rerun") {
			t.Fatalf("probe retry changed fingerprint sibling: %+v", step)
		}
		if step.Name == "sha256" && (step.State != "failed" || step.SafeError == nil || *step.SafeError != "synthetic hash execution failure") {
			t.Fatalf("probe retry changed SHA sibling: %+v", step)
		}
	}
	assertSettled(probeRow.ID)

	// The settings endpoint is backed by the same PostgreSQL registry used by
	// source analysis. Verify both values survive a real HTTP write/read cycle.
	for _, enabled := range []bool{false, true} {
		body := fmt.Sprintf(`{"enabled":%t}`, enabled)
		if response := sourceRequest(t, handler, http.MethodPut, "/settings/sha256", body); response.Code != http.StatusNoContent {
			t.Fatalf("set SHA-256 enabled=%t status=%d: %s", enabled, response.Code, response.Body.String())
		}
		response := sourceRequest(t, handler, http.MethodGet, "/settings", "")
		if response.Code != http.StatusOK {
			t.Fatalf("read settings status=%d: %s", response.Code, response.Body.String())
		}
		var settingsResponse api.SetupStateBody
		if err := json.Unmarshal(response.Body.Bytes(), &settingsResponse); err != nil {
			t.Fatalf("decode settings: %v", err)
		}
		if settingsResponse.Settings.SHA256Enabled != enabled {
			t.Fatalf("settings SHA-256 enabled=%t, want %t", settingsResponse.Settings.SHA256Enabled, enabled)
		}
		stored, exists, err := store.Get(ctx, settings.SHA256EnabledKey)
		if err != nil || !exists || stored != fmt.Sprint(enabled) {
			t.Fatalf("persisted SHA-256 setting = %q, %t, %v; want %t", stored, exists, err, enabled)
		}
	}

	// Two locations that share one canonical digest expose that same successful
	// SHA result through the inspector, rather than an invented location-local hash.
	digest := make([]byte, sha256.Size)
	digest[0] = 0x42
	calculatedAt := time.Now().UTC().Truncate(time.Microsecond)
	algorithm := "SHA-256"
	shaOperationID := uuid.New()
	sharedVariant := &persistence.SourceMediaVariant{
		ID: uuid.New(), SizeBytes: 512, SourceSHA256: digest,
		SHA256CalculatedAt: &calculatedAt, SHA256Algorithm: &algorithm, SHA256AppliedOperationID: &shaOperationID,
	}
	if _, err := database.NewInsert().Model(sharedVariant).Exec(ctx); err != nil {
		t.Fatalf("insert shared SHA-256 identity: %v", err)
	}
	sharedPaths := []string{"shared/one.flac", "shared/two.flac"}
	for _, relativePath := range sharedPaths {
		sharedLocation := persistence.SourceLocation{
			ID: uuid.New(), SourceRootID: root.ID, RelativePath: relativePath, SizeBytes: 512,
			Mtime: time.Now().UTC().Truncate(time.Microsecond), LastSeenScanGeneration: 1,
			ProbeStatus: persistence.SourceProbeStatusAudio, MediaVariantID: &sharedVariant.ID,
		}
		if _, err := database.NewInsert().Model(&sharedLocation).Exec(ctx); err != nil {
			t.Fatalf("insert shared location %s: %v", relativePath, err)
		}
		sharedWork := &persistence.SourceAnalysisWork{
			ID: uuid.New(), LocationID: sharedLocation.ID, SourceRootID: root.ID,
			ConfiguredPath: root.ConfiguredPath, InventoryPath: root.ConfiguredPath,
			RelativePath: relativePath, SizeBytes: sharedLocation.SizeBytes, Mtime: sharedLocation.Mtime,
			SHA256Enabled: true, OriginScanOperationID: uuid.New(),
		}
		if err := inventory.StoreSourceAnalysisWork(ctx, sharedWork, []persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}}); err != nil {
			t.Fatalf("store shared analysis work for %s: %v", relativePath, err)
		}
		if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='succeeded',success_sha_variant_id=?,success_reuse_origin='sha256' WHERE work_id=? AND step='sha256'`, sharedVariant.ID, sharedWork.ID); err != nil {
			t.Fatalf("seed shared SHA success for %s: %v", relativePath, err)
		}
		sharedDetail := decodeSourceLocationDetail(t, sourceRequest(t, handler, http.MethodGet,
			"/sources/"+root.ID.String()+"/locations/"+sharedLocation.ID.String(), ""))
		found := false
		for _, step := range sharedDetail.Steps {
			if step.Name == string(persistence.SourceStepSHA256) {
				found = true
				if step.SHA256 == nil || step.SHA256.Value != hex.EncodeToString(digest) {
					t.Fatalf("shared SHA inspector result for %s = %+v, want %x", relativePath, step.SHA256, digest)
				}
			}
		}
		if !found {
			t.Fatalf("shared SHA inspector omitted hash step for %s: %+v", relativePath, sharedDetail.Steps)
		}
	}

	foreignRoot := decodeSourceRoot(t, sourceRequest(t, handler, http.MethodPost, "/sources",
		fmt.Sprintf(`{"display_name":"Other","configured_path":%q,"processing_mode":"in_place"}`, t.TempDir())))
	foreign := persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: foreignRoot.ID, RelativePath: "other.flac", SizeBytes: 64,
		Mtime: time.Now().UTC(), LastSeenScanGeneration: 1, ProbeStatus: persistence.SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(&foreign).Exec(ctx); err != nil {
		t.Fatalf("store a foreign location: %v", err)
	}
	foreignResponse := sourceRequest(t, handler, http.MethodGet,
		"/sources/"+root.ID.String()+"/locations/"+foreign.ID.String(), "")
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign location status=%d, want 404: %s", foreignResponse.Code, foreignResponse.Body.String())
	}

	// The former implicit /analyze API is intentionally gone; callers must now
	// name a failed normalized step and provide the current stat identity.
	if response := sourceRequest(t, handler, http.MethodPost, detailPath+"/analyze",
		`{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z"}`); response.Code != http.StatusNotFound {
		t.Fatalf("legacy analyze status=%d, want 404: %s", response.Code, response.Body.String())
	}
	for _, body := range []string{
		`{"expected_size_bytes":-1,"expected_mtime":"2026-09-02T08:30:00Z","step":"sha256"}`,
		`{"expected_size_bytes":2048,"expected_mtime":"soon","step":"sha256"}`,
		`{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z"}`,
		`{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z","step":"sha256","ffprobe_version":"arbitrary"}`,
	} {
		response := sourceRequest(t, handler, http.MethodPost, retryPath, body)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("body %s status=%d, want 422: %s", body, response.Code, response.Body.String())
		}
	}
	if response := sourceRequest(t, handler, http.MethodPost, rerunPath,
		`{"expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z","fpcalc_version":"arbitrary"}`); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rerun with caller-selected tool version status=%d, want 422: %s", response.Code, response.Body.String())
	}

	diagnostic := settings.PlatformState{
		Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}, Diagnostic: true, Reason: "instance platform mismatch",
	}
	readOnly := api.HandlerWithDependencies(api.Dependencies{
		Setup:                 service.NewSetup(store, registry, diagnostic, setupManager, nil),
		SourceLocationDetails: service.NewSourceLocationDetails(inventory),
		SourceAnalysis:        service.NewSourceAnalysisOperations(persistence.NewSourceAnalysisStartStore(database), registry, registry, diagnostic, nil),
		Operations:            service.NewOperations(setupManager),
	})
	if response := sourceRequest(t, readOnly, http.MethodGet, detailPath, ""); response.Code != http.StatusOK {
		t.Fatalf("diagnostic detail status=%d, want 200: %s", response.Code, response.Body.String())
	}
	if response := sourceRequest(t, readOnly, http.MethodPost, retryPath,
		`{"step":"sha256","expected_size_bytes":2048,"expected_mtime":"2026-09-02T08:30:00Z"}`); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("diagnostic analysis retry status=%d, want 503: %s", response.Code, response.Body.String())
	}
}
