package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type sourceAnalysisStartRepositoryFixture struct {
	detail             *persistence.SourceLocationDetailSnapshot
	normalizedWork     *persistence.SourceAnalysisWork
	normalizedLocation *persistence.SourceLocation
	normalizedWorkErr  error
	operations         map[uuid.UUID]*persistence.Operation
	installation       *persistence.ToolInstallation
	created            []*persistence.Operation
	createErr          error
}

func (repository *sourceAnalysisStartRepositoryFixture) GetNormalizedSourceAnalysisWork(context.Context, uuid.UUID) (*persistence.SourceAnalysisWork, *persistence.SourceLocation, error) {
	if repository.normalizedWorkErr != nil {
		return nil, nil, repository.normalizedWorkErr
	}
	return repository.normalizedWork, repository.normalizedLocation, nil
}

func (repository *sourceAnalysisStartRepositoryFixture) ReadSourceLocationDetail(context.Context, uuid.UUID, uuid.UUID) (*persistence.SourceLocationDetailSnapshot, error) {
	if repository.detail == nil {
		return nil, persistence.ErrSourceLocationNotFound
	}
	return repository.detail, nil
}
func (repository *sourceAnalysisStartRepositoryFixture) GetOperation(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
	if operation := repository.operations[id]; operation != nil {
		return operation, nil
	}
	return nil, errors.New("missing operation")
}
func (repository *sourceAnalysisStartRepositoryFixture) GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error) {
	return repository.installation, nil
}
func (repository *sourceAnalysisStartRepositoryFixture) CreateNormalizedSourceAnalysisOperationAndEnqueue(_ context.Context, operation *persistence.Operation, _ persistence.RiverInserter, _ river.JobArgs, _ *river.InsertOpts) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = append(repository.created, operation)
	return nil
}

type sourceAnalysisStartSetupFixture bool

func (setup sourceAnalysisStartSetupFixture) SetupCompleted(context.Context) (bool, error) {
	return bool(setup), nil
}

type sourceAnalysisStartRuntimeFixture settings.RuntimeSettings

func (runtime sourceAnalysisStartRuntimeFixture) ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error) {
	return settings.RuntimeSettings(runtime), nil
}

type sourceAnalysisStartRiverFixture struct{}

func (sourceAnalysisStartRiverFixture) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, nil
}

