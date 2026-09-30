package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// The coarse stages a scan operation passes through. An operator sees the stage
// name alone: the number of files is not known before the tree is read, so a
// scan never reports a byte count or an invented percentage.
const (
	// SourceScanStageQueued is the stage of a scan operation that was created but
	// whose worker has not started.
	SourceScanStageQueued = "queued"
	// SourceScanStageTraversing is the stage of a scan reading the tree and
	// confirming the audio stream of the files that need it.
	SourceScanStageTraversing = "traversing"
	// SourceScanStageApplying is the stage of a scan finalizing its verified
	// snapshot: the last candidates are stored and every traversed file is
	// confirmed to be the file that was read before the snapshot is complete.
	SourceScanStageApplying = "applying"
)

const (
	// sourceScanCandidateBatchSize bounds how many candidates one repository
	// batch carries, so a large tree is never stored in one insert.
	sourceScanCandidateBatchSize = 500
	// sourceScanInventoryPageSize bounds one page of the previous inventory the
	// traversal compares against.
	sourceScanInventoryPageSize = 500
)

// sourceScanProbeErrorText is the safe reason stored for a file whose audio
// stream ffprobe could not confirm. It carries no path, no ffprobe output and no
// internal detail, and it is what an operator reads on such a file.
const sourceScanProbeErrorText = "ffprobe could not confirm an audio stream in this file. The next scan will check it again."

// SourceScanRepository is the persistence contract of a source scan. A scan only
// ever stores candidates of its own operation: it writes no location, so the
// previous inventory stays untouched until a later step applies the candidates
// in one transaction.
type SourceScanRepository interface {
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
	ListSourceLocationsPage(context.Context, uuid.UUID, *persistence.SourceLocationCursor, int) ([]persistence.SourceLocation, *persistence.SourceLocationCursor, error)
	DeleteSourceScanCandidates(context.Context, uuid.UUID) error
	AppendSourceScanCandidates(context.Context, uuid.UUID, []persistence.SourceScanCandidateInput) error
}

// SourceProbe confirms whether one absolute server path carries an audio stream.
// A managed ffprobe is the production implementation: it reports a valid answer
// without an audio stream as (false, nil) and every other outcome as an error.
type SourceProbe interface {
	Probe(ctx context.Context, absoluteServerPath string) (bool, error)
}

// SourceScanStages records the coarse stage a running scan reached.
// *Operations satisfies it with Operations.Running.
type SourceScanStages interface {
	Running(ctx context.Context, operationID uuid.UUID, stage string) error
}

// SourceScanRequest names one scan: the durable operation that owns the stored
// candidates, and the root the scan reads.
type SourceScanRequest struct {
	OperationID uuid.UUID
	RootID      uuid.UUID
}

// SourceScan reads a registered source root and turns the files it finds into
// candidates of one scan operation. The source files belong to the operator:
// the traversal only reads them.
type SourceScan struct {
	repository SourceScanRepository
	probe      SourceProbe
	stages     SourceScanStages
}

func NewSourceScan(repository SourceScanRepository, probe SourceProbe, stages SourceScanStages) *SourceScan {
	return &SourceScan{repository: repository, probe: probe, stages: stages}
}

