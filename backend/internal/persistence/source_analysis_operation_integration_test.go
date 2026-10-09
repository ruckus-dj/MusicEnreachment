//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestNormalizedSourceAnalysisAdmissionClaimAndTerminalReleaseWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)

	// SHA-only admission pins work but must not manufacture a tool selection or
	// acquire a tool read hold.
	root := createInventoryRoot(t, ctx, repository, "/srv/normalized-sha-only")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "a/sha.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
	)
	probeVariantID := uuid.New()
	inspectedAt := time.Now().UTC()
	if _, err := database.ExecContext(ctx, `INSERT INTO media_variant
		(id,size_bytes,ffprobe_version,ffprobe_json,analysis_policy_version,observed_tags,inspected_at,applied_operation_id,audio_stream_count)
		VALUES (?,?,?,?::jsonb,?,?,?, ?,1)`, probeVariantID, location.SizeBytes, "ffprobe version 7.1.2", `{"format":{"format_name":"flac"}}`, persistence.SourceAnalysisPolicyVersion, `{}`, inspectedAt, uuid.New()); err != nil {
		t.Fatalf("insert prior successful probe result: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='succeeded',success_probe_variant_id=?,success_reuse_origin='executed' WHERE work_id=? AND step='probe'`, probeVariantID, work.ID); err != nil {
		t.Fatalf("preserve successful sibling step fixture: %v", err)
	}
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil)
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("admit SHA-only batch: %v", err)
	}
	if operation.RiverJobID == nil {
		t.Fatal("admitted operation has no River job")
	}
	assertOperationHoldCounts(t, ctx, database, operation.ID, 1, 0)
	if _, err := database.NewRaw(`UPDATE operation SET state='running',stage='hashing',started_at=now() WHERE id=?`, operation.ID).Exec(ctx); err != nil {
		t.Fatalf("start queued operation: %v", err)
	}
	claim := persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: *operation.RiverJobID, Step: persistence.SourceStepSHA256,
	}
	if _, err := repository.ClaimSourceAnalysisStep(ctx, claim); err != nil {
		t.Fatalf("claim queued SHA step: %v", err)
	}
	if _, err := repository.ClaimSourceAnalysisStep(ctx, claim); err == nil {
		t.Fatal("duplicate claim of an already-running SHA step succeeded")
	}
	stale := persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID + 1}
	if err := repository.SettleNormalizedSourceAnalysisDelivery(ctx, operation.ID, stale, "failed", "hashing", "stale delivery"); err == nil {
		t.Fatal("stale delivery settled the current operation")
	}
	assertOperationHoldCounts(t, ctx, database, operation.ID, 1, 0)
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, operation.ID, "failed", "hashing", "hash failed"); err != nil {
		t.Fatalf("terminal failure and hold release: %v", err)
	}
	assertOperationHoldCounts(t, ctx, database, operation.ID, 0, 0)
	var activeTriples int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE execution_operation_id=? OR execution_job_id=?`, operation.ID, *operation.RiverJobID).Scan(ctx, &activeTriples); err != nil {
		t.Fatal(err)
	}
	if activeTriples != 0 {
		t.Fatalf("terminal operation retained %d delivery triples", activeTriples)
	}
	var sibling persistence.SourceAnalysisStep
	if err := database.NewSelect().Model(&sibling).Where("work_id=?", work.ID).Where("step='probe'").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if sibling.State != "succeeded" || sibling.SuccessProbeVariantID == nil || *sibling.SuccessProbeVariantID != probeVariantID {
		t.Fatalf("terminal SHA failure changed successful probe sibling: %+v", sibling)
	}
	stored, err := persistence.NewSetupManagerRepository(database).GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TargetSourceRootID != nil {
		t.Fatalf("terminal root target was not cleared: %v", stored.TargetSourceRootID)
	}
}

func TestEnumeratedWorkAdmitsPendingMetadataWithoutToolsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/enumeration-metadata-admission")
	scan := newSourceScanOperation(t, ctx, database, root, "running")
	mtime := probeMtime()
	if err := repository.ReplaceSourceScanCandidates(ctx, scan.ID, []persistence.SourceScanCandidateInput{
		enumerationCandidate("track.flac", 1024, mtime),
	}); err != nil {
		t.Fatalf("store enumeration candidate: %v", err)
	}
	if err := repository.ApplySourceEnumeration(ctx, enumerationApply(scan, root, nil)); err != nil {
		t.Fatalf("apply enumeration: %v", err)
	}
	// Normalized analysis admission is root-exclusive. The enumerated scan must
	// first reach a terminal state, just as it does before pending work is
	// dispatched by the production scan worker.
	setOperationState(t, ctx, database, scan.ID, "succeeded")
	location := readLocation(t, ctx, database, root.ID, "track.flac")
	var work persistence.SourceAnalysisWork
	if err := database.NewRaw(`SELECT * FROM source_analysis_work WHERE current_location_id=?`, location.ID).Scan(ctx, &work); err != nil {
		t.Fatalf("read enumerated analysis work: %v", err)
	}
	var metadata persistence.SourceAnalysisStep
	if err := database.NewRaw(`SELECT * FROM source_analysis_step WHERE work_id=? AND step='metadata'`, work.ID).Scan(ctx, &metadata); err != nil {
		t.Fatalf("read pending metadata step: %v", err)
	}
	if metadata.State != "pending" {
		t.Fatalf("enumerated metadata state = %q, want pending", metadata.State)
	}

	operation := normalizedOperation(t, root, location, &work, persistence.SourceAnalysisModeBatch, nil, nil, false, false, nil, true)
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("admit pending metadata: %v", err)
	}
	var selected []persistence.SourceAnalysisStepSelection
	snapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(operation.InputSnapshot)
	if err != nil {
		t.Fatalf("decode admitted operation snapshot: %v", err)
	}
	selected = snapshot.SelectedSteps
	if len(selected) != 1 || selected[0].WorkID != work.ID || selected[0].Step != persistence.SourceStepMetadata {
		t.Fatalf("admitted selected steps = %+v, want metadata for enumerated work", selected)
	}
	if operation.ToolsReadRequired || len(operation.SourceAnalysisTools) != 0 {
		t.Fatalf("metadata admission unexpectedly selected tools: %+v", operation.SourceAnalysisTools)
	}
	var queuedState string
	if err := database.NewRaw(`SELECT state FROM source_analysis_step WHERE work_id=? AND step='metadata'`, work.ID).Scan(ctx, &queuedState); err != nil || queuedState != "queued" {
		t.Fatalf("admitted metadata state = %q, %v; want queued", queuedState, err)
	}
	assertOperationHoldCounts(t, ctx, database, operation.ID, 1, 0)
}

func TestNormalizedSourceAnalysisAdmissionRejectsWrongStepAndRollsBackFailedOperationInsertWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)

	root := createInventoryRoot(t, ctx, repository, "/srv/normalized-exact-step")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "a/exact.flac", 512, probeMtime())
	establishInventory(t, ctx, database, root)
	priorFailure := "previous SHA attempt failed"
	work := normalizedWork(t, ctx, repository, root, location, true,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "failed", SafeError: &priorFailure},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
	)
	step := string(persistence.SourceStepSHA256)
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &step, true, false, nil)
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("admit exact SHA target: %v", err)
	}
	if _, err := database.NewRaw(`UPDATE operation SET state='running',stage='hashing',started_at=now() WHERE id=?`, operation.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wrong := persistence.SourceStepClaim{
		WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
		JobID: *operation.RiverJobID, Step: persistence.SourceStepProbe,
	}
	if _, err := repository.ClaimSourceAnalysisStep(ctx, wrong); err == nil {
		t.Fatal("claim for a different step than the single-step target succeeded")
	}
	if _, err := database.ExecContext(ctx, `UPDATE source_analysis_step SET state='queued',execution_operation_id=?,execution_operation_attempt=?,execution_job_id=? WHERE work_id=? AND step='probe'`, operation.ID, operation.Attempt, *operation.RiverJobID, work.ID); err == nil {
		t.Fatal("deferred database guard accepted an execution triple for a non-target step")
	}

	// Force the operation INSERT to fail after River's transactional insert; the
	// River row must roll back with the operation.
	otherRoot := createInventoryRoot(t, ctx, repository, "/srv/normalized-insert-rollback")
	otherLocation := insertAnalysisLocation(t, ctx, database, otherRoot.ID, "a/rollback.flac", 256, probeMtime())
	establishInventory(t, ctx, database, otherRoot)
	otherWork := normalizedWork(t, ctx, repository, otherRoot, otherLocation, true, persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"})
	duplicateID := uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO operation (id,kind,state,stage,input_snapshot,safe_error,finished_at)
		VALUES (?, 'move_tools_root','failed','failed','{"schema_version":1,"old_root":"/srv/tools-old","new_root":"/srv/tools-new"}','fixture failure',now())`, duplicateID); err != nil {
		t.Fatalf("insert operation primary-key collision fixture: %v", err)
	}
	rollback := normalizedOperation(t, otherRoot, otherLocation, otherWork, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil)
	rollback.ID = duplicateID
	jobsBefore := analysisJobs(t, ctx, database)
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, rollback, client, service.SourceAnalysisJobArgs{OperationID: rollback.ID}, nil); err == nil {
		t.Fatal("duplicate operation insert unexpectedly succeeded")
	}
	if jobsAfter := analysisJobs(t, ctx, database); jobsAfter != jobsBefore {
		t.Fatalf("River job count after failed operation insert = %d, want %d", jobsAfter, jobsBefore)
	}
}

