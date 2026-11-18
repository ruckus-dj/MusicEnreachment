package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const (
	// SourceAnalysisArtifactCleanupDrainInterval paces automatic maintenance.
	// Restart performs one immediate pass, then each tick drains one bounded batch.
	SourceAnalysisArtifactCleanupDrainInterval = time.Minute
	// SourceAnalysisArtifactCleanupDrainBatch bounds one automatic admission so a
	// large backlog is drained through several fenced deliveries instead of one
	// unbounded operation.
	SourceAnalysisArtifactCleanupDrainBatch = 500
	// sourceAnalysisArtifactCleanupDrainScan is the largest candidate listing the
	// repository supports. Failed artifacts can be interleaved with eligible ones,
	// so the scan is wider than the admitted batch.
	sourceAnalysisArtifactCleanupDrainScan = 1000
)

// SourceAnalysisArtifactCleanupDrainOperations lists active operations so the
// drain can avoid selecting artifacts already reserved by a queued or running
// cleanup delivery. Admission creates the operation before its first claim, so
// the candidate listing alone cannot detect that reservation window.
type SourceAnalysisArtifactCleanupDrainOperations interface {
	ListOperations(context.Context, ...string) ([]persistence.Operation, error)
}

// SourceAnalysisArtifactCleanupDrain admits bounded automatic cleanup batches
// for staged artifacts that are already eligible for removal. It never touches
// the filesystem: admission rechecks eligibility under the output gate and the
// cleanup delivery worker performs every removal behind its own fence.
type SourceAnalysisArtifactCleanupDrain struct {
	cleanup    *SourceAnalysisArtifactCleanup
	operations SourceAnalysisArtifactCleanupDrainOperations
}

func NewSourceAnalysisArtifactCleanupDrain(cleanup *SourceAnalysisArtifactCleanup, operations SourceAnalysisArtifactCleanupDrainOperations) *SourceAnalysisArtifactCleanupDrain {
	return &SourceAnalysisArtifactCleanupDrain{cleanup: cleanup, operations: operations}
}

// Drain admits at most one bounded batch and reports how many artifacts were
// admitted. Artifacts whose removal previously failed are left to the explicit
// retry endpoint so a permanent filesystem failure cannot become a periodic
// retry loop.
func (drain *SourceAnalysisArtifactCleanupDrain) Drain(ctx context.Context) (int, error) {
	if drain == nil || drain.cleanup == nil {
		return 0, fmt.Errorf("drain source analysis artifact cleanup: cleanup service is required")
	}
	if drain.operations == nil {
		return 0, fmt.Errorf("drain source analysis artifact cleanup: operation repository is required")
	}
	if retirement, ok := drain.operations.(interface {
		RetireIdleSourceAnalysisArtifacts(context.Context, int) error
	}); ok {
		if err := retirement.RetireIdleSourceAnalysisArtifacts(ctx, SourceAnalysisArtifactCleanupDrainBatch); err != nil {
			return 0, err
		}
	}
	reserved, err := drain.reservedArtifactIDs(ctx)
	if err != nil {
		return 0, err
	}
	var candidates []SourceAnalysisArtifactCleanupCandidate
	if repository, ok := drain.operations.(interface {
		ListAutomaticSourceAnalysisArtifactCleanupCandidates(context.Context, int) ([]persistence.SourceAnalysisArtifactCleanupCandidate, error)
	}); ok {
		eligible, listErr := repository.ListAutomaticSourceAnalysisArtifactCleanupCandidates(ctx, SourceAnalysisArtifactCleanupDrainBatch)
		if listErr != nil {
			return 0, listErr
		}
		for _, item := range eligible {
			candidates = append(candidates, SourceAnalysisArtifactCleanupCandidate{ArtifactID: item.ArtifactID, State: item.State})
		}
	} else {
		candidates, err = drain.cleanup.Candidates(ctx, sourceAnalysisArtifactCleanupDrainScan)
		if err != nil {
			return 0, fmt.Errorf("drain source analysis artifact cleanup: %w", err)
		}
	}
	selected := make([]uuid.UUID, 0, SourceAnalysisArtifactCleanupDrainBatch)
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if len(selected) == SourceAnalysisArtifactCleanupDrainBatch {
			break
		}
		if candidate.State != persistence.SourceAnalysisArtifactCleanupEligible {
			continue
		}
		if _, isReserved := reserved[candidate.ArtifactID]; isReserved {
			continue
		}
		if _, duplicate := seen[candidate.ArtifactID]; duplicate {
			continue
		}
		seen[candidate.ArtifactID] = struct{}{}
		selected = append(selected, candidate.ArtifactID)
	}
	if len(selected) == 0 {
		return 0, nil
	}
	if _, err := drain.cleanup.Admit(ctx, selected); err != nil {
		return 0, fmt.Errorf("drain source analysis artifact cleanup: %w", err)
	}
	return len(selected), nil
}

// reservedArtifactIDs reads the exact artifact set of every non-terminal cleanup
// operation. Decoding is strict: an unreadable snapshot fails the pass closed
// instead of risking a second admission for the same artifacts.
func (drain *SourceAnalysisArtifactCleanupDrain) reservedArtifactIDs(ctx context.Context) (map[uuid.UUID]struct{}, error) {
	operations, err := drain.operations.ListOperations(ctx, "queued", "running")
	if err != nil {
		return nil, fmt.Errorf("drain source analysis artifact cleanup: list active operations: %w", err)
	}
	reserved := make(map[uuid.UUID]struct{})
	for _, operation := range operations {
		if operation.Kind != SourceAnalysisArtifactCleanupOperationKind {
			continue
		}
		var snapshot persistence.SourceAnalysisArtifactCleanupSnapshot
		if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
			return nil, fmt.Errorf("drain source analysis artifact cleanup: decode operation %s snapshot: %w", operation.ID, err)
		}
		for _, artifactID := range snapshot.ArtifactIDs {
			reserved[artifactID] = struct{}{}
		}
	}
	return reserved, nil
}
