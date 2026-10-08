package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
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

// SourceScanDirectoryUnavailableReason is the safe UI text recorded on a root
// whose configured directory is proven inaccessible, whether the scan was
// refused before it started or its traversal failed on the root itself. It
// carries no path and no raw diagnostic, and the operation keeps its own
// separate safe error.
const SourceScanDirectoryUnavailableReason = persistence.SourceEnumerationRootUnavailableReason

// SourceScanRepository is the persistence contract of a source scan. A scan only
// ever stores candidates of its own operation: it writes no location, so the
// previous inventory stays untouched until a later step applies the candidates
// in one transaction.
type SourceScanRepository interface {
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
	ListSourceLocationsPage(context.Context, uuid.UUID, *persistence.SourceLocationCursor, int) ([]persistence.SourceLocation, *persistence.SourceLocationCursor, error)
	ReadSourceLocationDetail(context.Context, uuid.UUID, uuid.UUID) (*persistence.SourceLocationDetailSnapshot, error)
	DeleteSourceScanCandidatesForDelivery(context.Context, uuid.UUID, int, int64) error
	DeleteSourceScanCandidatesForRootDelivery(context.Context, uuid.UUID, uuid.UUID, string, int, int64) error
	AppendSourceScanCandidatesForDelivery(context.Context, uuid.UUID, int, int64, []persistence.SourceScanCandidateInput) error
}

// SourceProbe exposes the pre-traversal capability check a source scan performs.
// A managed ffprobe is the production implementation: it verifies the managed
// ffprobe file transport before the traversal begins. Per-file probing is not
// part of this contract; scans delegate it to the shared analysis preparer.
type SourceProbe interface {
	CheckFileTransport(context.Context) error
}

// SourceScanStages records the coarse stage a running scan reached.
// *Operations satisfies it with Operations.Running.
type SourceScanStages interface {
	Running(ctx context.Context, operationID uuid.UUID, stage string) error
}

// SourceScanRequest names one scan: the durable operation that owns the stored
// candidates, and the root the scan reads.
type SourceScanRequest struct {
	OperationID            uuid.UUID
	RootID                 uuid.UUID
	ExpectedConfiguredPath string
	ExpectedAttempt        int
	ExpectedJobID          int64
	// AnalysisTargets is sampled by the caller and is part of the durable scan
	// request. In particular SHA hashing is never inferred from service settings.
	AnalysisTargets        SourceAnalysisTarget
	BypassFingerprintCache bool
}

// SourceScan reads a registered source root and turns the files it finds into
// candidates of one scan operation. The source files belong to the operator:
// the traversal only reads them.
type SourceScan struct {
	repository SourceScanRepository
	probe      SourceProbe
	analysis   SourceAnalysisPreparing
	stages     SourceScanStages
	opener     sourcefs.Opener
}

// SourceScanOption replaces one filesystem dependency of a scan.
type SourceScanOption func(*SourceScan)

// WithSourceScanOpener replaces the platform source filesystem opener.
func WithSourceScanOpener(opener sourcefs.Opener) SourceScanOption {
	return func(scan *SourceScan) { scan.opener = opener }
}

// WithSourceScanAnalysis injects the shared executor configured with pinned
// tool metadata and cache lookup. Source scans do not construct tool runners.
func WithSourceScanAnalysis(analysis SourceAnalysisPreparing) SourceScanOption {
	return func(scan *SourceScan) { scan.analysis = analysis }
}