func TestRetryStepUsesInheritedSHASelectionAndExactFailedStep(t *testing.T) {
	rootID, locationID, workID := uuid.New(), uuid.New(), uuid.New()
	mtime := time.Date(2026, 9, 2, 8, 30, 0, 0, time.UTC)
	root := &persistence.SourceRoot{ID: rootID, Enabled: true, ConfiguredPath: "/music", InventoryPath: new("/music"), Status: persistence.SourceRootStatusAvailable}
	location := &persistence.SourceLocation{ID: locationID, SourceRootID: rootID, RelativePath: "track.flac", SizeBytes: 123, Mtime: mtime}
	work := &persistence.SourceAnalysisWork{ID: workID, SourceRootID: rootID, LocationID: locationID, ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "track.flac", SizeBytes: 123, Mtime: mtime, SHA256Enabled: true}
	repository := &sourceAnalysisStartRepositoryFixture{detail: &persistence.SourceLocationDetailSnapshot{
		Root: root, Location: location, Work: work,
		Steps: []persistence.SourceAnalysisStep{{WorkID: workID, Step: "sha256", State: "failed"}},
	}, operations: map[uuid.UUID]*persistence.Operation{}}
	service := NewSourceAnalysisOperations(repository, sourceAnalysisStartSetupFixture(true), sourceAnalysisStartRuntimeFixture{}, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, sourceAnalysisStartRiverFixture{})
	operation, err := service.RetryStep(context.Background(), SourceAnalysisStepRequest{
		RootID: rootID, LocationID: locationID, Step: persistence.SourceStepSHA256, ExpectedSizeBytes: 123, ExpectedMtime: mtime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.SourceAnalysisMode != persistence.SourceAnalysisModeSingleStep || operation.TargetStep == nil || *operation.TargetStep != "sha256" || len(repository.created) != 1 {
		t.Fatalf("operation=%+v, admitted=%d", operation, len(repository.created))
	}
	var snapshot persistence.SourceAnalysisOperationSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.SHA256Enabled == nil || !*snapshot.SHA256Enabled {
		t.Fatalf("snapshot=%+v, decode error=%v", snapshot, err)
	}
}

func TestRetryStepRejectsSucceededAndMismatchedWork(t *testing.T) {
	rootID, locationID, workID := uuid.New(), uuid.New(), uuid.New()
	mtime := time.Date(2026, 9, 2, 8, 30, 0, 0, time.UTC)
	root := &persistence.SourceRoot{ID: rootID, Enabled: true, ConfiguredPath: "/music", InventoryPath: new("/music"), Status: persistence.SourceRootStatusAvailable}
	location := &persistence.SourceLocation{ID: locationID, SourceRootID: rootID, RelativePath: "track.flac", SizeBytes: 123, Mtime: mtime}
	work := &persistence.SourceAnalysisWork{ID: workID, SourceRootID: rootID, LocationID: locationID, ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "track.flac", SizeBytes: 123, Mtime: mtime}
	repository := &sourceAnalysisStartRepositoryFixture{detail: &persistence.SourceLocationDetailSnapshot{Root: root, Location: location, Work: work,
		Steps: []persistence.SourceAnalysisStep{{WorkID: workID, Step: "sha256", State: "succeeded"}},
	}, operations: map[uuid.UUID]*persistence.Operation{}}
	service := NewSourceAnalysisOperations(repository, sourceAnalysisStartSetupFixture(true), sourceAnalysisStartRuntimeFixture{}, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, sourceAnalysisStartRiverFixture{})
	_, err := service.RetryStep(context.Background(), SourceAnalysisStepRequest{RootID: rootID, LocationID: locationID, Step: persistence.SourceStepSHA256, ExpectedSizeBytes: 123, ExpectedMtime: mtime})
	if !errors.Is(err, ErrSourceAnalysisStale) || len(repository.created) != 0 {
		t.Fatalf("retry err=%v, admitted=%d", err, len(repository.created))
	}
	_, err = service.RetryStep(context.Background(), SourceAnalysisStepRequest{RootID: rootID, LocationID: locationID, Step: persistence.SourceStepSHA256, ExpectedSizeBytes: 124, ExpectedMtime: mtime})
	if !errors.Is(err, ErrSourceAnalysisStale) {
		t.Fatalf("mismatched stat error=%v, want stale", err)
	}
}

func TestToolStepsPinOnlyTheirSelectedManagedExecutable(t *testing.T) {
	for _, test := range []struct {
		step, packageKind, executable, banner, version string
		runtimeKey                                     func(uuid.UUID) settings.RuntimeSettings
	}{
		{step: "probe", packageKind: "ffmpeg", executable: "ffprobe", banner: "ffprobe version 8.0 Copyright", version: "8.0", runtimeKey: func(id uuid.UUID) settings.RuntimeSettings {
			return settings.RuntimeSettings{ActiveFFmpegInstallation: id.String(), ActiveFPCalcInstallation: uuid.NewString()}
		}},
		{step: "fingerprint", packageKind: "fpcalc", executable: "fpcalc", banner: "fpcalc version 1.6.1 (FFmpeg Lavc62.11.100)", version: "1.6.1", runtimeKey: func(id uuid.UUID) settings.RuntimeSettings {
			return settings.RuntimeSettings{ActiveFFmpegInstallation: uuid.NewString(), ActiveFPCalcInstallation: id.String()}
		}},
	} {
		t.Run(test.step, func(t *testing.T) {
			rootID, locationID, workID, installationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			mtime := time.Date(2026, 9, 2, 8, 30, 0, 0, time.UTC)
			stepName := persistence.SourceStepName(test.step)
			root := &persistence.SourceRoot{ID: rootID, Enabled: true, ConfiguredPath: "/music", InventoryPath: new("/music"), Status: persistence.SourceRootStatusAvailable}
			location := &persistence.SourceLocation{ID: locationID, SourceRootID: rootID, RelativePath: "track.flac", SizeBytes: 123, Mtime: mtime}
			work := &persistence.SourceAnalysisWork{ID: workID, SourceRootID: rootID, LocationID: locationID, ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "track.flac", SizeBytes: 123, Mtime: mtime, SHA256Enabled: true}
			installation := &persistence.ToolInstallation{
				ID: installationID, PackageKind: test.packageKind, State: "ready", RelativePath: "managed/package-root",
				PlatformGOOS: "linux", PlatformGOARCH: "amd64", VerifiedAt: new(time.Now()),
				ExecutableVersions: mustJSON(t, map[string]string{test.executable: test.banner}),
			}
			repository := &sourceAnalysisStartRepositoryFixture{
				detail: &persistence.SourceLocationDetailSnapshot{Root: root, Location: location, Work: work,
					Steps: []persistence.SourceAnalysisStep{{WorkID: workID, Step: "probe", State: "failed"}, {WorkID: workID, Step: "fingerprint", State: "failed"}},
				}, operations: map[uuid.UUID]*persistence.Operation{}, installation: installation,
			}
			runtime := sourceAnalysisStartRuntimeFixture(test.runtimeKey(installationID))
			analysis := NewSourceAnalysisOperations(repository, sourceAnalysisStartSetupFixture(true), runtime, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, sourceAnalysisStartRiverFixture{})
			operation, err := analysis.RetryStep(context.Background(), SourceAnalysisStepRequest{RootID: rootID, LocationID: locationID, Step: stepName, ExpectedSizeBytes: 123, ExpectedMtime: mtime})
			if err != nil {
				t.Fatal(err)
			}
			var snapshot persistence.SourceAnalysisOperationSnapshot
			if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Tools) != 1 || snapshot.Tools[0].PackageKind != test.packageKind || snapshot.Tools[0].Executable != test.executable ||
				snapshot.Tools[0].InstallationID != installationID || snapshot.Tools[0].RelativePath != installation.RelativePath ||
				snapshot.Tools[0].Version != test.version || snapshot.Tools[0].VersionBanner != test.banner {
				t.Fatalf("pinned tool = %+v", snapshot.Tools)
			}
			if len(snapshot.WorkIDs) != 1 || snapshot.WorkIDs[0] != workID || operation.TargetWorkID == nil || *operation.TargetWorkID != workID {
				t.Fatalf("single-step target = %+v / %v", snapshot.WorkIDs, operation.TargetWorkID)
			}
		})
	}
}

func TestFingerprintRetryAndRerunDoNotDependOnProbeOrMatchingEligibility(t *testing.T) {
	for _, test := range []struct {
		name, priorFingerprintState string
		rerun                       bool
	}{
		{name: "retry after probe failure", priorFingerprintState: "failed"},
		{name: "first fingerprint failure", priorFingerprintState: "failed"},
		{name: "rerun stored result despite probe failure", priorFingerprintState: "succeeded", rerun: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootID, locationID, workID, installationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			mtime := time.Date(2026, 9, 2, 8, 30, 0, 0, time.UTC)
			root := &persistence.SourceRoot{ID: rootID, Enabled: true, ConfiguredPath: "/music", InventoryPath: new("/music"), Status: persistence.SourceRootStatusAvailable}
			location := &persistence.SourceLocation{ID: locationID, SourceRootID: rootID, RelativePath: "track.flac", SizeBytes: 123, Mtime: mtime, ProbeStatus: persistence.SourceProbeStatusNoAudio}
			work := &persistence.SourceAnalysisWork{ID: workID, SourceRootID: rootID, LocationID: locationID, ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "track.flac", SizeBytes: 123, Mtime: mtime}
			installation := &persistence.ToolInstallation{ID: installationID, PackageKind: "fpcalc", State: "ready", RelativePath: "fpcalc/1.6.1", PlatformGOOS: "linux", PlatformGOARCH: "amd64", VerifiedAt: new(time.Now()), ExecutableVersions: []byte(`{"fpcalc":"fpcalc version 1.6.1"}`)}
			repository := &sourceAnalysisStartRepositoryFixture{detail: &persistence.SourceLocationDetailSnapshot{
				Root: root, Location: location, Work: work, MatchingEligible: false,
				Steps: []persistence.SourceAnalysisStep{{WorkID: workID, Step: "probe", State: "failed"}, {WorkID: workID, Step: "fingerprint", State: test.priorFingerprintState}},
			}, operations: map[uuid.UUID]*persistence.Operation{}, installation: installation}
			runtime := sourceAnalysisStartRuntimeFixture(settings.RuntimeSettings{ActiveFFmpegInstallation: uuid.NewString(), ActiveFPCalcInstallation: installationID.String()})
			analysis := NewSourceAnalysisOperations(repository, sourceAnalysisStartSetupFixture(true), runtime, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, sourceAnalysisStartRiverFixture{})
			var operation *persistence.Operation
			var err error
			if test.rerun {
				operation, err = analysis.RerunFingerprint(context.Background(), SourceAnalysisRerunRequest{RootID: rootID, LocationID: locationID, ExpectedSizeBytes: 123, ExpectedMtime: mtime})
			} else {
				operation, err = analysis.RetryStep(context.Background(), SourceAnalysisStepRequest{RootID: rootID, LocationID: locationID, Step: persistence.SourceStepFingerprint, ExpectedSizeBytes: 123, ExpectedMtime: mtime})
			}
			if err != nil {
				t.Fatalf("fingerprint action unexpectedly gated by probe/matching: %v", err)
			}
			var snapshot persistence.SourceAnalysisOperationSnapshot
			if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.ToolsReadRequired != true || len(snapshot.Tools) != 1 || snapshot.Tools[0].Executable != "fpcalc" || snapshot.Tools[0].PackageKind != "fpcalc" || snapshot.RerunTarget == nil || *snapshot.RerunTarget != test.rerun {
				t.Fatalf("fingerprint operation snapshot = %+v", snapshot)
			}
		})
	}
}