// Run walks the configured path of the root and stores one candidate per
// approved file under request.OperationID. A file that kept its size and mtime
// and whose stored status is already audio or no_audio is carried over without a
// probe; a new file, a changed file and a file whose last probe failed are probed
// again. A failed probe becomes probe_error with a safe reason and the scan goes
// on with the next file, while a read error, a cancellation or a file that
// changed under the traversal fails the whole scan: the candidates of the failed
// attempt are dropped, so no partial snapshot survives it and the previous
// inventory is never touched.
func (s *SourceScan) Run(ctx context.Context, request SourceScanRequest) error {
	root, err := s.repository.GetSourceRoot(ctx, request.RootID)
	if err != nil {
		return fmt.Errorf("scan source root: %w", err)
	}
	if !root.Enabled {
		return fmt.Errorf("scan source root: the root %q is disabled", root.DisplayName)
	}
	previous, err := s.previousInventory(ctx, root)
	if err != nil {
		return err
	}
	// A retry re-runs the whole traversal, and (operation_id, relative_path) is
	// unique, so the candidates an earlier attempt left behind go first.
	if err := s.repository.DeleteSourceScanCandidates(ctx, request.OperationID); err != nil {
		return fmt.Errorf("scan source root: drop the candidates of an earlier attempt: %w", err)
	}
	if err := s.reportStage(ctx, request.OperationID, SourceScanStageTraversing); err != nil {
		return err
	}
	var traversed []SourceWalkEntry
	var batch []persistence.SourceScanCandidateInput
	walkErr := WalkSourceTree(ctx, root.ConfiguredPath, func(entry SourceWalkEntry) error {
		candidate, err := s.candidateFor(ctx, previous, entry)
		if err != nil {
			return err
		}
		traversed = append(traversed, entry)
		batch = append(batch, candidate)
		if len(batch) < sourceScanCandidateBatchSize {
			return nil
		}
		if err := s.appendCandidates(ctx, request.OperationID, batch); err != nil {
			return err
		}
		batch = nil
		return nil
	})
	if walkErr != nil {
		return s.abandonFailedScan(ctx, request.OperationID,
			fmt.Errorf("scan source root: traverse %q: %w", root.ConfiguredPath, walkErr))
	}
	if err := s.reportStage(ctx, request.OperationID, SourceScanStageApplying); err != nil {
		// The traversal may already have stored full batches while it walked, so
		// a scan that cannot report its snapshot complete must drop them instead
		// of leaving them for a later step to apply.
		return s.abandonFailedScan(ctx, request.OperationID, err)
	}
	if len(batch) > 0 {
		if err := s.appendCandidates(ctx, request.OperationID, batch); err != nil {
			return s.abandonFailedScan(ctx, request.OperationID, err)
		}
	}
	for _, entry := range traversed {
		if err := sourceScanConfirmFile(entry); err != nil {
			return s.abandonFailedScan(ctx, request.OperationID, err)
		}
	}
	return nil
}

// candidateFor turns one walked file into a candidate. It reuses the stored
// status of a file that is unchanged since the last successful scan and probes
// every other file: only a successful probe decides between audio and no_audio,
// and a failed one becomes probe_error rather than a status the file never
// earned.
func (s *SourceScan) candidateFor(ctx context.Context, previous map[string]persistence.SourceLocation, entry SourceWalkEntry) (persistence.SourceScanCandidateInput, error) {
	candidate := persistence.SourceScanCandidateInput{
		RelativePath: entry.RelativePath, SizeBytes: entry.SizeBytes, Mtime: sourceScanMtime(entry.Mtime),
	}
	if stored, known := previous[entry.RelativePath]; known && sourceScanReusesStatus(stored, candidate) {
		candidate.ProbeStatus = stored.ProbeStatus
		return candidate, nil
	}
	// The walk's own stat is the metadata before the probe; a file that moved
	// between that reading and the probe is a changing file, not a snapshot.
	if err := sourceScanConfirmFile(entry); err != nil {
		return candidate, err
	}
	hasAudio, err := s.probe.Probe(ctx, entry.AbsolutePath)
	if err != nil {
		// A probe that died because the scan itself was canceled is not a file
		// problem: the whole scan fails instead of blaming one file.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return candidate, fmt.Errorf("confirm the audio stream of %q: %w", entry.RelativePath, ctxErr)
		}
		// A failing probe is no excuse for an unread file: the metadata is read
		// again after it, so a file that changed or vanished while its probe ran
		// fails the scan instead of being remembered as a probe_error.
		if confirmErr := sourceScanConfirmFile(entry); confirmErr != nil {
			return candidate, confirmErr
		}
		safe := sourceScanProbeErrorText
		candidate.ProbeStatus = persistence.SourceProbeStatusProbeError
		candidate.SafeError = &safe
		return candidate, nil
	}
	if err := sourceScanConfirmFile(entry); err != nil {
		return candidate, err
	}
	candidate.ProbeStatus = persistence.SourceProbeStatusNoAudio
	if hasAudio {
		candidate.ProbeStatus = persistence.SourceProbeStatusAudio
	}
	return candidate, nil
}

