package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const (
	// SourceScanStageQueued is the stage of a scan operation that was created but
	// whose worker has not started.
	SourceScanStageQueued = "queued"
	// SourceScanStageTraversing is the stage of a scan reading the source tree.
	SourceScanStageTraversing = "traversing"
	// SourceScanStageApplying is the stage of a scan finalizing its verified
	// enumeration.
	SourceScanStageApplying = "applying"

	sourceScanCandidateBatchSize = 500
)

const SourceScanDirectoryUnavailableReason = persistence.SourceEnumerationRootUnavailableReason

// SourceScanRepository is the persistence contract needed to stage a
// stat-only enumeration. It does not read or modify the currently applied
// inventory; publication is a separate transactional step.
type SourceScanRepository interface {
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
	DeleteSourceScanCandidatesForRootDelivery(context.Context, uuid.UUID, uuid.UUID, string, int, int64) error
	AppendSourceScanCandidatesForDelivery(context.Context, uuid.UUID, int, int64, []persistence.SourceScanCandidateInput) error
}

// SourceScanRequest names one scan: the durable operation that owns the stored
// candidates, and the root the scan reads.
type SourceScanRequest struct {
	OperationID            uuid.UUID
	RootID                 uuid.UUID
	ExpectedConfiguredPath string
	ExpectedAttempt        int
	ExpectedJobID          int64
}

// SourceScan reads a registered source root and stages stat-only candidates.
// The source files belong to the operator; enumeration only reads them.
type SourceScan struct {
	repository SourceScanRepository
	opener     sourcefs.Opener
}

// SourceScanOption replaces one filesystem dependency of a scan.
type SourceScanOption func(*SourceScan)

// WithSourceScanOpener replaces the platform source filesystem opener.
func WithSourceScanOpener(opener sourcefs.Opener) SourceScanOption {
	return func(scan *SourceScan) { scan.opener = opener }
}

func NewSourceScan(repository SourceScanRepository, options ...SourceScanOption) *SourceScan {
	scan := &SourceScan{repository: repository, opener: sourcefs.NewOpener()}
	for _, option := range options {
		option(scan)
	}
	return scan
}

// EnumerateObserved prepares stat-only candidates and returns unreadable scopes
// for the worker's transactional reconciliation. The worker applies the
// enumeration before admitting per-file analysis.
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
			ProbeStatus: persistence.SourceProbeStatusNotAnalyzed,
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

// sourceScanConfirmRootNamespace ensures the configured pathname still names
// the directory pinned for enumeration.
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

// sourceScanMtime is the mtime as the inventory stores it. PostgreSQL keeps
// microseconds, so truncate filesystem readings to the same precision.
func sourceScanMtime(value time.Time) time.Time {
	return value.Truncate(time.Microsecond)
}
