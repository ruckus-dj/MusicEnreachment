package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// The durable contract of a scan operation. The kinds are part of the stored
// rows and of the queue, so they are named here and never spelled out again by
// a caller.
const (
	// SourceScanOperationKind is the operation kind of a scan of a source root.
	SourceScanOperationKind = "scan_source"
	// SourceScanJobKind is the River job kind that runs a scan operation. A scan
	// is delivered under its own kind so its worker is registered next to the
	// traversal it drives.
	SourceScanJobKind = "scan_source_v1"
	// SourceScanSnapshotVersion is the schema version of the snapshot a scan
	// start writes. A worker refuses a snapshot it does not know.
	SourceScanSnapshotVersion = 1
)

// ErrSourceScanDisabled reports a scan start refused because the operator
// disabled the root.
var ErrSourceScanDisabled = errors.New("source root is disabled")

// ErrSourceScanNotReady reports a scan start refused because the first-run
// Setup is unfinished or the instance platform is not usable. The scan reads
// its source through the managed tools Setup verifies, so it may not run
// without them.
var ErrSourceScanNotReady = errors.New("source scan requires a completed setup on a supported instance platform")

// ScanSourceSnapshot is the immutable input of a scan operation: the root the
// scan reads and the configured path that root carried when the scan was
// started. The path lets the worker refuse to publish a scan of a path the
// operator changed while it ran. It carries nothing else: the worker reloads
// every other input from the root itself.
type ScanSourceSnapshot struct {
	SchemaVersion  int       `json:"schema_version"`
	SourceRootID   uuid.UUID `json:"source_root_id"`
	ConfiguredPath string    `json:"configured_path"`
}

// ScanSourceJobArgs carries only the durable operation ID. The worker reloads
// the root, the path and the platform from that operation, so a job never
// carries an input that could contradict the snapshot.
type ScanSourceJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (ScanSourceJobArgs) Kind() string { return SourceScanJobKind }

// SourceScanStartRepository is the persistence contract of a scan start. The
// enqueue creates the operation with its River job in one transaction and
// decides, under the root lock, whether the root may be scanned at all.
type SourceScanStartRepository interface {
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
	CreateSourceScanOperationAndEnqueue(context.Context, *persistence.Operation, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error
	MarkSourceRootUnavailableForRoot(context.Context, persistence.SourceRootUnavailable) error
}

// SourceScanPathValidator re-validates a configured source path at start time.
// *SourceRoots implements it: the path must still be a readable server
// directory that overlaps no managed root, and no other root may carry it.
type SourceScanPathValidator interface {
	ValidateSourcePath(context.Context, string, *uuid.UUID) (string, error)
}

// SourceScanSetup reports whether the first-run Setup is complete.
// *settings.Registry implements it.
type SourceScanSetup interface {
	SetupCompleted(context.Context) (bool, error)
}

// SourceScanOperations starts scans of registered source roots. A start never
// walks the tree and never writes to it: it records the operation and its job,
// and the worker does the traversal.
type SourceScanOperations struct {
	repository SourceScanStartRepository
	paths      SourceScanPathValidator
	setup      SourceScanSetup
	platform   settings.PlatformState
	river      persistence.RiverInserter
}

func NewSourceScanOperations(repository SourceScanStartRepository, paths SourceScanPathValidator, setup SourceScanSetup, platform settings.PlatformState, riverClient persistence.RiverInserter) *SourceScanOperations {
	return &SourceScanOperations{
		repository: repository, paths: paths, setup: setup, platform: platform, river: riverClient,
	}
}

// Start queues one scan of a root and returns its operation. The path is
// re-validated here, and the platform, the Setup completion, the enabled flag
// and the absence of another queued or running scan of the root are all checked
// before the operation exists: a refusal creates neither an operation nor a
// River job. The duplicate start is decided by the database under the root
// lock, so two concurrent starts of one root cannot both succeed.
//
// A refusal reported with ErrSourceRootBusy leaves the root and its previous
// inventory untouched.
func (s *SourceScanOperations) Start(ctx context.Context, rootID uuid.UUID) (*persistence.Operation, error) {
	root, err := s.repository.GetSourceRoot(ctx, rootID)
	if err != nil {
		return nil, fmt.Errorf("scan source root: %w", err)
	}
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	path, err := s.paths.ValidateSourcePath(ctx, root.ConfiguredPath, &root.ID)
	if err != nil {
		// Only a directory the validator proved inaccessible is recorded on the
		// root: a managed-path overlap, a duplicate configured path and a failed
		// database read are refusals that say nothing about the directory.
		if errors.Is(err, ErrSourceRootInaccessible) {
			if markErr := s.recordUnavailableRoot(ctx, root); markErr != nil {
				return nil, fmt.Errorf("scan source root: %w", markErr)
			}
		}
		return nil, fmt.Errorf("scan source root: %w", err)
	}
	snapshot, err := json.Marshal(ScanSourceSnapshot{
		SchemaVersion: SourceScanSnapshotVersion, SourceRootID: root.ID, ConfiguredPath: path,
	})
	if err != nil {
		return nil, fmt.Errorf("scan source root: encode the snapshot: %w", err)
	}
	if s.river == nil {
		return nil, fmt.Errorf("scan source root: River client is required")
	}
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: SourceScanOperationKind, State: "queued", Stage: SourceScanStageQueued,
		InputSnapshot: snapshot, TargetSourceRootID: &root.ID,
	}
	if err := s.repository.CreateSourceScanOperationAndEnqueue(ctx, operation, s.river, ScanSourceJobArgs{OperationID: operation.ID}, nil); err != nil {
		return nil, sourceScanStartRefusal(err)
	}
	return operation, nil
}