func TestNormalizedSourceAnalysisGuardsOperationSourceSelectorsAfterAdmissionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)

	for _, mode := range []string{string(persistence.SourceAnalysisModeBatch), string(persistence.SourceAnalysisModeSingleStep)} {
		t.Run(string(mode), func(t *testing.T) {
			root := createInventoryRoot(t, ctx, repository, "/srv/source-selector-"+string(mode))
			otherRoot := createInventoryRoot(t, ctx, repository, "/srv/source-selector-other-"+string(mode))
			location := insertAnalysisLocation(t, ctx, database, root.ID, "selected.flac", 1024, probeMtime())
			otherLocation := insertAnalysisLocation(t, ctx, database, root.ID, "other.flac", 1024, probeMtime())
			foreignLocation := insertAnalysisLocation(t, ctx, database, otherRoot.ID, "foreign.flac", 1024, probeMtime())
			establishInventory(t, ctx, database, root)
			establishInventory(t, ctx, database, otherRoot)
			stepInput := persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"}
			if mode == persistence.SourceAnalysisModeSingleStep {
				safeError := "previous SHA attempt failed"
				stepInput = persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "failed", SafeError: &safeError}
			}
			work := normalizedWork(t, ctx, repository, root, location, true, stepInput)
			var targetWorkID *uuid.UUID
			var targetStep *string
			if mode == persistence.SourceAnalysisModeSingleStep {
				step := string(persistence.SourceStepSHA256)
				targetWorkID, targetStep = &work.ID, &step
			}
			operation := normalizedOperation(t, root, location, work, mode, targetWorkID, targetStep, true, false, nil)
			if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
				t.Fatalf("admit %s analysis: %v", mode, err)
			}

			if _, err := database.ExecContext(ctx, `UPDATE operation SET target_source_root_id=? WHERE id=?`, otherRoot.ID, operation.ID); err == nil {
				t.Fatalf("%s operation accepted a root selector unrelated to its held work", mode)
			}
			if mode == persistence.SourceAnalysisModeBatch {
				if _, err := database.ExecContext(ctx, `UPDATE operation SET target_source_location_id=? WHERE id=?`, location.ID, operation.ID); err == nil {
					t.Fatal("batch operation accepted a single-location selector")
				}
			} else {
				for _, mismatch := range []struct {
					name       string
					rootID     uuid.UUID
					locationID uuid.UUID
				}{
					{name: "same-root different location", rootID: root.ID, locationID: otherLocation.ID},
					{name: "foreign-root location", rootID: otherRoot.ID, locationID: foreignLocation.ID},
				} {
					t.Run(mismatch.name, func(t *testing.T) {
						if _, err := database.ExecContext(ctx, `UPDATE operation SET target_source_root_id=?,target_source_location_id=? WHERE id=?`, mismatch.rootID, mismatch.locationID, operation.ID); err == nil {
							t.Fatal("single-step operation accepted a location selector unrelated to its held work")
						}
					})
				}
			}

			foreignWork := normalizedWork(t, ctx, repository, otherRoot, foreignLocation, true,
				persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"})
			if err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				if _, err := tx.NewRaw(`INSERT INTO operation_source_work_hold(operation_id,work_id) VALUES (?,?)`, operation.ID, foreignWork.ID).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewRaw(`UPDATE operation SET input_snapshot=jsonb_set(input_snapshot,'{work_ids}',input_snapshot->'work_ids'||jsonb_build_array(?::text)) WHERE id=?`, foreignWork.ID, operation.ID).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewRaw(`UPDATE source_analysis_step SET state='queued',execution_operation_id=?,execution_operation_attempt=?,execution_job_id=? WHERE work_id=? AND step='sha256'`, operation.ID, operation.Attempt, *operation.RiverJobID, foreignWork.ID).Exec(ctx); err != nil {
					return err
				}
				_, err := tx.NewRaw(`SET CONSTRAINTS source_analysis_step_membership_guard IMMEDIATE`).Exec(ctx)
				return err
			}); err == nil {
				t.Fatalf("%s execution accepted held work from another root", mode)
			}
		})
	}
}

