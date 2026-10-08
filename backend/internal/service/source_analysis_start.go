package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

const (
	SourceAnalysisOperationKind = "analyze_source"
	SourceAnalysisJobKind       = "analyze_source_v1"
	SourceAnalysisQueue         = "source_analysis"
	SourceAnalysisStageQueued   = "queued"
	SourceAnalysisStageProbing  = "probing"
	SourceAnalysisStageApplying = "applying"
)

var (
	ErrSourceAnalysisNotReady    = errors.New("source analysis requires a completed setup on a supported instance platform")
	ErrSourceAnalysisDisabled    = errors.New("source root is disabled")
	ErrSourceAnalysisBusy        = errors.New("the source root has an active scan or analysis")
	ErrSourceAnalysisToolChanged = errors.New("the selected managed analysis tool changed; retry the request")
	ErrSourceAnalysisMoveActive  = errors.New("a managed tools root move is active")
	ErrSourceAnalysisStale       = persistence.ErrSourceAnalysisStale
	ErrSourceLocationNotFound    = persistence.ErrSourceLocationNotFound
)

// SourceAnalysisJobArgs carries only the durable operation ID. The worker
// reloads the snapshot and every input from that operation.
type SourceAnalysisJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (SourceAnalysisJobArgs) Kind() string { return SourceAnalysisJobKind }

type SourceAnalysisStepRequest struct {
	RootID            uuid.UUID
	LocationID        uuid.UUID
	Step              persistence.SourceStepName
	ExpectedSizeBytes int64
	ExpectedMtime     time.Time
}

type SourceAnalysisRerunRequest struct {
	RootID            uuid.UUID
	LocationID        uuid.UUID
	ExpectedSizeBytes int64
	ExpectedMtime     time.Time
}

// SourceAnalysisStartRepository is the durable boundary for normalized
// single-step admissions. Admission must revalidate state under its transaction;
// service reads here are for useful early refusals, not the concurrency guard.
type SourceAnalysisStartRepository interface {
	ReadSourceLocationDetail(context.Context, uuid.UUID, uuid.UUID) (*persistence.SourceLocationDetailSnapshot, error)
	GetNormalizedSourceAnalysisWork(context.Context, uuid.UUID) (*persistence.SourceAnalysisWork, *persistence.SourceLocation, error)
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
	CreateNormalizedSourceAnalysisOperationAndEnqueue(context.Context, *persistence.Operation, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error
}

type SourceAnalysisSetup interface {
	SetupCompleted(context.Context) (bool, error)
}

type SourceAnalysisRuntimeSettings interface {
	ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error)
}

type SourceAnalysisOperations struct {
	repository SourceAnalysisStartRepository
	setup      SourceAnalysisSetup
	runtime    SourceAnalysisRuntimeSettings
	platform   settings.PlatformState
	river      persistence.RiverInserter
}

// Constructor arity is stable for app composition and existing fixtures.
func NewSourceAnalysisOperations(repository SourceAnalysisStartRepository, setup SourceAnalysisSetup, runtime SourceAnalysisRuntimeSettings, platform settings.PlatformState, riverClient persistence.RiverInserter) *SourceAnalysisOperations {
	return &SourceAnalysisOperations{repository: repository, setup: setup, runtime: runtime, platform: platform, river: riverClient}
}