// previousInventory reads the last applied generation of the root, keyed by
// exact relative path. A root whose configured path changed since its last
// success is treated as never scanned: its locations describe the old path, so
// reusing a stored status would attribute one file's result to another file.
func (s *SourceScan) previousInventory(ctx context.Context, root *persistence.SourceRoot) (map[string]persistence.SourceLocation, error) {
	locations := map[string]persistence.SourceLocation{}
	if root.Stale() {
		return locations, nil
	}
	var cursor *persistence.SourceLocationCursor
	for {
		page, next, err := s.repository.ListSourceLocationsPage(ctx, root.ID, cursor, sourceScanInventoryPageSize)
		if err != nil {
			return nil, fmt.Errorf("scan source root: read the previous inventory: %w", err)
		}
		for _, location := range page {
			locations[location.RelativePath] = location
		}
		if next == nil || len(page) == 0 {
			return locations, nil
		}
		cursor = next
	}
}

func (s *SourceScan) reportStage(ctx context.Context, operationID uuid.UUID, stage string) error {
	if err := s.stages.Running(ctx, operationID, stage); err != nil {
		return fmt.Errorf("scan source root: report the %s stage: %w", stage, err)
	}
	return nil
}

func (s *SourceScan) appendCandidates(ctx context.Context, operationID uuid.UUID, batch []persistence.SourceScanCandidateInput) error {
	if err := s.repository.AppendSourceScanCandidates(ctx, operationID, batch); err != nil {
		return fmt.Errorf("scan source root: store a candidate batch: %w", err)
	}
	return nil
}

// abandonFailedScan drops the candidates of an attempt that failed, so a run
// that did not read the whole tree leaves nothing another step could apply. The
// cleanup ignores the cancellation that failed the scan; both errors are
// reported, never one instead of the other.
func (s *SourceScan) abandonFailedScan(ctx context.Context, operationID uuid.UUID, scanErr error) error {
	if err := s.repository.DeleteSourceScanCandidates(context.WithoutCancel(ctx), operationID); err != nil {
		return errors.Join(scanErr, fmt.Errorf("scan source root: drop the candidates of the failed scan: %w", err))
	}
	return scanErr
}

// sourceScanReusesStatus reports whether the stored location of a path still
// describes the walked file and already carries a decided status. A stored
// probe_error is deliberately not reusable: a failure is a problem to check
// again, not a result to confirm.
func sourceScanReusesStatus(stored persistence.SourceLocation, candidate persistence.SourceScanCandidateInput) bool {
	return stored.ProbeStatus != persistence.SourceProbeStatusProbeError &&
		stored.SizeBytes == candidate.SizeBytes && stored.Mtime.Equal(candidate.Mtime)
}

// sourceScanConfirmFile re-reads the metadata of a traversed file and fails when
// the file is no longer the regular file the traversal recorded. The read never
// follows a symlink, so a file swapped for a link cannot pass as itself, and the
// mtime is compared at the resolution the inventory keeps.
func sourceScanConfirmFile(entry SourceWalkEntry) error {
	info, err := os.Lstat(entry.AbsolutePath)
	if err != nil {
		return fmt.Errorf("confirm source file %q: %w", entry.RelativePath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("confirm source file %q: the file is no longer a regular file", entry.RelativePath)
	}
	if info.Size() != entry.SizeBytes || !sourceScanMtime(info.ModTime()).Equal(sourceScanMtime(entry.Mtime)) {
		return fmt.Errorf("confirm source file %q: the file changed while it was being scanned", entry.RelativePath)
	}
	return nil
}

// sourceScanMtime is the mtime as the inventory stores it. PostgreSQL keeps
// microseconds, so comparing a nanosecond filesystem reading with a stored value
// directly would report every unchanged file as changed and probe it again on
// every scan.
func sourceScanMtime(value time.Time) time.Time {
	return value.Truncate(time.Microsecond)
}
