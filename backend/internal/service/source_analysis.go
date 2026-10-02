package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// ErrSourceAnalysisToolUnavailable reports the managed FFmpeg installation an
// analysis snapshot pins cannot be used: it is missing, not ready, built for
// another platform, carries an invalid managed path, or fails its version query.
var ErrSourceAnalysisToolUnavailable = errors.New("the selected managed ffmpeg installation is unavailable")

// SourceAnalysisRepository is the read-only persistence contract of one source
// analysis. It only reads: the analysis never writes the database, so a caller
// applies the prepared result later, in the transactional apply of step 2.
type SourceAnalysisRepository interface {
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
	GetSourceLocation(context.Context, uuid.UUID, uuid.UUID) (*persistence.SourceLocation, error)
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
}

// SourceTechnicalProbe runs the bounded technical ffprobe request of one
// absolute server path. *tools.FFProbe is the production implementation.
type SourceTechnicalProbe interface {
	ProbeTechnical(ctx context.Context, absoluteServerPath string) ([]byte, error)
}

// SourceTechnicalProbeFactory builds the technical probe over the resolved
// absolute managed executable. It is a seam so a test can answer without
// spawning ffprobe, while the production default builds the real one.
type SourceTechnicalProbeFactory func(executable string) (SourceTechnicalProbe, error)

// NewManagedSourceTechnicalProbe is the production probe factory: it builds the
// bounded technical ffprobe over an absolute managed executable and refuses a
// path that is not absolute.
func NewManagedSourceTechnicalProbe(executable string) (SourceTechnicalProbe, error) {
	return tools.NewFFProbe(executable)
}

// SourceAnalysisOption replaces one dependency of an analysis. It exists so the
// production defaults (the real tool lifecycle and the real ffprobe) are what an
// unconfigured service uses, while a test substitutes them deliberately.
type SourceAnalysisOption func(*SourceAnalysis)

// WithSourceAnalysisVerifier replaces the tool lifecycle version query.
func WithSourceAnalysisVerifier(verifier InstallationVerifier) SourceAnalysisOption {
	return func(analysis *SourceAnalysis) { analysis.verifier = verifier }
}

// WithSourceTechnicalProbeFactory replaces the technical probe factory.
func WithSourceTechnicalProbeFactory(factory SourceTechnicalProbeFactory) SourceAnalysisOption {
	return func(analysis *SourceAnalysis) { analysis.newProbe = factory }
}

// SourceAnalysisRequest names one analysis: the durable operation that will own
// the result and the immutable snapshot that pins the file, its inventory
// identity and the managed installation to use.
type SourceAnalysisRequest struct {
	OperationID uuid.UUID
	Snapshot    persistence.SourceAnalysisSnapshot
}

// SourceAnalysis runs one safe, read-only technical analysis of a single source
// location. It resolves the registered root and location from the snapshot,
// proves they still describe the pinned file, resolves the exact source file
// below the root without following a symlink or escaping it, verifies the pinned
// managed FFmpeg installation with a fresh version query and probes the file
// once. It returns the prepared result for a later transactional apply.
//
// The source is only ever read: the analysis never opens it for writing, creates
// no scratch file, and mutates neither the scan generation, the probe status nor
// the availability of the root. A file that changed while it was being analyzed
// is never published; the previous variant stays exactly as it was because
// nothing is applied here.
type SourceAnalysis struct {
	repository     SourceAnalysisRepository
	toolsDirectory ToolsDirectoryReader
	verifier       InstallationVerifier
	platform       settings.Platform
	newProbe       SourceTechnicalProbeFactory
}

// NewSourceAnalysis builds the analysis use case. The managed installation is
// verified and the ffprobe is built through the real tool lifecycle and the real
// technical ffprobe unless an option substitutes them.
func NewSourceAnalysis(repository SourceAnalysisRepository, toolsDirectory ToolsDirectoryReader, platform settings.Platform, options ...SourceAnalysisOption) *SourceAnalysis {
	analysis := &SourceAnalysis{
		repository: repository, toolsDirectory: toolsDirectory, platform: platform,
		verifier: tools.NewLifecycle(nil),
		newProbe: NewManagedSourceTechnicalProbe,
	}
	for _, option := range options {
		option(analysis)
	}
	return analysis
}