func (s *SourceAnalysisOperations) RetryStep(ctx context.Context, request SourceAnalysisStepRequest) (*persistence.Operation, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	if request.RootID == uuid.Nil || request.LocationID == uuid.Nil || request.ExpectedSizeBytes < 0 || request.ExpectedMtime.IsZero() {
		return nil, fmt.Errorf("retry source analysis step: expected location identity is required: %w", ErrSourceAnalysisStale)
	}
	if !validAnalysisStep(request.Step) {
		return nil, fmt.Errorf("retry source analysis step: unsupported step %q", request.Step)
	}
	detail, err := s.repository.ReadSourceLocationDetail(ctx, request.RootID, request.LocationID)
	if err != nil {
		return nil, fmt.Errorf("retry source analysis step: %w", err)
	}
	work, err := validateCurrentAnalysisWork(detail, request.ExpectedSizeBytes, request.ExpectedMtime)
	if err != nil {
		return nil, fmt.Errorf("retry source analysis step: %w", err)
	}
	step, ok := findAnalysisStep(detail.Steps, request.Step)
	if !ok || step.State != "failed" {
		return nil, fmt.Errorf("retry source analysis step: only the exact failed step can be retried: %w", ErrSourceAnalysisStale)
	}
	toolsPinned, err := s.selectedTools(ctx, request.Step)
	if err != nil {
		return nil, err
	}
	return s.enqueueSingleStep(ctx, detail, work, request.Step, false, toolsPinned)
}

func (s *SourceAnalysisOperations) RerunFingerprint(ctx context.Context, request SourceAnalysisRerunRequest) (*persistence.Operation, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	if request.RootID == uuid.Nil || request.LocationID == uuid.Nil || request.ExpectedSizeBytes < 0 || request.ExpectedMtime.IsZero() {
		return nil, fmt.Errorf("rerun source fingerprint: expected location identity is required: %w", ErrSourceAnalysisStale)
	}
	detail, err := s.repository.ReadSourceLocationDetail(ctx, request.RootID, request.LocationID)
	if err != nil {
		return nil, fmt.Errorf("rerun source fingerprint: %w", err)
	}
	work, err := validateCurrentAnalysisWork(detail, request.ExpectedSizeBytes, request.ExpectedMtime)
	if err != nil {
		return nil, fmt.Errorf("rerun source fingerprint: %w", err)
	}
	step, ok := findAnalysisStep(detail.Steps, persistence.SourceStepFingerprint)
	if !ok || step.State != "succeeded" {
		return nil, fmt.Errorf("rerun source fingerprint: only a succeeded fingerprint can be rerun: %w", ErrSourceAnalysisStale)
	}
	toolsPinned, err := s.selectedTools(ctx, persistence.SourceStepFingerprint)
	if err != nil {
		return nil, err
	}
	return s.enqueueSingleStep(ctx, detail, work, persistence.SourceStepFingerprint, true, toolsPinned)
}

// RetryOperation creates a fresh single-step retry from the failed operation's
// own normalized snapshot. It never infers a batch target or consults current
// tool settings, and a rerun retry is converted back to an ordinary retry.
func (s *SourceAnalysisOperations) RetryOperation(ctx context.Context, originalID uuid.UUID) (*persistence.Operation, error) {
	original, err := s.repository.GetOperation(ctx, originalID)
	if err != nil {
		return nil, fmt.Errorf("retry source analysis operation: %w", err)
	}
	if original.State != "failed" {
		return nil, fmt.Errorf("retry source analysis operation: operation is not failed: %w", ErrSourceAnalysisStale)
	}
	snapshot, err := persistence.ValidateSourceAnalysisOperationContract(original)
	if err != nil {
		return nil, fmt.Errorf("retry source analysis operation: %w", err)
	}
	if snapshot.Mode != persistence.SourceAnalysisModeSingleStep || snapshot.TargetWorkID == nil {
		return nil, fmt.Errorf("retry source analysis operation: only failed single-step operations can be retried: %w", ErrSourceAnalysisStale)
	}
	work, location, err := s.repository.GetNormalizedSourceAnalysisWork(ctx, *snapshot.TargetWorkID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("retry source analysis operation: normalized work is stale: %w", ErrSourceAnalysisStale)
		}
		return nil, fmt.Errorf("retry source analysis operation: read normalized work: %w", err)
	}
	if work == nil || location == nil || work.ID != *snapshot.TargetWorkID || location.ID != work.LocationID || location.SourceRootID != work.SourceRootID ||
		location.RelativePath != work.RelativePath || location.SizeBytes != work.SizeBytes || !analysisMtime(location.Mtime).Equal(analysisMtime(work.Mtime)) {
		return nil, fmt.Errorf("retry source analysis operation: normalized work identity is stale: %w", ErrSourceAnalysisStale)
	}
	detail, err := s.repository.ReadSourceLocationDetail(ctx, work.SourceRootID, work.LocationID)
	if err != nil {
		if errors.Is(err, ErrSourceLocationNotFound) {
			return nil, fmt.Errorf("retry source analysis operation: current location is stale: %w", ErrSourceAnalysisStale)
		}
		return nil, fmt.Errorf("retry source analysis operation: read current location: %w", err)
	}
	currentWork, err := validateCurrentAnalysisWork(detail, work.SizeBytes, work.Mtime)
	if err != nil || currentWork.ID != work.ID || currentWork.SourceRootID != work.SourceRootID || currentWork.LocationID != work.LocationID {
		return nil, fmt.Errorf("retry source analysis operation: normalized work is no longer current: %w", ErrSourceAnalysisStale)
	}
	step := persistence.SourceStepName(*snapshot.TargetStep)
	tools, err := s.selectedTools(ctx, step)
	if err != nil {
		return nil, err
	}
	return s.enqueueSingleStep(ctx, detail, currentWork, step, false, tools)
}