func NewSourceScan(repository SourceScanRepository, probe SourceProbe, stages SourceScanStages, options ...SourceScanOption) *SourceScan {
	scan := &SourceScan{repository: repository, probe: probe, stages: stages, opener: sourcefs.NewOpener()}
	for _, option := range options {
		option(scan)
	}
	return scan
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
	if request.ExpectedAttempt <= 0 || request.ExpectedJobID <= 0 {
		return fmt.Errorf("scan source root: delivery identity is required")
	}
	root, err := s.repository.GetSourceRoot(ctx, request.RootID)
	if err != nil {
		return fmt.Errorf("scan source root: %w", err)
	}
	if !root.Enabled {
		return fmt.Errorf("scan source root: the root %q is disabled", root.DisplayName)
	}
	if request.ExpectedConfiguredPath != "" && request.ExpectedConfiguredPath != root.ConfiguredPath {
		return fmt.Errorf("scan source root: configured path changed since the scan was queued")
	}
	if err := sourcefs.ValidateRootPathSupport(root.ConfiguredPath); err != nil {
		return fmt.Errorf("scan source root: %w", err)
	}
	if err := s.probe.CheckFileTransport(ctx); err != nil {
		return fmt.Errorf("scan source root: verify managed ffprobe file transport: %w", err)
	}
	if s.analysis == nil {
		return fmt.Errorf("scan source root: shared source analysis preparer is unavailable")
	}
	previous, err := s.previousInventory(ctx, root)
	if err != nil {
		return err
	}
	// A retry re-runs the whole traversal, and (operation_id, relative_path) is
	// unique, so the candidates an earlier attempt left behind go first.
	if err := s.repository.DeleteSourceScanCandidatesForDelivery(ctx, request.OperationID, request.ExpectedAttempt, request.ExpectedJobID); err != nil {
		return fmt.Errorf("scan source root: drop the candidates of an earlier attempt: %w", err)
	}
	if err := s.reportStage(ctx, request.OperationID, SourceScanStageTraversing); err != nil {
		return err
	}
	rootHandle, err := s.opener.OpenRoot(ctx, root.ConfiguredPath)
	if err != nil {
		return s.abandonFailedScan(ctx, request, fmt.Errorf("open pinned source root: %w: %w", ErrSourceRootInaccessible, err))
	}
	defer func() { _ = rootHandle.Close() }()
	var traversed []SourceWalkEntry
	var batch []persistence.SourceScanCandidateInput
	walkErr := walkSourceRoot(ctx, rootHandle, func(entry SourceWalkEntry, _ sourcefs.RegularFile) error {
		traversed = append(traversed, entry)
		return nil
	})
	if walkErr != nil {
		return s.abandonFailedScan(ctx, request,
			fmt.Errorf("scan source root: traverse %q: %w", root.ConfiguredPath, walkErr))
	}
	for _, entry := range traversed {
		candidate, err := s.candidateFor(ctx, rootHandle, root.ConfiguredPath, root.LastAppliedOperationID, request, previous, entry)
		if err != nil {
			return s.abandonFailedScan(ctx, request, err)
		}
		batch = append(batch, candidate)
		if len(batch) == sourceScanCandidateBatchSize {
			if err := s.appendCandidates(ctx, request, batch); err != nil {
				return s.abandonFailedScan(ctx, request, err)
			}
			batch = nil
		}
	}
	if err := s.reportStage(ctx, request.OperationID, SourceScanStageApplying); err != nil {
		return s.abandonFailedScan(ctx, request, err)
	}
	if len(batch) > 0 {
		if err := s.appendCandidates(ctx, request, batch); err != nil {
			return s.abandonFailedScan(ctx, request, err)
		}
	}
	for _, entry := range traversed {
		if err := sourceScanConfirmFile(ctx, rootHandle, entry); err != nil {
			return s.abandonFailedScan(ctx, request, err)
		}
	}
	if err := sourceScanConfirmRootNamespace(ctx, root.ConfiguredPath, rootHandle); err != nil {
		return s.abandonFailedScan(ctx, request, err)
	}
	return nil
}

// Enumerate records only filesystem identity for a future enumeration-based
// apply. It intentionally does not consult analysis results or tool capability,
// and is not wired to scan delivery yet; Run remains the production pipeline
// until per-file admission is introduced.
func (s *SourceScan) Enumerate(ctx context.Context, request SourceScanRequest) error {
	_, err := s.EnumerateObserved(ctx, request)
	return err
}