func TestNormalizedSourceAnalysisTerminalReleasesToolAndWorkHoldsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/normalized-tool-release")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "a/fingerprint.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	priorFailure := "previous fingerprint attempt failed"
	work := normalizedWork(t, ctx, repository, root, location, false,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepFingerprint, State: "failed", SafeError: &priorFailure},
	)
	installation := insertAnalysisFPCalcFixture(t, ctx, database, "fpcalc-test-release", "1.5.1")
	installationID := installation.ID
	step := string(persistence.SourceStepFingerprint)
	tool := persistence.SourceAnalysisToolSelection{
		PackageKind: "fpcalc", InstallationID: installationID, RelativePath: installation.RelativePath,
		Executable: "fpcalc", Version: "1.5.1", VersionBanner: "fpcalc version 1.5.1",
	}
	operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeSingleStep, &work.ID, &step, false, false, []persistence.SourceAnalysisToolSelection{tool})
	if err := repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
		t.Fatalf("admit fpcalc operation: %v", err)
	}
	assertOperationHoldCounts(t, ctx, database, operation.ID, 1, 1)
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, operation.ID, "failed", "fingerprint", "fingerprint unavailable"); err != nil {
		t.Fatalf("fail operation and release holds: %v", err)
	}
	assertOperationHoldCounts(t, ctx, database, operation.ID, 0, 0)
	var triples int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE execution_operation_id=? OR execution_job_id=?`, operation.ID, *operation.RiverJobID).Scan(ctx, &triples); err != nil {
		t.Fatal(err)
	}
	if triples != 0 {
		t.Fatalf("terminal operation retained %d step execution triples", triples)
	}
}

func insertAnalysisFPCalcFixture(t *testing.T, ctx context.Context, database *bun.DB, releaseIdentity, version string) *persistence.ToolInstallation {
	t.Helper()
	relativePath, err := tools.ManagedRelativePath(tools.PackageFPCalc, releaseIdentity)
	if err != nil {
		t.Fatalf("managed fpcalc analysis installation path: %v", err)
	}
	now := time.Now().UTC()
	installation := &persistence.ToolInstallation{
		ID: uuid.New(), PackageKind: string(tools.PackageFPCalc), PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		SourceName: "analysis-test", ReleaseIdentity: releaseIdentity, RelativePath: relativePath,
		State: "ready", ExecutableVersions: verifiedExecutableVersionMetadata(t, tools.PackageFPCalc, "darwin", version),
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &now,
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		t.Fatalf("insert verified fpcalc installation: %v", err)
	}
	return installation
}

func TestNormalizedSourceAnalysisConcurrentAdmissionQueuesWorkOnceWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	root := createInventoryRoot(t, ctx, repository, "/srv/normalized-duplicate-admission")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "a/one.flac", 768, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true, persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"})
	operations := []*persistence.Operation{
		normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil),
		normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, true, false, nil),
	}
	start := make(chan struct{})
	errorsByStart := make(chan error, len(operations))
	var wait sync.WaitGroup
	for _, operation := range operations {
		wait.Add(1)
		go func(operation *persistence.Operation) {
			defer wait.Done()
			<-start
			errorsByStart <- repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client, service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil)
		}(operation)
	}
	close(start)
	wait.Wait()
	close(errorsByStart)
	succeeded := 0
	for err := range errorsByStart {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent admissions accepted %d operations, want exactly one", succeeded)
	}
	var operationCount, jobCount int
	if err := database.NewRaw(`SELECT count(*) FROM operation WHERE source_analysis_mode='batch' AND target_source_root_id=? AND state='queued'`, root.ID).Scan(ctx, &operationCount); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, service.SourceAnalysisJobKind).Scan(ctx, &jobCount); err != nil {
		t.Fatal(err)
	}
	if operationCount != 1 || jobCount != 1 {
		t.Fatalf("concurrent admission persisted operations=%d jobs=%d, want 1 each", operationCount, jobCount)
	}
	var holds int
	if err := database.NewRaw(`SELECT count(*) FROM operation_source_work_hold WHERE work_id=?`, work.ID).Scan(ctx, &holds); err != nil {
		t.Fatal(err)
	}
	if holds != 1 {
		t.Fatalf("concurrent work holds = %d, want exactly one", holds)
	}
}

func normalizedWork(t *testing.T, ctx context.Context, repository *persistence.SourceInventoryRepository, root *persistence.SourceRoot, location persistence.SourceLocation, shaEnabled bool, steps ...persistence.SourceAnalysisStepInput) *persistence.SourceAnalysisWork {
	t.Helper()
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID,
		ConfiguredPath: root.ConfiguredPath, InventoryPath: *root.InventoryPath,
		RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime,
		SHA256Enabled: shaEnabled, OriginScanOperationID: uuid.New(),
	}
	if err := repository.StoreSourceAnalysisWork(ctx, work, steps); err != nil {
		t.Fatalf("store normalized work: %v", err)
	}
	return work
}

func normalizedOperation(t *testing.T, root *persistence.SourceRoot, location persistence.SourceLocation, work *persistence.SourceAnalysisWork, mode string, targetWorkID *uuid.UUID, targetStep *string, shaEnabled, rerun bool, tools []persistence.SourceAnalysisToolSelection, metadataSelected ...bool) *persistence.Operation {
	t.Helper()
	if tools == nil {
		tools = []persistence.SourceAnalysisToolSelection{}
	}
	cacheOnly := false
	var selectedSteps []persistence.SourceAnalysisStepSelection
	if mode == persistence.SourceAnalysisModeBatch {
		if shaEnabled {
			selectedSteps = append(selectedSteps, persistence.SourceAnalysisStepSelection{WorkID: work.ID, Step: persistence.SourceStepSHA256})
		}
		for _, tool := range tools {
			switch tool.PackageKind {
			case "ffmpeg":
				selectedSteps = append(selectedSteps, persistence.SourceAnalysisStepSelection{WorkID: work.ID, Step: persistence.SourceStepProbe})
			case "fpcalc":
				selectedSteps = append(selectedSteps, persistence.SourceAnalysisStepSelection{WorkID: work.ID, Step: persistence.SourceStepFingerprint})
			}
		}
		if len(metadataSelected) > 0 && metadataSelected[0] {
			selectedSteps = append(selectedSteps, persistence.SourceAnalysisStepSelection{WorkID: work.ID, Step: persistence.SourceStepMetadata})
		}
	}
	snapshot, err := json.Marshal(persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          mode, WorkIDs: []uuid.UUID{work.ID}, TargetWorkID: targetWorkID, TargetStep: targetStep,
		SelectedSteps: selectedSteps,
		SHA256Enabled: &shaEnabled, RerunTarget: &rerun, CacheOnlyReuse: &cacheOnly,
		ToolsReadRequired: len(tools) > 0, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: "analyze_source", State: "queued", Stage: "queued",
		InputSnapshot: snapshot, Attempt: 1, SourceAnalysisMode: mode,
		TargetSourceRootID: &root.ID, ToolsReadRequired: len(tools) > 0, RerunTarget: rerun,
		TargetWorkID: targetWorkID, TargetStep: targetStep, SourceAnalysisTools: tools,
	}
	if mode == persistence.SourceAnalysisModeSingleStep {
		operation.TargetSourceLocationID = &location.ID
	}
	return operation
}

// normalizedAnalysisFixture is the shared PostgreSQL fixture for normalized
// admission tests. Work and the operation snapshot are both persisted contracts;
// callers can choose the exact steps and pinned tools they need to exercise.
type normalizedAnalysisFixture struct {
	database   *bun.DB
	repository *persistence.SourceInventoryRepository
	client     persistence.RiverInserter
	root       *persistence.SourceRoot
	location   persistence.SourceLocation
	work       *persistence.SourceAnalysisWork
}

func newNormalizedWorkOperation(t *testing.T, path string, shaEnabled bool, steps []persistence.SourceAnalysisStepInput) normalizedAnalysisFixture {
	t.Helper()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, repository, path)
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 2048, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, shaEnabled, steps...)
	return normalizedAnalysisFixture{
		database: database, repository: repository, client: openScanEnqueueRiver(t, database),
		root: root, location: location, work: work,
	}
}

func (fixture normalizedAnalysisFixture) batchOperation(t *testing.T, tools ...persistence.SourceAnalysisToolSelection) *persistence.Operation {
	return normalizedOperation(t, fixture.root, fixture.location, fixture.work,
		persistence.SourceAnalysisModeBatch, nil, nil, fixture.work.SHA256Enabled, false, tools)
}

func (fixture normalizedAnalysisFixture) admit(t *testing.T, ctx context.Context, operation *persistence.Operation) error {
	t.Helper()
	return fixture.repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, fixture.client,
		service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil)
}

func normalizedQueuedAnalysis(t *testing.T, ctx context.Context, repository *persistence.SourceInventoryRepository, root *persistence.SourceRoot, location persistence.SourceLocation, tools []persistence.SourceAnalysisToolSelection) *persistence.Operation {
	t.Helper()
	shaEnabled := true
	steps := []persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepSHA256, State: "pending"}}
	if len(tools) > 0 {
		steps = append(steps, persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"})
	}
	work := normalizedWork(t, ctx, repository, root, location, shaEnabled, steps...)
	return normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, shaEnabled, false, tools)
}

func normalizedToolSelection(t *testing.T, ctx context.Context, database *bun.DB, installationID uuid.UUID, executable string) persistence.SourceAnalysisToolSelection {
	t.Helper()
	installation, err := persistence.NewSetupManagerRepository(database).GetInstallation(ctx, installationID)
	if err != nil {
		t.Fatalf("read pinned test installation: %v", err)
	}
	var versions map[string]string
	if err := json.Unmarshal(installation.ExecutableVersions, &versions); err != nil {
		t.Fatalf("decode pinned test installation versions: %v", err)
	}
	if versions == nil {
		versions = make(map[string]string)
	}
	if _, ok := versions[executable]; !ok {
		banner := executable + " version 7.1.2"
		versions[executable] = banner
		encoded, err := json.Marshal(versions)
		if err != nil {
			t.Fatalf("encode verified test installation versions: %v", err)
		}
		if _, err := database.NewRaw(`UPDATE tool_installation SET executable_versions=?::jsonb WHERE id=?`, string(encoded), installationID).Exec(ctx); err != nil {
			t.Fatalf("set verified test executable metadata: %v", err)
		}
		installation.ExecutableVersions = encoded
	}
	banner, ok := versions[executable]
	if !ok {
		t.Fatalf("test installation does not verify %s", executable)
	}
	version := strings.TrimSpace(strings.TrimPrefix(banner, executable+" version"))
	return persistence.SourceAnalysisToolSelection{
		PackageKind: installation.PackageKind, InstallationID: installation.ID,
		RelativePath: installation.RelativePath, Executable: executable,
		Version: version, VersionBanner: banner,
	}
}

func enqueueNormalizedAnalysis(t *testing.T, ctx context.Context, repository *persistence.SourceInventoryRepository, client persistence.RiverInserter, operation *persistence.Operation) error {
	t.Helper()
	return repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, client,
		service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil)
}

func assertOperationHoldCounts(t *testing.T, ctx context.Context, database *bun.DB, operationID uuid.UUID, expectedWork, expectedTools int) {
	t.Helper()
	var work, tools int
	if err := database.NewRaw(`SELECT count(*) FROM operation_source_work_hold WHERE operation_id=?`, operationID).Scan(ctx, &work); err != nil {
		t.Fatal(err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, operationID).Scan(ctx, &tools); err != nil {
		t.Fatal(err)
	}
	if work != expectedWork || tools != expectedTools {
		t.Fatalf("normalized holds = work:%d tools:%d, want work:%d tools:%d", work, tools, expectedWork, expectedTools)
	}
}