func (s *SourceAnalysisOperations) enqueueSingleStep(ctx context.Context, detail *persistence.SourceLocationDetailSnapshot, work *persistence.SourceAnalysisWork, step persistence.SourceStepName, rerun bool, pinnedTools []persistence.SourceAnalysisToolSelection) (*persistence.Operation, error) {
	shaEnabled := work.SHA256Enabled
	cacheOnly := false
	stepValue := string(step)
	snapshot := persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          persistence.SourceAnalysisModeSingleStep, WorkIDs: []uuid.UUID{work.ID},
		TargetWorkID: &work.ID, TargetStep: &stepValue, RerunTarget: &rerun,
		SHA256Enabled: &shaEnabled, CacheOnlyReuse: &cacheOnly,
		ToolsReadRequired: len(pinnedTools) != 0, Tools: pinnedTools,
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode source analysis snapshot: %w", err)
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "queued", Stage: SourceAnalysisStageQueued,
		InputSnapshot: raw, Attempt: 1, SourceAnalysisMode: snapshot.Mode,
		TargetSourceRootID: &detail.Root.ID, TargetSourceLocationID: &detail.Location.ID,
		TargetWorkID: &work.ID, TargetStep: &stepValue,
		ToolsReadRequired: snapshot.ToolsReadRequired, RerunTarget: rerun, SourceAnalysisTools: pinnedTools,
	}
	return s.admit(ctx, operation)
}

func (s *SourceAnalysisOperations) enqueueFromSnapshot(ctx context.Context, snapshot persistence.SourceAnalysisOperationSnapshot, step string, rerun bool, rootID, locationID uuid.UUID) (*persistence.Operation, error) {
	snapshot.RerunTarget = &rerun
	if snapshot.TargetWorkID == nil {
		return nil, fmt.Errorf("encode source analysis retry snapshot: exact work is required")
	}
	work, _, err := s.repository.GetNormalizedSourceAnalysisWork(ctx, *snapshot.TargetWorkID)
	if err != nil {
		return nil, fmt.Errorf("read current source analysis work for retry: %w", err)
	}
	if work == nil {
		return nil, fmt.Errorf("read current source analysis work for retry: %w", ErrSourceAnalysisStale)
	}
	shaEnabled, cacheOnly := work.SHA256Enabled, false
	snapshot.SHA256Enabled, snapshot.CacheOnlyReuse = &shaEnabled, &cacheOnly
	if persistence.SourceStepName(step) == persistence.SourceStepProbe || persistence.SourceStepName(step) == persistence.SourceStepFingerprint {
		snapshot.Tools, err = s.selectedTools(ctx, persistence.SourceStepName(step))
		if err != nil {
			return nil, err
		}
		snapshot.ToolsReadRequired = len(snapshot.Tools) != 0
	}
	projected, err := persistence.ProjectSourceAnalysisStepSnapshot(snapshot, *snapshot.TargetWorkID, persistence.SourceStepName(step))
	if err != nil {
		return nil, fmt.Errorf("project source analysis retry snapshot: %w", err)
	}
	raw := projected
	targetStep := step
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "queued", Stage: SourceAnalysisStageQueued,
		InputSnapshot: raw, Attempt: 1, SourceAnalysisMode: snapshot.Mode,
		TargetSourceRootID: &rootID, TargetSourceLocationID: &locationID,
		TargetWorkID: snapshot.TargetWorkID, TargetStep: &targetStep,
		ToolsReadRequired: snapshot.ToolsReadRequired, RerunTarget: rerun, SourceAnalysisTools: snapshot.Tools,
	}
	return s.admit(ctx, operation)
}