// EnumerateObserved prepares stat-only candidates and returns unreadable scopes
// for a later transactional reconciliation. It remains deliberately unwired from
// the legacy Run/worker path until per-file admission is switched atomically.
func (s *SourceScan) EnumerateObserved(ctx context.Context, request SourceScanRequest) ([]SourceEnumerationScope, error) {
	if request.ExpectedAttempt <= 0 || request.ExpectedJobID <= 0 {
		return nil, fmt.Errorf("enumerate source root: delivery identity is required")
	}
	root, err := s.repository.GetSourceRoot(ctx, request.RootID)
	if err != nil {
		return nil, fmt.Errorf("enumerate source root: %w", err)
	}
	if !root.Enabled || (request.ExpectedConfiguredPath != "" && request.ExpectedConfiguredPath != root.ConfiguredPath) {
		return nil, fmt.Errorf("enumerate source root: root is disabled or its configured path changed")
	}
	if err := sourcefs.ValidateRootPathSupport(root.ConfiguredPath); err != nil {
		return nil, fmt.Errorf("enumerate source root: %w", err)
	}
	if err := s.repository.DeleteSourceScanCandidatesForRootDelivery(ctx, request.OperationID, root.ID, root.ConfiguredPath, request.ExpectedAttempt, request.ExpectedJobID); err != nil {
		return nil, fmt.Errorf("enumerate source root: clear earlier candidates: %w", err)
	}
	candidates := make([]persistence.SourceScanCandidateInput, 0)
	scopes, err := enumerateSourceTree(ctx, s.opener, root.ConfiguredPath, func(entry SourceWalkEntry) error {
		candidates = append(candidates, persistence.SourceScanCandidateInput{
			RelativePath: filepath.ToSlash(entry.RelativePath), SizeBytes: entry.SizeBytes, Mtime: sourceScanMtime(entry.Mtime),
			ProbeStatus: "not_analyzed",
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate source root: %w", err)
	}
	filteredCandidates := candidates[:0]
	for _, candidate := range candidates {
		if !sourceEnumerationCandidateUnreadable(candidate.RelativePath, scopes) {
			filteredCandidates = append(filteredCandidates, candidate)
		}
	}
	candidates = filteredCandidates
	for start := 0; start < len(candidates); start += sourceScanCandidateBatchSize {
		end := min(start+sourceScanCandidateBatchSize, len(candidates))
		if err := s.repository.AppendSourceScanCandidatesForDelivery(ctx, request.OperationID, request.ExpectedAttempt, request.ExpectedJobID, candidates[start:end]); err != nil {
			return nil, fmt.Errorf("enumerate source root: persist candidates: %w", err)
		}
	}
	return scopes, nil
}

func sourceEnumerationCandidateUnreadable(path string, scopes []SourceEnumerationScope) bool {
	for _, scope := range scopes {
		if scope.Kind == "root" || path == scope.RelativePath ||
			(scope.Kind == "subtree" && strings.HasPrefix(path, scope.RelativePath+"/")) {
			return true
		}
	}
	return false
}

// candidateFor turns one walked file into a candidate. It reuses the stored
// status of a file that is unchanged since the last successful scan and probes
// every other file: only a successful probe decides between audio and no_audio,
// and a failed one becomes probe_error rather than a status the file never
// earned.
func (s *SourceScan) candidateFor(ctx context.Context, root sourcefs.Directory, rootPath string, originalScanOperationID *uuid.UUID, request SourceScanRequest, previous map[string]persistence.SourceLocation, entry SourceWalkEntry) (persistence.SourceScanCandidateInput, error) {
	candidate := persistence.SourceScanCandidateInput{
		RelativePath: entry.RelativePath, SizeBytes: entry.SizeBytes, Mtime: sourceScanMtime(entry.Mtime),
	}
	stored, known := previous[entry.RelativePath]
	unchangedIdentity := known && stored.SizeBytes == candidate.SizeBytes && stored.Mtime.Equal(candidate.Mtime)
	if unchangedIdentity && sourceScanReusesStatus(stored, candidate) {
		candidate.ProbeStatus = stored.ProbeStatus
		return candidate, nil
	}
	file, err := sourcefs.OpenRegularAt(ctx, root, entry.RelativePath)
	if err != nil {
		return candidate, fmt.Errorf("open source file for analysis %q: %w", entry.RelativePath, err)
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat(ctx)
	if err != nil {
		return candidate, fmt.Errorf("stat opened source file before probe: %w", err)
	}
	if !sourceScanMatchesEntry(before, entry) || !os.SameFile(before, entry.privateInfo) {
		return candidate, fmt.Errorf("source file %q changed between traversal and open", entry.RelativePath)
	}
	absoluteFilePath := filepath.Join(rootPath, filepath.FromSlash(entry.RelativePath))
	targets := request.AnalysisTargets
	var existingSHA *[sha256.Size]byte
	var retainedSHA *persistence.SourceMediaVariant
	var retainedFingerprint *persistence.SourceFingerprintResult
	if unchangedIdentity && stored.ProbeStatus == persistence.SourceProbeStatusProbeError {
		// A retry of a prior probe failure is not a new file identity. Reuse the
		// selected work snapshot so successful or already-attempted fingerprints
		// are not rerun; only an absent initial fingerprint is eligible to start.
		targets &^= SourceAnalysisTargetSHA256
		targets |= SourceAnalysisTargetProbe
		detail, detailErr := s.repository.ReadSourceLocationDetail(ctx, request.RootID, stored.ID)
		if detailErr != nil {
			return candidate, fmt.Errorf("read retained source analysis for %q: %w", entry.RelativePath, detailErr)
		}
		if detail == nil || detail.Location == nil || detail.Location.ID != stored.ID {
			return candidate, fmt.Errorf("read retained source analysis for %q: location detail is incomplete", entry.RelativePath)
		}
		retainedSHA = detail.SHAVariant
		if retainedSHA == nil && stored.MediaVariantID != nil {
			if variant, ok := s.repository.(interface {
				GetSourceMediaVariant(context.Context, uuid.UUID) (*persistence.SourceMediaVariant, error)
			}); ok {
				retainedSHA, err = variant.GetSourceMediaVariant(ctx, *stored.MediaVariantID)
				if err != nil {
					return candidate, fmt.Errorf("read retained source digest for %q: %w", entry.RelativePath, err)
				}
			}
		}
		if retainedSHA != nil && len(retainedSHA.SourceSHA256) == sha256.Size {
			var digest [sha256.Size]byte
			copy(digest[:], retainedSHA.SourceSHA256)
			existingSHA = &digest
		}
		fingerprintStepExists := false
		for _, step := range detail.Steps {
			if step.Step == string(persistence.SourceStepFingerprint) {
				fingerprintStepExists = true
				break
			}
		}
		if fingerprintStepExists {
			targets &^= SourceAnalysisTargetFingerprint
			retainedFingerprint = detail.Fingerprint
		} else {
			targets |= SourceAnalysisTargetFingerprint
		}
	}
	if err := sourceScanConfirmRootNamespace(ctx, rootPath, root); err != nil {
		return candidate, err
	}
	if err := sourceScanConfirmFileNamespace(absoluteFilePath, before); err != nil {
		return candidate, err
	}
	prepared := s.analysis.Prepare(ctx, SourceAnalysisPrepareRequest{
		File: file, ServerPath: filepath.Join(rootPath, filepath.FromSlash(entry.RelativePath)),
		Targets: targets, ExistingSHA256: existingSHA,
		BypassFingerprintCache: request.BypassFingerprintCache,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return candidate, fmt.Errorf("prepare source analysis for %q: %w", entry.RelativePath, ctxErr)
	}
	if err := sourceScanConfirmRootNamespace(ctx, rootPath, root); err != nil {
		return candidate, err
	}
	if err := sourceScanConfirmFileNamespace(absoluteFilePath, before); err != nil {
		return candidate, err
	}
	if targets&SourceAnalysisTargetProbe == 0 {
		return candidate, fmt.Errorf("prepare source analysis for %q: probe was not requested", entry.RelativePath)
	}
	if targets&SourceAnalysisTargetFingerprint != 0 && prepared.Fingerprint.State == SourceAnalysisNotRequested {
		return candidate, fmt.Errorf("prepare source analysis for %q: requested fingerprint was not prepared", entry.RelativePath)
	}
	if targets&SourceAnalysisTargetFingerprint == 0 && prepared.Fingerprint.State != SourceAnalysisNotRequested {
		return candidate, fmt.Errorf("prepare source analysis for %q: unrequested fingerprint was prepared", entry.RelativePath)
	}
	probedAfter, err := file.Stat(ctx)
	if err != nil {
		return candidate, fmt.Errorf("stat prepared source file after analysis: %w", err)
	}
	if !sourceScanMatchesEntry(probedAfter, entry) ||
		probedAfter.Size() != before.Size() || !sourceScanMtime(probedAfter.ModTime()).Equal(sourceScanMtime(before.ModTime())) {
		return candidate, fmt.Errorf("source file %q changed while it was being scanned", entry.RelativePath)
	}
	if err := sourceScanConfirmFile(ctx, root, entry); err != nil {
		return candidate, err
	}
	switch prepared.Probe.State {
	case SourceAnalysisFailed:
		safe := sourceScanProbeErrorText
		candidate.ProbeStatus = persistence.SourceProbeStatusProbeError
		candidate.SafeError = &safe
	case SourceAnalysisDeferred:
		if unchangedIdentity {
			candidate.ProbeStatus = stored.ProbeStatus
			candidate.SafeError = stored.SafeError
		} else {
			// Inventory has no pending probe classification. Keep this as an
			// inventory-only unknown; the durable analysis step below remains pending.
			safe := "source technical analysis is pending"
			candidate.ProbeStatus = persistence.SourceProbeStatusProbeError
			candidate.SafeError = &safe
		}
	case SourceAnalysisSucceeded, SourceAnalysisCacheHit:
		candidate.ProbeStatus = persistence.SourceProbeStatusNoAudio
		if prepared.Probe.AudioStreamCount > 0 {
			candidate.ProbeStatus = persistence.SourceProbeStatusAudio
		}
	default:
		return candidate, fmt.Errorf("prepare source analysis for %q: probe was not requested", entry.RelativePath)
	}
	preparedAnalysis, err := sourceScanPreparedAnalysis(request.OperationID, originalScanOperationID, candidate.SizeBytes, targets, prepared, retainedSHA, retainedFingerprint, known, stored)
	if err != nil {
		return candidate, fmt.Errorf("prepare source analysis for %q: %w", entry.RelativePath, err)
	}
	candidate.PreparedAnalysis = preparedAnalysis
	return candidate, nil
}

// sourceScanConfirmRootNamespace makes sure the absolute namespace used by
// pathname-based analysis still names the directory pinned for this scan.
func sourceScanConfirmRootNamespace(ctx context.Context, rootPath string, root sourcefs.Directory) error {
	pinnedInfo, err := root.Stat(ctx)
	if err != nil {
		return fmt.Errorf("stat pinned source root: %w", err)
	}
	currentInfo, err := os.Stat(rootPath)
	if err != nil {
		return fmt.Errorf("stat current source root namespace: %w", err)
	}
	if !currentInfo.IsDir() || !os.SameFile(pinnedInfo, currentInfo) {
		return fmt.Errorf("source root namespace changed while it was being scanned")
	}
	return nil
}

// sourceScanConfirmFileNamespace protects the pathname passed to tools such as
// fpcalc from resolving to a different file than the descriptor-backed input.
func sourceScanConfirmFileNamespace(path string, pinnedInfo fs.FileInfo) error {
	currentInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat current source file namespace: %w", err)
	}
	if !currentInfo.Mode().IsRegular() || !os.SameFile(pinnedInfo, currentInfo) {
		return fmt.Errorf("source file namespace changed while it was being scanned")
	}
	return nil
}

func sourceScanPreparedAnalysis(operationID uuid.UUID, originalScanOperationID *uuid.UUID, size int64, targets SourceAnalysisTarget, result SourceAnalysisPreparation, retained *persistence.SourceMediaVariant, retainedFingerprint *persistence.SourceFingerprintResult, hasOriginal bool, original persistence.SourceLocation) (*persistence.SourceScanPreparedAnalysis, error) {
	prepared := &persistence.SourceScanPreparedAnalysis{
		Version:          1,
		SHA256State:      persistence.SourcePreparedNotRequested,
		ProbeState:       persistence.SourcePreparedNotRequested,
		FingerprintState: persistence.SourcePreparedNotRequested,
	}
	if targets&SourceAnalysisTargetSHA256 != 0 {
		prepared.HashRequested = true
		switch result.SHA256.State {
		case SourceAnalysisSucceeded:
			algorithm := "sha256"
			calculated := time.Now().UTC()
			applied := operationID
			prepared.SHA256State = persistence.SourcePreparedSucceeded
			prepared.SHA256Variant = &persistence.SourceMediaVariant{
				ID: uuid.New(), SizeBytes: size, SourceSHA256: append([]byte(nil), result.SHA256.Digest[:]...),
				SHA256Algorithm: &algorithm, SHA256CalculatedAt: &calculated, SHA256AppliedOperationID: &applied,
			}
		case SourceAnalysisFailed:
			prepared.SHA256State = persistence.SourcePreparedFailed
			prepared.SHA256SafeError = result.SHA256.SafeError
		default:
			return nil, fmt.Errorf("requested SHA-256 was not prepared")
		}
	}
	if retained != nil && len(retained.SourceSHA256) == sha256.Size {
		prepared.RetainedSHA256Variant = retained
	}
	if retainedFingerprint != nil {
		prepared.FingerprintState = persistence.SourcePreparedSucceeded
		prepared.FingerprintResult = retainedFingerprint
		prepared.ReusedImmutableIDs = append(prepared.ReusedImmutableIDs, retainedFingerprint.ID)
	}
	if result.Probe.State == SourceAnalysisSucceeded || result.Probe.State == SourceAnalysisCacheHit {
		if result.Probe.Result == nil {
			return nil, fmt.Errorf("successful probe has no immutable result")
		}
		prepared.ProbeState = persistence.SourcePreparedSucceeded
		prepared.ProbeVariant = result.Probe.Result
		if result.Probe.State == SourceAnalysisCacheHit {
			prepared.ReusedImmutableIDs = append(prepared.ReusedImmutableIDs, result.Probe.Result.ID)
		}
	} else if result.Probe.State == SourceAnalysisFailed {
		prepared.ProbeState = persistence.SourcePreparedFailed
		prepared.ProbeSafeError = result.Probe.SafeError
	} else if result.Probe.State == SourceAnalysisDeferred {
		prepared.ProbeState = persistence.SourcePreparedDeferred
	} else if result.Probe.State != SourceAnalysisNotRequested {
		return nil, fmt.Errorf("unknown probe outcome %q", result.Probe.State)
	}
	if targets&SourceAnalysisTargetFingerprint == 0 && result.Fingerprint.State != SourceAnalysisNotRequested {
		return nil, fmt.Errorf("unrequested fingerprint was prepared")
	}
	if targets&SourceAnalysisTargetFingerprint != 0 && (result.Fingerprint.State == SourceAnalysisSucceeded || result.Fingerprint.State == SourceAnalysisCacheHit) {
		if result.Fingerprint.Result == nil {
			return nil, fmt.Errorf("successful fingerprint has no immutable result")
		}
		prepared.FingerprintState = persistence.SourcePreparedSucceeded
		prepared.FingerprintResult = result.Fingerprint.Result
		if result.Fingerprint.State == SourceAnalysisCacheHit {
			prepared.FingerprintReused = true
			prepared.FingerprintCacheSHA256 = sourceScanPreparedDigest(prepared, retained)
			prepared.FingerprintCacheVersion = result.Fingerprint.Result.FPCalcVersion
			prepared.ReusedImmutableIDs = append(prepared.ReusedImmutableIDs, result.Fingerprint.Result.ID)
		}
	} else if targets&SourceAnalysisTargetFingerprint != 0 && result.Fingerprint.State == SourceAnalysisFailed {
		prepared.FingerprintState = persistence.SourcePreparedFailed
		prepared.FingerprintSafeError = result.Fingerprint.SafeError
	} else if targets&SourceAnalysisTargetFingerprint != 0 && result.Fingerprint.State == SourceAnalysisDeferred {
		prepared.FingerprintState = persistence.SourcePreparedDeferred
	} else if targets&SourceAnalysisTargetFingerprint != 0 && result.Fingerprint.State != SourceAnalysisNotRequested {
		return nil, fmt.Errorf("unknown fingerprint outcome %q", result.Fingerprint.State)
	}
	if hasOriginal {
		locationID := original.ID
		prepared.OriginalLocationID = &locationID
		if originalScanOperationID != nil {
			originalOperationID := *originalScanOperationID
			prepared.OriginalScanOperationID = &originalOperationID
		}
		if original.MediaVariantID != nil {
			variantID := *original.MediaVariantID
			prepared.OriginalMediaVariantID = &variantID
		}
	}
	if err := prepared.Validate(); err != nil {
		return nil, err
	}
	return prepared, nil
}

func sourceScanPreparedDigest(prepared *persistence.SourceScanPreparedAnalysis, retained *persistence.SourceMediaVariant) string {
	variant := prepared.SHA256Variant
	if variant == nil {
		variant = prepared.RetainedSHA256Variant
	}
	if variant == nil && retained != nil {
		variant = retained
	}
	if variant == nil || len(variant.SourceSHA256) != sha256.Size {
		return ""
	}
	return fmt.Sprintf("%x", variant.SourceSHA256)
}

func sourceScanMatchesEntry(info fs.FileInfo, entry SourceWalkEntry) bool {
	return info.Mode().IsRegular() && info.Size() == entry.SizeBytes &&
		sourceScanMtime(info.ModTime()).Equal(sourceScanMtime(entry.Mtime))
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

func (s *SourceScan) appendCandidates(ctx context.Context, request SourceScanRequest, batch []persistence.SourceScanCandidateInput) error {
	err := s.repository.AppendSourceScanCandidatesForDelivery(ctx, request.OperationID, request.ExpectedAttempt, request.ExpectedJobID, batch)
	if err != nil {
		return fmt.Errorf("scan source root: store a candidate batch: %w", err)
	}
	return nil
}

// abandonFailedScan drops the candidates of an attempt that failed, so a run
// that did not read the whole tree leaves nothing another step could apply. The
// cleanup ignores the cancellation that failed the scan; both errors are
// reported, never one instead of the other.
func (s *SourceScan) abandonFailedScan(ctx context.Context, request SourceScanRequest, scanErr error) error {
	err := s.repository.DeleteSourceScanCandidatesForDelivery(context.WithoutCancel(ctx), request.OperationID, request.ExpectedAttempt, request.ExpectedJobID)
	if err != nil {
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
func sourceScanConfirmFile(ctx context.Context, root sourcefs.Directory, entry SourceWalkEntry) error {
	file, err := sourcefs.OpenRegularAt(ctx, root, entry.RelativePath)
	if err != nil {
		if errors.Is(err, sourcefs.ErrLink) || errors.Is(err, sourcefs.ErrNotRegular) {
			return fmt.Errorf("confirm source file %q: the file is no longer a regular file", entry.RelativePath)
		}
		return fmt.Errorf("confirm source file %q: %w", entry.RelativePath, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat(ctx)
	if err != nil {
		return fmt.Errorf("confirm source file %q: %w", entry.RelativePath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("confirm source file %q: the file is no longer a regular file", entry.RelativePath)
	}
	if !os.SameFile(info, entry.privateInfo) || info.Size() != entry.SizeBytes || !sourceScanMtime(info.ModTime()).Equal(sourceScanMtime(entry.Mtime)) {
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
