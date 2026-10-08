package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type operationCleanupDetailReader interface {
	ReadOperationWithCleanupItems(context.Context, uuid.UUID) (*persistence.OperationWithCleanupItems, error)
}

// OperationDetail keeps an operation snapshot and its cleanup outcomes
// together when the persistence repository supports a consistent read.
type OperationDetail struct {
	Snapshot       OperationSnapshot
	CleanupResults []SourceAnalysisArtifactCleanupItemResult
}

// Detail reads the full operation detail. Repositories without the optional
// combined reader retain the existing operation-only behavior.
func (s *Operations) Detail(ctx context.Context, id uuid.UUID) (OperationDetail, error) {
	reader, ok := s.repository.(operationCleanupDetailReader)
	if !ok {
		snapshot, err := s.Snapshot(ctx, id)
		if err != nil {
			return OperationDetail{}, err
		}
		return OperationDetail{Snapshot: snapshot}, nil
	}

	detail, err := reader.ReadOperationWithCleanupItems(ctx, id)
	if err != nil {
		return OperationDetail{}, err
	}
	result := OperationDetail{Snapshot: operationSnapshot(detail.Operation)}
	for _, item := range detail.CleanupItems {
		result.CleanupResults = append(result.CleanupResults, SourceAnalysisArtifactCleanupItemResult{
			ArtifactID: item.ArtifactID,
			State:      item.State,
			SafeError:  item.SafeError,
		})
	}
	return result, nil
}