func (s *SourceAnalysisOperations) admit(ctx context.Context, operation *persistence.Operation) (*persistence.Operation, error) {
	if s.river == nil {
		return nil, fmt.Errorf("admit source analysis: River client is required: %w", ErrSourceAnalysisToolUnavailable)
	}
	err := s.repository.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, s.river, SourceAnalysisJobArgs{OperationID: operation.ID}, &river.InsertOpts{Queue: SourceAnalysisQueue})
	if err != nil {
		return nil, sourceAnalysisStartRefusal(err)
	}
	return operation, nil
}

func (s *SourceAnalysisOperations) selectedTools(ctx context.Context, step persistence.SourceStepName) ([]persistence.SourceAnalysisToolSelection, error) {
	if step == persistence.SourceStepSHA256 {
		return []persistence.SourceAnalysisToolSelection{}, nil
	}
	runtimeSettings, err := s.runtime.ReadRuntimeSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read active source analysis tools: %w", err)
	}
	packageKind, executable, selectedID := "ffmpeg", "ffprobe", runtimeSettings.ActiveFFmpegInstallation
	if step == persistence.SourceStepFingerprint {
		packageKind, executable, selectedID = "fpcalc", "fpcalc", runtimeSettings.ActiveFPCalcInstallation
	}
	id, err := uuid.Parse(selectedID)
	if err != nil || id == uuid.Nil {
		return nil, fmt.Errorf("select managed source analysis executable: %w", ErrSourceAnalysisToolUnavailable)
	}
	installation, err := s.repository.GetInstallation(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("read selected source analysis installation: %w: %w", ErrSourceAnalysisToolUnavailable, err)
	}
	if installation == nil || installation.ID != id || installation.PackageKind != packageKind || installation.State != "ready" ||
		installation.PlatformGOOS != s.platform.Platform.GOOS || installation.PlatformGOARCH != s.platform.Platform.GOARCH || installation.VerifiedAt == nil {
		return nil, fmt.Errorf("selected source analysis installation is not verified for this platform: %w", ErrSourceAnalysisToolUnavailable)
	}
	var versions map[string]string
	if json.Unmarshal(installation.ExecutableVersions, &versions) != nil {
		return nil, fmt.Errorf("selected source analysis executable has no verified version: %w", ErrSourceAnalysisToolUnavailable)
	}
	banner, ok := tools.VerifiedExecutableVersion(versions, tools.PackageKind(packageKind), executable, installation.PlatformGOOS)
	if !ok {
		return nil, fmt.Errorf("selected source analysis executable has no verified version: %w", ErrSourceAnalysisToolUnavailable)
	}
	version := ""
	if executable == "fpcalc" {
		parsed, parseErr := tools.ParseFPCalcVersion(banner)
		if parseErr != nil {
			return nil, fmt.Errorf("selected fpcalc version is invalid: %w", ErrSourceAnalysisToolUnavailable)
		}
		version, banner = parsed.Version, parsed.Banner
	} else {
		fields := strings.Fields(banner)
		if len(fields) < 3 || fields[0] != "ffprobe" || fields[1] != "version" {
			return nil, fmt.Errorf("selected ffprobe version is invalid: %w", ErrSourceAnalysisToolUnavailable)
		}
		version = fields[2]
	}
	return []persistence.SourceAnalysisToolSelection{{
		PackageKind: packageKind, InstallationID: id,
		RelativePath: installation.RelativePath, Executable: executable,
		Version: version, VersionBanner: banner,
	}}, nil
}