// ready refuses a start the instance cannot fulfil: an unusable platform has no
// managed tools to read a source with, and an unfinished Setup has not verified
// them yet.
// recordUnavailableRoot stores the proven inaccessibility of a root whose scan
// start was refused before its operation existed. The repository refuses the
// write when the root moved to another path or a newer success landed since this
// attempt read it, so a rejected start never overwrites a newer result and never
// invents an unavailable state for a path the operator already replaced.
func (s *SourceScanOperations) recordUnavailableRoot(ctx context.Context, root *persistence.SourceRoot) error {
	if err := s.repository.MarkSourceRootUnavailableForRoot(ctx, persistence.SourceRootUnavailable{
		RootID:                 root.ID,
		SafeError:              SourceScanDirectoryUnavailableReason,
		ExpectedConfiguredPath: root.ConfiguredPath,
		ExpectedLastSuccess:    root.LastSuccessfulScanAt,
	}); err != nil {
		return fmt.Errorf("record the unavailable source root: %w", err)
	}
	return nil
}

func (s *SourceScanOperations) ready(ctx context.Context) error {
	if s.platform.Diagnostic {
		return fmt.Errorf("scan source root: the instance platform is not usable (%s): %w", s.platform.Reason, ErrSourceScanNotReady)
	}
	completed, err := s.setup.SetupCompleted(ctx)
	if err != nil {
		return fmt.Errorf("scan source root: read setup completion: %w", err)
	}
	if !completed {
		return fmt.Errorf("scan source root: setup is not complete: %w", ErrSourceScanNotReady)
	}
	return nil
}

// sourceScanStartRefusal turns the root state the transaction decided on into
// the service error its callers switch on. Everything else is returned as the
// wrapped database failure it is.
func sourceScanStartRefusal(err error) error {
	switch {
	case errors.Is(err, persistence.ErrSourceRootDisabled):
		return fmt.Errorf("scan source root: %w", ErrSourceScanDisabled)
	case errors.Is(err, persistence.ErrSourceRootActiveScan):
		return fmt.Errorf("scan source root: %w", ErrSourceRootBusy)
	default:
		return fmt.Errorf("scan source root: %w", err)
	}
}
