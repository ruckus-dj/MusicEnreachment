package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// The durable contract of an analysis operation. The kind is part of the stored
// rows and of the queue, so it is named here and never spelled out again by a
// caller.
const (
	// SourceAnalysisOperationKind is the operation kind of a technical analysis
	// of one source location.
	SourceAnalysisOperationKind = "analyze_source"
	// SourceAnalysisJobKind is the River job kind that runs an analysis
	// operation. The worker that serves it is registered with the queue.
	SourceAnalysisJobKind = "analyze_source_v1"
	// SourceAnalysisQueue is the dedicated River queue an analysis job is
	// inserted into. The queue runs one worker: this is a fixed technical limit
	// of the first slice, with no runtime setting and no automatic schedule.
	SourceAnalysisQueue = "source_analysis"
	// SourceAnalysisStageQueued is the stage of an analysis operation that was
	// created but not yet picked up by its worker.
	SourceAnalysisStageQueued = "queued"
	// SourceAnalysisStageProbing is the stage of an analysis reading the pinned
	// source file with the managed ffprobe.
	SourceAnalysisStageProbing = "probing"
	// SourceAnalysisStageApplying is the stage of an analysis committing its
	// prepared result and both of its read holds in one transaction.
	SourceAnalysisStageApplying = "applying"
)

var (
	// ErrSourceAnalysisNotReady reports an analysis start refused because the
	// first-run Setup is unfinished or the instance platform is not usable. The
	// analysis reads its source through the managed tools Setup verifies, so it
	// may not run without them.
	ErrSourceAnalysisNotReady = errors.New("source analysis requires a completed setup on a supported instance platform")
	// ErrSourceAnalysisDisabled reports an analysis start refused because the
	// operator disabled the root.
	ErrSourceAnalysisDisabled = errors.New("source root is disabled")
	// ErrSourceAnalysisBusy reports an analysis start refused because a scan or
	// an analysis of the same root is queued or running. The root exclusivity
	// rule accepts one root-targeting operation at a time.
	ErrSourceAnalysisBusy = errors.New("the source root has an active scan or analysis")
	// ErrSourceAnalysisToolChanged reports an analysis start refused because the
	// active managed FFmpeg installation changed between the operator's read and
	// the transaction that records the snapshot. The start linearizes on the
	// installation active under the activation lock, so the caller retries
	// instead of queuing an analysis against an installation nobody selected.
	ErrSourceAnalysisToolChanged = errors.New("the active managed ffmpeg installation changed; retry the analysis")
	// ErrSourceAnalysisMoveActive reports an analysis start refused because a
	// managed tools root move is queued or running.
	ErrSourceAnalysisMoveActive = errors.New("a managed tools root move is active")
)

// SourceAnalysisJobArgs carries only the durable operation ID. The worker
// reloads the snapshot and every input from that operation.
type SourceAnalysisJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (SourceAnalysisJobArgs) Kind() string { return SourceAnalysisJobKind }

// SourceAnalysisStartRequest names one analysis start: the location the operator
// asked to analyze and the inventory identity they observed when they asked, so
// a start can refuse a file that changed since the client read it.
type SourceAnalysisStartRequest struct {
	RootID            uuid.UUID
	LocationID        uuid.UUID
	ExpectedSizeBytes int64
	ExpectedMtime     time.Time
}

// SourceAnalysisStartRepository is the persistence contract of an analysis
// start. The enqueue creates the operation with its River job and both read
// holds in one transaction and decides, under the root lock, whether the file
// and the root may be analyzed at all.
type SourceAnalysisStartRepository interface {
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
	GetSourceLocation(context.Context, uuid.UUID, uuid.UUID) (*persistence.SourceLocation, error)
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
	CreateSourceAnalysisOperationAndEnqueue(context.Context, *persistence.Operation, string, string, string, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error
}

// SourceAnalysisSetup reports whether the first-run Setup is complete.
// *settings.Registry implements it.
type SourceAnalysisSetup interface {
	SetupCompleted(context.Context) (bool, error)
}

// SourceAnalysisRuntimeSettings reads the active managed tool selection.
// *settings.Registry implements it.
type SourceAnalysisRuntimeSettings interface {
	ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error)
}