func (s *SourceAnalysisOperations) ready(ctx context.Context) error {
	if s.platform.Diagnostic || !s.platform.Platform.Supported() {
		return fmt.Errorf("source analysis platform is unavailable: %w", ErrSourceAnalysisNotReady)
	}
	completed, err := s.setup.SetupCompleted(ctx)
	if err != nil {
		return fmt.Errorf("read setup completion: %w", err)
	}
	if !completed {
		return fmt.Errorf("source analysis setup is incomplete: %w", ErrSourceAnalysisNotReady)
	}
	return nil
}

func validateCurrentAnalysisWork(detail *persistence.SourceLocationDetailSnapshot, expectedSize int64, expectedMtime time.Time) (*persistence.SourceAnalysisWork, error) {
	if detail == nil || detail.Root == nil || detail.Location == nil || detail.Work == nil || !detail.Root.Enabled || detail.Root.Stale() || detail.Root.InventoryPath == nil {
		return nil, ErrSourceAnalysisStale
	}
	work, root, location := detail.Work, detail.Root, detail.Location
	if location.SizeBytes != expectedSize || !analysisMtime(location.Mtime).Equal(analysisMtime(expectedMtime)) ||
		work.SizeBytes != expectedSize || !analysisMtime(work.Mtime).Equal(analysisMtime(expectedMtime)) ||
		work.LocationID != location.ID || work.SourceRootID != root.ID || work.RelativePath != location.RelativePath ||
		work.ConfiguredPath != root.ConfiguredPath || work.InventoryPath != *root.InventoryPath ||
		location.SourceRootID != root.ID {
		return nil, ErrSourceAnalysisStale
	}
	return work, nil
}

func analysisMtime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func findAnalysisStep(steps []persistence.SourceAnalysisStep, target persistence.SourceStepName) (*persistence.SourceAnalysisStep, bool) {
	for index := range steps {
		if persistence.SourceStepName(steps[index].Step) == target {
			return &steps[index], true
		}
	}
	return nil, false
}

func validAnalysisStep(step persistence.SourceStepName) bool {
	return step == persistence.SourceStepSHA256 || step == persistence.SourceStepProbe || step == persistence.SourceStepFingerprint
}

func sourceAnalysisStartRefusal(err error) error {
	switch {
	case errors.Is(err, persistence.ErrSourceAnalysisInstallationChanged):
		return fmt.Errorf("admit source analysis: %w", ErrSourceAnalysisToolChanged)
	case errors.Is(err, persistence.ErrSourceAnalysisInstallationUnusable):
		return fmt.Errorf("admit source analysis: %w", ErrSourceAnalysisToolUnavailable)
	case errors.Is(err, persistence.ErrSourceAnalysisStale):
		return fmt.Errorf("admit source analysis: %w", ErrSourceAnalysisStale)
	case errors.Is(err, persistence.ErrSourceRootDisabled):
		return fmt.Errorf("admit source analysis: %w", ErrSourceAnalysisDisabled)
	case errors.Is(err, persistence.ErrSourceRootActiveScan), errors.Is(err, persistence.ErrSourceRootActiveAnalysis):
		return fmt.Errorf("admit source analysis: %w", ErrSourceAnalysisBusy)
	case errors.Is(err, persistence.ErrToolsRootMoveActive):
		return fmt.Errorf("admit source analysis: %w", ErrSourceAnalysisMoveActive)
	default:
		return fmt.Errorf("admit source analysis: %w", err)
	}
}
