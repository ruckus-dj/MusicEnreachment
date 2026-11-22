package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const SourceAnalysisArtifactCleanupOperationKind = persistence.SourceAnalysisArtifactCleanupOperationKind

const SourceAnalysisArtifactCleanupJobKind = persistence.SourceAnalysisArtifactCleanupJobKind

// SourceAnalysisArtifactCleanupJobArgs carries only the operation identity.
type SourceAnalysisArtifactCleanupJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (SourceAnalysisArtifactCleanupJobArgs) Kind() string {
	return SourceAnalysisArtifactCleanupJobKind
}

type SourceAnalysisArtifactCleanupRepository interface {
	ListSourceAnalysisArtifactCleanupCandidates(context.Context, int) ([]persistence.SourceAnalysisArtifactCleanupCandidate, error)
	AdmitSourceAnalysisArtifactCleanupWithArgsFactory(context.Context, []uuid.UUID, persistence.RiverInserter, func(uuid.UUID) river.JobArgs, *river.InsertOpts) (*persistence.Operation, error)
}

type SourceAnalysisArtifactCleanupCandidate struct {
	ArtifactID         uuid.UUID `json:"artifact_id"`
	SourceRelativePath string    `json:"source_relative_path"`
	State              string    `json:"state"`
}

type SourceAnalysisArtifactCleanupItemResult struct {
	ArtifactID uuid.UUID `json:"artifact_id"`
	State      string    `json:"state"`
	SafeError  *string   `json:"safe_error,omitempty"`
}

type SourceAnalysisArtifactCleanup struct {
	repository SourceAnalysisArtifactCleanupRepository
	river      persistence.RiverInserter
}

func NewSourceAnalysisArtifactCleanup(repository SourceAnalysisArtifactCleanupRepository, riverClient persistence.RiverInserter) *SourceAnalysisArtifactCleanup {
	return &SourceAnalysisArtifactCleanup{repository: repository, river: riverClient}
}

func (s *SourceAnalysisArtifactCleanup) Candidates(ctx context.Context, limit int) ([]SourceAnalysisArtifactCleanupCandidate, error) {
	candidates, err := s.repository.ListSourceAnalysisArtifactCleanupCandidates(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]SourceAnalysisArtifactCleanupCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, SourceAnalysisArtifactCleanupCandidate{ArtifactID: candidate.ArtifactID, SourceRelativePath: candidate.SourceRelativePath, State: candidate.State})
	}
	return result, nil
}

func (s *SourceAnalysisArtifactCleanup) Admit(ctx context.Context, ids []uuid.UUID) (*persistence.Operation, error) {
	if s.river == nil {
		return nil, fmt.Errorf("admit source analysis artifact cleanup: River client is required")
	}
	return s.repository.AdmitSourceAnalysisArtifactCleanupWithArgsFactory(ctx, ids, s.river, func(operationID uuid.UUID) river.JobArgs {
		return SourceAnalysisArtifactCleanupJobArgs{OperationID: operationID}
	}, nil)
}