// SourceAnalysisOperations starts analyses of source locations. A start never
// reads the file: it records the operation, its immutable snapshot and its job,
// and the worker does the probing.
type SourceAnalysisOperations struct {
	repository SourceAnalysisStartRepository
	setup      SourceAnalysisSetup
	runtime    SourceAnalysisRuntimeSettings
	platform   settings.PlatformState
	river      persistence.RiverInserter
}

func NewSourceAnalysisOperations(repository SourceAnalysisStartRepository, setup SourceAnalysisSetup, runtime SourceAnalysisRuntimeSettings, platform settings.PlatformState, riverClient persistence.RiverInserter) *SourceAnalysisOperations {
	return &SourceAnalysisOperations{
		repository: repository, setup: setup, runtime: runtime, platform: platform, river: riverClient,
	}
}

// Start queues one analysis of a location and returns its operation. The root,
// the location, the requested identity and the active managed FFmpeg
// installation are all checked before the operation exists: a refusal creates
// neither an operation nor a River job. The duplicate start and the active
// tools move are decided by the database under the same lock order the root and
// tool mutations use, so two concurrent starts of one root cannot both succeed
// and an analysis never starts under an active move. Nothing is written to the
// source and no inventory row is touched.
func (s *SourceAnalysisOperations) Start(ctx context.Context, request SourceAnalysisStartRequest) (*persistence.Operation, error) {
	if request.RootID == uuid.Nil || request.LocationID == uuid.Nil {
		return nil, fmt.Errorf("analyze source location: a root and a location are required")
	}
	root, err := s.repository.GetSourceRoot(ctx, request.RootID)
	if err != nil {
		return nil, fmt.Errorf("analyze source location: %w", err)
	}
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	if !root.Enabled {
		return nil, fmt.Errorf("analyze source location: %w", ErrSourceAnalysisDisabled)
	}
	if root.Stale() || root.InventoryPath == nil {
		return nil, fmt.Errorf("analyze source location: the root has no current inventory: %w", persistence.ErrSourceAnalysisStale)
	}
	location, err := s.repository.GetSourceLocation(ctx, root.ID, request.LocationID)
	if err != nil {
		return nil, fmt.Errorf("analyze source location: %w", err)
	}
	if location.ProbeStatus != persistence.SourceProbeStatusAudio ||
		location.SizeBytes != request.ExpectedSizeBytes ||
		!sourceScanMtime(location.Mtime).Equal(sourceScanMtime(request.ExpectedMtime)) {
		return nil, fmt.Errorf("analyze source location: the location changed since it was read: %w", persistence.ErrSourceAnalysisStale)
	}
	installation, err := s.activeInstallation(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := json.Marshal(persistence.SourceAnalysisSnapshot{
		SchemaVersion:          persistence.SourceAnalysisSnapshotVersion,
		SourceRootID:           root.ID,
		SourceLocationID:       location.ID,
		ConfiguredPath:         root.ConfiguredPath,
		InventoryPath:          *root.InventoryPath,
		RelativePath:           location.RelativePath,
		SizeBytes:              location.SizeBytes,
		Mtime:                  location.Mtime,
		PreviousVariantID:      location.MediaVariantID,
		AnalysisPolicyVersion:  persistence.SourceAnalysisPolicyVersion,
		AnalysisInstallationID: installation.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("analyze source location: encode the snapshot: %w", err)
	}
	if s.river == nil {
		return nil, fmt.Errorf("analyze source location: River client is required")
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "queued", Stage: SourceAnalysisStageQueued,
		InputSnapshot: snapshot,
		// target_installation_id stays NULL: the installation is a read hold, not
		// a mutation target.
		TargetSourceRootID:     &root.ID,
		TargetSourceLocationID: &location.ID,
		AnalysisInstallationID: &installation.ID,
		AnalysisMediaVariantID: location.MediaVariantID,
	}
	if err := s.repository.CreateSourceAnalysisOperationAndEnqueue(ctx, operation, s.platform.Platform.GOOS, s.platform.Platform.GOARCH, settings.ActiveFFmpegInstallationKey, s.river, SourceAnalysisJobArgs{OperationID: operation.ID}, &river.InsertOpts{Queue: SourceAnalysisQueue}); err != nil {
		return nil, sourceAnalysisStartRefusal(err)
	}
	return operation, nil
}

// activeInstallation resolves the active managed FFmpeg installation of this
// platform for the snapshot. The enqueue re-reads the active setting under the
// activation lock and refuses a start that raced an activation, so the snapshot
// is pinned to the installation active at that linearization point. A missing,
// non-FFmpeg, non-ready or foreign-platform selection is unavailable, never a
// silent analysis with another executable.
func (s *SourceAnalysisOperations) activeInstallation(ctx context.Context) (*persistence.ToolInstallation, error) {
	runtimeSettings, err := s.runtime.ReadRuntimeSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("analyze source location: read runtime settings: %w", err)
	}
	id, err := uuid.Parse(runtimeSettings.ActiveFFmpegInstallation)
	if err != nil {
		return nil, fmt.Errorf("analyze source location: no active ffmpeg installation: %w", ErrSourceAnalysisToolUnavailable)
	}
	installation, err := s.repository.GetInstallation(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("analyze source location: read the active installation: %w: %w", ErrSourceAnalysisToolUnavailable, err)
	}
	if installation.PackageKind != string(tools.PackageFFmpeg) || installation.State != "ready" ||
		installation.PlatformGOOS != s.platform.Platform.GOOS || installation.PlatformGOARCH != s.platform.Platform.GOARCH {
		return nil, fmt.Errorf("analyze source location: the active installation is not a ready ffmpeg package for this platform: %w", ErrSourceAnalysisToolUnavailable)
	}
	return installation, nil
}

// ready refuses a start the instance cannot fulfil: an unusable platform has no
// managed tools to analyze a source with, and an unfinished Setup has not
// verified them yet.
func (s *SourceAnalysisOperations) ready(ctx context.Context) error {
	if s.platform.Diagnostic {
		return fmt.Errorf("analyze source location: the instance platform is not usable (%s): %w", s.platform.Reason, ErrSourceAnalysisNotReady)
	}
	completed, err := s.setup.SetupCompleted(ctx)
	if err != nil {
		return fmt.Errorf("analyze source location: read setup completion: %w", err)
	}
	if !completed {
		return fmt.Errorf("analyze source location: setup is not complete: %w", ErrSourceAnalysisNotReady)
	}
	return nil
}

// sourceAnalysisStartRefusal turns the state the transaction decided on into the
// service error its callers switch on. Everything else is returned as the
// wrapped database failure it is.
func sourceAnalysisStartRefusal(err error) error {
	switch {
	case errors.Is(err, persistence.ErrSourceAnalysisInstallationChanged):
		return fmt.Errorf("analyze source location: %w", ErrSourceAnalysisToolChanged)
	case errors.Is(err, persistence.ErrSourceAnalysisInstallationUnusable):
		return fmt.Errorf("analyze source location: %w", ErrSourceAnalysisToolUnavailable)
	case errors.Is(err, persistence.ErrSourceAnalysisStale):
		return fmt.Errorf("analyze source location: %w", persistence.ErrSourceAnalysisStale)
	case errors.Is(err, persistence.ErrSourceRootDisabled):
		return fmt.Errorf("analyze source location: %w", ErrSourceAnalysisDisabled)
	case errors.Is(err, persistence.ErrSourceRootActiveScan), errors.Is(err, persistence.ErrSourceRootActiveAnalysis):
		return fmt.Errorf("analyze source location: %w", ErrSourceAnalysisBusy)
	case errors.Is(err, persistence.ErrToolsRootMoveActive):
		return fmt.Errorf("analyze source location: %w", ErrSourceAnalysisMoveActive)
	default:
		return fmt.Errorf("analyze source location: %w", err)
	}
}
