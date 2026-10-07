package persistence

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var (
	// ErrSourceRootActiveAnalysis reports an analysis start refused because
	// another analysis of the same root is queued or running. The root
	// exclusivity rule accepts one root-targeting operation at a time, whether
	// it is a scan or an analysis.
	ErrSourceRootActiveAnalysis = errors.New("source root has an active analysis")
	// ErrToolsRootMoveActive reports an analysis start refused because a managed
	// tools root move is queued or running. A move rewrites the tools directory
	// an analysis would read its pinned executable from, so the two exclude
	// each other in both directions.
	ErrToolsRootMoveActive = errors.New("a managed tools root move is active")
	// ErrToolsInstallationHeldByAnalysis reports a tools root move refused
	// because an active analysis holds one of the managed installations it would
	// relocate. The hold is a read hold, so it may be held by several roots at
	// once and never consumes the installation mutation target.
	ErrToolsInstallationHeldByAnalysis = errors.New("a managed installation is held by an active source analysis")
	// ErrSourceAnalysisInstallationChanged reports an analysis start refused
	// because the active managed FFmpeg installation changed between the
	// operator's read and the transaction that records the snapshot. The start
	// linearizes on the active installation it read under the activation lock:
	// an activation that finished first is observed and the start is refused
	// rather than queued against a selection the operator no longer sees.
	ErrSourceAnalysisInstallationChanged = errors.New("the active managed ffmpeg installation changed while the analysis was starting")
	// ErrSourceAnalysisInstallationUnusable reports an analysis start refused
	// because the installation the active setting names is absent, unparsable,
	// not a ready FFmpeg package, or built for another platform.
	ErrSourceAnalysisInstallationUnusable = errors.New("the selected managed ffmpeg installation is not usable for this instance")
)

// SourceAnalysisStartStore combines the source inventory and setup manager
// repositories behind the read and enqueue contract an analysis start needs,
// without making either repository depend on the other.
type SourceAnalysisStartStore struct {
	*SourceInventoryRepository
	*SetupManagerRepository
}

func NewSourceAnalysisStartStore(database *bun.DB) *SourceAnalysisStartStore {
	return &SourceAnalysisStartStore{
		SourceInventoryRepository: NewSourceInventoryRepository(database),
		SetupManagerRepository:    NewSetupManagerRepository(database),
	}
}

func (store *SourceAnalysisStartStore) GetInstallation(ctx context.Context, id uuid.UUID) (*ToolInstallation, error) {
	return store.SetupManagerRepository.GetInstallation(ctx, id)
}