// Run validates the snapshot against the current root and location, resolves the
// exact source file and the pinned managed ffprobe, probes the file once and
// returns the prepared result. Nothing is written anywhere.
//
// Every disagreement between the snapshot and the persisted state, and every
// change to the file observed around the probe, is
// persistence.ErrSourceAnalysisStale and publishes nothing, so a stale snapshot
// can never attach a result to a file it no longer describes. A cancellation is
// returned as its context error so the caller can retry the whole operation.
func (s *SourceAnalysis) Run(ctx context.Context, request SourceAnalysisRequest) (persistence.SourceAnalysisApply, error) {
	var empty persistence.SourceAnalysisApply
	snapshot := request.Snapshot
	if err := s.validateRequest(request); err != nil {
		return empty, err
	}
	root, err := s.repository.GetSourceRoot(ctx, snapshot.SourceRootID)
	if err != nil {
		return empty, fmt.Errorf("analyze source location: read the source root: %w", err)
	}
	if !root.Enabled || root.Stale() || root.ConfiguredPath != snapshot.ConfiguredPath ||
		root.InventoryPath == nil || *root.InventoryPath != snapshot.InventoryPath {
		return empty, fmt.Errorf("analyze source location: the source root no longer matches the snapshot: %w", persistence.ErrSourceAnalysisStale)
	}
	location, err := s.repository.GetSourceLocation(ctx, root.ID, snapshot.SourceLocationID)
	if err != nil {
		return empty, fmt.Errorf("analyze source location: read the source location: %w", err)
	}
	if location.SourceRootID != root.ID || location.RelativePath != snapshot.RelativePath ||
		location.SizeBytes != snapshot.SizeBytes ||
		!sourceScanMtime(location.Mtime).Equal(sourceScanMtime(snapshot.Mtime)) ||
		location.ProbeStatus != persistence.SourceProbeStatusAudio ||
		!sourceAnalysisSameVariant(location.MediaVariantID, snapshot.PreviousVariantID) {
		return empty, fmt.Errorf("analyze source location: the location no longer matches the snapshot: %w", persistence.ErrSourceAnalysisStale)
	}
	absolute, before, err := sourceAnalysisResolveFile(root.ConfiguredPath, snapshot.RelativePath)
	if err != nil {
		return empty, fmt.Errorf("analyze source location: %w", err)
	}
	// The inventory keeps microseconds, so this comparison uses exactly that
	// precision and accepts an unchanged file whose mtime is not aligned to a
	// microsecond.
	if before.Size() != snapshot.SizeBytes || !sourceScanMtime(before.ModTime()).Equal(sourceScanMtime(snapshot.Mtime)) {
		return empty, fmt.Errorf("analyze source location: the file changed since it was inventoried: %w", persistence.ErrSourceAnalysisStale)
	}
	executable, version, err := s.resolveManagedFFProbe(ctx, snapshot)
	if err != nil {
		return empty, err
	}
	probe, err := s.newProbe(executable)
	if err != nil {
		return empty, fmt.Errorf("analyze source location: build the managed ffprobe: %w: %w", ErrSourceAnalysisToolUnavailable, err)
	}
	raw, err := probe.ProbeTechnical(ctx, absolute)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return empty, fmt.Errorf("analyze source location: %w", ctxErr)
		}
		return empty, fmt.Errorf("analyze source location: probe the source file: %w", err)
	}
	// The path and the file are read again after the probe with the full
	// filesystem precision: a file or a directory swapped for a symlink, a
	// vanished file, and a file changed within the same PostgreSQL microsecond
	// but a different filesystem nanosecond all fail here, so a changed file can
	// never publish a result.
	resolved, after, err := sourceAnalysisResolveFile(root.ConfiguredPath, snapshot.RelativePath)
	if err != nil {
		return empty, fmt.Errorf("analyze source location: %w", err)
	}
	if resolved != absolute || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return empty, fmt.Errorf("analyze source location: the file changed while it was being analyzed: %w", persistence.ErrSourceAnalysisStale)
	}
	analysis, err := ParseSourceTechnicalAnalysis(raw)
	if err != nil {
		return empty, fmt.Errorf("analyze source location: parse the technical response: %w", err)
	}
	tags, err := json.Marshal(analysis.Tags)
	if err != nil {
		return empty, fmt.Errorf("analyze source location: encode the observed tags: %w", err)
	}
	return persistence.SourceAnalysisApply{
		OperationID:           request.OperationID,
		RelativePath:          snapshot.RelativePath,
		SizeBytes:             before.Size(),
		Mtime:                 before.ModTime(),
		AnalysisPolicyVersion: snapshot.AnalysisPolicyVersion,
		FFProbeVersion:        version,
		FFProbeJSON:           analysis.RawJSON,
		ObservedTags:          tags,
		InspectedAt:           time.Now().UTC(),
	}, nil
}

// validateRequest refuses a snapshot that is not the shape and policy this build
// writes, so a caller cannot hand the analysis an input it would misread.
func (s *SourceAnalysis) validateRequest(request SourceAnalysisRequest) error {
	snapshot := request.Snapshot
	if request.OperationID == uuid.Nil {
		return fmt.Errorf("analyze source location: an operation id is required")
	}
	if snapshot.SchemaVersion != persistence.SourceAnalysisSnapshotVersion {
		return fmt.Errorf("analyze source location: unsupported snapshot schema version %d", snapshot.SchemaVersion)
	}
	if snapshot.SourceRootID == uuid.Nil || snapshot.SourceLocationID == uuid.Nil || snapshot.AnalysisInstallationID == uuid.Nil {
		return fmt.Errorf("analyze source location: the snapshot is incomplete")
	}
	if snapshot.AnalysisPolicyVersion != persistence.SourceAnalysisPolicyVersion {
		return fmt.Errorf("analyze source location: unsupported analysis policy version %d", snapshot.AnalysisPolicyVersion)
	}
	return nil
}

// sourceAnalysisSameVariant compares two nullable variant identifiers, treating
// nil as a value that must match nil.
func sourceAnalysisSameVariant(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
