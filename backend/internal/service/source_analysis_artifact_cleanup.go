package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
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
	ListSourceAnalysisArtifactCleanupCandidates(context.Context, int) ([]*persistence.SourceAnalysisArtifact, error)
	AdmitSourceAnalysisArtifactCleanupWithArgsFactory(context.Context, []uuid.UUID, persistence.RiverInserter, func(uuid.UUID) river.JobArgs, *river.InsertOpts) (*persistence.Operation, error)
	ListSourceAnalysisArtifactCleanupItems(context.Context, uuid.UUID) ([]persistence.SourceAnalysisArtifactCleanupItem, error)
}

type SourceAnalysisArtifactCleanupSettings interface {
	ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error)
}

type SourceAnalysisArtifactCleanupCandidate struct {
	ArtifactID   uuid.UUID `json:"artifact_id"`
	RelativePath string    `json:"relative_path"`
	State        string    `json:"state"`
}

type SourceAnalysisArtifactCleanupItemResult struct {
	ArtifactID uuid.UUID `json:"artifact_id"`
	State      string    `json:"state"`
	SafeError  *string   `json:"safe_error,omitempty"`
}

type SourceAnalysisArtifactCleanup struct {
	repository SourceAnalysisArtifactCleanupRepository
	settings   SourceAnalysisArtifactCleanupSettings
	river      persistence.RiverInserter
}

func NewSourceAnalysisArtifactCleanup(repository SourceAnalysisArtifactCleanupRepository, runtime SourceAnalysisArtifactCleanupSettings, riverClient persistence.RiverInserter) *SourceAnalysisArtifactCleanup {
	return &SourceAnalysisArtifactCleanup{repository: repository, settings: runtime, river: riverClient}
}

func (s *SourceAnalysisArtifactCleanup) Candidates(ctx context.Context, limit int) ([]SourceAnalysisArtifactCleanupCandidate, error) {
	artifacts, err := s.repository.ListSourceAnalysisArtifactCleanupCandidates(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]SourceAnalysisArtifactCleanupCandidate, 0, len(artifacts))
	for _, artifact := range artifacts {
		result = append(result, SourceAnalysisArtifactCleanupCandidate{ArtifactID: artifact.ID, RelativePath: artifact.RelativeOutputPath, State: artifact.State})
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

func (s *SourceAnalysisArtifactCleanup) Results(ctx context.Context, operationID uuid.UUID) ([]SourceAnalysisArtifactCleanupItemResult, error) {
	items, err := s.repository.ListSourceAnalysisArtifactCleanupItems(ctx, operationID)
	if err != nil {
		return nil, err
	}
	result := make([]SourceAnalysisArtifactCleanupItemResult, 0, len(items))
	for _, item := range items {
		result = append(result, SourceAnalysisArtifactCleanupItemResult{ArtifactID: item.ArtifactID, State: item.State, SafeError: item.SafeError})
	}
	return result, nil
}

func (s *SourceAnalysisArtifactCleanup) OutputDirectory(ctx context.Context) (string, error) {
	if s.settings == nil {
		return "", fmt.Errorf("source analysis artifact cleanup runtime settings are unavailable")
	}
	runtime, err := s.settings.ReadRuntimeSettings(ctx)
	if err != nil {
		return "", fmt.Errorf("read output directory: %w", err)
	}
	return runtime.OutputDirectory, nil
}