func TestRetryOperationReusesFailedSnapshotWithoutCurrentToolSettings(t *testing.T) {
	rootID, locationID, workID, oldToolID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	step := string(persistence.SourceStepFingerprint)
	rerun, shaEnabled, cacheOnly := false, true, false
	tool := persistence.SourceAnalysisToolSelection{PackageKind: "fpcalc", InstallationID: oldToolID, RelativePath: "fpcalc/old", Executable: "fpcalc", Version: "1.5.1", VersionBanner: "fpcalc version 1.5.1"}
	probeTool := persistence.SourceAnalysisToolSelection{PackageKind: "ffmpeg", InstallationID: uuid.New(), RelativePath: "ffmpeg/old", Executable: "ffprobe", Version: "7.1", VersionBanner: "ffprobe version 7.1"}
	raw, err := json.Marshal(persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion, Mode: persistence.SourceAnalysisModeSingleStep,
		WorkIDs: []uuid.UUID{workID}, TargetWorkID: &workID, TargetStep: &step,
		RerunTarget: &rerun, SHA256Enabled: &shaEnabled, CacheOnlyReuse: &cacheOnly,
		ToolsReadRequired: true, Tools: []persistence.SourceAnalysisToolSelection{probeTool, tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	original := &persistence.Operation{
		ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "failed", SourceAnalysisMode: persistence.SourceAnalysisModeSingleStep,
		TargetWorkID: nil, TargetStep: nil,
		ToolsReadRequired: false, RerunTarget: false, InputSnapshot: raw,
	}
	mtime := time.Date(2026, 9, 2, 8, 30, 0, 0, time.UTC)
	root := &persistence.SourceRoot{ID: rootID, Enabled: true, ConfiguredPath: "/music", InventoryPath: new("/music"), Status: persistence.SourceRootStatusAvailable}
	location := &persistence.SourceLocation{ID: locationID, SourceRootID: rootID, RelativePath: "track.flac", SizeBytes: 123, Mtime: mtime}
	work := &persistence.SourceAnalysisWork{ID: workID, SourceRootID: rootID, LocationID: locationID, ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "track.flac", SizeBytes: 123, Mtime: mtime, SHA256Enabled: true}
	repository := &sourceAnalysisStartRepositoryFixture{operations: map[uuid.UUID]*persistence.Operation{original.ID: original},
		normalizedWork: work, normalizedLocation: location,
		detail: &persistence.SourceLocationDetailSnapshot{Root: root, Location: location, Work: work},
	}
	changedCurrent := sourceAnalysisStartRuntimeFixture(settings.RuntimeSettings{ActiveFPCalcInstallation: uuid.NewString(), ActiveFFmpegInstallation: uuid.NewString()})
	analysis := NewSourceAnalysisOperations(repository, sourceAnalysisStartSetupFixture(true), changedCurrent, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, sourceAnalysisStartRiverFixture{})
	operation, err := analysis.RetryOperation(context.Background(), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot persistence.SourceAnalysisOperationSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SHA256Enabled == nil || !*snapshot.SHA256Enabled || snapshot.TargetStep == nil || *snapshot.TargetStep != step ||
		snapshot.RerunTarget == nil || *snapshot.RerunTarget || len(snapshot.Tools) != 1 || snapshot.Tools[0] != tool ||
		snapshot.CacheOnlyReuse == nil || *snapshot.CacheOnlyReuse || snapshot.CacheOnlyFPCalcVersion != "" ||
		len(snapshot.WorkIDs) != 1 || snapshot.WorkIDs[0] != workID ||
		operation.TargetWorkID == nil || *operation.TargetWorkID != workID || !operation.ToolsReadRequired || operation.ToolsReadRequired != snapshot.ToolsReadRequired ||
		operation.TargetSourceRootID == nil || *operation.TargetSourceRootID != rootID || operation.TargetSourceLocationID == nil || *operation.TargetSourceLocationID != locationID ||
		original.TargetSourceRootID != nil || original.TargetSourceLocationID != nil || original.TargetWorkID != nil || original.TargetStep != nil {
		t.Fatalf("retry operation/snapshot changed pinned inputs: operation=%+v snapshot=%+v", operation, snapshot)
	}
}

func TestRetryOperationTreatsDeletedNormalizedWorkAsStale(t *testing.T) {
	workID := uuid.New()
	step := string(persistence.SourceStepFingerprint)
	rerun, shaEnabled, cacheOnly := false, true, true
	raw, err := json.Marshal(persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion, Mode: persistence.SourceAnalysisModeSingleStep,
		WorkIDs: []uuid.UUID{workID}, TargetWorkID: &workID, TargetStep: &step,
		RerunTarget: &rerun, SHA256Enabled: &shaEnabled, CacheOnlyReuse: &cacheOnly,
		CacheOnlyFPCalcVersion: "1.6.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	original := &persistence.Operation{
		ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "failed", SourceAnalysisMode: persistence.SourceAnalysisModeSingleStep,
		InputSnapshot: raw,
	}
	repository := &sourceAnalysisStartRepositoryFixture{
		operations:        map[uuid.UUID]*persistence.Operation{original.ID: original},
		normalizedWorkErr: fmt.Errorf("get normalized source analysis work: %w", sql.ErrNoRows),
	}
	analysis := NewSourceAnalysisOperations(repository, sourceAnalysisStartSetupFixture(true), sourceAnalysisStartRuntimeFixture{}, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, sourceAnalysisStartRiverFixture{})
	if _, err := analysis.RetryOperation(context.Background(), original.ID); !errors.Is(err, ErrSourceAnalysisStale) {
		t.Fatalf("retry deleted normalized work error=%v, want stale", err)
	}
	if len(repository.created) != 0 {
		t.Fatalf("deleted normalized work enqueued %d operations", len(repository.created))
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
